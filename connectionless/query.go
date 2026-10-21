package connectionless

import (
	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/protocol"
)

// NoChallenge is sent in A2S_PLAYER and A2S_RULES requests to ask the server
// for a challenge number.
const NoChallenge uint32 = 0xffffffff

// AppIDTheShip identifies The Ship, whose A2S responses carry extra fields.
const AppIDTheShip = 2400

// Extra data flags of the A2S_INFO response.
const (
	EDFGameID   = 0x01
	EDFSteamID  = 0x10
	EDFKeywords = 0x20
	EDFSourceTV = 0x40
	EDFPort     = 0x80
)

func boolByte(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// QueryChallenge (S2C_CHALLENGE, 'A', short form) carries the challenge a
// client must echo in its next A2S request.
type QueryChallenge struct {
	Challenge uint32
}

// ID implements Packet.
func (p *QueryChallenge) ID() byte { return protocol.S2CChallenge }

// Marshal implements Packet.
func (p *QueryChallenge) Marshal() []byte {
	w := newPacket(p.ID())
	w.WriteUint32(p.Challenge)
	return w.Bytes()
}

func (p *QueryChallenge) decode(r *bitbuf.Reader) error {
	p.Challenge = r.ReadUint32()
	return checkRead(r)
}

// ServerQueryGetChallenge (A2S_SERVERQUERY_GETCHALLENGE, 'W') requests a
// query challenge. Deprecated by Valve in favour of sending NoChallenge.
type ServerQueryGetChallenge struct{}

// ID implements Packet.
func (p *ServerQueryGetChallenge) ID() byte { return protocol.A2SServerQueryChallenge }

// Marshal implements Packet.
func (p *ServerQueryGetChallenge) Marshal() []byte { return newPacket(p.ID()).Bytes() }

func (p *ServerQueryGetChallenge) decode(*bitbuf.Reader) error { return nil }

// InfoRequest (A2S_INFO, 'T') requests server information. Since December
// 2020 servers may answer with a QueryChallenge, after which the request is
// repeated with the challenge appended.
type InfoRequest struct {
	Challenge    uint32
	HasChallenge bool
}

// ID implements Packet.
func (p *InfoRequest) ID() byte { return protocol.A2SInfo }

// Marshal implements Packet.
func (p *InfoRequest) Marshal() []byte {
	w := newPacket(p.ID())
	w.WriteString(protocol.A2SKeyString)
	if p.HasChallenge {
		w.WriteUint32(p.Challenge)
	}
	return w.Bytes()
}

func (p *InfoRequest) decode(r *bitbuf.Reader) error {
	r.ReadString()
	if err := checkRead(r); err != nil {
		return err
	}
	if r.BytesLeft() >= 4 {
		p.HasChallenge = true
		p.Challenge = r.ReadUint32()
	}
	return nil
}

// Info (S2A_INFO_SRC, 'I') is the A2S_INFO response.
type Info struct {
	// Protocol is the query protocol version (17 for current servers).
	Protocol    uint8
	Name        string
	Map         string
	Folder      string
	Game        string
	AppID       uint16
	Players     uint8
	MaxPlayers  uint8
	Bots        uint8
	ServerType  byte // 'd' dedicated, 'l' listen, 'p' SourceTV relay
	Environment byte // 'l' Linux, 'w' Windows, 'm' or 'o' macOS
	Visibility  uint8
	VAC         uint8
	// The Ship (AppID 2400) only.
	ShipMode, ShipWitnesses, ShipDuration uint8
	Version                               string
	// EDF holds the EDF* flags describing which of the following fields
	// are present.
	EDF          byte
	Port         uint16
	SteamID      uint64
	SourceTVPort uint16
	SourceTVName string
	Keywords     string
	// GameID is the 64-bit game id; its low 24 bits are the full app id.
	GameID uint64
}

// ID implements Packet.
func (p *Info) ID() byte { return protocol.S2AInfoSrc }

// Marshal implements Packet. The extra data byte is only written when EDF is
// non-zero.
func (p *Info) Marshal() []byte {
	w := newPacket(p.ID())
	w.WriteUint8(p.Protocol)
	w.WriteString(p.Name)
	w.WriteString(p.Map)
	w.WriteString(p.Folder)
	w.WriteString(p.Game)
	w.WriteUint16(p.AppID)
	w.WriteUint8(p.Players)
	w.WriteUint8(p.MaxPlayers)
	w.WriteUint8(p.Bots)
	w.WriteUint8(p.ServerType)
	w.WriteUint8(p.Environment)
	w.WriteUint8(p.Visibility)
	w.WriteUint8(p.VAC)
	if p.AppID == AppIDTheShip {
		w.WriteUint8(p.ShipMode)
		w.WriteUint8(p.ShipWitnesses)
		w.WriteUint8(p.ShipDuration)
	}
	w.WriteString(p.Version)
	if p.EDF == 0 {
		return w.Bytes()
	}
	w.WriteUint8(p.EDF)
	if p.EDF&EDFPort != 0 {
		w.WriteUint16(p.Port)
	}
	if p.EDF&EDFSteamID != 0 {
		w.WriteUint64(p.SteamID)
	}
	if p.EDF&EDFSourceTV != 0 {
		w.WriteUint16(p.SourceTVPort)
		w.WriteString(p.SourceTVName)
	}
	if p.EDF&EDFKeywords != 0 {
		w.WriteString(p.Keywords)
	}
	if p.EDF&EDFGameID != 0 {
		w.WriteUint64(p.GameID)
	}
	return w.Bytes()
}

func (p *Info) decode(r *bitbuf.Reader) error {
	p.Protocol = r.ReadUint8()
	p.Name = r.ReadString()
	p.Map = r.ReadString()
	p.Folder = r.ReadString()
	p.Game = r.ReadString()
	p.AppID = r.ReadUint16()
	p.Players = r.ReadUint8()
	p.MaxPlayers = r.ReadUint8()
	p.Bots = r.ReadUint8()
	p.ServerType = r.ReadUint8()
	p.Environment = r.ReadUint8()
	p.Visibility = r.ReadUint8()
	p.VAC = r.ReadUint8()
	if p.AppID == AppIDTheShip {
		p.ShipMode = r.ReadUint8()
		p.ShipWitnesses = r.ReadUint8()
		p.ShipDuration = r.ReadUint8()
	}
	p.Version = r.ReadString()
	if err := checkRead(r); err != nil {
		return err
	}
	if r.BytesLeft() < 1 {
		return nil
	}
	p.EDF = r.ReadUint8()
	if p.EDF&EDFPort != 0 {
		p.Port = r.ReadUint16()
	}
	if p.EDF&EDFSteamID != 0 {
		p.SteamID = r.ReadUint64()
	}
	if p.EDF&EDFSourceTV != 0 {
		p.SourceTVPort = r.ReadUint16()
		p.SourceTVName = r.ReadString()
	}
	if p.EDF&EDFKeywords != 0 {
		p.Keywords = r.ReadString()
	}
	if p.EDF&EDFGameID != 0 {
		p.GameID = r.ReadUint64()
	}
	return checkRead(r)
}

// PlayerRequest (A2S_PLAYER, 'U') requests the player list. Send NoChallenge
// first; the server answers with a QueryChallenge to repeat the request with.
type PlayerRequest struct {
	Challenge uint32
}

// ID implements Packet.
func (p *PlayerRequest) ID() byte { return protocol.A2SPlayer }

// Marshal implements Packet.
func (p *PlayerRequest) Marshal() []byte {
	w := newPacket(p.ID())
	w.WriteUint32(p.Challenge)
	return w.Bytes()
}

func (p *PlayerRequest) decode(r *bitbuf.Reader) error {
	p.Challenge = r.ReadUint32()
	return checkRead(r)
}

// Player is one entry of an A2S_PLAYER response.
type Player struct {
	Index uint8
	Name  string
	Score int32
	// Duration is the connection time in seconds.
	Duration float32
}

// Players (S2A_PLAYER, 'D') is the A2S_PLAYER response.
type Players struct {
	Players []Player
}

// ID implements Packet.
func (p *Players) ID() byte { return protocol.S2APlayer }

// Marshal implements Packet.
func (p *Players) Marshal() []byte {
	w := newPacket(p.ID())
	w.WriteUint8(uint8(len(p.Players)))
	for _, pl := range p.Players {
		w.WriteUint8(pl.Index)
		w.WriteString(pl.Name)
		w.WriteInt32(pl.Score)
		w.WriteFloat32(pl.Duration)
	}
	return w.Bytes()
}

// decode reads up to the advertised number of players, stopping cleanly when
// the data runs out (the count byte is unreliable on servers with more than
// 255 entries).
func (p *Players) decode(r *bitbuf.Reader) error {
	count := int(r.ReadUint8())
	if err := checkRead(r); err != nil {
		return err
	}
	p.Players = make([]Player, 0, count)
	for i := 0; i < count && r.BytesLeft() > 0; i++ {
		var pl Player
		pl.Index = r.ReadUint8()
		pl.Name = r.ReadString()
		pl.Score = r.ReadInt32()
		pl.Duration = r.ReadFloat32()
		if err := checkRead(r); err != nil {
			return err
		}
		p.Players = append(p.Players, pl)
	}
	return nil
}

// RulesRequest (A2S_RULES, 'V') requests the server's public convars.
type RulesRequest struct {
	Challenge uint32
}

// ID implements Packet.
func (p *RulesRequest) ID() byte { return protocol.A2SRules }

// Marshal implements Packet.
func (p *RulesRequest) Marshal() []byte {
	w := newPacket(p.ID())
	w.WriteUint32(p.Challenge)
	return w.Bytes()
}

func (p *RulesRequest) decode(r *bitbuf.Reader) error {
	p.Challenge = r.ReadUint32()
	return checkRead(r)
}

// Rule is one name/value pair of an A2S_RULES response.
type Rule struct {
	Name  string
	Value string
}

// Rules (S2A_RULES, 'E') is the A2S_RULES response. Rules are kept in wire
// order, duplicates included.
type Rules struct {
	Rules []Rule
}

// ID implements Packet.
func (p *Rules) ID() byte { return protocol.S2ARules }

// Get returns the value of the first rule with the given name.
func (p *Rules) Get(name string) (string, bool) {
	for _, r := range p.Rules {
		if r.Name == name {
			return r.Value, true
		}
	}
	return "", false
}

// Map returns the rules as a map; later duplicates win.
func (p *Rules) Map() map[string]string {
	m := make(map[string]string, len(p.Rules))
	for _, r := range p.Rules {
		m[r.Name] = r.Value
	}
	return m
}

// Marshal implements Packet.
func (p *Rules) Marshal() []byte {
	w := newPacket(p.ID())
	w.WriteUint16(uint16(len(p.Rules)))
	for _, r := range p.Rules {
		w.WriteString(r.Name)
		w.WriteString(r.Value)
	}
	return w.Bytes()
}

// decode reads up to the advertised number of rules; servers with long rule
// lists are known to truncate the response, so running out of data between
// rules is not an error.
func (p *Rules) decode(r *bitbuf.Reader) error {
	count := int(r.ReadUint16())
	if err := checkRead(r); err != nil {
		return err
	}
	p.Rules = make([]Rule, 0, count)
	for i := 0; i < count && r.BytesLeft() > 0; i++ {
		var rule Rule
		rule.Name = r.ReadString()
		rule.Value = r.ReadString()
		if err := checkRead(r); err != nil {
			return err
		}
		p.Rules = append(p.Rules, rule)
	}
	return nil
}
