package gameevent

import (
	"testing"

	"github.com/galaco/sourcenet/bitbuf"
)

func sampleList() *List {
	return NewList(
		&Descriptor{ID: 3, Name: "player_death", Keys: []Key{
			{"userid", Short}, {"attacker", Short}, {"weapon", String}, {"crit", Bool},
			{"damage", Long}, {"customkill", Byte}, {"distance", Float}, {"clientonly", Local},
		}},
		&Descriptor{ID: 300, Name: "round_start", Keys: []Key{{"timelimit", Long}}},
		&Descriptor{ID: 511, Name: "empty_event"},
	)
}

func TestListRoundTrip(t *testing.T) {
	src := sampleList()
	w := bitbuf.NewWriter(0)
	src.Write(w)

	r := w.Reader()
	l, err := ParseList(r, src.Len())
	if err != nil {
		t.Fatal(err)
	}
	if r.BitsLeft() != 0 {
		t.Fatalf("%d bits left", r.BitsLeft())
	}
	if l.Len() != 3 || l.ByName("round_start").ID != 300 || l.ByID(511).Name != "empty_event" || l.ByID(1) != nil {
		t.Fatalf("list %+v", l.Descriptors())
	}
	d := l.ByName("player_death")
	if len(d.Keys) != 7 {
		t.Fatalf("local keys must not be networked: %+v", d.Keys)
	}
	for i, k := range d.Keys {
		if k != src.ByID(3).Keys[i] {
			t.Errorf("key %d = %+v", i, k)
		}
	}
}

func TestEventRoundTripAllTypes(t *testing.T) {
	src := sampleList()
	w := bitbuf.NewWriter(0)
	src.Write(w)
	recv, err := ParseList(w.Reader(), src.Len())
	if err != nil {
		t.Fatal(err)
	}

	e := NewEvent(src.ByName("player_death"))
	e.Set("userid", Value{Int: -2})
	e.Set("attacker", Value{Int: 1234})
	e.Set("weapon", Value{String: "tf_projectile_rocket"})
	e.Set("crit", Value{Bool: true})
	e.Set("damage", Value{Int: -100000})
	e.Set("customkill", Value{Int: 200})
	e.Set("distance", Value{Float: 512.5})
	if e.Set("nope", Value{}) {
		t.Error("unknown key accepted")
	}

	ew := bitbuf.NewWriter(0)
	if err := src.Encode(ew, e); err != nil {
		t.Fatal(err)
	}
	r := ew.Reader()
	got, err := recv.Decode(r)
	if err != nil {
		t.Fatal(err)
	}
	if r.BitsLeft() != 0 {
		t.Fatalf("%d bits left", r.BitsLeft())
	}
	if got.Name() != "player_death" || got.Int("userid") != -2 || got.Int("attacker") != 1234 ||
		got.String("weapon") != "tf_projectile_rocket" || !got.Bool("crit") || got.Int("crit") != 1 ||
		got.Int("damage") != -100000 || got.Int("customkill") != 200 || got.Float("distance") != 512.5 ||
		got.Float("damage") != -100000 || got.Int("distance") != 512 {
		t.Fatalf("decoded %+v", got.Values)
	}
	if got.Has("clientonly") || !got.Has("weapon") || got.Int("missing") != 0 || got.String("missing") != "" {
		t.Error("key presence")
	}
}

func TestDecodeErrors(t *testing.T) {
	l := sampleList()
	w := bitbuf.NewWriter(0)
	w.WriteBits(42, 9)
	if _, err := l.Decode(w.Reader()); err == nil {
		t.Error("unknown event id accepted")
	}
	w = bitbuf.NewWriter(0)
	w.WriteBits(300, 9)
	w.WriteBits(1, 8) // truncated long
	if _, err := l.Decode(w.Reader()); err == nil {
		t.Error("truncated event accepted")
	}
	if err := l.Encode(bitbuf.NewWriter(0), NewEvent(&Descriptor{ID: 7, Name: "other"})); err == nil {
		t.Error("encoding an event outside the list accepted")
	}
}

func TestListenMask(t *testing.T) {
	l := sampleList()
	m := l.ListenMask("round_start", "player_death", "unknown")
	if m[0] != 1<<3 || m[300/32] != 1<<(300%32) || m[15] != 0 {
		t.Fatalf("mask %v", m)
	}
	all := l.ListenMask()
	if all[0] != 1<<3 || all[9] != 1<<12 || all[15] != 1<<31 {
		t.Fatalf("all mask %v", all)
	}
}
