package sourcenet

import (
	"github.com/galaco/bitbuf"
	"github.com/galaco/sourcenet/message"
	"log"
	"sync"
	"time"
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

	disconnected bool
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
func (client *Client) SendMessage(msg IMessage, hasSubChannels bool) bool {
	if msg == nil {
		return false
	}
	if msg.Connectionless() == false {
		msg = client.channel.WriteHeader(msg, hasSubChannels)
	}
	_,err := client.net.Send(msg)
	if err != nil {
		return false
	}

	return true
}

func (client *Client) AddListener(target IListener) {
	client.listeners = append(client.listeners, target)
}

// receive Goroutine that receives messages as they come in.
// This adds messages to the end of a received queue, so its possible they may be delayed in processing
func (client *Client) receive() {
	for true {
		log.Println("in loop receive")
		if client.disconnected == true {
			return
		}
		client.channel.ProcessPacket(client.net.Receive())
		if client.channel.WaitingOnFragments() == true {
			log.Println("waiting on fragments")
			buf := bitbuf.NewWriter(1024)
			buf.WriteSignedBitInt32(0, 1)
			buf.WriteSignedBitInt32(0, 1)
			client.SendMessage(message.NewGenericDatagram(buf.Data()), true)
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
		if client.disconnected == true {
			return
		}
		queueSize = len(client.receivedQueue)
		if queueSize == 0 {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		log.Println("in loop process - items to process")

		for i = 0; i < queueSize; i++ {
			log.Println("in loop process:queueSize")
			// Do actual processing
			msgType := uint8(0)
			if client.receivedQueue[i].Connectionless() == true {
				msgTypeL, _ := bitbuf.NewReader(client.receivedQueue[i].Data()).ReadUint32Bits(netmsgTypeBits)
				msgType = uint8(msgTypeL)
				log.Printf("Message type: Long: %d, short: %d\n", msgTypeL, msgType)
			}
			for _, listen := range client.listeners {
				log.Println("in loop process:listeners")
				listen.Receive(client.receivedQueue[i], int(msgType))
			}
		}

		// Clear read messages from the queue
		client.receiveQueueMutex.Lock()
		client.receivedQueue = client.receivedQueue[queueSize:]
		client.receiveQueueMutex.Unlock()

		time.Sleep(20 * time.Millisecond)
	}
}

func (client *Client) Channel() *Channel {
	return client.channel
}

func (client *Client) Disconnect(msg IMessage) {
	// kill the send/receive routines
	log.Println("Disconnect")
	client.disconnected = true
	client.channel.challengeValueInStream = false // challenge is in message content in this case
	client.SendMessage(msg, false)
	client.net.Disconnect()
}

// NewClient returns a new client object
func NewClient() *Client {
	return &Client{
		channel:       NewChannel(),
		receivedQueue: make([]IMessage, 0),
		listeners:     make([]IListener, 0),
	}
}
