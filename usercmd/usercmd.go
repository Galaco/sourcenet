// Package usercmd implements the player input command (CUserCmd) and the
// delta encoding used to send it to the server inside clc_Move messages.
//
// Each command is encoded against the previous one: every field is preceded
// by a single bit saying whether it changed. The first command of a
// clc_Move is encoded against a zeroed command.
package usercmd

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/protocol"
)

// WeaponSubtypeBits is the number of bits used for UserCmd.WeaponSubtype.
const WeaponSubtypeBits = 6

// Button bits for UserCmd.Buttons (in_buttons.h).
const (
	InAttack    = 1 << 0
	InJump      = 1 << 1
	InDuck      = 1 << 2
	InForward   = 1 << 3
	InBack      = 1 << 4
	InUse       = 1 << 5
	InCancel    = 1 << 6
	InLeft      = 1 << 7
	InRight     = 1 << 8
	InMoveLeft  = 1 << 9
	InMoveRight = 1 << 10
	InAttack2   = 1 << 11
	InRun       = 1 << 12
	InReload    = 1 << 13
	InAlt1      = 1 << 14
	InAlt2      = 1 << 15
	InScore     = 1 << 16
	InSpeed     = 1 << 17
	InWalk      = 1 << 18
	InZoom      = 1 << 19
	InWeapon1   = 1 << 20
	InWeapon2   = 1 << 21
	InBullrush  = 1 << 22
	InGrenade1  = 1 << 23
	InGrenade2  = 1 << 24
	InAttack3   = 1 << 25
)

// UserCmd is one tick of player input.
type UserCmd struct {
	CommandNumber int32
	TickCount     int32
	ViewAngles    [3]float32
	ForwardMove   float32
	SideMove      float32
	UpMove        float32
	Buttons       int32
	Impulse       uint8
	WeaponSelect  int32
	WeaponSubtype int32
	// RandomSeed is not networked; the server derives it from the command
	// number (see RandomSeed). ReadDelta fills it in the same way.
	RandomSeed int32
	MouseDX    int16
	MouseDY    int16
	// GroundContacts is only networked by the HL2 codec.
	GroundContacts []GroundContact
}

// GroundContact describes an entity the player is standing on (HL2 only).
type GroundContact struct {
	EntIndex  int32
	MinHeight float32
	MaxHeight float32
}

// RandomSeed returns the prediction random seed the engine derives from a
// command number: four middle bytes of the MD5 of the little-endian number,
// masked to 31 bits (MD5_PseudoRandom).
func RandomSeed(commandNumber int32) int32 {
	var in [4]byte
	binary.LittleEndian.PutUint32(in[:], uint32(commandNumber))
	sum := md5.Sum(in[:])
	return int32(binary.LittleEndian.Uint32(sum[6:]) & 0x7fffffff)
}

// Codec encodes and decodes user command deltas. Games differ only in
// optional trailing fields.
type Codec interface {
	// WriteDelta writes to as a delta against from.
	WriteDelta(w *bitbuf.Writer, from, to *UserCmd)
	// ReadDelta reads a command encoded as a delta against from.
	ReadDelta(r *bitbuf.Reader, from *UserCmd) (UserCmd, error)
}

// Standard is the codec of Source 2013 multiplayer games (TF2, CS:S, DoD:S).
var Standard Codec = codec{}

// HL2 additionally networks entity ground contacts. It is compiled into
// Half-Life 2 based client DLLs (HL2_CLIENT_DLL), which includes Half-Life 2:
// Deathmatch (client_hl2mp.vpc), as well as HL2, the episodes and Portal.
var HL2 Codec = codec{groundContacts: true}

type codec struct {
	groundContacts bool
}

func (c codec) WriteDelta(w *bitbuf.Writer, from, to *UserCmd) {
	if from == nil {
		from = &UserCmd{}
	}
	if to.CommandNumber != from.CommandNumber+1 {
		w.WriteBit(true)
		w.WriteBits(uint32(to.CommandNumber), 32)
	} else {
		w.WriteBit(false)
	}
	if to.TickCount != from.TickCount+1 {
		w.WriteBit(true)
		w.WriteBits(uint32(to.TickCount), 32)
	} else {
		w.WriteBit(false)
	}
	for i := 0; i < 3; i++ {
		writeFloat(w, from.ViewAngles[i], to.ViewAngles[i])
	}
	writeFloat(w, from.ForwardMove, to.ForwardMove)
	writeFloat(w, from.SideMove, to.SideMove)
	writeFloat(w, from.UpMove, to.UpMove)
	if to.Buttons != from.Buttons {
		w.WriteBit(true)
		w.WriteBits(uint32(to.Buttons), 32)
	} else {
		w.WriteBit(false)
	}
	if to.Impulse != from.Impulse {
		w.WriteBit(true)
		w.WriteBits(uint32(to.Impulse), 8)
	} else {
		w.WriteBit(false)
	}
	if to.WeaponSelect != from.WeaponSelect {
		w.WriteBit(true)
		w.WriteBits(uint32(to.WeaponSelect), protocol.MaxEdictBits)
		if to.WeaponSubtype != from.WeaponSubtype {
			w.WriteBit(true)
			w.WriteBits(uint32(to.WeaponSubtype), WeaponSubtypeBits)
		} else {
			w.WriteBit(false)
		}
	} else {
		w.WriteBit(false)
	}
	if to.MouseDX != from.MouseDX {
		w.WriteBit(true)
		w.WriteInt16(to.MouseDX)
	} else {
		w.WriteBit(false)
	}
	if to.MouseDY != from.MouseDY {
		w.WriteBit(true)
		w.WriteInt16(to.MouseDY)
	} else {
		w.WriteBit(false)
	}
	if c.groundContacts {
		// Written whenever present, regardless of the previous command.
		if len(to.GroundContacts) != 0 {
			w.WriteBit(true)
			w.WriteInt16(int16(len(to.GroundContacts)))
			for _, g := range to.GroundContacts {
				w.WriteBits(uint32(g.EntIndex), protocol.MaxEdictBits)
				w.WriteBitCoord(g.MinHeight)
				w.WriteBitCoord(g.MaxHeight)
			}
		} else {
			w.WriteBit(false)
		}
	}
}

