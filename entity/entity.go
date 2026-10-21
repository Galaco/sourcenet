// Package entity tracks networked entity state from svc_PacketEntities and
// decodes svc_TempEntities, using the flattened server classes from the
// datatable package.
//
// State mirrors the engine's client-side handling (CL_ProcessPacketEntities,
// CBaseClientState::ReadPacketEntities): it keeps the history of received
// frames needed to resolve delta updates, the two entity baseline slots and
// the decoded instance baselines.
package entity

import (
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/datatable"
	"github.com/galaco/sourcenet/protocol"
)

// Entity is the client-side state of one networked entity.
type Entity struct {
	Index  int
	Serial int
	Class  *datatable.ServerClass
	// Props holds the current value of every flattened property of Class,
	// indexed like Class.Props.
	Props []datatable.Value
	// InPVS is false while the entity is dormant (it left the PVS without
	// being deleted).
	InPVS bool
}

// Prop returns the current value of the named property.
func (e *Entity) Prop(name string) (datatable.Value, bool) {
	i := e.Class.PropIndex(name)
	if i < 0 {
		return datatable.Value{}, false
	}
	return e.Props[i], true
}

// PacketEntities mirrors the fields of svc_PacketEntities.
type PacketEntities struct {
	MaxEntries     int
	IsDelta        bool
	DeltaFrom      int32
	Baseline       int
	UpdatedEntries int
	UpdateBaseline bool
	Data           *bitbuf.Reader
}

// BaselineSource returns the instance baseline of a server class: the
// userdata of the "instancebaseline" string table entry named by the decimal
// class ID.
type BaselineSource func(classID int) ([]byte, bool)

// UpdateKind classifies a Change.
type UpdateKind int

// Change kinds.
const (
	// Created reports a newly allocated entity, including recreation after a
	// serial number or class change.
	Created UpdateKind = iota
	// EnteredPVS reports a dormant entity that is transmitted again. Like the
	// engine's SHOULDTRANSMIT_START, it is derived from the frame's transmit
	// set after all updates are applied, so it may accompany an Updated
	// change for the same entity.
	EnteredPVS
	// Updated reports new property values for an existing entity, from a
	// delta update or a full re-send (EnterPVS with an unchanged serial).
	Updated
	// LeftPVS reports an entity that is no longer transmitted and became
	// dormant (SHOULDTRANSMIT_END).
	LeftPVS
	// Deleted reports an entity that was destroyed. Entity holds its last state.
	Deleted
)

func (k UpdateKind) String() string {
	switch k {
	case Created:
		return "created"
	case EnteredPVS:
		return "entered-pvs"
	case Updated:
		return "updated"
	case LeftPVS:
		return "left-pvs"
	case Deleted:
		return "deleted"
	}
	return "unknown"
}

// Change describes what happened to one entity in a packet.
type Change struct {
	Kind   UpdateKind
	Index  int
	Entity *Entity
	// ChangedProps lists the flat property indices written by this update,
	// in ascending order. For entities (re)sent in full it includes the
	// props set from the baseline. It is nil for PVS transitions.
	ChangedProps []int
}

// ErrDeltaFrameNotFound is returned when a delta update references a frame
// the client no longer (or never) had. The caller should request a full
// update by acknowledging delta tick -1.
var ErrDeltaFrameNotFound = errors.New("entity: delta frame not found")

const (
	// maxFrames bounds frame history. The engine only warns past
	// MAX_CLIENT_FRAMES (128) and relies on frames older than the delta
	// source being deleted, which Process also does; servers using
	// sv_maxreplay may delta from older frames, so keep a generous margin.
	maxFrames  = 1024
	bitsetSize = protocol.MaxEdicts / 64
	sentinel   = protocol.EntitySentinel
)

type bitset [bitsetSize]uint64

func (b *bitset) set(i int)      { b[i>>6] |= 1 << uint(i&63) }
func (b *bitset) get(i int) bool { return b[i>>6]&(1<<uint(i&63)) != 0 }

