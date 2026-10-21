package query

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/galaco/sourcenet/connectionless"
)

const testChallenge = 0x5eed1234

// fakeServer answers A2S requests on 127.0.0.1, requiring challenges for
// every query and splitting large responses.
type fakeServer struct {
	conn     *net.UDPConn
	dropNext atomic.Int32 // number of requests to ignore
	silent   atomic.Bool
	requests atomic.Int32
	wg       sync.WaitGroup
	info     *connectionless.Info
	players  *connectionless.Players
	rules    *connectionless.Rules
}

func newFakeServer(t *testing.T) *fakeServer {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{
		conn: conn,
		info: &connectionless.Info{Protocol: 17, Name: "fake", Map: "pl_upward", Folder: "tf", Game: "Team Fortress",
			AppID: 440, Players: 2, MaxPlayers: 24, ServerType: 'd', Environment: 'l', Version: "1.0",
			EDF: connectionless.EDFPort | connectionless.EDFKeywords, Port: 27015, Keywords: "payload"},
		players: &connectionless.Players{},
		rules:   &connectionless.Rules{},
	}
	for i := 0; i < 40; i++ {
		s.players.Players = append(s.players.Players, connectionless.Player{Index: uint8(i), Name: "a reasonably long player name", Score: int32(i), Duration: float32(i)})
	}
	for i := 0; i < 200; i++ {
		s.rules.Rules = append(s.rules.Rules, connectionless.Rule{Name: "some_convar_name", Value: "a value"})
	}
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(func() {
		conn.Close()
		s.wg.Wait()
	})
	return s
}

func (s *fakeServer) addr() string { return s.conn.LocalAddr().String() }

func (s *fakeServer) serve() {
	defer s.wg.Done()
	buf := make([]byte, 2048)
	for {
		n, from, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		s.requests.Add(1)
		if s.silent.Load() {
			continue
		}
		if s.dropNext.Load() > 0 {
			s.dropNext.Add(-1)
			continue
		}
		req, err := connectionless.Parse(buf[:n])
		if err != nil {
			continue
		}
		var resp connectionless.Packet
		switch r := req.(type) {
		case *connectionless.InfoRequest:
			if !r.HasChallenge || r.Challenge != testChallenge {
				resp = &connectionless.QueryChallenge{Challenge: testChallenge}
			} else {
				resp = s.info
			}
		case *connectionless.PlayerRequest:
			if r.Challenge != testChallenge {
				resp = &connectionless.QueryChallenge{Challenge: testChallenge}
			} else {
				resp = s.players
			}
		case *connectionless.RulesRequest:
			if r.Challenge != testChallenge {
				resp = &connectionless.QueryChallenge{Challenge: testChallenge}
			} else {
				resp = s.rules
			}
		default:
			continue
		}
		data := resp.Marshal()
		if len(data) > 1200 {
			frags := connectionless.SplitQueryResponse(data, 99, 1200)
			// Send out of order to exercise reassembly.
			for i := len(frags) - 1; i >= 0; i-- {
				s.conn.WriteToUDP(frags[i], from)
			}
			continue
		}
		s.conn.WriteToUDP(data, from)
	}
}

func TestInfo(t *testing.T) {
	s := newFakeServer(t)
	c := &Client{Timeout: time.Second}
	info, err := c.Info(context.Background(), s.addr())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "fake" || info.Keywords != "payload" || info.Port != 27015 || info.AppID != 440 {
		t.Fatalf("%+v", info)
	}
	if s.requests.Load() != 2 {
		t.Fatalf("expected challenge round trip, server saw %d requests", s.requests.Load())
	}
}

func TestPlayersAndRulesSplit(t *testing.T) {
	s := newFakeServer(t)
	c := &Client{Timeout: time.Second}
	players, err := c.Players(context.Background(), s.addr())
	if err != nil {
		t.Fatal(err)
	}
	if len(players) != 40 || players[39].Score != 39 {
		t.Fatalf("got %d players", len(players))
	}
	rules, err := c.Rules(context.Background(), s.addr())
	if err != nil {
		t.Fatal(err)
	}
	if len(rules.Rules) != 200 {
		t.Fatalf("got %d rules", len(rules.Rules))
	}
}

func TestRetry(t *testing.T) {
	s := newFakeServer(t)
	s.dropNext.Store(1)
	c := &Client{Timeout: 100 * time.Millisecond, Retries: 2}
	if _, err := c.Info(context.Background(), s.addr()); err != nil {
		t.Fatal(err)
	}
	if s.requests.Load() != 3 {
		t.Fatalf("server saw %d requests, want 3", s.requests.Load())
	}
}

func TestTimeoutAndCancel(t *testing.T) {
	s := newFakeServer(t)
	s.silent.Store(true)
	c := &Client{Timeout: 50 * time.Millisecond, Retries: 1}
	_, err := c.Info(context.Background(), s.addr())
	var ne net.Error
	if err == nil || !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("expected timeout, got %v", err)
	}
	if s.requests.Load() != 2 {
		t.Fatalf("server saw %d requests, want 2", s.requests.Load())
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err = (&Client{Timeout: 5 * time.Second}).Players(ctx, s.addr())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("cancellation did not interrupt the read")
	}
}

func TestPing(t *testing.T) {
	s := newFakeServer(t)
	rtt, err := (&Client{}).Ping(context.Background(), s.addr())
	if err != nil {
		t.Fatal(err)
	}
	if rtt <= 0 || rtt > time.Second {
		t.Fatalf("rtt %v", rtt)
	}
}
