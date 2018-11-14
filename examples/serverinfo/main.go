package main

import (
	"bufio"
	"github.com/galaco/sourcenet"
	"github.com/galaco/sourcenet/message"
	"log"
	"os"
	"strings"
)

func main() {
	// target server
	host := "142.44.143.138"
	port := "27015"

	// Connect to host
	client := sourcenet.NewClient()
	client.Connect(host, port)
	defer client.SendMessage(message.Disconnect(), false)

	// Add a receiver for our expected packet type
	client.AddListener(&QueryInfoReceiver{})

	// Send request to server
	client.SendMessage(message.QueryServerInfo(), false)

	// Let us decide when to exit
	reader := bufio.NewReader(os.Stdin)
	log.Println("Enter anything to disconnect: ")
	reader.ReadString('\n')
}

// Callback struct for out client
// The client operates by passing received messages into listeners to process expected packets
type QueryInfoReceiver struct {
}

func (listener *QueryInfoReceiver) Register(client *sourcenet.Client) {

}

func (listener *QueryInfoReceiver) Receive(msg sourcenet.IMessage, msgType int) {
	data := msg.Data()

	props := strings.Split(string(data[6:]), "\x00")
	log.Println("Server name: " + props[0])
	log.Println("Map: " + props[1])
	log.Println("Game id: " + props[2])
	log.Println("Game mode: " + props[3])
	//log.Printf("Players: %d/%d\n", uint8(props[5][0]), uint8(props[5][1]))
}
