package datatable

import (
	"bytes"
	"math"
	"reflect"
	"testing"

	"github.com/galaco/sourcenet/bitbuf"
)

// roundTrip encodes v with p, checks SkipProp consumes the same bits and
// returns the decoded value.
func roundTrip(t *testing.T, p *SendProp, v Value) Value {
	t.Helper()
	w := bitbuf.NewWriter(0)
	w.WriteBits(5, 3) // misalign
	EncodeProp(w, p, v)
	n := w.BitsWritten() - 3

	r := w.Reader()
	r.ReadBits(3)
	got := DecodeProp(r, p)
	if r.Err() != nil || r.BitsLeft() != 0 {
		t.Fatalf("%s: decode err=%v left=%d", p.Name, r.Err(), r.BitsLeft())
	}
	r = w.Reader()
	r.ReadBits(3)
	SkipProp(r, p)
	if r.Pos()-3 != n {
		t.Fatalf("%s: skip consumed %d bits, encode wrote %d", p.Name, r.Pos()-3, n)
	}
	return got
}

func TestIntProps(t *testing.T) {
	cases := []struct {
		p    *SendProp
		in   int64
		want int64
	}{
		{intProp("signed5", 5, 0), -3, -3},
		{intProp("signed5max", 5, 0), 15, 15},
		{intProp("unsigned8", 8, FlagUnsigned), 255, 255},
		{intProp("unsigned32", 32, FlagUnsigned), 0xFFFFFFFF, 0xFFFFFFFF},
		{intProp("signed32", 32, 0), -123456789, -123456789},
		{intProp("varint", 0, FlagVarInt|FlagUnsigned), 300, 300},
		{intProp("svarint", 0, FlagVarInt), -100, -100},
		{intProp("svarintmin", 0, FlagVarInt), math.MinInt32, math.MinInt32},
	}
	for _, c := range cases {
		if got := roundTrip(t, c.p, Value{Int: c.in}); got.Int != c.want {
			t.Errorf("%s: %d -> %d", c.p.Name, c.in, got.Int)
		}
	}
	// Signed 5 bit -3 is 0b11101 on the wire.
	w := bitbuf.NewWriter(0)
	EncodeProp(w, intProp("x", 5, 0), Value{Int: -3})
	if w.Bytes()[0] != 0x1d {
		t.Fatalf("wire %#x", w.Bytes()[0])
	}
	w = bitbuf.NewWriter(0)
	EncodeProp(w, intProp("x", 0, FlagVarInt|FlagUnsigned), Value{Int: 300})
	if !bytes.Equal(w.Bytes(), []byte{0xac, 0x02}) {
		t.Fatalf("varint wire % x", w.Bytes())
	}
}

func TestRangedFloat(t *testing.T) {
	p := &SendProp{Type: Float, Name: "f", NumBits: 10, LowValue: 0, HighValue: 100}
	// Decoding is low + (high-low) * (raw / (2^bits - 1)) in single precision.
	for _, raw := range []uint32{0, 1, 512, 1023} {
		w := bitbuf.NewWriter(0)
		w.WriteBits(raw, 10)
		got := DecodeProp(w.Reader(), p).Float
		want := float32(100) * (float32(raw) / 1023)
		if got != want {
			t.Errorf("raw %d -> %v want %v", raw, got, want)
		}
	}
	if got := roundTrip(t, p, Value{Float: 100}).Float; got != 100 {
		t.Errorf("high end -> %v", got)
	}
	if got := roundTrip(t, p, Value{Float: 0}).Float; got != 0 {
		t.Errorf("low end -> %v", got)
	}
	// Out of range values clamp.
	if got := roundTrip(t, p, Value{Float: 1e6}).Float; got != 100 {
		t.Errorf("clamp high -> %v", got)
	}
	if got := roundTrip(t, p, Value{Float: -5}).Float; got != 0 {
		t.Errorf("clamp low -> %v", got)
	}
	// Quantisation error is bounded by one step.
	for _, f := range []float32{12.34, 50, 99.9} {
		if got := roundTrip(t, p, Value{Float: f}).Float; math.Abs(float64(got-f)) > 100.0/1023 {
			t.Errorf("%v -> %v", f, got)
		}
	}
	small := &SendProp{Type: Float, Name: "small", NumBits: 2, LowValue: -1, HighValue: 1}
	w := bitbuf.NewWriter(0)
	w.WriteBits(1, 2)
	if got := DecodeProp(w.Reader(), small).Float; got != float32(-1)+float32(2)*(float32(1)/3) {
		t.Errorf("2-bit ranged float -> %v", got)
	}
}

func TestSpecialFloats(t *testing.T) {
	cases := []struct {
		p  *SendProp
		in []float32
	}{
		{&SendProp{Type: Float, Name: "coord", Flags: FlagCoord}, []float32{0, 1.5, -4095.25, 0.03125}},
		{&SendProp{Type: Float, Name: "coordmp", Flags: FlagCoordMP}, []float32{0, 2047.5, -3000.25, 0.125}},
		{&SendProp{Type: Float, Name: "coordmplow", Flags: FlagCoordMPLowPrecision}, []float32{0, 100.125, -7.875}},
		{&SendProp{Type: Float, Name: "coordmpint", Flags: FlagCoordMPIntegral}, []float32{0, 1, -2048, 9000}},
		{&SendProp{Type: Float, Name: "noscale", Flags: FlagNoScale}, []float32{3.14159, -1e30, float32(math.Inf(1))}},
		{&SendProp{Type: Float, Name: "normal", Flags: FlagNormal}, []float32{1, -1, 0}},
	}
	for _, c := range cases {
		for _, f := range c.in {
			if got := roundTrip(t, c.p, Value{Float: f}).Float; got != f {
				t.Errorf("%s: %v -> %v", c.p.Name, f, got)
			}
		}
	}
	// Integral coordMP flags take precedence in decoding even with the plain
	// coordMP bit set.
	both := &SendProp{Type: Float, Name: "mpint", Flags: FlagCoordMP | FlagCoordMPIntegral}
	if got := roundTrip(t, both, Value{Float: 42}).Float; got != 42 {
		t.Errorf("coordMP|integral -> %v", got)
	}
}

