# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

`github.com/galaco/sourcenet` is a Go implementation of the Source Engine multiplayer network protocol, version 24 (TF2, CS:S, DoD:S, HL2:DM since the 2013 SteamPipe engine). It is intended to be embedded in the Go engine at `~/code/galaco/source_engine/kero`. The module has no dependencies outside the standard library, which is how it should stay.

The authoritative reference for every wire format is the leaked TF2 engine source at `~/code/source_engine/TF2-Source-Code/tf2_src`. Read it before changing an encoding, and write original Go rather than transliterating it. The most useful files are:
- `engine/net_chan.cpp`, `engine/net_ws.cpp` (netchannel, split/compressed packets)
- `common/netmessages.cpp` (all netmessages)
- `engine/baseclientstate.cpp`, `engine/client.cpp`, `engine/cl_main.cpp`, `engine/baseclient.cpp`, `engine/sv_client.cpp` (handshake and signon from both sides)
- `engine/dt*.cpp`, `engine/cl_ents_parse.cpp` (send tables and entities)
- `engine/networkstringtable.cpp`, `engine/GameEventManager.cpp`, `game/shared/*/..._usermessages.cpp`, `game/shared/usercmd.cpp`

## Commands

```sh
go build ./...
go vet ./...
go test -race ./...               # CI also checks gofmt -l . is empty
go test ./netchan -run TestReliableDeliveryOverLossyLink -v
go run ./examples/serverinfo host:port
go run ./examples/connect -game tf -addr host:port -steamid <id> [-tables recent.dem]
go run ./examples/demoinfo [-dump tables.bin] file.dem
```

Never point tests or examples at real servers without the user's consent; tests use in-memory transports and fake servers.

## Architecture

The layers are separate packages, lowest first. Each lower layer is IO-free and takes the current time as a parameter, so it can be driven deterministically.

`bitbuf` → `compression`, `packet` → `netchan` → `message` → `stringtable` / `gameevent` / `usermessage` / `datatable` → `entity` → `client`

- **Inbound pipeline** (`client.receive`):
  1. UDP datagram → `packet.Decoder` (split reassembly, then decompression of `-3` packets).
  2. Connectionless (`0xFFFFFFFF`) packets go to `connectionless`. Everything else goes to `netchan.Channel.ProcessPacket`.
  3. The channel returns completed reliable blocks first, then the unreliable remainder.
  4. Each is decoded with `message.ReadAll` and dispatched in order by `client.handleMessage`, then user handlers registered with `Client.Handle`.
- **Outbound pipeline:** messages are written into `Channel.Reliable()`/`Unreliable()`. `Channel.Transmit` builds a packet, and `packet.Encoder` compresses and splits it.
- **Messages carry no length.** If one message is mis-decoded, every later message in that packet is lost too. Embedded payloads (entity data, string table updates, user messages, …) are kept as `message.Bits` and decoded by the higher-level packages.
- **Per-game behaviour lives in `game.Profile`:**
  - user message names (only indices are networked, in each game's registration order)
  - the usercmd codec
  - the send tables and their CRC
  - the product version
  - the replay flag
  - message registry customisation

  The engine layer itself is shared by all games.
- **Send tables are not sent by servers by default** (`sv_sendtables 0`, `CGameClient::SendSignonData`).
  - The client must announce a matching CRC (`datatable.Tables.CRC`) and must know the tables locally.
  - They come from demos (`demo.LoadDataTables`, `game.DataTablesFromDemo`).
  - `crcFlags` restores the un-networked `SPROP_ENCODED_AGAINST_TICKCOUNT` flag so a CRC computed from demo tables matches the server's.
- **`client.Client` is single-threaded.** `UDPTransport` reads in a goroutine into a queue, and `Update(now)` does all the work: drain, retry/timeout timers, one usercmd per server tick, sends at `cl_cmdrate`. Events are queued for `PollEvents` and optionally pushed to `Config.OnEvent`.
- **Demo playback** (`client.NewPlayback`) runs demos through the same `Client` in playback mode: sends are no-ops and signon checks are relaxed.

## Conventions and gotchas

- **Deliberate deviations from the engine** are commented where they occur:
  - `netchan` counts each received fragment once.
  - `packet` validates split parts more strictly.
  - `SvcCreateStringTable.TableName` is named so to avoid a clash with `Name()`.
  - The `ClcSaveReplay` 4-bit `StartSendByte` reproduces an engine bug.
- **Tests:**
  - `client/server_test.go` contains a fake server implementing the server side of the signon. Extend it when adding client behaviour.
  - `netchan` tests run two channels over a simulated lossy/reordering link.
- The bundled user message lists date from the 2017-era leak. Live games may have appended messages.
- Steam authentication: `client.OfflineAuth` only satisfies LAN (`sv_lan 1`) servers. Real tickets come from a Steamworks binding via `client.TicketFunc`. The old cgo steamworks dependency was removed on purpose.
