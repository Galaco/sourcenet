package stringtable

import (
	"encoding/binary"
	"fmt"

	"github.com/galaco/sourcenet/protocol"
)

// Layout of player_info_t (public/cdll_int.h) as stored in the userinfo
// table. The server converts the struct to little endian before networking
// it. Every multi-byte member is 4 bytes wide (CRC32_t is an unsigned int),
// so x86 and x64 builds on Windows and Linux share one layout; the optional
// isreplay flag fits into existing padding and does not move later members.
const (
	piName            = 0
	piUserID          = 32
	piGUID            = 36
	piFriendsID       = 72
	piFriendsName     = 76
	piFakePlayer      = 108
	piIsHLTV          = 109
	piIsReplay        = 110
	piCustomFiles     = 112
	piFilesDownloaded = 128
	// PlayerInfoSize is sizeof(player_info_t).
	PlayerInfoSize = 132
)

// PlayerInfo is the decoded user data of a userinfo table entry. The entry
// index is the player's client slot (entity index - 1).
type PlayerInfo struct {
	Name            string
	UserID          int32
	GUID            string // network ID, e.g. "[U:1:12345]" or "BOT"
	FriendsID       uint32 // Steam account ID
	FriendsName     string
	FakePlayer      bool // bot
	IsHLTV          bool // SourceTV proxy
	IsReplay        bool // replay proxy; only networked by replay-enabled engines
	CustomFiles     [protocol.MaxCustomFiles]uint32
	FilesDownloaded uint8
}

// ParsePlayerInfo decodes userinfo table user data. replayEnabled selects
// whether the engine was built with REPLAY_ENABLED (TF2 and CS:S are).
func ParsePlayerInfo(data []byte, replayEnabled bool) (PlayerInfo, error) {
	var p PlayerInfo
	if len(data) < piFilesDownloaded+1 {
		return p, fmt.Errorf("stringtable: player info is %d bytes, need %d", len(data), piFilesDownloaded+1)
	}
	p.Name = cString(data[piName : piName+protocol.MaxPlayerNameLength])
	p.UserID = int32(binary.LittleEndian.Uint32(data[piUserID:]))
	p.GUID = cString(data[piGUID : piGUID+protocol.SignedGUIDLen+1])
	p.FriendsID = binary.LittleEndian.Uint32(data[piFriendsID:])
	p.FriendsName = cString(data[piFriendsName : piFriendsName+protocol.MaxPlayerNameLength])
	p.FakePlayer = data[piFakePlayer] != 0
	p.IsHLTV = data[piIsHLTV] != 0
	if replayEnabled {
		p.IsReplay = data[piIsReplay] != 0
	}
	for i := range p.CustomFiles {
		p.CustomFiles[i] = binary.LittleEndian.Uint32(data[piCustomFiles+4*i:])
	}
	p.FilesDownloaded = data[piFilesDownloaded]
	return p, nil
}

// Marshal encodes the player info as userinfo table user data.
func (p PlayerInfo) Marshal(replayEnabled bool) []byte {
	b := make([]byte, PlayerInfoSize)
	putCString(b[piName:piName+protocol.MaxPlayerNameLength], p.Name)
	binary.LittleEndian.PutUint32(b[piUserID:], uint32(p.UserID))
	putCString(b[piGUID:piGUID+protocol.SignedGUIDLen+1], p.GUID)
	binary.LittleEndian.PutUint32(b[piFriendsID:], p.FriendsID)
	putCString(b[piFriendsName:piFriendsName+protocol.MaxPlayerNameLength], p.FriendsName)
	b[piFakePlayer] = boolByte(p.FakePlayer)
	b[piIsHLTV] = boolByte(p.IsHLTV)
	if replayEnabled {
		b[piIsReplay] = boolByte(p.IsReplay)
	}
	for i, crc := range p.CustomFiles {
		binary.LittleEndian.PutUint32(b[piCustomFiles+4*i:], crc)
	}
	b[piFilesDownloaded] = p.FilesDownloaded
	return b
}

func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// putCString copies s into a fixed buffer, always leaving room for a NUL.
func putCString(dst []byte, s string) {
	n := copy(dst[:len(dst)-1], s)
	for i := n; i < len(dst); i++ {
		dst[i] = 0
	}
}

func boolByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}
