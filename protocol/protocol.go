// Package protocol defines the constants of the Source Engine network
// protocol shared by every layer of sourcenet: wire limits, packet framing
// headers, netchannel flags, signon states and message type identifiers.
//
// Values correspond to network protocol version 24, used by Team Fortress 2,
// Counter-Strike: Source, Day of Defeat: Source and Half-Life 2: Deathmatch
// since the 2013 SteamPipe engine update.
package protocol

// Version is the network protocol version spoken by this package.
const Version = 24

// Historic protocol versions that change message encodings.
const (
	Version23     = 23 // last version with 17-bit payload lengths
	Version22     = 22 // last version with 13-bit sound indices
	Version17     = 17 // last version with a map CRC instead of an MD5
	Version14     = 14 // last version without string table compression flag
	VersionReplay = 16 // first version with replay flags
)

// Packet framing headers: the first 32-bit little-endian word of a datagram.
const (
	HeaderConnectionless int32 = -1 // out-of-band packet
	HeaderSplit          int32 = -2 // fragment of a larger datagram
	HeaderCompressed     int32 = -3 // LZSS/Snappy compressed datagram
)

// Wire size limits.
const (
	MaxRoutablePayload     = 1260   // default split size (net_maxroutable)
	MinUserRoutablePayload = 576    // smallest permitted split size
	MinRoutablePayload     = 16     // datagrams are padded to at least this size
	NetMaxPayload          = 288000 // largest message in bytes
	NetMaxPayloadV23       = 96000  // largest message for protocol <= 23
	NetMaxPayloadBitsV23   = 17
	NetMaxDatagramPayload  = 4000 // largest unreliable payload
	UDPHeaderSize          = 28   // IP + UDP header bytes, used for rate accounting
	NetMinMessage          = 5    // smallest valid datagram
	SplitHeaderSize        = 12   // size of the split packet header
)

// Reliable stream fragmentation.
const (
	FragmentBits    = 8
	FragmentSize    = 1 << FragmentBits
	MaxFileSizeBits = 26
	MaxFileSize     = 1<<MaxFileSizeBits - 1
	MaxStreams      = 2 // 0: normal reliable stream, 1: file stream
	StreamNormal    = 0
	StreamFile      = 1
	MaxSubChannels  = 8
)

// Netchannel packet flags (first header byte after the sequence numbers).
const (
	PacketFlagReliable   = 1 << 0 // packet contains subchannel stream data
	PacketFlagCompressed = 1 << 1 // unused by protocol 24
	PacketFlagEncrypted  = 1 << 2 // unused by protocol 24
	PacketFlagSplit      = 1 << 3 // unused by protocol 24
	PacketFlagChoked     = 1 << 4 // number of choked packets follows
	PacketFlagChallenge  = 1 << 5 // challenge number follows
)

// EncodePadBits stores the number of trailing pad bits in flag bits 5-7.
func EncodePadBits(n int) uint8 { return uint8(n<<5) & 0xff }

// DecodePadBits extracts the number of trailing pad bits from the flags.
func DecodePadBits(flags uint8) int { return int(flags>>5) & 0xff }

// Message framing.
const (
	NetMsgTypeBits   = 6  // bits used for every message type
	NetMsgLengthBits = 11 // length field of user/entity/game event messages
)

// Signon states, sent with net_SignonState.
const (
	SignonNone        = 0 // not connected
	SignonChallenge   = 1 // challenging the server with out-of-band packets
	SignonConnected   = 2 // netchannel established
	SignonNew         = 3 // received server info and string tables
	SignonPrespawn    = 4 // received signon buffers
	SignonSpawn       = 5 // ready to receive entity packets
	SignonFull        = 6 // first full entity update received
	SignonChangeLevel = 7 // server is changing level
)

// SignonStateName returns a readable name for a signon state.
func SignonStateName(s int) string {
	switch s {
	case SignonNone:
		return "none"
	case SignonChallenge:
		return "challenge"
	case SignonConnected:
		return "connected"
	case SignonNew:
		return "new"
	case SignonPrespawn:
		return "prespawn"
	case SignonSpawn:
		return "spawn"
	case SignonFull:
		return "full"
	case SignonChangeLevel:
		return "changelevel"
	}
	return "unknown"
}

// Messages understood by both directions.
const (
	NetNOP         = 0 // padding
	NetDisconnect  = 1 // disconnect, last message in a connection
	NetFile        = 2 // file transmission request/deny
	NetTick        = 3 // last world tick
	NetStringCmd   = 4 // console command
	NetSetConVar   = 5 // one or more convar values
	NetSignonState = 6 // signon state change
)

