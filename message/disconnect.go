package message

import "github.com/galaco/bitbuf"

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
func Disconnect() *MsgDisconnect {
	buf := bitbuf.NewWriter(1024)

	buf.WriteSignedBitInt32(1, 6)
	buf.WriteString("Disconnect by User.")
	buf.WriteSignedBitInt32(0, 2)
	buf.WriteByte(0xc0)

	return &MsgDisconnect{
		buf: buf.Data(),
	}
}
