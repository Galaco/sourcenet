package datatable

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
	"strings"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/protocol"
)

// ClassInfo associates a server class ID with its network name and the name
// of its top-level send table, as sent in svc_ClassInfo.
type ClassInfo struct {
	ID            int
	Name          string
	DataTableName string
}

// FlatProp is one entry of a server class's flattened property list.
//
// Name is the property name prefixed by the names of the non-collapsible
// DataTable props leading to it, joined with ".". Collapsible tables (base
// classes, normally the "baseclass" prop) do not contribute to the path, so
// CBaseEntity's origin is "m_vecOrigin" while a player's local data is e.g.
// "localdata.m_Local.m_iHideHUD".
type FlatProp struct {
	Prop  *SendProp
	Table *SendTable // table that declares Prop
	Name  string
}

// ServerClass is a networked entity class.
type ServerClass struct {
	ID            int
	Name          string
	DataTableName string
	Table         *SendTable
	// Props is the flattened property list; delta updates index into it.
	Props []FlatProp

	index map[string]int
}

// PropIndex returns the flat index of the named property, or -1.
func (c *ServerClass) PropIndex(name string) int {
	if c.index == nil {
		c.index = make(map[string]int, len(c.Props))
		for i, p := range c.Props {
			if _, dup := c.index[p.Name]; !dup {
				c.index[p.Name] = i
			}
		}
	}
	if i, ok := c.index[name]; ok {
		return i
	}
	return -1
}

// Tables holds the send tables and server classes announced by a server.
type Tables struct {
	tables      []*SendTable
	byName      map[string]int
	classes     []*ServerClass // indexed by class ID
	classOrder  []*ServerClass // arrival order
	classByName map[string]*ServerClass
	classBits   int
	linked      bool
}

// NewTables returns an empty table set.
func NewTables() *Tables {
	return &Tables{byName: map[string]int{}, classByName: map[string]*ServerClass{}}
}

// AddTable adds a send table, replacing any table with the same name in place.
func (t *Tables) AddTable(st *SendTable) {
	t.linked = false
	if i, ok := t.byName[st.Name]; ok {
		t.tables[i] = st
		return
	}
	t.byName[st.Name] = len(t.tables)
	t.tables = append(t.tables, st)
}

// Table returns the named send table or nil. Names match case-insensitively
// as a fallback, like the engine's table lookups.
func (t *Tables) Table(name string) *SendTable {
	if i, ok := t.byName[name]; ok {
		return t.tables[i]
	}
	for _, st := range t.tables {
		if strings.EqualFold(st.Name, name) {
			return st
		}
	}
	return nil
}

// SendTables returns all tables in arrival order.
func (t *Tables) SendTables() []*SendTable { return t.tables }

// SetClasses replaces the server class list.
func (t *Tables) SetClasses(classes []ClassInfo) {
	t.linked = false
	maxID := -1
	for _, c := range classes {
		if c.ID > maxID {
			maxID = c.ID
		}
	}
	t.classes = make([]*ServerClass, maxID+1)
	t.classOrder = make([]*ServerClass, 0, len(classes))
	t.classByName = make(map[string]*ServerClass, len(classes))
	for _, c := range classes {
		sc := &ServerClass{ID: c.ID, Name: c.Name, DataTableName: c.DataTableName}
		if c.ID >= 0 {
			t.classes[c.ID] = sc
		}
		t.classOrder = append(t.classOrder, sc)
		t.classByName[c.Name] = sc
	}
	t.classBits = log2(len(classes)) + 1
}

// Class returns the server class with the given ID or nil.
func (t *Tables) Class(id int) *ServerClass {
	if id < 0 || id >= len(t.classes) {
		return nil
	}
	return t.classes[id]
}

// ClassByName returns the server class with the given network name or nil.
func (t *Tables) ClassByName(name string) *ServerClass { return t.classByName[name] }

// Classes returns the server classes ordered by ID.
func (t *Tables) Classes() []*ServerClass {
	out := make([]*ServerClass, 0, len(t.classes))
	for _, c := range t.classes {
		if c != nil {
			out = append(out, c)
		}
	}
	return out
}

// NumClasses returns the number of server classes.
func (t *Tables) NumClasses() int { return len(t.classOrder) }

// ClassBits returns the number of bits used to encode a class ID
// (Q_log2(numClasses)+1).
func (t *Tables) ClassBits() int { return t.classBits }

