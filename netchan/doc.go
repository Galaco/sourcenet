// Package netchan implements the Source Engine network channel (CNetChan):
// sequenced datagrams carrying an unreliable payload plus reliable data that
// is fragmented, sent over eight alternating-bit subchannels and resent until
// acknowledged.
//
// A Channel performs no I/O and starts no goroutines. Callers feed it the
// logical packets received from the peer (after split packet reassembly and
// decompression, see package packet) and ask it to build outgoing packets,
// passing the current time to every call. This makes it straightforward to
// drive from a game loop and to test deterministically.
//
// The channel transports opaque bit streams. Reliable and unreliable message
// payloads are written into the writers returned by Reliable and Unreliable
// (usually with package message), and incoming payloads are returned as
// readers in the order the engine processes them: completed reliable blocks
// first, then the unreliable remainder of the packet.
package netchan
