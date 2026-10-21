// Command connect joins a Source Engine server as a player and prints what
// happens: signon progress, chat, game events and entity counts.
//
// Steam-secured servers require a real Steam auth ticket (see
// client.TicketFunc). Servers running with sv_lan 1 accept the offline
// authenticator used here.
//
//	go run ./examples/connect -game tf -addr 192.168.1.10:27015 -steamid 76561197960287930
//
// Entities are only decoded when the client knows the game's send tables;
// pass a demo recorded on the same game version with -tables.
package main

import (
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/galaco/sourcenet/client"
	"github.com/galaco/sourcenet/game"
	"github.com/galaco/sourcenet/protocol"
	"github.com/galaco/sourcenet/usermessage"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:27015", "server address")
	gameName := flag.String("game", "tf", "game profile: tf, cstrike, dod, hl2mp")
	name := flag.String("name", "sourcenet", "player name")
	password := flag.String("password", "", "server password")
	steamID := flag.Uint64("steamid", 0, "64-bit SteamID claimed when the server asks for Steam auth (LAN servers)")
	tables := flag.String("tables", "", "demo file to load send tables from")
	verbose := flag.Bool("v", false, "debug logging")
	flag.Parse()

	var profile *game.Profile
	switch *gameName {
	case "tf":
		profile = game.TF2()
	case "cstrike":
		profile = game.CStrike()
	case "dod":
		profile = game.DODS()
	case "hl2mp":
		profile = game.HL2MP()
	default:
		log.Fatalf("unknown game %q", *gameName)
	}
	if *tables != "" {
		path := *tables
		profile.DataTables = game.DataTablesFromDemo(path)
	}

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	c := client.New(client.Config{
		Game:     profile,
		Name:     *name,
		Password: *password,
		Auth:     client.OfflineAuth{SteamID: *steamID},
		Logger:   slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})),
	})
	if err := c.Connect(*addr); err != nil {
		log.Fatal(err)
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	status := time.NewTicker(10 * time.Second)
	defer status.Stop()

	for {
		select {
		case <-interrupt:
			c.Disconnect("Disconnect by user.")
			return
		case <-status.C:
			if ents := c.Entities(); ents != nil && c.Active() {
				st := c.Channel().Stats(0)
				fmt.Printf("tick %d, %d entities, ping %v\n", c.ServerTick(), len(ents.Entities()), st.Latency.Round(time.Millisecond))
			}
		case now := <-ticker.C:
			if err := c.Update(now); err != nil {
				log.Fatal(err)
			}
			for _, ev := range c.PollEvents() {
				if done := report(ev); done {
					return
				}
			}
		}
	}
}

func report(ev client.Event) (done bool) {
	switch e := ev.(type) {
	case client.EventConnecting:
		fmt.Printf("connecting (attempt %d)\n", e.Attempt)
	case client.EventConnected:
		fmt.Println("connected")
	case client.EventSignonState:
		fmt.Printf("signon: %s\n", protocol.SignonStateName(e.State))
	case client.EventServerInfo:
		fmt.Printf("server %q map %s, %d players max\n", e.Info.HostName, e.Info.MapName, e.Info.MaxClients)
	case client.EventPrint:
		fmt.Print(e.Text)
	case client.EventUserMessage:
		switch m := e.Message.(type) {
		case usermessage.SayText2:
			fmt.Printf("[chat] %s: %s\n", m.Params[0], m.Params[1])
		case usermessage.TextMsg:
			fmt.Printf("[text] %s\n", m.Name)
		}
	case client.EventGameEvent:
		fmt.Printf("[event] %s\n", e.Event.Name())
	case client.EventError:
		fmt.Printf("warning: %v\n", e.Err)
	case client.EventDisconnected:
		fmt.Printf("disconnected: %s\n", e.Reason)
		return true
	}
	return false
}
