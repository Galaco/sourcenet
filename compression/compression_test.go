package compression

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/rand"
	"strings"
	"testing"
)

func snap(raw ...byte) []byte {
	return append([]byte(MagicSnappy), raw...)
}

func lzss(size uint32, stream ...byte) []byte {
	out := []byte(MagicLZSS)
	out = binary.LittleEndian.AppendUint32(out, size)
	return append(out, stream...)
}

// Vectors built by hand from the Snappy format description.
func TestSnappyDecodeSpecVectors(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{"empty", snap(0x00), ""},
		{"short literal", snap(0x03, 0x02<<2, 'a', 'b', 'c'), "abc"},
		{
			// literal "abcd", copy1 len 7 offset 4 (overlapping)
			"copy1 overlapping",
			snap(11, 0x03<<2, 'a', 'b', 'c', 'd', 0x01|(7-4)<<2, 0x04),
			"abcdabcdabc",
		},
		{
			// copy1 with offset high bits: offset 0x103 = 259 needs the tag's top 3 bits.
			"copy1 high offset",
			func() []byte {
				lit := bytes.Repeat([]byte{'x'}, 255)
				lit = append(lit, 'w', 'x', 'y', 'z')
				b := binary.AppendUvarint(nil, 259+4)
				b = append(b, 61<<2, 0x02, 0x01) // literal of 259 bytes, 2 length bytes
				b = append(b, lit...)
				b = append(b, 0x01|0<<2|(259>>8)<<5, 259&0xff) // len 4 offset 259
				return snap(b...)
			}(),
			strings.Repeat("x", 255) + "wxyz" + "xxxx",
		},
		{
			// literal "ab", copy2 len 6 offset 2
			"copy2",
			snap(8, 0x01<<2, 'a', 'b', 0x02|(6-1)<<2, 0x02, 0x00),
			"abababab",
		},
		{
			// literal "q", copy4 len 3 offset 1
			"copy4",
			snap(4, 0x00, 'q', 0x03|(3-1)<<2, 0x01, 0, 0, 0),
			"qqqq",
		},
		{
			// literal length stored in two extra bytes (tag 61)
			"literal two byte length",
			snap(append([]byte{0x80, 0x02, 61 << 2, 0xff, 0x00}, bytes.Repeat([]byte{'z'}, 256)...)...),
			strings.Repeat("z", 256),
		},
	}
	for _, tc := range tests {
		got, err := DecompressSnappy(tc.in)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
		if n, err := UncompressedSize(tc.in); err != nil || n != len(tc.want) {
			t.Errorf("%s: UncompressedSize = %d, %v", tc.name, n, err)
		}
	}
}

func TestSnappyDecodeCorrupt(t *testing.T) {
	tests := map[string][]byte{
		"bad varint":             snap(0xff, 0xff, 0xff, 0xff, 0xff, 0xff),
		"offset before start":    snap(5, 0x00, 'a', 0x01|(4-4)<<2, 0x02),
		"zero offset":            snap(5, 0x00, 'a', 0x01, 0x00),
		"copy past declared len": snap(3, 0x00, 'a', 0x01, 0x01),
		"literal past input":     snap(4, 0x03<<2, 'a', 'b'),
		"literal past declared":  snap(1, 0x01<<2, 'a', 'b'),
		"short output":           snap(5, 0x00, 'a'),
		"truncated copy2":        snap(4, 0x00, 'a', 0x02|2<<2, 0x01),
		"truncated copy4":        snap(4, 0x00, 'a', 0x03|2<<2, 0x01, 0x00),
		"truncated literal len":  snap(70, 60<<2),
	}
	for name, in := range tests {
		if _, err := DecompressSnappy(in); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: err = %v, want ErrCorrupt", name, err)
		}
	}
}

func TestSnappyLimit(t *testing.T) {
	in := snap(0x80, 0x80, 0x80, 0x40) // declares 128 MiB
	if _, err := Decompress(in); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if _, err := DecompressWithLimit(snap(3, 0x02<<2, 'a', 'b', 'c'), 2); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("limit err = %v", err)
	}
}

func testInputs() map[string][]byte {
	rng := rand.New(rand.NewSource(7))
	random := make([]byte, 70000)
	rng.Read(random)
	text := []byte(strings.Repeat("models/weapons/c_models/c_shotgun/c_shotgun.mdl\x00", 400))
	mixed := make([]byte, 0, 200000)
	for len(mixed) < 200000 {
		if rng.Intn(3) == 0 {
			chunk := make([]byte, rng.Intn(64))
			rng.Read(chunk)
			mixed = append(mixed, chunk...)
		} else {
			start := rng.Intn(len(text) - 100)
			mixed = append(mixed, text[start:start+rng.Intn(100)]...)
		}
	}
	return map[string][]byte{
		"empty":  {},
		"one":    {42},
		"short":  []byte("hello hello hello"),
		"zeros":  make([]byte, 150000),
		"random": random,
		"text":   text,
		"mixed":  mixed,
	}
}

