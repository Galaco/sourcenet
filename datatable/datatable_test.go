package datatable

import (
	"encoding/binary"
	"hash/crc32"
	"math"
	"reflect"
	"testing"

	"github.com/galaco/sourcenet/bitbuf"
)

func intProp(name string, bits int, flags uint32) *SendProp {
	return &SendProp{Type: Int, Name: name, NumBits: bits, Flags: flags}
}

func dtProp(name, table string, flags uint32) *SendProp {
	return &SendProp{Type: DataTable, Name: name, DataTableName: table, Flags: flags}
}

func excludeProp(table, prop string) *SendProp {
	return &SendProp{Type: Int, Name: prop, ExcludeDTName: table, Flags: FlagExclude}
}

// testTables builds a hierarchy exercising collapsible and non-collapsible
// sub tables, exclusions, arrays and CHANGES_OFTEN reordering.
func testTables() *Tables {
	t := NewTables()
	t.AddTable(&SendTable{Name: "DT_Leaf", NeedsDecoder: true, Props: []*SendProp{
		dtProp("baseclass", "DT_Middle", FlagCollapsible),
		{Type: Vector, Name: "m_d", Flags: FlagCoord},
		{Type: String, Name: "m_e", Flags: FlagChangesOften},
		excludeProp("DT_Sub", "m_s2"),
	}})
	t.AddTable(&SendTable{Name: "DT_Middle", Props: []*SendProp{
		dtProp("baseclass", "DT_Base", FlagCollapsible),
		{Type: Float, Name: "m_c", NumBits: 10, LowValue: 0, HighValue: 100},
		intProp("m_arrElem", 4, FlagUnsigned),
		{Type: Array, Name: "m_arr", NumElements: 5},
		excludeProp("DT_Base", "m_a"),
		dtProp("local", "DT_Local", 0),
	}})
	t.AddTable(&SendTable{Name: "DT_Base", Props: []*SendProp{
		intProp("m_a", 8, 0),
		intProp("m_b", 8, FlagUnsigned|FlagChangesOften),
		dtProp("sub", "DT_Sub", 0),
	}})
	t.AddTable(&SendTable{Name: "DT_Sub", Props: []*SendProp{
		intProp("m_s1", 3, FlagUnsigned),
		intProp("m_s2", 3, FlagUnsigned),
	}})
	t.AddTable(&SendTable{Name: "DT_Local", Props: []*SendProp{
		intProp("m_l1", 16, 0),
		intProp("m_l2", 16, FlagChangesOften),
	}})
	t.AddTable(&SendTable{Name: "DT_Other", NeedsDecoder: true, Props: []*SendProp{
		intProp("m_x", 32, 0),
	}})
	t.SetClasses([]ClassInfo{
		{ID: 0, Name: "CLeaf", DataTableName: "DT_Leaf"},
		{ID: 1, Name: "COther", DataTableName: "DT_Other"},
	})
	return t
}

func flatNames(c *ServerClass) []string {
	var out []string
	for _, p := range c.Props {
		out = append(out, p.Name)
	}
	return out
}

func TestFlattenOrder(t *testing.T) {
	tables := testTables()
	if err := tables.Link(); err != nil {
		t.Fatal(err)
	}
	c := tables.ClassByName("CLeaf")
	// Hierarchy order before sorting:
	//   sub.m_s1, local.m_l1, local.m_l2, m_b, m_c, m_arr, m_d, m_e
	// SendTable_SortByPriority then swaps each CHANGES_OFTEN prop to the front.
	want := []string{"local.m_l2", "m_b", "m_e", "local.m_l1", "m_c", "m_arr", "m_d", "sub.m_s1"}
	if got := flatNames(c); !reflect.DeepEqual(got, want) {
		t.Fatalf("flattened order\n got %v\nwant %v", got, want)
	}
	if c.Props[0].Table.Name != "DT_Local" || c.Props[1].Table.Name != "DT_Base" {
		t.Fatal("owning tables not recorded")
	}
	arr := c.Props[c.PropIndex("m_arr")].Prop
	if arr.ArrayElement == nil || arr.ArrayElement.Name != "m_arrElem" || arr.ArrayElement.Flags&FlagInsideArray == 0 {
		t.Fatal("array element not resolved")
	}
	if c.PropIndex("m_a") != -1 || c.PropIndex("sub.m_s2") != -1 {
		t.Fatal("excluded props must not be flattened")
	}
	if tables.ClassBits() != 2 {
		t.Fatalf("class bits %d", tables.ClassBits())
	}
}

