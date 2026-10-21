package entity

import (
	"errors"
	"sort"
	"testing"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/datatable"
	"github.com/galaco/sourcenet/protocol"
)

func intProp(name string, bits int, flags uint32) *datatable.SendProp {
	return &datatable.SendProp{Type: datatable.Int, Name: name, NumBits: bits, Flags: flags}
}

func testTables(t *testing.T) *datatable.Tables {
	t.Helper()
	tables := datatable.NewTables()
	tables.AddTable(&datatable.SendTable{Name: "DT_Player", NeedsDecoder: true, Props: []*datatable.SendProp{
		{Type: datatable.DataTable, Name: "baseclass", DataTableName: "DT_Base", Flags: datatable.FlagCollapsible},
		intProp("m_iHealth", 10, 0),
		{Type: datatable.String, Name: "m_szName"},
		intProp("m_ammoElem", 6, datatable.FlagUnsigned),
		{Type: datatable.Array, Name: "m_iAmmo", NumElements: 4},
		{Type: datatable.Float, Name: "m_flSpeed", NumBits: 10, LowValue: 0, HighValue: 400},
	}})
	tables.AddTable(&datatable.SendTable{Name: "DT_Base", Props: []*datatable.SendProp{
		{Type: datatable.Vector, Name: "m_vecOrigin", Flags: datatable.FlagCoord | datatable.FlagChangesOften},
		intProp("m_iTeamNum", 4, datatable.FlagUnsigned),
	}})
	tables.AddTable(&datatable.SendTable{Name: "DT_Prop", NeedsDecoder: true, Props: []*datatable.SendProp{
		{Type: datatable.DataTable, Name: "baseclass", DataTableName: "DT_Base", Flags: datatable.FlagCollapsible},
		intProp("m_nModelIndex", 13, datatable.FlagUnsigned),
	}})
	tables.AddTable(&datatable.SendTable{Name: "DT_TEExplosion", NeedsDecoder: true, Props: []*datatable.SendProp{
		{Type: datatable.Vector, Name: "m_vecOrigin", Flags: datatable.FlagCoord},
		intProp("m_nMagnitude", 8, datatable.FlagUnsigned),
	}})
	tables.SetClasses([]datatable.ClassInfo{
		{ID: 0, Name: "CPlayer", DataTableName: "DT_Player"},
		{ID: 1, Name: "CProp", DataTableName: "DT_Prop"},
		{ID: 2, Name: "CTEExplosion", DataTableName: "DT_TEExplosion"},
	})
	if err := tables.Link(); err != nil {
		t.Fatal(err)
	}
	return tables
}

// canonical round-trips values through the codec so quantised props compare
// exactly.
func canonical(c *datatable.ServerClass, vals []datatable.Value) []datatable.Value {
	all := make([]int, len(c.Props))
	for i := range all {
		all[i] = i
	}
	w := bitbuf.NewWriter(0)
	datatable.EncodeProps(w, c, vals, all)
	out := make([]datatable.Value, len(c.Props))
	if _, err := datatable.DecodeProps(w.Reader(), c, out, nil); err != nil {
		panic(err)
	}
	return out
}

func valuesEqual(a, b datatable.Value) bool {
	if a.Int != b.Int || a.Float != b.Float || a.Vector != b.Vector || a.String != b.String || len(a.Array) != len(b.Array) {
		return false
	}
	for i := range a.Array {
		if !valuesEqual(a.Array[i], b.Array[i]) {
			return false
		}
	}
	return true
}

// testServer mirrors the server side of packet entities (sv_ents_write.cpp)
// closely enough to drive State through every update type.
type testServer struct {
	t         *testing.T
	tables    *datatable.Tables
	instance  map[int][]datatable.Value
	baselines [2]map[int]*svEntity
}

type svEntity struct {
	class  *datatable.ServerClass
	serial int
	props  []datatable.Value
}

type snapshot struct {
	tick     int32
	alive    map[int]*svEntity // every existing entity
	transmit []int             // entities sent to the client, ascending
}