func TestSnappyRoundTrip(t *testing.T) {
	for name, in := range testInputs() {
		enc := CompressSnappy(in)
		if !IsCompressed(enc) {
			t.Fatalf("%s: missing magic", name)
		}
		out, err := Decompress(enc)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(out, in) {
			t.Fatalf("%s: round trip mismatch", name)
		}
		if name == "zeros" || name == "text" {
			if len(enc) > len(in)/10 {
				t.Errorf("%s: poor compression %d -> %d", name, len(in), len(enc))
			}
		}
	}
}

// Hand-built LZSS stream: literals "abc", a match copying "abcabc" (distance
// 3 => encoded 2, length 6), then the end marker.
func TestLZSSDecodeHandBuilt(t *testing.T) {
	// cmd bits (LSB first): lit, lit, lit, match, end
	cmd := byte(0b11000)
	in := lzss(9, cmd, 'a', 'b', 'c', 0x00, 0x20|(6-1), 0x00, 0x00)
	got, err := DecompressLZSS(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "abcabcabc" {
		t.Fatalf("got %q", got)
	}
	if n, _ := UncompressedSize(in); n != 9 {
		t.Fatalf("size %d", n)
	}
}

func TestLZSSDecodeCorrupt(t *testing.T) {
	tests := map[string][]byte{
		"truncated":        lzss(3, 0x00, 'a'),
		"distance too far": lzss(5, 0b10, 'a', 0x00, 0x13),
		"overrun size":     lzss(2, 0b000, 'a', 'b', 'c'),
		"size mismatch":    lzss(4, 0b10, 'a', 0x00, 0x00),
		"match past size":  lzss(3, 0b10, 'a', 0x00, 0x05),
		"truncated match":  lzss(3, 0b10, 'a', 0x00),
		"missing end":      lzss(1, 0x00, 'a'),
	}
	for name, in := range tests {
		if _, err := DecompressLZSS(in); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: err = %v, want ErrCorrupt", name, err)
		}
	}
	if out, err := DecompressLZSS(lzss(0)); err != nil || len(out) != 0 {
		t.Errorf("zero size: %v %v", out, err)
	}
	if _, err := DecompressLZSS(lzss(1 << 30)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("huge size: %v", err)
	}
}

func TestLZSSRoundTrip(t *testing.T) {
	for name, in := range testInputs() {
		enc, err := CompressLZSS(in)
		if name == "random" || name == "empty" || name == "one" || name == "short" {
			if !errors.Is(err, ErrNotCompressible) {
				t.Errorf("%s: expected ErrNotCompressible, got %v", name, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(enc) >= len(in) {
			t.Fatalf("%s: output not smaller", name)
		}
		out, err := Decompress(enc)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(out, in) {
			t.Fatalf("%s: round trip mismatch", name)
		}
	}
}

// Every possible final command-byte occupancy must produce a valid stream.
func TestLZSSEndMarkerAlignment(t *testing.T) {
	base := bytes.Repeat([]byte("abcdefgh"), 8)
	for extra := 0; extra < 16; extra++ {
		in := append(append([]byte{}, base...), []byte("ZYXWVUTSRQPONMLK")[:extra]...)
		enc, err := CompressLZSS(in)
		if err != nil {
			t.Fatalf("extra %d: %v", extra, err)
		}
		out, err := DecompressLZSS(enc)
		if err != nil || !bytes.Equal(out, in) {
			t.Fatalf("extra %d: %v", extra, err)
		}
	}
}

func TestUnknownFormat(t *testing.T) {
	for _, in := range [][]byte{nil, []byte("LZS"), []byte("ABCDxxxx"), []byte("SNAP")} {
		if IsCompressed(in) {
			t.Errorf("%q reported as compressed", in)
		}
		if _, err := Decompress(in); !errors.Is(err, ErrUnknownFormat) {
			t.Errorf("%q: err = %v", in, err)
		}
		if _, err := UncompressedSize(in); !errors.Is(err, ErrUnknownFormat) {
			t.Errorf("%q: size err = %v", in, err)
		}
	}
}

func FuzzDecompress(f *testing.F) {
	f.Add(snap(11, 0x03<<2, 'a', 'b', 'c', 'd', 0x01|(7-4)<<2, 0x04))
	f.Add(lzss(9, 0b11000, 'a', 'b', 'c', 0x00, 0x25, 0x00, 0x00))
	f.Add(CompressSnappy([]byte("hello hello hello hello hello")))
	f.Fuzz(func(t *testing.T, in []byte) {
		out, err := DecompressWithLimit(in, 1<<20)
		if err == nil {
			if n, _ := UncompressedSize(in); n != len(out) {
				t.Fatalf("size %d != %d", n, len(out))
			}
		}
	})
}
