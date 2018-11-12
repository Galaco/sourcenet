package message

import "github.com/galaco/bitbuf"

type MsgConnectionlessK struct {
	buf *bitbuf.Writer
}

// Connectionless: is this message a connectionless message?
func (msg *MsgConnectionlessK) Connectionless() bool {
	return true
}

// Data Get packet data
func (msg *MsgConnectionlessK) Data() []byte {
	return msg.buf.Data()
}

// Disconnect returns new disconnect packet data
func ConnectionlessK(clientChallenge int32, serverChallenge int32, playerName string, password string, gameVersion string, steamId uint64, steamKey []byte) *MsgConnectionlessK {
	buf := bitbuf.NewWriter(1024)
	senddata := bitbuf.NewWriter(1000)
	senddata.WriteByte(255)
	senddata.WriteByte(255)
	senddata.WriteByte(255)
	senddata.WriteByte(255)
	senddata.WriteByte('k')
	senddata.WriteInt32(0x18)
	senddata.WriteInt32(0x03)
	senddata.WriteInt32(serverChallenge)
	senddata.WriteInt32(clientChallenge)
	//senddata.WriteUint32(2729496039)
	senddata.WriteString(playerName) //player name
	senddata.WriteByte(0)
	senddata.WriteString(password) //password
	senddata.WriteByte(0)
	senddata.WriteString(gameVersion) //game version
	senddata.WriteByte(0)

	senddata.WriteInt16(242)
	senddata.WriteUint64(steamId)

	if len(steamKey) > 0 {
		senddata.WriteBytes(steamKey)
	}

	return &MsgConnectionlessK{
		buf: buf,
	}
}
