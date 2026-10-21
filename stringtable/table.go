// Package stringtable implements the engine's network string tables: named,
// indexed lists of strings with optional binary user data, created by
// svc_CreateStringTable and updated by svc_UpdateStringTable.
//
// Tables carry precache lists (models, sounds, decals), downloadables, the
// userinfo table describing connected players and the instance baselines
// used to decode entities.
package stringtable

import (
	"fmt"
	"strings"

	"github.com/galaco/sourcenet/bitbuf"
)

const (
	// maxUserDataBits is the width of a variable-size user data length.
	maxUserDataBits = 14
	// substringBits is the width of the history index and prefix length.
	substringBits = 5
	// historySize is the number of recent strings available as prefixes.
	historySize = 32
	// maxEntryLength mirrors the engine's 1024 byte entry buffers.
	maxEntryLength = 1023
)

// Entry is a single string table item.
type Entry struct {
	String   string
	UserData []byte
}

// Table is a network string table.
type Table struct {
	ID                int
	Name              string
	MaxEntries        int
	UserDataFixedSize bool
	UserDataSize      int // fixed user data size in bytes
	UserDataSizeBits  int // fixed user data size in bits
	IsFilenames       bool
	Entries           []Entry

	lookup  map[string]int
	indexed int // number of entries reflected in lookup
}

// NewTable returns an empty table.
func NewTable(name string, maxEntries int) *Table {
	return &Table{Name: name, MaxEntries: maxEntries}
}

// EntryBits returns the number of bits used to encode an entry index
// (floor(log2(MaxEntries))).
func (t *Table) EntryBits() int {
	bits := 0
	for n := t.MaxEntries; n > 1; n >>= 1 {
		bits++
	}
	return bits
}

// Len returns the number of entries.
func (t *Table) Len() int { return len(t.Entries) }

func (t *Table) key(s string) string {
	s = strings.ToLower(s)
	if t.IsFilenames {
		s = strings.ReplaceAll(s, "\\", "/")
	}
	return s
}

// Find returns the index of s, or -1 when it is absent. Like the engine's
// dictionary, the lookup is case-insensitive (and slash-insensitive for
// filename tables).
func (t *Table) Find(s string) int {
	if t.lookup == nil || t.indexed != len(t.Entries) {
		t.rebuildLookup()
	}
	if i, ok := t.lookup[t.key(s)]; ok {
		return i
	}
	return -1
}

func (t *Table) rebuildLookup() {
	t.lookup = make(map[string]int, len(t.Entries))
	for i, e := range t.Entries {
		k := t.key(e.String)
		if _, ok := t.lookup[k]; !ok {
			t.lookup[k] = i
		}
	}
	t.indexed = len(t.Entries)
}

// Add appends a string or, if it is already present, replaces its user data.
// It returns the entry index, or -1 if the table is full.
func (t *Table) Add(s string, userData []byte) int {
	if i := t.Find(s); i >= 0 {
		t.Entries[i].UserData = cloneBytes(userData)
		return i
	}
	if len(t.Entries) >= t.MaxEntries {
		return -1
	}
	t.Entries = append(t.Entries, Entry{String: s, UserData: cloneBytes(userData)})
	i := len(t.Entries) - 1
	t.lookup[t.key(s)] = i
	t.indexed = len(t.Entries)
	return i
}

