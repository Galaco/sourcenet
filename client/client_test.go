package client

import (
	"testing"
	"time"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/game"
	"github.com/galaco/sourcenet/gameevent"
	"github.com/galaco/sourcenet/message"
	"github.com/galaco/sourcenet/protocol"
	"github.com/galaco/sourcenet/usercmd"
	"github.com/galaco/sourcenet/usermessage"
)

var epoch = time.Unix(1700000000, 0)

func newTestClient(t *testing.T, cfg Config) (*Client, *fakeServer) {
	t.Helper()
	cp, sp := NewPipe()
	if cfg.Game == nil {
		cfg.Game = game.TF2()
	}
	if cfg.Name == "" {
		cfg.Name = "tester"
	}
	c := New(cfg)
	c.ConnectTransport(cp)
	return c, newFakeServer(t, sp)
}

func TestFullSignon(t *testing.T) {
	c, s := newTestClient(t, Config{})
	end := run(t, c, s, epoch, 10*time.Second, func() bool { return c.Active() && len(s.gotMoves) > 10 })

	if !c.Active() {
		t.Fatalf("client not active: state %v signon %s", c.State(), protocol.SignonStateName(c.SignonState()))
	}
	if s.gotConnect == nil || s.gotConnect.Name != "tester" || s.gotConnect.ProductVersion != s.version ||
		s.gotConnect.AuthProtocol != protocol.AuthHashedCDKey || len(s.gotConnect.CDKeyHash) != 32 {
		t.Fatalf("connect packet %+v", s.gotConnect)
	}
	if s.gotConVars["name"] != "tester" || s.gotConVars["rate"] == "" {
		t.Fatalf("userinfo convars %v", s.gotConVars)
	}
	if s.gotClientInfo == nil || s.gotClientInfo.ServerCount != s.spawnCount {
		t.Fatalf("client info %+v", s.gotClientInfo)
	}
	if s.gotListen == nil || !s.gotListen.IsSet(3) {
		t.Fatal("listen events not sent for announced event")
	}
	if info := c.ServerInfo(); info == nil || info.MapName != "ctf_2fort" || c.PlayerSlot() != 2 {
		t.Fatalf("server info %+v", info)
	}
	if c.TickInterval() != 15*time.Millisecond {
		t.Fatalf("tick interval %v", c.TickInterval())
	}
	if c.ReplicatedConVars()["sv_cheats"] != "0" {
		t.Fatal("replicated convars")
	}
	players := c.Players()
	if p, ok := players[0]; !ok || p.Name != "bot" || p.UserID != 7 || !p.FakePlayer {
		t.Fatalf("players %+v", players)
	}
	if c.GameEvents() == nil || c.GameEvents().ByName("player_say") == nil {
		t.Fatal("game event list")
	}

	// Commands are numbered by outgoing sequence and increase by one.
	for i := 1; i < len(s.gotMoves); i++ {
		if s.gotMoves[i].CommandNumber != s.gotMoves[i-1].CommandNumber+1 {
			t.Fatalf("command numbers not consecutive: %d then %d", s.gotMoves[i-1].CommandNumber, s.gotMoves[i].CommandNumber)
		}
	}
	// The client acknowledges entity snapshots through net_Tick.
	if last := s.gotTicks[len(s.gotTicks)-1]; last < 1000 {
		t.Fatalf("client delta tick %d", last)
	}

	// Signon states arrive in order.
	var states []int
	for _, ev := range c.PollEvents() {
		if e, ok := ev.(EventSignonState); ok {
			states = append(states, e.State)
		}
	}
	want := []int{protocol.SignonConnected, protocol.SignonNew, protocol.SignonPrespawn, protocol.SignonSpawn, protocol.SignonFull}
	if len(states) != len(want) {
		t.Fatalf("signon states %v", states)
	}
	for i := range want {
		if states[i] != want[i] {
			t.Fatalf("signon states %v", states)
		}
	}

	c.Disconnect("bye")
	s.update(end)
	if s.gotDisconnect != "bye" {
		t.Fatalf("server got disconnect %q", s.gotDisconnect)
	}
	if c.State() != StateDisconnected {
		t.Fatal("client still connected")
	}
}

func TestProductVersionQuery(t *testing.T) {
	g := game.TF2()
	g.ProductVersion = ""
	c, s := newTestClient(t, Config{Game: g})
	if c.State() != StateQuerying {
		t.Fatalf("state %v", c.State())
	}
	run(t, c, s, epoch, 5*time.Second, func() bool { return c.State() == StateConnected })
	if s.gotConnect == nil || s.gotConnect.ProductVersion != s.version {
		t.Fatalf("connect %+v", s.gotConnect)
	}
}

func TestRejected(t *testing.T) {
	c, s := newTestClient(t, Config{})
	s.reject = "#GameUI_ServerRejectServerFull"
	run(t, c, s, epoch, 5*time.Second, func() bool { return c.State() == StateDisconnected })
	var got *EventDisconnected
	for _, ev := range c.PollEvents() {
		if e, ok := ev.(EventDisconnected); ok {
			got = &e
		}
	}
	if got == nil || !got.Rejected || got.Reason != s.reject {
		t.Fatalf("disconnect event %+v", got)
	}
}

