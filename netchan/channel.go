package netchan

import (
	"errors"
	"fmt"
	"hash/crc32"
	"time"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/compression"
	"github.com/galaco/sourcenet/protocol"
)

// Rate limits in bytes per second (net.h).
const (
	MinRate     = 1000
	MaxRate     = 1024 * 1024
	DefaultRate = 80000
)

// Timeouts (net.h / net_chan.cpp).
const (
	SignonTimeout          = 300 * time.Second // used while connecting and loading
	ConnectionProblemTime  = 4 * time.Second   // IsTimingOut threshold
	DefaultMaxClearTime    = 4 * time.Second   // net_maxcleartime
	DefaultMaxPacketDrop   = 5000              // net_maxpacketdrop
	defaultCompressMinSize = 1024              // net_compresspackets_minsize
)

// Errors returned by ProcessPacket. Callers normally drop the packet and
// carry on: ErrStalePacket and ErrFragmentOrder are routine on lossy links
// (the peer resends), while ErrReliableState and repeated corruption
// indicate a broken connection.
var (
	ErrStalePacket      = errors.New("netchan: duplicate or out of order packet")
	ErrChecksum         = errors.New("netchan: packet checksum mismatch")
	ErrChallenge        = errors.New("netchan: packet challenge mismatch")
	ErrTooManyDropped   = errors.New("netchan: too many dropped packets")
	ErrReliableState    = errors.New("netchan: reliable state invalid")
	ErrMalformed        = errors.New("netchan: malformed packet")
	ErrFragmentOrder    = errors.New("netchan: fragment received before its header")
	ErrReliableOverflow = errors.New("netchan: reliable stream overflow")
)

const (
	subChannelFree    = 0
	subChannelToSend  = 1
	subChannelWaiting = 2
	subChannelDirty   = 3
)

// Config holds channel settings. The zero value is valid; unset fields take
// the engine defaults.
type Config struct {
	// Challenge is the connection challenge sent in every packet header and
	// required in every packet received once the peer has sent one.
	Challenge uint32
	// Rate is the outgoing bandwidth limit in bytes per second.
	Rate int
	// MaxFragmentBytes limits reliable fragment bytes per packet
	// (net_maxfragments). Defaults to protocol.MaxRoutablePayload.
	MaxFragmentBytes int
	// DisableChecksum turns off the 16-bit packet checksum, as the engine
	// does for loopback and demo channels.
	DisableChecksum bool
	// CompressFragments compresses large reliable blocks and files with
	// Snappy before fragmenting them (CNetChan::SetCompressionMode).
	CompressFragments bool
	// ForegroundFileTransmission sends as many file fragments per packet as
	// fit. By default files trickle one fragment per packet, as the engine
	// does once a client is fully connected.
	ForegroundFileTransmission bool
	// MaxPacketDrop rejects packets that skip more sequence numbers than
	// this (0 means DefaultMaxPacketDrop, negative disables the check).
	MaxPacketDrop int
	// MaxClearTime caps how far into the future rate limiting may push the
	// next send (0 means DefaultMaxClearTime, negative disables the cap).
	MaxClearTime time.Duration
	// Timeout after which IsTimedOut reports true. Defaults to SignonTimeout.
	Timeout time.Duration
}

// fragments is a block of reliable data or a file queued for sending or
// being received (dataFragments_t).
type fragments struct {
	filename         string
	isFile           bool
	buffer           []byte
	bits             int
	bytes            int
	transferID       uint32
	compressed       bool
	uncompressedSize int
	numFragments     int
	ackedFragments   int
	pendingFragments int
	received         []bool // receive side: fragments already stored
}

type subChannel struct {
	startFragment [protocol.MaxStreams]int
	numFragments  [protocol.MaxStreams]int
	sendSeq       int32
	state         int
}

func (s *subChannel) free() {
	s.state = subChannelFree
	s.sendSeq = -1
	for i := range s.numFragments {
		s.numFragments[i] = 0
		s.startFragment[i] = -1
	}
}

