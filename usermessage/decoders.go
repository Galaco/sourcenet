package usermessage

import "github.com/galaco/sourcenet/bitbuf"

// Message destinations used by TextMsg (HUD_PRINT*).
const (
	PrintNotify  = 1
	PrintConsole = 2
	PrintTalk    = 3
	PrintCenter  = 4
)

// SayText is a chat line (UTIL_SayTextFilter).
type SayText struct {
	Client uint8 // entity index of the speaker, 0 for the server
	Text   string
	Chat   bool // raw chat text rather than a localisation token
}

// SayText2 is a localised chat line (UTIL_SayText2Filter). MsgName is
// usually a localisation token such as "#TF_Chat_All"; Params[0] is the
// player name and Params[1] the chat text.
type SayText2 struct {
	Client  uint8
	Chat    bool
	MsgName string
	Params  [4]string
}

// TextMsg is a localisable text message (UTIL_ClientPrintFilter).
type TextMsg struct {
	Dest   uint8 // PrintNotify, PrintConsole, PrintTalk or PrintCenter
	Name   string
	Params [4]string
}

// HintText shows a hint.
type HintText struct {
	Text string
}

// KeyHintText shows key hints; the client only displays messages with
// exactly one string.
type KeyHintText struct {
	Messages []string
}

// HudText adds a titles.txt message to the HUD.
type HudText struct {
	Text string
}

// HudMsg displays a positioned, coloured HUD message (UTIL_HudMessage).
type HudMsg struct {
	Channel  uint8
	X, Y     float32
	Color1   [4]uint8
	Color2   [4]uint8
	Effect   uint8
	FadeIn   float32
	FadeOut  float32
	HoldTime float32
	FxTime   float32
	Message  string
}

// Shake starts or adjusts a screen shake.
type Shake struct {
	Command   uint8 // SHAKE_START, SHAKE_STOP, SHAKE_AMPLITUDE, SHAKE_FREQUENCY, ...
	Amplitude float32
	Frequency float32
	Duration  float32
}

// Fade fades the screen to or from a colour. Duration and HoldTime are
// fixed point with 9 fractional bits (SCREENFADE_FRACBITS).
type Fade struct {
	Duration uint16
	HoldTime uint16
	Flags    uint16
	Color    [4]uint8
}

// ResetHUD is sent on every respawn.
type ResetHUD struct {
	Value uint8
}

// Train updates the train HUD position.
type Train struct {
	Pos uint8
}

// Geiger updates the geiger counter range (scaled down by 4).
type Geiger struct {
	Range uint8
}

// VGUIMenu shows or hides a viewport panel.
type VGUIMenu struct {
	Name string
	Show bool
	Keys []KeyValue
}

// KeyValue is a key/value pair carried by VGUIMenu.
type KeyValue struct {
	Key, Value string
}

// ShowMenu shows a radio-style menu; long menus arrive in several parts with
// NeedMore set.
type ShowMenu struct {
	ValidSlots  uint16
	DisplayTime int8
	NeedMore    bool
	Text        string
}

// CloseCaption shows a caption token for Duration tenths of a second.
type CloseCaption struct {
	Token    string
	Duration int16
	Flags    uint8
}

// VoiceSubtitle shows a voice command subtitle.
type VoiceSubtitle struct {
	Client uint8
	Menu   uint8
	Item   uint8
}

// Rumble drives controller rumble.
type Rumble struct {
	Waveform uint8
	Data     uint8
	Flags    uint8
}

// DefaultDecoders registers decoders for the user messages shared by the
// stock multiplayer games. Only messages present in r are affected.
func DefaultDecoders(r *Registry) {
	for name, d := range defaultDecoders {
		if _, ok := r.ID(name); ok {
			r.RegisterDecoder(name, d)
		}
	}
}

