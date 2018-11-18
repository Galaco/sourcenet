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

func NewGeneric(data []byte) *Generic {
	return &Generic{
		data: data,
	}
}
