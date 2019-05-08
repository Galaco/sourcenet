package sourcenet

import (
	"github.com/galaco/sourcenet/message"
	"net"
)

// Connection is A UDP Connection for sending and receiving messages
// to a Source Engine server
type Connection struct {
	proto net.Conn
}

// Send Sends a passed message to connected server
func (conn *Connection) Send(msg IMessage) (length int, err error) {
	return conn.proto.Write(msg.Data())
}

// Receive waits for a message from connected server
func (conn *Connection) Receive() IMessage {
	buf := make([]byte, 2048)
	conn.proto.Read(buf)
	return message.NewGeneric(buf)
}

// Connect Establishes a connection with a server.
// Only ensures target ip:port is reachable.
func Connect(host string, port string) (*Connection, error) {
	conn := Connection{}
	proto, err := net.Dial("udp", host+":"+port)
	if err != nil {
		return nil, err
	}
	conn.proto = proto

	return &conn, nil
}
