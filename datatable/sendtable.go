// Package datatable implements the Source Engine networked data tables:
// send table descriptions, server classes, property flattening, the
// property value codec and the send table CRC.
//
// A server describes every networked class with a tree of send tables. The
// client flattens each class's tree into a single ordered property list; delta
// updates then refer to properties by their index in that list. The
// flattening order must match the engine exactly, so the algorithm here
// mirrors CSendTablePrecalc::SetupFlatPropertyArray.
package datatable

import (
	"errors"
	"fmt"

	"github.com/galaco/sourcenet/bitbuf"
)

// PropType is the type of a send property (SendPropType).
type PropType int

// Property types in DPT_ order.
const (
	Int PropType = iota
	Float
	Vector
	VectorXY
	String
	Array
	DataTable
	// Int64 is not networked by protocol 24 games; it is accepted for
	// engines built with 64-bit integer support.
	Int64
)

func (t PropType) String() string {
	switch t {
	case Int:
		return "int"
	case Float:
		return "float"
	case Vector:
		return "vector"
	case VectorXY:
		return "vectorxy"
	case String:
		return "string"
	case Array:
		return "array"
	case DataTable:
		return "datatable"
	case Int64:
		return "int64"
	}
	return fmt.Sprintf("proptype(%d)", int(t))
}

// Send property flags (SPROP_*).
const (
	FlagUnsigned            uint32 = 1 << 0
	FlagCoord               uint32 = 1 << 1
	FlagNoScale             uint32 = 1 << 2
	FlagRoundDown           uint32 = 1 << 3
	FlagRoundUp             uint32 = 1 << 4
	FlagNormal              uint32 = 1 << 5
	FlagExclude             uint32 = 1 << 6
	FlagXYZE                uint32 = 1 << 7
	FlagInsideArray         uint32 = 1 << 8
	FlagProxyAlwaysYes      uint32 = 1 << 9
	FlagChangesOften        uint32 = 1 << 10
	FlagIsAVectorElem       uint32 = 1 << 11
	FlagCollapsible         uint32 = 1 << 12
	FlagCoordMP             uint32 = 1 << 13
	FlagCoordMPLowPrecision uint32 = 1 << 14
	FlagCoordMPIntegral     uint32 = 1 << 15
	// FlagVarInt shares its bit with FlagNormal; it applies to integer props.
	FlagVarInt = FlagNormal
	// FlagEncodedAgainstTickCount is a server-side flag that is never
	// networked but is included in the send table CRC.
	FlagEncodedAgainstTickCount uint32 = 1 << 16
)

// Wire field widths of a serialised send table (dt.h PROPINFOBITS_*).
const (
	propInfoBitsNumProps    = 10
	propInfoBitsType        = 5
	propInfoBitsFlags       = 16
	propInfoBitsNumBits     = 7
	propInfoBitsNumElements = 10
)

// SendProp describes one property of a send table.
type SendProp struct {
	Type          PropType
	Name          string
	Flags         uint32
	ExcludeDTName string // table name, for exclude props
	DataTableName string // referenced table, for DataTable props
	NumElements   int    // element count, for Array props
	LowValue      float32
	HighValue     float32
	NumBits       int

	// DataTable is the referenced table, resolved by Tables.Link.
	DataTable *SendTable
	// ArrayElement describes each element of an Array prop; it is the prop
	// immediately preceding the array in its table. Resolved by Tables.Link.
	ArrayElement *SendProp
}

// IsExclude reports whether the prop excludes another prop rather than
// describing data.
func (p *SendProp) IsExclude() bool { return p.Flags&FlagExclude != 0 }

// SendTable is a named list of send properties.
type SendTable struct {
	Name string
	// NeedsDecoder is set for the top-level table of a server class.
	NeedsDecoder bool
	Props        []*SendProp
}

// ReadSendTable reads a serialised send table, the payload of svc_SendTable
// and the per-table body of a demo data table block.
func ReadSendTable(r *bitbuf.Reader) (*SendTable, error) {
	t := &SendTable{Name: r.ReadString()}
	n := int(r.ReadBits(propInfoBitsNumProps))
	t.Props = make([]*SendProp, 0, n)
	for i := 0; i < n; i++ {
		p := &SendProp{
			Type: PropType(r.ReadBits(propInfoBitsType)),
			Name: r.ReadString(),
		}
		p.Flags = r.ReadBits(propInfoBitsFlags)
		switch {
		case p.Type == DataTable:
			p.DataTableName = r.ReadString()
		case p.IsExclude():
			p.ExcludeDTName = r.ReadString()
		case p.Type == Array:
			p.NumElements = int(r.ReadBits(propInfoBitsNumElements))
		default:
			p.LowValue = r.ReadFloat32()
			p.HighValue = r.ReadFloat32()
			p.NumBits = int(r.ReadBits(propInfoBitsNumBits))
		}
		if r.Overflowed() {
			break
		}
		if p.Type > Int64 && !p.IsExclude() {
			return nil, fmt.Errorf("datatable: table %q prop %q has invalid type %d", t.Name, p.Name, p.Type)
		}
		if maxBits := maxPropBits(p.Type); p.NumBits > maxBits {
			return nil, fmt.Errorf("datatable: table %q prop %q uses %d bits (max %d)", t.Name, p.Name, p.NumBits, maxBits)
		}
		t.Props = append(t.Props, p)
	}
	if err := r.Err(); err != nil {
		return nil, fmt.Errorf("datatable: reading send table %q: %w", t.Name, err)
	}
	return t, nil
}

// Write serialises the table in the svc_SendTable payload format.
func (t *SendTable) Write(w *bitbuf.Writer) {
	w.WriteString(t.Name)
	w.WriteBits(uint32(len(t.Props)), propInfoBitsNumProps)
	for _, p := range t.Props {
		w.WriteBits(uint32(p.Type), propInfoBitsType)
		w.WriteString(p.Name)
		w.WriteBits(p.Flags&(1<<propInfoBitsFlags-1), propInfoBitsFlags)
		switch {
		case p.Type == DataTable:
			name := p.DataTableName
			if p.DataTable != nil {
				name = p.DataTable.Name
			}
			w.WriteString(name)
		case p.IsExclude():
			w.WriteString(p.ExcludeDTName)
		case p.Type == Array:
			w.WriteBits(uint32(p.NumElements), propInfoBitsNumElements)
		default:
			w.WriteFloat32(p.LowValue)
			w.WriteFloat32(p.HighValue)
			w.WriteBits(uint32(p.NumBits), propInfoBitsNumBits)
		}
	}
}

// ErrUnlinked is returned when an operation needs tables that have not been
// resolved with Tables.Link.
var ErrUnlinked = errors.New("datatable: tables are not linked")

// maxPropBits bounds NumBits so malformed tables cannot request reads wider
// than the bit reader supports.
func maxPropBits(t PropType) int {
	if t == Int64 {
		return 64
	}
	return 32
}

// log2 is Q_log2: the index of the highest set bit, 0 for values <= 1.
func log2(v int) int {
	n := 0
	for v >>= 1; v > 0; v >>= 1 {
		n++
	}
	return n
}