// next returns the first set bit at or after i, or -1.
func (b *bitset) next(i int) int {
	for ; i < protocol.MaxEdicts; i++ {
		w := b[i>>6] >> uint(i&63)
		if w == 0 {
			i |= 63
			continue
		}
		for w&1 == 0 {
			w >>= 1
			i++
		}
		return i
	}
	return -1
}

// frame records which entities a received packet transmitted (CClientFrame).
type frame struct {
	tick     int32
	transmit bitset
	last     int
}

// packed is a decoded property list: the props an entity baseline sets.
type packed struct {
	class   *datatable.ServerClass
	indices []int
	values  []datatable.Value
}

// State tracks entities across packet entities updates.
type State struct {
	tables    *datatable.Tables
	baseline  BaselineSource
	entities  [protocol.MaxEdicts]*Entity
	frames    []*frame
	baselines [2][protocol.MaxEdicts]*packed
	instance  map[int]*packed

	scratch   []datatable.Value
	arena     []int
	classBits int
}

// NewState returns an empty entity state for the given linked tables.
func NewState(tables *datatable.Tables, baseline BaselineSource) *State {
	return &State{tables: tables, baseline: baseline, instance: map[int]*packed{}}
}

// SetTables replaces the server classes (new map or class info) and clears
// all entity state.
func (s *State) SetTables(t *datatable.Tables) {
	s.tables = t
	s.Reset()
}

// SetClassBits sets the width of class IDs in entity headers. The engine
// derives it from svc_ServerInfo.MaxClasses (Q_log2(n)+1); zero uses the
// class count of the tables, which is the same on consistent servers.
func (s *State) SetClassBits(n int) { s.classBits = n }

func (s *State) classIDBits() int {
	if s.classBits > 0 {
		return s.classBits
	}
	return s.tables.ClassBits()
}

// SetBaselineSource replaces the instance baseline lookup.
func (s *State) SetBaselineSource(b BaselineSource) {
	s.baseline = b
	s.InvalidateBaselines()
}

// Reset discards all entities, frames and baselines.
func (s *State) Reset() {
	s.entities = [protocol.MaxEdicts]*Entity{}
	s.frames = nil
	s.baselines = [2][protocol.MaxEdicts]*packed{}
	s.InvalidateBaselines()
}

// InvalidateBaselines drops cached instance baselines; call it when the
// instancebaseline string table changes.
func (s *State) InvalidateBaselines() {
	s.instance = map[int]*packed{}
}

// Entity returns the entity at index, or nil.
func (s *State) Entity(index int) *Entity {
	if index < 0 || index >= protocol.MaxEdicts {
		return nil
	}
	return s.entities[index]
}

// Entities returns all existing entities, dormant ones included, in index
// order.
func (s *State) Entities() []*Entity {
	var out []*Entity
	for _, e := range s.entities {
		if e != nil {
			out = append(out, e)
		}
	}
	return out
}

// HasFrame reports whether a delta update from tick can be decoded.
func (s *State) HasFrame(tick int32) bool { return s.findFrame(tick) != nil }

func (s *State) findFrame(tick int32) *frame {
	for _, f := range s.frames {
		if f.tick == tick {
			return f
		}
	}
	return nil
}

// update types (protocol.h UpdateType).
const (
	enterPVS = iota
	leavePVS
	deltaEnt
	preserveEnt
	finished
)

// header flags (FHDR_*).
const (
	hdrLeavePVS = 1 << 0
	hdrDelete   = 1 << 1
	hdrEnterPVS = 1 << 2
)

type readInfo struct {
	msg         *PacketEntities
	r           *bitbuf.Reader
	from, to    *frame
	oldEntity   int
	newEntity   int
	headerBase  int
	headerCount int
	flags       int
	isEntity    bool
	updateType  int
	changes     []Change
}

func (u *readInfo) nextOldEntity() {
	if u.from == nil {
		u.oldEntity = sentinel
		return
	}
	if n := u.from.transmit.next(u.oldEntity + 1); n >= 0 {
		u.oldEntity = n
	} else {
		u.oldEntity = sentinel
	}
}

