// Package compression implements the two compression formats used by the
// Source Engine network protocol: Valve's LZSS variant and Snappy.
//
// Compressed buffers are self-describing: they start with a four byte magic
// ("LZSS" or "SNAP"), as produced by the engine's COM_BufferToBufferCompress
// helpers. LZSS buffers store the uncompressed size after the magic, Snappy
// buffers carry it in the Snappy block preamble.
package compression

import (
	"encoding/binary"
	"errors"

	"github.com/galaco/sourcenet/protocol"
)

var (
	// ErrUnknownFormat is returned when a buffer does not start with a
	// recognised compression magic.
	ErrUnknownFormat = errors.New("compression: unknown format")
	// ErrCorrupt is returned for malformed compressed data or when the
	// decompressed size does not match the declared size.
	ErrCorrupt = errors.New("compression: corrupt data")
	// ErrTooLarge is returned when the declared uncompressed size exceeds the
	// permitted limit.
	ErrTooLarge = errors.New("compression: uncompressed size exceeds limit")
	// ErrNotCompressible is returned by CompressLZSS when compression would not
	// reduce the size of the input.
	ErrNotCompressible = errors.New("compression: input is not compressible")
)

// Magic identifiers that prefix compressed buffers.
const (
	MagicLZSS   = "LZSS"
	MagicSnappy = "SNAP"
)

// MaxDecompressedSize is the default limit applied by Decompress. It matches
// the largest transfer the netchannel can describe (MAX_FILE_SIZE).
const MaxDecompressedSize = protocol.MaxFileSize

const (
	magicSize      = 4
	lzssHeaderSize = 8
)

// IsCompressed reports whether src starts with a recognised compression magic.
func IsCompressed(src []byte) bool {
	return isLZSS(src) || isSnappy(src)
}

func isLZSS(src []byte) bool {
	return len(src) >= lzssHeaderSize && string(src[:magicSize]) == MagicLZSS
}

func isSnappy(src []byte) bool {
	return len(src) > magicSize && string(src[:magicSize]) == MagicSnappy
}

// UncompressedSize returns the declared uncompressed size of src without
// decompressing it.
func UncompressedSize(src []byte) (int, error) {
	switch {
	case isLZSS(src):
		return int(binary.LittleEndian.Uint32(src[magicSize:])), nil
	case isSnappy(src):
		n, _, err := snappyDecodedLen(src[magicSize:])
		return n, err
	}
	return 0, ErrUnknownFormat
}

// Decompress decompresses an LZSS or Snappy buffer, dispatching on its magic.
// Outputs larger than MaxDecompressedSize are rejected.
func Decompress(src []byte) ([]byte, error) {
	return DecompressWithLimit(src, MaxDecompressedSize)
}

// DecompressWithLimit is Decompress with a caller supplied size limit.
func DecompressWithLimit(src []byte, limit int) ([]byte, error) {
	switch {
	case isLZSS(src):
		return decompressLZSS(src, limit)
	case isSnappy(src):
		return decompressSnappy(src, limit)
	}
	return nil, ErrUnknownFormat
}
