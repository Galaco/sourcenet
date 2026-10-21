package message

import (
	"errors"
	"fmt"
	"strings"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/protocol"
)

// ClcClientInfo is sent by the client once it has entered signon state NEW.
type ClcClientInfo struct {
	ServerCount int32
	// SendTableCRC must match the server's SendTable CRC, or the server will
	// disconnect the client unless it is configured to send its tables.
	SendTableCRC uint32
	IsHLTV       bool
	FriendsID    uint32
	FriendsName  string
	// CustomFiles holds the CRCs of the client's customization files (spray
	// etc.), 0 for unused slots.
	CustomFiles [protocol.MaxCustomFiles]uint32
	// IsReplay is only on the wire when Context.ReplayEnabled is set.
	IsReplay bool
}

// Type implements Message.
func (m *ClcClientInfo) Type() int { return protocol.ClcClientInfo }

// Name implements Message.
func (m *ClcClientInfo) Name() string { return "clc_ClientInfo" }

// Decode implements Message.
func (m *ClcClientInfo) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.ServerCount = r.ReadInt32()
	m.SendTableCRC = r.ReadUint32()
	m.IsHLTV = r.ReadBit()
	m.FriendsID = r.ReadUint32()
	m.FriendsName = r.ReadString()
	for i := range m.CustomFiles {
		if r.ReadBit() {
			m.CustomFiles[i] = r.ReadUint32()
		} else {
			m.CustomFiles[i] = 0
		}
	}
	if ctx.ReplayEnabled {
		m.IsReplay = r.ReadBit()
	}
	return r.Err()
}

// Encode implements Message.
func (m *ClcClientInfo) Encode(w *bitbuf.Writer, ctx *Context) error {
	w.WriteInt32(m.ServerCount)
	w.WriteUint32(m.SendTableCRC)
	w.WriteBit(m.IsHLTV)
	w.WriteUint32(m.FriendsID)
	w.WriteString(m.FriendsName)
	for _, crc := range m.CustomFiles {
		if crc != 0 {
			w.WriteBit(true)
			w.WriteUint32(crc)
		} else {
			w.WriteBit(false)
		}
	}
	if ctx.ReplayEnabled {
		w.WriteBit(m.IsReplay)
	}
	return w.Err()
}

func (m *ClcClientInfo) String() string {
	return fmt.Sprintf("%s: SendTableCRC %d", m.Name(), int32(m.SendTableCRC))
}

// ClcMove carries delta-compressed user commands; see package usercmd.
type ClcMove struct {
	NewCommands    int
	BackupCommands int
	Data           Bits
}

// Type implements Message.
func (m *ClcMove) Type() int { return protocol.ClcMove }

// Name implements Message.
func (m *ClcMove) Name() string { return "clc_Move" }

// Decode implements Message.
func (m *ClcMove) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.NewCommands = int(r.ReadBits(protocol.NumNewCommandBits))
	m.BackupCommands = int(r.ReadBits(protocol.NumBackupCommandBits))
	n := int(r.ReadUint16())
	if r.Overflowed() {
		return r.Err()
	}
	m.Data = readPayload(r, n)
	return r.Err()
}

// Encode implements Message.
func (m *ClcMove) Encode(w *bitbuf.Writer, ctx *Context) error {
	if err := fits("new commands", m.NewCommands, protocol.NumNewCommandBits); err != nil {
		return err
	}
	if err := fits("backup commands", m.BackupCommands, protocol.NumBackupCommandBits); err != nil {
		return err
	}
	if err := fits("length", m.Data.N, 16); err != nil {
		return err
	}
	w.WriteBits(uint32(m.NewCommands), protocol.NumNewCommandBits)
	w.WriteBits(uint32(m.BackupCommands), protocol.NumBackupCommandBits)
	w.WriteUint16(uint16(m.Data.N))
	if err := writePayload(w, m.Data); err != nil {
		return err
	}
	return w.Err()
}

func (m *ClcMove) String() string {
	return fmt.Sprintf("%s: backup %d, new %d, bytes %d", m.Name(), m.BackupCommands, m.NewCommands, m.Data.Bytes())
}

// ClcVoiceData carries the client's voice data.
type ClcVoiceData struct {
	Data Bits
}

// Type implements Message.
func (m *ClcVoiceData) Type() int { return protocol.ClcVoiceData }

// Name implements Message.
func (m *ClcVoiceData) Name() string { return "clc_VoiceData" }

// Decode implements Message.
func (m *ClcVoiceData) Decode(r *bitbuf.Reader, ctx *Context) error {
	n := int(r.ReadUint16())
	if r.Overflowed() {
		return r.Err()
	}
	m.Data = readPayload(r, n)
	return r.Err()
}

