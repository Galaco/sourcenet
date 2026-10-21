package client

import (
	"errors"
	"testing"
	"time"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/datatable"
	"github.com/galaco/sourcenet/entity"
	"github.com/galaco/sourcenet/game"
	"github.com/galaco/sourcenet/message"
	"github.com/galaco/sourcenet/protocol"
	"github.com/galaco/sourcenet/stringtable"
)

func entityTestTables(t *testing.T) *datatable.Tables {
	t.Helper()
	tables := datatable.NewTables()
	tables.AddTable(&datatable.SendTable{Name: "DT_World", NeedsDecoder: true, Props: []*datatable.SendProp{
		{Type: datatable.Int, Name: "m_iWorldFlag", NumBits: 8, Flags: datatable.FlagUnsigned},
	}})
	tables.AddTable(&datatable.SendTable{Name: "DT_TFPlayer", NeedsDecoder: true, Props: []*datatable.SendProp{
		{Type: datatable.Int, Name: "m_iHealth", NumBits: 10, Flags: datatable.FlagUnsigned},
		{Type: datatable.Vector, Name: "m_vecOrigin", Flags: datatable.FlagCoord},
	}})
	tables.SetClasses([]datatable.ClassInfo{
		{ID: 0, Name: "CWorld", DataTableName: "DT_World"},
		{ID: 1, Name: "CTFPlayer", DataTableName: "DT_TFPlayer"},
	})
	if err := tables.Link(); err != nil {
		t.Fatal(err)
	}
	return tables
}

// entityServer adds a minimal entity snapshot writer to fakeServer: the
// world (index 0) never changes; player 3's health follows the tick.
type entityServer struct {
	tables *datatable.Tables
	// updateBaseline sets UpdateBaseline on full updates.
	updateBaseline bool
	// badDelta makes delta updates reference a tick the client never saw.
	badDelta bool
}

func (e *entityServer) health(tick int32) int64 { return int64(tick % 1000) }

func (e *entityServer) write(s *fakeServer, full bool) *message.SvcPacketEntities {
	world, player := e.tables.ClassByName("CWorld"), e.tables.ClassByName("CTFPlayer")
	w := bitbuf.NewWriter(0)
	base := -1
	header := func(idx int, enter bool) {
		w.WriteUBitVar(uint32(idx - base - 1))
		w.WriteBit(false)
		w.WriteBit(enter)
		base = idx
	}
	hp := player.PropIndex("m_iHealth")
	vals := make([]datatable.Value, len(player.Props))
	vals[hp].Int = e.health(s.tick)
	vals[player.PropIndex("m_vecOrigin")].Vector = [3]float32{128, -64.5, 32}

	ack := int32(-1)
	if len(s.gotTicks) > 0 {
		ack = s.gotTicks[len(s.gotTicks)-1]
	}
	if full || ack < 0 {
		header(0, true)
		w.WriteBits(uint32(world.ID), e.tables.ClassBits())
		w.WriteBits(1, protocol.NumNetworkedEHandleSerialNumberBits)
		wv := make([]datatable.Value, len(world.Props))
		wv[0].Int = 7
		datatable.EncodeProps(w, world, wv, []int{0})
		header(3, true)
		w.WriteBits(uint32(player.ID), e.tables.ClassBits())
		w.WriteBits(5, protocol.NumNetworkedEHandleSerialNumberBits)
		datatable.EncodeProps(w, player, vals, []int{0, 1})
		return &message.SvcPacketEntities{MaxEntries: 64, UpdatedEntries: 2, UpdateBaseline: e.updateBaseline, Data: message.BitsFromWriter(w)}
	}
	if e.badDelta {
		ack = 1
	}
	header(3, false)
	datatable.EncodeProps(w, player, vals, []int{hp})
	w.WriteBit(false) // no explicit deletions
	return &message.SvcPacketEntities{
		MaxEntries:     64,
		IsDelta:        true,
		DeltaFrom:      ack,
		UpdatedEntries: 1,
		Data:           message.BitsFromWriter(w),
	}
}