// Process applies one svc_PacketEntities message received at serverTick and
// returns the resulting entity changes. When msg.UpdateBaseline is set the
// caller must acknowledge it with clc_BaselineAck{serverTick, msg.Baseline}.
//
// ErrDeltaFrameNotFound is returned, without modifying state, when the delta
// frame is unknown. Other errors leave the state undefined; the connection
// should request a full update.
func (s *State) Process(serverTick int32, msg PacketEntities) ([]Change, error) {
	if s.tables == nil || !s.tables.Linked() {
		return nil, datatable.ErrUnlinked
	}
	if msg.Baseline != 0 && msg.Baseline != 1 {
		return nil, fmt.Errorf("entity: invalid baseline slot %d", msg.Baseline)
	}
	if msg.Data == nil {
		return nil, errors.New("entity: packet entities without data")
	}
	u := &readInfo{msg: &msg, r: msg.Data, headerBase: -1, newEntity: -1, oldEntity: -1, headerCount: msg.UpdatedEntries}
	if msg.IsDelta {
		if serverTick == msg.DeltaFrom {
			return nil, errors.New("entity: update references its own tick")
		}
		if u.from = s.findFrame(msg.DeltaFrom); u.from == nil {
			return nil, ErrDeltaFrameNotFound
		}
	} else {
		for i, e := range s.entities {
			if e != nil {
				u.changes = append(u.changes, Change{Kind: Deleted, Index: i, Entity: e})
				s.entities[i] = nil
			}
		}
	}
	if msg.UpdateBaseline {
		other := 1 - msg.Baseline
		s.baselines[other] = s.baselines[msg.Baseline]
	}
	s.arena = nil // Change slices of earlier calls keep their own storage
	u.to = &frame{tick: serverTick}

	if err := s.readEntities(u); err != nil {
		return u.changes, err
	}
	if msg.IsDelta {
		for u.r.ReadBit() {
			idx := int(u.r.ReadBits(protocol.MaxEdictBits))
			if e := s.entities[idx]; e != nil {
				u.changes = append(u.changes, Change{Kind: Deleted, Index: idx, Entity: e})
				s.entities[idx] = nil
			}
		}
	}
	if err := u.r.Err(); err != nil {
		return u.changes, fmt.Errorf("entity: reading packet entities: %w", err)
	}
	// Dormancy follows the new frame's transmit set (CL_MarkEntitiesOutOfPVS):
	// the server deltas from an older acknowledged frame, so headers alone do
	// not reveal every transition.
	for i, e := range s.entities {
		if e == nil {
			continue
		}
		if in := u.to.transmit.get(i); in != e.InPVS {
			e.InPVS = in
			kind := LeftPVS
			if in {
				kind = EnteredPVS
			}
			u.changes = append(u.changes, Change{Kind: kind, Index: i, Entity: e})
		}
	}

	s.deleteFramesBefore(msg)
	s.frames = append(s.frames, u.to)
	if len(s.frames) > maxFrames {
		s.frames = s.frames[len(s.frames)-maxFrames:]
	}
	return u.changes, nil
}

func (s *State) deleteFramesBefore(msg PacketEntities) {
	if !msg.IsDelta {
		s.frames = s.frames[:0]
		return
	}
	kept := s.frames[:0]
	for _, f := range s.frames {
		if f.tick >= msg.DeltaFrom {
			kept = append(kept, f)
		}
	}
	for i := len(kept); i < len(s.frames); i++ {
		s.frames[i] = nil
	}
	s.frames = kept
}