// Encode implements Message.
func (m *ClcVoiceData) Encode(w *bitbuf.Writer, ctx *Context) error {
	if err := fits("length", m.Data.N, 16); err != nil {
		return err
	}
	w.WriteUint16(uint16(m.Data.N))
	if err := writePayload(w, m.Data); err != nil {
		return err
	}
	return w.Err()
}

func (m *ClcVoiceData) String() string { return fmt.Sprintf("%s: %d bytes", m.Name(), m.Data.Bytes()) }

// ClcBaselineAck acknowledges an entity baseline update.
type ClcBaselineAck struct {
	BaselineTick int32
	// BaselineNr is the baseline index, 0 or 1.
	BaselineNr int
}

// Type implements Message.
func (m *ClcBaselineAck) Type() int { return protocol.ClcBaselineAck }

// Name implements Message.
func (m *ClcBaselineAck) Name() string { return "clc_BaselineAck" }

// Decode implements Message.
func (m *ClcBaselineAck) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.BaselineTick = r.ReadInt32()
	m.BaselineNr = int(r.ReadBits(1))
	return r.Err()
}

// Encode implements Message.
func (m *ClcBaselineAck) Encode(w *bitbuf.Writer, ctx *Context) error {
	if err := fits("baseline number", m.BaselineNr, 1); err != nil {
		return err
	}
	w.WriteInt32(m.BaselineTick)
	w.WriteBits(uint32(m.BaselineNr), 1)
	return w.Err()
}

func (m *ClcBaselineAck) String() string { return fmt.Sprintf("%s: tick %d", m.Name(), m.BaselineTick) }

// ClcListenEvents tells the server which game events the client wants. Bit
// i of the mask corresponds to the game event with ID i.
type ClcListenEvents struct {
	Mask [protocol.MaxEventNumber / 32]uint32
}

// Type implements Message.
func (m *ClcListenEvents) Type() int { return protocol.ClcListenEvents }

// Name implements Message.
func (m *ClcListenEvents) Name() string { return "clc_ListenEvents" }

// Set marks the event with the given ID as wanted. Out of range IDs are
// ignored.
func (m *ClcListenEvents) Set(id int) {
	if id >= 0 && id < protocol.MaxEventNumber {
		m.Mask[id>>5] |= 1 << uint(id&31)
	}
}

// IsSet reports whether the event with the given ID is wanted.
func (m *ClcListenEvents) IsSet(id int) bool {
	if id < 0 || id >= protocol.MaxEventNumber {
		return false
	}
	return m.Mask[id>>5]&(1<<uint(id&31)) != 0
}

// Decode implements Message.
func (m *ClcListenEvents) Decode(r *bitbuf.Reader, ctx *Context) error {
	for i := range m.Mask {
		m.Mask[i] = r.ReadUint32()
	}
	return r.Err()
}

// Encode implements Message.
func (m *ClcListenEvents) Encode(w *bitbuf.Writer, ctx *Context) error {
	for _, v := range m.Mask {
		w.WriteUint32(v)
	}
	return w.Err()
}

func (m *ClcListenEvents) String() string {
	count := 0
	for i := 0; i < protocol.MaxEventNumber; i++ {
		if m.IsSet(i) {
			count++
		}
	}
	return fmt.Sprintf("%s: registered events %d", m.Name(), count)
}

// Cvar query status codes for ClcRespondCvarValue.
const (
	CvarQueryValueIntact = 0
	CvarQueryNotFound    = 1
	CvarQueryNotACvar    = 2
	CvarQueryProtected   = 3
)

// ClcRespondCvarValue answers SvcGetCvarValue.
type ClcRespondCvarValue struct {
	Cookie int32
	// Status is one of the CvarQuery constants; it is encoded as a signed
	// 4-bit value.
	Status    int32
	CvarName  string
	CvarValue string
}

// Type implements Message.
func (m *ClcRespondCvarValue) Type() int { return protocol.ClcRespondCvarValue }

// Name implements Message.
func (m *ClcRespondCvarValue) Name() string { return "clc_RespondCvarValue" }

// Decode implements Message.
func (m *ClcRespondCvarValue) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.Cookie = r.ReadSBits(32)
	m.Status = r.ReadSBits(4)
	m.CvarName = r.ReadString()
	m.CvarValue = r.ReadString()
	return r.Err()
}

