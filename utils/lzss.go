package utils

import (
	"bytes"
	"encoding/binary"
	"github.com/blacktop/lzss"
)

const lzssIdentifier = uint32(('S' << 24) | ('S' << 16) | ('Z' << 8) | ('L'))

// lszzHeader provides identifier and true size of lzss data
type lzssHeader struct {
	Id         uint32 // Constant to prove if data is lzss or not
	ActualSize uint32
}

// isCompressed determines if data is lzss compressed
func isCompressed(data []byte) bool {
	if len(data) < 8 {
		return false
	}
	header := lzssHeader{}
	binary.Read(bytes.NewBuffer(data[:8]), binary.LittleEndian, &header)

	if header.Id != lzssIdentifier {
		return false
	}

	return true
}

// LZSSDecompress decompresses lzss compressed data
func LZSSDecompress(data []byte) []byte {
	if !isCompressed(data) {
		return data
	}
	// Ignore 8 byte header
	return lzss.Decompress(data[8:])
}
