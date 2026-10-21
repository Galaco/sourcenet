package packet

import (
	"encoding/binary"
	"time"

	"github.com/galaco/sourcenet/protocol"
)

// StaleTimeout is how long an incomplete split packet is kept without
// receiving another part (SPLIT_PACKET_STALE_TIME).
const StaleTimeout = 2 * time.Second

// SplitHeader is the header carried by each part of a split packet.
type SplitHeader struct {
	// Sequence identifies the split packet; all parts share it.
	Sequence int32
	// Index is the zero based part number and Count the number of parts.
	Index, Count int
	// SplitSize is the payload size of every part except possibly the last.
	SplitSize int
}

// ParseSplitHeader decodes the header of a split datagram and returns it with
// the part's payload. Only structural validity is checked here; sizes are
// validated by the Reassembler.
func ParseSplitHeader(datagram []byte) (SplitHeader, []byte, error) {
	if len(datagram) < protocol.SplitHeaderSize {
		return SplitHeader{}, nil, ErrTooShort
	}
	if header(datagram) != protocol.HeaderSplit {
		return SplitHeader{}, nil, ErrNotSplit
	}
	id := binary.LittleEndian.Uint16(datagram[8:])
	h := SplitHeader{
		Sequence:  int32(binary.LittleEndian.Uint32(datagram[4:])),
		Index:     int(id >> 8),
		Count:     int(id & 0xff),
		SplitSize: int(int16(binary.LittleEndian.Uint16(datagram[10:]))),
	}
	return h, datagram[protocol.SplitHeaderSize:], nil
}

// Split breaks payload into split datagrams of at most maxRoutable bytes
// (clamped to [protocol.MinUserRoutablePayload, protocol.MaxRoutablePayload])
// sharing the given sequence number.
func Split(payload []byte, sequence int32, maxRoutable int) ([][]byte, error) {
	if len(payload) == 0 {
		return nil, ErrEmpty
	}
	splitSize := clampRoutable(maxRoutable) - protocol.SplitHeaderSize
	count := (len(payload) + splitSize - 1) / splitSize
	if count > MaxSendSplits {
		return nil, ErrTooManySplits
	}
	parts := make([][]byte, 0, count)
	for i := 0; i < count; i++ {
		chunk := payload[i*splitSize:]
		if len(chunk) > splitSize {
			chunk = chunk[:splitSize]
		}
		part := make([]byte, protocol.SplitHeaderSize, protocol.SplitHeaderSize+len(chunk))
		putHeader(part, protocol.HeaderSplit)
		binary.LittleEndian.PutUint32(part[4:], uint32(sequence))
		binary.LittleEndian.PutUint16(part[8:], uint16(i<<8|count))
		binary.LittleEndian.PutUint16(part[10:], uint16(splitSize))
		parts = append(parts, append(part, chunk...))
	}
	return parts, nil
}

// noSequence marks a part slot that has never been received. It cannot
// collide with a 32-bit sequence number.
const noSequence = int64(-1) << 40

// Reassembler rebuilds split packets received from a single remote peer,
// following the engine's rules (NET_GetLong): one packet is assembled at a
// time, a part with a new sequence number restarts assembly, duplicate parts
// are ignored, parts must agree on the split size and an assembly that sees
// no new part for StaleTimeout is discarded.
type Reassembler struct {
	sequence   int64 // sequence being assembled, or noSequence
	remaining  int   // parts still missing
	totalSize  int
	splitSize  int
	lastActive time.Time
	received   [256]int64 // sequence each part slot was last filled for
	buf        []byte
}

// NewReassembler returns an empty reassembler.
func NewReassembler() *Reassembler {
	r := &Reassembler{}
	r.Reset()
	return r
}

// Reset discards any partially assembled packet.
func (r *Reassembler) Reset() {
	r.sequence = noSequence
	r.remaining = 0
	r.totalSize = 0
	r.splitSize = 0
	r.lastActive = time.Time{}
	for i := range r.received {
		r.received[i] = noSequence
	}
	r.buf = r.buf[:0]
}

// Add processes one split datagram. When it completes a packet, the packet is
// returned with complete=true. The returned slice is owned by the caller.
func (r *Reassembler) Add(now time.Time, datagram []byte) (packet []byte, complete bool, err error) {
	h, payload, err := ParseSplitHeader(datagram)
	if err != nil {
		return nil, false, err
	}
	if h.SplitSize < MinSplitSize || h.SplitSize > MaxSplitSize ||
		h.Count == 0 || h.Count > MaxSplits || h.Index >= h.Count ||
		len(payload) == 0 || len(payload) > h.SplitSize ||
		(h.Index < h.Count-1 && len(payload) != h.SplitSize) {
		return nil, false, ErrBadSplit
	}

	if !r.lastActive.IsZero() && now.Sub(r.lastActive) >= StaleTimeout {
		r.Reset()
	}
	r.lastActive = now

	seq := int64(h.Sequence)
	if r.sequence == noSequence || seq != r.sequence {
		r.sequence = seq
		r.remaining = h.Count
		r.splitSize = h.SplitSize
		r.totalSize = 0
	}
	if h.SplitSize != r.splitSize {
		return nil, false, ErrSplitMismatch
	}

	offset := h.Index * h.SplitSize
	end := offset + len(payload)
	if end > MaxMessage {
		return nil, false, ErrTooLarge
	}
	if r.received[h.Index] != seq {
		if h.Index == h.Count-1 {
			r.totalSize = end
		}
		r.remaining--
		r.received[h.Index] = seq
	}
	if len(r.buf) < end {
		r.buf = append(r.buf, make([]byte, end-len(r.buf))...)
	}
	copy(r.buf[offset:], payload)

	if r.remaining > 0 {
		return nil, false, nil
	}
	r.sequence = noSequence
	if r.totalSize == 0 || r.totalSize > len(r.buf) {
		return nil, false, ErrBadSplit
	}
	return append([]byte(nil), r.buf[:r.totalSize]...), true, nil
}
