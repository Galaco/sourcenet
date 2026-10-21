// Package packet implements the datagram framing layer that sits between UDP
// sockets and the netchannel: classification of datagrams, reassembly and
// creation of split packets, and whole-packet compression.
//
// A logical packet (a netchannel packet or a connectionless packet) may be
// compressed (header -3) and, if larger than the routable payload size, split
// into several datagrams (header -2). Receivers reassemble splits first and
// then decompress, matching the engine's receive path.
package packet

import (
	"encoding/binary"
	"errors"
	"time"

	"github.com/galaco/sourcenet/compression"
	"github.com/galaco/sourcenet/protocol"
)

// Kind classifies a datagram by its leading 32-bit header.
type Kind int

// Datagram kinds.
const (
	KindNetChannel Kind = iota
	KindConnectionless
	KindSplit
	KindCompressed
	KindInvalid
)

// String returns the name of the kind.
func (k Kind) String() string {
	switch k {
	case KindNetChannel:
		return "netchannel"
	case KindConnectionless:
		return "connectionless"
	case KindSplit:
		return "split"
	case KindCompressed:
		return "compressed"
	case KindInvalid:
		return "invalid"
	}
	return "unknown"
}

// Errors returned by the framing layer.
var (
	ErrTooShort       = errors.New("packet: datagram too short")
	ErrNotSplit       = errors.New("packet: not a split packet")
	ErrBadSplit       = errors.New("packet: invalid split packet header")
	ErrSplitMismatch  = errors.New("packet: split size differs from the rest of the sequence")
	ErrTooLarge       = errors.New("packet: reassembled packet too large")
	ErrNotCompressed  = errors.New("packet: not a compressed packet")
	ErrDecompress     = errors.New("packet: decompression failed")
	ErrEmpty          = errors.New("packet: empty payload")
	ErrTooManySplits  = errors.New("packet: payload needs too many split packets")
	ErrConnectionless = errors.New("packet: connectionless packets must not be compressed")
)

// Engine limits on split packets (net_ws.cpp).
const (
	// MinSplitSize and MaxSplitSize bound the payload bytes carried by a split
	// datagram, excluding its header.
	MinSplitSize = protocol.MinUserRoutablePayload - protocol.SplitHeaderSize
	MaxSplitSize = protocol.MaxRoutablePayload - protocol.SplitHeaderSize
	// MaxMessage is the largest logical packet (NET_MAX_MESSAGE).
	MaxMessage = (protocol.NetMaxPayload + 9 + 15) &^ 15
	// MaxSplits is the most parts a single packet may be split into
	// (MAX_SPLITPACKET_SPLITS).
	MaxSplits = MaxMessage / MinSplitSize
	// MaxSendSplits is the most parts Split will produce. The engine decodes
	// the part number as a signed byte, so part numbers of 128 and above are
	// mishandled by real servers and clients.
	MaxSendSplits = 128
	// DefaultCompressMinSize is the smallest packet the Encoder compresses
	// (net_compresspackets_minsize).
	DefaultCompressMinSize = 1024
)

func header(b []byte) int32 {
	return int32(binary.LittleEndian.Uint32(b))
}

func putHeader(b []byte, h int32) {
	binary.LittleEndian.PutUint32(b, uint32(h))
}

// Classify returns the kind of a datagram based on its first four bytes.
// Datagrams shorter than protocol.NetMinMessage bytes are invalid.
func Classify(datagram []byte) Kind {
	if len(datagram) < protocol.NetMinMessage {
		return KindInvalid
	}
	switch header(datagram) {
	case protocol.HeaderConnectionless:
		return KindConnectionless
	case protocol.HeaderSplit:
		return KindSplit
	case protocol.HeaderCompressed:
		return KindCompressed
	}
	return KindNetChannel
}