// File is a file received over the file stream.
type File struct {
	Name       string
	TransferID uint32
	Data       []byte
}

// Incoming is the result of processing one packet. Payload readers share
// memory with the packet passed to ProcessPacket and with the channel's
// receive buffers; consume them before the next call.
type Incoming struct {
	Sequence    int32 // sequence number of this packet
	Acknowledge int32 // our newest sequence number the peer has seen
	Flags       uint8
	Choked      int // packets the peer choked before this one
	Dropped     int // packets lost between the previous packet and this one
	// Reliable holds completed reliable message blocks, to be processed
	// before Unreliable.
	Reliable []*bitbuf.Reader
	// Files holds completed incoming file transfers.
	Files []File
	// Unreliable is the remainder of the packet after the header and any
	// reliable fragments.
	Unreliable *bitbuf.Reader
}

// Channel is a single network channel to a remote peer.
type Channel struct {
	cfg Config

	outSeq    int32
	inSeq     int32
	outSeqAck int32

	outReliableState uint8
	inReliableState  uint8
	choked           int

	reliable   *bitbuf.Writer
	unreliable *bitbuf.Writer
	voice      *bitbuf.Writer

	waiting       [protocol.MaxStreams][]*fragments
	receive       [protocol.MaxStreams]fragments
	subChans      [protocol.MaxSubChannels]subChannel
	challengeSeen bool

	clearTime    time.Time
	lastReceived time.Time
	connectTime  time.Time
	packetDrop   int

	flow [2]flow
}

// New returns a channel ready to talk to a peer whose first packet will
// carry sequence number 1.
func New(now time.Time, cfg Config) *Channel {
	if cfg.Rate == 0 {
		cfg.Rate = DefaultRate
	}
	cfg.Rate = clampInt(cfg.Rate, MinRate, MaxRate)
	if cfg.MaxFragmentBytes <= 0 {
		cfg.MaxFragmentBytes = protocol.MaxRoutablePayload
	}
	cfg.MaxFragmentBytes = clampInt(cfg.MaxFragmentBytes, protocol.FragmentSize, protocol.MaxRoutablePayload)
	if cfg.MaxPacketDrop == 0 {
		cfg.MaxPacketDrop = DefaultMaxPacketDrop
	}
	if cfg.MaxClearTime == 0 {
		cfg.MaxClearTime = DefaultMaxClearTime
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = SignonTimeout
	}
	c := &Channel{
		cfg:          cfg,
		outSeq:       1,
		reliable:     bitbuf.NewLimitedWriter(protocol.NetMaxPayload),
		unreliable:   bitbuf.NewLimitedWriter(protocol.NetMaxDatagramPayload),
		voice:        bitbuf.NewLimitedWriter(protocol.NetMaxDatagramPayload),
		lastReceived: now,
		connectTime:  now,
	}
	for i := range c.subChans {
		c.subChans[i].free()
	}
	return c
}

// Reliable returns the writer for reliable messages. Its contents are
// fragmented and queued on the next Transmit.
func (c *Channel) Reliable() *bitbuf.Writer { return c.reliable }

// Unreliable returns the writer for unreliable messages, sent with the next
// packet if they fit and discarded otherwise.
func (c *Channel) Unreliable() *bitbuf.Writer { return c.unreliable }

// Voice returns the writer for voice data, appended after the unreliable
// payload when it fits.
func (c *Channel) Voice() *bitbuf.Writer { return c.voice }

// Challenge returns the connection challenge.
func (c *Channel) Challenge() uint32 { return c.cfg.Challenge }

// SetChallenge changes the connection challenge.
func (c *Channel) SetChallenge(v uint32) { c.cfg.Challenge = v }

// SetRate sets the outgoing bandwidth limit in bytes per second.
func (c *Channel) SetRate(rate int) { c.cfg.Rate = clampInt(rate, MinRate, MaxRate) }

// Rate returns the outgoing bandwidth limit in bytes per second.
func (c *Channel) Rate() int { return c.cfg.Rate }