// instanceBaselines builds the instancebaseline string table: zeroed props
// for every class, keyed by class id.
func instanceBaselines(t *testing.T, tables *datatable.Tables) *message.SvcCreateStringTable {
	t.Helper()
	tbl := stringtable.NewTable(protocol.TableInstanceBaseline, 1024)
	var idx []int
	for _, c := range tables.Classes() {
		w := bitbuf.NewWriter(0)
		all := make([]int, len(c.Props))
		for i := range all {
			all[i] = i
		}
		datatable.EncodeProps(w, c, make([]datatable.Value, len(c.Props)), all)
		idx = append(idx, tbl.Add(itoa(c.ID), w.Bytes()))
	}
	w := bitbuf.NewWriter(0)
	tbl.WriteUpdate(w, idx)
	return &message.SvcCreateStringTable{TableName: protocol.TableInstanceBaseline, MaxEntries: 1024, NumEntries: len(idx), Data: message.BitsFromWriter(w)}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

func TestEntitiesFromServerSentTables(t *testing.T) {
	tables := entityTestTables(t)
	c, s := newTestClient(t, Config{Game: game.Generic()})
	es := &entityServer{tables: tables}
	s.sendEnts = es.write
	s.maxClasses = uint16(tables.NumClasses())

	// sv_sendtables 1: the server sends every table and the full class list.
	var signon []message.Message
	for _, st := range tables.SendTables() {
		w := bitbuf.NewWriter(0)
		st.Write(w)
		signon = append(signon, &message.SvcSendTable{NeedsDecoder: st.NeedsDecoder, Data: message.BitsFromWriter(w)})
	}
	ci := &message.SvcClassInfo{NumServerClasses: 2}
	for _, cl := range tables.Classes() {
		ci.Classes = append(ci.Classes, message.ClassInfo{ClassID: cl.ID, ClassName: cl.Name, DataTableName: cl.DataTableName})
	}
	s.classInfo = ci
	s.sendTables = signon
	s.extraSignon = []message.Message{instanceBaselines(t, tables)}

	now := run(t, c, s, epoch, 10*time.Second, func() bool { return c.Active() && len(s.gotTicks) > 20 })
	if !c.Active() || c.Entities() == nil {
		for _, ev := range c.PollEvents() {
			t.Logf("%T %+v", ev, ev)
		}
		t.Fatalf("active=%v entities=%v", c.Active(), c.Entities() != nil)
	}
	run(t, c, s, now, time.Second, nil)

	var full, deltas int
	for _, ev := range c.PollEvents() {
		switch e := ev.(type) {
		case EventEntities:
			if e.Full {
				full++
			} else {
				deltas++
			}
		case EventError:
			// Expected: the generic profile has no tables, the server sends them.
			if !errors.Is(e.Err, ErrNoSendTables) {
				t.Fatalf("client error: %v", e.Err)
			}
		}
	}
	if full == 0 || deltas < 10 {
		t.Fatalf("full updates %d, delta updates %d", full, deltas)
	}

	ents := c.Entities()
	player := ents.Entity(3)
	if player == nil || player.Class.Name != "CTFPlayer" || player.Serial != 5 {
		t.Fatalf("player entity %+v", player)
	}
	hp, ok := player.Prop("m_iHealth")
	if !ok || hp.Int != es.health(c.ServerTick()) {
		t.Fatalf("health %+v, want %d at tick %d", hp, es.health(c.ServerTick()), c.ServerTick())
	}
	origin, _ := player.Prop("m_vecOrigin")
	if origin.Vector != [3]float32{128, -64.5, 32} {
		t.Fatalf("origin %v", origin.Vector)
	}
	if w := ents.Entity(0); w == nil || w.Class.Name != "CWorld" {
		t.Fatal("world entity missing or not preserved across deltas")
	}
	_ = entity.Created
}

func TestEntitiesFromProfileTables(t *testing.T) {
	tables := entityTestTables(t)
	profile := game.Generic()
	profile.DataTables = func() (*datatable.Tables, error) { return tables, nil }
	c, s := newTestClient(t, Config{Game: profile})
	es := &entityServer{tables: tables}
	s.sendEnts = es.write
	s.maxClasses = uint16(tables.NumClasses())
	s.extraSignon = []message.Message{instanceBaselines(t, tables)}

	run(t, c, s, epoch, 10*time.Second, func() bool { return c.Active() && len(s.gotTicks) > 10 })
	if c.Entities() == nil || c.Entities().Entity(3) == nil {
		t.Fatal("entities not decoded with client-side tables")
	}
	if s.gotClientInfo.SendTableCRC != tables.CRC() {
		t.Fatalf("client announced CRC %#x, want %#x", s.gotClientInfo.SendTableCRC, tables.CRC())
	}
}

func TestBaselineAckAndBadDelta(t *testing.T) {
	tables := entityTestTables(t)
	profile := game.Generic()
	profile.DataTables = func() (*datatable.Tables, error) { return tables, nil }
	c, s := newTestClient(t, Config{Game: profile})
	es := &entityServer{tables: tables, updateBaseline: true}
	s.sendEnts = es.write
	s.maxClasses = uint16(tables.NumClasses())
	s.extraSignon = []message.Message{instanceBaselines(t, tables)}

	now := run(t, c, s, epoch, 10*time.Second, func() bool { return c.Active() && s.gotBaselineAck != nil })
	if s.gotBaselineAck == nil || s.gotBaselineAck.BaselineNr != 0 {
		t.Fatalf("baseline ack %+v", s.gotBaselineAck)
	}

	// A delta against an unknown frame must not be acknowledged; the client
	// asks for a full update by sending delta tick -1.
	es.badDelta = true
	es.updateBaseline = false
	s.gotTicks = nil
	run(t, c, s, now, time.Second, func() bool {
		for _, tk := range s.gotTicks {
			if tk == -1 {
				return true
			}
		}
		return false
	})
	found := false
	for _, tk := range s.gotTicks {
		found = found || tk == -1
	}
	if !found {
		t.Fatalf("client did not request a full update after a bad delta: ticks %v", s.gotTicks)
	}
}
