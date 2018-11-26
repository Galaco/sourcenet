package message

// Generic is a packet struct for when the data we need to send
// or receive is not known or buildable ahead of time, or when
// we don't actually care about the packet type (useful for passing
// data around internally).
type Generic struct {
	data []byte
}

// Connectionless: is this message a connectionless message? This resolves
// based on whether the first 4 bytes = [4]byte{255,255,255,255}. This means
// that it could be possible a connected packet could resolve as connectionless
// in exceedingly rare scenarios. Use GenericDatagram in cases where this may
// happen
func (msg *Generic) Connectionless() bool {
	if len(msg.data) < 4 {
		return false
	}
	if msg.data[0] == 255 && msg.data[1] == 255 && msg.data[2] == 255 && msg.data[3] == 255 {
		return true
	}

	return false
}

// Data returns packet data
func (msg *Generic) Data() []byte {
	return msg.data
}

// NewGeneric builds a new generic packet to send.
// Note that if the packet is connectionless it does need the 4byte header prepended
func NewGeneric(data []byte) *Generic {
	return &Generic{
		data: data,
	}
}


// GenericDatagram avoids resolution of packet type issues
// These packets are always treated as datagrams, never as connectionless
// Only really useful for building sendable packets.
type GenericDatagram struct {
	Generic
}

// Connectionless: is this message a connectionless message?
func (msg *GenericDatagram) Connectionless() bool {
	return false
}

// NewGenericDatagram returns a generic packet, but that will always be treated as
// a datagram/non-connectionless packet
func NewGenericDatagram(data []byte) *GenericDatagram {
	return &GenericDatagram{
		Generic: *NewGeneric(data),
	}
}

