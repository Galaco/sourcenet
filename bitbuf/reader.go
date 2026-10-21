package bitbuf

import (
	"encoding/binary"
	"math"
)

// Reader reads bit-packed data. The zero value is an empty, overflowed-on-read
// reader; use NewReader or NewReaderBits.
type Reader struct {
	data  []byte
	start int // first readable bit (absolute)
	pos   int // cursor (absolute bit index)
	end   int // bit limit (absolute, exclusive)
	err   error
}

// NewReader returns a reader over all bits of data.
func NewReader(data []byte) *Reader {
	return &Reader{data: data, end: len(data) * 8}
}

// NewReaderBits returns a reader over the first nbits bits of data. nbits is
// clamped to the size of data.
func NewReaderBits(data []byte, nbits int) *Reader {
	if nbits > len(data)*8 {
		nbits = len(data) * 8
	}
	if nbits < 0 {
		nbits = 0
	}
	return &Reader{data: data, end: nbits}
}

// Err returns ErrOverflow if any read ran past the end of the buffer.
func (r *Reader) Err() error { return r.err }

// Overflowed reports whether any read ran past the end of the buffer.
func (r *Reader) Overflowed() bool { return r.err != nil }

// Pos returns the cursor position in bits, relative to the start of the reader.
func (r *Reader) Pos() int { return r.pos - r.start }

// Len returns the total number of readable bits.
func (r *Reader) Len() int { return r.end - r.start }

// BitsLeft returns the number of unread bits.
func (r *Reader) BitsLeft() int { return r.end - r.pos }

// BytesLeft returns the number of whole unread bytes.
func (r *Reader) BytesLeft() int { return (r.end - r.pos) >> 3 }

// Data returns the underlying byte slice. The reader's bits may start at a
// non-zero offset into it (see SubReader).
func (r *Reader) Data() []byte { return r.data }

// Clone returns an independent copy of the reader's state sharing the same data.
func (r *Reader) Clone() *Reader {
	c := *r
	return &c
}

// Seek moves the cursor to bit (relative to the start of the reader).
func (r *Reader) Seek(bit int) error {
	if bit < 0 || r.start+bit > r.end {
		r.fail()
		return ErrOverflow
	}
	r.pos = r.start + bit
	return nil
}

// Skip advances the cursor by n bits. Negative values move backwards.
func (r *Reader) Skip(n int) error {
	return r.Seek(r.pos - r.start + n)
}

func (r *Reader) fail() {
	r.pos = r.end
	r.err = ErrOverflow
}

// peek loads n (<= 32) bits at the cursor without bounds checks against end.
func (r *Reader) peek(n int) uint32 {
	idx := r.pos >> 3
	shift := uint(r.pos & 7)
	var acc uint64
	if idx+8 <= len(r.data) {
		acc = binary.LittleEndian.Uint64(r.data[idx:])
	} else {
		nb := (int(shift) + n + 7) >> 3
		for i := 0; i < nb && idx+i < len(r.data); i++ {
			acc |= uint64(r.data[idx+i]) << (8 * uint(i))
		}
	}
	return uint32((acc >> shift) & (1<<uint(n) - 1))
}

// ReadBits reads an unsigned n-bit value (0 <= n <= 32). Equivalent to
// bf_read::ReadUBitLong.
func (r *Reader) ReadBits(n int) uint32 {
	if n <= 0 {
		return 0
	}
	if n > 32 {
		panic("bitbuf: ReadBits supports at most 32 bits")
	}
	if r.end-r.pos < n {
		r.fail()
		return 0
	}
	v := r.peek(n)
	r.pos += n
	return v
}

// PeekBits returns the next n bits without advancing the cursor.
func (r *Reader) PeekBits(n int) uint32 {
	if n <= 0 || r.end-r.pos < n {
		return 0
	}
	return r.peek(n)
}

// ReadSBits reads a signed, sign-extended n-bit value.
func (r *Reader) ReadSBits(n int) int32 {
	v := r.ReadBits(n)
	if n > 0 && n < 32 && v&(1<<uint(n-1)) != 0 {
		v |= ^uint32(0) << uint(n)
	}
	return int32(v)
}

// ReadBit reads a single bit.
func (r *Reader) ReadBit() bool {
	return r.ReadBits(1) != 0
}

// ReadUint8 reads an unsigned byte.
func (r *Reader) ReadUint8() uint8 { return uint8(r.ReadBits(8)) }

// ReadInt8 reads a signed byte (bf_read::ReadChar).
func (r *Reader) ReadInt8() int8 { return int8(r.ReadBits(8)) }

// ReadUint16 reads an unsigned 16-bit value (bf_read::ReadWord).
func (r *Reader) ReadUint16() uint16 { return uint16(r.ReadBits(16)) }

// ReadInt16 reads a signed 16-bit value (bf_read::ReadShort).
func (r *Reader) ReadInt16() int16 { return int16(r.ReadBits(16)) }

