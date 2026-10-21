package message

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/protocol"
)

// SvcPrint prints text to the client console.
type SvcPrint struct {
	Text string
}

// Type implements Message.
func (m *SvcPrint) Type() int { return protocol.SvcPrint }

// Name implements Message.
func (m *SvcPrint) Name() string { return "svc_Print" }

// Decode implements Message.
func (m *SvcPrint) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.Text = r.ReadString()
	return r.Err()
}

// Encode implements Message.
func (m *SvcPrint) Encode(w *bitbuf.Writer, ctx *Context) error {
	w.WriteString(m.Text)
	return w.Err()
}

func (m *SvcPrint) String() string { return fmt.Sprintf("%s: %q", m.Name(), m.Text) }

// SvcServerInfo is the first message of a connection, describing the game,
// map and the client's player slot.
type SvcServerInfo struct {
	// Protocol is the server's network protocol version. It also selects
	// between the map MD5 (protocol > 17) and map CRC encodings.
	Protocol    int16
	ServerCount int32
	IsHLTV      bool
	IsDedicated bool
	// ClientCRC is a legacy field; current servers send -1.
	ClientCRC    int32
	MaxClasses   uint16
	MapMD5       [16]byte
	MapCRC       uint32
	PlayerSlot   uint8
	MaxClients   uint8
	TickInterval float32
	// OS is 'w' for Windows and 'l' for Linux servers.
	OS       byte
	GameDir  string
	MapName  string
	SkyName  string
	HostName string
	// IsReplay is only on the wire when Context.ReplayEnabled is set and the
	// protocol version is at least 16.
	IsReplay bool
}

// Type implements Message.
func (m *SvcServerInfo) Type() int { return protocol.SvcServerInfo }

// Name implements Message.
func (m *SvcServerInfo) Name() string { return "svc_ServerInfo" }

func serverInfoHasReplay(ctx *Context) bool {
	return ctx.ReplayEnabled && ctx.ProtocolVersion >= protocol.VersionReplay
}

// Decode implements Message.
func (m *SvcServerInfo) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.Protocol = r.ReadInt16()
	m.ServerCount = r.ReadInt32()
	m.IsHLTV = r.ReadBit()
	m.IsDedicated = r.ReadBit()
	m.ClientCRC = r.ReadInt32()
	m.MaxClasses = r.ReadUint16()
	if m.Protocol > protocol.Version17 {
		copy(m.MapMD5[:], r.ReadBytes(len(m.MapMD5)))
	} else {
		m.MapCRC = r.ReadUint32()
	}
	m.PlayerSlot = r.ReadUint8()
	m.MaxClients = r.ReadUint8()
	m.TickInterval = r.ReadFloat32()
	m.OS = r.ReadUint8()
	m.GameDir = r.ReadString()
	m.MapName = r.ReadString()
	m.SkyName = r.ReadString()
	m.HostName = r.ReadString()
	if serverInfoHasReplay(ctx) {
		m.IsReplay = r.ReadBit()
	}
	return r.Err()
}

// Encode implements Message.
func (m *SvcServerInfo) Encode(w *bitbuf.Writer, ctx *Context) error {
	w.WriteInt16(m.Protocol)
	w.WriteInt32(m.ServerCount)
	w.WriteBit(m.IsHLTV)
	w.WriteBit(m.IsDedicated)
	w.WriteInt32(m.ClientCRC)
	w.WriteUint16(m.MaxClasses)
	if m.Protocol > protocol.Version17 {
		w.WriteBytes(m.MapMD5[:])
	} else {
		w.WriteUint32(m.MapCRC)
	}
	w.WriteUint8(m.PlayerSlot)
	w.WriteUint8(m.MaxClients)
	w.WriteFloat32(m.TickInterval)
	w.WriteUint8(m.OS)
	w.WriteString(m.GameDir)
	w.WriteString(m.MapName)
	w.WriteString(m.SkyName)
	w.WriteString(m.HostName)
	if serverInfoHasReplay(ctx) {
		w.WriteBit(m.IsReplay)
	}
	return w.Err()
}

func (m *SvcServerInfo) String() string {
	return fmt.Sprintf("%s: game %q, map %q, max %d", m.Name(), m.GameDir, m.MapName, m.MaxClients)
}

// SvcSendTable describes one SendTable. Data holds the table encoding read
// by the datatable package.
type SvcSendTable struct {
	NeedsDecoder bool
	Data         Bits
}

// Type implements Message.
func (m *SvcSendTable) Type() int { return protocol.SvcSendTable }

// Name implements Message.
func (m *SvcSendTable) Name() string { return "svc_SendTable" }

// Decode implements Message.
func (m *SvcSendTable) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.NeedsDecoder = r.ReadBit()
	n := int(r.ReadInt16())
	if r.Overflowed() {
		return r.Err()
	}
	if n < 0 {
		return errNegativeLength
	}
	m.Data = readPayload(r, n)
	return r.Err()
}

// Encode implements Message.
func (m *SvcSendTable) Encode(w *bitbuf.Writer, ctx *Context) error {
	if err := fits("length", m.Data.N, 15); err != nil {
		return err
	}
	w.WriteBit(m.NeedsDecoder)
	w.WriteInt16(int16(m.Data.N))
	if err := writePayload(w, m.Data); err != nil {
		return err
	}
	return w.Err()
}

