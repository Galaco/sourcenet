package connectionless

import (
	"bytes"
	"encoding/hex"
	"hash/crc32"
	"reflect"
	"testing"

	"github.com/galaco/sourcenet/protocol"
)

// b builds a byte slice from mixed parts: strings are written verbatim,
// bytes and byte slices as-is.
func b(parts ...any) []byte {
	var out []byte
	for _, p := range parts {
		switch v := p.(type) {
		case string:
			out = append(out, v...)
		case byte:
			out = append(out, v)
		case []byte:
			out = append(out, v...)
		default:
			panic("unsupported part")
		}
	}
	return out
}

func le32b(v uint32) []byte { return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)} }
func le16b(v uint16) []byte { return []byte{byte(v), byte(v >> 8)} }
func le64b(v uint64) []byte { return append(le32b(uint32(v)), le32b(uint32(v>>32))...) }

var hdr = []byte{0xff, 0xff, 0xff, 0xff}

func TestGoldenPackets(t *testing.T) {
	cases := []struct {
		name string
		p    Packet
		want []byte
	}{
		{"getchallenge", &GetChallenge{ClientChallenge: 0x0abc1234},
			b(hdr, "q", le32b(0x0abc1234), "0000000000\x00")},
		{"challenge steam", &Challenge{ServerChallenge: 7, ClientChallenge: 9, AuthProtocol: protocol.AuthSteam, ServerSteamID: 0x0110000100000001, HasSteamID: true, Secure: true},
			b(hdr, "A", le32b(protocol.S2CMagicVersion), le32b(7), le32b(9), le32b(3), le16b(0), le64b(0x0110000100000001), byte(1), "000000\x00")},
		{"challenge cdkey", &Challenge{ServerChallenge: 1, ClientChallenge: 2, AuthProtocol: protocol.AuthHashedCDKey},
			b(hdr, "A", le32b(protocol.S2CMagicVersion), le32b(1), le32b(2), le32b(2), "000000\x00")},
		{"connect steam", &Connect{Protocol: 24, AuthProtocol: protocol.AuthSteam, ServerChallenge: 5, ClientChallenge: 6, Name: "bob", Password: "pw", ProductVersion: "8622567", SteamCookie: []byte{1, 2, 3}},
			b(hdr, "k", le32b(24), le32b(3), le32b(5), le32b(6), "bob\x00pw\x008622567\x00", le16b(3), []byte{1, 2, 3})},
		{"connect cdkey", &Connect{Protocol: 24, AuthProtocol: protocol.AuthHashedCDKey, ServerChallenge: 5, ClientChallenge: 6, Name: "a", ProductVersion: "1", CDKeyHash: "abc"},
			b(hdr, "k", le32b(24), le32b(2), le32b(5), le32b(6), "a\x00\x001\x00abc\x00")},
		{"accept", &Accept{ClientChallenge: 0x01020304},
			b(hdr, "B", le32b(0x01020304), "0000000000\x00")},
		{"accept legacy", &Accept{Legacy: true},
			b(hdr, "B00000000000000\x00")},
		{"reject", &Reject{ClientChallenge: 3, Reason: "#GameUI_ServerRejectBadChallenge"},
			b(hdr, "9", le32b(3), "#GameUI_ServerRejectBadChallenge\x00")},
		{"info request", &InfoRequest{},
			b(hdr, "TSource Engine Query\x00")},
		{"info request challenge", &InfoRequest{Challenge: 0x11223344, HasChallenge: true},
			b(hdr, "TSource Engine Query\x00", le32b(0x11223344))},
		{"query challenge", &QueryChallenge{Challenge: 0xdeadbeef},
			b(hdr, "A", le32b(0xdeadbeef))},
		{"player request", &PlayerRequest{Challenge: NoChallenge},
			b(hdr, "U", le32b(0xffffffff))},
		{"rules request", &RulesRequest{Challenge: 42},
			b(hdr, "V", le32b(42))},
		{"players", &Players{Players: []Player{{Index: 0, Name: "x", Score: -2, Duration: 1.5}}},
			b(hdr, "D", byte(1), byte(0), "x\x00", le32b(0xfffffffe), le32b(0x3fc00000))},
		{"rules", &Rules{Rules: []Rule{{"sv_gravity", "800"}, {"mp_timelimit", "30"}}},
			b(hdr, "E", le16b(2), "sv_gravity\x00800\x00mp_timelimit\x0030\x00")},
		{"servergetchallenge", &ServerQueryGetChallenge{}, b(hdr, "W")},
		{"ping", &Ping{}, b(hdr, "i")},
		{"ack", &Ack{Payload: "00000000000000"}, b(hdr, "j00000000000000\x00")},
		{"info", &Info{Protocol: 17, Name: "srv", Map: "cp_badlands", Folder: "tf", Game: "Team Fortress", AppID: 440,
			Players: 3, MaxPlayers: 24, Bots: 1, ServerType: 'd', Environment: 'l', Visibility: 0, VAC: 1, Version: "8622567",
			EDF: EDFPort | EDFSteamID | EDFSourceTV | EDFKeywords | EDFGameID, Port: 27015, SteamID: 90000000000000001,
			SourceTVPort: 27020, SourceTVName: "tv", Keywords: "cp,increased_maxplayers", GameID: 440},
			b(hdr, "I", byte(17), "srv\x00cp_badlands\x00tf\x00Team Fortress\x00", le16b(440), byte(3), byte(24), byte(1), "d", "l", byte(0), byte(1),
				"8622567\x00", byte(0xf1), le16b(27015), le64b(90000000000000001), le16b(27020), "tv\x00", "cp,increased_maxplayers\x00", le64b(440))},
	}
	for _, c := range cases {
		got := c.p.Marshal()
		if !bytes.Equal(got, c.want) {
			t.Errorf("%s: marshal\n got % x\nwant % x", c.name, got, c.want)
			continue
		}
		parsed, err := Parse(got)
		if err != nil {
			t.Errorf("%s: parse: %v", c.name, err)
			continue
		}
		if !reflect.DeepEqual(parsed, c.p) {
			t.Errorf("%s: round trip\n got %#v\nwant %#v", c.name, parsed, c.p)
		}
		if parsed.ID() != c.p.ID() {
			t.Errorf("%s: id %q != %q", c.name, parsed.ID(), c.p.ID())
		}
	}
}

