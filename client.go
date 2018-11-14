package sourcenet

import (
	"sync"
)

// Client is a Source Engine multiplayer client
type Client struct {
	// Interface for sendng and receiving data
	net *Connection
	channel Channel

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

// SendMessage send a message to connected server
func (client *Client) SendMessage(msg IMessage) {
	client.net.Send(msg)
}

func (client *Client) AddListener(target IListener) {
	target.Register(client)
	client.listeners = append(client.listeners, target)
}

// receive Goroutine that receives messages as they come in.
// This adds messages to the end of a received queue, so its possible they may be delayed in processing
func (client *Client) receive() {
	for true {
		client.channel.ProcessPacket(client.net.Receive())
		client.receiveQueueMutex.Lock()
		client.receivedQueue = append(client.receivedQueue, client.channel.receivedProcessed...)
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
			msgType := -1
			if client.receivedQueue[i].Connectionless() == true {
				msgType,_ = (bifBuf.NewReader(client.receivedQueue[i].Data()).ReadUnsignedInt32Bits(netMsgTypeBits))
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
		receivedQueue: make([]IMessage, 0),
		listeners:     make([]IListener, 0),
	}
}
