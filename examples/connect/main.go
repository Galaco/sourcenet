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
	if err := steamworks.InitClient(true); err != nil {
		panic(err)
	}
	// target server
	host := "151.80.230.149"
	port := "27015"

	// Connect to host
	client := sourcenet.NewClient()
	if err := client.Connect(host, port); err != nil {
		panic(err)
	}
	defer client.Disconnect(message.Disconnect("Disconnect by User."))

	// Add a receiver for our expected packet type
	playerName := "DormantLemon^___"
	password := "test789"
	gameVersion := "4630212"
	clientChallenge := int32(167679079)

	connector := listener.NewConnector(playerName, password, gameVersion, clientChallenge)
	client.AddListener(connector)

	// Send request to server
	client.SendMessage(connector.InitialMessage(), false)

	// Let us decide when to exit
	reader := bufio.NewReader(os.Stdin)
	log.Println("Enter anything to disconnect: ")
	if _,err := reader.ReadString('\n'); err != nil {
		panic(err)
	}
}
