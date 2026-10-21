package packet

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/rand"
	"testing"
	"time"

	"github.com/galaco/sourcenet/compression"
	"github.com/galaco/sourcenet/protocol"
)

var t0 = time.Unix(1000, 0)

func withHeader(h int32, rest ...byte) []byte {
	b := make([]byte, 4)
	putHeader(b, h)
	return append(b, rest...)
}

func payload(rng *rand.Rand, n int, compressible bool) []byte {
	b := make([]byte, n)
	if compressible {
		for i := range b {
			b[i] = byte("sourcenet"[i%9])
		}
		for i := 0; i < n/50; i++ {
			b[rng.Intn(n)] = byte(rng.Intn(256))
		}
	} else {
		rng.Read(b)
	}
	// Make sure it doesn't look like a framing header.
	if n >= 4 {
		binary.LittleEndian.PutUint32(b, 1234)
	}
	return b
}

func TestClassify(t *testing.T) {
	tests := []struct {
		in   []byte
		want Kind
	}{
		{nil, KindInvalid},
		{withHeader(-1), KindInvalid},
		{withHeader(-1, 'T'), KindConnectionless},
		{withHeader(-2, 0), KindSplit},
		{withHeader(-3, 0), KindCompressed},
		{withHeader(42, 0), KindNetChannel},
		{withHeader(-7, 0), KindNetChannel},
	}
	for _, tc := range tests {
		if got := Classify(tc.in); got != tc.want {
			t.Errorf("Classify(% x) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestSplitHeaderLayout(t *testing.T) {
	data := bytes.Repeat([]byte{7}, 2000)
	parts, err := Split(data, 77, 1260)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 {
		t.Fatalf("got %d parts", len(parts))
	}
	want := []byte{0xfe, 0xff, 0xff, 0xff, 77, 0, 0, 0, 0x02, 0x01, 0xe0, 0x04}
	if !bytes.Equal(parts[1][:12], want) {
		t.Fatalf("header % x, want % x", parts[1][:12], want)
	}
	if len(parts[0]) != 1260 || len(parts[1]) != 12+2000-1248 {
		t.Fatalf("part sizes %d %d", len(parts[0]), len(parts[1]))
	}
	h, p, err := ParseSplitHeader(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	if h != (SplitHeader{Sequence: 77, Index: 1, Count: 2, SplitSize: 1248}) || len(p) != 752 {
		t.Fatalf("header %+v payload %d", h, len(p))
	}
}

func TestSplitClampsRoutable(t *testing.T) {
	data := make([]byte, 5000)
	parts, _ := Split(data, 1, 100) // clamped up to 576
	if len(parts[0]) != protocol.MinUserRoutablePayload {
		t.Fatalf("part size %d", len(parts[0]))
	}
	parts, _ = Split(data, 1, 9000) // clamped down to 1260
	if len(parts[0]) != protocol.MaxRoutablePayload {
		t.Fatalf("part size %d", len(parts[0]))
	}
	if _, err := Split(nil, 1, 1260); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := Split(make([]byte, MaxSendSplits*MaxSplitSize+1), 1, 1260); !errors.Is(err, ErrTooManySplits) {
		t.Fatalf("too many: %v", err)
	}
}

func reassemble(t *testing.T, r *Reassembler, parts [][]byte, order []int) []byte {
	t.Helper()
	var out []byte
	for i, idx := range order {
		pkt, done, err := r.Add(t0, parts[idx])
		if err != nil {
			t.Fatalf("part %d: %v", idx, err)
		}
		if done != (i == len(order)-1) {
			t.Fatalf("part %d (step %d): complete=%v", idx, i, done)
		}
		out = pkt
	}
	return out
}

func TestReassemblerOrders(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	data := payload(rng, 6000, false)
	parts, err := Split(data, 5, 1260)
	if err != nil {
		t.Fatal(err)
	}
	n := len(parts)
	inOrder := make([]int, n)
	reversed := make([]int, n)
	for i := range inOrder {
		inOrder[i] = i
		reversed[i] = n - 1 - i
	}
	for name, order := range map[string][]int{
		"in order": inOrder,
		"reversed": reversed,
		"shuffled": rng.Perm(n),
	} {
		got := reassemble(t, NewReassembler(), parts, order)
		if !bytes.Equal(got, data) {
			t.Errorf("%s: mismatch", name)
		}
	}
}

func TestReassemblerDuplicates(t *testing.T) {
	data := bytes.Repeat([]byte{1, 2, 3}, 1000)
	parts, _ := Split(data, 9, 1260)
	r := NewReassembler()
	for _, idx := range []int{0, 0, 1, 0, 1} {
		if _, done, err := r.Add(t0, parts[idx]); err != nil || done {
			t.Fatalf("part %d: done=%v err=%v", idx, done, err)
		}
	}
	got, done, err := r.Add(t0, parts[2])
	if err != nil || !done || !bytes.Equal(got, data) {
		t.Fatalf("completion: done=%v err=%v", done, err)
	}
	// A late duplicate of a completed packet must not deliver it again.
	for _, p := range parts {
		if _, done, _ := r.Add(t0, p); done {
			t.Fatal("completed packet delivered twice")
		}
	}
}

func TestReassemblerNewSequenceAbandonsOld(t *testing.T) {
	a := bytes.Repeat([]byte{0xaa}, 3000)
	b := bytes.Repeat([]byte{0xbb}, 2600)
	pa, _ := Split(a, 10, 1260)
	pb, _ := Split(b, 11, 1260)
	r := NewReassembler()
	r.Add(t0, pa[0])
	r.Add(t0, pa[1])
	got := reassemble(t, r, pb, []int{1, 0, 2})
	if !bytes.Equal(got, b) {
		t.Fatal("second packet mismatch")
	}
	// The abandoned packet's last part now restarts a fresh, incomplete assembly.
	if _, done, err := r.Add(t0, pa[2]); err != nil || done {
		t.Fatalf("abandoned part: done=%v err=%v", done, err)
	}
}

func TestReassemblerStale(t *testing.T) {
	data := bytes.Repeat([]byte{5}, 2000)
	parts, _ := Split(data, 3, 1260)
	r := NewReassembler()
	if _, done, _ := r.Add(t0, parts[0]); done {
		t.Fatal("unexpected completion")
	}
	_, done, err := r.Add(t0.Add(StaleTimeout), parts[1])
	if err != nil || done {
		t.Fatalf("stale assembly completed: done=%v err=%v", done, err)
	}
	got, done, err := r.Add(t0.Add(StaleTimeout+time.Second), parts[0])
	if err != nil || !done || !bytes.Equal(got, data) {
		t.Fatalf("restart after stale: done=%v err=%v", done, err)
	}
}

func TestReassemblerRejectsBadHeaders(t *testing.T) {
	mk := func(seq int32, idx, count, splitSize, n int) []byte {
		b := withHeader(-2)
		b = binary.LittleEndian.AppendUint32(b, uint32(seq))
		b = binary.LittleEndian.AppendUint16(b, uint16(idx<<8|count))
		b = binary.LittleEndian.AppendUint16(b, uint16(splitSize))
		return append(b, make([]byte, n)...)
	}
	tests := map[string][]byte{
		"split size too small": mk(1, 0, 2, MinSplitSize-1, MinSplitSize-1),
		"split size too large": mk(1, 0, 2, MaxSplitSize+1, MaxSplitSize+1),
		"negative split size":  mk(1, 0, 2, 0xffff, 100),
		"zero count":           mk(1, 0, 0, 1000, 1000),
		"index past count":     mk(1, 2, 2, 1000, 10),
		"short middle part":    mk(1, 0, 2, 1000, 999),
		"oversize last part":   mk(1, 1, 2, 1000, 1001),
		"empty payload":        mk(1, 1, 2, 1000, 0),
	}
	for name, d := range tests {
		if _, _, err := NewReassembler().Add(t0, d); !errors.Is(err, ErrBadSplit) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, _, err := NewReassembler().Add(t0, withHeader(-2, 1, 2, 3)); !errors.Is(err, ErrTooShort) {
		t.Errorf("short: %v", err)
	}
	if _, _, err := NewReassembler().Add(t0, mk(1, 0, 2, 1000, 1000)[:12]); !errors.Is(err, ErrBadSplit) {
		t.Errorf("header only: %v", err)
	}
	if _, _, err := ParseSplitHeader(withHeader(-1, make([]byte, 20)...)); !errors.Is(err, ErrNotSplit) {
		t.Errorf("not split: %v", err)
	}

	r := NewReassembler()
	if _, _, err := r.Add(t0, mk(4, 0, 3, 1000, 1000)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Add(t0, mk(4, 1, 3, 900, 900)); !errors.Is(err, ErrSplitMismatch) {
		t.Errorf("mismatch: %v", err)
	}
}

func TestCompressDecompress(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	data := payload(rng, 4000, true)
	framed, ok := Compress(data)
	if !ok || len(framed) >= len(data) || Classify(framed) != KindCompressed {
		t.Fatalf("compress: ok=%v size=%d", ok, len(framed))
	}
	out, err := Decompress(framed)
	if err != nil || !bytes.Equal(out, data) {
		t.Fatalf("decompress: %v", err)
	}

	random := payload(rng, 4000, false)
	if out, ok := Compress(random); ok || !bytes.Equal(out, random) {
		t.Fatal("incompressible data should be returned unchanged")
	}
	oob := withHeader(-1, bytes.Repeat([]byte{'a'}, 2000)...)
	if _, ok := Compress(oob); ok {
		t.Fatal("connectionless packets must not be compressed")
	}

	// LZSS payloads are accepted too.
	lz, err := compression.CompressLZSS(data)
	if err != nil {
		t.Fatal(err)
	}
	out, err = Decompress(withHeader(-3, lz...))
	if err != nil || !bytes.Equal(out, data) {
		t.Fatalf("lzss: %v", err)
	}

	for name, d := range map[string][]byte{
		"not compressed": withHeader(5, 1, 2),
		"garbage":        withHeader(-3, 'x', 'y', 'z', 'w', 1),
		"too large":      withHeader(-3, append([]byte("SNAP"), 0x80, 0x80, 0x80, 0x01)...),
		"empty":          withHeader(-3, 'S', 'N', 'A', 'P', 0),
	} {
		if _, err := Decompress(d); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestDecoderPassThrough(t *testing.T) {
	d := NewDecoder()
	for _, in := range [][]byte{withHeader(-1, 'A', 1, 2), withHeader(7, 1, 2, 3)} {
		out, ok, err := d.Decode(t0, in)
		if err != nil || !ok || !bytes.Equal(out, in) {
			t.Fatalf("pass through: ok=%v err=%v", ok, err)
		}
	}
	if _, _, err := d.Decode(t0, []byte{1, 2}); !errors.Is(err, ErrTooShort) {
		t.Fatalf("short: %v", err)
	}
}

func TestEncoderDecoderRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	sizes := []int{5, 100, 1023, 1024, 1259, 1260, 1261, 2500, 4096, 10000, 50000, 100000}
	for i := 0; i < 20; i++ {
		sizes = append(sizes, 5+rng.Intn(100000))
	}
	for _, compress := range []bool{false, true} {
		for _, maxRoutable := range []int{0, 576, 1000} {
			enc := &Encoder{MaxRoutable: maxRoutable, Compress: compress}
			dec := NewDecoder()
			for _, size := range sizes {
				data := payload(rng, size, rng.Intn(2) == 0)
				limit := clampRoutable(maxRoutable)
				datagrams, err := enc.Encode(data)
				if errors.Is(err, ErrTooManySplits) && size > MaxSendSplits*(limit-protocol.SplitHeaderSize) {
					continue
				}
				if err != nil {
					t.Fatalf("size %d: %v", size, err)
				}
				for _, dg := range datagrams {
					if len(dg) > limit {
						t.Fatalf("size %d: datagram of %d bytes exceeds %d", size, len(dg), limit)
					}
				}
				rng.Shuffle(len(datagrams), func(i, j int) { datagrams[i], datagrams[j] = datagrams[j], datagrams[i] })
				var got []byte
				for j, dg := range datagrams {
					out, ok, err := dec.Decode(t0, dg)
					if err != nil {
						t.Fatalf("size %d part %d: %v", size, j, err)
					}
					if ok != (j == len(datagrams)-1) {
						t.Fatalf("size %d part %d/%d: ok=%v", size, j, len(datagrams), ok)
					}
					got = out
				}
				if !bytes.Equal(got, data) {
					t.Fatalf("size %d compress=%v routable=%d: mismatch", size, compress, maxRoutable)
				}
			}
		}
	}
}

func TestEncoderSequenceAndCompression(t *testing.T) {
	enc := &Encoder{Compress: true}
	small := bytes.Repeat([]byte{'a'}, 1000)
	binary.LittleEndian.PutUint32(small, 3)
	out, _ := enc.Encode(small)
	if !bytes.Equal(out[0], small) {
		t.Fatal("packets below CompressMinSize must not be compressed")
	}
	big := bytes.Repeat([]byte{'a'}, 2000)
	binary.LittleEndian.PutUint32(big, 3)
	out, _ = enc.Encode(big)
	if len(out) != 1 || Classify(out[0]) != KindCompressed {
		t.Fatal("large compressible packet should be compressed")
	}

	enc = &Encoder{}
	rng := rand.New(rand.NewSource(1))
	for want := int32(2); want < 5; want++ {
		parts, err := enc.Encode(payload(rng, 3000, false))
		if err != nil {
			t.Fatal(err)
		}
		h, _, _ := ParseSplitHeader(parts[0])
		if h.Sequence != want {
			t.Fatalf("split sequence %d, want %d", h.Sequence, want)
		}
	}
	if _, err := enc.Encode(nil); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty: %v", err)
	}
}

func FuzzDecoder(f *testing.F) {
	parts, _ := Split(bytes.Repeat([]byte{9}, 1500), 2, 1260)
	f.Add(parts[0], parts[1])
	f.Add(withHeader(-3, compression.CompressSnappy([]byte("hello hello hello"))...), []byte{})
	f.Fuzz(func(t *testing.T, a, b []byte) {
		d := NewDecoder()
		d.Decode(t0, a)
		d.Decode(t0, b)
	})
}