func (m *SvcSendTable) String() string {
	needs := "no"
	if m.NeedsDecoder {
		needs = "yes"
	}
	return fmt.Sprintf("%s: needs Decoder %s,bytes %d", m.Name(), needs, m.Data.Bytes())
}

// ClassInfo maps a server class ID to its class and datatable names.
type ClassInfo struct {
	ClassID       int
	ClassName     string
	DataTableName string
}

// SvcClassInfo lists the server classes. When CreateOnClient is set the
// client is expected to use its own class definitions and no list follows.
type SvcClassInfo struct {
	CreateOnClient bool
	// NumServerClasses is the class count on the wire. When encoding a full
	// list (CreateOnClient false) len(Classes) is used instead.
	NumServerClasses int
	Classes          []ClassInfo
}

// Type implements Message.
func (m *SvcClassInfo) Type() int { return protocol.SvcClassInfo }

// Name implements Message.
func (m *SvcClassInfo) Name() string { return "svc_ClassInfo" }

// Decode implements Message.
func (m *SvcClassInfo) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.NumServerClasses = int(r.ReadInt16())
	bits := log2(m.NumServerClasses) + 1
	m.CreateOnClient = r.ReadBit()
	m.Classes = nil
	if m.CreateOnClient {
		return r.Err()
	}
	if m.NumServerClasses < 0 {
		return errNegativeLength
	}
	for i := 0; i < m.NumServerClasses && !r.Overflowed(); i++ {
		var c ClassInfo
		c.ClassID = int(r.ReadBits(bits))
		c.ClassName = r.ReadString()
		c.DataTableName = r.ReadString()
		m.Classes = append(m.Classes, c)
	}
	return r.Err()
}

// Encode implements Message.
func (m *SvcClassInfo) Encode(w *bitbuf.Writer, ctx *Context) error {
	n := m.NumServerClasses
	if !m.CreateOnClient {
		n = len(m.Classes)
	}
	if err := fits("class count", n, 15); err != nil {
		return err
	}
	bits := log2(n) + 1
	w.WriteInt16(int16(n))
	w.WriteBit(m.CreateOnClient)
	if m.CreateOnClient {
		return w.Err()
	}
	for _, c := range m.Classes {
		if err := fits("class id", c.ClassID, bits); err != nil {
			return err
		}
		w.WriteBits(uint32(c.ClassID), bits)
		w.WriteString(c.ClassName)
		w.WriteString(c.DataTableName)
	}
	return w.Err()
}

func (m *SvcClassInfo) String() string {
	kind := "full update"
	if m.CreateOnClient {
		kind = "use client classes"
	}
	return fmt.Sprintf("%s: num %d, %s", m.Name(), m.NumServerClasses, kind)
}

// SvcSetPause pauses or unpauses the game.
type SvcSetPause struct {
	Paused bool
}

// Type implements Message.
func (m *SvcSetPause) Type() int { return protocol.SvcSetPause }

// Name implements Message.
func (m *SvcSetPause) Name() string { return "svc_SetPause" }

// Decode implements Message.
func (m *SvcSetPause) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.Paused = r.ReadBit()
	return r.Err()
}

// Encode implements Message.
func (m *SvcSetPause) Encode(w *bitbuf.Writer, ctx *Context) error {
	w.WriteBit(m.Paused)
	return w.Err()
}

func (m *SvcSetPause) String() string {
	if m.Paused {
		return m.Name() + ": paused"
	}
	return m.Name() + ": unpaused"
}

// SvcCreateStringTable creates a network string table and carries its
// initial entries.
type SvcCreateStringTable struct {
	// TableName excludes the ':' prefix that marks filename tables.
	TableName         string
	IsFilenames       bool
	MaxEntries        uint16
	NumEntries        int
	UserDataFixedSize bool
	UserDataSize      int
	UserDataSizeBits  int
	// DataCompressed is only on the wire for protocol versions above 14.
	DataCompressed bool
	Data           Bits
}

// Type implements Message.
func (m *SvcCreateStringTable) Type() int { return protocol.SvcCreateStringTable }

// Name implements Message.
func (m *SvcCreateStringTable) Name() string { return "svc_CreateStringTable" }

// Decode implements Message.
func (m *SvcCreateStringTable) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.IsFilenames = r.PeekBits(8) == ':'
	if m.IsFilenames {
		r.ReadUint8()
	}
	m.TableName = r.ReadString()
	m.MaxEntries = r.ReadUint16()
	m.NumEntries = int(r.ReadBits(log2(int(m.MaxEntries)) + 1))
	var n int
	if ctx.ProtocolVersion > protocol.Version23 {
		n = int(r.ReadVarInt32())
	} else {
		n = int(r.ReadBits(protocol.NetMaxPayloadBitsV23 + 3))
	}
	m.UserDataFixedSize = r.ReadBit()
	if m.UserDataFixedSize {
		m.UserDataSize = int(r.ReadBits(12))
		m.UserDataSizeBits = int(r.ReadBits(4))
	} else {
		m.UserDataSize = 0
		m.UserDataSizeBits = 0
	}
	m.DataCompressed = false
	if ctx.ProtocolVersion > protocol.Version14 {
		m.DataCompressed = r.ReadBit()
	}
	if r.Overflowed() {
		return r.Err()
	}
	m.Data = readPayload(r, n)
	return r.Err()
}

