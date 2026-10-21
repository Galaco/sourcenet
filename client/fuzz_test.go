package client

import (
	"testing"
	"time"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/datatable"
	"github.com/galaco/sourcenet/game"
	"github.com/galaco/sourcenet/message"
)

// FuzzServerMessages feeds arbitrary message streams to a fully connected
// client with entity decoding enabled. Malformed input must never panic.
func FuzzServerMessages(f *testing.F) {
	ctx := message.DefaultContext()
	seed := func(msgs ...message.Message) []byte {
		w := bitbuf.NewWriter(0)
		for _, m := range msgs {
			_ = message.Write(w, m, ctx)
		}
		return w.Bytes()
	}
	f.Add(seed(&message.NetTick{Tick: 5}, &message.SvcPrint{Text: "x"}))
	f.Add(seed(&message.SvcPacketEntities{MaxEntries: 10, UpdatedEntries: 3, Data: message.Bits{Data: []byte{0xff, 0x01, 0x22}, N: 20}}))
	f.Add(seed(&message.SvcUpdateStringTable{TableID: 0, ChangedEntries: 2, Data: message.Bits{Data: []byte{1, 2, 3}, N: 24}}))
	f.Add(seed(&message.SvcGameEventList{NumEvents: 2, Data: message.Bits{Data: []byte{9, 9, 9, 9}, N: 32}}))
	f.Add(seed(&message.SvcTempEntities{NumEntries: 1, Data: message.Bits{Data: []byte{0xaa, 0x55}, N: 16}}))

	f.Fuzz(func(t *testing.T, data []byte) {
		tables := entityTestTables(t)
		profile := game.TF2()
		profile.DataTables = func() (*datatable.Tables, error) { return tables, nil }
		c, s := newTestClient(t, Config{Game: profile})
		es := &entityServer{tables: tables}
		s.sendEnts = es.write
		s.maxClasses = uint16(tables.NumClasses())
		s.extraSignon = []message.Message{instanceBaselines(t, tables)}
		now := run(t, c, s, epoch, 5*time.Second, func() bool { return c.Active() && len(s.gotTicks) > 2 })
		if !c.Active() {
			t.Fatal("setup did not reach full signon")
		}
		c.now = now
		c.processMessages(bitbuf.NewReader(data))
	})
}
