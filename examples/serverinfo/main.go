// Command serverinfo prints the A2S information, players and rules of a
// Source Engine server, as shown in the Steam server browser.
//
//	go run ./examples/serverinfo 127.0.0.1:27015
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/galaco/sourcenet/query"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("usage: %s host:port", os.Args[0])
	}
	addr := os.Args[1]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	q := &query.Client{Retries: 1}

	info, err := q.Info(ctx, addr)
	if err != nil {
		log.Fatalf("info: %v", err)
	}
	fmt.Printf("Name:     %s\n", info.Name)
	fmt.Printf("Map:      %s\n", info.Map)
	fmt.Printf("Game:     %s (%s, app %d)\n", info.Game, info.Folder, info.AppID)
	fmt.Printf("Players:  %d/%d (%d bots)\n", info.Players, info.MaxPlayers, info.Bots)
	fmt.Printf("Version:  %s\n", info.Version)
	fmt.Printf("VAC:      %v\n", info.VAC == 1)
	if info.Keywords != "" {
		fmt.Printf("Keywords: %s\n", info.Keywords)
	}

	if ping, err := q.Ping(ctx, addr); err == nil {
		fmt.Printf("Ping:     %v\n", ping.Round(time.Millisecond))
	}

	players, err := q.Players(ctx, addr)
	if err != nil {
		log.Printf("players: %v", err)
	} else {
		fmt.Println("\nPlayers:")
		for _, p := range players {
			fmt.Printf("  %-32s score %4d  %v\n", p.Name, p.Score, time.Duration(p.Duration*float32(time.Second)).Round(time.Second))
		}
	}

	rules, err := q.Rules(ctx, addr)
	if err != nil {
		log.Printf("rules: %v", err)
		return
	}
	fmt.Println("\nRules:")
	for _, r := range rules.Rules {
		fmt.Printf("  %s = %s\n", r.Name, r.Value)
	}
}
