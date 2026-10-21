package game

import (
	"os"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/datatable"
	"github.com/galaco/sourcenet/demo"
)

// DataTablesFromDemo returns a Profile.DataTables loader that reads the send
// tables recorded in a demo file. The demo must come from the same game
// version as the servers being joined.
func DataTablesFromDemo(path string) func() (*datatable.Tables, error) {
	return func() (*datatable.Tables, error) { return demo.LoadDataTables(path) }
}

// DataTablesFromFile returns a Profile.DataTables loader for a raw data table
// dump, as written by datatable.Tables.WriteDataTables (the payload of a
// demo's datatables command).
func DataTablesFromFile(path string) func() (*datatable.Tables, error) {
	return func() (*datatable.Tables, error) {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return datatable.ReadDataTables(bitbuf.NewReader(b))
	}
}
