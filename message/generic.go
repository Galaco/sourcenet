package message

// Generic is a unknown message type. It should still contain the same header structure
// as known packet types, but the payload is unknown
type Generic struct {
	data []byte
	err  error
}

// WithError associate an error with this message.
// Errors would tend to be related to malformed data.
func (msg *Generic) WithError(err error) *Generic {
	msg.err = err
	return msg
}

// Connectionless is this message a connectionless message?
func (msg *Generic) Connectionless() bool {
	if len(msg.data) < 4 {
		return false
	}
	if msg.data[0] == 255 && msg.data[1] == 255 && msg.data[2] == 255 && msg.data[3] == 255 {
		return true
	}

	return false
}

// Data gets packet data
func (msg *Generic) Data() []byte {
	return msg.data
}

// NewGeneric returns a new generic packet
func NewGeneric(data []byte) *Generic {
	return &Generic{
		data: data,
	}
}
