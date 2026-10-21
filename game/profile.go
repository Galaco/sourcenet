// Package game describes the per-game parts of the Source Engine network
// protocol. The engine layer (netchannel, netmessages, string tables, data
// tables, entities, game events) is shared by every game of the protocol 24
// generation; a Profile supplies what differs between games:
//
//   - the user message names in the game's registration order (the wire only
//     carries indices) and optional decoders for their payloads,
//   - the user command delta codec,
//   - the send tables used to decode entities. Servers do not send these by
//     default (sv_sendtables 0): the client must already know them and must
//     announce their CRC, or the server rejects it with "Server uses
//     different class tables". The tables of a game version can be taken
//     from any demo recorded on it, see datatable.ReadDataTables,
//   - the product version string the server compares with its steam.inf,
//   - message registry customisation for mods with extra netmessages.
//
// Profiles for Team Fortress 2, Counter-Strike: Source, Day of Defeat: Source
// and Half-Life 2: Deathmatch are provided. Their data tables are not bundled
// because they change with game updates; set Profile.DataTables.
package game

import (
	"fmt"
	"sync"

	"github.com/galaco/sourcenet/datatable"
	"github.com/galaco/sourcenet/message"
	"github.com/galaco/sourcenet/protocol"
	"github.com/galaco/sourcenet/usercmd"
	"github.com/galaco/sourcenet/usermessage"
)

// Profile holds everything game-specific the client needs.
type Profile struct {
	// Name is the game directory, e.g. "tf" or "cstrike".
	Name string
	// AppID is the Steam application id.
	AppID uint32
	// ProtocolVersion is the network protocol version (protocol.Version).
	ProtocolVersion int
	// ProductVersion is the steam.inf PatchVersion the server requires
	// clients to match. Leave empty to let the client fetch it from the
	// server with an A2S_INFO query before connecting.
	ProductVersion string
	// ReplayEnabled reports whether the game's engine was built with replay
	// support, which adds a flag to svc_ServerInfo and clc_ClientInfo. True
	// for every Orange Box engine game since the 2010 update.
	ReplayEnabled bool
	// UserMessages lists user message names in registration order.
	UserMessages []string
	// RegisterUserMessageDecoders installs payload decoders on a registry.
	// Nil installs usermessage.DefaultDecoders.
	RegisterUserMessageDecoders func(r *usermessage.Registry)
	// UserCmd encodes movement commands. Nil means usercmd.Standard.
	UserCmd usercmd.Codec
	// DataTables loads the game's send tables and server classes. It is
	// called at most once per Profile; nil disables entity decoding unless
	// the server sends its tables (sv_sendtables 1).
	DataTables func() (*datatable.Tables, error)
	// SendTableCRC overrides the CRC announced in clc_ClientInfo. Zero means
	// compute it from DataTables.
	SendTableCRC uint32
	// CustomizeMessages may register additional or replacement server to
	// client netmessages for mods.
	CustomizeMessages func(r *message.Registry)

	once      sync.Once
	tables    *datatable.Tables
	tablesErr error
}

// MessageContext returns the netmessage encoding context for the game.
func (p *Profile) MessageContext() *message.Context {
	v := p.ProtocolVersion
	if v == 0 {
		v = protocol.Version
	}
	return &message.Context{ProtocolVersion: v, ReplayEnabled: p.ReplayEnabled}
}

// ServerMessages returns the registry used to decode server messages.
func (p *Profile) ServerMessages() *message.Registry {
	r := message.ServerToClient()
	if p.CustomizeMessages != nil {
		p.CustomizeMessages(r)
	}
	return r
}

// UserMessageRegistry returns a user message registry with decoders.
func (p *Profile) UserMessageRegistry() *usermessage.Registry {
	r := usermessage.NewRegistry(p.UserMessages)
	if p.RegisterUserMessageDecoders != nil {
		p.RegisterUserMessageDecoders(r)
	} else {
		usermessage.DefaultDecoders(r)
	}
	return r
}

// UserCmdCodec returns the user command codec.
func (p *Profile) UserCmdCodec() usercmd.Codec {
	if p.UserCmd != nil {
		return p.UserCmd
	}
	return usercmd.Standard
}

// Tables returns the game's linked data tables, loading them on first use.
// It returns (nil, nil) when the profile has no table source.
func (p *Profile) Tables() (*datatable.Tables, error) {
	if p.DataTables == nil {
		return nil, nil
	}
	p.once.Do(func() {
		t, err := p.DataTables()
		if err == nil && t != nil {
			err = t.Link()
		}
		if err != nil {
			p.tablesErr = fmt.Errorf("game %s: load data tables: %w", p.Name, err)
			return
		}
		p.tables = t
	})
	return p.tables, p.tablesErr
}

// TableCRC returns the send table CRC to announce: SendTableCRC when set,
// otherwise the CRC of the loaded tables, otherwise zero.
func (p *Profile) TableCRC() (uint32, error) {
	if p.SendTableCRC != 0 {
		return p.SendTableCRC, nil
	}
	t, err := p.Tables()
	if err != nil || t == nil {
		return 0, err
	}
	return t.CRC(), nil
}

func newProfile(name string, appID uint32, userMessages []string) *Profile {
	return &Profile{
		Name:            name,
		AppID:           appID,
		ProtocolVersion: protocol.Version,
		ReplayEnabled:   true,
		UserMessages:    userMessages,
	}
}

// TF2 returns a profile for Team Fortress 2.
func TF2() *Profile { return newProfile("tf", 440, usermessage.TF2) }

// CStrike returns a profile for Counter-Strike: Source.
func CStrike() *Profile { return newProfile("cstrike", 240, usermessage.CStrike) }

// DODS returns a profile for Day of Defeat: Source.
func DODS() *Profile { return newProfile("dod", 300, usermessage.DODS) }

// HL2MP returns a profile for Half-Life 2: Deathmatch, whose user commands
// carry ground contact entities.
func HL2MP() *Profile {
	p := newProfile("hl2mp", 320, usermessage.HL2MP)
	p.UserCmd = usercmd.HL2
	return p
}

// Generic returns a profile with no game-specific knowledge. User messages
// are surfaced by index only and entities are not decoded unless the server
// sends its tables.
func Generic() *Profile { return newProfile("", 0, nil) }
