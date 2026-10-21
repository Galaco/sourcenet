package usermessage

import (
	"reflect"
	"testing"

	"github.com/galaco/sourcenet/bitbuf"
)

func TestRegistryIndices(t *testing.T) {
	cases := []struct {
		list  []string
		name  string
		index int
		total int
	}{
		{TF2, "SayText2", 4, 85},
		{TF2, "VoteStart", 46, 85},
		{TF2, "AutoBalanceVolunteer_Cancel", 78, 85},
		{TF2, "HapMeleeContact", 84, 85},
		{CStrike, "HudMsg", 6, 47},
		{CStrike, "SPHapWeapEvent", 36, 47},
		{CStrike, "PlayerStatsUpdate", 46, 47},
		{DODS, "SayText", 3, 42},
		{DODS, "VoteSetup", 35, 42},
		{HL2MP, "Battery", 15, 34},
		{HL2MP, "UpdateJalopyRadar", 27, 34},
	}
	for _, c := range cases {
		r := NewRegistry(c.list)
		if r.Len() != c.total {
			t.Errorf("%s list: %d messages, want %d", c.name, r.Len(), c.total)
		}
		if id, ok := r.ID(c.name); !ok || id != c.index {
			t.Errorf("%s = %d, want %d", c.name, id, c.index)
		}
		if n, _ := r.Name(c.index); n != c.name {
			t.Errorf("index %d = %s, want %s", c.index, n, c.name)
		}
	}
	r := NewRegistry(TF2)
	if _, ok := r.Name(-1); ok {
		t.Error("negative index")
	}
	if id := r.Register("NewerMessage"); id != 85 || r.Register("SayText") != 3 {
		t.Error("Register")
	}
}

func TestDecodeUnknownAndUndecoded(t *testing.T) {
	r := NewRegistry(TF2)
	if _, _, err := r.Decode(200, bitbuf.NewReader(nil)); err == nil {
		t.Error("unknown index accepted")
	}
	name, msg, err := r.Decode(0, bitbuf.NewReader([]byte{1}))
	if name != "Geiger" || msg != nil || err != nil {
		t.Errorf("undecoded message: %s %v %v", name, msg, err)
	}
	r.RegisterDecoder("Geiger", func(br *bitbuf.Reader) (any, error) { return br.ReadUint32(), nil })
	if _, _, err := r.Decode(0, bitbuf.NewReader([]byte{1})); err == nil {
		t.Error("overflowing decoder must fail")
	}
}