func TestVectors(t *testing.T) {
	coord := &SendProp{Type: Vector, Name: "v", Flags: FlagCoord}
	v := [3]float32{1, -2.5, 4095}
	if got := roundTrip(t, coord, Value{Vector: v}).Vector; got != v {
		t.Errorf("coord vector -> %v", got)
	}
	normal := &SendProp{Type: Vector, Name: "n", Flags: FlagNormal}
	got := roundTrip(t, normal, Value{Vector: [3]float32{0.6, 0, -0.8}}).Vector
	if math.Abs(float64(got[0]-0.6)) > 1e-3 || got[1] != 0 || math.Abs(float64(got[2]+0.8)) > 1e-3 {
		t.Errorf("normal vector -> %v", got)
	}
	xy := &SendProp{Type: VectorXY, Name: "xy", NumBits: 8, LowValue: -1, HighValue: 1}
	gotXY := roundTrip(t, xy, Value{Vector: [3]float32{1, -1, 99}}).Vector
	if gotXY != [3]float32{1, -1, 0} {
		t.Errorf("vectorxy -> %v", gotXY)
	}
}

func TestStringAndArray(t *testing.T) {
	sp := &SendProp{Type: String, Name: "s"}
	w := bitbuf.NewWriter(0)
	EncodeProp(w, sp, Value{String: "hello"})
	if w.BitsWritten() != 9+5*8 {
		t.Fatalf("string used %d bits", w.BitsWritten())
	}
	if got := roundTrip(t, sp, Value{String: "hello"}).String; got != "hello" {
		t.Errorf("string -> %q", got)
	}
	long := string(bytes.Repeat([]byte("x"), 600))
	if got := roundTrip(t, sp, Value{String: long}).String; len(got) != 511 {
		t.Errorf("long string truncated to %d", len(got))
	}

	elem := intProp("e", 4, FlagUnsigned)
	arr := &SendProp{Type: Array, Name: "a", NumElements: 5, ArrayElement: elem}
	in := Value{Array: []Value{{Int: 1}, {Int: 2}, {Int: 15}}}
	w = bitbuf.NewWriter(0)
	EncodeProp(w, arr, in)
	if w.BitsWritten() != 3+3*4 { // count uses Q_log2(5)+1 = 3 bits
		t.Fatalf("array used %d bits", w.BitsWritten())
	}
	if got := roundTrip(t, arr, in); !reflect.DeepEqual(got.Array, in.Array) {
		t.Errorf("array -> %+v", got.Array)
	}

	i64 := &SendProp{Type: Int64, Name: "i64", NumBits: 40}
	if got := roundTrip(t, i64, Value{Int: -(1<<38 + 5)}).Int; got != -(1<<38 + 5) {
		t.Errorf("int64 -> %d", got)
	}
	u64 := &SendProp{Type: Int64, Name: "u64", NumBits: 48, Flags: FlagUnsigned}
	if got := roundTrip(t, u64, Value{Int: 1<<47 + 9}).Int; got != 1<<47+9 {
		t.Errorf("uint64 -> %d", got)
	}
}

func TestDecodePropsDelta(t *testing.T) {
	tables := mustLink(t, testTables())
	c := tables.ClassByName("CLeaf")
	src := make([]Value, len(c.Props))
	src[c.PropIndex("m_b")] = Value{Int: 200}
	src[c.PropIndex("m_e")] = Value{String: "name"}
	src[c.PropIndex("m_arr")] = Value{Array: []Value{{Int: 3}, {Int: 4}}}
	src[c.PropIndex("sub.m_s1")] = Value{Int: 6}
	indices := []int{c.PropIndex("m_b"), c.PropIndex("m_e"), c.PropIndex("m_arr"), c.PropIndex("sub.m_s1")}

	w := bitbuf.NewWriter(0)
	EncodeProps(w, c, src, indices)
	dst := make([]Value, len(c.Props))
	dst[c.PropIndex("m_c")] = Value{Float: 7} // untouched by the delta
	changed, err := DecodeProps(w.Reader(), c, dst, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(changed, indices) {
		t.Fatalf("changed %v want %v", changed, indices)
	}
	for _, i := range indices {
		if !reflect.DeepEqual(dst[i], src[i]) {
			t.Errorf("prop %s = %+v want %+v", c.Props[i].Name, dst[i], src[i])
		}
	}
	if dst[c.PropIndex("m_c")].Float != 7 {
		t.Error("delta overwrote an unchanged prop")
	}

	// Out of range indices are rejected.
	w = bitbuf.NewWriter(0)
	WritePropIndex(w, -1, len(c.Props))
	WritePropListEnd(w)
	if _, err := DecodeProps(w.Reader(), c, dst, nil); err == nil {
		t.Fatal("expected out of range error")
	}
}
