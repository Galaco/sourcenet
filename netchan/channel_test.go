package netchan

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/protocol"
)

const testMsgType = 63

var epoch = time.Unix(1700000000, 0)

// writeTestMsg frames an arbitrary payload as a pseudo netmessage.
func writeTestMsg(w *bitbuf.Writer, id uint32, payload []byte) {
	w.WriteBits(testMsgType, protocol.NetMsgTypeBits)
	w.WriteUint32(id)
	w.WriteBits(uint32(len(payload)), 20)
	w.WriteBytes(payload)
}

type testMsg struct {
	id      uint32
	payload []byte
}

// readTestMsgs parses a block of pseudo netmessages the way the engine walks
// a message stream: until fewer than 6 bits remain, NOPs skipped.
func readTestMsgs(t *testing.T, r *bitbuf.Reader) []testMsg {
	t.Helper()
	var out []testMsg
	for r.BitsLeft() >= protocol.NetMsgTypeBits {
		switch typ := r.ReadBits(protocol.NetMsgTypeBits); typ {
		case protocol.NetNOP:
		case testMsgType:
			id := r.ReadUint32()
			n := int(r.ReadBits(20))
			out = append(out, testMsg{id, r.ReadBytes(n)})
		default:
			t.Fatalf("unexpected message type %d at bit %d", typ, r.Pos())
		}
	}
	if r.Overflowed() {
		t.Fatal("stream overflow")
	}
	return out
}

func payloadFor(id uint32, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(int(id)*31 + i)
	}
	return b
}

// link simulates an unreliable datagram path between two channels.
type link struct {
	rng       *rand.Rand
	loss      float64
	duplicate float64
	reorder   float64
	queue     []queued
	seq       int
}

type queued struct {
	at  int
	seq int
	pkt []byte
}

func (l *link) send(tick int, pkt []byte) {
	if l.rng.Float64() < l.loss {
		return
	}
	delay := 1
	if l.rng.Float64() < l.reorder {
		delay += l.rng.Intn(4)
	}
	cp := append([]byte(nil), pkt...)
	l.seq++
	l.queue = append(l.queue, queued{tick + delay, l.seq, cp})
	if l.rng.Float64() < l.duplicate {
		l.seq++
		l.queue = append(l.queue, queued{tick + delay + l.rng.Intn(3), l.seq, cp})
	}
}

func (l *link) due(tick int) [][]byte {
	sort.SliceStable(l.queue, func(i, j int) bool {
		if l.queue[i].at != l.queue[j].at {
			return l.queue[i].at < l.queue[j].at
		}
		return l.queue[i].seq < l.queue[j].seq
	})
	var out [][]byte
	n := 0
	for _, q := range l.queue {
		if q.at <= tick {
			out = append(out, q.pkt)
			n++
		}
	}
	l.queue = l.queue[n:]
	return out
}

type endpoint struct {
	ch       *Channel
	out      *link
	gotRel   []testMsg
	gotFiles []File
	gotUnrel int
}

func (e *endpoint) receive(t *testing.T, now time.Time, pkt []byte) {
	in, err := e.ch.ProcessPacket(now, pkt)
	if err != nil {
		if errors.Is(err, ErrStalePacket) || errors.Is(err, ErrFragmentOrder) {
			return
		}
		t.Fatalf("process packet: %v", err)
	}
	for _, r := range in.Reliable {
		e.gotRel = append(e.gotRel, readTestMsgs(t, r)...)
	}
	e.gotFiles = append(e.gotFiles, in.Files...)
	for _, m := range readTestMsgs(t, in.Unreliable) {
		if !bytes.Equal(m.payload, payloadFor(m.id, len(m.payload))) {
			t.Fatalf("unreliable payload corrupt")
		}
		e.gotUnrel++
	}
}

func newPair(cfg Config, l func() *link) (*endpoint, *endpoint) {
	a := &endpoint{ch: New(epoch, cfg), out: l()}
	b := &endpoint{ch: New(epoch, cfg), out: l()}
	return a, b
}

// exchange runs both endpoints for a number of ticks, each sending one
// packet per tick and delivering whatever the links make available.
func exchange(t *testing.T, a, b *endpoint, start, ticks int) time.Time {
	t.Helper()
	var now time.Time
	for tick := start; tick < start+ticks; tick++ {
		now = epoch.Add(time.Duration(tick) * 15 * time.Millisecond)
		for _, p := range a.out.due(tick) {
			b.receive(t, now, p)
		}
		for _, p := range b.out.due(tick) {
			a.receive(t, now, p)
		}
		for _, e := range []*endpoint{a, b} {
			pkt, err := e.ch.Transmit(now)
			if err != nil {
				t.Fatalf("transmit: %v", err)
			}
			e.out.send(tick, pkt)
		}
	}
	return now
}