// Encode implements Message.
func (m *SvcCreateStringTable) Encode(w *bitbuf.Writer, ctx *Context) error {
	entryBits := log2(int(m.MaxEntries)) + 1
	if err := fits("entry count", m.NumEntries, entryBits); err != nil {
		return err
	}
	if m.IsFilenames {
		w.WriteUint8(':')
	}
	w.WriteString(m.TableName)
	w.WriteUint16(m.MaxEntries)
	w.WriteBits(uint32(m.NumEntries), entryBits)
	if ctx.ProtocolVersion > protocol.Version23 {
		w.WriteVarInt32(uint32(m.Data.N))
	} else {
		if err := fits("length", m.Data.N, protocol.NetMaxPayloadBitsV23+3); err != nil {
			return err
		}
		w.WriteBits(uint32(m.Data.N), protocol.NetMaxPayloadBitsV23+3)
	}
	w.WriteBit(m.UserDataFixedSize)
	if m.UserDataFixedSize {
		if err := fits("user data size", m.UserDataSize, 12); err != nil {
			return err
		}
		if err := fits("user data size bits", m.UserDataSizeBits, 4); err != nil {
			return err
		}
		w.WriteBits(uint32(m.UserDataSize), 12)
		w.WriteBits(uint32(m.UserDataSizeBits), 4)
	}
	if ctx.ProtocolVersion > protocol.Version14 {
		w.WriteBit(m.DataCompressed)
	}
	if err := writePayload(w, m.Data); err != nil {
		return err
	}
	return w.Err()
}

func (m *SvcCreateStringTable) String() string {
	return fmt.Sprintf("%s: table %s, entries %d, bytes %d userdatasize %d userdatabits %d",
		m.Name(), m.TableName, m.NumEntries, m.Data.Bytes(), m.UserDataSize, m.UserDataSizeBits)
}

// SvcUpdateStringTable carries changed entries of an existing string table.
type SvcUpdateStringTable struct {
	TableID        int
	ChangedEntries int
	Data           Bits
}

// Type implements Message.
func (m *SvcUpdateStringTable) Type() int { return protocol.SvcUpdateStringTable }

// Name implements Message.
func (m *SvcUpdateStringTable) Name() string { return "svc_UpdateStringTable" }

// Decode implements Message.
func (m *SvcUpdateStringTable) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.TableID = int(r.ReadBits(protocol.MaxTableBits))
	if r.ReadBit() {
		m.ChangedEntries = int(r.ReadUint16())
	} else {
		m.ChangedEntries = 1
	}
	n := int(r.ReadBits(20))
	if r.Overflowed() {
		return r.Err()
	}
	m.Data = readPayload(r, n)
	return r.Err()
}

// Encode implements Message.
func (m *SvcUpdateStringTable) Encode(w *bitbuf.Writer, ctx *Context) error {
	if err := fits("table id", m.TableID, protocol.MaxTableBits); err != nil {
		return err
	}
	if err := fits("changed entries", m.ChangedEntries, 16); err != nil {
		return err
	}
	if err := fits("length", m.Data.N, 20); err != nil {
		return err
	}
	w.WriteBits(uint32(m.TableID), protocol.MaxTableBits)
	if m.ChangedEntries == 1 {
		w.WriteBit(false)
	} else {
		w.WriteBit(true)
		w.WriteUint16(uint16(m.ChangedEntries))
	}
	w.WriteBits(uint32(m.Data.N), 20)
	if err := writePayload(w, m.Data); err != nil {
		return err
	}
	return w.Err()
}

func (m *SvcUpdateStringTable) String() string {
	return fmt.Sprintf("%s: table %d, changed %d, bytes %d", m.Name(), m.TableID, m.ChangedEntries, m.Data.Bytes())
}

// voiceInitCurrentQuality marks the current SvcVoiceInit layout, in which a
// sample rate follows the codec name.
const voiceInitCurrentQuality = 255

// SvcVoiceInit announces the voice codec used by the server.
type SvcVoiceInit struct {
	Codec string
	// Quality is a legacy field. 255 (or 0 when encoding) selects the current
	// layout with an explicit SampleRate; other values are legacy streams for
	// which SampleRate is derived from the codec when decoding.
	Quality    uint8
	SampleRate int
}

// Type implements Message.
func (m *SvcVoiceInit) Type() int { return protocol.SvcVoiceInit }

// Name implements Message.
func (m *SvcVoiceInit) Name() string { return "svc_VoiceInit" }

// Decode implements Message.
func (m *SvcVoiceInit) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.Codec = r.ReadString()
	m.Quality = r.ReadUint8()
	switch {
	case m.Quality == voiceInitCurrentQuality:
		m.SampleRate = int(r.ReadInt16())
	case strings.EqualFold(m.Codec, "vaudio_celt"):
		m.SampleRate = 22050
	default:
		m.SampleRate = 11025
	}
	return r.Err()
}

