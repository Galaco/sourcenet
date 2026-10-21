// Package demo reads and writes Source Engine demo files (.dem).
//
// A demo is a fixed-size header followed by a sequence of commands. Packet
// and signon commands carry raw netmessage streams (the same svc_* messages a
// client receives), which can be decoded with the message package. The
// datatables command carries the send tables and server classes needed to
// decode entities, in the format read by the datatable package.
//
// Demo protocol 3 (Source 2013) and 2 share this container layout. The only
// difference relevant to decoding is inside the datatables payload: protocol
// 2 stores send prop flags in 11 bits instead of 16. Protocol 2 demos never
// contain StringTables commands.
package demo

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// HeaderID is the magic at the start of every demo file.
const HeaderID = "HL2DEMO"

// Protocol is the demo container version written by protocol 24 engines.
const Protocol = 3

const (
	headerStampSize = 8
	pathSize        = 260
	headerSize      = headerStampSize + 4 + 4 + 4*pathSize + 4 + 4 + 4 + 4
	cmdInfoSize     = 4 + 6*12
	// maxDataLength bounds the length prefix of command payloads.
	maxDataLength = 1 << 26
)

// Command identifies a demo command.
type Command uint8

// Demo commands.
const (
	Signon       Command = 1 // signon data (netmessages)
	Packet       Command = 2 // network packet (netmessages)
	SyncTick     Command = 3 // clock sync marker, no payload
	ConsoleCmd   Command = 4 // console command string
	UserCmd      Command = 5 // encoded user command
	DataTables   Command = 6 // send tables and server classes
	Stop         Command = 7 // end of demo
	StringTables Command = 8 // string table snapshot (protocol 3)
)

// String returns the engine name of the command.
func (c Command) String() string {
	switch c {
	case Signon:
		return "dem_signon"
	case Packet:
		return "dem_packet"
	case SyncTick:
		return "dem_synctick"
	case ConsoleCmd:
		return "dem_consolecmd"
	case UserCmd:
		return "dem_usercmd"
	case DataTables:
		return "dem_datatables"
	case Stop:
		return "dem_stop"
	case StringTables:
		return "dem_stringtables"
	}
	return fmt.Sprintf("dem_unknown(%d)", uint8(c))
}

// Header is the demo file header.
type Header struct {
	// Protocol is the demo container version (Protocol, or 2 for old demos).
	Protocol int32
	// NetworkProtocol is the network protocol version of the recording.
	NetworkProtocol int32
	ServerName      string
	ClientName      string
	MapName         string
	GameDir         string
	PlaybackTime    float32
	PlaybackTicks   int32
	PlaybackFrames  int32
	// SignonLength is the size in bytes of the signon section.
	SignonLength int32
}

// CmdInfo is the view information recorded with every packet.
type CmdInfo struct {
	Flags            int32
	ViewOrigin       [3]float32
	ViewAngles       [3]float32
	LocalViewAngles  [3]float32
	ViewOrigin2      [3]float32
	ViewAngles2      [3]float32
	LocalViewAngles2 [3]float32
}

// CmdInfo flags.
const (
	FlagUseOrigin2 = 1 << 0
	FlagUseAngles2 = 1 << 1
	FlagNoInterp   = 1 << 2
)

// Frame is one demo command.
type Frame struct {
	Command Command
	Tick    int32
	// CmdInfo, SeqIn and SeqOut are set for Signon and Packet frames.
	CmdInfo CmdInfo
	SeqIn   int32
	SeqOut  int32
	// OutgoingSequence is set for UserCmd frames.
	OutgoingSequence int32
	// Data is the payload of every command except SyncTick and Stop.
	Data []byte
}

// ErrInvalidHeader is returned when a file is not a demo.
var ErrInvalidHeader = errors.New("demo: invalid header")

// Reader reads demo frames.
type Reader struct {
	r       io.Reader
	header  Header
	stopped bool
	buf     [cmdInfoSize]byte
}

