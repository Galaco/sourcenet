package listener

import (
	"github.com/BenLubar/steamworks"
	"github.com/BenLubar/steamworks/steamauth"
	"github.com/galaco/bitbuf"
	"github.com/galaco/network"
	"github.com/galaco/sourcenet"
	"github.com/galaco/sourcenet/message"
	"log"
)

// Connector is a standard mechanism for connecting to source engine servers
// Many games implement the same communication, particularly early games. If
type Connector struct {
	playerName  string
	password    string
	gameVersion string

	clientChallenge int32
	serverChallenge int32

	activeClient   *sourcenet.Client
	connectionStep int32
}

func (listener *Connector) Register(client *sourcenet.Client) {
	listener.activeClient = client
}

func (listener *Connector) Receive(msg sourcenet.IMessage, msgType int) {
	if msg.Connectionless() == false {
		return
	}
	packet := bitbuf.NewReader(msg.Data())

	packet.ReadInt32() // connectionless header

	packetType, _ := packet.ReadUint8()

	switch packetType {
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

		// CREATE NEW PACKET
		msg := message.ConnectionlessK(
			listener.clientChallenge,
			listener.serverChallenge,
			listener.playerName,
			listener.password,
			listener.gameVersion,
			steamid64,
			steamKey)

		listener.activeClient.SendMessage(msg, false)
	case 'B':
		if listener.connectionStep < 3 {
			log.Println("Connected successfully")
			listener.connectionStep = 3

			//client.channel.PrepareStreams()

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

			// @TODO Implement non-connectionless packet headers
			// Until this is done, the server wont start sending us stuff
			listener.activeClient.SendMessage(message.NewMessage(senddata.Data()))
		}
	}
}

func (listener *Connector) InitialMessage() network.IMessage {
	return message.ConnectionlessQ(listener.clientChallenge)
}

func NewConnector(playerName string, password string, gameVersion string, clientChallenge int32) *Connector {
	return &Connector{
		playerName:      playerName,
		password:        password,
		gameVersion:     gameVersion,
		clientChallenge: clientChallenge,
		connectionStep:  1,
	}
}