// Server to client messages.
const (
	SvcPrint             = 7
	SvcServerInfo        = 8
	SvcSendTable         = 9
	SvcClassInfo         = 10
	SvcSetPause          = 11
	SvcCreateStringTable = 12
	SvcUpdateStringTable = 13
	SvcVoiceInit         = 14
	SvcVoiceData         = 15
	SvcSounds            = 17
	SvcSetView           = 18
	SvcFixAngle          = 19
	SvcCrosshairAngle    = 20
	SvcBSPDecal          = 21
	SvcUserMessage       = 23
	SvcEntityMessage     = 24
	SvcGameEvent         = 25
	SvcPacketEntities    = 26
	SvcTempEntities      = 27
	SvcPrefetch          = 28
	SvcMenu              = 29
	SvcGameEventList     = 30
	SvcGetCvarValue      = 31
	SvcCmdKeyValues      = 32
	SvcSetPauseTimed     = 33
)

// Client to server messages.
const (
	ClcClientInfo       = 8
	ClcMove             = 9
	ClcVoiceData        = 10
	ClcBaselineAck      = 11
	ClcListenEvents     = 12
	ClcRespondCvarValue = 13
	ClcFileCRCCheck     = 14
	ClcSaveReplay       = 15
	ClcCmdKeyValues     = 16
	ClcFileMD5Check     = 17
)

// Engine limits used by message encodings.
const (
	MaxEdictBits                        = 11
	MaxEdicts                           = 1 << MaxEdictBits
	MaxServerClassBits                  = 9
	MaxServerClasses                    = 1 << MaxServerClassBits
	NumNetworkedEHandleSerialNumberBits = 10
	MaxDecalIndexBits                   = 9
	SPModelIndexBits                    = 13
	MaxSoundIndexBits                   = 14
	MaxSoundIndexBitsV22                = 13
	EventIndexBits                      = 8 // temp entity count bits
	MaxEventBits                        = 9 // game event id bits
	MaxEventNumber                      = 1 << MaxEventBits
	MaxTables                           = 32 // string tables
	MaxTableBits                        = 5
	MaxCustomFiles                      = 4
	DeltaSizeBits                       = 20 // packet entities payload length bits
	NumNewCommandBits                   = 4
	MaxNewCommands                      = 1<<NumNewCommandBits - 1
	NumBackupCommandBits                = 3
	MaxBackupCommands                   = 1<<NumBackupCommandBits - 1
	MaxPlayerNameLength                 = 32
	MaxOSPath                           = 260
	MaxUserMessageData                  = 255
	SignedGUIDLen                       = 32
	SteamKeySize                        = 2048
	MaxDatatableProps                   = 4096
	EntitySentinel                      = 9999
)

// Authentication protocols announced in the challenge and connect packets.
const (
	AuthCertificate = 1 // WON certificate, obsolete
	AuthHashedCDKey = 2 // MD5 of a CD key, used for LAN/insecure servers
	AuthSteam       = 3 // Steam session ticket
)

// Out-of-band message identifiers (proto_oob.h).
const (
	A2APing                        = 'i'
	A2AAck                         = 'j'
	C2SConnect                     = 'k'
	A2APrint                       = 'l'
	S2AInfoDetailed                = 'm' // GoldSrc style info response
	A2SGetChallenge                = 'q'
	A2SRcon                        = 'r'
	S2CChallenge                   = 'A'
	S2CConnection                  = 'B'
	S2AInfoSrc                     = 'I'
	S2CRedirect                    = 'L'
	S2ALogString                   = 'R'
	S2ALogString2                  = 'S'
	A2SInfo                        = 'T'
	A2SPlayer                      = 'U'
	A2SRules                       = 'V'
	A2SServerQueryChallenge        = 'W'
	C2CMod                         = 'X'
	A2SPing2                       = 'Y'
	S2APing2Reply                  = 'Z'
	S2CConnReject                  = '9'
	S2APlayer                      = 'D'
	S2ARules                       = 'E'
	S2CMagicVersion         uint32 = 0x5a4f4933
	A2SKeyString                   = "Source Engine Query"
)

// Well known string table names.
const (
	TableInstanceBaseline = "instancebaseline"
	TableLightStyles      = "lightstyles"
	TableUserInfo         = "userinfo"
	TableServerQueryInfo  = "server_query_info"
	TableDownloadables    = "downloadables"
	TableModelPrecache    = "modelprecache"
	TableGenericPrecache  = "genericprecache"
	TableSoundPrecache    = "soundprecache"
	TableDecalPrecache    = "decalprecache"
)

// Default ports.
const (
	PortServer = 27015
	PortClient = 27005
	PortHLTV   = 27020
)