func newTestServer(t *testing.T, tables *datatable.Tables) *testServer {
	s := &testServer{t: t, tables: tables, instance: map[int][]datatable.Value{}}
	s.baselines[0], s.baselines[1] = map[int]*svEntity{}, map[int]*svEntity{}
	for _, c := range tables.Classes() {
		vals := make([]datatable.Value, len(c.Props))
		for i, p := range c.Props {
			switch p.Prop.Type {
			case datatable.Int:
				vals[i].Int = int64(i + 1)
			case datatable.String:
				vals[i].String = "baseline"
			case datatable.Float:
				vals[i].Float = 200
			}
		}
		s.instance[c.ID] = canonical(c, vals)
	}
	return s
}

func (s *testServer) baselineSource() BaselineSource {
	return func(classID int) ([]byte, bool) {
		vals, ok := s.instance[classID]
		if !ok {
			return nil, false
		}
		c := s.tables.Class(classID)
		all := make([]int, len(c.Props))
		for i := range all {
			all[i] = i
		}
		w := bitbuf.NewWriter(0)
		datatable.EncodeProps(w, c, vals, all)
		return w.Bytes(), true
	}
}

func diff(c *datatable.ServerClass, from, to []datatable.Value) []int {
	var out []int
	for i := range c.Props {
		if !valuesEqual(from[i], to[i]) {
			out = append(out, i)
		}
	}
	return out
}

func (s *testServer) writeHeader(w *bitbuf.Writer, base *int, idx, flags int) {
	w.WriteUBitVar(uint32(idx - *base - 1))
	if flags&hdrLeavePVS != 0 {
		w.WriteBit(true)
		w.WriteBit(flags&hdrDelete != 0)
	} else {
		w.WriteBit(false)
		w.WriteBit(flags&hdrEnterPVS != 0)
	}
	*base = idx
}

func (s *testServer) write(from, to *snapshot, slot int, updateBaseline bool) PacketEntities {
	w := bitbuf.NewWriter(0)
	if updateBaseline {
		other := map[int]*svEntity{}
		for k, v := range s.baselines[slot] {
			other[k] = v
		}
		s.baselines[1-slot] = other
	}
	headerBase, headers := -1, 0
	var oldList []int
	if from != nil {
		oldList = from.transmit
	}
	oi, ni := 0, 0
	next := func(list []int, i int) int {
		if i < len(list) {
			return list[i]
		}
		return sentinel
	}
	for next(oldList, oi) != sentinel || next(to.transmit, ni) != sentinel {
		oldE, newE := next(oldList, oi), next(to.transmit, ni)
		switch {
		case newE < oldE || (newE == oldE && from.alive[oldE].serial != to.alive[newE].serial):
			e := to.alive[newE]
			s.writeHeader(w, &headerBase, newE, hdrEnterPVS)
			headers++
			w.WriteBits(uint32(e.class.ID), s.tables.ClassBits())
			w.WriteBits(uint32(e.serial), protocol.NumNetworkedEHandleSerialNumberBits)
			base := s.instance[e.class.ID]
			if bl := s.baselines[slot][newE]; from != nil && bl != nil && bl.class == e.class {
				base = bl.props
			}
			datatable.EncodeProps(w, e.class, e.props, diff(e.class, base, e.props))
			if updateBaseline {
				s.baselines[1-slot][newE] = e
			}
			if newE == oldE {
				oi++
			}
			ni++
		case newE > oldE:
			flags := hdrLeavePVS
			if _, ok := to.alive[oldE]; !ok {
				flags |= hdrDelete
			}
			s.writeHeader(w, &headerBase, oldE, flags)
			headers++
			oi++
		default:
			e := to.alive[newE]
			if changed := diff(e.class, from.alive[oldE].props, e.props); len(changed) > 0 {
				s.writeHeader(w, &headerBase, newE, 0)
				headers++
				datatable.EncodeProps(w, e.class, e.props, changed)
			}
			oi++
			ni++
		}
	}
	if from != nil {
		inTo := map[int]bool{}
		for _, i := range to.transmit {
			inTo[i] = true
		}
		var idxs []int
		for i := range from.alive {
			idxs = append(idxs, i)
		}
		sort.Ints(idxs)
		for _, i := range idxs {
			_, alive := to.alive[i]
			leftWithDelete := false
			for _, o := range oldList {
				if o == i && !inTo[i] && !alive {
					leftWithDelete = true
				}
			}
			if !inTo[i] && !alive && !leftWithDelete {
				w.WriteBit(true)
				w.WriteBits(uint32(i), protocol.MaxEdictBits)
			}
		}
		w.WriteBit(false)
	}
	msg := PacketEntities{
		MaxEntries:     protocol.MaxEdicts,
		IsDelta:        from != nil,
		DeltaFrom:      -1,
		Baseline:       slot,
		UpdatedEntries: headers,
		UpdateBaseline: updateBaseline,
		Data:           w.Reader(),
	}
	if from != nil {
		msg.DeltaFrom = from.tick
	}
	return msg
}