// ReadUint32 reads an unsigned 32-bit value.
func (r *Reader) ReadUint32() uint32 { return r.ReadBits(32) }

// ReadInt32 reads a signed 32-bit value (bf_read::ReadLong).
func (r *Reader) ReadInt32() int32 { return int32(r.ReadBits(32)) }

// ReadUint64 reads a 64-bit value, low 32 bits first (bf_read::ReadLongLong).
func (r *Reader) ReadUint64() uint64 {
	lo := uint64(r.ReadBits(32))
	hi := uint64(r.ReadBits(32))
	return lo | hi<<32
}

// ReadInt64 reads a signed 64-bit value.
func (r *Reader) ReadInt64() int64 { return int64(r.ReadUint64()) }

// ReadFloat32 reads a raw IEEE-754 float (bf_read::ReadFloat / ReadBitFloat).
func (r *Reader) ReadFloat32() float32 {
	return math.Float32frombits(r.ReadBits(32))
}

// ReadUBitVar reads the engine's variable-width unsigned integer encoding
// (2 bit selector followed by 4, 8, 12 or 32 bits).
func (r *Reader) ReadUBitVar() uint32 {
	v := r.ReadBits(6)
	switch v & 3 {
	case 0:
		return v >> 2
	case 1:
		return v>>2 | r.ReadBits(4)<<4
	case 2:
		return v>>2 | r.ReadBits(8)<<4
	default:
		return v>>2 | r.ReadBits(28)<<4
	}
}

// ReadVarInt32 reads a protobuf-style base-128 varint (at most 5 bytes).
func (r *Reader) ReadVarInt32() uint32 {
	var result uint32
	for count := 0; count < maxVarInt32Bytes; count++ {
		b := r.ReadBits(8)
		result |= (b & 0x7f) << (7 * uint(count))
		if b&0x80 == 0 {
			break
		}
	}
	return result
}

// ReadSignedVarInt32 reads a zig-zag encoded varint.
func (r *Reader) ReadSignedVarInt32() int32 {
	v := r.ReadVarInt32()
	return int32(v>>1) ^ -int32(v&1)
}

// ReadVarInt64 reads a protobuf-style base-128 varint (at most 10 bytes).
func (r *Reader) ReadVarInt64() uint64 {
	var result uint64
	for count := 0; count < maxVarInt64Bytes; count++ {
		b := uint64(r.ReadBits(8))
		result |= (b & 0x7f) << (7 * uint(count))
		if b&0x80 == 0 {
			break
		}
	}
	return result
}

// ReadSignedVarInt64 reads a zig-zag encoded 64-bit varint.
func (r *Reader) ReadSignedVarInt64() int64 {
	v := r.ReadVarInt64()
	return int64(v>>1) ^ -int64(v&1)
}

// ReadString reads a NUL-terminated string. Like bf_read::ReadString, the
// whole string is always consumed.
func (r *Reader) ReadString() string {
	if r.pos&7 == 0 {
		start := r.pos >> 3
		limit := r.end >> 3
		for i := start; i < limit; i++ {
			if r.data[i] == 0 {
				s := string(r.data[start:i])
				r.pos = (i + 1) << 3
				return s
			}
		}
		// Unterminated: consume what is there and overflow, as the engine does.
		s := string(r.data[start:limit])
		r.fail()
		return s
	}
	var buf []byte
	for {
		if r.end-r.pos < 8 {
			r.fail()
			return string(buf)
		}
		c := byte(r.ReadBits(8))
		if c == 0 {
			return string(buf)
		}
		buf = append(buf, c)
	}
}

// ReadLimitedString reads a NUL-terminated string, keeping at most max-1
// characters while still consuming the whole string, mirroring the
// truncation behaviour of fixed-size engine buffers.
func (r *Reader) ReadLimitedString(max int) string {
	s := r.ReadString()
	if max > 0 && len(s) > max-1 {
		s = s[:max-1]
	}
	return s
}

// ReadBytes reads n bytes into a new slice.
func (r *Reader) ReadBytes(n int) []byte {
	if n < 0 || r.end-r.pos < n*8 {
		r.fail()
		return nil
	}
	out := make([]byte, n)
	if r.pos&7 == 0 {
		copy(out, r.data[r.pos>>3:])
		r.pos += n * 8
		return out
	}
	for i := range out {
		out[i] = byte(r.ReadBits(8))
	}
	return out
}

// ReadBitsToBytes copies nbits bits into a new slice of BitsToBytes(nbits)
// bytes. Unused high bits of the final byte are zero.
func (r *Reader) ReadBitsToBytes(nbits int) []byte {
	if nbits < 0 || r.end-r.pos < nbits {
		r.fail()
		return nil
	}
	out := make([]byte, BitsToBytes(nbits))
	full := nbits >> 3
	if r.pos&7 == 0 {
		copy(out[:full], r.data[r.pos>>3:])
		r.pos += full * 8
	} else {
		for i := 0; i < full; i++ {
			out[i] = byte(r.ReadBits(8))
		}
	}
	if rem := nbits & 7; rem != 0 {
		out[full] = byte(r.ReadBits(rem))
	}
	return out
}