func TestChallengeWithoutSteamID(t *testing.T) {
	// A Steam challenge whose steam id is absent (no more than 8 bytes left).
	raw := b(hdr, "A", le32b(protocol.S2CMagicVersion), le32b(1), le32b(2), le32b(3), le16b(0), "000000\x00")
	p, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	c := p.(*Challenge)
	if c.HasSteamID || c.AuthProtocol != protocol.AuthSteam {
		t.Fatalf("%+v", c)
	}
}

func TestShipInfo(t *testing.T) {
	in := &Info{Name: "ship", AppID: AppIDTheShip, ShipMode: 1, ShipWitnesses: 2, ShipDuration: 3, Version: "1"}
	p, err := Parse(in.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p, in) {
		t.Fatalf("%+v", p)
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse([]byte{1, 2, 3, 4, 5}); err != ErrNotConnectionless {
		t.Errorf("missing header: %v", err)
	}
	if _, err := Parse(hdr); err != ErrTruncated {
		t.Errorf("no id: %v", err)
	}
	p, err := Parse(b(hdr, "z", "payload"))
	if err != nil {
		t.Fatal(err)
	}
	u, ok := p.(*Unknown)
	if !ok || u.IDByte != 'z' || string(u.Payload) != "payload" || !bytes.Equal(u.Marshal(), b(hdr, "zpayload")) {
		t.Errorf("unknown: %#v", p)
	}
	truncated := []Packet{
		&Connect{Protocol: 24, AuthProtocol: protocol.AuthSteam, SteamCookie: []byte{1, 2, 3, 4}},
		&Reject{ClientChallenge: 1, Reason: "x"},
		&Info{Name: "n", Version: "v", EDF: EDFKeywords, Keywords: "k"},
		&Players{Players: []Player{{Name: "a"}}},
	}
	for _, pk := range truncated {
		raw := pk.Marshal()
		if _, err := Parse(raw[:len(raw)-2]); err == nil {
			t.Errorf("%T: expected error for truncated packet", pk)
		}
	}
	if _, err := Parse(b(hdr, "A", le32b(protocol.S2CMagicVersion), le32b(1), le32b(2), le32b(3))); err == nil {
		t.Error("challenge truncated inside the auth protocol must fail")
	}
	if _, err := Parse(b(hdr, "k", le32b(24), le32b(3), le32b(0), le32b(0), "\x00\x00\x00", le16b(0xffff))); err == nil {
		t.Error("negative cookie length must fail")
	}
}

func TestTruncatedListsAreTolerated(t *testing.T) {
	// The rule count claims 3 entries but only one is present.
	p, err := Parse(b(hdr, "E", le16b(3), "a\x00b\x00"))
	if err != nil {
		t.Fatal(err)
	}
	if r := p.(*Rules); len(r.Rules) != 1 || r.Map()["a"] != "b" {
		t.Fatalf("%+v", r)
	}
	if v, ok := p.(*Rules).Get("a"); !ok || v != "b" {
		t.Fatal("Get")
	}
}

func TestCDKeyHash(t *testing.T) {
	// md5("1234567890123")
	if got := CDKeyHash(""); got != "ee76971984d25138a199ac90553401dd" {
		t.Fatalf("hash %q", got)
	}
	if CDKeyHash("") != CDKeyHash(DefaultCDKey) {
		t.Fatal("empty key must use default")
	}
	if got := CDKeyHash("abc"); got != "900150983cd24fb0d6963f7d28e17f72" {
		t.Fatalf("md5(abc) = %s", got)
	}
}

func TestQuerySplitRoundTrip(t *testing.T) {
	big := (&Rules{}).Marshal()
	for i := 0; i < 300; i++ {
		big = append(big, "some_rule_name\x00some value\x00"...)
	}
	frags := SplitQueryResponse(big, 77, 1248)
	if len(frags) < 3 {
		t.Fatalf("expected several fragments, got %d", len(frags))
	}
	var q QuerySplitReassembler
	// Deliver in reverse order with a duplicate.
	order := []int{}
	for i := len(frags) - 1; i >= 0; i-- {
		order = append(order, i)
	}
	order = append([]int{len(frags) - 1}, order...)
	var out []byte
	for n, i := range order {
		got, done, err := q.Add(frags[i])
		if err != nil {
			t.Fatal(err)
		}
		if done != (n == len(order)-1) {
			t.Fatalf("done=%v at step %d", done, n)
		}
		out = got
	}
	if !bytes.Equal(out, big) {
		t.Fatal("reassembled payload differs")
	}
	if q.Pending() != 0 {
		t.Fatal("state left behind")
	}
	s, err := ParseQuerySplit(frags[1])
	if err != nil || s.ID != 77 || s.Number != 1 || int(s.Total) != len(frags) || s.Size != 1248 || s.Compressed {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestQuerySplitCompressed(t *testing.T) {
	plain := []byte("\xff\xff\xff\xffEhello compressed rules response repeated repeated repeated")
	comp, _ := hex.DecodeString("425a683931415926535961d6cee600001fd580c000400002002e47de000000a00054568d308d1a6d41a6a4f49b44fd43294a2074dfc6f480fc69162a5b842a8740f74eda940397abd802e021f17724538509061d6cee60")
	half := len(comp) / 2
	frag0 := b(le32b(0xfffffffe), le32b(5|0x80000000), byte(2), byte(0), le16b(1248), le32b(uint32(len(plain))), le32b(crc32.ChecksumIEEE(plain)), comp[:half])
	frag1 := b(le32b(0xfffffffe), le32b(5|0x80000000), byte(2), byte(1), le16b(1248), comp[half:])
	var q QuerySplitReassembler
	if _, done, err := q.Add(frag1); done || err != nil {
		t.Fatal(done, err)
	}
	out, done, err := q.Add(frag0)
	if err != nil || !done {
		t.Fatal(done, err)
	}
	if !bytes.Equal(out, plain) {
		t.Fatalf("got %q", out)
	}

	// A bad CRC must be rejected.
	bad := append([]byte(nil), frag0...)
	bad[16] ^= 0xff
	q.Reset()
	q.Add(frag1)
	if _, _, err := q.Add(bad); err == nil {
		t.Fatal("expected CRC error")
	}
}

func TestParseQuerySplitErrors(t *testing.T) {
	if _, err := ParseQuerySplit(hdr); err != ErrNotSplit {
		t.Error(err)
	}
	if _, err := ParseQuerySplit(le32b(0xfffffffe)); err != ErrTruncated {
		t.Error(err)
	}
	if _, err := ParseQuerySplit(b(le32b(0xfffffffe), le32b(1), byte(2), byte(2), le16b(0))); err == nil {
		t.Error("fragment index beyond total must fail")
	}
}
