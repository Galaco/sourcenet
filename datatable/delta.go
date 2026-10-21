package datatable

import (
	"fmt"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/protocol"
)

// ReadPropIndex reads the next property index of a delta encoded property
// list (CDeltaBitsReader::ReadNextPropIndex). last is the previous index, or
// -1 before the first. ok is false at the end of the list.
func ReadPropIndex(r *bitbuf.Reader, last int) (next int, ok bool) {
	if !r.ReadBit() {
		return 0, false
	}
	next = last + 1 + int(r.ReadUBitVar())
	if r.Overflowed() {
		return 0, false
	}
	return next, true
}

// WritePropIndex writes a property index following last (-1 before the
// first). Indices must be strictly increasing.
func WritePropIndex(w *bitbuf.Writer, last, next int) {
	w.WriteBit(true)
	w.WriteUBitVar(uint32(next - last - 1))
}

// WritePropListEnd terminates a delta encoded property list.
func WritePropListEnd(w *bitbuf.Writer) {
	w.WriteBit(false)
}

// DecodeProps reads a delta encoded property list for class c, storing each
// value into dst (which must have len(c.Props) elements). The flat indices of
// the decoded props are appended to changed and returned.
func DecodeProps(r *bitbuf.Reader, c *ServerClass, dst []Value, changed []int) ([]int, error) {
	if len(dst) < len(c.Props) {
		return changed, fmt.Errorf("datatable: destination holds %d props, class %q has %d", len(dst), c.Name, len(c.Props))
	}
	last := -1
	for {
		idx, ok := ReadPropIndex(r, last)
		if !ok {
			break
		}
		if idx >= len(c.Props) || idx >= protocol.MaxDatatableProps {
			return changed, fmt.Errorf("datatable: class %q prop index %d out of range (%d props)", c.Name, idx, len(c.Props))
		}
		decodeInto(r, c.Props[idx].Prop, &dst[idx])
		changed = append(changed, idx)
		last = idx
	}
	if err := r.Err(); err != nil {
		return changed, fmt.Errorf("datatable: decoding %q props: %w", c.Name, err)
	}
	return changed, nil
}

// EncodeProps writes the props at the given (strictly increasing) flat
// indices of src as a delta encoded property list, including the terminator.
func EncodeProps(w *bitbuf.Writer, c *ServerClass, src []Value, indices []int) {
	last := -1
	for _, idx := range indices {
		WritePropIndex(w, last, idx)
		EncodeProp(w, c.Props[idx].Prop, src[idx])
		last = idx
	}
	WritePropListEnd(w)
}
