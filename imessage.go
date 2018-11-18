package sourcenet

// IMessage interface for any message type we should
// either send or receive
type IMessage interface {
	Connectionless() bool // Is this message a connectionless message
	Data() []byte         // Get message contents
}
