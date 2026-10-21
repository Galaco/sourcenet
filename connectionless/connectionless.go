// Package connectionless encodes and decodes the Source Engine's
// out-of-band (connectionless) packets: the connection handshake
// (challenge, connect, accept, reject) and the A2S server query protocol.
//
// Every connectionless packet starts with the 32-bit header 0xFFFFFFFF
// followed by a one byte packet identifier. All fields are byte aligned and
// little-endian; strings are NUL terminated.
package connectionless

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/protocol"
)

// ErrNotConnectionless is returned when a packet lacks the 0xFFFFFFFF header.
var ErrNotConnectionless = errors.New("connectionless: missing connectionless header")

// ErrTruncated is returned when a packet ends before all fields are read.
var ErrTruncated = errors.New("connectionless: truncated packet")

// Padding strings appended by the engine to some handshake packets.
const (
	challengePad = "0000000000"
	replyPad     = "000000"
)

// IsConnectionless reports whether b starts with the 0xFFFFFFFF header.
func IsConnectionless(b []byte) bool {
	return len(b) >= 4 && b[0] == 0xff && b[1] == 0xff && b[2] == 0xff && b[3] == 0xff
}

// Packet is an out-of-band packet.
type Packet interface {
	// ID returns the packet identifier byte.
	ID() byte
	// Marshal encodes the packet including the 0xFFFFFFFF header.
	Marshal() []byte
}

// Parse decodes an out-of-band packet. Unknown identifiers are returned as
// *Unknown. The identifier 'A' is shared by the connect handshake challenge
// (long form, starting with protocol.S2CMagicVersion) and the A2S query
// challenge (a lone int32); they are distinguished by length and magic.
func Parse(b []byte) (Packet, error) {
	if !IsConnectionless(b) {
		return nil, ErrNotConnectionless
	}
	if len(b) < 5 {
		return nil, ErrTruncated
	}
	id := b[4]
	payload := b[5:]
	var p interface {
		Packet
		decode(r *bitbuf.Reader) error
	}
	switch id {
	case protocol.A2SGetChallenge:
		p = &GetChallenge{}
	case protocol.S2CChallenge:
		if len(payload) >= 16 && le32(payload) == protocol.S2CMagicVersion {
			p = &Challenge{}
		} else {
			p = &QueryChallenge{}
		}
	case protocol.C2SConnect:
		p = &Connect{}
	case protocol.S2CConnection:
		p = &Accept{}
	case protocol.S2CConnReject:
		p = &Reject{}
	case protocol.A2SInfo:
		p = &InfoRequest{}
	case protocol.S2AInfoSrc:
		p = &Info{}
	case protocol.A2SPlayer:
		p = &PlayerRequest{}
	case protocol.S2APlayer:
		p = &Players{}
	case protocol.A2SRules:
		p = &RulesRequest{}
	case protocol.S2ARules:
		p = &Rules{}
	case protocol.A2SServerQueryChallenge:
		p = &ServerQueryGetChallenge{}
	case protocol.A2APing:
		p = &Ping{}
	case protocol.A2AAck:
		p = &Ack{}
	default:
		return &Unknown{IDByte: id, Payload: append([]byte(nil), payload...)}, nil
	}
	if err := p.decode(bitbuf.NewReader(payload)); err != nil {
		return nil, fmt.Errorf("connectionless: decoding %q: %w", id, err)
	}
	return p, nil
}

func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

// newPacket starts a packet with the connectionless header and identifier.
func newPacket(id byte) *bitbuf.Writer {
	w := bitbuf.NewWriter(64)
	w.WriteInt32(protocol.HeaderConnectionless)
	w.WriteUint8(id)
	return w
}

func checkRead(r *bitbuf.Reader) error {
	if r.Overflowed() {
		return ErrTruncated
	}
	return nil
}

// Unknown is a packet with an identifier this package does not decode.
type Unknown struct {
	IDByte  byte
	Payload []byte
}

// ID implements Packet.
func (p *Unknown) ID() byte { return p.IDByte }

// Marshal implements Packet.
func (p *Unknown) Marshal() []byte {
	w := newPacket(p.IDByte)
	w.WriteBytes(p.Payload)
	return w.Bytes()
}