// ParseUpdate applies numEntries entry updates read from r and returns the
// indices that were added or modified.
func (t *Table) ParseUpdate(r *bitbuf.Reader, numEntries int) ([]int, error) {
	var history []string
	changed := make([]int, 0, numEntries)
	last := -1
	entryBits := t.EntryBits()

	for n := 0; n < numEntries; n++ {
		index := last + 1
		if !r.ReadBit() {
			index = int(r.ReadBits(entryBits))
		}
		last = index
		if index < 0 || index >= t.MaxEntries {
			return changed, fmt.Errorf("stringtable %s: bogus string index %d", t.Name, index)
		}

		var str string
		hasString := r.ReadBit()
		if hasString {
			if r.ReadBit() {
				hi := int(r.ReadBits(substringBits))
				count := int(r.ReadBits(substringBits))
				if hi >= len(history) {
					return changed, fmt.Errorf("stringtable %s: bogus substring index %d", t.Name, hi)
				}
				prefix := history[hi]
				if count < len(prefix) {
					prefix = prefix[:count]
				}
				str = truncate(prefix + truncate(r.ReadString()))
			} else {
				str = truncate(r.ReadString())
			}
		}

		var userData []byte
		if r.ReadBit() {
			if t.UserDataFixedSize {
				userData = make([]byte, t.UserDataSize)
				copy(userData, r.ReadBitsToBytes(t.UserDataSizeBits))
			} else {
				userData = r.ReadBytes(int(r.ReadBits(maxUserDataBits)))
			}
		}
		if err := r.Err(); err != nil {
			return changed, fmt.Errorf("stringtable %s: %w", t.Name, err)
		}

		if index < len(t.Entries) {
			// Existing entry: only the user data can change.
			t.Entries[index].UserData = userData
			str = t.Entries[index].String
			changed = append(changed, index)
		} else {
			// New entries are appended; the engine treats index as a hint.
			if i := t.Add(str, userData); i >= 0 {
				changed = append(changed, i)
			}
		}

		if len(history) == historySize {
			history = history[1:]
		}
		history = append(history, historyString(str))
	}
	return changed, nil
}

// WriteUpdate encodes the listed entries, including their strings, in the
// format read by ParseUpdate. Indices must be ascending.
func (t *Table) WriteUpdate(w *bitbuf.Writer, indices []int) {
	t.writeUpdate(w, indices, true)
}

// WriteUserDataUpdate encodes the listed entries without their strings. The
// receiver must already know every listed entry.
func (t *Table) WriteUserDataUpdate(w *bitbuf.Writer, indices []int) {
	t.writeUpdate(w, indices, false)
}

func (t *Table) writeUpdate(w *bitbuf.Writer, indices []int, withStrings bool) {
	var history []string
	last := -1
	entryBits := t.EntryBits()
	for _, i := range indices {
		e := t.Entries[i]
		if i == last+1 {
			w.WriteBit(true)
		} else {
			w.WriteBit(false)
			w.WriteBits(uint32(i), entryBits)
		}

		w.WriteBit(withStrings)
		if withStrings {
			best, size := bestPreviousString(history, e.String)
			if best >= 0 {
				w.WriteBit(true)
				w.WriteBits(uint32(best), substringBits)
				w.WriteBits(uint32(size), substringBits)
				w.WriteString(e.String[size:])
			} else {
				w.WriteBit(false)
				w.WriteString(e.String)
			}
		}

		if len(e.UserData) > 0 {
			w.WriteBit(true)
			if t.UserDataFixedSize {
				w.WriteBitsFromBytes(e.UserData, t.UserDataSizeBits)
			} else {
				w.WriteBits(uint32(len(e.UserData)), maxUserDataBits)
				w.WriteBytes(e.UserData)
			}
		} else {
			w.WriteBit(false)
		}

		if len(history) == historySize {
			history = history[1:]
		}
		history = append(history, historyString(e.String))
		last = i
	}
}

// bestPreviousString finds the history entry sharing the longest prefix (at
// least 3 characters) with s.
func bestPreviousString(history []string, s string) (index, size int) {
	index = -1
	for i, prev := range history {
		n := 0
		for n < len(prev) && n < len(s) && prev[n] == s[n] && n < historySize-1 {
			n++
		}
		if n >= 3 && n > size {
			size = n
			index = i
		}
	}
	return index, size
}

// historyString truncates s to the 31 characters kept in history slots.
func historyString(s string) string {
	if len(s) > historySize-1 {
		return s[:historySize-1]
	}
	return s
}

func truncate(s string) string {
	if len(s) > maxEntryLength {
		return s[:maxEntryLength]
	}
	return s
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte(nil), b...)
}
