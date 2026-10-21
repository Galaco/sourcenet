package bitbuf

import (
	"math"
	"slices"
)

// Writer writes bit-packed data into a growable buffer. The zero value is an
// empty, unlimited writer ready for use.
type Writer struct {
	buf   []byte
	pos   int // cursor in bits; also the number of bits written
	limit int // maximum number of bits, 0 for unlimited
	err   error
}

// NewWriter returns an unlimited writer with an initial capacity in bytes.
func NewWriter(capacity int) *Writer {
	return &Writer{buf: make([]byte, 0, capacity)}
}

// NewLimitedWriter returns a writer that overflows once more than maxBytes
// bytes would be written, like a fixed-size bf_write. Memory is allocated as
// the buffer grows, not up front.
func NewLimitedWriter(maxBytes int) *Writer {
	return &Writer{buf: make([]byte, 0, min(maxBytes, 2048)), limit: maxBytes * 8}
}

// Err returns ErrOverflow if a write exceeded the writer's limit.
func (w *Writer) Err() error { return w.err }

// Overflowed reports whether a write exceeded the writer's limit.
func (w *Writer) Overflowed() bool { return w.err != nil }

// BitsWritten returns the cursor position in bits.
func (w *Writer) BitsWritten() int { return w.pos }

// BytesWritten returns the number of bytes touched by the written bits.
func (w *Writer) BytesWritten() int { return BitsToBytes(w.pos) }

// BitsLeft returns the remaining capacity in bits, or a very large number for
// unlimited writers.
func (w *Writer) BitsLeft() int {
	if w.limit == 0 {
		return math.MaxInt32
	}
	return w.limit - w.pos
}

// Bytes returns the written data. The final byte may be partially filled; its
// unused high bits are zero unless they were previously written and the
// cursor was moved back with Seek.
func (w *Writer) Bytes() []byte {
	return w.buf[:BitsToBytes(w.pos)]
}

// Reset discards all written data but keeps the allocated buffer.
func (w *Writer) Reset() {
	w.buf = w.buf[:0]
	w.pos = 0
	w.err = nil
}

// Seek moves the cursor to an absolute bit position that has already been
// written (or to the end). Subsequent writes overwrite existing bits.
func (w *Writer) Seek(bit int) {
	if bit < 0 {
		bit = 0
	}
	if bit > len(w.buf)*8 {
		bit = len(w.buf) * 8
	}
	w.pos = bit
}

// Reader returns a reader over the bits written so far.
func (w *Writer) Reader() *Reader {
	return NewReaderBits(w.Bytes(), w.pos)
}

func (w *Writer) reserve(n int) bool {
	if w.err != nil {
		return false
	}
	if w.limit != 0 && w.pos+n > w.limit {
		w.pos = w.limit
		w.err = ErrOverflow
		return false
	}
	if need, have := BitsToBytes(w.pos+n), len(w.buf); need > have {
		// Bytes past len may hold data from before a Reset; clear them
		// because setBits merges into existing bits.
		w.buf = slices.Grow(w.buf, need-have)[:need]
		clear(w.buf[have:])
	}
	return true
}

// setBits stores the low n bits of v at absolute bit position pos.
func (w *Writer) setBits(pos int, v uint64, n int) {
	for n > 0 {
		idx := pos >> 3
		off := uint(pos & 7)
		take := 8 - int(off)
		if take > n {
			take = n
		}
		mask := byte((1<<uint(take) - 1) << off)
		w.buf[idx] = w.buf[idx]&^mask | byte(v<<off)&mask
		v >>= uint(take)
		n -= take
		pos += take
	}
}

// WriteBits writes the low n bits of v (0 <= n <= 32).
func (w *Writer) WriteBits(v uint32, n int) {
	if n <= 0 {
		return
	}
	if n > 32 {
		panic("bitbuf: WriteBits supports at most 32 bits")
	}
	if !w.reserve(n) {
		return
	}
	w.setBits(w.pos, uint64(v), n)
	w.pos += n
}

// WriteBitsAt overwrites n bits at an absolute position that has already been
// written, without moving the cursor. Used to patch headers.
func (w *Writer) WriteBitsAt(pos int, v uint32, n int) {
	if pos < 0 || pos+n > len(w.buf)*8 {
		w.err = ErrOverflow
		return
	}
	w.setBits(pos, uint64(v), n)
}

// WriteSBits writes a signed value as n bits.
func (w *Writer) WriteSBits(v int32, n int) {
	w.WriteBits(uint32(v), n)
}