// NewReader reads and validates the demo header.
func NewReader(r io.Reader) (*Reader, error) {
	var raw [headerSize]byte
	if _, err := io.ReadFull(r, raw[:]); err != nil {
		return nil, fmt.Errorf("demo: reading header: %w", err)
	}
	if !bytes.Equal(raw[:headerStampSize], []byte(HeaderID+"\x00")) {
		return nil, ErrInvalidHeader
	}
	h := Header{}
	off := headerStampSize
	h.Protocol = int32(binary.LittleEndian.Uint32(raw[off:]))
	h.NetworkProtocol = int32(binary.LittleEndian.Uint32(raw[off+4:]))
	off += 8
	for _, s := range []*string{&h.ServerName, &h.ClientName, &h.MapName, &h.GameDir} {
		*s = cString(raw[off : off+pathSize])
		off += pathSize
	}
	h.PlaybackTime = math.Float32frombits(binary.LittleEndian.Uint32(raw[off:]))
	h.PlaybackTicks = int32(binary.LittleEndian.Uint32(raw[off+4:]))
	h.PlaybackFrames = int32(binary.LittleEndian.Uint32(raw[off+8:]))
	h.SignonLength = int32(binary.LittleEndian.Uint32(raw[off+12:]))
	if h.Protocol < 2 || h.Protocol > Protocol {
		return nil, fmt.Errorf("demo: unsupported demo protocol %d", h.Protocol)
	}
	return &Reader{r: r, header: h}, nil
}

func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// Header returns the demo header.
func (r *Reader) Header() Header { return r.header }

func (r *Reader) readInt32() (int32, error) {
	if _, err := io.ReadFull(r.r, r.buf[:4]); err != nil {
		return 0, err
	}
	return int32(binary.LittleEndian.Uint32(r.buf[:4])), nil
}

func (r *Reader) readData() ([]byte, error) {
	n, err := r.readInt32()
	if err != nil {
		return nil, err
	}
	if n < 0 || n > maxDataLength {
		return nil, fmt.Errorf("demo: invalid data length %d", n)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r.r, data); err != nil {
		return nil, err
	}
	return data, nil
}

// Next returns the next frame. The Stop frame is returned like any other;
// calls after it (or at the end of the file) return io.EOF. A file that ends
// in the middle of a frame returns io.ErrUnexpectedEOF.
func (r *Reader) Next() (*Frame, error) {
	if r.stopped {
		return nil, io.EOF
	}
	var hdr [5]byte
	n, err := io.ReadFull(r.r, hdr[:])
	if n == 0 && (err == io.EOF || err == io.ErrUnexpectedEOF) {
		r.stopped = true
		return nil, io.EOF
	}
	if err != nil {
		return nil, unexpected(err)
	}
	f := &Frame{Command: Command(hdr[0]), Tick: int32(binary.LittleEndian.Uint32(hdr[1:]))}
	switch f.Command {
	case Signon, Packet:
		if _, err := io.ReadFull(r.r, r.buf[:cmdInfoSize]); err != nil {
			return nil, unexpected(err)
		}
		f.CmdInfo = decodeCmdInfo(r.buf[:cmdInfoSize])
		if f.SeqIn, err = r.readInt32(); err != nil {
			return nil, unexpected(err)
		}
		if f.SeqOut, err = r.readInt32(); err != nil {
			return nil, unexpected(err)
		}
		f.Data, err = r.readData()
	case SyncTick:
	case Stop:
		r.stopped = true
	case UserCmd:
		if f.OutgoingSequence, err = r.readInt32(); err != nil {
			return nil, unexpected(err)
		}
		f.Data, err = r.readData()
	case ConsoleCmd, DataTables, StringTables:
		f.Data, err = r.readData()
	default:
		return nil, fmt.Errorf("demo: unknown command %d at tick %d", hdr[0], f.Tick)
	}
	if err != nil {
		return nil, unexpected(err)
	}
	return f, nil
}

