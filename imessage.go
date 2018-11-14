package sourcenet

type IMessage interface {
	Connectionless() bool // Is this message a connectionless message
	Data() []byte         // Get message contents
}