func (s *testServer) entity(class string, serial int, mutate func(c *datatable.ServerClass, v []datatable.Value)) *svEntity {
	c := s.tables.ClassByName(class)
	vals := make([]datatable.Value, len(c.Props))
	for i, v := range s.instance[c.ID] {
		vals[i] = v.Clone()
	}
	if mutate != nil {
		mutate(c, vals)
	}
	return &svEntity{class: c, serial: serial, props: canonical(c, vals)}
}

func setProp(name string, v datatable.Value) func(c *datatable.ServerClass, vals []datatable.Value) {
	return func(c *datatable.ServerClass, vals []datatable.Value) {
		vals[c.PropIndex(name)] = v
	}
}

func snap(tick int32, alive map[int]*svEntity, transmit ...int) *snapshot {
	return &snapshot{tick: tick, alive: alive, transmit: transmit}
}

func kinds(changes []Change) map[int][]UpdateKind {
	out := map[int][]UpdateKind{}
	for _, c := range changes {
		out[c.Index] = append(out[c.Index], c.Kind)
	}
	return out
}

func expectKinds(t *testing.T, changes []Change, want map[int][]UpdateKind) {
	t.Helper()
	got := kinds(changes)
	if len(got) != len(want) {
		t.Fatalf("changes %v want %v", got, want)
	}
	for i, k := range want {
		g := got[i]
		if len(g) != len(k) {
			t.Fatalf("entity %d: kinds %v want %v (all %v)", i, g, k, got)
		}
		for j := range k {
			if g[j] != k[j] {
				t.Fatalf("entity %d: kinds %v want %v", i, g, k)
			}
		}
	}
}

func expectEntity(t *testing.T, st *State, idx int, want *svEntity, inPVS bool) {
	t.Helper()
	e := st.Entity(idx)
	if e == nil {
		t.Fatalf("entity %d missing", idx)
	}
	if e.Class != want.class || e.Serial != want.serial || e.InPVS != inPVS {
		t.Fatalf("entity %d: class %s serial %d inPVS %v", idx, e.Class.Name, e.Serial, e.InPVS)
	}
	for i := range want.props {
		if !valuesEqual(e.Props[i], want.props[i]) {
			t.Fatalf("entity %d prop %s = %+v want %+v", idx, e.Class.Props[i].Name, e.Props[i], want.props[i])
		}
	}
}

