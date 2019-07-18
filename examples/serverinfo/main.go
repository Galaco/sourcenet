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
	host := "151.80.230.149"
	port := "27015"

	// Connect to host
	client := sourcenet.NewClient()
	if err := client.Connect(host, port); err != nil {
		panic(err)
	}
	defer client.Disconnect(nil)

	// Add a receiver for our expected packet type
	client.AddListener(&QueryInfoReceiver{})

	// Send request to server
	client.SendMessage(message.QueryServerInfo(), false)

	// Let us decide when to exit
	reader := bufio.NewReader(os.Stdin)
	log.Println("Enter anything to disconnect: ")
	if _, err := reader.ReadString('\n'); err != nil {
		panic(err)
	}
}

// QueryInfoReceiver is a Callback struct for out client
// The client operates by passing received messages into listeners to process expected packets
type QueryInfoReceiver struct {
}

// Register fulfills interface
func (listener *QueryInfoReceiver) Register(client *sourcenet.Client) {

}

// Receive is a handler for server response packets
func (listener *QueryInfoReceiver) Receive(msg sourcenet.IMessage, msgType int) {
	data := msg.Data()
	log.Println(strings.Split(string(data), "\x00"))

	props := strings.Split(string(data[6:]), "\x00")
	log.Println("Server name: " + props[0])
	log.Println("Map: " + props[1])
	log.Println("Game id: " + props[2])
	log.Println("Game mode: " + props[3])
	// Playercount
	currentPlayers := 0
	totalPlayers := int([]byte(props[6])[0])
	if props[5] != "" {
		currentPlayers = int([]byte(props[5])[0])
		totalPlayers = int([]byte(props[5])[1])
	}
	log.Printf("Players: %d/%d\n", currentPlayers, totalPlayers)
}