// Encode implements Message.
func (m *SvcVoiceInit) Encode(w *bitbuf.Writer, ctx *Context) error {
	q := m.Quality
	if q == 0 {
		q = voiceInitCurrentQuality
	}
	w.WriteString(m.Codec)
	w.WriteUint8(q)
	if q == voiceInitCurrentQuality {
		w.WriteInt16(int16(m.SampleRate))
	}
	return w.Err()
}

func (m *SvcVoiceInit) String() string {
	return fmt.Sprintf("%s: codec %q, sample rate %d", m.Name(), m.Codec, m.SampleRate)
}

// SvcVoiceData relays voice data from a client.
type SvcVoiceData struct {
	FromClient uint8
	Proximity  bool
	Data       Bits
}

// Type implements Message.
func (m *SvcVoiceData) Type() int { return protocol.SvcVoiceData }

// Name implements Message.
func (m *SvcVoiceData) Name() string { return "svc_VoiceData" }

// Decode implements Message.
func (m *SvcVoiceData) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.FromClient = r.ReadUint8()
	m.Proximity = r.ReadUint8() != 0
	n := int(r.ReadUint16())
	if r.Overflowed() {
		return r.Err()
	}
	m.Data = readPayload(r, n)
	return r.Err()
}

// Encode implements Message.
func (m *SvcVoiceData) Encode(w *bitbuf.Writer, ctx *Context) error {
	if err := fits("length", m.Data.N, 16); err != nil {
		return err
	}
	w.WriteUint8(m.FromClient)
	if m.Proximity {
		w.WriteUint8(1)
	} else {
		w.WriteUint8(0)
	}
	w.WriteUint16(uint16(m.Data.N))
	if err := writePayload(w, m.Data); err != nil {
		return err
	}
	return w.Err()
}

func (m *SvcVoiceData) String() string {
	return fmt.Sprintf("%s: client %d, bytes %d", m.Name(), m.FromClient, m.Data.Bytes())
}

// SvcSounds starts sounds. Data holds delta-encoded sound infos.
type SvcSounds struct {
	// Reliable messages carry exactly one sound.
	Reliable  bool
	NumSounds int
	Data      Bits
}

// Type implements Message.
func (m *SvcSounds) Type() int { return protocol.SvcSounds }

// Name implements Message.
func (m *SvcSounds) Name() string { return "svc_Sounds" }

// Decode implements Message.
func (m *SvcSounds) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.Reliable = r.ReadBit()
	var n int
	if m.Reliable {
		m.NumSounds = 1
		n = int(r.ReadBits(8))
	} else {
		m.NumSounds = int(r.ReadBits(8))
		n = int(r.ReadBits(16))
	}
	if r.Overflowed() {
		return r.Err()
	}
	m.Data = readPayload(r, n)
	return r.Err()
}

// Encode implements Message.
func (m *SvcSounds) Encode(w *bitbuf.Writer, ctx *Context) error {
	w.WriteBit(m.Reliable)
	if m.Reliable {
		if err := fits("length", m.Data.N, 8); err != nil {
			return err
		}
		w.WriteBits(uint32(m.Data.N), 8)
	} else {
		if err := fits("sound count", m.NumSounds, 8); err != nil {
			return err
		}
		if err := fits("length", m.Data.N, 16); err != nil {
			return err
		}
		w.WriteBits(uint32(m.NumSounds), 8)
		w.WriteBits(uint32(m.Data.N), 16)
	}
	if err := writePayload(w, m.Data); err != nil {
		return err
	}
	return w.Err()
}

func (m *SvcSounds) String() string {
	rel := ""
	if m.Reliable {
		rel = " reliable,"
	}
	return fmt.Sprintf("%s: number %d,%s bytes %d", m.Name(), m.NumSounds, rel, m.Data.Bytes())
}

// SvcSetView sets the entity the client views from.
type SvcSetView struct {
	EntityIndex int
}

// Type implements Message.
func (m *SvcSetView) Type() int { return protocol.SvcSetView }

// Name implements Message.
func (m *SvcSetView) Name() string { return "svc_SetView" }

// Decode implements Message.
func (m *SvcSetView) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.EntityIndex = int(r.ReadBits(protocol.MaxEdictBits))
	return r.Err()
}

// Encode implements Message.
func (m *SvcSetView) Encode(w *bitbuf.Writer, ctx *Context) error {
	if err := fits("entity index", m.EntityIndex, protocol.MaxEdictBits); err != nil {
		return err
	}
	w.WriteBits(uint32(m.EntityIndex), protocol.MaxEdictBits)
	return w.Err()
}

func (m *SvcSetView) String() string {
	return fmt.Sprintf("%s: view entity %d", m.Name(), m.EntityIndex)
}

// SvcFixAngle sets or corrects the player's view angles. Angles are
// quantised to 16 bits.
type SvcFixAngle struct {
	Relative bool
	Angle    [3]float32
}

// Type implements Message.
func (m *SvcFixAngle) Type() int { return protocol.SvcFixAngle }

// Name implements Message.
func (m *SvcFixAngle) Name() string { return "svc_FixAngle" }

