// Package usermessage decodes game-specific user messages (svc_UserMessage).
//
// User messages are identified on the wire only by an index. The index is
// the position at which the game registered the message on startup, so the
// mapping differs between games (and between versions of a game): it is not
// sent by the server. A Registry holds one game's ordered name list together
// with optional decoders for message payloads.
package usermessage

import (
	"fmt"

	"github.com/galaco/sourcenet/bitbuf"
)

// Decoder decodes a user message payload into a value.
type Decoder func(r *bitbuf.Reader) (any, error)

// Registry maps user message indices to names and decoders.
type Registry struct {
	names    []string
	ids      map[string]int
	decoders map[string]Decoder
}

// NewRegistry returns a registry for names in registration order; the
// position of a name is its message index.
func NewRegistry(names []string) *Registry {
	r := &Registry{
		names:    append([]string(nil), names...),
		ids:      make(map[string]int, len(names)),
		decoders: make(map[string]Decoder),
	}
	for i, n := range r.names {
		if _, dup := r.ids[n]; !dup {
			r.ids[n] = i
		}
	}
	return r
}

// Name returns the name registered at index id.
func (r *Registry) Name(id int) (string, bool) {
	if id < 0 || id >= len(r.names) {
		return "", false
	}
	return r.names[id], true
}

// ID returns the index of the named message.
func (r *Registry) ID(name string) (int, bool) {
	id, ok := r.ids[name]
	return id, ok
}

// Len returns the number of registered messages.
func (r *Registry) Len() int { return len(r.names) }

// Names returns the registered names in index order.
func (r *Registry) Names() []string { return append([]string(nil), r.names...) }

// Register appends a message name, as later registrations in a game do, and
// returns its index. Registering an existing name returns its index.
func (r *Registry) Register(name string) int {
	if id, ok := r.ids[name]; ok {
		return id
	}
	r.names = append(r.names, name)
	r.ids[name] = len(r.names) - 1
	return len(r.names) - 1
}

// RegisterDecoder sets the payload decoder for a message name, replacing any
// existing one. A nil decoder removes it.
func (r *Registry) RegisterDecoder(name string, d Decoder) {
	if d == nil {
		delete(r.decoders, name)
		return
	}
	r.decoders[name] = d
}

// Decode resolves message index id and decodes data with the registered
// decoder. msg is nil when no decoder is registered for the message.
func (r *Registry) Decode(id int, data *bitbuf.Reader) (name string, msg any, err error) {
	name, ok := r.Name(id)
	if !ok {
		return "", nil, fmt.Errorf("usermessage: unknown message index %d", id)
	}
	d := r.decoders[name]
	if d == nil {
		return name, nil, nil
	}
	msg, err = d(data)
	if err == nil {
		err = data.Err()
	}
	if err != nil {
		return name, msg, fmt.Errorf("usermessage: decoding %s: %w", name, err)
	}
	return name, msg, nil
}
