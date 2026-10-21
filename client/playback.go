package client

import (
	"fmt"
	"io"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/datatable"
	"github.com/galaco/sourcenet/demo"
	"github.com/galaco/sourcenet/protocol"
	"github.com/galaco/sourcenet/stringtable"
)

// Playback replays a demo file through a Client. The client's state (server
// info, string tables, game events, entities) and its events evolve exactly
// as they would on a live connection, which lets a game engine render demos
// with the same code path it uses online.
type Playback struct {
	c      *Client
	r      *demo.Reader
	header demo.Header
}

// NewPlayback reads the demo header from r and prepares a client in
// playback mode. Config.Game selects user message names; its data tables are
// only used when the demo does not carry its own.
func NewPlayback(cfg Config, r io.Reader) (*Playback, error) {
	dr, err := demo.NewReader(r)
	if err != nil {
		return nil, err
	}
	c := New(cfg)
	h := dr.Header()
	c.playback = true
	c.msgCtx.ProtocolVersion = int(h.NetworkProtocol)
	c.state = StateConnected
	c.signon = protocol.SignonConnected
	c.deltaTick = -1
	c.serverCount = -1
	c.stringTables = stringtable.NewContainer()
	c.replicated = make(map[string]string)
	return &Playback{c: c, r: dr, header: h}, nil
}

// Header returns the demo header.
func (p *Playback) Header() demo.Header { return p.header }

// Client returns the client holding the replayed state. Read its events with
// PollEvents after each Step.
func (p *Playback) Client() *Client { return p.c }

// Step processes the next demo frame and returns it. It returns io.EOF after
// the last frame.
func (p *Playback) Step() (*demo.Frame, error) {
	f, err := p.r.Next()
	if err != nil {
		return nil, err
	}
	c := p.c
	switch f.Command {
	case demo.Signon, demo.Packet:
		c.processMessages(bitbuf.NewReader(f.Data))
	case demo.DataTables:
		t, err := datatable.ReadDataTables(bitbuf.NewReader(f.Data))
		if err == nil {
			err = t.Link()
		}
		if err != nil {
			return f, fmt.Errorf("client: demo data tables: %w", err)
		}
		c.demoTables = t
		if c.entities == nil {
			c.tables = t
			c.entities = c.newEntityState(t)
		}
	case demo.StringTables:
		if err := p.applyStringTables(f.Data); err != nil {
			return f, err
		}
	case demo.Stop:
		c.state = StateDisconnected
		c.emit(EventDisconnected{Reason: "Demo finished"})
	}
	return f, nil
}

// Run replays every remaining frame.
func (p *Playback) Run() error {
	for {
		if _, err := p.Step(); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// applyStringTables applies a dem_stringtables snapshot, which replaces the
// contents of tables created earlier in the demo
// (CNetworkStringTableContainer::ReadStringTables).
func (p *Playback) applyStringTables(data []byte) error {
	r := bitbuf.NewReader(data)
	n := int(r.ReadUint8())
	for i := 0; i < n; i++ {
		name := r.ReadString()
		t := p.c.stringTables.ByName(name)
		var entries []stringtable.Entry
		count := int(r.ReadUint16())
		for j := 0; j < count; j++ {
			e := stringtable.Entry{String: r.ReadString()}
			if r.ReadBit() {
				e.UserData = r.ReadBytes(int(r.ReadUint16()))
			}
			entries = append(entries, e)
		}
		// Client-side entries are not networked state; skip them.
		if r.ReadBit() {
			cs := int(r.ReadUint16())
			for j := 0; j < cs; j++ {
				r.ReadString()
				if r.ReadBit() {
					r.ReadBytes(int(r.ReadUint16()))
				}
			}
		}
		if r.Overflowed() {
			return fmt.Errorf("client: demo string tables truncated")
		}
		if t == nil {
			continue
		}
		t.Entries = nil
		changed := make([]int, 0, len(entries))
		for _, e := range entries {
			if j := t.Add(e.String, e.UserData); j >= 0 {
				changed = append(changed, j)
			}
		}
		p.c.stringTableChanged(t, false, changed)
	}
	return nil
}