// GetChallenge (A2S_GETCHALLENGE, 'q') starts a connection by asking the
// server for a challenge number.
type GetChallenge struct {
	// ClientChallenge is a random value echoed back by the server.
	ClientChallenge uint32
}

// ID implements Packet.
func (p *GetChallenge) ID() byte { return protocol.A2SGetChallenge }

// Marshal implements Packet.
func (p *GetChallenge) Marshal() []byte {
	w := newPacket(p.ID())
	w.WriteUint32(p.ClientChallenge)
	w.WriteString(challengePad)
	return w.Bytes()
}

func (p *GetChallenge) decode(r *bitbuf.Reader) error {
	p.ClientChallenge = r.ReadUint32()
	return checkRead(r)
}

// Challenge (S2C_CHALLENGE, 'A', long form) is the server's reply to
// GetChallenge.
type Challenge struct {
	ServerChallenge uint32
	ClientChallenge uint32
	// AuthProtocol is the authentication the server expects, one of the
	// protocol.Auth* constants.
	AuthProtocol int32
	// ServerSteamID and Secure are only present for protocol.AuthSteam.
	ServerSteamID uint64
	HasSteamID    bool
	Secure        bool
}

// ID implements Packet.
func (p *Challenge) ID() byte { return protocol.S2CChallenge }

// Marshal implements Packet.
func (p *Challenge) Marshal() []byte {
	w := newPacket(p.ID())
	w.WriteUint32(protocol.S2CMagicVersion)
	w.WriteUint32(p.ServerChallenge)
	w.WriteUint32(p.ClientChallenge)
	w.WriteInt32(p.AuthProtocol)
	if p.AuthProtocol == protocol.AuthSteam {
		w.WriteInt16(0) // legacy steam2 key length
		if p.HasSteamID {
			w.WriteUint64(p.ServerSteamID)
			w.WriteUint8(boolByte(p.Secure))
		}
	}
	w.WriteString(replyPad)
	return w.Bytes()
}

func (p *Challenge) decode(r *bitbuf.Reader) error {
	if magic := r.ReadUint32(); magic != protocol.S2CMagicVersion {
		return fmt.Errorf("unexpected magic %#x", magic)
	}
	p.ServerChallenge = r.ReadUint32()
	p.ClientChallenge = r.ReadUint32()
	p.AuthProtocol = r.ReadInt32()
	if p.AuthProtocol == protocol.AuthSteam {
		if keyLen := r.ReadInt16(); keyLen != 0 {
			return fmt.Errorf("unexpected steam key length %d", keyLen)
		}
		// The client only reads the steam id when more than 8 bytes remain.
		if r.BytesLeft() > 8 {
			p.HasSteamID = true
			p.ServerSteamID = r.ReadUint64()
			p.Secure = r.ReadUint8() == 1
		}
	}
	return checkRead(r)
}

// Connect (C2S_CONNECT, 'k') asks the server to accept a connection.
type Connect struct {
	// Protocol is the network protocol version, normally protocol.Version.
	Protocol int32
	// AuthProtocol is one of the protocol.Auth* constants.
	AuthProtocol    int32
	ServerChallenge uint32
	ClientChallenge uint32
	Name            string
	Password        string
	// ProductVersion must match the server's steam.inf version string.
	ProductVersion string
	// CDKeyHash is sent for every auth protocol other than protocol.AuthSteam.
	CDKeyHash string
	// SteamCookie is the Steam auth ticket prefixed with the client SteamID,
	// sent for protocol.AuthSteam.
	SteamCookie []byte
}

// ID implements Packet.
func (p *Connect) ID() byte { return protocol.C2SConnect }

// Marshal implements Packet.
func (p *Connect) Marshal() []byte {
	w := newPacket(p.ID())
	w.WriteInt32(p.Protocol)
	w.WriteInt32(p.AuthProtocol)
	w.WriteUint32(p.ServerChallenge)
	w.WriteUint32(p.ClientChallenge)
	w.WriteString(p.Name)
	w.WriteString(p.Password)
	w.WriteString(p.ProductVersion)
	if p.AuthProtocol == protocol.AuthSteam {
		w.WriteInt16(int16(len(p.SteamCookie)))
		w.WriteBytes(p.SteamCookie)
	} else {
		w.WriteString(p.CDKeyHash)
	}
	return w.Bytes()
}

