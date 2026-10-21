package demo

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func sampleHeader() Header {
	return Header{Protocol: Protocol, NetworkProtocol: 24, ServerName: "127.0.0.1:27015", ClientName: "player",
		MapName: "ctf_2fort", GameDir: "tf", PlaybackTime: 12.5, PlaybackTicks: 825, PlaybackFrames: 400, SignonLength: 123456}
}

func sampleFrames() []*Frame {
	return []*Frame{
		{Command: Signon, Tick: 0, CmdInfo: CmdInfo{Flags: FlagNoInterp, ViewOrigin: [3]float32{1, 2, 3}}, SeqIn: 1, SeqOut: 2, Data: []byte{0xaa, 0xbb}},
		{Command: DataTables, Tick: 0, Data: []byte("tables")},
		{Command: StringTables, Tick: 0, Data: []byte("strings")},
		{Command: SyncTick, Tick: 1},
		{Command: Packet, Tick: 2, CmdInfo: CmdInfo{ViewAngles: [3]float32{0, 90, 0}, LocalViewAngles2: [3]float32{4, 5, 6}}, SeqIn: 10, SeqOut: 9, Data: []byte{1, 2, 3}},
		{Command: ConsoleCmd, Tick: 3, Data: []byte("say hi\x00")},
		{Command: UserCmd, Tick: 4, OutgoingSequence: 77, Data: []byte{9}},
		{Command: Packet, Tick: 5, Data: []byte{}},
		{Command: Stop, Tick: 6},
	}
}

func TestRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, sampleHeader())
	if err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 1072 {
		t.Fatalf("header is %d bytes, want 1072", buf.Len())
	}
	for _, f := range sampleFrames() {
		if err := w.WriteFrame(f); err != nil {
			t.Fatal(err)
		}
	}
	r, err := NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if r.Header() != sampleHeader() {
		t.Fatalf("header %+v", r.Header())
	}
	for i, want := range sampleFrames() {
		got, err := r.Next()
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("frame %d:\n got %+v\nwant %+v", i, got, want)
		}
	}
	if _, err := r.Next(); err != io.EOF {
		t.Fatalf("after stop: %v", err)
	}
}

func TestGoldenPacketLayout(t *testing.T) {
	var buf bytes.Buffer
	w := &Writer{w: &buf}
	if err := w.WriteFrame(&Frame{Command: Packet, Tick: 0x01020304, CmdInfo: CmdInfo{Flags: 7}, SeqIn: 5, SeqOut: 6, Data: []byte{0xee}}); err != nil {
		t.Fatal(err)
	}
	want := []byte{2, 4, 3, 2, 1, 7, 0, 0, 0}
	want = append(want, make([]byte, 72)...)
	want = append(want, 5, 0, 0, 0, 6, 0, 0, 0, 1, 0, 0, 0, 0xee)
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("got % x", buf.Bytes())
	}
}

func TestEndWithoutStop(t *testing.T) {
	var buf bytes.Buffer
	w, _ := NewWriter(&buf, sampleHeader())
	w.WriteFrame(&Frame{Command: SyncTick, Tick: 1})
	r, err := NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Next(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Next(); err != io.EOF {
		t.Fatalf("got %v", err)
	}
}

func TestErrors(t *testing.T) {
	if _, err := NewReader(bytes.NewReader([]byte("short"))); err == nil {
		t.Error("short header")
	}
	bad := encodeHeader(sampleHeader())
	copy(bad, "HL3DEMO")
	if _, err := NewReader(bytes.NewReader(bad)); err != ErrInvalidHeader {
		t.Errorf("bad magic: %v", err)
	}
	h := sampleHeader()
	h.Protocol = 9
	if _, err := NewReader(bytes.NewReader(encodeHeader(h))); err == nil {
		t.Error("unsupported protocol")
	}

	// Truncated frame.
	var buf bytes.Buffer
	w, _ := NewWriter(&buf, sampleHeader())
	w.WriteFrame(&Frame{Command: Packet, Tick: 1, Data: []byte{1, 2, 3, 4}})
	r, _ := NewReader(bytes.NewReader(buf.Bytes()[:buf.Len()-2]))
	if _, err := r.Next(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("truncated frame: %v", err)
	}

	// Unknown command and absurd length.
	data := append(encodeHeader(sampleHeader()), 42, 0, 0, 0, 0)
	r, _ = NewReader(bytes.NewReader(data))
	if _, err := r.Next(); err == nil {
		t.Error("unknown command")
	}
	data = append(encodeHeader(sampleHeader()), byte(ConsoleCmd), 0, 0, 0, 0)
	data = binary.LittleEndian.AppendUint32(data, 0xffffffff)
	r, _ = NewReader(bytes.NewReader(data))
	if _, err := r.Next(); err == nil {
		t.Error("negative length")
	}
}

func TestRewriteHeader(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "x.dem"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w, err := NewWriter(f, Header{Protocol: Protocol, NetworkProtocol: 24})
	if err != nil {
		t.Fatal(err)
	}
	w.WriteFrame(&Frame{Command: Stop, Tick: 3})
	final := sampleHeader()
	if err := w.RewriteHeader(final); err != nil {
		t.Fatal(err)
	}
	f.Seek(0, io.SeekStart)
	r, err := NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if r.Header() != final {
		t.Fatal("header not rewritten")
	}
	if fr, err := r.Next(); err != nil || fr.Command != Stop || fr.Tick != 3 {
		t.Fatalf("%+v %v", fr, err)
	}
	if (&Writer{w: &bytes.Buffer{}}).RewriteHeader(final) == nil {
		t.Error("non-seekable writer must fail")
	}
}

func TestCommandString(t *testing.T) {
	if Packet.String() != "dem_packet" || Command(99).String() != "dem_unknown(99)" {
		t.Fatal("names")
	}
	f := &Frame{Data: []byte("echo\x00junk")}
	if f.ConsoleCommand() != "echo" {
		t.Fatal("console command")
	}
}
