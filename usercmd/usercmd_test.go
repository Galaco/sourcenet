package usercmd

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/galaco/sourcenet/bitbuf"
)

func TestUnchangedDeltaIsCompact(t *testing.T) {
	from := UserCmd{CommandNumber: 10, TickCount: 100, Buttons: InAttack}
	to := from
	to.CommandNumber++
	to.TickCount++
	w := bitbuf.NewWriter(4)
	Standard.WriteDelta(w, &from, &to)
	// 13 change bits for the standard codec, all zero.
	if w.BitsWritten() != 13 || !bytes.Equal(w.Bytes(), []byte{0, 0}) {
		t.Fatalf("got %d bits % x", w.BitsWritten(), w.Bytes())
	}
	w = bitbuf.NewWriter(4)
	HL2.WriteDelta(w, &from, &to)
	if w.BitsWritten() != 14 {
		t.Fatalf("hl2: got %d bits", w.BitsWritten())
	}
}

func TestGoldenDelta(t *testing.T) {
	from := UserCmd{}
	to := UserCmd{CommandNumber: 1, TickCount: 5, ViewAngles: [3]float32{0, 1.5, 0}, Buttons: InJump, WeaponSelect: 3, MouseDX: -1}

	// Hand-assembled reference following WriteUsercmd.
	ref := bitbuf.NewWriter(0)
	ref.WriteBits(0, 1) // command number increments
	ref.WriteBits(1, 1)
	ref.WriteBits(5, 32)
	ref.WriteBits(0, 1) // pitch
	ref.WriteBits(1, 1)
	ref.WriteBits(0x3fc00000, 32) // yaw 1.5
	ref.WriteBits(0, 1)           // roll
	ref.WriteBits(0, 3)           // forward, side, up
	ref.WriteBits(1, 1)
	ref.WriteBits(InJump, 32)
	ref.WriteBits(0, 1) // impulse
	ref.WriteBits(1, 1)
	ref.WriteBits(3, 11)
	ref.WriteBits(0, 1) // subtype
	ref.WriteBits(1, 1)
	ref.WriteBits(0xffff, 16) // mousedx
	ref.WriteBits(0, 1)       // mousedy

	w := bitbuf.NewWriter(0)
	Standard.WriteDelta(w, &from, &to)
	if w.BitsWritten() != ref.BitsWritten() || !bytes.Equal(w.Bytes(), ref.Bytes()) {
		t.Fatalf("got % x (%d bits) want % x (%d bits)", w.Bytes(), w.BitsWritten(), ref.Bytes(), ref.BitsWritten())
	}

	got, err := Standard.ReadDelta(w.Reader(), &from)
	if err != nil {
		t.Fatal(err)
	}
	to.RandomSeed = RandomSeed(1)
	if !reflect.DeepEqual(got, to) {
		t.Fatalf("got %+v want %+v", got, to)
	}
}

func TestRoundTripAllFields(t *testing.T) {
	for _, c := range []Codec{Standard, HL2} {
		from := UserCmd{CommandNumber: 7, TickCount: 9, ViewAngles: [3]float32{1, 2, 3}, Impulse: 4, WeaponSelect: 5, WeaponSubtype: 6}
		to := UserCmd{CommandNumber: 100, TickCount: 200, ViewAngles: [3]float32{-10, 20.5, 0}, ForwardMove: 450, SideMove: -450, UpMove: 320,
			Buttons: InDuck | InForward, Impulse: 201, WeaponSelect: 2047, WeaponSubtype: 63, MouseDX: 1234, MouseDY: -4321}
		if c == HL2 {
			to.GroundContacts = []GroundContact{{EntIndex: 42, MinHeight: -1.5, MaxHeight: 64}}
		}
		w := bitbuf.NewWriter(0)
		c.WriteDelta(w, &from, &to)
		r := w.Reader()
		got, err := c.ReadDelta(r, &from)
		if err != nil {
			t.Fatal(err)
		}
		to.RandomSeed = RandomSeed(to.CommandNumber)
		if !reflect.DeepEqual(got, to) {
			t.Fatalf("got %+v want %+v", got, to)
		}
		if r.BitsLeft() >= 8 {
			t.Fatalf("%d bits left", r.BitsLeft())
		}
	}
}

func TestReadDeltaKeepsUnsentFields(t *testing.T) {
	from := UserCmd{CommandNumber: 1, TickCount: 1, WeaponSelect: 8, WeaponSubtype: 2, GroundContacts: []GroundContact{{EntIndex: 1}}}
	to := from
	to.CommandNumber, to.TickCount = 2, 2
	to.GroundContacts = nil
	w := bitbuf.NewWriter(0)
	HL2.WriteDelta(w, &from, &to)
	got, err := HL2.ReadDelta(w.Reader(), &from)
	if err != nil {
		t.Fatal(err)
	}
	// Like the engine, unsent ground contacts are inherited from the previous command.
	if got.WeaponSubtype != 2 || len(got.GroundContacts) != 1 {
		t.Fatalf("%+v", got)
	}
	got.GroundContacts[0].EntIndex = 99
	if from.GroundContacts[0].EntIndex != 1 {
		t.Fatal("ReadDelta must not alias the previous command's slice")
	}
}

func TestBuildMoveRoundTrip(t *testing.T) {
	var history []UserCmd
	for i := int32(1); i <= 20; i++ {
		history = append(history, UserCmd{CommandNumber: i, TickCount: 1000 + i, ViewAngles: [3]float32{0, float32(i), 0}, Buttons: i % 3})
	}
	data, nbits := BuildMove(Standard, history, 2, 3)
	cmds, err := ReadMove(Standard, bitbuf.NewReaderBits(data, nbits), 5)
	if err != nil {
		t.Fatal(err)
	}
	for i, cmd := range cmds {
		want := history[15+i]
		want.RandomSeed = RandomSeed(want.CommandNumber)
		if !reflect.DeepEqual(cmd, want) {
			t.Fatalf("cmd %d: got %+v want %+v", i, cmd, want)
		}
	}

	// Counts are clamped and short histories padded with zero commands.
	data, nbits = BuildMove(Standard, history[:1], 100, 100)
	n, b := ClampCounts(100, 100)
	if n != 15 || b != 7 {
		t.Fatal("clamp")
	}
	cmds, err = ReadMove(Standard, bitbuf.NewReaderBits(data, nbits), n+b)
	if err != nil {
		t.Fatal(err)
	}
	if last := cmds[len(cmds)-1]; last.CommandNumber != 1 || last.TickCount != 1001 {
		t.Fatalf("last %+v", last)
	}
}

func TestTruncatedDelta(t *testing.T) {
	w := bitbuf.NewWriter(0)
	Standard.WriteDelta(w, &UserCmd{}, &UserCmd{CommandNumber: 50, Buttons: 1})
	data := w.Bytes()
	if _, err := Standard.ReadDelta(bitbuf.NewReaderBits(data, 20), &UserCmd{}); err == nil {
		t.Fatal("expected overflow error")
	}
}

func TestRandomSeed(t *testing.T) {
	// The seed is deterministic and fits in 31 bits.
	if RandomSeed(1) != RandomSeed(1) || RandomSeed(1) == RandomSeed(2) || RandomSeed(12345) < 0 {
		t.Fatal("unexpected seeds")
	}
}