func TestReliableDeliveryOverLossyLink(t *testing.T) {
	for _, tc := range []struct {
		name               string
		loss, dup, reorder float64
		compress           bool
	}{
		{"perfect", 0, 0, 0, false},
		{"lossy", 0.3, 0, 0, false},
		{"lossy duplicated reordered", 0.25, 0.2, 0.3, false},
		{"compressed fragments", 0.2, 0.1, 0.2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(7))
			mk := func() *link { return &link{rng: rng, loss: tc.loss, duplicate: tc.dup, reorder: tc.reorder} }
			a, b := newPair(Config{Challenge: 0x1234, Rate: MaxRate, CompressFragments: tc.compress}, mk)

			var sent []testMsg
			id := uint32(0)
			tick := 0
			sizes := []int{0, 1, 17, 255, 256, 257, 1023, 1024, 1025, 4000, 20000, 70000}
			for round := 0; round < 6; round++ {
				for _, n := range sizes {
					id++
					p := payloadFor(id, n)
					if tc.compress {
						p = bytes.Repeat([]byte{byte(id)}, n)
					}
					writeTestMsg(a.ch.Reliable(), id, p)
					sent = append(sent, testMsg{id, p})
					// A second message in the same stream exercises block merging.
					if n%2 == 1 {
						id++
						writeTestMsg(a.ch.Reliable(), id, []byte{byte(id)})
						sent = append(sent, testMsg{id, []byte{byte(id)}})
					}
					writeTestMsg(a.ch.Unreliable(), 1_000_000+id, payloadFor(1_000_000+id, 20))
					exchange(t, a, b, tick, 3)
					tick += 3
				}
			}
			for i := 0; i < 4000 && a.ch.HasPendingReliableData(); i += 50 {
				exchange(t, a, b, tick, 50)
				tick += 50
			}
			exchange(t, a, b, tick, 20)

			if a.ch.HasPendingReliableData() {
				t.Fatal("reliable data still pending")
			}
			if len(b.gotRel) != len(sent) {
				t.Fatalf("received %d reliable messages, want %d", len(b.gotRel), len(sent))
			}
			for i := range sent {
				if b.gotRel[i].id != sent[i].id || !bytes.Equal(b.gotRel[i].payload, sent[i].payload) {
					t.Fatalf("message %d mismatch: id %d want %d", i, b.gotRel[i].id, sent[i].id)
				}
			}
			if tc.loss == 0 && b.gotUnrel == 0 {
				t.Fatal("no unreliable messages delivered")
			}
		})
	}
}

func TestBidirectionalReliable(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	mk := func() *link { return &link{rng: rng, loss: 0.2, reorder: 0.2} }
	a, b := newPair(Config{Challenge: 99, Rate: MaxRate}, mk)
	var toA, toB []uint32
	tick := 0
	for i := uint32(1); i <= 200; i++ {
		writeTestMsg(a.ch.Reliable(), i, payloadFor(i, int(i*37%3000)))
		toB = append(toB, i)
		writeTestMsg(b.ch.Reliable(), 5000+i, payloadFor(5000+i, int(i*53%2000)))
		toA = append(toA, 5000+i)
		exchange(t, a, b, tick, 2)
		tick += 2
	}
	for i := 0; i < 100 && (a.ch.HasPendingReliableData() || b.ch.HasPendingReliableData()); i++ {
		exchange(t, a, b, tick, 20)
		tick += 20
	}
	exchange(t, a, b, tick, 10)
	check := func(got []testMsg, want []uint32) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %d messages, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i].id != want[i] || !bytes.Equal(got[i].payload, payloadFor(want[i], len(got[i].payload))) {
				t.Fatalf("message %d: id %d want %d", i, got[i].id, want[i])
			}
		}
	}
	check(b.gotRel, toB)
	check(a.gotRel, toA)
}

func TestFileTransfer(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	mk := func() *link { return &link{rng: rng, loss: 0.15} }
	for _, foreground := range []bool{false, true} {
		a, b := newPair(Config{Challenge: 1, Rate: MaxRate, ForegroundFileTransmission: foreground}, mk)
		small := payloadFor(1, 100)
		big := payloadFor(2, 30000)
		if err := a.ch.QueueFile("user_custom/aa/abc.dat", 7, small); err != nil {
			t.Fatal(err)
		}
		if err := a.ch.QueueFile("downloads/map.bsp", 8, big); err != nil {
			t.Fatal(err)
		}
		writeTestMsg(a.ch.Reliable(), 1, []byte("alongside"))
		tick := 0
		for i := 0; i < 200 && a.ch.HasPendingReliableData(); i++ {
			exchange(t, a, b, tick, 20)
			tick += 20
		}
		exchange(t, a, b, tick, 10)
		if len(b.gotFiles) != 2 {
			t.Fatalf("foreground=%v: got %d files", foreground, len(b.gotFiles))
		}
		if f := b.gotFiles[0]; f.Name != "user_custom/aa/abc.dat" || f.TransferID != 7 || !bytes.Equal(f.Data, small) {
			t.Fatalf("file 0 mismatch: %+v", f.Name)
		}
		if f := b.gotFiles[1]; f.Name != "downloads/map.bsp" || f.TransferID != 8 || !bytes.Equal(f.Data, big) {
			t.Fatalf("file 1 mismatch")
		}
		if len(b.gotRel) != 1 || string(b.gotRel[0].payload) != "alongside" {
			t.Fatalf("reliable message alongside files not delivered")
		}
	}
}