// WriteBit writes a single bit.
func (w *Writer) WriteBit(b bool) {
	if b {
		w.WriteBits(1, 1)
	} else {
		w.WriteBits(0, 1)
	}
}

// WriteUint8 writes a byte.
func (w *Writer) WriteUint8(v uint8) { w.WriteBits(uint32(v), 8) }

// WriteInt8 writes a signed byte.
func (w *Writer) WriteInt8(v int8) { w.WriteBits(uint32(uint8(v)), 8) }

// WriteUint16 writes a 16-bit value.
func (w *Writer) WriteUint16(v uint16) { w.WriteBits(uint32(v), 16) }

// WriteInt16 writes a signed 16-bit value.
func (w *Writer) WriteInt16(v int16) { w.WriteBits(uint32(uint16(v)), 16) }

// WriteUint32 writes a 32-bit value.
func (w *Writer) WriteUint32(v uint32) { w.WriteBits(v, 32) }

// WriteInt32 writes a signed 32-bit value.
func (w *Writer) WriteInt32(v int32) { w.WriteBits(uint32(v), 32) }

// WriteUint64 writes a 64-bit value, low 32 bits first.
func (w *Writer) WriteUint64(v uint64) {
	w.WriteBits(uint32(v), 32)
	w.WriteBits(uint32(v>>32), 32)
}

// WriteInt64 writes a signed 64-bit value.
func (w *Writer) WriteInt64(v int64) { w.WriteUint64(uint64(v)) }

// WriteFloat32 writes a raw IEEE-754 float.
func (w *Writer) WriteFloat32(v float32) { w.WriteBits(math.Float32bits(v), 32) }

// WriteUBitVar writes the engine's variable-width unsigned integer encoding.
func (w *Writer) WriteUBitVar(v uint32) {
	switch {
	case v < 0x10:
		w.WriteBits(0, 2)
		w.WriteBits(v, 4)
	case v < 0x100:
		w.WriteBits(1, 2)
		w.WriteBits(v, 8)
	case v < 0x1000:
		w.WriteBits(2, 2)
		w.WriteBits(v, 12)
	default:
		w.WriteBits(3, 2)
		w.WriteBits(v, 32)
	}
}

// WriteVarInt32 writes a protobuf-style base-128 varint.
func (w *Writer) WriteVarInt32(v uint32) {
	for v > 0x7f {
		w.WriteBits(v&0x7f|0x80, 8)
		v >>= 7
	}
	w.WriteBits(v&0x7f, 8)
}

// WriteSignedVarInt32 writes a zig-zag encoded varint.
func (w *Writer) WriteSignedVarInt32(v int32) {
	w.WriteVarInt32(uint32(v<<1) ^ uint32(v>>31))
}

// WriteVarInt64 writes a protobuf-style base-128 varint.
func (w *Writer) WriteVarInt64(v uint64) {
	for v > 0x7f {
		w.WriteBits(uint32(v&0x7f|0x80), 8)
		v >>= 7
	}
	w.WriteBits(uint32(v&0x7f), 8)
}

// WriteSignedVarInt64 writes a zig-zag encoded 64-bit varint.
func (w *Writer) WriteSignedVarInt64(v int64) {
	w.WriteVarInt64(uint64(v<<1) ^ uint64(v>>63))
}

// WriteString writes s followed by a NUL terminator. Writing stops at the
// first embedded NUL, as in the engine.
func (w *Writer) WriteString(s string) {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			break
		}
		w.WriteBits(uint32(s[i]), 8)
	}
	w.WriteBits(0, 8)
}

// WriteBytes writes raw bytes.
func (w *Writer) WriteBytes(b []byte) {
	if len(b) == 0 {
		return
	}
	if w.pos&7 == 0 {
		if !w.reserve(len(b) * 8) {
			return
		}
		copy(w.buf[w.pos>>3:], b)
		w.pos += len(b) * 8
		return
	}
	for _, c := range b {
		w.WriteBits(uint32(c), 8)
	}
}

// WriteBitsFromBytes writes the first nbits bits of b.
func (w *Writer) WriteBitsFromBytes(b []byte, nbits int) {
	if nbits > len(b)*8 {
		nbits = len(b) * 8
	}
	full := nbits >> 3
	w.WriteBytes(b[:full])
	if rem := nbits & 7; rem != 0 {
		w.WriteBits(uint32(b[full]), rem)
	}
}

// WriteFromReader copies nbits bits from r.
func (w *Writer) WriteFromReader(r *Reader, nbits int) {
	for nbits > 32 {
		w.WriteBits(r.ReadBits(32), 32)
		nbits -= 32
	}
	w.WriteBits(r.ReadBits(nbits), nbits)
}

