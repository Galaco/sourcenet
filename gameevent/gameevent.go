// Package gameevent decodes the engine's game events. The server describes
// every networked event and its keys with svc_GameEventList; individual
// events then arrive as compact svc_GameEvent payloads that can only be
// decoded with those descriptors. A client only receives the events it
// subscribed to with clc_ListenEvents (see List.ListenMask).
package gameevent

import (
	"fmt"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/protocol"
)

// KeyType is the wire type of an event key.
type KeyType int

// Key types, in wire order.
const (
	Local  KeyType = iota // not networked; also terminates a descriptor's key list
	String                // NUL-terminated string
	Float                 // 32-bit float
	Long                  // signed 32-bit integer
	Short                 // signed 16-bit integer
	Byte                  // unsigned 8-bit integer
	Bool                  // single bit
)

func (k KeyType) String() string {
	switch k {
	case Local:
		return "local"
	case String:
		return "string"
	case Float:
		return "float"
	case Long:
		return "long"
	case Short:
		return "short"
	case Byte:
		return "byte"
	case Bool:
		return "bool"
	}
	return fmt.Sprintf("KeyType(%d)", int(k))
}

const keyTypeBits = 3

// Key is a named, typed event field.
type Key struct {
	Name string
	Type KeyType
}

// Descriptor describes an event as announced by the server.
type Descriptor struct {
	ID   int
	Name string
	Keys []Key
}

// KeyIndex returns the position of the named key, or -1.
func (d *Descriptor) KeyIndex(name string) int {
	for i, k := range d.Keys {
		if k.Name == name {
			return i
		}
	}
	return -1
}

// List is the set of event descriptors received from svc_GameEventList.
type List struct {
	descriptors []*Descriptor
	byID        map[int]*Descriptor
	byName      map[string]*Descriptor
}

// NewList builds a list from descriptors, e.g. to act as a server.
func NewList(descriptors ...*Descriptor) *List {
	l := &List{byID: make(map[int]*Descriptor), byName: make(map[string]*Descriptor)}
	for _, d := range descriptors {
		l.add(d)
	}
	return l
}

func (l *List) add(d *Descriptor) {
	if old, ok := l.byID[d.ID]; ok {
		for i, existing := range l.descriptors {
			if existing == old {
				l.descriptors = append(l.descriptors[:i], l.descriptors[i+1:]...)
				break
			}
		}
		delete(l.byName, old.Name)
	}
	l.descriptors = append(l.descriptors, d)
	l.byID[d.ID] = d
	l.byName[d.Name] = d
}

// ParseList reads numEvents descriptors from an svc_GameEventList payload.
// Names are kept in full, whereas the engine truncates them to 31 characters.
func ParseList(r *bitbuf.Reader, numEvents int) (*List, error) {
	l := NewList()
	for i := 0; i < numEvents; i++ {
		d := &Descriptor{
			ID:   int(r.ReadBits(protocol.MaxEventBits)),
			Name: r.ReadString(),
		}
		for {
			t := KeyType(r.ReadBits(keyTypeBits))
			if t == Local || r.Overflowed() {
				break
			}
			d.Keys = append(d.Keys, Key{Name: r.ReadString(), Type: t})
		}
		if err := r.Err(); err != nil {
			return l, fmt.Errorf("gameevent: reading event list: %w", err)
		}
		l.add(d)
	}
	return l, nil
}

// Write encodes the list as an svc_GameEventList payload; Len gives the
// event count that accompanies it. Local keys are not networked.
func (l *List) Write(w *bitbuf.Writer) {
	for _, d := range l.descriptors {
		w.WriteBits(uint32(d.ID), protocol.MaxEventBits)
		w.WriteString(d.Name)
		for _, k := range d.Keys {
			if k.Type == Local {
				continue
			}
			w.WriteBits(uint32(k.Type), keyTypeBits)
			w.WriteString(k.Name)
		}
		w.WriteBits(uint32(Local), keyTypeBits)
	}
}

// Len returns the number of descriptors.
func (l *List) Len() int { return len(l.descriptors) }

// ByID returns the descriptor with the given event ID, or nil.
func (l *List) ByID(id int) *Descriptor { return l.byID[id] }

// ByName returns the named descriptor, or nil.
func (l *List) ByName(name string) *Descriptor { return l.byName[name] }

// Descriptors returns all descriptors in announcement order.
func (l *List) Descriptors() []*Descriptor { return l.descriptors }

// ListenMask builds the clc_ListenEvents bit mask subscribing to the named
// events. With no names, every event in the list is selected. Unknown names
// are ignored.
func (l *List) ListenMask(names ...string) [protocol.MaxEventNumber / 32]uint32 {
	var mask [protocol.MaxEventNumber / 32]uint32
	set := func(id int) {
		if id >= 0 && id < protocol.MaxEventNumber {
			mask[id>>5] |= 1 << uint(id&31)
		}
	}
	if len(names) == 0 {
		for _, d := range l.descriptors {
			set(d.ID)
		}
		return mask
	}
	for _, n := range names {
		if d := l.byName[n]; d != nil {
			set(d.ID)
		}
	}
	return mask
}