func TestLinkMissingTable(t *testing.T) {
	tables := NewTables()
	tables.AddTable(&SendTable{Name: "DT_A", Props: []*SendProp{dtProp("x", "DT_Missing", 0)}})
	tables.SetClasses([]ClassInfo{{ID: 0, Name: "CA", DataTableName: "DT_A"}})
	if err := tables.Link(); err == nil {
		t.Fatal("expected error")
	}
}

func stripResolved(st *SendTable) *SendTable {
	out := &SendTable{Name: st.Name, NeedsDecoder: st.NeedsDecoder}
	for _, p := range st.Props {
		c := *p
		c.DataTable, c.ArrayElement = nil, nil
		out.Props = append(out.Props, &c)
	}
	return out
}

func TestSendTableRoundTrip(t *testing.T) {
	st := &SendTable{Name: "DT_Test", Props: []*SendProp{
		intProp("m_i", 7, FlagUnsigned),
		{Type: Float, Name: "m_f", Flags: FlagRoundDown, LowValue: -2.5, HighValue: 1024, NumBits: 12},
		dtProp("m_dt", "DT_Child", FlagCollapsible|FlagProxyAlwaysYes),
		excludeProp("DT_Other", "m_gone"),
		{Type: Array, Name: "m_arr", NumElements: 1023},
		{Type: String, Name: "m_s"},
	}}
	w := bitbuf.NewWriter(0)
	w.WriteBit(true) // misalign
	st.Write(w)
	r := w.Reader()
	r.ReadBit()
	got, err := ReadSendTable(r)
	if err != nil {
		t.Fatal(err)
	}
	if r.BitsLeft() != 0 {
		t.Fatalf("%d bits left", r.BitsLeft())
	}
	if !reflect.DeepEqual(stripResolved(got), stripResolved(st)) {
		t.Fatalf("round trip mismatch\n got %+v\nwant %+v", got, st)
	}
}

func TestReadSendTableRejectsWideProps(t *testing.T) {
	w := bitbuf.NewWriter(0)
	(&SendTable{Name: "DT_Bad", Props: []*SendProp{intProp("m_wide", 40, 0)}}).Write(w)
	if _, err := ReadSendTable(w.Reader()); err == nil {
		t.Fatal("expected error for a 40 bit int prop")
	}
}

