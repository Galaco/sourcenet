// Package bitbuf implements the bit-level reader and writer used by the
// Source Engine network protocol (bf_read / bf_write).
//
// Bits are packed least-significant-bit first into little-endian bytes, which
// is equivalent to the engine's little-endian 32-bit word packing. All of the
// engine's specialised encodings (UBitVar, protobuf-style varints, world
// coordinates, normals, angles) are reproduced bit-for-bit.
//
// Reads past the end of a buffer do not panic. As in the engine, the reader
// is marked as overflowed, the cursor is moved to the end of the buffer and
// zero values are returned. Callers decode a whole structure and check Err
// once at the end, which keeps protocol decoding code compact.
package bitbuf

import "errors"

// ErrOverflow is reported when a read or write runs past the end of a buffer.
var ErrOverflow = errors.New("bitbuf: buffer overflow")

// Coordinate and normal encoding parameters (coordsize.h).
const (
	CoordIntegerBits          = 14
	CoordFractionalBits       = 5
	CoordDenominator          = 1 << CoordFractionalBits
	CoordResolution           = 1.0 / CoordDenominator
	CoordIntegerBitsMP        = 11
	CoordFractionalBitsMPLowP = 3
	CoordDenominatorLowP      = 1 << CoordFractionalBitsMPLowP
	CoordResolutionLowP       = 1.0 / CoordDenominatorLowP
	NormalFractionalBits      = 11
	NormalDenominator         = (1 << NormalFractionalBits) - 1
	NormalResolution          = 1.0 / NormalDenominator
	maxVarInt32Bytes          = 5
	maxVarInt64Bytes          = 10
)

// BitsToBytes returns the number of bytes needed to hold n bits.
func BitsToBytes(n int) int {
	return (n + 7) >> 3
}
