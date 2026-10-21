package bitbuf

import (
	"bytes"
	"math"
	"math/rand"
	"testing"
)

func TestBitOrderIsLSBFirst(t *testing.T) {
	w := NewWriter(4)
	w.WriteBits(1, 1)
	w.WriteBits(0, 1)
	w.WriteBits(3, 2)
	w.WriteBits(0xA, 4)
	w.WriteBits(0x1FF, 9)
	if got, want := w.Bytes(), []byte{0xAD, 0xFF, 0x01}; !bytes.Equal(got, want) {
		t.Fatalf("bytes = % x, want % x", got, want)
	}
	r := NewReader(w.Bytes())
	if r.ReadBits(1) != 1 || r.ReadBits(1) != 0 || r.ReadBits(2) != 3 || r.ReadBits(4) != 0xA || r.ReadBits(9) != 0x1FF {
		t.Fatal("read back mismatch")
	}
}

func TestReadBitsRandomRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	type item struct {
		v uint32
		n int
	}
	var items []item
	w := NewWriter(0)
	for i := 0; i < 5000; i++ {
		n := rng.Intn(33)
		v := rng.Uint32()
		if n < 32 {
			v &= 1<<uint(n) - 1
		}
		items = append(items, item{v, n})
		w.WriteBits(v, n)
	}
	r := w.Reader()
	for i, it := range items {
		if got := r.ReadBits(it.n); got != it.v {
			t.Fatalf("item %d: got %#x want %#x (n=%d)", i, got, it.v, it.n)
		}
	}
	if r.BitsLeft() != 0 || r.Err() != nil {
		t.Fatalf("bits left %d err %v", r.BitsLeft(), r.Err())
	}
}

func TestSignedBits(t *testing.T) {
	w := NewWriter(0)
	for _, v := range []int32{-1, -64, 63, 0, -32768} {
		w.WriteSBits(v, 16)
	}
	w.WriteSBits(-3, 3)
	r := w.Reader()
	for _, want := range []int32{-1, -64, 63, 0, -32768, -3} {
		n := 16
		if want == -3 {
			n = 3
		}
		if got := r.ReadSBits(n); got != want {
			t.Fatalf("got %d want %d", got, want)
		}
	}
}

func TestUBitVar(t *testing.T) {
	values := []uint32{0, 1, 15, 16, 255, 256, 4095, 4096, 0xFFFF, 0x10000, math.MaxUint32}
	widths := []int{6, 6, 6, 10, 10, 14, 14, 34, 34, 34, 34}
	for i, v := range values {
		w := NewWriter(0)
		w.WriteBit(true) // misalign
		w.WriteUBitVar(v)
		if got := w.BitsWritten() - 1; got != widths[i] {
			t.Errorf("value %d encoded in %d bits, want %d", v, got, widths[i])
		}
		r := w.Reader()
		r.ReadBit()
		if got := r.ReadUBitVar(); got != v {
			t.Errorf("UBitVar round trip %d -> %d", v, got)
		}
	}
}

// TestUBitVarMatchesEngineFormula checks the writer against the engine's
// single-call encoding: WriteUBitLong(data*4 + n + 3, 6 + n*4 + 12).
func TestUBitVarMatchesEngineFormula(t *testing.T) {
	for _, v := range []uint32{3, 0x42, 0x987} {
		n := 0
		if v < 0x10 {
			n--
		}
		if v < 0x100 {
			n--
		}
		if v < 0x1000 {
			n--
		}
		ref := NewWriter(0)
		ref.WriteBits(uint32(int(v)*4+n+3), 6+n*4+12)
		got := NewWriter(0)
		got.WriteUBitVar(v)
		if !bytes.Equal(ref.Bytes(), got.Bytes()) || ref.BitsWritten() != got.BitsWritten() {
			t.Errorf("value %#x: % x != engine % x", v, got.Bytes(), ref.Bytes())
		}
	}
}