func (s *State) readEntities(u *readInfo) error {
	u.updateType = preserveEnt
	u.nextOldEntity()
	for u.updateType < finished {
		u.headerCount--
		u.isEntity = u.headerCount >= 0
		if u.isEntity {
			s.parseDeltaHeader(u)
		}
		u.updateType = preserveEnt
		for u.updateType == preserveEnt {
			if !s.determineUpdateType(u) {
				if u.isEntity {
					return fmt.Errorf("entity: header for entity %d past the end of the delta frame", u.newEntity)
				}
				break
			}
			var err error
			switch u.updateType {
			case enterPVS:
				err = s.readEnterPVS(u)
			case leavePVS:
				err = s.readLeavePVS(u)
			case deltaEnt:
				err = s.readDeltaEnt(u)
			case preserveEnt:
				err = s.readPreserveEnt(u)
			}
			if err != nil {
				return err
			}
		}
		if u.r.Overflowed() {
			return fmt.Errorf("entity: reading packet entities: %w", u.r.Err())
		}
	}
	return nil
}

func (s *State) parseDeltaHeader(u *readInfo) {
	u.flags = 0
	u.newEntity = u.headerBase + 1 + int(u.r.ReadUBitVar())
	u.headerBase = u.newEntity
	if !u.r.ReadBit() {
		if u.r.ReadBit() {
			u.flags |= hdrEnterPVS
		}
	} else {
		u.flags |= hdrLeavePVS
		if u.r.ReadBit() {
			u.flags |= hdrDelete
		}
	}
}

// determineUpdateType mirrors CL_DetermineUpdateType; it returns false when
// reading is finished.
func (s *State) determineUpdateType(u *readInfo) bool {
	if !u.isEntity || u.newEntity > u.oldEntity {
		if u.from == nil || u.oldEntity > u.from.last {
			u.updateType = finished
			return false
		}
		u.updateType = preserveEnt
		return true
	}
	switch {
	case u.flags&hdrEnterPVS != 0:
		u.updateType = enterPVS
	case u.flags&hdrLeavePVS != 0:
		u.updateType = leavePVS
	default:
		u.updateType = deltaEnt
	}
	return true
}

func (s *State) checkIndex(i int) error {
	if i < 0 || i >= protocol.MaxEdicts {
		return fmt.Errorf("entity: index %d out of range", i)
	}
	return nil
}

func (s *State) readEnterPVS(u *readInfo) error {
	classID := int(u.r.ReadBits(s.classIDBits()))
	serial := int(u.r.ReadBits(protocol.NumNetworkedEHandleSerialNumberBits))
	if err := s.copyNewEntity(u, classID, serial); err != nil {
		return err
	}
	if u.newEntity == u.oldEntity {
		u.nextOldEntity() // recreate
	}
	return nil
}

func (s *State) copyNewEntity(u *readInfo, classID, serial int) error {
	idx := u.newEntity
	if err := s.checkIndex(idx); err != nil {
		return err
	}
	class := s.tables.Class(classID)
	if class == nil {
		return fmt.Errorf("entity: entity %d has invalid class %d", idx, classID)
	}
	kind := Updated
	ent := s.entities[idx]
	if ent != nil && (ent.Serial != serial || ent.Class != class) {
		u.changes = append(u.changes, Change{Kind: Deleted, Index: idx, Entity: ent})
		ent = nil
	}
	if ent == nil {
		ent = &Entity{Index: idx, Serial: serial, Class: class, Props: make([]datatable.Value, len(class.Props)), InPVS: true}
		s.entities[idx] = ent
		kind = Created
	}

	var base *packed
	if u.msg.IsDelta {
		if bl := s.baselines[u.msg.Baseline][idx]; bl != nil && bl.class == class {
			base = bl
		}
	}
	if base == nil {
		var err error
		if base, err = s.instanceBaseline(class); err != nil {
			return err
		}
	}
	for k, i := range base.indices {
		ent.Props[i] = base.values[k].Clone()
	}
	start := len(s.arena)
	delta, err := datatable.DecodeProps(u.r, class, ent.Props, s.arena)
	if err != nil {
		return fmt.Errorf("entity: entity %d (%s): %w", idx, class.Name, err)
	}
	s.arena = delta
	changed := mergeSorted(base.indices, delta[start:])

	if u.msg.UpdateBaseline {
		bl := &packed{class: class, indices: slices.Clone(changed), values: make([]datatable.Value, len(changed))}
		for k, i := range changed {
			bl.values[k] = ent.Props[i].Clone()
		}
		s.baselines[1-u.msg.Baseline][idx] = bl
	}
	u.to.transmit.set(idx)
	u.to.last = idx
	u.changes = append(u.changes, Change{Kind: kind, Index: idx, Entity: ent, ChangedProps: changed})
	return nil
}