// SetTimeout sets the inactivity timeout. Non-positive values disable it;
// small values are raised to ConnectionProblemTime and large ones capped at
// one hour, as the engine does.
func (c *Channel) SetTimeout(d time.Duration) {
	switch {
	case d <= 0:
		d = -1
	case d > time.Hour:
		d = time.Hour
	case d < ConnectionProblemTime:
		d = ConnectionProblemTime
	}
	c.cfg.Timeout = d
}

// SetFileTransmissionMode selects background (one fragment per packet) or
// foreground file sending.
func (c *Channel) SetFileTransmissionMode(background bool) {
	c.cfg.ForegroundFileTransmission = !background
}

// SetCompressFragments enables Snappy compression of large reliable blocks.
func (c *Channel) SetCompressFragments(enabled bool) { c.cfg.CompressFragments = enabled }

// OutSequence returns the sequence number the next packet will use.
func (c *Channel) OutSequence() int32 { return c.outSeq }

// InSequence returns the newest sequence number received from the peer.
func (c *Channel) InSequence() int32 { return c.inSeq }

// OutSequenceAck returns our newest sequence number acknowledged by the peer.
func (c *Channel) OutSequenceAck() int32 { return c.outSeqAck }

// DropCount returns the number of packets lost before the last one received.
func (c *Channel) DropCount() int { return c.packetDrop }

// SetChoked records that a packet was deliberately not sent this frame. The
// outgoing sequence still advances so that command numbering stays aligned.
func (c *Channel) SetChoked() {
	c.outSeq++
	c.choked++
}

// CanPacket reports whether the rate limiter allows sending now.
func (c *Channel) CanPacket(now time.Time) bool {
	return c.clearTime.Before(now)
}

// IsTimedOut reports whether nothing was received within the timeout.
func (c *Channel) IsTimedOut(now time.Time) bool {
	if c.cfg.Timeout < 0 {
		return false
	}
	return now.Sub(c.lastReceived) > c.cfg.Timeout
}

// IsTimingOut reports whether the connection appears to be having problems.
func (c *Channel) IsTimingOut(now time.Time) bool {
	if c.cfg.Timeout < 0 {
		return false
	}
	return now.Sub(c.lastReceived) > ConnectionProblemTime
}

// TimeSinceLastReceived returns the time since the last valid packet.
func (c *Channel) TimeSinceLastReceived(now time.Time) time.Duration {
	if d := now.Sub(c.lastReceived); d > 0 {
		return d
	}
	return 0
}

// TimeConnected returns the time since the channel was created.
func (c *Channel) TimeConnected(now time.Time) time.Duration {
	if d := now.Sub(c.connectTime); d > 0 {
		return d
	}
	return 0
}

// IsOverflowed reports whether the reliable stream overflowed. The
// connection cannot recover and should be dropped.
func (c *Channel) IsOverflowed() bool { return c.reliable.Overflowed() }

// HasPendingReliableData reports whether reliable data or files are waiting
// to be sent or acknowledged.
func (c *Channel) HasPendingReliableData() bool {
	return c.reliable.BitsWritten() > 0 ||
		len(c.waiting[protocol.StreamNormal]) > 0 ||
		len(c.waiting[protocol.StreamFile]) > 0
}

// StreamProgress reports bytes received and expected for the transfer in
// progress on the given stream (GetStreamProgress), for loading screens.
func (c *Channel) StreamProgress(stream int) (received, total int) {
	d := &c.receive[stream]
	if d.buffer == nil || d.numFragments == 0 {
		return 0, 0
	}
	received = d.ackedFragments * protocol.FragmentSize
	if received > d.bytes {
		received = d.bytes
	}
	return received, d.bytes
}

// Reset discards queued unreliable and reliable stream data that has not
// been fragmented yet and lifts the rate limit.
func (c *Channel) Reset() {
	c.unreliable.Reset()
	c.reliable.Reset()
	c.clearTime = time.Time{}
	c.choked = 0
}

