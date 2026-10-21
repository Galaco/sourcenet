package compression

import "encoding/binary"

// LZSS parameters used by the engine (tier1/lzss.cpp).
const (
	lzssWindowSize = 4096
	lzssLookShift  = 4
	lzssLookAhead  = 1 << lzssLookShift
	lzssMinMatch   = 3
)

// DecompressLZSS decompresses an engine LZSS buffer. src must include the
// "LZSS" magic and size header.
func DecompressLZSS(src []byte) ([]byte, error) {
	return decompressLZSS(src, MaxDecompressedSize)
}

func decompressLZSS(src []byte, limit int) ([]byte, error) {
	if !isLZSS(src) {
		return nil, ErrUnknownFormat
	}
	size := binary.LittleEndian.Uint32(src[magicSize:])
	if uint64(size) > uint64(limit) {
		return nil, ErrTooLarge
	}
	out := make([]byte, 0, size)
	if size == 0 {
		return out, nil
	}
	in := src[lzssHeaderSize:]
	pos := 0
	var cmd byte
	for bit := 0; ; bit = (bit + 1) & 7 {
		if bit == 0 {
			if pos >= len(in) {
				return nil, ErrCorrupt
			}
			cmd = in[pos]
			pos++
		}
		if cmd&1 != 0 {
			if pos+2 > len(in) {
				return nil, ErrCorrupt
			}
			distance := int(in[pos])<<lzssLookShift | int(in[pos+1])>>lzssLookShift
			count := int(in[pos+1]&0x0f) + 1
			pos += 2
			if count == 1 {
				break
			}
			from := len(out) - distance - 1
			if from < 0 || len(out)+count > int(size) {
				return nil, ErrCorrupt
			}
			// Matches may overlap the bytes being produced.
			for i := 0; i < count; i++ {
				out = append(out, out[from+i])
			}
		} else {
			if pos >= len(in) || len(out) >= int(size) {
				return nil, ErrCorrupt
			}
			out = append(out, in[pos])
			pos++
		}
		cmd >>= 1
	}
	if len(out) != int(size) {
		return nil, ErrCorrupt
	}
	return out, nil
}

// CompressLZSS compresses src with the engine's LZSS encoder. Like the
// engine, it gives up and returns ErrNotCompressible when src is too small
// (16 bytes or fewer) or when the output would not be at least 16 bytes
// smaller than the input.
func CompressLZSS(src []byte) ([]byte, error) {
	n := len(src)
	if n <= lzssHeaderSize+8 {
		return nil, ErrNotCompressible
	}
	// Output must stay below this bound while encoding, as in the engine.
	bound := n - lzssHeaderSize - 8

	out := make([]byte, lzssHeaderSize, n)
	copy(out, MagicLZSS)
	binary.LittleEndian.PutUint32(out[magicSize:], uint32(n))

	// Chains of previous positions sharing the same first byte, most recent
	// first, limited to the sliding window.
	var head [256]int32
	for i := range head {
		head[i] = -1
	}
	prev := make([]int32, n)

	cmdPos := -1
	slot := 0
	pos := 0
	for pos < n {
		if slot == 0 {
			cmdPos = len(out)
			out = append(out, 0)
		}
		slot = (slot + 1) & 7

		lookAhead := n - pos
		if lookAhead > lzssLookAhead {
			lookAhead = lzssLookAhead
		}
		bestLen, bestPos := 0, 0
		windowStart := pos - lzssWindowSize
		for cand := head[src[pos]]; cand >= 0 && int(cand) >= windowStart; cand = prev[cand] {
			l := 0
			for l < lookAhead && src[int(cand)+l] == src[pos+l] {
				l++
			}
			if l > bestLen {
				bestLen, bestPos = l, int(cand)
			}
			if l == lookAhead {
				break
			}
		}

		if bestLen >= lzssMinMatch {
			distance := pos - bestPos - 1
			out[cmdPos] = out[cmdPos]>>1 | 0x80
			out = append(out, byte(distance>>lzssLookShift), byte(distance<<lzssLookShift)|byte(bestLen-1))
		} else {
			bestLen = 1
			out[cmdPos] >>= 1
			out = append(out, src[pos])
		}
		for i := 0; i < bestLen; i++ {
			prev[pos] = head[src[pos]]
			head[src[pos]] = int32(pos)
			pos++
		}
		if len(out) >= bound {
			return nil, ErrNotCompressible
		}
	}

	// Terminate with a zero-length match.
	if slot == 0 {
		out = append(out, 0x01)
	} else {
		out[cmdPos] = (out[cmdPos]>>1 | 0x80) >> uint(7-slot)
	}
	out = append(out, 0, 0)
	return out, nil
}
