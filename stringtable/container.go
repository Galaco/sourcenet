package stringtable

import (
	"fmt"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/compression"
	"github.com/galaco/sourcenet/protocol"
)

// CreateParams describes a table announced by svc_CreateStringTable.
type CreateParams struct {
	Name              string
	MaxEntries        int
	NumEntries        int
	UserDataFixedSize bool
	UserDataSize      int
	UserDataSizeBits  int
	IsFilenames       bool
	DataCompressed    bool
}

// Container holds the string tables of a connection. Table IDs are assigned
// in creation order, matching the IDs used by svc_UpdateStringTable.
type Container struct {
	tables []*Table
	byName map[string]*Table
}

// NewContainer returns an empty container.
func NewContainer() *Container {
	return &Container{byName: make(map[string]*Table)}
}

// Create creates a table and applies its initial entries from data, the
// svc_CreateStringTable payload. The payload must be consumed exactly.
func (c *Container) Create(p CreateParams, data *bitbuf.Reader) (*Table, error) {
	if len(c.tables) >= protocol.MaxTables {
		return nil, fmt.Errorf("stringtable: too many tables creating %q", p.Name)
	}
	if _, ok := c.byName[p.Name]; ok {
		return nil, fmt.Errorf("stringtable: table %q already exists", p.Name)
	}
	t := &Table{
		ID:                len(c.tables),
		Name:              p.Name,
		MaxEntries:        p.MaxEntries,
		UserDataFixedSize: p.UserDataSize != 0,
		UserDataSize:      p.UserDataSize,
		UserDataSizeBits:  p.UserDataSizeBits,
		IsFilenames:       p.IsFilenames,
	}
	c.tables = append(c.tables, t)
	c.byName[p.Name] = t

	if data == nil {
		return t, nil
	}
	if p.DataCompressed {
		uncompressedSize := int(data.ReadUint32())
		compressedSize := int(data.ReadUint32())
		if data.Err() != nil || compressedSize < 0 || compressedSize > data.BytesLeft() {
			return t, fmt.Errorf("stringtable %s: malformed compressed payload", p.Name)
		}
		compressed := data.ReadBytes(compressedSize)
		raw, err := compression.Decompress(compressed)
		if err != nil {
			return t, fmt.Errorf("stringtable %s: %w", p.Name, err)
		}
		if len(raw) != uncompressedSize {
			return t, fmt.Errorf("stringtable %s: decompressed %d bytes, expected %d", p.Name, len(raw), uncompressedSize)
		}
		if _, err := t.ParseUpdate(bitbuf.NewReader(raw), p.NumEntries); err != nil {
			return t, err
		}
	} else if _, err := t.ParseUpdate(data, p.NumEntries); err != nil {
		return t, err
	}
	if n := data.BitsLeft(); n != 0 {
		return t, fmt.Errorf("stringtable %s: %d unread payload bits", p.Name, n)
	}
	return t, nil
}

// Update applies an svc_UpdateStringTable payload to the table with the
// given ID and returns the changed entry indices. The payload must be
// consumed exactly.
func (c *Container) Update(tableID, changedEntries int, data *bitbuf.Reader) (*Table, []int, error) {
	t := c.ByID(tableID)
	if t == nil {
		return nil, nil, fmt.Errorf("stringtable: update for unknown table %d", tableID)
	}
	changed, err := t.ParseUpdate(data, changedEntries)
	if err != nil {
		return t, changed, err
	}
	if n := data.BitsLeft(); n != 0 {
		return t, changed, fmt.Errorf("stringtable %s: %d unread payload bits", t.Name, n)
	}
	return t, changed, nil
}

// ByID returns the table with the given ID, or nil.
func (c *Container) ByID(id int) *Table {
	if id < 0 || id >= len(c.tables) {
		return nil
	}
	return c.tables[id]
}

// ByName returns the named table, or nil.
func (c *Container) ByName(name string) *Table {
	return c.byName[name]
}

// Tables returns all tables in ID order.
func (c *Container) Tables() []*Table {
	return c.tables
}

// Reset removes all tables, as happens on level change.
func (c *Container) Reset() {
	c.tables = nil
	c.byName = make(map[string]*Table)
}
