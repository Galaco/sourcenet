package sourcenet

import (
	"github.com/galaco/bitbuf"
	"sync"
)

// Client is a Source Engine multiplayer client
type Client struct {
	// Interface for sendng and receiving data
	net     *Connection
	channel *Channel

	// FIFO queue of received messages from the server to process
	receivedQueue     []IMessage
	receiveQueueMutex sync.Mutex

	listeners []IListener
}

// Connect Connects to a Source Engine Server
func (client *Client) Connect(host string, port string) error {
	// Establish first connection
	conn, err := Connect(host, port)
	if err != nil {
		return err
	}
	client.net = conn

	// Setup our sending and processing routines
	// These will just run forever, receiving messages, and processing the received queue
	go client.receive()
	go client.process()

	return nil
}

// Disconnect ends the socket connection.
// This must not be confused with sending the disconnect packet to
// a server. Failure to send a disconnect packet before calling Disconnect() will
// result in the server waiting for client packets until it times out.
func (client *Client) Disconnect() {
	client.net.proto.Close()
}

// SendMessage send a message to connected server
func (client *Client) SendMessage(msg IMessage, hasSubChannels bool) bool {
	if msg == nil || msg.Connectionless() == false {
		msg = client.channel.WriteHeader(msg, hasSubChannels)
	}
	if msg == nil {
		return false
	}
	client.net.Send(msg)

	return true
}

// AddListener adds a callback handler for packet data
func (client *Client) AddListener(target IListener) {
	target.Register(client)
	client.listeners = append(client.listeners, target)
}

// Receive Goroutine that receives messages as they come in.
// This adds messages to the end of a received queue, so its possible they may be delayed in processing
func (client *Client) receive() {
	for true {
		client.channel.ProcessPacket(client.net.Receive())
		if client.channel.WaitingOnFragments() == true {
			// @TODO send
		}
		client.receiveQueueMutex.Lock()
		client.receivedQueue = append(client.receivedQueue, client.channel.GetMessages()...)
		client.receiveQueueMutex.Unlock()
	}
}

// process Goroutine that repeatedly reads and removes received messages
// from the queue.
// This will not empty the queue each loop, but will process all messages that existed at the
// start of each loop
func (client *Client) process() {
	queueSize := 0
	i := 0
	for true {
		queueSize = len(client.receivedQueue)
		if queueSize == 0 {
			continue
		}

		for i = 0; i < queueSize; i++ {
			// Do actual processing
			msgType := uint32(packetHeaderFlagQuery)
			if client.receivedQueue[i].Connectionless() == true {
				msgType, _ = bitbuf.NewReader(client.receivedQueue[i].Data()).ReadUint32Bits(netmsgTypeBits)
			}
			for _, listen := range client.listeners {
				listen.Receive(client.receivedQueue[i], int(msgType))
			}
		}

		// Clear read messages from the queue
		client.receiveQueueMutex.Lock()
		client.receivedQueue = client.receivedQueue[queueSize:]
		client.receiveQueueMutex.Unlock()
	}
}

// NewClient returns a new client object
func NewClient() *Client {
	return &Client{
		channel:       NewChannel(),
		receivedQueue: make([]IMessage, 0),
		listeners:     make([]IListener, 0),
	}
}