func TestDefaultDecoders(t *testing.T) {
	r := NewRegistry(TF2)
	DefaultDecoders(r)

	type tc struct {
		name  string
		write func(w *bitbuf.Writer)
		want  any
	}
	cases := []tc{
		{"SayText", func(w *bitbuf.Writer) { w.WriteUint8(3); w.WriteString("hi"); w.WriteUint8(1) },
			SayText{Client: 3, Text: "hi", Chat: true}},
		{"SayText2", func(w *bitbuf.Writer) {
			w.WriteUint8(2)
			w.WriteUint8(1)
			w.WriteString("#TF_Chat_All")
			w.WriteString("Scout")
			w.WriteString("hello")
			w.WriteString("")
			w.WriteString("")
		}, SayText2{Client: 2, Chat: true, MsgName: "#TF_Chat_All", Params: [4]string{"Scout", "hello"}}},
		{"TextMsg", func(w *bitbuf.Writer) {
			w.WriteUint8(PrintCenter)
			w.WriteString("#Game_will_restart")
			w.WriteString("1")
			w.WriteString("")
			w.WriteString("")
			w.WriteString("")
		}, TextMsg{Dest: PrintCenter, Name: "#Game_will_restart", Params: [4]string{"1"}}},
		{"HintText", func(w *bitbuf.Writer) { w.WriteString("#Hint") }, HintText{Text: "#Hint"}},
		{"KeyHintText", func(w *bitbuf.Writer) { w.WriteUint8(1); w.WriteString("press E") }, KeyHintText{Messages: []string{"press E"}}},
		{"HudText", func(w *bitbuf.Writer) { w.WriteString("GAMETITLE") }, HudText{Text: "GAMETITLE"}},
		{"HudMsg", func(w *bitbuf.Writer) {
			w.WriteUint8(2)
			w.WriteFloat32(-1)
			w.WriteFloat32(0.25)
			for _, b := range []uint8{1, 2, 3, 4, 5, 6, 7, 8, 9} {
				w.WriteUint8(b)
			}
			for _, f := range []float32{0.1, 0.2, 3, 0.5} {
				w.WriteFloat32(f)
			}
			w.WriteString("text")
		}, HudMsg{Channel: 2, X: -1, Y: 0.25, Color1: [4]uint8{1, 2, 3, 4}, Color2: [4]uint8{5, 6, 7, 8}, Effect: 9,
			FadeIn: 0.1, FadeOut: 0.2, HoldTime: 3, FxTime: 0.5, Message: "text"}},
		{"Shake", func(w *bitbuf.Writer) { w.WriteUint8(0); w.WriteFloat32(4); w.WriteFloat32(10); w.WriteFloat32(1.5) },
			Shake{Command: 0, Amplitude: 4, Frequency: 10, Duration: 1.5}},
		{"Fade", func(w *bitbuf.Writer) {
			w.WriteUint16(512)
			w.WriteUint16(1024)
			w.WriteUint16(0x11)
			for _, b := range []uint8{10, 20, 30, 255} {
				w.WriteUint8(b)
			}
		}, Fade{Duration: 512, HoldTime: 1024, Flags: 0x11, Color: [4]uint8{10, 20, 30, 255}}},
		{"ResetHUD", func(w *bitbuf.Writer) { w.WriteUint8(0) }, ResetHUD{}},
		{"Train", func(w *bitbuf.Writer) { w.WriteUint8(4) }, Train{Pos: 4}},
		{"Geiger", func(w *bitbuf.Writer) { w.WriteUint8(7) }, Geiger{Range: 7}},
		{"VGUIMenu", func(w *bitbuf.Writer) {
			w.WriteString("info")
			w.WriteUint8(1)
			w.WriteUint8(2)
			w.WriteString("title")
			w.WriteString("MOTD")
			w.WriteString("type")
			w.WriteString("1")
		}, VGUIMenu{Name: "info", Show: true, Keys: []KeyValue{{"title", "MOTD"}, {"type", "1"}}}},
		{"ShowMenu", func(w *bitbuf.Writer) { w.WriteUint16(0x3ff); w.WriteInt8(-1); w.WriteUint8(0); w.WriteString("menu") },
			ShowMenu{ValidSlots: 0x3ff, DisplayTime: -1, Text: "menu"}},
		{"CloseCaption", func(w *bitbuf.Writer) { w.WriteString("tok"); w.WriteInt16(25); w.WriteUint8(3) },
			CloseCaption{Token: "tok", Duration: 25, Flags: 3}},
		{"VoiceSubtitle", func(w *bitbuf.Writer) { w.WriteUint8(1); w.WriteUint8(2); w.WriteUint8(3) },
			VoiceSubtitle{Client: 1, Menu: 2, Item: 3}},
		{"Rumble", func(w *bitbuf.Writer) { w.WriteUint8(1); w.WriteUint8(2); w.WriteUint8(3) },
			Rumble{Waveform: 1, Data: 2, Flags: 3}},
	}
	for _, c := range cases {
		id, ok := r.ID(c.name)
		if !ok {
			t.Fatalf("%s missing from TF2 list", c.name)
		}
		w := bitbuf.NewWriter(0)
		c.write(w)
		br := w.Reader()
		name, msg, err := r.Decode(id, br)
		if err != nil || name != c.name {
			t.Errorf("%s: %v (%s)", c.name, err, name)
			continue
		}
		if !reflect.DeepEqual(msg, c.want) {
			t.Errorf("%s = %+v, want %+v", c.name, msg, c.want)
		}
		if br.BitsLeft() != 0 {
			t.Errorf("%s: %d bits left", c.name, br.BitsLeft())
		}
	}

	// Decoders are only registered for messages the game has.
	d := NewRegistry(DODS)
	DefaultDecoders(d)
	if _, ok := d.decoders["SayText2"]; ok {
		t.Error("SayText2 decoder registered for a game without SayText2")
	}
}