func unexpected(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

// ConsoleCommand returns the command string of a ConsoleCmd frame.
func (f *Frame) ConsoleCommand() string { return cString(f.Data) }

func decodeCmdInfo(b []byte) CmdInfo {
	var c CmdInfo
	c.Flags = int32(binary.LittleEndian.Uint32(b))
	off := 4
	for _, v := range c.vectors() {
		for i := range v {
			v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[off:]))
			off += 4
		}
	}
	return c
}

func (c *CmdInfo) vectors() []*[3]float32 {
	return []*[3]float32{&c.ViewOrigin, &c.ViewAngles, &c.LocalViewAngles, &c.ViewOrigin2, &c.ViewAngles2, &c.LocalViewAngles2}
}

func encodeCmdInfo(c CmdInfo) []byte {
	b := make([]byte, cmdInfoSize)
	binary.LittleEndian.PutUint32(b, uint32(c.Flags))
	off := 4
	for _, v := range c.vectors() {
		for _, f := range v {
			binary.LittleEndian.PutUint32(b[off:], math.Float32bits(f))
			off += 4
		}
	}
	return b
}

// Writer writes demo files.
type Writer struct {
	w io.Writer
}

// NewWriter writes the demo header. Header fields that are only known at the
// end of a recording (playback time, ticks, frames, signon length) can be
// updated with RewriteHeader when w is an io.WriteSeeker.
func NewWriter(w io.Writer, h Header) (*Writer, error) {
	if _, err := w.Write(encodeHeader(h)); err != nil {
		return nil, err
	}
	return &Writer{w: w}, nil
}

func encodeHeader(h Header) []byte {
	b := make([]byte, headerSize)
	copy(b, HeaderID)
	off := headerStampSize
	binary.LittleEndian.PutUint32(b[off:], uint32(h.Protocol))
	binary.LittleEndian.PutUint32(b[off+4:], uint32(h.NetworkProtocol))
	off += 8
	for _, s := range []string{h.ServerName, h.ClientName, h.MapName, h.GameDir} {
		if len(s) > pathSize-1 {
			s = s[:pathSize-1]
		}
		copy(b[off:], s)
		off += pathSize
	}
	binary.LittleEndian.PutUint32(b[off:], math.Float32bits(h.PlaybackTime))
	binary.LittleEndian.PutUint32(b[off+4:], uint32(h.PlaybackTicks))
	binary.LittleEndian.PutUint32(b[off+8:], uint32(h.PlaybackFrames))
	binary.LittleEndian.PutUint32(b[off+12:], uint32(h.SignonLength))
	return b
}

// RewriteHeader overwrites the header at the start of the file and restores
// the write position. The underlying writer must be an io.WriteSeeker.
func (w *Writer) RewriteHeader(h Header) error {
	ws, ok := w.w.(io.WriteSeeker)
	if !ok {
		return errors.New("demo: writer does not support seeking")
	}
	pos, err := ws.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err := ws.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := ws.Write(encodeHeader(h)); err != nil {
		return err
	}
	_, err = ws.Seek(pos, io.SeekStart)
	return err
}

// WriteFrame writes one frame using the layout of its command.
func (w *Writer) WriteFrame(f *Frame) error {
	var b bytes.Buffer
	b.WriteByte(byte(f.Command))
	writeInt32(&b, f.Tick)
	switch f.Command {
	case Signon, Packet:
		b.Write(encodeCmdInfo(f.CmdInfo))
		writeInt32(&b, f.SeqIn)
		writeInt32(&b, f.SeqOut)
		writeData(&b, f.Data)
	case SyncTick, Stop:
	case UserCmd:
		writeInt32(&b, f.OutgoingSequence)
		writeData(&b, f.Data)
	case ConsoleCmd, DataTables, StringTables:
		writeData(&b, f.Data)
	default:
		return fmt.Errorf("demo: cannot write unknown command %d", uint8(f.Command))
	}
	_, err := w.w.Write(b.Bytes())
	return err
}

func writeInt32(b *bytes.Buffer, v int32) {
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], uint32(v))
	b.Write(tmp[:])
}

func writeData(b *bytes.Buffer, data []byte) {
	writeInt32(b, int32(len(data)))
	b.Write(data)
}