// Value holds a decoded key value; the field used depends on the key type.
// Short and Byte values are stored in Int.
type Value struct {
	String string
	Float  float32
	Int    int32
	Bool   bool
}

// Event is a decoded game event.
type Event struct {
	Descriptor *Descriptor
	Values     []Value // parallel to Descriptor.Keys
}

// NewEvent returns an event with zero values for every key of d.
func NewEvent(d *Descriptor) *Event {
	return &Event{Descriptor: d, Values: make([]Value, len(d.Keys))}
}

// Name returns the event name.
func (e *Event) Name() string { return e.Descriptor.Name }

// Has reports whether the event has the named key.
func (e *Event) Has(key string) bool { return e.Descriptor.KeyIndex(key) >= 0 }

func (e *Event) value(key string) Value {
	if i := e.Descriptor.KeyIndex(key); i >= 0 && i < len(e.Values) {
		return e.Values[i]
	}
	return Value{}
}

// Int returns an integer key (long, short, byte or bool), or 0.
func (e *Event) Int(key string) int32 {
	i := e.Descriptor.KeyIndex(key)
	if i < 0 || i >= len(e.Values) {
		return 0
	}
	switch e.Descriptor.Keys[i].Type {
	case Bool:
		if e.Values[i].Bool {
			return 1
		}
		return 0
	case Float:
		return int32(e.Values[i].Float)
	}
	return e.Values[i].Int
}

// Float returns a float key, converting integers, or 0.
func (e *Event) Float(key string) float32 {
	i := e.Descriptor.KeyIndex(key)
	if i < 0 || i >= len(e.Values) {
		return 0
	}
	if e.Descriptor.Keys[i].Type == Float {
		return e.Values[i].Float
	}
	return float32(e.Int(key))
}

// String returns a string key, or "".
func (e *Event) String(key string) string { return e.value(key).String }

// Bool returns a boolean key; integer keys are true when non-zero.
func (e *Event) Bool(key string) bool { return e.Int(key) != 0 }

// Set stores a value for the named key; it returns false if the key is unknown.
func (e *Event) Set(key string, v Value) bool {
	i := e.Descriptor.KeyIndex(key)
	if i < 0 {
		return false
	}
	if len(e.Values) < len(e.Descriptor.Keys) {
		e.Values = append(e.Values, make([]Value, len(e.Descriptor.Keys)-len(e.Values))...)
	}
	e.Values[i] = v
	return true
}

// Decode reads an svc_GameEvent payload.
func (l *List) Decode(r *bitbuf.Reader) (*Event, error) {
	id := int(r.ReadBits(protocol.MaxEventBits))
	if err := r.Err(); err != nil {
		return nil, fmt.Errorf("gameevent: %w", err)
	}
	d := l.byID[id]
	if d == nil {
		return nil, fmt.Errorf("gameevent: unknown event id %d", id)
	}
	e := NewEvent(d)
	for i, k := range d.Keys {
		v := &e.Values[i]
		switch k.Type {
		case Local:
		case String:
			v.String = r.ReadString()
		case Float:
			v.Float = r.ReadFloat32()
		case Long:
			v.Int = r.ReadInt32()
		case Short:
			v.Int = int32(r.ReadInt16())
		case Byte:
			v.Int = int32(r.ReadUint8())
		case Bool:
			v.Bool = r.ReadBit()
		default:
			return nil, fmt.Errorf("gameevent: %s: unknown type %d for key %q", d.Name, k.Type, k.Name)
		}
	}
	if err := r.Err(); err != nil {
		return nil, fmt.Errorf("gameevent: decoding %s: %w", d.Name, err)
	}
	return e, nil
}

// Encode writes e as an svc_GameEvent payload.
func (l *List) Encode(w *bitbuf.Writer, e *Event) error {
	d := e.Descriptor
	if d == nil || l.byID[d.ID] == nil {
		return fmt.Errorf("gameevent: event is not in the list")
	}
	w.WriteBits(uint32(d.ID), protocol.MaxEventBits)
	for i, k := range d.Keys {
		var v Value
		if i < len(e.Values) {
			v = e.Values[i]
		}
		switch k.Type {
		case Local:
		case String:
			w.WriteString(v.String)
		case Float:
			w.WriteFloat32(v.Float)
		case Long:
			w.WriteInt32(v.Int)
		case Short:
			w.WriteInt16(int16(v.Int))
		case Byte:
			w.WriteUint8(uint8(v.Int))
		case Bool:
			w.WriteBit(v.Bool)
		default:
			return fmt.Errorf("gameevent: %s: unknown type %d for key %q", d.Name, k.Type, k.Name)
		}
	}
	return w.Err()
}