// Decode implements Message.
func (m *SvcFixAngle) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.Relative = r.ReadBit()
	for i := range m.Angle {
		m.Angle[i] = r.ReadBitAngle(16)
	}
	return r.Err()
}

// Encode implements Message.
func (m *SvcFixAngle) Encode(w *bitbuf.Writer, ctx *Context) error {
	w.WriteBit(m.Relative)
	for _, a := range m.Angle {
		w.WriteBitAngle(a, 16)
	}
	return w.Err()
}

func (m *SvcFixAngle) String() string {
	kind := "absolute"
	if m.Relative {
		kind = "relative"
	}
	return fmt.Sprintf("%s: %s %.1f %.1f %.1f ", m.Name(), kind, m.Angle[0], m.Angle[1], m.Angle[2])
}

// SvcCrosshairAngle adjusts the crosshair in auto-aim mode. Angles are
// quantised to 16 bits.
type SvcCrosshairAngle struct {
	Angle [3]float32
}

// Type implements Message.
func (m *SvcCrosshairAngle) Type() int { return protocol.SvcCrosshairAngle }

// Name implements Message.
func (m *SvcCrosshairAngle) Name() string { return "svc_CrosshairAngle" }

// Decode implements Message.
func (m *SvcCrosshairAngle) Decode(r *bitbuf.Reader, ctx *Context) error {
	for i := range m.Angle {
		m.Angle[i] = r.ReadBitAngle(16)
	}
	return r.Err()
}

// Encode implements Message.
func (m *SvcCrosshairAngle) Encode(w *bitbuf.Writer, ctx *Context) error {
	for _, a := range m.Angle {
		w.WriteBitAngle(a, 16)
	}
	return w.Err()
}

func (m *SvcCrosshairAngle) String() string {
	return fmt.Sprintf("%s: (%.1f %.1f %.1f)", m.Name(), m.Angle[0], m.Angle[1], m.Angle[2])
}

// SvcBSPDecal places a static decal on the world or a brush entity.
type SvcBSPDecal struct {
	Pos               [3]float32
	DecalTextureIndex int
	// EntityIndex and ModelIndex are only sent when EntityIndex is non-zero.
	EntityIndex int
	ModelIndex  int
	LowPriority bool
}

// Type implements Message.
func (m *SvcBSPDecal) Type() int { return protocol.SvcBSPDecal }

// Name implements Message.
func (m *SvcBSPDecal) Name() string { return "svc_BSPDecal" }

// Decode implements Message.
func (m *SvcBSPDecal) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.Pos = r.ReadBitVec3Coord()
	m.DecalTextureIndex = int(r.ReadBits(protocol.MaxDecalIndexBits))
	if r.ReadBit() {
		m.EntityIndex = int(r.ReadBits(protocol.MaxEdictBits))
		m.ModelIndex = int(r.ReadBits(protocol.SPModelIndexBits))
	} else {
		m.EntityIndex = 0
		m.ModelIndex = 0
	}
	m.LowPriority = r.ReadBit()
	return r.Err()
}

// Encode implements Message.
func (m *SvcBSPDecal) Encode(w *bitbuf.Writer, ctx *Context) error {
	if err := fits("decal index", m.DecalTextureIndex, protocol.MaxDecalIndexBits); err != nil {
		return err
	}
	w.WriteBitVec3Coord(m.Pos)
	w.WriteBits(uint32(m.DecalTextureIndex), protocol.MaxDecalIndexBits)
	if m.EntityIndex != 0 {
		if err := fits("entity index", m.EntityIndex, protocol.MaxEdictBits); err != nil {
			return err
		}
		if err := fits("model index", m.ModelIndex, protocol.SPModelIndexBits); err != nil {
			return err
		}
		w.WriteBit(true)
		w.WriteBits(uint32(m.EntityIndex), protocol.MaxEdictBits)
		w.WriteBits(uint32(m.ModelIndex), protocol.SPModelIndexBits)
	} else {
		w.WriteBit(false)
	}
	w.WriteBit(m.LowPriority)
	return w.Err()
}

func (m *SvcBSPDecal) String() string {
	lp := 0
	if m.LowPriority {
		lp = 1
	}
	return fmt.Sprintf("%s: tex %d, ent %d, mod %d lowpriority %d", m.Name(), m.DecalTextureIndex, m.EntityIndex, m.ModelIndex, lp)
}

// SvcUserMessage carries a game-specific user message. MsgType indexes the
// game's user message registration order.
type SvcUserMessage struct {
	MsgType uint8
	Data    Bits
}

// Type implements Message.
func (m *SvcUserMessage) Type() int { return protocol.SvcUserMessage }

// Name implements Message.
func (m *SvcUserMessage) Name() string { return "svc_UserMessage" }

// Decode implements Message.
func (m *SvcUserMessage) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.MsgType = r.ReadUint8()
	n := int(r.ReadBits(protocol.NetMsgLengthBits))
	if r.Overflowed() {
		return r.Err()
	}
	m.Data = readPayload(r, n)
	return r.Err()
}

// Encode implements Message.
func (m *SvcUserMessage) Encode(w *bitbuf.Writer, ctx *Context) error {
	if err := fits("length", m.Data.N, protocol.NetMsgLengthBits); err != nil {
		return err
	}
	w.WriteUint8(m.MsgType)
	w.WriteBits(uint32(m.Data.N), protocol.NetMsgLengthBits)
	if err := writePayload(w, m.Data); err != nil {
		return err
	}
	return w.Err()
}