// Encode implements Message.
func (m *ClcRespondCvarValue) Encode(w *bitbuf.Writer, ctx *Context) error {
	if m.Status < -8 || m.Status > 7 {
		return fmt.Errorf("status %d does not fit in 4 signed bits", m.Status)
	}
	w.WriteSBits(m.Cookie, 32)
	w.WriteSBits(m.Status, 4)
	w.WriteString(m.CvarName)
	w.WriteString(m.CvarValue)
	return w.Err()
}

func (m *ClcRespondCvarValue) String() string {
	return fmt.Sprintf("%s: status: %d, value: %s, cookie: %d", m.Name(), m.Status, m.CvarValue, m.Cookie)
}

// Path IDs and directory prefixes abbreviated by the file check messages.
var (
	commonPathIDs   = []string{"GAME", "MOD"}
	commonPrefixes  = []string{"materials", "models", "sounds", "scripts"}
	errInvalidCode  = errors.New("invalid path ID or prefix code")
	fileCRCCheckNew = byte(1) // marker byte of the current clc_FileCRCCheck layout
)

func findCommonPathID(pathID string) int {
	for i, p := range commonPathIDs {
		if strings.EqualFold(p, pathID) {
			return i
		}
	}
	return -1
}

func findCommonPrefix(filename string) int {
	for i, p := range commonPrefixes {
		if len(filename) > len(p) && strings.EqualFold(filename[:len(p)], p) {
			if c := filename[len(p)]; c == '/' || c == '\\' {
				return i
			}
		}
	}
	return -1
}

func writePathID(w *bitbuf.Writer, pathID string) {
	if code := findCommonPathID(pathID); code >= 0 {
		w.WriteBits(uint32(code+1), 2)
	} else {
		w.WriteBits(0, 2)
		w.WriteString(pathID)
	}
}

func readPathID(r *bitbuf.Reader) (string, error) {
	code := int(r.ReadBits(2))
	if code == 0 {
		return r.ReadString(), nil
	}
	if code-1 >= len(commonPathIDs) {
		return "", errInvalidCode
	}
	return commonPathIDs[code-1], nil
}

// splitPrefix returns the prefix code and the filename without the prefix.
func splitPrefix(filename string) (int, string) {
	code := findCommonPrefix(filename)
	if code < 0 {
		return 0, filename
	}
	return code + 1, filename[len(commonPrefixes[code])+1:]
}

func joinPrefix(code int, rest string) (string, error) {
	if code == 0 {
		return rest, nil
	}
	if code-1 >= len(commonPrefixes) {
		return "", errInvalidCode
	}
	return commonPrefixes[code-1] + "/" + rest, nil
}

// ClcFileCRCCheck reports the hash of a file to a server enforcing sv_pure.
// Prefixed filenames are decoded with '/' as the path separator.
type ClcFileCRCCheck struct {
	PathID         string
	Filename       string
	MD5            [16]byte
	CRCIOs         uint32
	FileHashType   int32
	FileLen        int32
	PackFileNumber int32
	PackFileID     int32
	FileFraction   int32
	// Legacy is set when a message in the old layout (CRC instead of MD5) was
	// decoded; LegacyCRC holds its CRC. Encoding always uses the new layout.
	Legacy    bool
	LegacyCRC uint32
}

// Type implements Message.
func (m *ClcFileCRCCheck) Type() int { return protocol.ClcFileCRCCheck }

// Name implements Message.
func (m *ClcFileCRCCheck) Name() string { return "clc_FileCRCCheck" }

// Decode implements Message.
func (m *ClcFileCRCCheck) Decode(r *bitbuf.Reader, ctx *Context) error {
	r.ReadBit() // reserved
	var err error
	if m.PathID, err = readPathID(r); err != nil {
		return err
	}
	code := int(r.ReadBits(3))
	var name string
	c := r.ReadUint8()
	if c == fileCRCCheckNew {
		m.Legacy = false
		name = r.ReadString()
	} else {
		m.Legacy = true
		rest := r.ReadString()
		if c != 0 {
			name = string([]byte{c}) + rest
		}
	}
	if m.Filename, err = joinPrefix(code, name); err != nil {
		return err
	}
	if m.Legacy {
		m.LegacyCRC = r.ReadUint32()
		m.CRCIOs = r.ReadUint32()
		m.FileHashType = r.ReadInt32()
		return r.Err()
	}
	copy(m.MD5[:], r.ReadBytes(len(m.MD5)))
	m.CRCIOs = r.ReadUint32()
	m.FileHashType = r.ReadInt32()
	m.FileLen = r.ReadInt32()
	m.PackFileNumber = r.ReadInt32()
	m.PackFileID = r.ReadInt32()
	m.FileFraction = r.ReadInt32()
	return r.Err()
}