func TestVarInt(t *testing.T) {
	w := NewWriter(0)
	w.WriteBit(false)
	u32 := []uint32{0, 1, 127, 128, 300, 1 << 21, math.MaxUint32}
	s32 := []int32{0, -1, 1, -64, 64, math.MinInt32, math.MaxInt32}
	u64 := []uint64{0, 1 << 40, math.MaxUint64}
	for _, v := range u32 {
		w.WriteVarInt32(v)
	}
	for _, v := range s32 {
		w.WriteSignedVarInt32(v)
	}
	for _, v := range u64 {
		w.WriteVarInt64(v)
	}
	w.WriteSignedVarInt64(math.MinInt64)
	r := w.Reader()
	r.ReadBit()
	for _, v := range u32 {
		if got := r.ReadVarInt32(); got != v {
			t.Errorf("varint32 %d -> %d", v, got)
		}
	}
	for _, v := range s32 {
		if got := r.ReadSignedVarInt32(); got != v {
			t.Errorf("signed varint32 %d -> %d", v, got)
		}
	}
	for _, v := range u64 {
		if got := r.ReadVarInt64(); got != v {
			t.Errorf("varint64 %d -> %d", v, got)
		}
	}
	if got := r.ReadSignedVarInt64(); got != math.MinInt64 {
		t.Errorf("signed varint64 -> %d", got)
	}
	// 300 is the canonical protobuf example: ac 02.
	w2 := NewWriter(0)
	w2.WriteVarInt32(300)
	if !bytes.Equal(w2.Bytes(), []byte{0xac, 0x02}) {
		t.Errorf("varint 300 = % x", w2.Bytes())
	}
}

func TestStringsAndBytes(t *testing.T) {
	for _, offset := range []int{0, 3} {
		w := NewWriter(0)
		w.WriteBits(0, offset)
		w.WriteString("hello")
		w.WriteString("")
		w.WriteBytes([]byte{1, 2, 3})
		w.WriteUint64(0x0102030405060708)
		w.WriteFloat32(1.5)
		r := w.Reader()
		r.ReadBits(offset)
		if s := r.ReadString(); s != "hello" {
			t.Fatalf("offset %d: string %q", offset, s)
		}
		if s := r.ReadString(); s != "" {
			t.Fatalf("offset %d: empty string %q", offset, s)
		}
		if b := r.ReadBytes(3); !bytes.Equal(b, []byte{1, 2, 3}) {
			t.Fatalf("offset %d: bytes % x", offset, b)
		}
		if v := r.ReadUint64(); v != 0x0102030405060708 {
			t.Fatalf("offset %d: uint64 %#x", offset, v)
		}
		if f := r.ReadFloat32(); f != 1.5 {
			t.Fatalf("offset %d: float %v", offset, f)
		}
		if r.Err() != nil {
			t.Fatal(r.Err())
		}
	}
}

func TestUint64IsLowDwordFirst(t *testing.T) {
	w := NewWriter(0)
	w.WriteUint64(0x1122334455667788)
	if !bytes.Equal(w.Bytes(), []byte{0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11}) {
		t.Fatalf("% x", w.Bytes())
	}
}

func TestOverflowIsSticky(t *testing.T) {
	r := NewReader([]byte{0xff})
	if r.ReadBits(4) != 0xf {
		t.Fatal("first read")
	}
	if v := r.ReadBits(8); v != 0 {
		t.Fatalf("overflowing read returned %d", v)
	}
	if !r.Overflowed() || r.BitsLeft() != 0 {
		t.Fatal("expected overflow state")
	}
	if r.ReadBits(1) != 0 || r.Err() != ErrOverflow {
		t.Fatal("overflow must be sticky")
	}

	w := NewLimitedWriter(1)
	w.WriteBits(0, 6)
	w.WriteBits(0, 3)
	if !w.Overflowed() {
		t.Fatal("writer should overflow")
	}
}

func TestUnterminatedString(t *testing.T) {
	r := NewReader([]byte("abc"))
	if s := r.ReadString(); s != "abc" || !r.Overflowed() {
		t.Fatalf("got %q overflow=%v", s, r.Overflowed())
	}
	r = NewReaderBits([]byte{0x61 << 1, 0x62 << 1, 0}, 20)
	r.ReadBit()
	if s := r.ReadString(); s != "ab" || !r.Overflowed() {
		t.Fatalf("unaligned: got %q overflow=%v", s, r.Overflowed())
	}
}

func TestSubReader(t *testing.T) {
	w := NewWriter(0)
	w.WriteBits(5, 3)
	w.WriteBits(0xABC, 12)
	w.WriteBits(1, 1)
	r := w.Reader()
	r.ReadBits(3)
	sub := r.SubReader(12)
	if r.Pos() != 15 {
		t.Fatalf("parent pos %d", r.Pos())
	}
	if sub.Len() != 12 || sub.ReadBits(12) != 0xABC || sub.BitsLeft() != 0 {
		t.Fatal("sub reader contents")
	}
	if sub.ReadBits(1) != 0 || !sub.Overflowed() {
		t.Fatal("sub reader must be bounded")
	}
	if r.ReadBits(1) != 1 {
		t.Fatal("parent continues after sub reader")
	}
	if err := sub.Seek(4); err != nil || sub.ReadBits(4) != 0xB {
		t.Fatal("seek in sub reader is relative")
	}
}

