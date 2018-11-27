package listener

import (
	"github.com/BenLubar/steamworks"
	"github.com/BenLubar/steamworks/steamauth"
	"github.com/galaco/bitbuf"
	"github.com/galaco/sourcenet"
	"github.com/galaco/sourcenet/message"
	"log"
)

// M = master, S = server, C = client, A = any
const c2sConnect = 'k'
const a2sGetChallenge = 'q'

const s2cChallenge = 'A'
const s2cConnection = 'B'
const s2cConnectRejection = '9'

const netTick = 3
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

	keepAliveSkips int32
	tick int32
	hostFrametime uint32
	hostFrametimeDeviation uint32

	shouldDisconnect bool
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
	} else {
		listener.handleConnectionless(msg)
	}

	if listener.keepAliveSkips > 7 {
		//message.KeepAlive(listener.tick, listener.hostFrametime, listener.hostFrametimeDeviation)
		listener.activeClient.SendMessage(message.NewGenericDatagram(make([]byte, 0)), false)

		listener.keepAliveSkips = 0
	}

	listener.keepAliveSkips++
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

	switch packetType {
	// 'A' is connection request acknowledgement.
	// We are required to authenticate game ownership now.
	case s2cChallenge:
		listener.connectionStep = 2
		packet.ReadInt32()
		serverChallenge, _ := packet.ReadInt32()
		clientChallenge, _ := packet.ReadInt32()

		authprotocol,_ := packet.ReadInt32()

		steamkey_encryptionsize,_ := packet.ReadInt16() // gotta be 0

		//steamkey_encryptionkey,_ := packet.ReadBytes(uint(steamkey_encryptionsize))
		packet.ReadBytes(uint(steamkey_encryptionsize))

		//serversteamid,_ := packet.ReadBits(2048)
		packet.ReadBits(2048)
		vacsecured,_ := packet.ReadByte()

		log.Printf("Challenge: %d, Auth: %d, SKey: %d, VAC: %d\n", uint32(serverChallenge), uint32(clientChallenge), authprotocol, vacsecured)

		listener.activeClient.Channel().ServerChallenge = serverChallenge
		listener.activeClient.Channel().ClientChallenge = clientChallenge

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
	case s2cConnection:
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

			listener.activeClient.SendMessage(message.NewGenericDatagram(senddata.Data()), false)

			go func() {
				if listener.shouldDisconnect == true {
					return
				}
				// Some sort of keep alive
			}()
		} else {
			listener.activeClient.SendMessage(message.NewGeneric(make([]byte, 0)), false)
		}
	// '9' Connection was refused. A reason
	// is usually provided.
	case s2cConnectRejection:
		packet.ReadInt32() // contents not needed
		reason, _ := packet.ReadString(1024)
		log.Printf("Connection refused. Reason: %s\n", reason)
	default:
		return
	}
}

// handleConnected Connected message handler
func (listener *Connector) handleConnected(msg sourcenet.IMessage, msgType int) {
	switch msgType {
	case netSignOnState:
		log.Println("SignOnState")
		return
	case netTick:
		log.Println("Tick")
		buf := bitbuf.NewReader(msg.Data())
		listener.tick,_ = buf.ReadInt32()
		listener.hostFrametime,_ = buf.ReadUint32Bits(16)
		listener.hostFrametimeDeviation,_ = buf.ReadUint32Bits(16)
	}


/*
	SendClientInfo()
	// tell server that we entered now that state
	m_NetChannel->SendNetMsg( NET_SignonState( m_nSignonState, m_nServerCount ) );


void CClientState::SendClientInfo( void )
{
	CLC_ClientInfo info;

	info.m_nSendTableCRC = SendTable_GetCRC();
	info.m_nServerCount = m_nServerCount;
	info.m_bIsHLTV = false;
#if !defined( NO_STEAM )
	info.m_nFriendsID = SteamUser() ? SteamUser()->GetSteamID().GetAccountID() : 0;
#else
	info.m_nFriendsID = 0;
#endif
	Q_strncpy( info.m_FriendsName, m_FriendsName, sizeof(info.m_FriendsName) );

	CheckOwnCustomFiles(); // load & verfiy custom player files

	for ( int i=0; i< MAX_CUSTOM_FILES; i++ )
		info.m_nCustomFiles[i] = m_nCustomFiles[i].crc;

	m_NetChannel->SendNetMsg( info );
}
 */
}

func (listener *Connector) Disconnect() {
	listener.activeClient.Disconnect(message.Disconnect(listener.serverChallenge))
}

// NewConnector returns a new connector object.
func NewConnector(activeClient *sourcenet.Client, playerName string, password string, gameVersion string, clientChallenge int32) *Connector {
	return &Connector{
		activeClient: activeClient,
		playerName:      playerName,
		password:        password,
		gameVersion:     gameVersion,
		clientChallenge: clientChallenge,
		connectionStep:  1,
		keepAliveSkips:  0,
	}
}
