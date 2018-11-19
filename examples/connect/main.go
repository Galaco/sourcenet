package main

import (
	"bufio"
	"github.com/BenLubar/steamworks"
	"github.com/galaco/sourcenet"
	"github.com/galaco/sourcenet/listener"
	"github.com/galaco/sourcenet/message"
	"log"
	"os"
)

func main() {
	// REQUIRES STEAM RUNNING
	err := steamworks.InitClient(true)
	if err != nil {
		log.Println(err)
	}
	// target server
	host := "142.44.143.138"
	port := "27015"

	// Connect to host
	client := sourcenet.NewClient()
	client.Connect(host, port)
	defer client.SendMessage(message.Disconnect(), false)

	// Add a receiver for our expected packet type
	playerName := "DormantLemon^___"
	password := "test789"
	gameVersion := "4630212"
	clientChallenge := int32(167679079)

	connector := listener.NewConnector(client, playerName, password, gameVersion, clientChallenge)
	client.AddListener(connector)

	// Send request to server
	client.SendMessage(connector.InitialMessage(), false)

	// Let us decide when to exit
	reader := bufio.NewReader(os.Stdin)
	log.Println("Enter anything to disconnect: ")
	reader.ReadString('\n')
}
