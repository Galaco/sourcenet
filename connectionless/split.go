package connectionless

import (
	"bytes"
	"compress/bzip2"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"

	"github.com/galaco/sourcenet/protocol"
)

// Split query responses.
//
// A2S responses too large for one datagram are split by the Steam game server
// library. Each fragment starts with the header -2 followed by:
//
//	int32  id       (bit 31 set when the payload is bzip2 compressed)
//	uint8  total    number of fragments
//	uint8  number   fragment index, starting at 0
//	uint16 size     maximum fragment size
//	[uint32 uncompressed size, uint32 CRC32]  first fragment of compressed responses only
//	payload
//
// The concatenated payload is a complete connectionless packet, starting with
// the 0xFFFFFFFF header. This byte layout matches the netchannel split
// header; only the id's compression bit and the extra compressed fields are
// specific to queries. Servers of a few old games (app ids 215, 17550, 17700
// and 240 at query protocol 7) omit the size field; they are not supported.

const querySplitCompressedFlag = 0x80000000

// ErrNotSplit is returned when a datagram does not carry the split header.
var ErrNotSplit = errors.New("connectionless: not a split packet")

// QuerySplit is a single fragment of a split query response.
type QuerySplit struct {
	ID         uint32 // id without the compression bit
	Total      uint8
	Number     uint8
	Size       uint16
	Compressed bool
	// UncompressedSize and CRC are only set on the first fragment of a
	// compressed response.
	UncompressedSize uint32
	CRC              uint32
	Payload          []byte
}

// IsSplit reports whether b starts with the split header (-2).
func IsSplit(b []byte) bool {
	return len(b) >= 4 && int32(binary.LittleEndian.Uint32(b)) == protocol.HeaderSplit
}

// ParseQuerySplit decodes one split query fragment.
func ParseQuerySplit(b []byte) (*QuerySplit, error) {
	if !IsSplit(b) {
		return nil, ErrNotSplit
	}
	if len(b) < protocol.SplitHeaderSize {
		return nil, ErrTruncated
	}
	id := binary.LittleEndian.Uint32(b[4:])
	s := &QuerySplit{
		ID:         id &^ querySplitCompressedFlag,
		Compressed: id&querySplitCompressedFlag != 0,
		Total:      b[8],
		Number:     b[9],
		Size:       binary.LittleEndian.Uint16(b[10:]),
	}
	rest := b[protocol.SplitHeaderSize:]
	if s.Compressed && s.Number == 0 {
		if len(rest) < 8 {
			return nil, ErrTruncated
		}
		s.UncompressedSize = binary.LittleEndian.Uint32(rest)
		s.CRC = binary.LittleEndian.Uint32(rest[4:])
		rest = rest[8:]
	}
	if s.Total == 0 || s.Number >= s.Total {
		return nil, fmt.Errorf("connectionless: invalid split fragment %d of %d", s.Number, s.Total)
	}
	s.Payload = append([]byte(nil), rest...)
	return s, nil
}

// MaxQueryResponseSize bounds the size of a reassembled (and decompressed)
// query response.
const MaxQueryResponseSize = 1 << 20

// maxPendingQuerySplits bounds the number of incomplete responses tracked;
// all state is dropped when it is exceeded.
const maxPendingQuerySplits = 32

type querySplitEntry struct {
	total      uint8
	compressed bool
	size, crc  uint32
	parts      [][]byte
	received   int
}

// QuerySplitReassembler collects split query fragments into complete
// connectionless packets. It keeps state for several responses at once,
// keyed by the split id. The zero value is ready for use.
type QuerySplitReassembler struct {
	entries map[uint32]*querySplitEntry
}

// Add processes one datagram carrying a split fragment. When the final
// missing fragment arrives it returns the reassembled packet and true.
// Duplicate fragments are ignored.
func (q *QuerySplitReassembler) Add(b []byte) ([]byte, bool, error) {
	s, err := ParseQuerySplit(b)
	if err != nil {
		return nil, false, err
	}
	if q.entries == nil || len(q.entries) >= maxPendingQuerySplits {
		q.entries = make(map[uint32]*querySplitEntry)
	}
	e := q.entries[s.ID]
	if e == nil || e.total != s.Total {
		e = &querySplitEntry{total: s.Total, parts: make([][]byte, s.Total)}
		q.entries[s.ID] = e
	}
	if s.Compressed {
		e.compressed = true
		if s.Number == 0 {
			e.size, e.crc = s.UncompressedSize, s.CRC
		}
	}
	if e.parts[s.Number] != nil {
		return nil, false, nil
	}
	e.parts[s.Number] = s.Payload
	e.received++
	if e.received < int(e.total) {
		return nil, false, nil
	}
	delete(q.entries, s.ID)
	data := bytes.Join(e.parts, nil)
	if !e.compressed {
		return data, true, nil
	}
	if e.size > MaxQueryResponseSize {
		return nil, false, fmt.Errorf("connectionless: compressed response of %d bytes too large", e.size)
	}
	out, err := io.ReadAll(io.LimitReader(bzip2.NewReader(bytes.NewReader(data)), int64(e.size)+1))
	if err != nil {
		return nil, false, fmt.Errorf("connectionless: decompressing split response: %w", err)
	}
	if uint32(len(out)) != e.size {
		return nil, false, fmt.Errorf("connectionless: decompressed %d bytes, expected %d", len(out), e.size)
	}
	if crc32.ChecksumIEEE(out) != e.crc {
		return nil, false, errors.New("connectionless: split response CRC mismatch")
	}
	return out, true, nil
}

// Pending returns the number of incomplete responses being tracked.
func (q *QuerySplitReassembler) Pending() int { return len(q.entries) }

// Reset discards all incomplete responses.
func (q *QuerySplitReassembler) Reset() { q.entries = nil }

// SplitQueryResponse splits a connectionless packet into uncompressed query
// fragments carrying at most maxPayload bytes each (1248 on Valve servers).
func SplitQueryResponse(packet []byte, id uint32, maxPayload int) [][]byte {
	if maxPayload <= 0 {
		maxPayload = 1248
	}
	total := (len(packet) + maxPayload - 1) / maxPayload
	if total == 0 {
		total = 1
	}
	out := make([][]byte, 0, total)
	for i := 0; i < total; i++ {
		start := i * maxPayload
		end := start + maxPayload
		if end > len(packet) {
			end = len(packet)
		}
		frag := make([]byte, protocol.SplitHeaderSize, protocol.SplitHeaderSize+end-start)
		binary.LittleEndian.PutUint32(frag, 0xfffffffe)
		binary.LittleEndian.PutUint32(frag[4:], id&^querySplitCompressedFlag)
		frag[8] = byte(total)
		frag[9] = byte(i)
		binary.LittleEndian.PutUint16(frag[10:], uint16(maxPayload))
		out = append(out, append(frag, packet[start:end]...))
	}
	return out
}
