package sourcenet

// IListener Listener interface for receive packets
type IListener interface {
	Register(*Client)
	Receive(msg IMessage, msgType int)
}
