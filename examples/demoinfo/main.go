// Command demoinfo summarises a Source Engine demo: its header, the send
// tables it carries (with the CRC a client must announce to servers running
// the same game version) and the events produced by replaying it.
//
//	go run ./examples/demoinfo -game tf match.dem
//	go run ./examples/demoinfo -dump tf_tables.bin match.dem
//
// A dump written with -dump can be used by a game profile:
//
//	profile.DataTables = game.DataTablesFromFile("tf_tables.bin")
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sort"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/client"
	"github.com/galaco/sourcenet/demo"
	"github.com/galaco/sourcenet/game"
)

func main() {
	gameName := flag.String("game", "tf", "game profile for user message names: tf, cstrike, dod, hl2mp")
	dump := flag.String("dump", "", "write the demo's data tables to this file")
	flag.Parse()
	if flag.NArg() != 1 {
		log.Fatalf("usage: %s [flags] file.dem", os.Args[0])
	}
	path := flag.Arg(0)

	profiles := map[string]func() *game.Profile{"tf": game.TF2, "cstrike": game.CStrike, "dod": game.DODS, "hl2mp": game.HL2MP}
	newProfile, ok := profiles[*gameName]
	if !ok {
		log.Fatalf("unknown game %q", *gameName)
	}

	tables, err := demo.LoadDataTables(path)
	if err != nil {
		log.Fatalf("data tables: %v", err)
	}
	if *dump != "" {
		w := bitbuf.NewWriter(0)
		tables.WriteDataTables(w)
		if err := os.WriteFile(*dump, w.Bytes(), 0o644); err != nil {
			log.Fatal(err)
		}
	}
	if err := tables.Link(); err != nil {
		log.Fatalf("link data tables: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	p, err := client.NewPlayback(client.Config{Game: newProfile()}, f)
	if err != nil {
		log.Fatal(err)
	}
	h := p.Header()
	fmt.Printf("Demo protocol %d, network protocol %d\n", h.Protocol, h.NetworkProtocol)
	fmt.Printf("Server %q, client %q, map %s, game %s\n", h.ServerName, h.ClientName, h.MapName, h.GameDir)
	fmt.Printf("%.1fs, %d ticks, %d frames\n", h.PlaybackTime, h.PlaybackTicks, h.PlaybackFrames)
	fmt.Printf("%d send tables, %d server classes, send table CRC %#08x\n\n", len(tables.SendTables()), tables.NumClasses(), tables.CRC())

	counts := map[string]int{}
	errors := 0
	c := p.Client()
	for {
		if _, err := p.Step(); err != nil {
			break
		}
		for _, ev := range c.PollEvents() {
			switch e := ev.(type) {
			case client.EventGameEvent:
				counts["game event "+e.Event.Name()]++
			case client.EventUserMessage:
				counts["user message "+e.Name]++
			case client.EventEntities:
				counts["entity updates"]++
			case client.EventTempEntities:
				counts["temp entities"] += len(e.Entities)
			case client.EventError:
				if errors < 10 {
					fmt.Printf("error: %v\n", e.Err)
				}
				errors++
			}
		}
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("%6d  %s\n", counts[k], k)
	}
	if ents := c.Entities(); ents != nil {
		fmt.Printf("\n%d entities at end of demo (tick %d)\n", len(ents.Entities()), c.ServerTick())
	}
	if errors > 0 {
		fmt.Printf("%d decode errors\n", errors)
	}
}