func TestReadBitsToBytes(t *testing.T) {
	w := NewWriter(0)
	w.WriteBits(1, 1)
	w.WriteBitsFromBytes([]byte{0xde, 0xad, 0x05}, 19)
	r := w.Reader()
	r.ReadBit()
	got := r.ReadBitsToBytes(19)
	if !bytes.Equal(got, []byte{0xde, 0xad, 0x05}) {
		t.Fatalf("% x", got)
	}
}

func TestWriteBitsAtPatches(t *testing.T) {
	w := NewWriter(0)
	w.WriteUint32(0)
	pos := w.BitsWritten()
	w.WriteUint8(0)
	w.WriteUint16(0xBEEF)
	w.WriteBitsAt(pos, 0x7F, 8)
	r := w.Reader()
	r.ReadUint32()
	if r.ReadUint8() != 0x7F || r.ReadUint16() != 0xBEEF {
		t.Fatal("patch failed")
	}
}

func TestBitCoord(t *testing.T) {
	cases := []float32{0, 1, -1, 0.03125, -0.5, 123.25, -4095.96875, 16383.96875}
	w := NewWriter(0)
	for _, c := range cases {
		w.WriteBitCoord(c)
	}
	r := w.Reader()
	for _, c := range cases {
		if got := r.ReadBitCoord(); got != c {
			t.Errorf("coord %v -> %v", c, got)
		}
	}
}

func TestBitCoordMP(t *testing.T) {
	cases := []float32{0, 1, -1, 2047, -2047, 2048, -9000.5, 0.25, -0.125, 15.875}
	for _, integral := range []bool{false, true} {
		for _, low := range []bool{false, true} {
			w := NewWriter(0)
			for _, c := range cases {
				w.WriteBitCoordMP(c, integral, low)
			}
			r := w.Reader()
			for _, c := range cases {
				want := c
				if integral {
					want = float32(int(c))
				}
				got := r.ReadBitCoordMP(integral, low)
				if got != want {
					t.Errorf("integral=%v low=%v: %v -> %v", integral, low, c, got)
				}
			}
			if r.BitsLeft() != 0 {
				t.Errorf("integral=%v low=%v: %d bits left", integral, low, r.BitsLeft())
			}
		}
	}
}

func TestBitNormalAndVectors(t *testing.T) {
	w := NewWriter(0)
	w.WriteBitNormal(1)
	w.WriteBitNormal(-1)
	w.WriteBitNormal(0.5)
	n := [3]float32{0.6, 0, -0.8}
	w.WriteBitVec3Normal(n)
	c := [3]float32{10, 0, -2.5}
	w.WriteBitVec3Coord(c)
	r := w.Reader()
	if r.ReadBitNormal() != 1 || r.ReadBitNormal() != -1 {
		t.Fatal("normal extremes")
	}
	if got := r.ReadBitNormal(); math.Abs(float64(got-0.5)) > NormalResolution {
		t.Fatalf("normal 0.5 -> %v", got)
	}
	got := r.ReadBitVec3Normal()
	for i := range n {
		if math.Abs(float64(got[i]-n[i])) > 0.002 {
			t.Fatalf("vec3 normal %v -> %v", n, got)
		}
	}
	if v := r.ReadBitVec3Coord(); v != c {
		t.Fatalf("vec3 coord %v -> %v", c, v)
	}
}

func TestBitAngle(t *testing.T) {
	w := NewWriter(0)
	w.WriteBitAngle(90, 16)
	w.WriteBitAngle(359.9, 8)
	r := w.Reader()
	if got := r.ReadBitAngle(16); got != 90 {
		t.Fatalf("angle 90 -> %v", got)
	}
	if got := r.ReadBitAngle(8); math.Abs(float64(got-358.59375)) > 1e-4 {
		t.Fatalf("angle 359.9 -> %v", got)
	}
}

func TestWriterResetDoesNotLeakOldBits(t *testing.T) {
	w := NewLimitedWriter(64)
	w.WriteBytes(bytes.Repeat([]byte{0xff}, 16))
	w.Reset()
	w.WriteBits(0, 3)
	w.WriteBits(1, 1)
	w.WriteBytes(make([]byte, 4))
	if got := w.Bytes(); !bytes.Equal(got, []byte{0x08, 0, 0, 0, 0}) {
		t.Fatalf("stale bits after reset: % x", got)
	}
}