func TestPacketEntitiesSequence(t *testing.T) {
	tables := testTables(t)
	sv := newTestServer(t, tables)
	st := NewState(tables, sv.baselineSource())

	p1 := sv.entity("CPlayer", 5, setProp("m_iHealth", datatable.Value{Int: 125}))
	prop3 := sv.entity("CProp", 1, nil)
	p7 := sv.entity("CPlayer", 2, setProp("m_szName", datatable.Value{String: "seven"}))
	prop9 := sv.entity("CProp", 3, setProp("m_nModelIndex", datatable.Value{Int: 77}))

	// Tick 10: full update.
	s10 := snap(10, map[int]*svEntity{1: p1, 3: prop3, 7: p7, 9: prop9}, 1, 3, 7, 9)
	changes, err := st.Process(10, sv.write(nil, s10, 0, false))
	if err != nil {
		t.Fatal(err)
	}
	expectKinds(t, changes, map[int][]UpdateKind{1: {Created}, 3: {Created}, 7: {Created}, 9: {Created}})
	expectEntity(t, st, 1, p1, true)
	expectEntity(t, st, 9, prop9, true)

	// Tick 11: 1 changes, 3 unchanged, 5 enters, 7 and 9 leave the PVS.
	p1b := sv.entity("CPlayer", 5, func(c *datatable.ServerClass, v []datatable.Value) {
		v[c.PropIndex("m_iHealth")] = datatable.Value{Int: -40}
		v[c.PropIndex("m_vecOrigin")] = datatable.Value{Vector: [3]float32{10, -20.5, 300}}
		v[c.PropIndex("m_iAmmo")] = datatable.Value{Array: []datatable.Value{{Int: 1}, {Int: 63}}}
	})
	p5 := sv.entity("CPlayer", 9, setProp("m_flSpeed", datatable.Value{Float: 320}))
	s11 := snap(11, map[int]*svEntity{1: p1b, 3: prop3, 5: p5, 7: p7, 9: prop9}, 1, 3, 5)
	changes, err = st.Process(11, sv.write(s10, s11, 0, false))
	if err != nil {
		t.Fatal(err)
	}
	expectKinds(t, changes, map[int][]UpdateKind{1: {Updated}, 5: {Created}, 7: {LeftPVS}, 9: {LeftPVS}})
	for _, c := range changes {
		if c.Index == 1 && len(c.ChangedProps) != 3 {
			t.Fatalf("entity 1 changed props %v", c.ChangedProps)
		}
	}
	expectEntity(t, st, 1, p1b, true)
	expectEntity(t, st, 3, prop3, true)
	expectEntity(t, st, 5, p5, true)
	expectEntity(t, st, 7, p7, false)

	// Tick 12: 1 deleted while leaving, 5 recreated with a new serial,
	// 7 re-enters with new data, 9 stays dormant.
	p5b := sv.entity("CPlayer", 10, nil)
	p7b := sv.entity("CPlayer", 2, setProp("m_iTeamNum", datatable.Value{Int: 3}))
	s12 := snap(12, map[int]*svEntity{3: prop3, 5: p5b, 7: p7b, 9: prop9}, 3, 5, 7)
	changes, err = st.Process(12, sv.write(s11, s12, 0, false))
	if err != nil {
		t.Fatal(err)
	}
	expectKinds(t, changes, map[int][]UpdateKind{1: {Deleted}, 5: {Deleted, Created}, 7: {Updated, EnteredPVS}})
	if st.Entity(1) != nil {
		t.Fatal("entity 1 should be deleted")
	}
	expectEntity(t, st, 5, p5b, true)
	expectEntity(t, st, 7, p7b, true)
	expectEntity(t, st, 9, prop9, false)

	// Tick 13: dormant entity 9 is destroyed via the explicit deletion list.
	s13 := snap(13, map[int]*svEntity{3: prop3, 5: p5b, 7: p7b}, 3, 5, 7)
	changes, err = st.Process(13, sv.write(s12, s13, 0, false))
	if err != nil {
		t.Fatal(err)
	}
	expectKinds(t, changes, map[int][]UpdateKind{9: {Deleted}})
	if len(st.Entities()) != 3 {
		t.Fatalf("%d entities remain", len(st.Entities()))
	}

	// Frame 11 was discarded once a delta from 12 arrived.
	if st.HasFrame(11) || !st.HasFrame(12) || !st.HasFrame(13) {
		t.Fatal("frame bookkeeping")
	}
	before := len(st.Entities())
	if _, err := st.Process(14, sv.write(s11, s13, 0, false)); !errors.Is(err, ErrDeltaFrameNotFound) {
		t.Fatalf("expected ErrDeltaFrameNotFound, got %v", err)
	}
	if len(st.Entities()) != before || st.HasFrame(14) {
		t.Fatal("state modified by rejected delta")
	}
	if _, err := st.Process(13, PacketEntities{IsDelta: true, DeltaFrom: 13, Data: bitbuf.NewReader(nil)}); err == nil {
		t.Fatal("expected self-reference error")
	}

	// A new full update replaces everything.
	s20 := snap(20, map[int]*svEntity{4: prop3}, 4)
	changes, err = st.Process(20, sv.write(nil, s20, 0, false))
	if err != nil {
		t.Fatal(err)
	}
	expectKinds(t, changes, map[int][]UpdateKind{3: {Deleted}, 5: {Deleted}, 7: {Deleted}, 4: {Created}})
	if st.HasFrame(13) {
		t.Fatal("full update must clear frames")
	}
}

