package message

import "github.com/galaco/bitbuf"

// QueryServerInfo is a connectionless request to a server to obtain
// basic information about its current status
type MsgQueryServerInfo struct {
	buf *bitbuf.Writer
}

// Connectionless: is this message a connectionless message?
func (msg *MsgQueryServerInfo) Connectionless() bool {
	return true
}

// Data Get packet data
func (msg *MsgQueryServerInfo) Data() []byte {
	return msg.buf.Data()
}

// Disconnect returns new disconnect packet data
func QueryServerInfo() *MsgQueryServerInfo {
	buf := bitbuf.NewWriter(64)
	buf.WriteByte(255)
	buf.WriteByte(255)
	buf.WriteByte(255)
	buf.WriteByte(255)
	buf.WriteByte('T')
	buf.WriteString("Source Engine Query\x00")

	return &MsgQueryServerInfo{
		buf: buf,
	}
}
