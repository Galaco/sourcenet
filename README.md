[![GoDoc](https://godoc.org/github.com/galaco/sourcenet?status.svg)](https://godoc.org/github.com/galaco/sourcenet)
[![Go report card](https://goreportcard.com/badge/github.com/galaco/sourcenet)](https://goreportcard.com/badge/github.com/galaco/sourcenet)
[![CircleCI](https://circleci.com/gh/Galaco/sourcenet.svg?style=svg)](https://circleci.com/gh/Galaco/sourcenet)

# SourceNet

A Go implementation of the Source Engine multiplayer network protocol (version 24), as spoken by
Team Fortress 2, Counter-Strike: Source, Day of Defeat: Source and Half-Life 2: Deathmatch.
It covers the full client stack: server queries, the connection handshake, the netchannel,
every engine netmessage, string tables, game events, user messages, send tables and entity
decoding, user commands and demo playback.

The library has no dependencies outside the Go standard library and is designed to be embedded
in a game engine: nothing runs in the background except a socket reader, and all protocol work
happens inside `Client.Update`, which the host calls once per frame.

## Status

All layers are implemented and unit tested, including end-to-end tests against an in-process
server built from the same packages. The implementation follows the engine source closely, but
it has not yet been exercised against live servers or real demo recordings. Treat it as
experimental until it has been.

## Packages

| Package | Purpose |
|---|---|
| `bitbuf` | Bit reader/writer matching the engine's `bf_read`/`bf_write`, including coord, normal and varint encodings |
| `protocol` | Protocol constants: limits, flags, signon states, message ids |
| `compression` | LZSS and Snappy codecs used for packets, fragments and string tables |
| `packet` | Datagram framing: split packet reassembly/splitting and compressed packets |
| `netchan` | The network channel: sequencing, reliable subchannels, fragmentation, file transfers, rate control, statistics |
| `message` | Every engine netmessage (`net_*`, `svc_*`, `clc_*`) and per-direction registries |
| `connectionless` | Out-of-band packets: challenge/connect/accept/reject and A2S queries |
| `query` | Blocking A2S_INFO/PLAYER/RULES/ping helpers for server browsers |
| `stringtable` | Network string tables and the `userinfo` player info layout |
| `gameevent` | Game event descriptors, decoding and listen masks |
| `usermessage` | User message registries per game and decoders for common messages |
| `datatable` | Send tables, server classes, property flattening, property codecs and the send table CRC |
| `entity` | Packet entity processing: PVS enter/leave, deltas, baselines, temp entities |
| `usercmd` | `CUserCmd` and its delta encoding |
| `demo` | `.dem` file reader/writer |
| `game` | Per-game profiles (TF2, CS:S, DoD:S, HL2:DM) |
| `client` | The high-level client: handshake, signon, dispatch, user commands and demo playback |

## Usage

### Query a server

```go
info, err := (&query.Client{}).Info(ctx, "1.2.3.4:27015")
fmt.Println(info.Name, info.Map, info.Players, info.MaxPlayers)
```

### Join a server

```go
c := client.New(client.Config{
	Game: game.TF2(),
	Name: "player",
	Auth: client.OfflineAuth{SteamID: 76561197960287930},
})
if err := c.Connect("192.168.1.10:27015"); err != nil {
	log.Fatal(err)
}
for {
	c.Update(time.Now())
	for _, ev := range c.PollEvents() {
		switch e := ev.(type) {
		case client.EventServerInfo:
			fmt.Println("map", e.Info.MapName)
		case client.EventUserMessage:
			if say, ok := e.Message.(usermessage.SayText2); ok {
				fmt.Println(say.Params[0], ":", say.Params[1])
			}
		case client.EventDisconnected:
			return
		}
	}
	time.Sleep(5 * time.Millisecond)
}
```

Movement is supplied through `Config.CreateMove`, chat and console commands with
`SendStringCmd`, and any server message can be observed with `Handle`.

### Play back a demo

```go
p, _ := client.NewPlayback(client.Config{Game: game.TF2()}, file)
for {
	if _, err := p.Step(); err != nil {
		break
	}
	for _, ev := range p.Client().PollEvents() { /* same events as online */ }
}
```

## Things to know

**Authentication.** Servers ask for Steam authentication. `client.OfflineAuth` sends a cookie
without a valid ticket, which only LAN servers (`sv_lan 1`) accept. For internet servers, supply
a Steam session ticket from a Steamworks binding through `client.TicketFunc`.

**Send tables.** Servers do not send their send tables to clients by default (`sv_sendtables 0`).
The client must announce the CRC of the server's tables in `clc_ClientInfo` or it is disconnected
with "Server uses different class tables", and it needs the tables to decode entities. Every demo
contains the tables of the game version it was recorded on, so load them from a recent demo:

```go
profile := game.TF2()
profile.DataTables = game.DataTablesFromDemo("recent.dem")
```

`go run ./examples/demoinfo -dump tf_tables.bin recent.dem` extracts them to a standalone file
for `game.DataTablesFromFile`.

**Product version.** Servers reject clients whose version string differs from their `steam.inf`.
If `Profile.ProductVersion` is empty the client fetches it with an A2S_INFO query first.

**User messages.** Only indices go over the wire, and their meaning depends on the order in which
each game registers messages. The bundled lists come from the reference source; if a game update
adds messages, extend `Profile.UserMessages`.

## Examples

* `examples/serverinfo`: print a server's info, players and rules.
* `examples/connect`: join a server and print chat, game events and entity counts.
* `examples/demoinfo`: summarise a demo, print its send table CRC and dump its tables.

## Credits

Built by reference to the Source Engine source code. Earlier versions drew on
[leystryku/leysourceengineclient](https://github.com/Leystryku/leysourceengineclient).
