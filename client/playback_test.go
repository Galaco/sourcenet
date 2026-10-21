package client

import (
	"bytes"
	"testing"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/datatable"
	"github.com/galaco/sourcenet/demo"
	"github.com/galaco/sourcenet/game"
	"github.com/galaco/sourcenet/message"
	"github.com/galaco/sourcenet/protocol"
)

// writeDemo records a short SourceTV-style demo: server info and string
// tables in the signon section, the data tables, then entity packets.
func writeDemo(t *testing.T, tables *datatable.Tables) []byte {
	t.Helper()
	var out bytes.Buffer
	dw, err := demo.NewWriter(&out, demo.Header{
		Protocol:        demo.Protocol,
		NetworkProtocol: protocol.Version,
		ServerName:      "fake",
		MapName:         "ctf_2fort",
		GameDir:         "tf",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := message.DefaultContext()
	frame := func(cmd demo.Command, tick int32, msgs ...message.Message) {
		w := bitbuf.NewWriter(0)
		for _, m := range msgs {
			if err := message.Write(w, m, ctx); err != nil {
				t.Fatal(err)
			}
		}
		if err := dw.WriteFrame(&demo.Frame{Command: cmd, Tick: tick, Data: w.Bytes()}); err != nil {
			t.Fatal(err)
		}
	}

	frame(demo.Signon, 0,
		&message.SvcServerInfo{Protocol: protocol.Version, ServerCount: 1, IsHLTV: true, ClientCRC: -1, MaxClasses: 2,
			MaxClients: 24, TickInterval: 0.015, OS: 'l', GameDir: "tf", MapName: "ctf_2fort", SkyName: "sky", HostName: "fake"},
		&message.NetTick{Tick: 100},
	)
	w := bitbuf.NewWriter(0)
	tables.WriteDataTables(w)
	if err := dw.WriteFrame(&demo.Frame{Command: demo.DataTables, Data: w.Bytes()}); err != nil {
		t.Fatal(err)
	}
	frame(demo.Signon, 0,
		instanceBaselines(t, tables),
		&message.SvcClassInfo{CreateOnClient: true, NumServerClasses: 2},
	)

	es := &entityServer{tables: tables}
	s := &fakeServer{tick: 100}
	frame(demo.Packet, 100, &message.NetTick{Tick: 100}, es.write(s, true))
	for tick := int32(101); tick <= 110; tick++ {
		s.tick = tick
		s.gotTicks = []int32{tick - 1}
		frame(demo.Packet, tick, &message.NetTick{Tick: tick}, es.write(s, false))
	}
	if err := dw.WriteFrame(&demo.Frame{Command: demo.Stop, Tick: 110}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestPlayback(t *testing.T) {
	tables := entityTestTables(t)
	data := writeDemo(t, tables)

	// The demo's own tables are used even though the profile has none.
	p, err := NewPlayback(Config{Game: game.TF2()}, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if p.Header().MapName != "ctf_2fort" {
		t.Fatalf("header %+v", p.Header())
	}
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	c := p.Client()
	var updates int
	for _, ev := range c.PollEvents() {
		switch e := ev.(type) {
		case EventEntities:
			updates++
		case EventError:
			t.Fatalf("playback error: %v", e.Err)
		}
	}
	if updates != 11 {
		t.Fatalf("%d entity updates, want 11", updates)
	}
	if !c.Active() || c.ServerTick() != 110 {
		t.Fatalf("signon %d tick %d", c.SignonState(), c.ServerTick())
	}
	player := c.Entities().Entity(3)
	if player == nil {
		t.Fatal("player entity missing")
	}
	if hp, _ := player.Prop("m_iHealth"); hp.Int != 110 {
		t.Fatalf("health %d", hp.Int)
	}

	// The tables can also be loaded from the demo for live use.
	dt, err := demo.ReadDataTables(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := dt.Link(); err != nil {
		t.Fatal(err)
	}
	if dt.CRC() != tables.CRC() {
		t.Fatalf("demo table CRC %#x, want %#x", dt.CRC(), tables.CRC())
	}
}
