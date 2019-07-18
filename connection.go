package sourcenet

import (
	"fmt"
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
	if _, err := conn.proto.Read(buf); err != nil {
		return message.NewGeneric(buf).WithError(err)
	}
	return message.NewGeneric(buf)
}

// Connect Establishes a connection with a server.
// Only ensures target ip:port is reachable.
func Connect(host string, port string) (*Connection, error) {
	proto, err := net.Dial("udp", fmt.Sprintf("%s:%s", host, port))
	if err != nil {
		return nil, err
	}
	return &Connection{
		proto: proto,
	}, nil
}