// Encode implements Message.
func (m *ClcFileCRCCheck) Encode(w *bitbuf.Writer, ctx *Context) error {
	w.WriteBit(false)
	writePathID(w, m.PathID)
	code, rest := splitPrefix(m.Filename)
	w.WriteBits(uint32(code), 3)
	w.WriteUint8(fileCRCCheckNew)
	w.WriteString(rest)
	w.WriteBytes(m.MD5[:])
	w.WriteUint32(m.CRCIOs)
	w.WriteInt32(m.FileHashType)
	w.WriteInt32(m.FileLen)
	w.WriteInt32(m.PackFileNumber)
	w.WriteInt32(m.PackFileID)
	w.WriteInt32(m.FileFraction)
	return w.Err()
}

func (m *ClcFileCRCCheck) String() string {
	return fmt.Sprintf("%s: path: %s, file: %s", m.Name(), m.PathID, m.Filename)
}

// ClcFileMD5Check reports the MD5 of a file to the server.
type ClcFileMD5Check struct {
	PathID   string
	Filename string
	MD5      [16]byte
}

// Type implements Message.
func (m *ClcFileMD5Check) Type() int { return protocol.ClcFileMD5Check }

// Name implements Message.
func (m *ClcFileMD5Check) Name() string { return "clc_FileMD5Check" }

// Decode implements Message.
func (m *ClcFileMD5Check) Decode(r *bitbuf.Reader, ctx *Context) error {
	r.ReadBit() // reserved
	var err error
	if m.PathID, err = readPathID(r); err != nil {
		return err
	}
	code := int(r.ReadBits(3))
	if m.Filename, err = joinPrefix(code, r.ReadString()); err != nil {
		return err
	}
	copy(m.MD5[:], r.ReadBytes(len(m.MD5)))
	return r.Err()
}

// Encode implements Message.
func (m *ClcFileMD5Check) Encode(w *bitbuf.Writer, ctx *Context) error {
	w.WriteBit(false)
	writePathID(w, m.PathID)
	code, rest := splitPrefix(m.Filename)
	w.WriteBits(uint32(code), 3)
	w.WriteString(rest)
	w.WriteBytes(m.MD5[:])
	return w.Err()
}

func (m *ClcFileMD5Check) String() string {
	return fmt.Sprintf("%s: path: %s, file: %s", m.Name(), m.PathID, m.Filename)
}

// ClcSaveReplay asks the server to save a replay.
type ClcSaveReplay struct {
	Filename string
	// StartSendByte is sent with only 4 bits: the engine passes
	// sizeof(int) as the bit count. Only the low 4 bits survive.
	StartSendByte       int32
	PostDeathRecordTime float32
}

// Type implements Message.
func (m *ClcSaveReplay) Type() int { return protocol.ClcSaveReplay }

// Name implements Message.
func (m *ClcSaveReplay) Name() string { return "clc_SaveReplay" }

const saveReplayStartBits = 4

// Decode implements Message.
func (m *ClcSaveReplay) Decode(r *bitbuf.Reader, ctx *Context) error {
	m.Filename = r.ReadString()
	m.StartSendByte = int32(r.ReadBits(saveReplayStartBits))
	m.PostDeathRecordTime = r.ReadFloat32()
	return r.Err()
}

// Encode implements Message.
func (m *ClcSaveReplay) Encode(w *bitbuf.Writer, ctx *Context) error {
	w.WriteString(m.Filename)
	w.WriteBits(uint32(m.StartSendByte), saveReplayStartBits)
	w.WriteFloat32(m.PostDeathRecordTime)
	return w.Err()
}

func (m *ClcSaveReplay) String() string {
	return fmt.Sprintf("%s: filename: %s, start byte: %d, post death record time: %f",
		m.Name(), m.Filename, m.StartSendByte, m.PostDeathRecordTime)
}

// ClcCmdKeyValues sends a KeyValues command to the server. Data is a binary
// KeyValues buffer.
type ClcCmdKeyValues struct {
	Data []byte
}

// Type implements Message.
func (m *ClcCmdKeyValues) Type() int { return protocol.ClcCmdKeyValues }

// Name implements Message.
func (m *ClcCmdKeyValues) Name() string { return "clc_CmdKeyValues" }

// Decode implements Message.
func (m *ClcCmdKeyValues) Decode(r *bitbuf.Reader, ctx *Context) error {
	var err error
	m.Data, err = decodeKeyValues(r)
	return err
}

// Encode implements Message.
func (m *ClcCmdKeyValues) Encode(w *bitbuf.Writer, ctx *Context) error {
	return encodeKeyValues(w, m.Data)
}

func (m *ClcCmdKeyValues) String() string { return keyValuesString(m.Name(), m.Data) }