// Clear discards all queued and partially received data (CNetChan::Clear).
// Subchannels already on the wire are marked dirty so their state bits stay
// consistent with the peer.
func (c *Channel) Clear() {
	for i := range c.waiting {
		c.waiting[i] = nil
		c.receive[i] = fragments{}
	}
	for i := range c.subChans {
		s := &c.subChans[i]
		switch s.state {
		case subChannelToSend:
			c.outReliableState ^= 1 << uint(i)
			s.free()
		case subChannelWaiting:
			s.state = subChannelDirty
		}
	}
	c.Reset()
}

// QueueFile schedules data to be sent to the peer over the file stream.
func (c *Channel) QueueFile(name string, transferID uint32, data []byte) error {
	if len(data) >= protocol.MaxFileSize {
		return fmt.Errorf("netchan: file %q too large (%d bytes)", name, len(data))
	}
	for _, f := range c.waiting[protocol.StreamFile] {
		if f.filename == name {
			return nil
		}
	}
	buf := make([]byte, len(data))
	copy(buf, data)
	c.waiting[protocol.StreamFile] = append(c.waiting[protocol.StreamFile], &fragments{
		filename:     name,
		isFile:       true,
		buffer:       buf,
		bytes:        len(buf),
		bits:         len(buf) * 8,
		transferID:   transferID,
		numFragments: bytesToFragments(len(buf)),
	})
	return nil
}

