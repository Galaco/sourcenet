package client

import (
	"github.com/galaco/sourcenet/entity"
	"github.com/galaco/sourcenet/gameevent"
	"github.com/galaco/sourcenet/message"
	"github.com/galaco/sourcenet/netchan"
	"github.com/galaco/sourcenet/stringtable"
)

// Event is something that happened during Update. Events are delivered in
// order on the goroutine calling Update, through Config.OnEvent and
// PollEvents.
type Event interface{ isEvent() }

// EventConnecting is emitted for each connection attempt (challenge request).
type EventConnecting struct{ Attempt int }

// EventConnected is emitted when the server accepted the connection and the
// netchannel is established (signon state connected).
type EventConnected struct{}

// EventSignonState is emitted on every signon state change.
type EventSignonState struct{ State int }

// EventServerInfo carries svc_ServerInfo, the first message of a new map.
type EventServerInfo struct{ Info *message.SvcServerInfo }

// EventDisconnected is emitted once when the connection ends.
type EventDisconnected struct {
	Reason string
	// Rejected is set when the server refused the connection attempt.
	Rejected bool
}

// EventPrint carries console text from svc_Print.
type EventPrint struct{ Text string }

// EventStringCmd carries a console command the server asked the client to
// execute (net_StringCmd).
type EventStringCmd struct{ Command string }

// EventConVars carries replicated convars set by the server.
type EventConVars struct{ ConVars []message.ConVar }

// EventStringTable is emitted when a string table is created or updated.
type EventStringTable struct {
	Table   *stringtable.Table
	Created bool
	Changed []int
}

// EventGameEvent carries a decoded game event.
type EventGameEvent struct{ Event *gameevent.Event }

// EventUserMessage carries a user message. Message is the decoded payload
// when the game profile has a decoder for it, nil otherwise.
type EventUserMessage struct {
	ID      int
	Name    string
	Message any
	Data    message.Bits
}

// EventEntities is emitted after each processed svc_PacketEntities.
type EventEntities struct {
	ServerTick int32
	Full       bool
	Changes    []entity.Change
}

// EventTempEntities carries decoded temporary entities.
type EventTempEntities struct{ Entities []entity.TempEntity }

// EventFileReceived carries a file transferred by the server. File.Name is
// chosen by the server: sanitise it (no absolute paths, no "..") before
// using it as a filesystem path.
type EventFileReceived struct{ File netchan.File }

// EventFileDenied reports that the server refused a RequestFile.
type EventFileDenied struct {
	Name       string
	TransferID uint32
}

// EventError reports a non-fatal problem, such as a message that could not
// be decoded. The engine skips the rest of the packet in these cases.
type EventError struct{ Err error }

func (EventConnecting) isEvent()   {}
func (EventConnected) isEvent()    {}
func (EventSignonState) isEvent()  {}
func (EventServerInfo) isEvent()   {}
func (EventDisconnected) isEvent() {}
func (EventPrint) isEvent()        {}
func (EventStringCmd) isEvent()    {}
func (EventConVars) isEvent()      {}
func (EventStringTable) isEvent()  {}
func (EventGameEvent) isEvent()    {}
func (EventUserMessage) isEvent()  {}
func (EventEntities) isEvent()     {}
func (EventTempEntities) isEvent() {}
func (EventFileReceived) isEvent() {}
func (EventFileDenied) isEvent()   {}
func (EventError) isEvent()        {}
