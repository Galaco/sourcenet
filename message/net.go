package message

import (
	"fmt"
	"strings"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/protocol"
)

// NetNOP is a padding message with no body.
type NetNOP struct{}

// Type implements Message.
func (m *NetNOP) Type() int { return protocol.NetNOP }

// Name implements Message.
func (m *NetNOP) Name() string { return "net_NOP" }

// Decode implements Message.
func (m *NetNOP) Decode(r *bitbuf.Reader, ctx *Context) error { return nil }

// Encode implements Message.
func (m *NetNOP) Encode(w *bitbuf.Writer, ctx *Context) error { return nil }

func (m *NetNOP) String() string { return m.Name() }

// NetDisconnect closes the connection, giving a reason.
type NetDisconnect struct {
	Reason string
}

// Type implements Message.
func (m *NetDisconnect) Type() int { return protocol.NetDisconnect }

// Name implements Message.
func (m *NetDisconnect) Name() string { return "net_Disconnect" }

// Decode implements Message.
func (m *NetDisconnect) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.Reason = r.ReadString()
	return r.Err()
}

// Encode implements Message.
func (m *NetDisconnect) Encode(w *bitbuf.Writer, ctx *Context) error {
	w.WriteString(m.Reason)
	return w.Err()
}

func (m *NetDisconnect) String() string { return fmt.Sprintf("%s: %q", m.Name(), m.Reason) }

// NetFile requests a file from the peer or denies a file request.
type NetFile struct {
	TransferID uint32
	Filename   string
	// Requested is true for a request and false for a denial.
	Requested bool
}

// Type implements Message.
func (m *NetFile) Type() int { return protocol.NetFile }

// Name implements Message.
func (m *NetFile) Name() string { return "net_File" }

// Decode implements Message.
func (m *NetFile) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.TransferID = r.ReadUint32()
	m.Filename = r.ReadString()
	m.Requested = r.ReadBit()
	return r.Err()
}

// Encode implements Message.
func (m *NetFile) Encode(w *bitbuf.Writer, ctx *Context) error {
	w.WriteUint32(m.TransferID)
	w.WriteString(m.Filename)
	w.WriteBit(m.Requested)
	return w.Err()
}

func (m *NetFile) String() string {
	verb := "deny"
	if m.Requested {
		verb = "request"
	}
	return fmt.Sprintf("%s: %s %q (ID %d)", m.Name(), verb, m.Filename, m.TransferID)
}

const netTickScale = 100000.0

// NetTick carries the sender's current tick and host frame time statistics.
type NetTick struct {
	Tick int32
	// HostFrameTime and HostFrameTimeStdDev are quantised to 1e-5 seconds
	// and clamped to [0, 0.65535] on the wire.
	HostFrameTime       float32
	HostFrameTimeStdDev float32
}

// Type implements Message.
func (m *NetTick) Type() int { return protocol.NetTick }

// Name implements Message.
func (m *NetTick) Name() string { return "net_Tick" }

func scaleFrameTime(f float32) uint32 {
	v := int64(float32(netTickScale) * f)
	if v < 0 {
		v = 0
	}
	if v > 65535 {
		v = 65535
	}
	return uint32(v)
}

// Decode implements Message.
func (m *NetTick) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.Tick = r.ReadInt32()
	m.HostFrameTime = float32(r.ReadBits(16)) / netTickScale
	m.HostFrameTimeStdDev = float32(r.ReadBits(16)) / netTickScale
	return r.Err()
}

// Encode implements Message.
func (m *NetTick) Encode(w *bitbuf.Writer, ctx *Context) error {
	w.WriteInt32(m.Tick)
	w.WriteBits(scaleFrameTime(m.HostFrameTime), 16)
	w.WriteBits(scaleFrameTime(m.HostFrameTimeStdDev), 16)
	return w.Err()
}

func (m *NetTick) String() string { return fmt.Sprintf("%s: tick %d", m.Name(), m.Tick) }

// NetStringCmd executes a console command on the peer.
type NetStringCmd struct {
	Command string
}

// Type implements Message.
func (m *NetStringCmd) Type() int { return protocol.NetStringCmd }

// Name implements Message.
func (m *NetStringCmd) Name() string { return "net_StringCmd" }

// Decode implements Message.
func (m *NetStringCmd) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.Command = r.ReadString()
	return r.Err()
}

// Encode implements Message.
func (m *NetStringCmd) Encode(w *bitbuf.Writer, ctx *Context) error {
	w.WriteString(m.Command)
	return w.Err()
}

func (m *NetStringCmd) String() string { return fmt.Sprintf("%s: %q", m.Name(), m.Command) }

// ConVar is a console variable name and value pair.
type ConVar struct {
	Name, Value string
}

// NetSetConVar sets console variables on the peer.
type NetSetConVar struct {
	ConVars []ConVar
}

// Type implements Message.
func (m *NetSetConVar) Type() int { return protocol.NetSetConVar }

// Name implements Message.
func (m *NetSetConVar) Name() string { return "net_SetConVar" }

// Decode implements Message.
func (m *NetSetConVar) Decode(r *bitbuf.Reader, ctx *Context) error {
	n := int(r.ReadUint8())
	m.ConVars = make([]ConVar, 0, n)
	for i := 0; i < n && !r.Overflowed(); i++ {
		name := r.ReadString()
		value := r.ReadString()
		m.ConVars = append(m.ConVars, ConVar{Name: name, Value: value})
	}
	return r.Err()
}

// Encode implements Message.
func (m *NetSetConVar) Encode(w *bitbuf.Writer, ctx *Context) error {
	if err := fits("convar count", len(m.ConVars), 8); err != nil {
		return err
	}
	w.WriteUint8(uint8(len(m.ConVars)))
	for _, cv := range m.ConVars {
		w.WriteString(cv.Name)
		w.WriteString(cv.Value)
	}
	return w.Err()
}

func (m *NetSetConVar) String() string {
	parts := make([]string, len(m.ConVars))
	for i, cv := range m.ConVars {
		parts[i] = fmt.Sprintf("%q=%q", cv.Name, cv.Value)
	}
	return fmt.Sprintf("%s: %d cvars, %s", m.Name(), len(m.ConVars), strings.Join(parts, ", "))
}

// NetSignonState announces a signon state transition.
type NetSignonState struct {
	State      uint8
	SpawnCount int32
}

// Type implements Message.
func (m *NetSignonState) Type() int { return protocol.NetSignonState }

// Name implements Message.
func (m *NetSignonState) Name() string { return "net_SignonState" }

// Decode implements Message.
func (m *NetSignonState) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.State = r.ReadUint8()
	m.SpawnCount = r.ReadInt32()
	return r.Err()
}

// Encode implements Message.
func (m *NetSignonState) Encode(w *bitbuf.Writer, ctx *Context) error {
	w.WriteUint8(m.State)
	w.WriteInt32(m.SpawnCount)
	return w.Err()
}

func (m *NetSignonState) String() string {
	return fmt.Sprintf("%s: state %d (%s), count %d", m.Name(), m.State, protocol.SignonStateName(int(m.State)), m.SpawnCount)
}
