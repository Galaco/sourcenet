package stringtable

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/compression"
)

func newTable(name string, max int) *Table {
	return &Table{Name: name, MaxEntries: max}
}

func populate(t *Table, entries ...Entry) {
	for _, e := range entries {
		t.Add(e.String, e.UserData)
	}
}

func allIndices(t *Table) []int {
	idx := make([]int, t.Len())
	for i := range idx {
		idx[i] = i
	}
	return idx
}

func TestEntryBits(t *testing.T) {
	for max, want := range map[int]int{0: 0, 1: 0, 2: 1, 64: 6, 1024: 10, 1000: 9, 65536: 16} {
		if got := (&Table{MaxEntries: max}).EntryBits(); got != want {
			t.Errorf("EntryBits(%d) = %d, want %d", max, got, want)
		}
	}
}

func TestRoundTripWithHistory(t *testing.T) {
	src := newTable("modelprecache", 1024)
	populate(src,
		Entry{String: ""},
		Entry{String: "maps/ctf_2fort.bsp"},
		Entry{String: "models/player/scout.mdl", UserData: []byte{1, 2, 3}},
		Entry{String: "models/player/soldier.mdl"},
		Entry{String: "models/player/pyro.mdl"},
		Entry{String: "sound/ui/hint.wav"},
		Entry{String: "models/player/an_extremely_long_model_name_exceeding_history.mdl"},
		Entry{String: "models/player/an_extremely_long_model_name_exceeding_history2.mdl"},
	)

	w := bitbuf.NewWriter(0)
	src.WriteUpdate(w, allIndices(src))

	// The prefix compression must make the update smaller than raw strings.
	raw := 0
	for _, e := range src.Entries {
		raw += (len(e.String) + 1) * 8
	}
	if w.BitsWritten() >= raw {
		t.Errorf("update is %d bits, raw strings alone are %d", w.BitsWritten(), raw)
	}

	dst := newTable("modelprecache", 1024)
	r := w.Reader()
	changed, err := dst.ParseUpdate(r, src.Len())
	if err != nil {
		t.Fatal(err)
	}
	if r.BitsLeft() != 0 {
		t.Fatalf("%d bits left", r.BitsLeft())
	}
	if len(changed) != src.Len() {
		t.Fatalf("changed %v", changed)
	}
	for i, e := range src.Entries {
		got := dst.Entries[i]
		if got.String != e.String || !bytes.Equal(got.UserData, e.UserData) {
			t.Errorf("entry %d = %+v, want %+v", i, got, e)
		}
	}
}

func TestHistoryIsLimitedTo32Entries(t *testing.T) {
	src := newTable("generic", 256)
	for i := 0; i < 70; i++ {
		src.Add(fmt.Sprintf("materials/generic/item_%03d.vmt", i), nil)
	}
	w := bitbuf.NewWriter(0)
	src.WriteUpdate(w, allIndices(src))
	dst := newTable("generic", 256)
	if _, err := dst.ParseUpdate(w.Reader(), src.Len()); err != nil {
		t.Fatal(err)
	}
	for i := range src.Entries {
		if dst.Entries[i].String != src.Entries[i].String {
			t.Fatalf("entry %d = %q", i, dst.Entries[i].String)
		}
	}
}

// writeEntry hand-encodes one entry in the wire format.
type rawEntry struct {
	index     int // -1 means "previous + 1"
	str       *string
	substr    [2]int // history index, prefix length; used when substrOK
	substrOK  bool
	userData  []byte
	fixedBits int // >0 for fixed-size user data
	entryBits int
}

func (e rawEntry) write(w *bitbuf.Writer) {
	if e.index < 0 {
		w.WriteBit(true)
	} else {
		w.WriteBit(false)
		w.WriteBits(uint32(e.index), e.entryBits)
	}
	w.WriteBit(e.str != nil)
	if e.str != nil {
		w.WriteBit(e.substrOK)
		if e.substrOK {
			w.WriteBits(uint32(e.substr[0]), 5)
			w.WriteBits(uint32(e.substr[1]), 5)
		}
		w.WriteString(*e.str)
	}
	w.WriteBit(e.userData != nil)
	if e.userData != nil {
		if e.fixedBits > 0 {
			w.WriteBitsFromBytes(e.userData, e.fixedBits)
		} else {
			w.WriteBits(uint32(len(e.userData)), 14)
			w.WriteBytes(e.userData)
		}
	}
}

