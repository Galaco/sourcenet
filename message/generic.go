package message

type Generic struct {
	data []byte
}

// Connectionless: is this message a connectionless message?
func (msg *Generic) Connectionless() bool {
	if len(msg.data) < 4 {
		return false
	}
	if msg.data[0] == 255 && msg.data[1] == 255 && msg.data[2] == 255 && msg.data[3] == 255 {
		return true
	}

	return false
}

// Data: Get packet data
func (msg *Generic) Data() []byte {
	return msg.data
}

func NewMessage(data []byte) *Generic {
	return &Generic{
		data: data,
	}
}

// Header: Connected packet header
type Header struct {
	Sequence    int32
	SequenceAck int32
	Flags       uint8
	Checksum    uint16
	RelState    uint8
	NumChoked   uint8 // Only set if Flags includes choked
}
