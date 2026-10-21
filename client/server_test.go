package client

import (
	"errors"
	"testing"
	"time"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/connectionless"
	"github.com/galaco/sourcenet/gameevent"
	"github.com/galaco/sourcenet/message"
	"github.com/galaco/sourcenet/netchan"
	"github.com/galaco/sourcenet/packet"
	"github.com/galaco/sourcenet/protocol"
	"github.com/galaco/sourcenet/stringtable"
	"github.com/galaco/sourcenet/usercmd"
)

// fakeServer implements the server side of the connection sequence
// (CBaseServer / CBaseClient / CGameClient) closely enough to drive the
// client through a complete signon.
type fakeServer struct {
	t         *testing.T
	transport *Pipe
	decoder   *packet.Decoder
	encoder   packet.Encoder
	ctx       *message.Context
	reg       *message.Registry

	challenge    uint32
	authProtocol int32
	maxClasses   uint16
	reject       string
	version      string

	ch          *netchan.Channel
	signon      int
	spawnCount  int32
	tick        int32
	events      *gameevent.List
	sendEnts    func(s *fakeServer, full bool) *message.SvcPacketEntities
	classInfo   *message.SvcClassInfo
	sendTables  []message.Message // svc_SendTable, sent before classInfo
	extraSignon []message.Message

	gotConnect     *connectionless.Connect
	gotConVars     map[string]string
	gotClientInfo  *message.ClcClientInfo
	gotListen      *message.ClcListenEvents
	gotStringCmds  []string
	gotMoves       []usercmd.UserCmd
	gotTicks       []int32
	gotDisconnect  string
	gotBaselineAck *message.ClcBaselineAck
	gotCvarResp    *message.ClcRespondCvarValue
}

func newFakeServer(t *testing.T, transport *Pipe) *fakeServer {
	desc := &gameevent.Descriptor{ID: 3, Name: "player_say", Keys: []gameevent.Key{
		{Name: "userid", Type: gameevent.Short},
		{Name: "text", Type: gameevent.String},
	}}
	return &fakeServer{
		t:            t,
		transport:    transport,
		decoder:      packet.NewDecoder(),
		ctx:          message.DefaultContext(),
		reg:          message.ClientToServer(),
		challenge:    0x5eed1234,
		authProtocol: protocol.AuthHashedCDKey,
		version:      "7654321",
		maxClasses:   300,
		spawnCount:   3,
		tick:         1000,
		events:       gameevent.NewList(desc),
		gotConVars:   map[string]string{},
	}
}

func (s *fakeServer) send(p connectionless.Packet) {
	if err := s.transport.Send(p.Marshal()); err != nil {
		s.t.Fatal(err)
	}
}

func (s *fakeServer) update(now time.Time) {
	for {
		d, ok := s.transport.Poll()
		if !ok {
			break
		}
		pkt, ok, err := s.decoder.Decode(now, d)
		if err != nil {
			s.t.Fatalf("server decode: %v", err)
		}
		if !ok {
			continue
		}
		if connectionless.IsConnectionless(pkt) {
			s.handleOOB(now, pkt)
			continue
		}
		if s.ch == nil {
			continue
		}
		in, err := s.ch.ProcessPacket(now, pkt)
		if err != nil {
			if errors.Is(err, netchan.ErrStalePacket) {
				continue
			}
			s.t.Fatalf("server netchan: %v", err)
		}
		for _, r := range in.Reliable {
			s.process(now, r)
		}
		s.process(now, in.Unreliable)
	}
	if s.ch != nil && s.signon == protocol.SignonFull && s.sendEnts != nil {
		s.tick++
		_ = message.Write(s.ch.Unreliable(), &message.NetTick{Tick: s.tick}, s.ctx)
		if pe := s.sendEnts(s, false); pe != nil {
			_ = message.Write(s.ch.Unreliable(), pe, s.ctx)
		}
	}
	if s.ch != nil {
		s.transmit(now)
	}
}

func (s *fakeServer) transmit(now time.Time) {
	pkt, err := s.ch.Transmit(now)
	if err != nil {
		s.t.Fatalf("server transmit: %v", err)
	}
	ds, err := s.encoder.Encode(pkt)
	if err != nil {
		s.t.Fatal(err)
	}
	for _, d := range ds {
		_ = s.transport.Send(d)
	}
}

func (s *fakeServer) handleOOB(now time.Time, pkt []byte) {
	p, err := connectionless.Parse(pkt)
	if err != nil {
		s.t.Fatalf("server parse oob: %v", err)
	}
	switch p := p.(type) {
	case *connectionless.InfoRequest:
		if !p.HasChallenge {
			s.send(&connectionless.QueryChallenge{Challenge: 777})
			return
		}
		s.send(&connectionless.Info{Protocol: 17, Name: "fake", Map: "ctf_2fort", Folder: "tf", Version: s.version})
	case *connectionless.GetChallenge:
		s.send(&connectionless.Challenge{ServerChallenge: s.challenge, ClientChallenge: p.ClientChallenge, AuthProtocol: s.authProtocol})
	case *connectionless.Connect:
		s.gotConnect = p
		if s.reject != "" {
			s.send(&connectionless.Reject{ClientChallenge: p.ClientChallenge, Reason: s.reject})
			return
		}
		if p.ServerChallenge != s.challenge || p.ProductVersion != s.version {
			s.send(&connectionless.Reject{ClientChallenge: p.ClientChallenge, Reason: "#GameUI_ServerRejectBadChallenge"})
			return
		}
		s.ch = netchan.New(now, netchan.Config{Challenge: s.challenge, Rate: netchan.MaxRate})
		s.signon = protocol.SignonConnected
		s.send(&connectionless.Accept{ClientChallenge: p.ClientChallenge})
	}
}