func bytesToFragments(n int) int {
	return (n + protocol.FragmentSize - 1) / protocol.FragmentSize
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// shortChecksum folds a CRC32 into 16 bits (BufferToShortChecksum).
func shortChecksum(b []byte) uint16 {
	crc := crc32.ChecksumIEEE(b)
	return uint16(crc&0xffff) ^ uint16(crc>>16)
}

// createFragmentsFromBuffer moves the reliable stream into the normal
// stream's waiting list, appending to the newest queued block when that
// block has not started sending yet.
func (c *Channel) createFragmentsFromBuffer(src *bitbuf.Writer) {
	list := c.waiting[protocol.StreamNormal]
	var data *fragments
	if n := len(list); n > 1 {
		last := list[n-1]
		total := pad4(bitbuf.BitsToBytes(last.bits + src.BitsWritten()))
		if total < protocol.NetMaxPayload && !last.isFile {
			data = last
		}
	}
	if data == nil {
		data = &fragments{}
		c.waiting[protocol.StreamNormal] = append(list, data)
	}

	w := bitbuf.NewWriter(pad4(bitbuf.BitsToBytes(data.bits + src.BitsWritten())))
	w.WriteBitsFromBytes(data.buffer, data.bits)
	w.WriteBitsFromBytes(src.Bytes(), src.BitsWritten())
	if rem := w.BitsWritten() % 8; rem > 0 && rem <= 8-protocol.NetMsgTypeBits {
		w.WriteBits(protocol.NetNOP, protocol.NetMsgTypeBits)
	}
	data.bits += src.BitsWritten()
	data.bytes = bitbuf.BitsToBytes(data.bits)
	data.buffer = w.Bytes()[:data.bytes]
	data.numFragments = bytesToFragments(data.bytes)
	data.ackedFragments = 0
	data.pendingFragments = 0
}

func pad4(n int) int { return (n + 3) &^ 3 }

// compressFragments compresses the head block of each waiting list before
// its first fragment is sent.
func (c *Channel) compressFragments() {
	if !c.cfg.CompressFragments {
		return
	}
	for i := range c.waiting {
		if len(c.waiting[i]) == 0 {
			continue
		}
		d := c.waiting[i][0]
		if d.compressed || d.bytes < defaultCompressMinSize || d.ackedFragments > 0 || d.pendingFragments > 0 {
			continue
		}
		out := compression.CompressSnappy(d.buffer[:d.bytes])
		if len(out) >= d.bytes {
			continue
		}
		d.uncompressedSize = d.bytes
		d.buffer = out
		d.bytes = len(out)
		d.numFragments = bytesToFragments(d.bytes)
		d.compressed = true
	}
}

func (c *Channel) freeSubChannel() *subChannel {
	for i := range c.subChans {
		if c.subChans[i].state == subChannelFree {
			return &c.subChans[i]
		}
	}
	return nil
}

// updateSubChannels assigns not yet sent fragments to a free subchannel.
func (c *Channel) updateSubChannels() {
	sub := c.freeSubChannel()
	if sub == nil {
		return
	}
	maxFragments := c.cfg.MaxFragmentBytes / protocol.FragmentSize
	send := false
	for i := 0; i < protocol.MaxStreams; i++ {
		if len(c.waiting[i]) == 0 {
			continue
		}
		d := c.waiting[i][0]
		sent := d.ackedFragments + d.pendingFragments
		if sent >= d.numFragments {
			continue
		}
		n := min(maxFragments, d.numFragments-sent)
		if i == protocol.StreamFile && !c.cfg.ForegroundFileTransmission {
			n = min(1, n)
		}
		sub.startFragment[i] = sent
		sub.numFragments[i] = n
		d.pendingFragments += n
		send = true
		maxFragments -= n
		if maxFragments <= 0 {
			break
		}
	}
	if send {
		idx := c.subChannelIndex(sub)
		c.outReliableState ^= 1 << uint(idx)
		sub.state = subChannelToSend
		sub.sendSeq = 0
	}
}

func (c *Channel) subChannelIndex(s *subChannel) int {
	for i := range c.subChans {
		if &c.subChans[i] == s {
			return i
		}
	}
	return -1
}

// writeSubChannelData writes the first subchannel scheduled for sending.
func (c *Channel) writeSubChannelData(w *bitbuf.Writer) bool {
	c.compressFragments()
	c.updateSubChannels()

	idx := -1
	for i := range c.subChans {
		if c.subChans[i].state == subChannelToSend {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}
	sub := &c.subChans[idx]
	w.WriteBits(uint32(idx), 3)

	for i := 0; i < protocol.MaxStreams; i++ {
		if sub.numFragments[i] == 0 {
			w.WriteBit(false)
			continue
		}
		d := c.waiting[i][0]
		w.WriteBit(true)

		offset := sub.startFragment[i] * protocol.FragmentSize
		length := sub.numFragments[i] * protocol.FragmentSize
		if sub.startFragment[i]+sub.numFragments[i] == d.numFragments {
			if rest := protocol.FragmentSize - d.bytes%protocol.FragmentSize; rest < protocol.FragmentSize {
				length -= rest
			}
		}

		single := sub.numFragments[i] == d.numFragments && !d.isFile
		if single {
			w.WriteBit(false)
			w.WriteBit(d.compressed)
			if d.compressed {
				w.WriteBits(uint32(d.uncompressedSize), protocol.MaxFileSizeBits)
			}
			w.WriteVarInt32(uint32(d.bytes))
		} else {
			w.WriteBit(true)
			w.WriteBits(uint32(sub.startFragment[i]), protocol.MaxFileSizeBits-protocol.FragmentBits)
			w.WriteBits(uint32(sub.numFragments[i]), 3)
			if offset == 0 {
				w.WriteBit(d.isFile)
				if d.isFile {
					w.WriteUint32(d.transferID)
					w.WriteString(d.filename)
				}
				w.WriteBit(d.compressed)
				if d.compressed {
					w.WriteBits(uint32(d.uncompressedSize), protocol.MaxFileSizeBits)
				}
				w.WriteBits(uint32(d.bytes), protocol.MaxFileSizeBits)
			}
		}
		w.WriteBytes(d.buffer[offset : offset+length])

		sub.sendSeq = c.outSeq
		sub.state = subChannelWaiting
	}
	return true
}

// Transmit builds the next packet: header, reliable fragments, the
// unreliable and voice payloads and padding. The returned packet should be
// handed to a packet.Encoder for optional compression and splitting.
func (c *Channel) Transmit(now time.Time) ([]byte, error) {
	return c.TransmitDatagram(now, nil)
}

// TransmitDatagram is like Transmit but also includes datagram, which takes
// precedence over the unreliable stream and is sent whole or not at all
// (CNetChan::SendDatagram).
func (c *Channel) TransmitDatagram(now time.Time, datagram *bitbuf.Writer) ([]byte, error) {
	if c.reliable.Overflowed() {
		return nil, ErrReliableOverflow
	}
	if c.reliable.BitsWritten() > 0 {
		c.createFragmentsFromBuffer(c.reliable)
		c.reliable.Reset()
	}

	w := bitbuf.NewLimitedWriter(protocol.NetMaxPayload + 16)
	w.WriteInt32(c.outSeq)
	w.WriteInt32(c.inSeq)
	flagsPos := w.BitsWritten()
	w.WriteUint8(0)
	checksumPos := -1
	if !c.cfg.DisableChecksum {
		checksumPos = w.BitsWritten()
		w.WriteUint16(0)
	}
	checksumStart := w.BytesWritten()

	w.WriteUint8(c.inReliableState)
	var flags uint8
	if c.choked > 0 {
		flags |= protocol.PacketFlagChoked
		w.WriteUint8(uint8(c.choked & 0xff))
	}
	flags |= protocol.PacketFlagChallenge
	w.WriteUint32(c.cfg.Challenge)

	if c.writeSubChannelData(w) {
		flags |= protocol.PacketFlagReliable
	}
	if datagram != nil && datagram.BitsWritten() < w.BitsLeft() {
		w.WriteBitsFromBytes(datagram.Bytes(), datagram.BitsWritten())
	}
	if c.unreliable.BitsWritten() < w.BitsLeft() {
		w.WriteBitsFromBytes(c.unreliable.Bytes(), c.unreliable.BitsWritten())
	}
	c.unreliable.Reset()
	if n := c.voice.BitsWritten(); n > 0 && n < w.BitsLeft() {
		w.WriteBitsFromBytes(c.voice.Bytes(), n)
	}
	c.voice.Reset()

	for w.BytesWritten() < protocol.MinRoutablePayload {
		w.WriteBits(protocol.NetNOP, protocol.NetMsgTypeBits)
	}
	if rem := w.BitsWritten() % 8; rem > 0 && rem <= 8-protocol.NetMsgTypeBits {
		w.WriteBits(protocol.NetNOP, protocol.NetMsgTypeBits)
	}
	if rem := w.BitsWritten() % 8; rem > 0 {
		pad := 8 - rem
		flags |= protocol.EncodePadBits(pad)
		w.WriteBits(1<<uint(pad)-1, pad)
	}
	if w.Overflowed() {
		return nil, ErrMalformed
	}

	w.WriteBitsAt(flagsPos, uint32(flags), 8)
	pkt := w.Bytes()
	if checksumPos >= 0 {
		w.WriteBitsAt(checksumPos, uint32(shortChecksum(pkt[checksumStart:])), 16)
	}

	size := len(pkt) + protocol.UDPHeaderSize
	c.flowNewPacket(flowOutgoing, now, c.outSeq, c.inSeq, c.choked, 0, size)
	c.flowUpdate(flowOutgoing, now, size)

	if c.clearTime.Before(now) {
		c.clearTime = now
	}
	c.clearTime = c.clearTime.Add(time.Duration(float64(size) / float64(c.cfg.Rate) * float64(time.Second)))
	if c.cfg.MaxClearTime > 0 {
		if latest := now.Add(c.cfg.MaxClearTime); c.clearTime.After(latest) {
			c.clearTime = latest
		}
	}

	c.choked = 0
	c.outSeq++
	return pkt, nil
}

// ProcessPacket parses a logical packet from the peer: validates the header,
// updates acknowledgement state and extracts reliable and unreliable
// payloads.
//
// When the error is non-nil and the returned Incoming is nil the packet was
// rejected and must be ignored. When both are non-nil the packet was
// accepted but a reliable transfer in it was corrupt and has been discarded:
// the returned Incoming holds the payloads completed before the failure
// (Unreliable is nil, as the engine stops processing the packet) and should
// still be handled.
func (c *Channel) ProcessPacket(now time.Time, pkt []byte) (*Incoming, error) {
	c.flowUpdate(flowIncoming, now, len(pkt)+protocol.UDPHeaderSize)

	r := bitbuf.NewReader(pkt)
	in, err := c.processHeader(now, r, pkt)
	if err != nil {
		return nil, err
	}
	c.lastReceived = now

	if in.Flags&protocol.PacketFlagReliable != 0 {
		bit := uint8(1) << r.ReadBits(3)
		for i := 0; i < protocol.MaxStreams; i++ {
			if r.ReadBit() {
				if err := c.readSubChannelData(r, i); err != nil {
					return nil, err
				}
			}
		}
		if r.Overflowed() {
			return nil, ErrMalformed
		}
		c.inReliableState ^= bit
		for i := 0; i < protocol.MaxStreams; i++ {
			if err := c.checkReceivingList(i, in); err != nil {
				return in, err
			}
		}
	}
	in.Unreliable = r
	return in, nil
}

func (c *Channel) processHeader(now time.Time, r *bitbuf.Reader, pkt []byte) (*Incoming, error) {
	seq := r.ReadInt32()
	ack := r.ReadInt32()
	flags := r.ReadUint8()
	if !c.cfg.DisableChecksum {
		sum := r.ReadUint16()
		off := r.Pos() >> 3
		if r.Overflowed() || shortChecksum(pkt[off:]) != sum {
			return nil, ErrChecksum
		}
	}
	relState := r.ReadUint8()
	choked := 0
	if flags&protocol.PacketFlagChoked != 0 {
		choked = int(r.ReadUint8())
	}
	if flags&protocol.PacketFlagChallenge != 0 {
		if r.ReadUint32() != c.cfg.Challenge {
			return nil, ErrChallenge
		}
		c.challengeSeen = true
	} else if c.challengeSeen {
		return nil, ErrChallenge
	}
	if r.Overflowed() {
		return nil, ErrMalformed
	}
	if seq <= c.inSeq {
		return nil, ErrStalePacket
	}
	drop := int(seq - (c.inSeq + int32(choked) + 1))
	if c.cfg.MaxPacketDrop > 0 && drop > c.cfg.MaxPacketDrop {
		return nil, ErrTooManyDropped
	}

	// Validate before mutating so a rejected packet leaves no trace.
	for i := range c.subChans {
		s := &c.subChans[i]
		mask := uint8(1) << uint(i)
		if c.outReliableState&mask == relState&mask && s.state != subChannelDirty && s.sendSeq > ack {
			return nil, ErrReliableState
		}
	}
	for i := range c.subChans {
		s := &c.subChans[i]
		mask := uint8(1) << uint(i)
		if c.outReliableState&mask == relState&mask {
			switch s.state {
			case subChannelDirty:
				s.free()
			case subChannelWaiting:
				for j := 0; j < protocol.MaxStreams; j++ {
					if s.numFragments[j] == 0 || len(c.waiting[j]) == 0 {
						continue
					}
					d := c.waiting[j][0]
					d.ackedFragments += s.numFragments[j]
					d.pendingFragments -= s.numFragments[j]
				}
				s.free()
			}
		} else if s.sendSeq <= ack {
			switch s.state {
			case subChannelWaiting:
				s.state = subChannelToSend
			case subChannelDirty:
				c.outReliableState ^= mask
				s.free()
			}
		}
	}

	c.packetDrop = drop
	c.inSeq = seq
	c.outSeqAck = ack
	for i := 0; i < protocol.MaxStreams; i++ {
		c.checkWaitingList(i)
	}
	c.flowNewPacket(flowIncoming, now, seq, ack, choked, max(drop, 0), len(pkt)+protocol.UDPHeaderSize)

	return &Incoming{
		Sequence:    seq,
		Acknowledge: ack,
		Flags:       flags,
		Choked:      choked,
		Dropped:     max(drop, 0),
	}, nil
}

// checkWaitingList removes the head block of a waiting list once every
// fragment has been acknowledged.
func (c *Channel) checkWaitingList(stream int) {
	if len(c.waiting[stream]) == 0 || c.outSeqAck <= 0 {
		return
	}
	d := c.waiting[stream][0]
	if d.ackedFragments == d.numFragments {
		c.waiting[stream] = c.waiting[stream][1:]
	}
}

func (c *Channel) readSubChannelData(r *bitbuf.Reader, stream int) error {
	d := &c.receive[stream]
	single := !r.ReadBit()
	startFragment, numFragments := 0, 0
	offset, length := 0, 0
	if !single {
		startFragment = int(r.ReadBits(protocol.MaxFileSizeBits - protocol.FragmentBits))
		numFragments = int(r.ReadBits(3))
		offset = startFragment * protocol.FragmentSize
		length = numFragments * protocol.FragmentSize
	}

	if offset == 0 {
		var hdr fragments
		if single {
			if r.ReadBit() {
				hdr.compressed = true
				hdr.uncompressedSize = int(r.ReadBits(protocol.MaxFileSizeBits))
			}
			hdr.bytes = int(r.ReadVarInt32())
		} else {
			if r.ReadBit() {
				hdr.isFile = true
				hdr.transferID = r.ReadUint32()
				hdr.filename = r.ReadLimitedString(protocol.MaxOSPath)
			}
			if r.ReadBit() {
				hdr.compressed = true
				hdr.uncompressedSize = int(r.ReadBits(protocol.MaxFileSizeBits))
			}
			hdr.bytes = int(r.ReadBits(protocol.MaxFileSizeBits))
		}
		if r.Overflowed() {
			return ErrMalformed
		}
		// A new header replaces any aborted transfer on this stream.
		*d = hdr
		d.bits = d.bytes * 8
		d.numFragments = bytesToFragments(d.bytes)
		if single {
			numFragments = d.numFragments
			length = numFragments * protocol.FragmentSize
		}
		if d.bytes > protocol.MaxFileSize {
			*d = fragments{}
			return ErrMalformed
		}
		d.buffer = make([]byte, pad4(d.bytes))
		d.received = make([]bool, d.numFragments)
	} else if d.buffer == nil {
		return ErrFragmentOrder
	}

	switch {
	case startFragment+numFragments == d.numFragments:
		if rest := protocol.FragmentSize - d.bytes%protocol.FragmentSize; rest < protocol.FragmentSize {
			length -= rest
		}
	case startFragment+numFragments > d.numFragments:
		return ErrMalformed
	}
	if length <= 0 || offset+length > d.bytes {
		*d = fragments{}
		return ErrMalformed
	}
	chunk := r.ReadBytes(length)
	if r.Overflowed() {
		return ErrMalformed
	}
	copy(d.buffer[offset:], chunk)
	// Count each fragment once so a resent chunk cannot complete a block early.
	for i := startFragment; i < startFragment+numFragments; i++ {
		if !d.received[i] {
			d.received[i] = true
			d.ackedFragments++
		}
	}
	return nil
}

// checkReceivingList delivers a stream's block once all fragments arrived.
func (c *Channel) checkReceivingList(stream int, in *Incoming) error {
	d := &c.receive[stream]
	if d.buffer == nil || d.ackedFragments < d.numFragments {
		return nil
	}
	if d.ackedFragments > d.numFragments {
		*d = fragments{}
		return ErrMalformed
	}
	data := d.buffer[:d.bytes]
	if d.compressed {
		out, err := compression.Decompress(data)
		switch {
		case errors.Is(err, compression.ErrUnknownFormat):
			// The engine copies unrecognised payloads through unchanged.
		case err != nil:
			*d = fragments{}
			return fmt.Errorf("netchan: decompress fragments: %w", err)
		default:
			if len(out) != d.uncompressedSize {
				*d = fragments{}
				return ErrMalformed
			}
			data = out
		}
	}
	if d.isFile {
		in.Files = append(in.Files, File{Name: d.filename, TransferID: d.transferID, Data: data})
	} else {
		in.Reliable = append(in.Reliable, bitbuf.NewReader(data))
	}
	*d = fragments{}
	return nil
}