// Linked reports whether Link has succeeded since the last modification.
func (t *Tables) Linked() bool { return t.linked }

// Link resolves table references and array element props, then flattens the
// property list of every server class.
func (t *Tables) Link() error {
	for _, st := range t.tables {
		for i, p := range st.Props {
			switch p.Type {
			case DataTable:
				if p.IsExclude() {
					continue
				}
				child := t.Table(p.DataTableName)
				if child == nil {
					return fmt.Errorf("datatable: table %q prop %q references missing table %q", st.Name, p.Name, p.DataTableName)
				}
				p.DataTable = child
			case Array:
				if p.IsExclude() {
					continue
				}
				if i == 0 {
					return fmt.Errorf("datatable: array prop %q is the first prop of table %q", p.Name, st.Name)
				}
				elem := st.Props[i-1]
				elem.Flags |= FlagInsideArray
				p.ArrayElement = elem
			}
		}
	}
	for _, c := range t.classOrder {
		st := t.Table(c.DataTableName)
		if st == nil {
			return fmt.Errorf("datatable: class %q references missing table %q", c.Name, c.DataTableName)
		}
		c.Table = st
		props, err := flatten(st)
		if err != nil {
			return fmt.Errorf("datatable: class %q: %w", c.Name, err)
		}
		c.Props = props
		c.index = nil
	}
	t.linked = true
	return nil
}

type excludeEntry struct{ table, prop string }

type flattener struct {
	excludes []excludeEntry
	props    []FlatProp
}

// flatten mirrors CSendTablePrecalc::SetupFlatPropertyArray: gather
// exclusions, build the hierarchy (child tables before a table's own props,
// collapsible tables inlined) and move SPROP_CHANGES_OFTEN props forward.
func flatten(root *SendTable) ([]FlatProp, error) {
	f := &flattener{}
	f.gatherExcludes(root, 0)
	if err := f.build(root, "", 0); err != nil {
		return nil, err
	}
	if len(f.props) > protocol.MaxDatatableProps {
		return nil, fmt.Errorf("%d flattened props exceeds limit of %d", len(f.props), protocol.MaxDatatableProps)
	}
	sortByPriority(f.props)
	return f.props, nil
}

const maxTableDepth = 64

func (f *flattener) gatherExcludes(t *SendTable, depth int) {
	if depth > maxTableDepth {
		return
	}
	for _, p := range t.Props {
		if p.IsExclude() {
			f.excludes = append(f.excludes, excludeEntry{p.ExcludeDTName, p.Name})
		} else if p.Type == DataTable && p.DataTable != nil {
			f.gatherExcludes(p.DataTable, depth+1)
		}
	}
}

func (f *flattener) isExcluded(table, prop string) bool {
	for _, e := range f.excludes {
		if strings.EqualFold(e.table, table) && strings.EqualFold(e.prop, prop) {
			return true
		}
	}
	return false
}

func (f *flattener) build(t *SendTable, path string, depth int) error {
	var own []FlatProp
	if err := f.iterate(t, path, &own, depth); err != nil {
		return err
	}
	f.props = append(f.props, own...)
	return nil
}

func (f *flattener) iterate(t *SendTable, path string, own *[]FlatProp, depth int) error {
	if depth > maxTableDepth {
		return fmt.Errorf("table %q nests deeper than %d levels", t.Name, maxTableDepth)
	}
	for _, p := range t.Props {
		if p.IsExclude() || p.Flags&FlagInsideArray != 0 || f.isExcluded(t.Name, p.Name) {
			continue
		}
		if p.Type == DataTable {
			if p.DataTable == nil {
				return fmt.Errorf("unresolved table %q", p.DataTableName)
			}
			if p.Flags&FlagCollapsible != 0 {
				if err := f.iterate(p.DataTable, path, own, depth+1); err != nil {
					return err
				}
			} else if err := f.build(p.DataTable, joinPath(path, p.Name), depth+1); err != nil {
				return err
			}
			continue
		}
		*own = append(*own, FlatProp{Prop: p, Table: t, Name: joinPath(path, p.Name)})
	}
	return nil
}

func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// sortByPriority reproduces SendTable_SortByPriority, which swaps (rather
// than shifts) each SPROP_CHANGES_OFTEN prop to the front in turn.
func sortByPriority(props []FlatProp) {
	start := 0
	for {
		i := start
		for ; i < len(props); i++ {
			if props[i].Prop.Flags&FlagChangesOften != 0 {
				props[i], props[start] = props[start], props[i]
				start++
				break
			}
		}
		if i == len(props) {
			return
		}
	}
}