func TestConnectRetriesAndGivesUp(t *testing.T) {
	cp, _ := NewPipe() // nobody answers
	c := New(Config{Game: game.TF2(), RetryInterval: time.Second, MaxRetries: 3})
	c.ConnectTransport(cp)
	attempts := 0
	for now := epoch; now.Before(epoch.Add(10 * time.Second)); now = now.Add(100 * time.Millisecond) {
		c.Update(now)
		for _, ev := range c.PollEvents() {
			switch e := ev.(type) {
			case EventConnecting:
				attempts = e.Attempt
			case EventDisconnected:
				if attempts != 3 {
					t.Fatalf("gave up after %d attempts", attempts)
				}
				return
			}
		}
	}
	t.Fatal("client never gave up")
}

func TestSteamAuthWithoutSteamID(t *testing.T) {
	c, s := newTestClient(t, Config{})
	s.authProtocol = protocol.AuthSteam
	run(t, c, s, epoch, 2*time.Second, func() bool { return c.State() == StateDisconnected })
	if c.State() != StateDisconnected || s.gotConnect != nil {
		t.Fatal("offline auth without SteamID must not connect to a Steam server")
	}

	c, s = newTestClient(t, Config{Auth: OfflineAuth{SteamID: 76561197960287930}})
	s.authProtocol = protocol.AuthSteam
	run(t, c, s, epoch, 2*time.Second, func() bool { return s.gotConnect != nil })
	if s.gotConnect == nil || len(s.gotConnect.SteamCookie) <= 8 {
		t.Fatalf("steam cookie %v", s.gotConnect)
	}
}

func TestGameEventsUserMessagesAndCommands(t *testing.T) {
	var moves int
	c, s := newTestClient(t, Config{CreateMove: func(cmd *usercmd.UserCmd) {
		moves++
		cmd.ViewAngles = [3]float32{10, 20, 0}
		cmd.Buttons = usercmd.InAttack
	}})
	now := run(t, c, s, epoch, 10*time.Second, c.Active)
	if !c.Active() {
		t.Fatal("not active")
	}
	c.PollEvents()

	// A game event, a user message and a cvar query from the server.
	ev := gameevent.NewEvent(s.events.ByName("player_say"))
	ev.Set("userid", gameevent.Value{Int: 7})
	ev.Set("text", gameevent.Value{String: "hello"})
	w := bitbuf.NewWriter(0)
	if err := s.events.Encode(w, ev); err != nil {
		t.Fatal(err)
	}
	s.reliable(&message.SvcGameEvent{Data: message.BitsFromWriter(w)})

	sayID, _ := usermessage.NewRegistry(usermessage.TF2).ID("SayText2")
	um := bitbuf.NewWriter(0)
	um.WriteUint8(1)
	um.WriteUint8(1)
	um.WriteString("TF_Chat_All")
	um.WriteString("bot")
	um.WriteString("hi there")
	um.WriteString("")
	um.WriteString("")
	s.reliable(&message.SvcUserMessage{MsgType: uint8(sayID), Data: message.BitsFromWriter(um)})
	s.reliable(&message.SvcGetCvarValue{Cookie: 42, CvarName: "cl_cmdrate"})
	s.reliable(&message.NetStringCmd{Command: "playgamesound Hud.Hint"})

	if err := c.SendStringCmd("say hello"); err != nil {
		t.Fatal(err)
	}
	run(t, c, s, now, 2*time.Second, func() bool { return s.gotCvarResp != nil && len(s.gotStringCmds) > 0 })

	var gotEvent, gotSay, gotCmd bool
	for _, e := range c.PollEvents() {
		switch e := e.(type) {
		case EventGameEvent:
			gotEvent = e.Event.Name() == "player_say" && e.Event.String("text") == "hello" && e.Event.Int("userid") == 7
		case EventUserMessage:
			if m, ok := e.Message.(usermessage.SayText2); ok && e.Name == "SayText2" {
				gotSay = m.Params[1] == "hi there"
			}
		case EventStringCmd:
			gotCmd = e.Command == "playgamesound Hud.Hint"
		}
	}
	if !gotEvent || !gotSay || !gotCmd {
		t.Fatalf("event=%v say=%v stringcmd=%v", gotEvent, gotSay, gotCmd)
	}
	if s.gotCvarResp.Cookie != 42 || s.gotCvarResp.Status != message.CvarQueryValueIntact || s.gotCvarResp.CvarValue != "66" {
		t.Fatalf("cvar response %+v", s.gotCvarResp)
	}
	if s.gotStringCmds[0] != "say hello" {
		t.Fatalf("string cmds %v", s.gotStringCmds)
	}
	if moves == 0 {
		t.Fatal("CreateMove never called")
	}
	last := s.gotMoves[len(s.gotMoves)-1]
	if last.ViewAngles != [3]float32{10, 20, 0} || last.Buttons != usercmd.InAttack {
		t.Fatalf("move %+v", last)
	}
}

func TestMapChangeReconnect(t *testing.T) {
	c, s := newTestClient(t, Config{})
	now := run(t, c, s, epoch, 10*time.Second, c.Active)
	if !c.Active() {
		t.Fatal("not active")
	}
	// CBaseClient::Reconnect: clear the channel and restart from connected.
	s.ch.Clear()
	s.spawnCount++
	s.signon = protocol.SignonConnected
	s.reliable(&message.NetSignonState{State: protocol.SignonConnected, SpawnCount: -1})
	c.PollEvents()
	run(t, c, s, now, 10*time.Second, func() bool { return c.Active() && c.ServerInfo().ServerCount == s.spawnCount })
	if !c.Active() || c.ServerInfo().ServerCount != s.spawnCount {
		t.Fatalf("client did not rejoin after level change: signon %d", c.SignonState())
	}
}