// SubReader returns a reader over the next nbits bits and advances the cursor
// past them. The returned reader shares the underlying data.
func (r *Reader) SubReader(nbits int) *Reader {
	if nbits < 0 || r.end-r.pos < nbits {
		r.fail()
		return &Reader{data: r.data, start: r.end, pos: r.end, end: r.end}
	}
	sub := &Reader{data: r.data, start: r.pos, pos: r.pos, end: r.pos + nbits}
	r.pos += nbits
	return sub
}

// ReadBitCoord reads a world coordinate (bf_read::ReadBitCoord).
func (r *Reader) ReadBitCoord() float32 {
	hasInt := r.ReadBit()
	hasFrac := r.ReadBit()
	if !hasInt && !hasFrac {
		return 0
	}
	sign := r.ReadBit()
	var intval, fracval uint32
	if hasInt {
		intval = r.ReadBits(CoordIntegerBits) + 1
	}
	if hasFrac {
		fracval = r.ReadBits(CoordFractionalBits)
	}
	v := float32(float64(intval) + float64(fracval)*CoordResolution)
	if sign {
		v = -v
	}
	return v
}

// ReadBitCoordMP reads a multiplayer-optimised world coordinate
// (bf_read::ReadBitCoordMP).
func (r *Reader) ReadBitCoordMP(integral, lowPrecision bool) float32 {
	if integral {
		flags := r.ReadBits(2)
		if flags&2 == 0 {
			return 0
		}
		n := CoordIntegerBits
		if flags&1 != 0 {
			n = CoordIntegerBitsMP
		}
		bits := r.ReadBits(n + 1)
		v := float32(bits>>1 + 1)
		if bits&1 != 0 {
			v = -v
		}
		return v
	}
	flags := r.ReadBits(3)
	inBounds, hasInt, sign := flags&1 != 0, flags&2 != 0, flags&4 != 0
	fracBits := CoordFractionalBits
	if lowPrecision {
		fracBits = CoordFractionalBitsMPLowP
	}
	var whole uint32
	if hasInt {
		n := CoordIntegerBits
		if inBounds {
			n = CoordIntegerBitsMP
		}
		whole = r.ReadBits(n) + 1
	}
	frac := r.ReadBits(fracBits)
	v := float32(int32(whole<<uint(fracBits)|frac)) * (1 / float32(int32(1)<<uint(fracBits)))
	if sign {
		v = -v
	}
	return v
}

// ReadBitNormal reads a normal component in [-1, 1] (bf_read::ReadBitNormal).
func (r *Reader) ReadBitNormal() float32 {
	sign := r.ReadBit()
	frac := r.ReadBits(NormalFractionalBits)
	v := float32(float64(frac) * NormalResolution)
	if sign {
		v = -v
	}
	return v
}

// ReadBitVec3Coord reads a vector of world coordinates where zero components
// are omitted.
func (r *Reader) ReadBitVec3Coord() [3]float32 {
	var v [3]float32
	hasX, hasY, hasZ := r.ReadBit(), r.ReadBit(), r.ReadBit()
	if hasX {
		v[0] = r.ReadBitCoord()
	}
	if hasY {
		v[1] = r.ReadBitCoord()
	}
	if hasZ {
		v[2] = r.ReadBitCoord()
	}
	return v
}

// ReadBitVec3Normal reads a unit vector whose Z component is reconstructed
// from X and Y.
func (r *Reader) ReadBitVec3Normal() [3]float32 {
	var v [3]float32
	hasX, hasY := r.ReadBit(), r.ReadBit()
	if hasX {
		v[0] = r.ReadBitNormal()
	}
	if hasY {
		v[1] = r.ReadBitNormal()
	}
	negZ := r.ReadBit()
	// Explicit conversions prevent FMA fusion, keeping engine-identical results.
	sq := float32(v[0]*v[0]) + float32(v[1]*v[1])
	if sq < 1 {
		v[2] = float32(math.Sqrt(float64(1 - sq)))
	}
	if negZ {
		v[2] = -v[2]
	}
	return v
}

// ReadBitAngle reads an angle in degrees quantised to nbits bits.
func (r *Reader) ReadBitAngle(nbits int) float32 {
	i := r.ReadBits(nbits)
	shift := float64(uint64(1) << uint(nbits))
	return float32(float64(i) * (360.0 / shift))
}

// ReadBitAngles reads three angles encoded as world coordinates.
func (r *Reader) ReadBitAngles() [3]float32 {
	return r.ReadBitVec3Coord()
}