func strp(s string) *string { return &s }

func TestParseUpdateSemantics(t *testing.T) {
	tbl := newTable("downloadables", 16)
	populate(tbl, Entry{String: "a", UserData: []byte{9}}, Entry{String: "b"})

	w := bitbuf.NewWriter(0)
	// Existing entry 0: string ignored, missing user data clears it.
	rawEntry{index: -1, str: strp("ignored"), entryBits: 4}.write(w)
	// New entry at explicit index 2 with a prefix (3 chars of history[0] = "a"
	// which is shorter, so the whole string is used) and user data.
	rawEntry{index: 2, str: strp("xyz"), substr: [2]int{0, 3}, substrOK: true, userData: []byte{7, 7}, entryBits: 4}.write(w)
	// New entry using 2 chars of history[1] ("axyz").
	rawEntry{index: -1, str: strp("Q"), substr: [2]int{1, 2}, substrOK: true, entryBits: 4}.write(w)
	// Existing entry 1 gains user data without a string.
	rawEntry{index: 1, userData: []byte{5}, entryBits: 4}.write(w)

	changed, err := tbl.ParseUpdate(w.Reader(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(changed) != "[0 2 3 1]" {
		t.Errorf("changed = %v", changed)
	}
	want := []Entry{{"a", nil}, {"b", []byte{5}}, {"axyz", []byte{7, 7}}, {"axQ", nil}}
	if len(tbl.Entries) != len(want) {
		t.Fatalf("entries %+v", tbl.Entries)
	}
	for i, e := range want {
		got := tbl.Entries[i]
		if got.String != e.String || !bytes.Equal(got.UserData, e.UserData) {
			t.Errorf("entry %d = %+v, want %+v", i, got, e)
		}
	}
	if tbl.Find("AXYZ") != 2 || tbl.Find("missing") != -1 {
		t.Error("case-insensitive Find")
	}
}

func TestParseUpdateNewEntryWithKnownStringReusesIndex(t *testing.T) {
	tbl := newTable("t", 8)
	populate(tbl, Entry{String: "Dup"})
	w := bitbuf.NewWriter(0)
	rawEntry{index: 1, str: strp("dup"), userData: []byte{1}, entryBits: 3}.write(w)
	changed, err := tbl.ParseUpdate(w.Reader(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if tbl.Len() != 1 || fmt.Sprint(changed) != "[0]" || !bytes.Equal(tbl.Entries[0].UserData, []byte{1}) {
		t.Fatalf("entries %+v changed %v", tbl.Entries, changed)
	}
}

func TestParseUpdateErrors(t *testing.T) {
	tbl := newTable("t", 4)
	w := bitbuf.NewWriter(0)
	// Index 3 is valid for 2 entry bits, but a 3-bit index beyond max isn't
	// representable; use a table whose max is not a power of two instead.
	tbl.MaxEntries = 3
	rawEntry{index: 1, str: strp("x"), entryBits: 1}.write(w)
	rawEntry{index: -1, str: strp("y"), entryBits: 1}.write(w)
	rawEntry{index: -1, str: strp("z"), entryBits: 1}.write(w)
	if _, err := tbl.ParseUpdate(w.Reader(), 3); err == nil || !strings.Contains(err.Error(), "bogus string index") {
		t.Errorf("expected bogus index error, got %v", err)
	}

	tbl = newTable("t", 4)
	w = bitbuf.NewWriter(0)
	rawEntry{index: -1, str: strp("x"), substr: [2]int{0, 3}, substrOK: true, entryBits: 2}.write(w)
	if _, err := tbl.ParseUpdate(w.Reader(), 1); err == nil || !strings.Contains(err.Error(), "substring") {
		t.Errorf("expected bogus substring error, got %v", err)
	}

	tbl = newTable("t", 4)
	if _, err := tbl.ParseUpdate(bitbuf.NewReader([]byte{0x01}), 3); err == nil {
		t.Error("expected overflow error")
	}
}

func TestFixedSizeUserData(t *testing.T) {
	src := &Table{Name: "lightstyles", MaxEntries: 64, UserDataFixedSize: true, UserDataSize: 2, UserDataSizeBits: 12}
	populate(src, Entry{String: "0", UserData: []byte{0xAB, 0x0C}}, Entry{String: "1"}, Entry{String: "2", UserData: []byte{0xFF, 0x0F}})
	w := bitbuf.NewWriter(0)
	src.WriteUpdate(w, []int{0, 2})
	// 3 index bits... entry 0: 1+1+1+8(str)+8(nul) ... just verify round trip.
	dst := &Table{Name: "lightstyles", MaxEntries: 64, UserDataFixedSize: true, UserDataSize: 2, UserDataSizeBits: 12}
	populate(dst, Entry{String: "0"}, Entry{String: "1"}, Entry{String: "2"})
	r := w.Reader()
	changed, err := dst.ParseUpdate(r, 2)
	if err != nil || r.BitsLeft() != 0 {
		t.Fatalf("err %v, %d bits left", err, r.BitsLeft())
	}
	if fmt.Sprint(changed) != "[0 2]" {
		t.Errorf("changed %v", changed)
	}
	if !bytes.Equal(dst.Entries[0].UserData, []byte{0xAB, 0x0C}) || !bytes.Equal(dst.Entries[2].UserData, []byte{0xFF, 0x0F}) {
		t.Errorf("user data %+v", dst.Entries)
	}
}

func TestUserDataOnlyUpdate(t *testing.T) {
	src := newTable("userinfo", 64)
	populate(src, Entry{String: "0"}, Entry{String: "1"}, Entry{String: "2"})
	dst := newTable("userinfo", 64)
	populate(dst, src.Entries...)
	src.Entries[2].UserData = []byte("hello")
	w := bitbuf.NewWriter(0)
	src.WriteUserDataUpdate(w, []int{2})
	if _, err := dst.ParseUpdate(w.Reader(), 1); err != nil {
		t.Fatal(err)
	}
	if string(dst.Entries[2].UserData) != "hello" {
		t.Errorf("user data %q", dst.Entries[2].UserData)
	}
}

func TestAddRespectsMaxEntries(t *testing.T) {
	tbl := newTable("t", 2)
	if tbl.Add("a", nil) != 0 || tbl.Add("b", nil) != 1 {
		t.Fatal("add")
	}
	if tbl.Add("c", nil) != -1 || tbl.Len() != 2 {
		t.Fatal("full table must reject additions")
	}
	if tbl.Add("A", []byte{1}) != 0 || !bytes.Equal(tbl.Entries[0].UserData, []byte{1}) {
		t.Fatal("re-adding an existing string updates its user data")
	}
}

func buildPayload(entries []Entry, max int) ([]byte, int) {
	src := newTable("x", max)
	populate(src, entries...)
	w := bitbuf.NewWriter(0)
	src.WriteUpdate(w, allIndices(src))
	return w.Bytes(), w.BitsWritten()
}

func TestContainerCreateAndUpdate(t *testing.T) {
	entries := []Entry{{String: "models/a.mdl"}, {String: "models/b.mdl"}, {String: "models/c.mdl", UserData: []byte{1}}}
	data, nbits := buildPayload(entries, 1024)

	c := NewContainer()
	tbl, err := c.Create(CreateParams{Name: "modelprecache", MaxEntries: 1024, NumEntries: 3}, bitbuf.NewReaderBits(data, nbits))
	if err != nil {
		t.Fatal(err)
	}
	if tbl.ID != 0 || tbl.Len() != 3 || tbl.Entries[2].String != "models/c.mdl" {
		t.Fatalf("table %+v", tbl)
	}
	second, err := c.Create(CreateParams{Name: "soundprecache", MaxEntries: 1024}, nil)
	if err != nil || second.ID != 1 {
		t.Fatalf("second table %+v %v", second, err)
	}
	if _, err := c.Create(CreateParams{Name: "modelprecache", MaxEntries: 8}, nil); err == nil {
		t.Error("duplicate table name accepted")
	}
	if c.ByName("soundprecache") != second || c.ByID(0) != tbl || c.ByID(5) != nil || len(c.Tables()) != 2 {
		t.Error("lookups")
	}

	// Trailing bits are rejected.
	if _, err := c.Create(CreateParams{Name: "extra", MaxEntries: 1024, NumEntries: 3}, bitbuf.NewReaderBits(append(data, 0), nbits+8)); err == nil {
		t.Error("expected unread bits error")
	}

	upd := newTable("modelprecache", 1024)
	populate(upd, entries...)
	upd.Add("models/d.mdl", []byte{4, 2})
	w := bitbuf.NewWriter(0)
	upd.WriteUpdate(w, []int{3})
	got, changed, err := c.Update(0, 1, w.Reader())
	if err != nil || got != tbl || fmt.Sprint(changed) != "[3]" || tbl.Entries[3].String != "models/d.mdl" {
		t.Fatalf("update: %v %v %+v", err, changed, tbl.Entries)
	}
	if _, _, err := c.Update(9, 1, w.Reader()); err == nil {
		t.Error("update of unknown table accepted")
	}

	c.Reset()
	if len(c.Tables()) != 0 || c.ByName("modelprecache") != nil {
		t.Error("reset")
	}
}

func TestContainerCompressedCreate(t *testing.T) {
	var entries []Entry
	for i := 0; i < 40; i++ {
		entries = append(entries, Entry{String: fmt.Sprintf("%d", i), UserData: bytes.Repeat([]byte{byte(i)}, 24)})
	}
	data, nbits := buildPayload(entries, 512)
	raw := data[:bitbuf.BitsToBytes(nbits)]

	lz, err := compression.CompressLZSS(raw)
	if err != nil {
		t.Fatal(err)
	}
	for name, compressed := range map[string][]byte{"lzss": lz, "snappy": compression.CompressSnappy(raw)} {
		w := bitbuf.NewWriter(0)
		w.WriteUint32(uint32(len(raw)))
		w.WriteUint32(uint32(len(compressed)))
		w.WriteBytes(compressed)

		c := NewContainer()
		tbl, err := c.Create(CreateParams{Name: "instancebaseline", MaxEntries: 512, NumEntries: len(entries), DataCompressed: true}, w.Reader())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if tbl.Len() != len(entries) || tbl.Entries[39].String != "39" || !bytes.Equal(tbl.Entries[39].UserData, entries[39].UserData) {
			t.Fatalf("%s: entries %+v", name, tbl.Entries)
		}
	}

	// Declared size mismatch.
	w := bitbuf.NewWriter(0)
	w.WriteUint32(uint32(len(raw) + 1))
	w.WriteUint32(uint32(len(lz)))
	w.WriteBytes(lz)
	if _, err := NewContainer().Create(CreateParams{Name: "x", MaxEntries: 512, NumEntries: 40, DataCompressed: true}, w.Reader()); err == nil {
		t.Error("size mismatch accepted")
	}
}

func TestPlayerInfoLayout(t *testing.T) {
	p := PlayerInfo{
		Name:            "Scout",
		UserID:          -7,
		GUID:            "[U:1:12345]",
		FriendsID:       12345,
		FriendsName:     "friend",
		FakePlayer:      true,
		IsHLTV:          false,
		IsReplay:        true,
		CustomFiles:     [4]uint32{1, 0, 0xdeadbeef, 4},
		FilesDownloaded: 3,
	}
	b := p.Marshal(true)
	if len(b) != PlayerInfoSize {
		t.Fatalf("size %d", len(b))
	}
	if string(b[0:5]) != "Scout" || b[32] != 0xf9 || b[35] != 0xff || string(b[36:47]) != "[U:1:12345]" ||
		b[72] != 0x39 || b[73] != 0x30 || string(b[76:82]) != "friend" || b[108] != 1 || b[110] != 1 ||
		b[120] != 0xef || b[123] != 0xde || b[128] != 3 {
		t.Fatalf("unexpected layout: % x", b)
	}
	got, err := ParsePlayerInfo(b, true)
	if err != nil || got != p {
		t.Fatalf("parse = %+v, %v", got, err)
	}
	got, _ = ParsePlayerInfo(b, false)
	if got.IsReplay {
		t.Error("replay flag must be ignored for non-replay engines")
	}
	if b := p.Marshal(false); b[110] != 0 {
		t.Error("replay flag must not be written for non-replay engines")
	}
	if _, err := ParsePlayerInfo(b[:100], true); err == nil {
		t.Error("short data accepted")
	}
	long := PlayerInfo{Name: strings.Repeat("n", 40)}
	if got, _ := ParsePlayerInfo(long.Marshal(true), true); got.Name != strings.Repeat("n", 31) {
		t.Errorf("long name = %q", got.Name)
	}
}