func TestPacketLayoutAndPadding(t *testing.T) {
	c := New(epoch, Config{Challenge: 0xdeadbeef})
	pkt, err := c.Transmit(epoch)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkt) < protocol.MinRoutablePayload {
		t.Fatalf("packet shorter than minimum routable payload: %d", len(pkt))
	}
	r := bitbuf.NewReader(pkt)
	if seq := r.ReadInt32(); seq != 1 {
		t.Fatalf("first sequence %d", seq)
	}
	if ack := r.ReadInt32(); ack != 0 {
		t.Fatalf("ack %d", ack)
	}
	flags := r.ReadUint8()
	if flags&protocol.PacketFlagChallenge == 0 || flags&protocol.PacketFlagReliable != 0 {
		t.Fatalf("flags %#x", flags)
	}
	sum := r.ReadUint16()
	if sum != shortChecksum(pkt[11:]) {
		t.Fatal("checksum does not cover the rest of the packet")
	}
	if rel := r.ReadUint8(); rel != 0 {
		t.Fatalf("reliable state %d", rel)
	}
	if ch := r.ReadUint32(); ch != 0xdeadbeef {
		t.Fatalf("challenge %#x", ch)
	}
	// Remaining bits must parse as NOPs with fewer than 6 trailing pad bits.
	for r.BitsLeft() >= protocol.NetMsgTypeBits {
		if typ := r.ReadBits(protocol.NetMsgTypeBits); typ != protocol.NetNOP {
			t.Fatalf("padding type %d", typ)
		}
	}
	if c.OutSequence() != 2 {
		t.Fatal("sequence not advanced")
	}
}

func TestChecksumAndChallengeRejection(t *testing.T) {
	a := New(epoch, Config{Challenge: 5})
	b := New(epoch, Config{Challenge: 5})
	pkt, _ := a.Transmit(epoch)
	bad := append([]byte(nil), pkt...)
	bad[len(bad)-1] ^= 0x40
	if _, err := b.ProcessPacket(epoch, bad); !errors.Is(err, ErrChecksum) {
		t.Fatalf("corrupt packet: %v", err)
	}
	c := New(epoch, Config{Challenge: 6})
	if _, err := c.ProcessPacket(epoch, pkt); !errors.Is(err, ErrChallenge) {
		t.Fatalf("wrong challenge: %v", err)
	}
	if _, err := b.ProcessPacket(epoch, pkt); err != nil {
		t.Fatalf("valid packet: %v", err)
	}
	if _, err := b.ProcessPacket(epoch, pkt); !errors.Is(err, ErrStalePacket) {
		t.Fatalf("duplicate: %v", err)
	}
	if b.InSequence() != 1 {
		t.Fatalf("in sequence %d", b.InSequence())
	}
}

func TestDropAndChokeAccounting(t *testing.T) {
	a := New(epoch, Config{Challenge: 1})
	b := New(epoch, Config{Challenge: 1})
	p1, _ := a.Transmit(epoch)
	a.Transmit(epoch) // lost
	a.Transmit(epoch) // lost
	a.SetChoked()
	p5, _ := a.Transmit(epoch)
	if _, err := b.ProcessPacket(epoch, p1); err != nil {
		t.Fatal(err)
	}
	in, err := b.ProcessPacket(epoch, p5)
	if err != nil {
		t.Fatal(err)
	}
	if in.Sequence != 5 || in.Choked != 1 || in.Dropped != 2 {
		t.Fatalf("seq %d choked %d dropped %d", in.Sequence, in.Choked, in.Dropped)
	}
}

func TestTooManyDropped(t *testing.T) {
	a := New(epoch, Config{Challenge: 1})
	b := New(epoch, Config{Challenge: 1, MaxPacketDrop: 3})
	for i := 0; i < 5; i++ {
		a.Transmit(epoch)
	}
	p, _ := a.Transmit(epoch)
	if _, err := b.ProcessPacket(epoch, p); !errors.Is(err, ErrTooManyDropped) {
		t.Fatalf("got %v", err)
	}
}

