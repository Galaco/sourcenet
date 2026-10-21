// Package message implements the Source Engine netmessages exchanged inside
// a netchannel: the shared net_* control messages, the server to client
// svc_* messages and the client to server clc_* messages of network
// protocol 24, including the encoding branches used by older protocol
// versions.
//
// Every message is a Go struct implementing Message. A message stream is a
// sequence of 6-bit type identifiers each followed by a message body; Read
// and ReadAll decode streams using a Registry that maps type identifiers to
// message constructors, and Write encodes a single message. Registries are
// per direction because svc_* and clc_* identifiers overlap. Games and mods
// can add or replace messages by registering their own factories on a cloned
// registry.
package message

import (
	"errors"
	"fmt"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/protocol"
)

// Context carries the connection properties that change message encodings.
type Context struct {
	// ProtocolVersion is the network protocol version of the connection.
	ProtocolVersion int
	// ReplayEnabled reports whether the peer engine was built with replay
	// support, which adds a flag to svc_ServerInfo and clc_ClientInfo. It is
	// true for TF2, CS:S, DoD:S and HL2:DM.
	ReplayEnabled bool
}

// DefaultContext returns the context for a current protocol 24 engine with
// replay support.
func DefaultContext() *Context {
	return &Context{ProtocolVersion: protocol.Version, ReplayEnabled: true}
}

func contextOrDefault(ctx *Context) *Context {
	if ctx == nil {
		return DefaultContext()
	}
	return ctx
}

// Message is a netmessage that can be decoded from and encoded to a bit
// stream. Decode and Encode handle the message body only; the 6-bit type
// identifier is handled by Read and Write.
type Message interface {
	// Type returns the wire type identifier.
	Type() int
	// Name returns the engine name of the message, such as "svc_ServerInfo".
	Name() string
	// Decode reads the message body.
	Decode(r *bitbuf.Reader, ctx *Context) error
	// Encode writes the message body.
	Encode(w *bitbuf.Writer, ctx *Context) error
}

// Bits is an owned copy of a bit payload embedded in a message, such as the
// entity data of svc_PacketEntities.
type Bits struct {
	// Data holds the payload; the final byte may be partially used.
	Data []byte
	// N is the payload length in bits.
	N int
}

// Reader returns a reader over the payload.
func (b Bits) Reader() *bitbuf.Reader {
	return bitbuf.NewReaderBits(b.Data, b.N)
}

// Bytes returns the payload length rounded up to whole bytes.
func (b Bits) Bytes() int {
	return bitbuf.BitsToBytes(b.N)
}

// BitsFromWriter returns a copy of everything written to w.
func BitsFromWriter(w *bitbuf.Writer) Bits {
	data := make([]byte, w.BytesWritten())
	copy(data, w.Bytes())
	return Bits{Data: data, N: w.BitsWritten()}
}

func readPayload(r *bitbuf.Reader, nbits int) Bits {
	return Bits{Data: r.ReadBitsToBytes(nbits), N: nbits}
}

func writePayload(w *bitbuf.Writer, b Bits) error {
	if b.N < 0 || b.N > len(b.Data)*8 {
		return fmt.Errorf("payload of %d bits does not fit in %d bytes", b.N, len(b.Data))
	}
	w.WriteBitsFromBytes(b.Data, b.N)
	return nil
}

// fits returns an error if v cannot be represented in nbits unsigned bits.
func fits(field string, v, nbits int) error {
	if v < 0 || (nbits < 63 && v >= 1<<uint(nbits)) {
		return fmt.Errorf("%s %d does not fit in %d bits", field, v, nbits)
	}
	return nil
}

// log2 mirrors the engine's Q_log2: the index of the highest set bit, 0 for 0.
func log2(v int) int {
	n := 0
	for v >>= 1; v > 0; v >>= 1 {
		n++
	}
	return n
}

// ErrOverflow is returned when a message body runs past the end of the stream.
var ErrOverflow = bitbuf.ErrOverflow