var defaultDecoders = map[string]Decoder{
	"SayText": func(r *bitbuf.Reader) (any, error) {
		return SayText{Client: r.ReadUint8(), Text: r.ReadString(), Chat: r.ReadUint8() != 0}, nil
	},
	"SayText2": func(r *bitbuf.Reader) (any, error) {
		m := SayText2{Client: r.ReadUint8(), Chat: r.ReadUint8() != 0, MsgName: r.ReadString()}
		for i := range m.Params {
			m.Params[i] = r.ReadString()
		}
		return m, nil
	},
	"TextMsg": func(r *bitbuf.Reader) (any, error) {
		m := TextMsg{Dest: r.ReadUint8(), Name: r.ReadString()}
		for i := range m.Params {
			m.Params[i] = r.ReadString()
		}
		return m, nil
	},
	"HintText": func(r *bitbuf.Reader) (any, error) {
		return HintText{Text: r.ReadString()}, nil
	},
	"KeyHintText": func(r *bitbuf.Reader) (any, error) {
		n := int(r.ReadUint8())
		m := KeyHintText{}
		for i := 0; i < n && !r.Overflowed(); i++ {
			m.Messages = append(m.Messages, r.ReadString())
		}
		return m, nil
	},
	"HudText": func(r *bitbuf.Reader) (any, error) {
		return HudText{Text: r.ReadString()}, nil
	},
	"HudMsg": func(r *bitbuf.Reader) (any, error) {
		m := HudMsg{Channel: r.ReadUint8(), X: r.ReadFloat32(), Y: r.ReadFloat32()}
		for i := range m.Color1 {
			m.Color1[i] = r.ReadUint8()
		}
		for i := range m.Color2 {
			m.Color2[i] = r.ReadUint8()
		}
		m.Effect = r.ReadUint8()
		m.FadeIn = r.ReadFloat32()
		m.FadeOut = r.ReadFloat32()
		m.HoldTime = r.ReadFloat32()
		m.FxTime = r.ReadFloat32()
		m.Message = r.ReadString()
		return m, nil
	},
	"Shake": func(r *bitbuf.Reader) (any, error) {
		return Shake{Command: r.ReadUint8(), Amplitude: r.ReadFloat32(), Frequency: r.ReadFloat32(), Duration: r.ReadFloat32()}, nil
	},
	"Fade": func(r *bitbuf.Reader) (any, error) {
		m := Fade{Duration: r.ReadUint16(), HoldTime: r.ReadUint16(), Flags: r.ReadUint16()}
		for i := range m.Color {
			m.Color[i] = r.ReadUint8()
		}
		return m, nil
	},
	"ResetHUD": func(r *bitbuf.Reader) (any, error) {
		var m ResetHUD
		if r.BitsLeft() >= 8 {
			m.Value = r.ReadUint8()
		}
		return m, nil
	},
	"Train": func(r *bitbuf.Reader) (any, error) {
		return Train{Pos: r.ReadUint8()}, nil
	},
	"Geiger": func(r *bitbuf.Reader) (any, error) {
		return Geiger{Range: r.ReadUint8()}, nil
	},
	"VGUIMenu": func(r *bitbuf.Reader) (any, error) {
		m := VGUIMenu{Name: r.ReadString(), Show: r.ReadUint8() != 0}
		n := int(r.ReadUint8())
		for i := 0; i < n && !r.Overflowed(); i++ {
			m.Keys = append(m.Keys, KeyValue{Key: r.ReadString(), Value: r.ReadString()})
		}
		return m, nil
	},
	"ShowMenu": func(r *bitbuf.Reader) (any, error) {
		m := ShowMenu{ValidSlots: r.ReadUint16(), DisplayTime: r.ReadInt8(), NeedMore: r.ReadUint8() != 0}
		if m.ValidSlots != 0 {
			m.Text = r.ReadString()
		}
		return m, nil
	},
	"CloseCaption": func(r *bitbuf.Reader) (any, error) {
		return CloseCaption{Token: r.ReadString(), Duration: r.ReadInt16(), Flags: r.ReadUint8()}, nil
	},
	"VoiceSubtitle": func(r *bitbuf.Reader) (any, error) {
		return VoiceSubtitle{Client: r.ReadUint8(), Menu: r.ReadUint8(), Item: r.ReadUint8()}, nil
	},
	"Rumble": func(r *bitbuf.Reader) (any, error) {
		return Rumble{Waveform: r.ReadUint8(), Data: r.ReadUint8(), Flags: r.ReadUint8()}, nil
	},
}