func (m *SvcUserMessage) String() string {
	return fmt.Sprintf("%s: type %d, bytes %d", m.Name(), m.MsgType, m.Data.Bytes())
}

// SvcEntityMessage carries a message for a client-side entity.
type SvcEntityMessage struct {
	EntityIndex int
	ClassID     int
	Data        Bits
}

// Type implements Message.
func (m *SvcEntityMessage) Type() int { return protocol.SvcEntityMessage }

// Name implements Message.
func (m *SvcEntityMessage) Name() string { return "svc_EntityMessage" }

// Decode implements Message.
func (m *SvcEntityMessage) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.EntityIndex = int(r.ReadBits(protocol.MaxEdictBits))
	m.ClassID = int(r.ReadBits(protocol.MaxServerClassBits))
	n := int(r.ReadBits(protocol.NetMsgLengthBits))
	if r.Overflowed() {
		return r.Err()
	}
	m.Data = readPayload(r, n)
	return r.Err()
}

// Encode implements Message.
func (m *SvcEntityMessage) Encode(w *bitbuf.Writer, ctx *Context) error {
	if err := fits("entity index", m.EntityIndex, protocol.MaxEdictBits); err != nil {
		return err
	}
	if err := fits("class id", m.ClassID, protocol.MaxServerClassBits); err != nil {
		return err
	}
	if err := fits("length", m.Data.N, protocol.NetMsgLengthBits); err != nil {
		return err
	}
	w.WriteBits(uint32(m.EntityIndex), protocol.MaxEdictBits)
	w.WriteBits(uint32(m.ClassID), protocol.MaxServerClassBits)
	w.WriteBits(uint32(m.Data.N), protocol.NetMsgLengthBits)
	if err := writePayload(w, m.Data); err != nil {
		return err
	}
	return w.Err()
}

func (m *SvcEntityMessage) String() string {
	return fmt.Sprintf("%s: entity %d, class %d, bytes %d", m.Name(), m.EntityIndex, m.ClassID, m.Data.Bytes())
}

// SvcGameEvent carries a serialized game event; see package gameevent.
type SvcGameEvent struct {
	Data Bits
}

// Type implements Message.
func (m *SvcGameEvent) Type() int { return protocol.SvcGameEvent }

// Name implements Message.
func (m *SvcGameEvent) Name() string { return "svc_GameEvent" }

// Decode implements Message.
func (m *SvcGameEvent) Decode(r *bitbuf.Reader, ctx *Context) error {
	n := int(r.ReadBits(protocol.NetMsgLengthBits))
	if r.Overflowed() {
		return r.Err()
	}
	m.Data = readPayload(r, n)
	return r.Err()
}

// Encode implements Message.
func (m *SvcGameEvent) Encode(w *bitbuf.Writer, ctx *Context) error {
	if err := fits("length", m.Data.N, protocol.NetMsgLengthBits); err != nil {
		return err
	}
	w.WriteBits(uint32(m.Data.N), protocol.NetMsgLengthBits)
	if err := writePayload(w, m.Data); err != nil {
		return err
	}
	return w.Err()
}

func (m *SvcGameEvent) String() string { return fmt.Sprintf("%s: bytes %d", m.Name(), m.Data.Bytes()) }

// SvcPacketEntities carries an entity snapshot, optionally delta compressed
// against an earlier tick acknowledged by the client.
type SvcPacketEntities struct {
	MaxEntries int
	IsDelta    bool
	// DeltaFrom is the tick the update is relative to, -1 for full updates.
	DeltaFrom      int32
	Baseline       int
	UpdatedEntries int
	UpdateBaseline bool
	Data           Bits
}

// Type implements Message.
func (m *SvcPacketEntities) Type() int { return protocol.SvcPacketEntities }

// Name implements Message.
func (m *SvcPacketEntities) Name() string { return "svc_PacketEntities" }

// Decode implements Message.
func (m *SvcPacketEntities) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.MaxEntries = int(r.ReadBits(protocol.MaxEdictBits))
	m.IsDelta = r.ReadBit()
	if m.IsDelta {
		m.DeltaFrom = r.ReadInt32()
	} else {
		m.DeltaFrom = -1
	}
	m.Baseline = int(r.ReadBits(1))
	m.UpdatedEntries = int(r.ReadBits(protocol.MaxEdictBits))
	n := int(r.ReadBits(protocol.DeltaSizeBits))
	m.UpdateBaseline = r.ReadBit()
	if r.Overflowed() {
		return r.Err()
	}
	m.Data = readPayload(r, n)
	return r.Err()
}