func writeFloat(w *bitbuf.Writer, from, to float32) {
	if to != from {
		w.WriteBit(true)
		w.WriteFloat32(to)
	} else {
		w.WriteBit(false)
	}
}

// ReadDelta follows the engine's ReadUsercmd: fields that are not sent keep
// the value from the previous command (including ground contacts), command
// and tick numbers increment, and RandomSeed is derived from the command
// number.
func (c codec) ReadDelta(r *bitbuf.Reader, from *UserCmd) (UserCmd, error) {
	if from == nil {
		from = &UserCmd{}
	}
	cmd := *from
	if len(from.GroundContacts) > 0 {
		cmd.GroundContacts = append([]GroundContact(nil), from.GroundContacts...)
	}
	if r.ReadBit() {
		cmd.CommandNumber = int32(r.ReadBits(32))
	} else {
		cmd.CommandNumber = from.CommandNumber + 1
	}
	if r.ReadBit() {
		cmd.TickCount = int32(r.ReadBits(32))
	} else {
		cmd.TickCount = from.TickCount + 1
	}
	for i := 0; i < 3; i++ {
		if r.ReadBit() {
			cmd.ViewAngles[i] = r.ReadFloat32()
		}
	}
	if r.ReadBit() {
		cmd.ForwardMove = r.ReadFloat32()
	}
	if r.ReadBit() {
		cmd.SideMove = r.ReadFloat32()
	}
	if r.ReadBit() {
		cmd.UpMove = r.ReadFloat32()
	}
	if r.ReadBit() {
		cmd.Buttons = int32(r.ReadBits(32))
	}
	if r.ReadBit() {
		cmd.Impulse = uint8(r.ReadBits(8))
	}
	if r.ReadBit() {
		cmd.WeaponSelect = int32(r.ReadBits(protocol.MaxEdictBits))
		if r.ReadBit() {
			cmd.WeaponSubtype = int32(r.ReadBits(WeaponSubtypeBits))
		}
	}
	cmd.RandomSeed = RandomSeed(cmd.CommandNumber)
	if r.ReadBit() {
		cmd.MouseDX = r.ReadInt16()
	}
	if r.ReadBit() {
		cmd.MouseDY = r.ReadInt16()
	}
	if c.groundContacts && r.ReadBit() {
		n := int(r.ReadInt16())
		if n < 0 || n > protocol.MaxEdicts {
			return cmd, fmt.Errorf("usercmd: invalid ground contact count %d", n)
		}
		cmd.GroundContacts = make([]GroundContact, n)
		for i := range cmd.GroundContacts {
			cmd.GroundContacts[i] = GroundContact{
				EntIndex:  int32(r.ReadBits(protocol.MaxEdictBits)),
				MinHeight: r.ReadBitCoord(),
				MaxHeight: r.ReadBitCoord(),
			}
		}
	}
	if err := r.Err(); err != nil {
		return cmd, fmt.Errorf("usercmd: %w", err)
	}
	return cmd, nil
}

// ClampCounts limits the numbers of new and backup commands to what a
// clc_Move message can carry.
func ClampCounts(newCommands, backupCommands int) (int, int) {
	return clamp(newCommands, 0, protocol.MaxNewCommands), clamp(backupCommands, 0, protocol.MaxBackupCommands)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// BuildMove encodes the payload of a clc_Move message, as CL_SendMove does.
// history holds the most recent commands, oldest first; its last
// newCommands+backupCommands entries are written (missing older entries are
// treated as zeroed commands). The first written command is encoded against
// a zeroed command and each following one against its predecessor.
//
// The counts are clamped with ClampCounts; the message header must carry the
// clamped values.
func BuildMove(codec Codec, history []UserCmd, newCommands, backupCommands int) (data []byte, nbits int) {
	newCommands, backupCommands = ClampCounts(newCommands, backupCommands)
	total := newCommands + backupCommands
	w := bitbuf.NewWriter(64)
	var zero UserCmd
	from := &zero
	for i := 0; i < total; i++ {
		idx := len(history) - total + i
		to := &zero
		if idx >= 0 {
			to = &history[idx]
		}
		codec.WriteDelta(w, from, to)
		from = to
	}
	return w.Bytes(), w.BitsWritten()
}

// ReadMove decodes count commands from a clc_Move payload, oldest first, as
// the server does in ProcessUsercmds. The first command is decoded against a
// zeroed command.
func ReadMove(codec Codec, r *bitbuf.Reader, count int) ([]UserCmd, error) {
	out := make([]UserCmd, 0, count)
	from := &UserCmd{}
	for i := 0; i < count; i++ {
		cmd, err := codec.ReadDelta(r, from)
		if err != nil {
			return out, err
		}
		out = append(out, cmd)
		from = &out[len(out)-1]
	}
	return out, nil
}