func TestDataTablesRoundTrip(t *testing.T) {
	tables := testTables()
	w := bitbuf.NewWriter(0)
	tables.WriteDataTables(w)
	r := w.Reader()
	got, err := ReadDataTables(r)
	if err != nil {
		t.Fatal(err)
	}
	if r.BitsLeft() != 0 {
		t.Fatalf("%d bits left", r.BitsLeft())
	}
	if len(got.SendTables()) != len(tables.SendTables()) {
		t.Fatalf("table count %d", len(got.SendTables()))
	}
	for i, st := range tables.SendTables() {
		if !reflect.DeepEqual(stripResolved(got.SendTables()[i]), stripResolved(st)) {
			t.Fatalf("table %s mismatch", st.Name)
		}
	}
	if got.NumClasses() != 2 || got.Class(1).Name != "COther" || got.Class(0).DataTableName != "DT_Leaf" {
		t.Fatal("class info mismatch")
	}
	if err := got.Link(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(flatNames(got.Class(0)), flatNames(mustLink(t, testTables()).Class(0))) {
		t.Fatal("re-read tables flatten differently")
	}
	if got.CRC() != testTables().CRC() {
		t.Fatal("CRC changed across serialisation")
	}
}

func mustLink(t *testing.T, tables *Tables) *Tables {
	t.Helper()
	if err := tables.Link(); err != nil {
		t.Fatal(err)
	}
	return tables
}

func TestCRCMatchesManualComputation(t *testing.T) {
	tables := NewTables()
	tables.AddTable(&SendTable{Name: "DT_A", NeedsDecoder: true, Props: []*SendProp{
		intProp("m_i", 5, FlagUnsigned),
		dtProp("baseclass", "DT_B", FlagCollapsible),
		{Type: Array, Name: "m_arr", NumElements: 3},
		excludeProp("DT_B", "m_x"),
	}})
	tables.AddTable(&SendTable{Name: "DT_B", Props: []*SendProp{intProp("m_x", 1, 0)}})
	tables.SetClasses([]ClassInfo{{ID: 0, Name: "CA", DataTableName: "DT_A"}})

	var buf []byte
	le := func(v uint32) { buf = binary.LittleEndian.AppendUint32(buf, v) }
	buf = append(buf, "DT_A"...)
	le(4)
	le(uint32(Int))
	buf = append(buf, "m_i"...)
	le(FlagUnsigned)
	le(math.Float32bits(0))
	le(math.Float32bits(0))
	le(5)
	le(uint32(DataTable))
	buf = append(buf, "baseclass"...)
	le(FlagCollapsible)
	buf = append(buf, "DT_B"...)
	le(uint32(Array))
	buf = append(buf, "m_arr"...)
	le(0)
	le(3)
	le(uint32(Int))
	buf = append(buf, "m_x"...)
	le(FlagExclude)
	buf = append(buf, "DT_B"...)

	if got, want := tables.CRC(), crc32.ChecksumIEEE(buf); got != want {
		t.Fatalf("crc %#x want %#x", got, want)
	}

	before := tables.CRC()
	tables.Table("DT_B").Props[0].NumBits = 2 // non-leaf tables are not covered
	if tables.CRC() != before {
		t.Fatal("CRC must only cover class tables")
	}
	tables.Table("DT_A").Props[0].Flags |= FlagEncodedAgainstTickCount
	if tables.CRC() == before {
		t.Fatal("CRC must change with flags")
	}
}

func TestPropIndexHelpers(t *testing.T) {
	w := bitbuf.NewWriter(0)
	indices := []int{0, 1, 17, 18, 300, 4095}
	last := -1
	for _, i := range indices {
		WritePropIndex(w, last, i)
		last = i
	}
	WritePropListEnd(w)
	r := w.Reader()
	last = -1
	var got []int
	for {
		i, ok := ReadPropIndex(r, last)
		if !ok {
			break
		}
		got = append(got, i)
		last = i
	}
	if !reflect.DeepEqual(got, indices) || r.BitsLeft() != 0 {
		t.Fatalf("got %v, %d bits left", got, r.BitsLeft())
	}
	// Index 0 after -1 uses the compact 7-bit form (1 + 2 + 4).
	w = bitbuf.NewWriter(0)
	WritePropIndex(w, -1, 0)
	if w.BitsWritten() != 7 {
		t.Fatalf("index encoding used %d bits", w.BitsWritten())
	}
}

func TestCRCRestoresTickCountFlag(t *testing.T) {
	build := func(extra uint32) *Tables {
		tables := NewTables()
		tables.AddTable(&SendTable{Name: "DT_BaseEntity", NeedsDecoder: true, Props: []*SendProp{
			{Type: Int, Name: "m_flSimulationTime", Flags: FlagUnsigned | FlagChangesOften | extra, NumBits: 8},
		}})
		tables.SetClasses([]ClassInfo{{ID: 0, Name: "CBaseEntity", DataTableName: "DT_BaseEntity"}})
		if err := tables.Link(); err != nil {
			t.Fatal(err)
		}
		return tables
	}
	// Tables read from a demo lack the un-networked flag but must CRC like
	// the server's, which has it.
	if build(0).CRC() != build(FlagEncodedAgainstTickCount).CRC() {
		t.Fatal("CRC does not restore SPROP_ENCODED_AGAINST_TICKCOUNT")
	}
}