// Encode implements Message.
func (m *SvcPacketEntities) Encode(w *bitbuf.Writer, ctx *Context) error {
	if err := fits("max entries", m.MaxEntries, protocol.MaxEdictBits); err != nil {
		return err
	}
	if err := fits("baseline", m.Baseline, 1); err != nil {
		return err
	}
	if err := fits("updated entries", m.UpdatedEntries, protocol.MaxEdictBits); err != nil {
		return err
	}
	if err := fits("length", m.Data.N, protocol.DeltaSizeBits); err != nil {
		return err
	}
	w.WriteBits(uint32(m.MaxEntries), protocol.MaxEdictBits)
	w.WriteBit(m.IsDelta)
	if m.IsDelta {
		w.WriteInt32(m.DeltaFrom)
	}
	w.WriteBits(uint32(m.Baseline), 1)
	w.WriteBits(uint32(m.UpdatedEntries), protocol.MaxEdictBits)
	w.WriteBits(uint32(m.Data.N), protocol.DeltaSizeBits)
	w.WriteBit(m.UpdateBaseline)
	if err := writePayload(w, m.Data); err != nil {
		return err
	}
	return w.Err()
}

func (m *SvcPacketEntities) String() string {
	bl := ""
	if m.UpdateBaseline {
		bl = " BL update,"
	}
	return fmt.Sprintf("%s: delta %d, max %d, changed %d,%s bytes %d",
		m.Name(), m.DeltaFrom, m.MaxEntries, m.UpdatedEntries, bl, m.Data.Bytes())
}

// SvcTempEntities carries non-reliable temporary entity events.
type SvcTempEntities struct {
	NumEntries int
	Data       Bits
}

// Type implements Message.
func (m *SvcTempEntities) Type() int { return protocol.SvcTempEntities }

// Name implements Message.
func (m *SvcTempEntities) Name() string { return "svc_TempEntities" }

// Decode implements Message.
func (m *SvcTempEntities) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.NumEntries = int(r.ReadBits(protocol.EventIndexBits))
	var n int
	if ctx.ProtocolVersion > protocol.Version23 {
		n = int(r.ReadVarInt32())
	} else {
		n = int(r.ReadBits(protocol.NetMaxPayloadBitsV23))
	}
	if r.Overflowed() {
		return r.Err()
	}
	m.Data = readPayload(r, n)
	return r.Err()
}

// Encode implements Message.
func (m *SvcTempEntities) Encode(w *bitbuf.Writer, ctx *Context) error {
	if err := fits("entry count", m.NumEntries, protocol.EventIndexBits); err != nil {
		return err
	}
	w.WriteBits(uint32(m.NumEntries), protocol.EventIndexBits)
	if ctx.ProtocolVersion > protocol.Version23 {
		w.WriteVarInt32(uint32(m.Data.N))
	} else {
		if err := fits("length", m.Data.N, protocol.NetMaxPayloadBitsV23); err != nil {
			return err
		}
		w.WriteBits(uint32(m.Data.N), protocol.NetMaxPayloadBitsV23)
	}
	if err := writePayload(w, m.Data); err != nil {
		return err
	}
	return w.Err()
}

func (m *SvcTempEntities) String() string {
	return fmt.Sprintf("%s: number %d, bytes %d", m.Name(), m.NumEntries, m.Data.Bytes())
}

// SvcPrefetch asks the client to precache a sound.
type SvcPrefetch struct {
	SoundIndex int
}

// Type implements Message.
func (m *SvcPrefetch) Type() int { return protocol.SvcPrefetch }

// Name implements Message.
func (m *SvcPrefetch) Name() string { return "svc_Prefetch" }

func prefetchBits(ctx *Context) int {
	if ctx.ProtocolVersion > protocol.Version22 {
		return protocol.MaxSoundIndexBits
	}
	return protocol.MaxSoundIndexBitsV22
}

// Decode implements Message.
func (m *SvcPrefetch) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.SoundIndex = int(r.ReadBits(prefetchBits(ctx)))
	return r.Err()
}

// Encode implements Message.
func (m *SvcPrefetch) Encode(w *bitbuf.Writer, ctx *Context) error {
	n := prefetchBits(ctx)
	if err := fits("sound index", m.SoundIndex, n); err != nil {
		return err
	}
	w.WriteBits(uint32(m.SoundIndex), n)
	return w.Err()
}

func (m *SvcPrefetch) String() string {
	return fmt.Sprintf("%s: type 0 index %d", m.Name(), m.SoundIndex)
}

// SvcMenu displays a plugin menu. Data is a binary KeyValues buffer.
type SvcMenu struct {
	DialogType int16
	Data       []byte
}

// Type implements Message.
func (m *SvcMenu) Type() int { return protocol.SvcMenu }

// Name implements Message.
func (m *SvcMenu) Name() string { return "svc_Menu" }

// Decode implements Message.
func (m *SvcMenu) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.DialogType = r.ReadInt16()
	n := int(r.ReadUint16())
	if r.Overflowed() {
		return r.Err()
	}
	m.Data = r.ReadBytes(n)
	return r.Err()
}

// Encode implements Message.
func (m *SvcMenu) Encode(w *bitbuf.Writer, ctx *Context) error {
	if err := fits("length", len(m.Data), 16); err != nil {
		return err
	}
	w.WriteInt16(m.DialogType)
	w.WriteUint16(uint16(len(m.Data)))
	w.WriteBytes(m.Data)
	return w.Err()
}

func (m *SvcMenu) String() string {
	return fmt.Sprintf("%s: %d (len:%d)", m.Name(), m.DialogType, len(m.Data))
}

