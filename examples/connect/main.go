package main

import (
	"bufio"
	"github.com/BenLubar/steamworks"
	"github.com/galaco/sourcenet"
	"github.com/galaco/sourcenet/listener"
	"log"
	"os"
	"time"
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

	// Add a receiver for our expected packet type
	playerName := "DormantLemon^___"
	password := "test789"
	gameVersion := "4630212"
	clientChallenge := int32(167679079)

	connector := listener.NewConnector(client, playerName, password, gameVersion, clientChallenge)
	client.AddListener(connector)
	defer connector.Disconnect()

	// Send request to server
	client.SendMessage(connector.InitialMessage(), false)

	time.Sleep(20 * time.Second)

	defer connector.Disconnect()

	// Let us decide when to exit
	log.Println("Enter anything to disconnect: ")
	input := bufio.NewScanner(os.Stdin)
	input.Scan()

	log.Println("Exiting...")
}
