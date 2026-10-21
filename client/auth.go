package client

import (
	"encoding/binary"
	"errors"
	"net"

	"github.com/galaco/sourcenet/connectionless"
	"github.com/galaco/sourcenet/protocol"
)

// AuthRequest describes what the server asked for in its challenge.
type AuthRequest struct {
	// Protocol is the auth protocol the server requested (protocol.Auth*).
	Protocol int32
	// ServerSteamID and Secure come from the challenge when Protocol is
	// protocol.AuthSteam.
	ServerSteamID uint64
	Secure        bool
	// ServerAddr is the server address the ticket is bound to.
	ServerAddr *net.UDPAddr
}

// Credentials are appended to the connect packet.
type Credentials struct {
	// Protocol is the auth protocol actually used (normally the requested one).
	Protocol int32
	// SteamCookie is the client SteamID (uint64 little-endian) followed by a
	// Steam auth session ticket. Used with protocol.AuthSteam.
	SteamCookie []byte
	// CDKeyHash is used with every other protocol.
	CDKeyHash string
}

// Authenticator produces connect credentials. Implementations backed by the
// Steamworks API return ISteamUser::GetAuthSessionTicket output prefixed with
// the user's SteamID, exactly as the engine does.
type Authenticator interface {
	Credentials(req AuthRequest) (Credentials, error)
	// Cancel is called on disconnect so a session ticket can be revoked
	// (ISteamUser::CancelAuthTicket). It may be a no-op.
	Cancel()
}

// ErrSteamRequired is returned by OfflineAuth when a server requires Steam
// authentication and no SteamID was configured.
var ErrSteamRequired = errors.New("client: server requires Steam authentication")

// OfflineAuth authenticates without Steam. It answers hashed CD key
// challenges, and for Steam challenges sends a cookie containing SteamID and
// an empty ticket. Real servers reject that cookie unless they are LAN-only
// (sv_lan 1), where the engine ignores Steam validation failures.
type OfflineAuth struct {
	// SteamID is the 64-bit SteamID claimed in the cookie. Zero refuses
	// Steam challenges with ErrSteamRequired.
	SteamID uint64
	// CDKey is hashed for CD key challenges; empty uses the engine default.
	CDKey string
}

// Credentials implements Authenticator.
func (a OfflineAuth) Credentials(req AuthRequest) (Credentials, error) {
	if req.Protocol != protocol.AuthSteam {
		return Credentials{Protocol: req.Protocol, CDKeyHash: connectionless.CDKeyHash(a.CDKey)}, nil
	}
	if a.SteamID == 0 {
		return Credentials{}, ErrSteamRequired
	}
	// The server only checks 0 < len < STEAM_KEYSIZE before handing the
	// ticket to Steam; pad so the ticket region is non-empty.
	cookie := make([]byte, 8, 8+4)
	binary.LittleEndian.PutUint64(cookie, a.SteamID)
	cookie = append(cookie, 0, 0, 0, 0)
	return Credentials{Protocol: protocol.AuthSteam, SteamCookie: cookie}, nil
}

// Cancel implements Authenticator.
func (OfflineAuth) Cancel() {}

// TicketFunc adapts a function returning a raw Steam session ticket (without
// the SteamID prefix) into an Authenticator, for integration with a
// Steamworks binding.
type TicketFunc struct {
	SteamID  uint64
	Ticket   func(req AuthRequest) ([]byte, error)
	OnCancel func()
}

// Credentials implements Authenticator.
func (t TicketFunc) Credentials(req AuthRequest) (Credentials, error) {
	if req.Protocol != protocol.AuthSteam {
		return OfflineAuth{}.Credentials(req)
	}
	ticket, err := t.Ticket(req)
	if err != nil {
		return Credentials{}, err
	}
	cookie := make([]byte, 8, 8+len(ticket))
	binary.LittleEndian.PutUint64(cookie, t.SteamID)
	return Credentials{Protocol: protocol.AuthSteam, SteamCookie: append(cookie, ticket...)}, nil
}

// Cancel implements Authenticator.
func (t TicketFunc) Cancel() {
	if t.OnCancel != nil {
		t.OnCancel()
	}
}
