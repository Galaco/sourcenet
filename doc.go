// Package sourcenet is the root of a Go implementation of the Source Engine
// multiplayer network protocol (version 24: Team Fortress 2,
// Counter-Strike: Source, Day of Defeat: Source, Half-Life 2: Deathmatch).
//
// The functionality lives in subpackages, from the wire upwards:
//
//   - bitbuf: engine-compatible bit reader and writer
//   - compression, packet: LZSS/Snappy, split and compressed datagrams
//   - netchan: the reliable/unreliable network channel
//   - message: every engine netmessage
//   - connectionless, query: handshake packets and A2S server queries
//   - stringtable, gameevent, usermessage, datatable, entity, usercmd:
//     the game state carried by the messages
//   - demo: .dem files
//   - game: per-game profiles
//   - client: a complete client, also able to replay demos
//
// Most applications only need packages client, game and query.
package sourcenet