func TestRateLimiting(t *testing.T) {
	c := New(epoch, Config{Challenge: 1, Rate: MinRate})
	if !c.CanPacket(epoch.Add(time.Nanosecond)) {
		t.Fatal("fresh channel should be able to send")
	}
	writeTestMsg(c.Unreliable(), 1, make([]byte, 900))
	if _, err := c.Transmit(epoch); err != nil {
		t.Fatal(err)
	}
	// ~950 bytes at 1000 B/s blocks sending for nearly a second.
	if c.CanPacket(epoch.Add(500 * time.Millisecond)) {
		t.Fatal("rate limit not applied")
	}
	if !c.CanPacket(epoch.Add(1100 * time.Millisecond)) {
		t.Fatal("rate limit not lifted")
	}
}

func TestClearMarksSubchannelsDirty(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	mk := func() *link { return &link{rng: rng} }
	a, b := newPair(Config{Challenge: 1, Rate: MaxRate}, mk)
	writeTestMsg(a.ch.Reliable(), 1, payloadFor(1, 5000))
	exchange(t, a, b, 0, 1) // first fragments on the wire, unacknowledged
	a.ch.Clear()
	writeTestMsg(a.ch.Reliable(), 2, payloadFor(2, 300))
	tick := 1
	for i := 0; i < 50 && a.ch.HasPendingReliableData(); i++ {
		exchange(t, a, b, tick, 10)
		tick += 10
	}
	exchange(t, a, b, tick, 10)
	if n := len(b.gotRel); n != 1 || b.gotRel[0].id != 2 {
		ids := make([]string, 0, n)
		for _, m := range b.gotRel {
			ids = append(ids, fmt.Sprint(m.id))
		}
		t.Fatalf("after clear got messages %v, want [2]", ids)
	}
}

func TestReliableOverflow(t *testing.T) {
	c := New(epoch, Config{})
	c.Reliable().WriteBytes(make([]byte, protocol.NetMaxPayload+1))
	if !c.IsOverflowed() {
		t.Fatal("expected overflow")
	}
	if _, err := c.Transmit(epoch); !errors.Is(err, ErrReliableOverflow) {
		t.Fatalf("got %v", err)
	}
}

func TestStatsLatency(t *testing.T) {
	a := New(epoch, Config{Challenge: 1})
	b := New(epoch, Config{Challenge: 1})
	now := epoch
	for i := 0; i < 40; i++ {
		pa, _ := a.Transmit(now)
		now = now.Add(50 * time.Millisecond)
		if _, err := b.ProcessPacket(now, pa); err != nil {
			t.Fatal(err)
		}
		pb, _ := b.Transmit(now)
		now = now.Add(50 * time.Millisecond)
		if _, err := a.ProcessPacket(now, pb); err != nil {
			t.Fatal(err)
		}
	}
	st := a.Stats(FlowOutgoing)
	if st.Latency < 90*time.Millisecond || st.Latency > 110*time.Millisecond {
		t.Fatalf("latency %v, want ~100ms", st.Latency)
	}
	if st.TotalPackets != 40 {
		t.Fatalf("total packets %d", st.TotalPackets)
	}
}

func TestTimeouts(t *testing.T) {
	c := New(epoch, Config{})
	if c.IsTimingOut(epoch.Add(3 * time.Second)) {
		t.Fatal("timing out too early")
	}
	if !c.IsTimingOut(epoch.Add(5 * time.Second)) {
		t.Fatal("should be timing out")
	}
	c.SetTimeout(10 * time.Second)
	if c.IsTimedOut(epoch.Add(9*time.Second)) || !c.IsTimedOut(epoch.Add(11*time.Second)) {
		t.Fatal("timeout")
	}
	c.SetTimeout(0)
	if c.IsTimedOut(epoch.Add(time.Hour * 24)) {
		t.Fatal("disabled timeout")
	}
}

// FuzzProcessPacket checks that arbitrary packets never panic the channel.
// Checksums are disabled so random input reaches the fragment parser.
func FuzzProcessPacket(f *testing.F) {
	a := New(epoch, Config{Challenge: 1, DisableChecksum: true})
	writeTestMsg(a.Reliable(), 1, payloadFor(1, 3000))
	for i := 0; i < 3; i++ {
		p, _ := a.Transmit(epoch)
		f.Add(p)
	}
	f.Fuzz(func(t *testing.T, pkt []byte) {
		b := New(epoch, Config{Challenge: 1, DisableChecksum: true})
		for i := 0; i < 2; i++ {
			in, _ := b.ProcessPacket(epoch, pkt)
			if in != nil {
				for _, r := range in.Reliable {
					r.ReadBits(32)
				}
			}
			// Mutate the sequence so the second pass is not stale.
			if len(pkt) >= 4 {
				pkt[0]++
			}
		}
		b.Transmit(epoch)
	})
}