func TestEntityBaselineUpdate(t *testing.T) {
	tables := testTables(t)
	sv := newTestServer(t, tables)
	st := NewState(tables, sv.baselineSource())

	// Full update that also establishes entity baselines in slot 1.
	p2 := sv.entity("CPlayer", 1, func(c *datatable.ServerClass, v []datatable.Value) {
		v[c.PropIndex("m_iHealth")] = datatable.Value{Int: 300}
		v[c.PropIndex("m_szName")] = datatable.Value{String: "baselined"}
	})
	s1 := snap(1, map[int]*svEntity{2: p2}, 2)
	if _, err := st.Process(1, sv.write(nil, s1, 0, true)); err != nil {
		t.Fatal(err)
	}

	// The entity is recreated with a new serial; the server now uses slot 1
	// and deltas against the entity baseline rather than the instance one.
	p2b := sv.entity("CPlayer", 2, func(c *datatable.ServerClass, v []datatable.Value) {
		v[c.PropIndex("m_iHealth")] = datatable.Value{Int: 300}
		v[c.PropIndex("m_szName")] = datatable.Value{String: "baselined"}
		v[c.PropIndex("m_iTeamNum")] = datatable.Value{Int: 2}
	})
	s2 := snap(2, map[int]*svEntity{2: p2b}, 2)
	msg := sv.write(s1, s2, 1, false)
	data := msg.Data.Clone()
	changes, err := st.Process(2, msg)
	if err != nil {
		t.Fatal(err)
	}
	expectKinds(t, changes, map[int][]UpdateKind{2: {Deleted, Created}})
	expectEntity(t, st, 2, p2b, true)

	// Sanity check: decoding that delta against the instance baseline would
	// have produced different values, so the entity baseline was used.
	alt := NewState(tables, sv.baselineSource())
	alt.frames = []*frame{{tick: 1, last: 2}}
	alt.frames[0].transmit.set(2)
	alt.entities[2] = &Entity{Index: 2, Serial: 1, Class: p2.class, Props: make([]datatable.Value, len(p2.props))}
	msg.Data = data
	if _, err := alt.Process(2, msg); err != nil {
		t.Fatal(err)
	}
	if valuesEqual(alt.Entity(2).Props[p2.class.PropIndex("m_szName")], datatable.Value{String: "baselined"}) {
		t.Fatal("test does not distinguish entity and instance baselines")
	}
}

func TestFullUpdateRejectsLeaveAndPreserve(t *testing.T) {
	tables := testTables(t)
	st := NewState(tables, nil)
	w := bitbuf.NewWriter(0)
	w.WriteUBitVar(0) // entity 0
	w.WriteBit(true)  // leave PVS
	w.WriteBit(false)
	msg := PacketEntities{UpdatedEntries: 1, Data: w.Reader()}
	if _, err := st.Process(5, msg); err == nil {
		t.Fatal("expected error for leave PVS in full update")
	}
}

func TestMissingInstanceBaseline(t *testing.T) {
	tables := testTables(t)
	sv := newTestServer(t, tables)
	st := NewState(tables, func(int) ([]byte, bool) { return nil, false })
	s1 := snap(1, map[int]*svEntity{1: sv.entity("CProp", 1, nil)}, 1)
	if _, err := st.Process(1, sv.write(nil, s1, 0, false)); err == nil {
		t.Fatal("expected missing baseline error")
	}
}

