package listener

import (
	"github.com/BenLubar/steamworks"
	"github.com/BenLubar/steamworks/steamauth"
	"github.com/galaco/bitbuf"
	"github.com/galaco/sourcenet"
	"github.com/galaco/sourcenet/message"
	"log"
)

const netSignOnState = 6

// Connector is a standard mechanism for connecting to source engine servers
// Many games implement the same communication, particularly early games. It
// will handle connectionless back-and-forth with a server, until we get
// a successfully connected message back from the server.
type Connector struct {
	playerName  string
	password    string
	gameVersion string

	clientChallenge int32
	serverChallenge int32

	activeClient   *sourcenet.Client
	connectionStep int32
}

// Register provides a mechanism for a listener to respond
// back to the client. This allows for encapsulation of certain
// back-and-forth logic for authentication.
func (listener *Connector) Register(client *sourcenet.Client) {
	listener.activeClient = client
}

// Receive is a callback that receives a message that the client
// received from the connected server.
func (listener *Connector) Receive(msg sourcenet.IMessage, msgType int) {
	if msg.Connectionless() == false {
		listener.handleConnected(msg, msgType)
	}

	listener.handleConnectionless(msg)
}

// InitialMessage Get the first message to initialize
// server authentication before connection.
func (listener *Connector) InitialMessage() sourcenet.IMessage {
	return message.ConnectionlessQ(listener.clientChallenge)
}

// handleConnectionless: Connectionless messages handler
func (listener *Connector) handleConnectionless(msg sourcenet.IMessage) {
	packet := bitbuf.NewReader(msg.Data())

	packet.ReadInt32() // connectionless header

	packetType, _ := packet.ReadUint8()

	log.Println(packetType)
	switch packetType {
	// 'A' is connection request acknowledgement.
	// We are required to authenicate game ownership now.
	case 'A':
		listener.connectionStep = 2
		packet.ReadInt32()
		serverChallenge, _ := packet.ReadInt32()
		clientChallenge, _ := packet.ReadInt32()

		listener.serverChallenge = serverChallenge
		listener.clientChallenge = clientChallenge

		localsid := steamworks.GetSteamID()
		steamid64 := uint64(localsid)
		steamKey := make([]byte, 2048)
		steamKey, _ = steamauth.CreateTicket()

		msg := message.ConnectionlessK(
			listener.clientChallenge,
			listener.serverChallenge,
			listener.playerName,
			listener.password,
			listener.gameVersion,
			steamid64,
			steamKey)

		listener.activeClient.SendMessage(msg, false)
	// 'B' is successful authentication.
	// Now send some user info bits.
	case 'B':
		if listener.connectionStep == 2 {
			log.Println("Connected successfully")
			listener.connectionStep = 3

			senddata := bitbuf.NewWriter(2048)

			senddata.WriteUnsignedBitInt32(6, 6)
			senddata.WriteByte(2)
			senddata.WriteInt32(-1)

			senddata.WriteUnsignedBitInt32(4, 8)
			senddata.WriteBytes([]byte("VModEnable 1"))
			senddata.WriteByte(0)
			senddata.WriteUnsignedBitInt32(4, 6)
			senddata.WriteString("vban 0 0 0 0")
			senddata.WriteByte(0)

			listener.activeClient.SendMessage(message.NewGeneric(senddata.Data()), false)
		}
	// '9' Connection was refused. A reason
	// is usually provided.
	case '9':
		packet.ReadInt32() // Not needed
		reason, _ := packet.ReadString(1024)
		log.Printf("Connection refused. Reason: %s\n", reason)
	default:
		return
	}
}

// handleConnected Connected message handler
func (listener *Connector) handleConnected(msg sourcenet.IMessage, msgType int) {
	if msgType != netSignOnState {
		return
	}
}

// NewConnector returns a new connector object.
func NewConnector(playerName string, password string, gameVersion string, clientChallenge int32) *Connector {
	return &Connector{
		playerName:      playerName,
		password:        password,
		gameVersion:     gameVersion,
		clientChallenge: clientChallenge,
		connectionStep:  1,
	}
}
