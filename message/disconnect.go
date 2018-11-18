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

	buf.WriteUint8(1)
	buf.WriteString("Disconnect by User.")
	buf.WriteByte(0xC0)

	return &MsgDisconnect{
		buf: buf.Data(),
	}
}