func TestTempEntities(t *testing.T) {
	tables := testTables(t)
	st := NewState(tables, nil)
	explosion := tables.ClassByName("CTEExplosion")
	prop := tables.ClassByName("CProp")
	origin, mag := explosion.PropIndex("m_vecOrigin"), explosion.PropIndex("m_nMagnitude")

	w := bitbuf.NewWriter(0)
	// Event 1: full, delayed 0.25s.
	ev1 := make([]datatable.Value, len(explosion.Props))
	ev1[origin] = datatable.Value{Vector: [3]float32{1, 2, 3}}
	ev1[mag] = datatable.Value{Int: 100}
	w.WriteBit(true)
	w.WriteSBits(25, 8)
	w.WriteBit(true)
	w.WriteBits(uint32(explosion.ID+1), tables.ClassBits())
	datatable.EncodeProps(w, explosion, ev1, []int{origin, mag})
	// Event 2: delta against event 1, only magnitude changes.
	ev2 := make([]datatable.Value, len(explosion.Props))
	copy(ev2, ev1)
	ev2[mag] = datatable.Value{Int: 7}
	w.WriteBit(false)
	w.WriteBit(false)
	datatable.EncodeProps(w, explosion, ev2, []int{mag})
	// Event 3: different class.
	ev3 := make([]datatable.Value, len(prop.Props))
	ev3[prop.PropIndex("m_nModelIndex")] = datatable.Value{Int: 12}
	w.WriteBit(false)
	w.WriteBit(true)
	w.WriteBits(uint32(prop.ID+1), tables.ClassBits())
	datatable.EncodeProps(w, prop, ev3, []int{prop.PropIndex("m_nModelIndex")})

	evs, err := st.DecodeTempEntities(3, w.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 3 || evs[0].Class != explosion || evs[1].Class != explosion || evs[2].Class != prop {
		t.Fatalf("classes %+v", evs)
	}
	if evs[0].FireDelay != 0.25 || evs[1].FireDelay != 0 || evs[0].Reliable {
		t.Fatalf("delays %v %v", evs[0].FireDelay, evs[1].FireDelay)
	}
	if evs[1].Props[origin].Vector != [3]float32{1, 2, 3} || evs[1].Props[mag].Int != 7 || evs[0].Props[mag].Int != 100 {
		t.Fatalf("delta event props %+v", evs[1].Props)
	}
	if evs[2].Props[prop.PropIndex("m_nModelIndex")].Int != 12 {
		t.Fatal("third event props")
	}

	// A zero entry count carries a single reliable event.
	w = bitbuf.NewWriter(0)
	w.WriteBit(false)
	w.WriteBit(true)
	w.WriteBits(uint32(explosion.ID+1), tables.ClassBits())
	datatable.EncodeProps(w, explosion, ev1, []int{mag})
	evs, err = st.DecodeTempEntities(0, w.Reader())
	if err != nil || len(evs) != 1 || !evs[0].Reliable {
		t.Fatalf("reliable event: %v %+v", err, evs)
	}

	// A leading delta is invalid.
	w = bitbuf.NewWriter(0)
	w.WriteBit(false)
	w.WriteBit(false)
	if _, err := st.DecodeTempEntities(1, w.Reader()); err == nil {
		t.Fatal("expected error for leading delta")
	}
}

func TestBitsetNext(t *testing.T) {
	var b bitset
	for _, i := range []int{0, 63, 64, 1000, protocol.MaxEdicts - 1} {
		b.set(i)
	}
	var got []int
	for i := b.next(0); i >= 0; i = b.next(i + 1) {
		got = append(got, i)
	}
	want := []int{0, 63, 64, 1000, protocol.MaxEdicts - 1}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v", got)
		}
	}
}

// TestDormancyFollowsTransmitSet covers transitions hidden by deltas from an
// older acknowledged frame (CL_MarkEntitiesOutOfPVS).
func TestDormancyFollowsTransmitSet(t *testing.T) {
	tables := testTables(t)
	sv := newTestServer(t, tables)
	st := NewState(tables, sv.baselineSource())

	p1 := sv.entity("CPlayer", 5, nil)
	prop4 := sv.entity("CProp", 1, nil)
	alive := map[int]*svEntity{1: p1, 4: prop4}

	s10 := snap(10, alive, 1)
	if _, err := st.Process(10, sv.write(nil, s10, 0, false)); err != nil {
		t.Fatal(err)
	}
	// Tick 11: entity 4 enters.
	s11 := snap(11, alive, 1, 4)
	if _, err := st.Process(11, sv.write(s10, s11, 0, false)); err != nil {
		t.Fatal(err)
	}
	expectEntity(t, st, 4, prop4, true)
	// Tick 12, delta from 10: 4 is in neither frame, so no header mentions
	// it, yet it left the PVS.
	s12 := snap(12, alive, 1)
	changes, err := st.Process(12, sv.write(s10, s12, 0, false))
	if err != nil {
		t.Fatal(err)
	}
	expectKinds(t, changes, map[int][]UpdateKind{4: {LeftPVS}})
	expectEntity(t, st, 4, prop4, false)

	// Tick 13: entity 1 leaves. Tick 14, delta from 12: 1 is preserved
	// unchanged, which means it is back in the PVS.
	s13 := snap(13, alive)
	if _, err := st.Process(13, sv.write(s12, s13, 0, false)); err != nil {
		t.Fatal(err)
	}
	expectEntity(t, st, 1, p1, false)
	s14 := snap(14, alive, 1)
	changes, err = st.Process(14, sv.write(s12, s14, 0, false))
	if err != nil {
		t.Fatal(err)
	}
	expectKinds(t, changes, map[int][]UpdateKind{1: {EnteredPVS}})
	expectEntity(t, st, 1, p1, true)
}
