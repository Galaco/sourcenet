package message

import "github.com/galaco/bitbuf"

// MsgConnectionlessQ is the first contact with a server packet
type MsgConnectionlessQ struct {
	buf *bitbuf.Writer
}

// Connectionless is this message a connectionless message?
func (msg *MsgConnectionlessQ) Connectionless() bool {
	return true
}

// Data Get packet data
func (msg *MsgConnectionlessQ) Data() []byte {
	return msg.buf.Data()
}

// ConnectionlessQ returns a new packet
func ConnectionlessQ(clientChallenge int32) *MsgConnectionlessQ {
	buf := bitbuf.NewWriter(1024)

	buf.WriteByte(255)
	buf.WriteByte(255)
	buf.WriteByte(255)
	buf.WriteByte(255)
	buf.WriteByte('q')
	buf.WriteInt32(clientChallenge)
	buf.WriteString("0000000000")
	buf.WriteByte(0)

	return &MsgConnectionlessQ{
		buf: buf,
	}
}
