package message

import "github.com/galaco/bitbuf"

const netDisconnect = 1
const netMsgTypeBits = 6

type MsgDisconnect struct {
	buf []byte
}

// Connectionless: is this message a connectionless message?
func (msg *MsgDisconnect) Connectionless() bool {
	return false
}

// Data Get packet data
func (msg *MsgDisconnect) Data() []byte {
	return msg.buf
}

// Disconnect returns new disconnect packet data
func Disconnect(challengeValue int32) *MsgDisconnect {
	buf := bitbuf.NewWriter(1024)

	buf.WriteInt32(challengeValue)
	buf.WriteSignedBitInt32(netDisconnect, netMsgTypeBits)
	buf.WriteString("Disconnect by User.")
	buf.WriteByte(0)
	//buf.WriteSignedBitInt32(0, 2)
	//buf.WriteByte(0xc0)

	return &MsgDisconnect{
		buf: buf.Data(),
	}
}