func (p *Connect) decode(r *bitbuf.Reader) error {
	p.Protocol = r.ReadInt32()
	p.AuthProtocol = r.ReadInt32()
	p.ServerChallenge = r.ReadUint32()
	p.ClientChallenge = r.ReadUint32()
	p.Name = r.ReadString()
	p.Password = r.ReadString()
	p.ProductVersion = r.ReadString()
	if p.AuthProtocol == protocol.AuthSteam {
		n := int(r.ReadInt16())
		if n < 0 || n > protocol.SteamKeySize {
			return fmt.Errorf("invalid steam cookie length %d", n)
		}
		p.SteamCookie = r.ReadBytes(n)
	} else {
		p.CDKeyHash = r.ReadString()
	}
	return checkRead(r)
}

// Accept (S2C_CONNECTION, 'B') tells the client the connection was accepted
// and the netchannel may start.
type Accept struct {
	ClientChallenge uint32
	// Legacy is set when the packet is the challenge-less form the server
	// periodically resends ("B00000000000000") to clients that have not yet
	// sent a netchannel packet. The engine's client ignores it because its
	// challenge never matches.
	Legacy bool
}

// ID implements Packet.
func (p *Accept) ID() byte { return protocol.S2CConnection }

// Marshal implements Packet.
func (p *Accept) Marshal() []byte {
	w := newPacket(p.ID())
	if p.Legacy {
		w.WriteString("00000000000000")
		return w.Bytes()
	}
	w.WriteUint32(p.ClientChallenge)
	w.WriteString(challengePad)
	return w.Bytes()
}

func (p *Accept) decode(r *bitbuf.Reader) error {
	if r.BytesLeft() >= 4 && r.PeekBits(32) == 0x30303030 {
		p.Legacy = true
		return nil
	}
	p.ClientChallenge = r.ReadUint32()
	return checkRead(r)
}

// Reject (S2C_CONNREJECT, '9') refuses a connection attempt.
type Reject struct {
	ClientChallenge uint32
	// Reason is often a localisation token such as "#GameUI_ServerRejectBadChallenge".
	Reason string
}

// ID implements Packet.
func (p *Reject) ID() byte { return protocol.S2CConnReject }

// Marshal implements Packet.
func (p *Reject) Marshal() []byte {
	w := newPacket(p.ID())
	w.WriteUint32(p.ClientChallenge)
	w.WriteString(p.Reason)
	return w.Bytes()
}

func (p *Reject) decode(r *bitbuf.Reader) error {
	p.ClientChallenge = r.ReadUint32()
	p.Reason = r.ReadString()
	return checkRead(r)
}

// DefaultCDKey is the key the engine substitutes when none is configured.
const DefaultCDKey = "1234567890123"

// CDKeyHash returns the hashed CD key sent with protocol.AuthHashedCDKey:
// the lower-case hex MD5 of the key. An empty key is replaced by DefaultCDKey.
func CDKeyHash(key string) string {
	if key == "" {
		key = DefaultCDKey
	}
	sum := md5.Sum([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Ping (A2A_PING, 'i') is a generic ping request.
type Ping struct{}

// ID implements Packet.
func (p *Ping) ID() byte { return protocol.A2APing }

// Marshal implements Packet.
func (p *Ping) Marshal() []byte { return newPacket(p.ID()).Bytes() }

func (p *Ping) decode(*bitbuf.Reader) error { return nil }

// Ack (A2A_ACK, 'j') is the reply to Ping. Servers usually append a padding
// string, kept in Payload.
type Ack struct {
	Payload string
}

// ID implements Packet.
func (p *Ack) ID() byte { return protocol.A2AAck }

// Marshal implements Packet.
func (p *Ack) Marshal() []byte {
	w := newPacket(p.ID())
	if p.Payload != "" {
		w.WriteString(p.Payload)
	}
	return w.Bytes()
}

func (p *Ack) decode(r *bitbuf.Reader) error {
	if r.BitsLeft() >= 8 {
		p.Payload = r.ReadString()
	}
	return nil
}