// ReadDataTables reads a demo data table block (dem_datatables): a list of
// send tables followed by the server class list. The result is not linked.
func ReadDataTables(r *bitbuf.Reader) (*Tables, error) {
	t := NewTables()
	for r.ReadBit() {
		needsDecoder := r.ReadBit()
		st, err := ReadSendTable(r)
		if err != nil {
			return nil, err
		}
		st.NeedsDecoder = needsDecoder
		t.AddTable(st)
	}
	n := int(r.ReadInt16())
	if n < 0 || n > protocol.MaxServerClasses {
		return nil, fmt.Errorf("datatable: invalid server class count %d", n)
	}
	classes := make([]ClassInfo, n)
	for i := range classes {
		id := int(r.ReadInt16())
		if id < 0 || id >= n {
			return nil, fmt.Errorf("datatable: invalid class id %d", id)
		}
		classes[i] = ClassInfo{ID: id, Name: r.ReadString(), DataTableName: r.ReadString()}
	}
	if err := r.Err(); err != nil {
		return nil, fmt.Errorf("datatable: reading data tables: %w", err)
	}
	t.SetClasses(classes)
	return t, nil
}

// WriteDataTables writes the tables and classes in the demo data table block
// format, preserving arrival order.
func (t *Tables) WriteDataTables(w *bitbuf.Writer) {
	for _, st := range t.tables {
		w.WriteBit(true)
		w.WriteBit(st.NeedsDecoder)
		st.Write(w)
	}
	w.WriteBit(false)
	w.WriteInt16(int16(len(t.classOrder)))
	for _, c := range t.classOrder {
		w.WriteInt16(int16(c.ID))
		w.WriteString(c.Name)
		w.WriteString(c.DataTableName)
	}
}

// crcFlags returns the flags the server CRCs for p.
// SPROP_ENCODED_AGAINST_TICKCOUNT is never networked, so it is restored for
// the props the shared game code declares with it (CBaseEntity and
// CFuncRotating m_flSimulationTime, DT_AnimTimeMustBeFirst m_flAnimTime):
// unsigned, often-changing int props of those names.
func crcFlags(p *SendProp) uint32 {
	f := p.Flags
	const want = FlagUnsigned | FlagChangesOften
	if p.Type == Int && f&want == want && (p.Name == "m_flSimulationTime" || p.Name == "m_flAnimTime") {
		f |= FlagEncodedAgainstTickCount
	}
	return f
}

// CRC computes the send table CRC a client reports in clc_ClientInfo
// (SendTable_ComputeCRC). It covers the top-level table of every server
// class in class ID order, or the NeedsDecoder tables in arrival order when
// no classes are set.
//
// The engine includes SPROP_ENCODED_AGAINST_TICKCOUNT in the CRC but never
// networks it. CRC restores the flag on the engine's own uses of it (see
// crcFlags); props of mods that use it elsewhere must have
// FlagEncodedAgainstTickCount set by the caller before computing.
func (t *Tables) CRC() uint32 {
	h := crc32.NewIEEE()
	var buf [4]byte
	putInt := func(v uint32) {
		binary.LittleEndian.PutUint32(buf[:], v)
		h.Write(buf[:])
	}
	crcTable := func(st *SendTable) {
		h.Write([]byte(st.Name))
		putInt(uint32(len(st.Props)))
		for _, p := range st.Props {
			putInt(uint32(p.Type))
			h.Write([]byte(p.Name))
			putInt(crcFlags(p))
			switch {
			case p.Type == DataTable:
				name := p.DataTableName
				if p.DataTable != nil {
					name = p.DataTable.Name
				}
				h.Write([]byte(name))
			case p.IsExclude():
				h.Write([]byte(p.ExcludeDTName))
			case p.Type == Array:
				putInt(uint32(p.NumElements))
			default:
				putInt(math.Float32bits(p.LowValue))
				putInt(math.Float32bits(p.HighValue))
				putInt(uint32(p.NumBits))
			}
		}
	}
	if len(t.classOrder) > 0 {
		for _, c := range t.Classes() {
			st := c.Table
			if st == nil {
				st = t.Table(c.DataTableName)
			}
			if st != nil {
				crcTable(st)
			}
		}
	} else {
		for _, st := range t.tables {
			if st.NeedsDecoder {
				crcTable(st)
			}
		}
	}
	return h.Sum32()
}