func (s *fakeServer) reliable(msgs ...message.Message) {
	for _, m := range msgs {
		if err := message.Write(s.ch.Reliable(), m, s.ctx); err != nil {
			s.t.Fatalf("server write %s: %v", m.Name(), err)
		}
	}
}

func (s *fakeServer) process(now time.Time, r *bitbuf.Reader) {
	msgs, err := message.ReadAll(r, s.reg, s.ctx)
	if err != nil {
		s.t.Fatalf("server read messages: %v", err)
	}
	for _, m := range msgs {
		switch m := m.(type) {
		case *message.NetSetConVar:
			for _, cv := range m.ConVars {
				s.gotConVars[cv.Name] = cv.Value
			}
		case *message.ClcClientInfo:
			s.gotClientInfo = m
		case *message.ClcListenEvents:
			s.gotListen = m
		case *message.NetStringCmd:
			s.gotStringCmds = append(s.gotStringCmds, m.Command)
		case *message.NetDisconnect:
			s.gotDisconnect = m.Reason
		case *message.ClcBaselineAck:
			s.gotBaselineAck = m
		case *message.ClcRespondCvarValue:
			s.gotCvarResp = m
		case *message.NetTick:
			s.gotTicks = append(s.gotTicks, m.Tick)
		case *message.ClcMove:
			cmds, err := usercmd.ReadMove(usercmd.Standard, m.Data.Reader(), m.NewCommands+m.BackupCommands)
			if err != nil {
				s.t.Fatalf("server read move: %v", err)
			}
			s.gotMoves = append(s.gotMoves, cmds[len(cmds)-m.NewCommands:]...)
		case *message.NetSignonState:
			s.signonState(int(m.State), m.SpawnCount)
		}
	}
}

// signonState mirrors CBaseClient::ProcessSignonState/SetSignonState.
func (s *fakeServer) signonState(state int, count int32) {
	if state != s.signon {
		s.t.Fatalf("client acknowledged signon %d, server at %d", state, s.signon)
	}
	if state > protocol.SignonConnected && count != s.spawnCount {
		s.t.Fatalf("spawn count %d, want %d", count, s.spawnCount)
	}
	switch state {
	case protocol.SignonConnected:
		s.sendServerInfo()
	case protocol.SignonNew:
		ci := s.classInfo
		if ci == nil {
			ci = &message.SvcClassInfo{CreateOnClient: true, NumServerClasses: 1}
		}
		w := bitbuf.NewWriter(0)
		s.events.Write(w)
		s.reliable(s.sendTables...)
		s.reliable(ci)
		s.reliable(&message.SvcVoiceInit{Codec: "vaudio_speex", Quality: 255, SampleRate: 11025})
		s.reliable(&message.SvcGameEventList{NumEvents: s.events.Len(), Data: message.BitsFromWriter(w)})
		s.reliable(s.extraSignon...)
		s.signon = protocol.SignonPrespawn
		s.reliable(&message.NetSignonState{State: protocol.SignonPrespawn, SpawnCount: s.spawnCount})
	case protocol.SignonPrespawn:
		s.signon = protocol.SignonSpawn
		s.reliable(&message.NetTick{Tick: s.tick}, &message.NetSignonState{State: protocol.SignonSpawn, SpawnCount: s.spawnCount})
	case protocol.SignonSpawn:
		s.signon = protocol.SignonFull
		pe := &message.SvcPacketEntities{MaxEntries: 1}
		if s.sendEnts != nil {
			pe = s.sendEnts(s, true)
		}
		s.reliable(&message.NetTick{Tick: s.tick}, pe)
	}
}

func (s *fakeServer) sendServerInfo() {
	s.reliable(&message.SvcPrint{Text: "Team Fortress\nMap: ctf_2fort\n"})
	s.reliable(&message.SvcServerInfo{
		Protocol:     protocol.Version,
		ServerCount:  s.spawnCount,
		IsDedicated:  true,
		ClientCRC:    -1,
		MaxClasses:   s.maxClasses,
		PlayerSlot:   2,
		MaxClients:   24,
		TickInterval: 0.015,
		OS:           'l',
		GameDir:      "tf",
		MapName:      "ctf_2fort",
		SkyName:      "sky_tf2_04",
		HostName:     "fake server",
	})
	s.reliable(&message.NetTick{Tick: s.tick})

	ui := stringtable.NewTable(protocol.TableUserInfo, 256)
	info := stringtable.PlayerInfo{Name: "bot", UserID: 7, GUID: "BOT", FakePlayer: true}
	ui.Add("0", info.Marshal(true))
	w := bitbuf.NewWriter(0)
	ui.WriteUpdate(w, []int{0})
	s.reliable(&message.SvcCreateStringTable{TableName: protocol.TableUserInfo, MaxEntries: 256, NumEntries: 1, Data: message.BitsFromWriter(w)})

	s.reliable(&message.NetSetConVar{ConVars: []message.ConVar{{Name: "sv_cheats", Value: "0"}}})
	s.signon = protocol.SignonNew
	s.reliable(&message.NetSignonState{State: protocol.SignonNew, SpawnCount: s.spawnCount})
}

// run drives client and server with a fake clock.
func run(t *testing.T, c *Client, s *fakeServer, start time.Time, d time.Duration, until func() bool) time.Time {
	t.Helper()
	now := start
	for end := start.Add(d); now.Before(end); now = now.Add(5 * time.Millisecond) {
		if err := c.Update(now); err != nil {
			t.Fatal(err)
		}
		s.update(now)
		if until != nil && until() {
			return now
		}
	}
	return now
}