// mergeSorted returns the sorted union of two ascending index lists.
func mergeSorted(a, b []int) []int {
	out := make([]int, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			out = append(out, a[i])
			i++
		case a[i] > b[j]:
			out = append(out, b[j])
			j++
		default:
			out = append(out, a[i])
			i++
			j++
		}
	}
	out = append(out, a[i:]...)
	return append(out, b[j:]...)
}

func (s *State) instanceBaseline(class *datatable.ServerClass) (*packed, error) {
	if p, ok := s.instance[class.ID]; ok {
		return p, nil
	}
	if s.baseline == nil {
		return nil, fmt.Errorf("entity: no instance baseline source for class %q", class.Name)
	}
	data, ok := s.baseline(class.ID)
	if !ok {
		return nil, fmt.Errorf("entity: missing instance baseline for class %q (%d)", class.Name, class.ID)
	}
	if cap(s.scratch) < len(class.Props) {
		s.scratch = make([]datatable.Value, len(class.Props))
	}
	values := s.scratch[:len(class.Props)]
	for i := range values {
		values[i] = datatable.Value{}
	}
	indices, err := datatable.DecodeProps(bitbuf.NewReader(data), class, values, nil)
	if err != nil {
		return nil, fmt.Errorf("entity: instance baseline for %q: %w", class.Name, err)
	}
	p := &packed{class: class, indices: indices, values: make([]datatable.Value, len(indices))}
	for k, i := range indices {
		p.values[k] = values[i].Clone()
	}
	s.instance[class.ID] = p
	return p, nil
}

func (s *State) readLeavePVS(u *readInfo) error {
	if !u.msg.IsDelta {
		return errors.New("entity: leave PVS in a full update")
	}
	idx := u.oldEntity
	if err := s.checkIndex(idx); err != nil {
		return err
	}
	if e := s.entities[idx]; e != nil && u.flags&hdrDelete != 0 {
		s.entities[idx] = nil
		u.changes = append(u.changes, Change{Kind: Deleted, Index: idx, Entity: e})
	}
	u.nextOldEntity()
	return nil
}

func (s *State) readDeltaEnt(u *readInfo) error {
	idx := u.newEntity
	if err := s.checkIndex(idx); err != nil {
		return err
	}
	ent := s.entities[idx]
	if ent == nil {
		return fmt.Errorf("entity: delta update for missing entity %d", idx)
	}
	start := len(s.arena)
	delta, err := datatable.DecodeProps(u.r, ent.Class, ent.Props, s.arena)
	if err != nil {
		return fmt.Errorf("entity: entity %d (%s): %w", idx, ent.Class.Name, err)
	}
	s.arena = delta
	u.to.transmit.set(idx)
	u.to.last = idx
	u.changes = append(u.changes, Change{Kind: Updated, Index: idx, Entity: ent, ChangedProps: delta[start:len(delta):len(delta)]})
	u.nextOldEntity()
	return nil
}

func (s *State) readPreserveEnt(u *readInfo) error {
	if !u.msg.IsDelta {
		return errors.New("entity: preserved entity in a full update")
	}
	idx := u.oldEntity
	if err := s.checkIndex(idx); err != nil {
		return err
	}
	if s.entities[idx] == nil {
		return fmt.Errorf("entity: preserved entity %d does not exist", idx)
	}
	u.to.transmit.set(idx)
	u.to.last = idx
	u.nextOldEntity()
	return nil
}

// BaselineKey returns the instancebaseline string table key for a class ID.
func BaselineKey(classID int) string { return strconv.Itoa(classID) }
