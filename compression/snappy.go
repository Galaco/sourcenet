package compression

import (
	"encoding/binary"
	"math/bits"
)

// Snappy block format element tags (low two bits of the tag byte).
const (
	tagLiteral = 0x00
	tagCopy1   = 0x01
	tagCopy2   = 0x02
	tagCopy4   = 0x03
)

// DecompressSnappy decompresses a Snappy block prefixed with the "SNAP" magic.
func DecompressSnappy(src []byte) ([]byte, error) {
	return decompressSnappy(src, MaxDecompressedSize)
}

func decompressSnappy(src []byte, limit int) ([]byte, error) {
	if !isSnappy(src) {
		return nil, ErrUnknownFormat
	}
	return snappyDecode(src[magicSize:], limit)
}

// snappyDecodedLen reads the uncompressed length preamble of a raw Snappy
// block, returning the length and the number of preamble bytes.
func snappyDecodedLen(src []byte) (int, int, error) {
	v, n := binary.Uvarint(src)
	if n <= 0 || v > 0xffffffff {
		return 0, 0, ErrCorrupt
	}
	return int(v), n, nil
}

func snappyDecode(src []byte, limit int) ([]byte, error) {
	size, n, err := snappyDecodedLen(src)
	if err != nil {
		return nil, err
	}
	if size > limit {
		return nil, ErrTooLarge
	}
	src = src[n:]
	dst := make([]byte, size)
	d, s := 0, 0
	for s < len(src) {
		tag := src[s]
		var length, offset int
		switch tag & 3 {
		case tagLiteral:
			x := int(tag >> 2)
			s++
			if x >= 60 {
				extra := x - 59 // 1..4 little-endian length bytes
				if s+extra > len(src) {
					return nil, ErrCorrupt
				}
				x = 0
				for i := 0; i < extra; i++ {
					x |= int(src[s+i]) << (8 * uint(i))
				}
				s += extra
			}
			length = x + 1
			if length <= 0 || length > len(src)-s || length > len(dst)-d {
				return nil, ErrCorrupt
			}
			copy(dst[d:], src[s:s+length])
			d += length
			s += length
			continue
		case tagCopy1:
			if s+2 > len(src) {
				return nil, ErrCorrupt
			}
			length = 4 + int(tag>>2)&0x7
			offset = int(tag&0xe0)<<3 | int(src[s+1])
			s += 2
		case tagCopy2:
			if s+3 > len(src) {
				return nil, ErrCorrupt
			}
			length = 1 + int(tag>>2)
			offset = int(binary.LittleEndian.Uint16(src[s+1:]))
			s += 3
		case tagCopy4:
			if s+5 > len(src) {
				return nil, ErrCorrupt
			}
			length = 1 + int(tag>>2)
			offset = int(binary.LittleEndian.Uint32(src[s+1:]))
			s += 5
		}
		if offset <= 0 || offset > d || length > len(dst)-d {
			return nil, ErrCorrupt
		}
		// Copies may overlap the output being produced.
		for i := 0; i < length; i++ {
			dst[d+i] = dst[d-offset+i]
		}
		d += length
	}
	if d != len(dst) {
		return nil, ErrCorrupt
	}
	return dst, nil
}

// CompressSnappy compresses src into a Snappy block prefixed with the "SNAP"
// magic, as produced by the engine's COM_BufferToBufferCompress_Snappy.
func CompressSnappy(src []byte) []byte {
	out := make([]byte, 0, magicSize+binary.MaxVarintLen32+len(src)+len(src)/6+16)
	out = append(out, MagicSnappy...)
	out = binary.AppendUvarint(out, uint64(len(src)))
	for len(src) > 0 {
		block := src
		if len(block) > snappyBlockSize {
			block = block[:snappyBlockSize]
		}
		out = snappyEncodeBlock(out, block)
		src = src[len(block):]
	}
	return out
}

const (
	snappyBlockSize    = 1 << 16
	snappyMinMatch     = 4
	snappyMaxTableBits = 14
	snappyInputMargin  = 15 // bytes at the end of a block never used to start a match
)

func snappyHash(u uint32, shift uint) uint32 {
	return (u * 0x1e35a7bd) >> shift
}

func load32(b []byte, i int) uint32 {
	return binary.LittleEndian.Uint32(b[i:])
}

// snappyEncodeBlock appends the encoding of one block (at most 64KiB, so all
// copy offsets fit in two bytes) to dst.
func snappyEncodeBlock(dst, src []byte) []byte {
	if len(src) < snappyMinMatch+snappyInputMargin {
		return snappyEmitLiteral(dst, src)
	}
	tableBits := uint(bits.Len(uint(len(src) - 1)))
	if tableBits < 8 {
		tableBits = 8
	}
	if tableBits > snappyMaxTableBits {
		tableBits = snappyMaxTableBits
	}
	shift := 32 - tableBits
	table := make([]int32, 1<<tableBits)
	for i := range table {
		table[i] = -1
	}

	limit := len(src) - snappyInputMargin
	literalStart := 0
	s := 0
	// skip grows the step through incompressible data, as in the reference
	// implementation, so random input is processed quickly.
	skip := 32
	for s < limit {
		cur := load32(src, s)
		h := snappyHash(cur, shift)
		cand := int(table[h])
		table[h] = int32(s)
		if cand < 0 || load32(src, cand) != cur {
			s += skip >> 5
			skip++
			continue
		}
		skip = 32

		dst = snappyEmitLiteral(dst, src[literalStart:s])
		// Extend the match as far as possible.
		matchLen := snappyMinMatch
		for s+matchLen < len(src) && src[cand+matchLen] == src[s+matchLen] {
			matchLen++
		}
		dst = snappyEmitCopy(dst, s-cand, matchLen)
		// Seed the table with a position inside the match to find follow-ups.
		if s+matchLen-1 < limit {
			p := s + matchLen - 1
			table[snappyHash(load32(src, p), shift)] = int32(p)
		}
		s += matchLen
		literalStart = s
	}
	return snappyEmitLiteral(dst, src[literalStart:])
}

func snappyEmitLiteral(dst, lit []byte) []byte {
	if len(lit) == 0 {
		return dst
	}
	n := len(lit) - 1
	switch {
	case n < 60:
		dst = append(dst, byte(n)<<2|tagLiteral)
	case n < 1<<8:
		dst = append(dst, 60<<2|tagLiteral, byte(n))
	case n < 1<<16:
		dst = append(dst, 61<<2|tagLiteral, byte(n), byte(n>>8))
	case n < 1<<24:
		dst = append(dst, 62<<2|tagLiteral, byte(n), byte(n>>8), byte(n>>16))
	default:
		dst = append(dst, 63<<2|tagLiteral, byte(n), byte(n>>8), byte(n>>16), byte(n>>24))
	}
	return append(dst, lit...)
}

func snappyEmitCopy(dst []byte, offset, length int) []byte {
	// Long matches are broken into 64 byte copies, keeping the remainder at
	// least 4 bytes so it can still be emitted.
	for length >= 68 {
		dst = append(dst, 63<<2|tagCopy2, byte(offset), byte(offset>>8))
		length -= 64
	}
	if length > 64 {
		dst = append(dst, 59<<2|tagCopy2, byte(offset), byte(offset>>8))
		length -= 60
	}
	if length >= 12 || offset >= 2048 {
		return append(dst, byte(length-1)<<2|tagCopy2, byte(offset), byte(offset>>8))
	}
	return append(dst, byte(offset>>8)<<5|byte(length-4)<<2|tagCopy1, byte(offset))
}
