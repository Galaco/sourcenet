package sourcenet

// IListener Listener interface for receive packets
type IListener interface {
	Receive(msg IMessage, msgType int)
}
