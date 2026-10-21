package demo

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/datatable"
)

// ErrNoDataTables is returned when a demo has no datatables command.
var ErrNoDataTables = errors.New("demo: no datatables in demo")

// ReadDataTables scans a demo for its datatables command and parses the send
// tables and server classes it contains. The result is not linked. Any demo
// recorded on a game version carries that version's tables, which is how a
// game.Profile obtains the tables servers expect clients to know.
func ReadDataTables(r io.Reader) (*datatable.Tables, error) {
	dr, err := NewReader(r)
	if err != nil {
		return nil, err
	}
	if p := dr.Header().Protocol; p != Protocol {
		return nil, fmt.Errorf("demo: datatables of demo protocol %d are not supported", p)
	}
	for {
		f, err := dr.Next()
		if errors.Is(err, io.EOF) {
			return nil, ErrNoDataTables
		}
		if err != nil {
			return nil, err
		}
		if f.Command == DataTables {
			return datatable.ReadDataTables(bitbuf.NewReader(f.Data))
		}
	}
}

// LoadDataTables reads the data tables from the demo file at path.
func LoadDataTables(path string) (*datatable.Tables, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ReadDataTables(f)
}