// SvcGameEventList describes the game events the server can send; see
// package gameevent.
type SvcGameEventList struct {
	NumEvents int
	Data      Bits
}

// Type implements Message.
func (m *SvcGameEventList) Type() int { return protocol.SvcGameEventList }

// Name implements Message.
func (m *SvcGameEventList) Name() string { return "svc_GameEventList" }

// Decode implements Message.
func (m *SvcGameEventList) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.NumEvents = int(r.ReadBits(protocol.MaxEventBits))
	n := int(r.ReadBits(20))
	if r.Overflowed() {
		return r.Err()
	}
	m.Data = readPayload(r, n)
	return r.Err()
}

// Encode implements Message.
func (m *SvcGameEventList) Encode(w *bitbuf.Writer, ctx *Context) error {
	if err := fits("event count", m.NumEvents, protocol.MaxEventBits); err != nil {
		return err
	}
	if err := fits("length", m.Data.N, 20); err != nil {
		return err
	}
	w.WriteBits(uint32(m.NumEvents), protocol.MaxEventBits)
	w.WriteBits(uint32(m.Data.N), 20)
	if err := writePayload(w, m.Data); err != nil {
		return err
	}
	return w.Err()
}

func (m *SvcGameEventList) String() string {
	return fmt.Sprintf("%s: number %d, bytes %d", m.Name(), m.NumEvents, m.Data.Bytes())
}

// SvcGetCvarValue asks the client for the value of a console variable; the
// client answers with ClcRespondCvarValue using the same cookie.
type SvcGetCvarValue struct {
	Cookie   int32
	CvarName string
}

// Type implements Message.
func (m *SvcGetCvarValue) Type() int { return protocol.SvcGetCvarValue }

// Name implements Message.
func (m *SvcGetCvarValue) Name() string { return "svc_GetCvarValue" }

// Decode implements Message.
func (m *SvcGetCvarValue) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.Cookie = r.ReadSBits(32)
	m.CvarName = r.ReadString()
	return r.Err()
}

// Encode implements Message.
func (m *SvcGetCvarValue) Encode(w *bitbuf.Writer, ctx *Context) error {
	w.WriteSBits(m.Cookie, 32)
	w.WriteString(m.CvarName)
	return w.Err()
}

func (m *SvcGetCvarValue) String() string {
	return fmt.Sprintf("%s: cvar: %s, cookie: %d", m.Name(), m.CvarName, m.Cookie)
}

var errKeyValuesLength = errors.New("invalid KeyValues length")

func decodeKeyValues(r *bitbuf.Reader) ([]byte, error) {
	n := int(r.ReadInt32())
	if r.Overflowed() {
		return nil, r.Err()
	}
	if n <= 0 || n > r.BytesLeft() {
		return nil, errKeyValuesLength
	}
	data := r.ReadBytes(n)
	return data, r.Err()
}

func encodeKeyValues(w *bitbuf.Writer, data []byte) error {
	if len(data) == 0 {
		return errKeyValuesLength
	}
	w.WriteInt32(int32(len(data)))
	w.WriteBytes(data)
	return w.Err()
}

func keyValuesString(name string, data []byte) string {
	n := len(data)
	if n > 16 {
		n = 16
	}
	return fmt.Sprintf("%s: %d bytes [%s]", name, len(data), hex.EncodeToString(data[:n]))
}

// SvcCmdKeyValues sends a KeyValues command to the client. Data is a binary
// KeyValues buffer.
type SvcCmdKeyValues struct {
	Data []byte
}

// Type implements Message.
func (m *SvcCmdKeyValues) Type() int { return protocol.SvcCmdKeyValues }

// Name implements Message.
func (m *SvcCmdKeyValues) Name() string { return "svc_CmdKeyValues" }

// Decode implements Message.
func (m *SvcCmdKeyValues) Decode(r *bitbuf.Reader, ctx *Context) error {
	var err error
	m.Data, err = decodeKeyValues(r)
	return err
}

// Encode implements Message.
func (m *SvcCmdKeyValues) Encode(w *bitbuf.Writer, ctx *Context) error {
	return encodeKeyValues(w, m.Data)
}

func (m *SvcCmdKeyValues) String() string { return keyValuesString(m.Name(), m.Data) }

// SvcSetPauseTimed pauses the game until ExpireTime.
type SvcSetPauseTimed struct {
	Paused     bool
	ExpireTime float32
}

// Type implements Message.
func (m *SvcSetPauseTimed) Type() int { return protocol.SvcSetPauseTimed }

// Name implements Message.
func (m *SvcSetPauseTimed) Name() string { return "svc_SetPauseTimed" }

// Decode implements Message.
func (m *SvcSetPauseTimed) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.Paused = r.ReadBit()
	m.ExpireTime = r.ReadFloat32()
	return r.Err()
}

// Encode implements Message.
func (m *SvcSetPauseTimed) Encode(w *bitbuf.Writer, ctx *Context) error {
	w.WriteBit(m.Paused)
	w.WriteFloat32(m.ExpireTime)
	return w.Err()
}

func (m *SvcSetPauseTimed) String() string {
	if m.Paused {
		return m.Name() + ": paused"
	}
	return m.Name() + ": unpaused"
}
