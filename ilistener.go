package network

// IListener Listener interface for receive packets
type IListener interface {
	Register(*Client)
	Receive(msg IMessage)
}