// Compress frames packet as a compressed datagram (header -3 followed by a
// Snappy buffer). It returns the original packet and false when compression
// would not make it smaller. Connectionless packets are never compressed.
func Compress(packet []byte) ([]byte, bool) {
	if len(packet) >= 4 && header(packet) == protocol.HeaderConnectionless {
		return packet, false
	}
	comp := compression.CompressSnappy(packet)
	if len(comp)+4 >= len(packet) {
		return packet, false
	}
	out := make([]byte, 4, 4+len(comp))
	putHeader(out, protocol.HeaderCompressed)
	return append(out, comp...), true
}

// Decompress unwraps a compressed datagram (header -3). The payload may be
// LZSS or Snappy; results larger than protocol.NetMaxPayload are rejected.
func Decompress(datagram []byte) ([]byte, error) {
	if len(datagram) < 4 || header(datagram) != protocol.HeaderCompressed {
		return nil, ErrNotCompressed
	}
	payload := datagram[4:]
	size, err := compression.UncompressedSize(payload)
	if err != nil || size <= 0 || size > protocol.NetMaxPayload {
		return nil, ErrDecompress
	}
	out, err := compression.DecompressWithLimit(payload, protocol.NetMaxPayload)
	if err != nil || len(out) != size {
		return nil, ErrDecompress
	}
	return out, nil
}

// Decoder turns received datagrams from a single remote peer into logical
// packets, reassembling split packets and decompressing compressed ones.
type Decoder struct {
	reassembler *Reassembler
}

// NewDecoder returns a decoder for one remote peer.
func NewDecoder() *Decoder {
	return &Decoder{reassembler: NewReassembler()}
}

// Decode processes one received datagram. It returns the logical packet and
// ok=true when a complete packet is available; ok=false (with a nil error)
// means more split parts are needed.
func (d *Decoder) Decode(now time.Time, datagram []byte) (packet []byte, ok bool, err error) {
	if len(datagram) < protocol.NetMinMessage {
		return nil, false, ErrTooShort
	}
	packet = datagram
	if header(packet) == protocol.HeaderSplit {
		packet, ok, err = d.reassembler.Add(now, packet)
		if err != nil || !ok {
			return nil, false, err
		}
		if len(packet) < 4 {
			return nil, false, ErrTooShort
		}
	}
	if header(packet) == protocol.HeaderCompressed {
		packet, err = Decompress(packet)
		if err != nil {
			return nil, false, err
		}
	}
	return packet, true, nil
}

// Reset discards any partially reassembled packet.
func (d *Decoder) Reset() {
	d.reassembler.Reset()
}

// Encoder turns logical packets into datagrams ready to send to one remote
// peer, optionally compressing them and splitting those that exceed the
// routable payload size.
type Encoder struct {
	// MaxRoutable is the largest datagram to send; 0 means
	// protocol.MaxRoutablePayload. It is clamped to
	// [protocol.MinUserRoutablePayload, protocol.MaxRoutablePayload].
	MaxRoutable int
	// Compress enables Snappy compression of netchannel packets.
	Compress bool
	// CompressMinSize is the smallest packet that is compressed; 0 means
	// DefaultCompressMinSize.
	CompressMinSize int

	splitSequence int32
}

// Encode returns the datagrams that carry packet.
func (e *Encoder) Encode(packet []byte) ([][]byte, error) {
	if len(packet) == 0 {
		return nil, ErrEmpty
	}
	connectionless := len(packet) >= 4 && header(packet) == protocol.HeaderConnectionless
	if e.Compress && !connectionless {
		minSize := e.CompressMinSize
		if minSize <= 0 {
			minSize = DefaultCompressMinSize
		}
		if len(packet) >= minSize {
			packet, _ = Compress(packet)
		}
	}
	maxRoutable := clampRoutable(e.MaxRoutable)
	if len(packet) <= maxRoutable {
		return [][]byte{packet}, nil
	}
	if e.splitSequence == 0 {
		e.splitSequence = 1
	}
	e.splitSequence++
	return Split(packet, e.splitSequence, maxRoutable)
}

func clampRoutable(n int) int {
	if n <= 0 || n > protocol.MaxRoutablePayload {
		return protocol.MaxRoutablePayload
	}
	if n < protocol.MinUserRoutablePayload {
		return protocol.MinUserRoutablePayload
	}
	return n
}
