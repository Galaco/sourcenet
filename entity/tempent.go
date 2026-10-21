package entity

import (
	"errors"
	"fmt"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/datatable"
)

// TempEntity is one event decoded from svc_TempEntities.
type TempEntity struct {
	ClassID int
	Class   *datatable.ServerClass
	// FireDelay is the server-requested delay in seconds; the engine fires
	// the event at client time + interpolation amount + FireDelay.
	FireDelay float32
	// Reliable is set for events sent through the reliable stream (a
	// message with zero entries carries exactly one reliable event).
	Reliable bool
	Props    []datatable.Value
}

// DecodeTempEntities decodes the payload of svc_TempEntities
// (CClientState::ProcessTempEntities). Each event is either encoded against
// zeros (with an explicit class) or as a delta against the previous event.
func (s *State) DecodeTempEntities(numEntries int, data *bitbuf.Reader) ([]TempEntity, error) {
	if s.tables == nil || !s.tables.Linked() {
		return nil, datatable.ErrUnlinked
	}
	reliable := numEntries == 0
	if reliable {
		numEntries = 1
	}
	out := make([]TempEntity, 0, numEntries)
	for i := 0; i < numEntries; i++ {
		ev := TempEntity{Reliable: reliable}
		if data.ReadBit() {
			ev.FireDelay = float32(data.ReadSBits(8)) / 100
		}
		if data.ReadBit() {
			ev.ClassID = int(data.ReadBits(s.classIDBits())) - 1
			if ev.Class = s.tables.Class(ev.ClassID); ev.Class == nil {
				return out, fmt.Errorf("entity: temp entity %d has invalid class %d", i, ev.ClassID)
			}
			ev.Props = make([]datatable.Value, len(ev.Class.Props))
		} else {
			if len(out) == 0 {
				return out, errors.New("entity: first temp entity is a delta")
			}
			prev := out[len(out)-1]
			ev.ClassID, ev.Class = prev.ClassID, prev.Class
			ev.Props = make([]datatable.Value, len(prev.Props))
			for k, v := range prev.Props {
				ev.Props[k] = v.Clone()
			}
		}
		if _, err := datatable.DecodeProps(data, ev.Class, ev.Props, nil); err != nil {
			return out, fmt.Errorf("entity: temp entity %d (%s): %w", i, ev.Class.Name, err)
		}
		out = append(out, ev)
	}
	return out, nil
}
