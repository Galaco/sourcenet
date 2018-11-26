package message

import "github.com/galaco/bitbuf"

type MsgKeepAlive struct {
	buf *bitbuf.Writer
}

// Connectionless: is this message a connectionless message?
func (msg *MsgKeepAlive) Connectionless() bool {
	return false
}

// Data Get packet data
func (msg *MsgKeepAlive) Data() []byte {
	return msg.buf.Data()
}

// Disconnect returns new disconnect packet data
func KeepAlive(tick int32, hostFrametime uint32, hostFrametimeDeviation uint32) *MsgKeepAlive {
	buf := bitbuf.NewWriter(1024)
	buf.WriteUnsignedBitInt32(3, 6)
	buf.WriteInt32(tick)
	buf.WriteUnsignedBitInt32(hostFrametime, 16)
	buf.WriteUnsignedBitInt32(hostFrametimeDeviation, 16)

	return &MsgKeepAlive{
		buf: buf,
	}
}