// UnknownMessageError is returned when a stream contains a type identifier
// that has no registered message. The stream cannot be parsed further
// because message bodies are not length prefixed.
type UnknownMessageError struct {
	Type int
}

func (e *UnknownMessageError) Error() string {
	return fmt.Sprintf("message: unknown message type %d", e.Type)
}

// Registry maps wire type identifiers to message constructors.
type Registry struct {
	factories [1 << protocol.NetMsgTypeBits]func() Message
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register associates msgType with a constructor, replacing any previous
// registration. It panics if msgType is outside the 6-bit type range.
func (r *Registry) Register(msgType int, factory func() Message) {
	if msgType < 0 || msgType >= len(r.factories) {
		panic(fmt.Sprintf("message: type %d out of range", msgType))
	}
	r.factories[msgType] = factory
}

// New constructs an empty message of the given type.
func (r *Registry) New(msgType int) (Message, bool) {
	if msgType < 0 || msgType >= len(r.factories) || r.factories[msgType] == nil {
		return nil, false
	}
	return r.factories[msgType](), true
}

// Clone returns an independent copy of the registry.
func (r *Registry) Clone() *Registry {
	c := *r
	return &c
}

func registerNet(r *Registry) {
	r.Register(protocol.NetNOP, func() Message { return &NetNOP{} })
	r.Register(protocol.NetDisconnect, func() Message { return &NetDisconnect{} })
	r.Register(protocol.NetFile, func() Message { return &NetFile{} })
	r.Register(protocol.NetTick, func() Message { return &NetTick{} })
	r.Register(protocol.NetStringCmd, func() Message { return &NetStringCmd{} })
	r.Register(protocol.NetSetConVar, func() Message { return &NetSetConVar{} })
	r.Register(protocol.NetSignonState, func() Message { return &NetSignonState{} })
}

// ServerToClient returns a registry of the net_* and svc_* messages a client
// receives.
func ServerToClient() *Registry {
	r := NewRegistry()
	registerNet(r)
	r.Register(protocol.SvcPrint, func() Message { return &SvcPrint{} })
	r.Register(protocol.SvcServerInfo, func() Message { return &SvcServerInfo{} })
	r.Register(protocol.SvcSendTable, func() Message { return &SvcSendTable{} })
	r.Register(protocol.SvcClassInfo, func() Message { return &SvcClassInfo{} })
	r.Register(protocol.SvcSetPause, func() Message { return &SvcSetPause{} })
	r.Register(protocol.SvcCreateStringTable, func() Message { return &SvcCreateStringTable{} })
	r.Register(protocol.SvcUpdateStringTable, func() Message { return &SvcUpdateStringTable{} })
	r.Register(protocol.SvcVoiceInit, func() Message { return &SvcVoiceInit{} })
	r.Register(protocol.SvcVoiceData, func() Message { return &SvcVoiceData{} })
	r.Register(protocol.SvcSounds, func() Message { return &SvcSounds{} })
	r.Register(protocol.SvcSetView, func() Message { return &SvcSetView{} })
	r.Register(protocol.SvcFixAngle, func() Message { return &SvcFixAngle{} })
	r.Register(protocol.SvcCrosshairAngle, func() Message { return &SvcCrosshairAngle{} })
	r.Register(protocol.SvcBSPDecal, func() Message { return &SvcBSPDecal{} })
	r.Register(protocol.SvcUserMessage, func() Message { return &SvcUserMessage{} })
	r.Register(protocol.SvcEntityMessage, func() Message { return &SvcEntityMessage{} })
	r.Register(protocol.SvcGameEvent, func() Message { return &SvcGameEvent{} })
	r.Register(protocol.SvcPacketEntities, func() Message { return &SvcPacketEntities{} })
	r.Register(protocol.SvcTempEntities, func() Message { return &SvcTempEntities{} })
	r.Register(protocol.SvcPrefetch, func() Message { return &SvcPrefetch{} })
	r.Register(protocol.SvcMenu, func() Message { return &SvcMenu{} })
	r.Register(protocol.SvcGameEventList, func() Message { return &SvcGameEventList{} })
	r.Register(protocol.SvcGetCvarValue, func() Message { return &SvcGetCvarValue{} })
	r.Register(protocol.SvcCmdKeyValues, func() Message { return &SvcCmdKeyValues{} })
	r.Register(protocol.SvcSetPauseTimed, func() Message { return &SvcSetPauseTimed{} })
	return r
}

// ClientToServer returns a registry of the net_* and clc_* messages a server
// receives.
func ClientToServer() *Registry {
	r := NewRegistry()
	registerNet(r)
	r.Register(protocol.ClcClientInfo, func() Message { return &ClcClientInfo{} })
	r.Register(protocol.ClcMove, func() Message { return &ClcMove{} })
	r.Register(protocol.ClcVoiceData, func() Message { return &ClcVoiceData{} })
	r.Register(protocol.ClcBaselineAck, func() Message { return &ClcBaselineAck{} })
	r.Register(protocol.ClcListenEvents, func() Message { return &ClcListenEvents{} })
	r.Register(protocol.ClcRespondCvarValue, func() Message { return &ClcRespondCvarValue{} })
	r.Register(protocol.ClcFileCRCCheck, func() Message { return &ClcFileCRCCheck{} })
	r.Register(protocol.ClcSaveReplay, func() Message { return &ClcSaveReplay{} })
	r.Register(protocol.ClcCmdKeyValues, func() Message { return &ClcCmdKeyValues{} })
	r.Register(protocol.ClcFileMD5Check, func() Message { return &ClcFileMD5Check{} })
	return r
}

// Write encodes m, preceded by its 6-bit type identifier.
func Write(w *bitbuf.Writer, m Message, ctx *Context) error {
	ctx = contextOrDefault(ctx)
	if err := fits("message type", m.Type(), protocol.NetMsgTypeBits); err != nil {
		return fmt.Errorf("message: encoding %s: %w", m.Name(), err)
	}
	w.WriteBits(uint32(m.Type()), protocol.NetMsgTypeBits)
	if err := m.Encode(w, ctx); err != nil {
		return fmt.Errorf("message: encoding %s: %w", m.Name(), err)
	}
	if err := w.Err(); err != nil {
		return fmt.Errorf("message: encoding %s: %w", m.Name(), err)
	}
	return nil
}

// Read decodes a single message from r.
func Read(r *bitbuf.Reader, reg *Registry, ctx *Context) (Message, error) {
	ctx = contextOrDefault(ctx)
	t := int(r.ReadBits(protocol.NetMsgTypeBits))
	if err := r.Err(); err != nil {
		return nil, fmt.Errorf("message: reading type: %w", err)
	}
	m, ok := reg.New(t)
	if !ok {
		return nil, &UnknownMessageError{Type: t}
	}
	if err := m.Decode(r, ctx); err != nil {
		return nil, fmt.Errorf("message: decoding %s: %w", m.Name(), err)
	}
	if err := r.Err(); err != nil {
		return nil, fmt.Errorf("message: decoding %s: %w", m.Name(), err)
	}
	return m, nil
}

// ReadAll decodes messages until fewer than 6 bits remain, as the engine's
// netchannel does. Padding net_NOP messages are consumed but not returned.
// On error the messages decoded so far are returned with the error.
func ReadAll(r *bitbuf.Reader, reg *Registry, ctx *Context) ([]Message, error) {
	ctx = contextOrDefault(ctx)
	var msgs []Message
	for r.BitsLeft() >= protocol.NetMsgTypeBits {
		m, err := Read(r, reg, ctx)
		if err != nil {
			return msgs, err
		}
		if m.Type() == protocol.NetNOP {
			continue
		}
		msgs = append(msgs, m)
	}
	if err := r.Err(); err != nil {
		return msgs, err
	}
	return msgs, nil
}

var errNegativeLength = errors.New("negative payload length")