// WriteBitCoord writes a world coordinate (bf_write::WriteBitCoord).
func (w *Writer) WriteBitCoord(f float32) {
	sign := f <= -CoordResolution
	intval := int(math.Abs(float64(f)))
	fracval := absInt(int(f*CoordDenominator)) & (CoordDenominator - 1)
	w.WriteBit(intval != 0)
	w.WriteBit(fracval != 0)
	if intval == 0 && fracval == 0 {
		return
	}
	w.WriteBit(sign)
	if intval != 0 {
		w.WriteBits(uint32(intval-1), CoordIntegerBits)
	}
	if fracval != 0 {
		w.WriteBits(uint32(fracval), CoordFractionalBits)
	}
}

// WriteBitCoordMP writes a multiplayer-optimised world coordinate
// (bf_write::WriteBitCoordMP).
func (w *Writer) WriteBitCoordMP(f float32, integral, lowPrecision bool) {
	res := float32(CoordResolution)
	if lowPrecision {
		res = CoordResolutionLowP
	}
	sign := uint32(0)
	if f <= -res {
		sign = 1
	}
	intval := int(math.Abs(float64(f)))
	var fracval int
	if lowPrecision {
		fracval = absInt(int(f*CoordDenominatorLowP)) & (CoordDenominatorLowP - 1)
	} else {
		fracval = absInt(int(f*CoordDenominator)) & (CoordDenominator - 1)
	}
	inBounds := uint32(0)
	if intval < 1<<CoordIntegerBitsMP {
		inBounds = 1
	}
	intBits := CoordIntegerBits
	if inBounds != 0 {
		intBits = CoordIntegerBitsMP
	}
	fracBits := CoordFractionalBits
	if lowPrecision {
		fracBits = CoordFractionalBitsMPLowP
	}

	var bits uint64
	var nbits int
	if integral {
		if intval != 0 {
			bits = uint64(intval-1)*8 + uint64(sign)*4 + 2 + uint64(inBounds)
			nbits = 3 + intBits
		} else {
			bits = uint64(inBounds)
			nbits = 2
		}
	} else {
		if intval != 0 {
			bits = uint64(intval-1)*8 + uint64(sign)*4 + 2 + uint64(inBounds)
			bits += uint64(fracval) << uint(3+intBits)
			nbits = 3 + intBits + fracBits
		} else {
			bits = uint64(fracval)*8 + uint64(sign)*4 + uint64(inBounds)
			nbits = 3 + fracBits
		}
	}
	if !w.reserve(nbits) {
		return
	}
	w.setBits(w.pos, bits, nbits)
	w.pos += nbits
}

// WriteBitNormal writes a normal component in [-1, 1].
func (w *Writer) WriteBitNormal(f float32) {
	frac := absInt(int(f * NormalDenominator))
	if frac > NormalDenominator {
		frac = NormalDenominator
	}
	w.WriteBit(f <= -NormalResolution)
	w.WriteBits(uint32(frac), NormalFractionalBits)
}

// WriteBitVec3Coord writes a vector of world coordinates, omitting zero
// components.
func (w *Writer) WriteBitVec3Coord(v [3]float32) {
	var flags [3]bool
	for i, c := range v {
		flags[i] = c >= CoordResolution || c <= -CoordResolution
		w.WriteBit(flags[i])
	}
	for i, c := range v {
		if flags[i] {
			w.WriteBitCoord(c)
		}
	}
}

// WriteBitVec3Normal writes a unit vector; Z is reconstructed by the reader.
func (w *Writer) WriteBitVec3Normal(v [3]float32) {
	hasX := v[0] >= NormalResolution || v[0] <= -NormalResolution
	hasY := v[1] >= NormalResolution || v[1] <= -NormalResolution
	w.WriteBit(hasX)
	w.WriteBit(hasY)
	if hasX {
		w.WriteBitNormal(v[0])
	}
	if hasY {
		w.WriteBitNormal(v[1])
	}
	w.WriteBit(v[2] <= -NormalResolution)
}

// WriteBitAngle writes an angle in degrees quantised to nbits bits.
func (w *Writer) WriteBitAngle(angle float32, nbits int) {
	shift := uint64(1) << uint(nbits)
	d := int64(float64(angle) / 360.0 * float64(shift))
	w.WriteBits(uint32(d)&uint32(shift-1), nbits)
}

// WriteBitAngles writes three angles encoded as world coordinates.
func (w *Writer) WriteBitAngles(v [3]float32) {
	w.WriteBitVec3Coord(v)
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
