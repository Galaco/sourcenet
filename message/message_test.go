package message

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/protocol"
)

// payload returns a deterministic n-bit payload whose unused high bits are zero.
func payload(n int) Bits {
	data := make([]byte, bitbuf.BitsToBytes(n))
	for i := range data {
		data[i] = byte(i*37 + 11)
	}
	if rem := n & 7; rem != 0 {
		data[len(data)-1] &= byte(1<<uint(rem) - 1)
	}
	return Bits{Data: data, N: n}
}

func md5Of(seed byte) (out [16]byte) {
	for i := range out {
		out[i] = seed + byte(i)
	}
	return
}

func serverMessages() []Message {
	return []Message{
		&NetNOP{},
		&NetDisconnect{Reason: "Kicked by Console"},
		&NetFile{TransferID: 42, Filename: "maps/ctf_2fort.bsp", Requested: true},
		&NetFile{TransferID: 7, Filename: "logo.dat"},
		&NetTick{Tick: 123456, HostFrameTime: 0.5, HostFrameTimeStdDev: 0.25},
		&NetStringCmd{Command: "say hello"},
		&NetSetConVar{ConVars: []ConVar{{"sv_cheats", "0"}, {"mp_timelimit", "30"}}},
		&NetSetConVar{ConVars: []ConVar{}},
		&NetSignonState{State: protocol.SignonPrespawn, SpawnCount: 3},
		&SvcPrint{Text: "Team Fortress\nMap: ctf_2fort\n"},
		&SvcServerInfo{Protocol: 24, ServerCount: 9, IsDedicated: true, ClientCRC: -1, MaxClasses: 348,
			MapMD5: md5Of(1), PlayerSlot: 4, MaxClients: 24, TickInterval: 0.015, OS: 'l',
			GameDir: "tf", MapName: "ctf_2fort", SkyName: "sky_tf2_04", HostName: "My Server", IsReplay: true},
		&SvcSendTable{NeedsDecoder: true, Data: payload(1234)},
		&SvcClassInfo{CreateOnClient: true, NumServerClasses: 348},
		&SvcClassInfo{NumServerClasses: 3, Classes: []ClassInfo{
			{0, "CAI_BaseNPC", "DT_AI_BaseNPC"}, {1, "CBaseEntity", "DT_BaseEntity"}, {2, "CWorld", "DT_World"}}},
		&SvcSetPause{Paused: true},
		&SvcCreateStringTable{TableName: "downloadables", MaxEntries: 8192, NumEntries: 17, DataCompressed: true, Data: payload(999)},
		&SvcCreateStringTable{TableName: "modelprecache", IsFilenames: true, MaxEntries: 1024, NumEntries: 1024,
			UserDataFixedSize: true, UserDataSize: 1, UserDataSizeBits: 2, Data: payload(77)},
		&SvcUpdateStringTable{TableID: 7, ChangedEntries: 1, Data: payload(33)},
		&SvcUpdateStringTable{TableID: 31, ChangedEntries: 400, Data: payload(5000)},
		&SvcVoiceInit{Codec: "vaudio_celt", Quality: 255, SampleRate: 22050},
		&SvcVoiceData{FromClient: 3, Proximity: true, Data: payload(700)},
		&SvcSounds{Reliable: true, NumSounds: 1, Data: payload(200)},
		&SvcSounds{NumSounds: 12, Data: payload(3000)},
		&SvcSetView{EntityIndex: 5},
		&SvcFixAngle{Relative: true, Angle: [3]float32{90, 180, 45}},
		&SvcCrosshairAngle{Angle: [3]float32{0, 270, 22.5}},
		&SvcBSPDecal{Pos: [3]float32{-128.5, 64.03125, 0}, DecalTextureIndex: 300, EntityIndex: 12, ModelIndex: 4000, LowPriority: true},
		&SvcBSPDecal{Pos: [3]float32{1, 2, 3}, DecalTextureIndex: 1},
		&SvcUserMessage{MsgType: 4, Data: payload(2047)},
		&SvcEntityMessage{EntityIndex: 2047, ClassID: 511, Data: payload(15)},
		&SvcGameEvent{Data: payload(120)},
		&SvcPacketEntities{MaxEntries: 2048 - 1, IsDelta: true, DeltaFrom: 1000, Baseline: 1, UpdatedEntries: 30, UpdateBaseline: true, Data: payload(9000)},
		&SvcPacketEntities{MaxEntries: 300, DeltaFrom: -1, UpdatedEntries: 300, Data: payload(40000)},
		&SvcTempEntities{NumEntries: 255, Data: payload(800)},
		&SvcPrefetch{SoundIndex: 16000},
		&SvcMenu{DialogType: 2, Data: []byte{0, 'm', 'e', 'n', 'u', 0, 8, 8}},
		&SvcGameEventList{NumEvents: 511, Data: payload(60000)},
		&SvcGetCvarValue{Cookie: -12345, CvarName: "cl_interp"},
		&SvcCmdKeyValues{Data: []byte{0, 'k', 'v', 0, 8}},
		&SvcSetPauseTimed{Paused: true, ExpireTime: 12.5},
	}
}

func clientMessages() []Message {
	return []Message{
		&NetTick{Tick: 77, HostFrameTime: 0.125},
		&NetSignonState{State: protocol.SignonFull, SpawnCount: -1},
		&ClcClientInfo{ServerCount: 9, SendTableCRC: 0xdeadbeef, FriendsID: 12345, FriendsName: "player",
			CustomFiles: [protocol.MaxCustomFiles]uint32{0, 0xabcdef01, 0, 5}, IsReplay: true},
		&ClcMove{NewCommands: 15, BackupCommands: 7, Data: payload(333)},
		&ClcVoiceData{Data: payload(64)},
		&ClcBaselineAck{BaselineTick: 900, BaselineNr: 1},
		&ClcListenEvents{Mask: [protocol.MaxEventNumber / 32]uint32{1, 0, 0xffffffff, 7}},
		&ClcRespondCvarValue{Cookie: 99, Status: CvarQueryNotFound, CvarName: "fps_max", CvarValue: ""},
		&ClcRespondCvarValue{Cookie: -1, Status: -8, CvarName: "x", CvarValue: "y"},
		&ClcFileCRCCheck{PathID: "GAME", Filename: "materials/brick/wall.vmt", MD5: md5Of(9), CRCIOs: 1, FileHashType: 2,
			FileLen: 3, PackFileNumber: 4, PackFileID: 5, FileFraction: 6},
		&ClcFileCRCCheck{PathID: "custom_path", Filename: "cfg/autoexec.cfg", MD5: md5Of(3)},
		&ClcSaveReplay{Filename: "replay.dmx", StartSendByte: 9, PostDeathRecordTime: 5.5},
		&ClcCmdKeyValues{Data: []byte{0, 'a', 0, 8}},
		&ClcFileMD5Check{PathID: "MOD", Filename: "models/player/scout.mdl", MD5: md5Of(200)},
		&ClcFileMD5Check{PathID: "other", Filename: "readme.txt", MD5: md5Of(1)},
	}
}

func roundTrip(t *testing.T, reg *Registry, ctx *Context, m Message, offset int) {
	t.Helper()
	w := bitbuf.NewWriter(0)
	w.WriteBits(0x15, offset)
	if err := Write(w, m, ctx); err != nil {
		t.Fatalf("%s: encode: %v", m.Name(), err)
	}
	end := w.BitsWritten()
	r := w.Reader()
	r.ReadBits(offset)
	got, err := Read(r, reg, ctx)
	if err != nil {
		t.Fatalf("%s: decode: %v", m.Name(), err)
	}
	if r.Pos() != end {
		t.Fatalf("%s: decoder consumed %d bits, encoder wrote %d", m.Name(), r.Pos()-offset, end-offset)
	}
	if !reflect.DeepEqual(got, m) {
		t.Fatalf("%s (offset %d): round trip mismatch\n got  %#v\n want %#v", m.Name(), offset, got, m)
	}
}

func TestRoundTripAllMessages(t *testing.T) {
	for _, offset := range []int{0, 3, 7} {
		for _, m := range serverMessages() {
			roundTrip(t, ServerToClient(), nil, m, offset)
		}
		for _, m := range clientMessages() {
			roundTrip(t, ClientToServer(), nil, m, offset)
		}
	}
}

func TestRegistriesCoverAllTypes(t *testing.T) {
	check := func(reg *Registry, ids []int) {
		for _, id := range ids {
			m, ok := reg.New(id)
			if !ok {
				t.Errorf("type %d not registered", id)
				continue
			}
			if m.Type() != id {
				t.Errorf("type %d constructs %s with type %d", id, m.Name(), m.Type())
			}
			if s, ok := m.(interface{ String() string }); !ok || !strings.HasPrefix(s.String(), m.Name()) {
				t.Errorf("%s: missing or malformed String()", m.Name())
			}
		}
	}
	net := []int{0, 1, 2, 3, 4, 5, 6}
	check(ServerToClient(), append(append([]int{}, net...), 7, 8, 9, 10, 11, 12, 13, 14, 15, 17, 18, 19, 20, 21, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33))
	check(ClientToServer(), append(append([]int{}, net...), 8, 9, 10, 11, 12, 13, 14, 15, 16, 17))
	if _, ok := ServerToClient().New(16); ok {
		t.Error("svc type 16 (HLTV) must not be registered")
	}
	if m, _ := ClientToServer().New(8); m.Name() != "clc_ClientInfo" {
		t.Errorf("client registry type 8 is %s", m.Name())
	}
}

func TestGoldenSignonState(t *testing.T) {
	w := bitbuf.NewWriter(0)
	if err := Write(w, &NetSignonState{State: 3, SpawnCount: 2}, nil); err != nil {
		t.Fatal(err)
	}
	if want := []byte{0xc6, 0x80, 0, 0, 0, 0}; !bytes.Equal(w.Bytes(), want) || w.BitsWritten() != 46 {
		t.Fatalf("got % x (%d bits), want % x (46 bits)", w.Bytes(), w.BitsWritten(), want)
	}
}

// assertSame compares an encoding against a layout written field by field.
func assertSame(t *testing.T, name string, got, want *bitbuf.Writer) {
	t.Helper()
	if got.BitsWritten() != want.BitsWritten() || !bytes.Equal(got.Bytes(), want.Bytes()) {
		t.Fatalf("%s:\n got  % x (%d bits)\n want % x (%d bits)", name, got.Bytes(), got.BitsWritten(), want.Bytes(), want.BitsWritten())
	}
}

func TestGoldenServerInfo(t *testing.T) {
	msg := &SvcServerInfo{Protocol: 24, ServerCount: 5, IsDedicated: true, ClientCRC: -1, MaxClasses: 300,
		MapMD5: md5Of(0x10), PlayerSlot: 2, MaxClients: 32, TickInterval: 0.015, OS: 'w',
		GameDir: "cstrike", MapName: "de_dust2", SkyName: "sky_dust", HostName: "srv", IsReplay: true}
	layout := func(replay bool, protocolVersion int16, crc bool) *bitbuf.Writer {
		w := bitbuf.NewWriter(0)
		w.WriteBits(protocol.SvcServerInfo, 6)
		w.WriteInt16(protocolVersion)
		w.WriteInt32(5)
		w.WriteBit(false)
		w.WriteBit(true)
		w.WriteUint32(0xffffffff)
		w.WriteUint16(300)
		if crc {
			w.WriteUint32(0xcafef00d)
		} else {
			md5 := md5Of(0x10)
			w.WriteBytes(md5[:])
		}
		w.WriteUint8(2)
		w.WriteUint8(32)
		w.WriteFloat32(0.015)
		w.WriteUint8('w')
		w.WriteString("cstrike")
		w.WriteString("de_dust2")
		w.WriteString("sky_dust")
		w.WriteString("srv")
		if replay {
			w.WriteBit(true)
		}
		return w
	}
	for _, replay := range []bool{true, false} {
		got := bitbuf.NewWriter(0)
		if err := Write(got, msg, &Context{ProtocolVersion: 24, ReplayEnabled: replay}); err != nil {
			t.Fatal(err)
		}
		assertSame(t, "svc_ServerInfo", got, layout(replay, 24, false))
	}
	// Replay engines only read the flag from protocol 16 onwards.
	got := bitbuf.NewWriter(0)
	if err := Write(got, msg, &Context{ProtocolVersion: 15, ReplayEnabled: true}); err != nil {
		t.Fatal(err)
	}
	assertSame(t, "svc_ServerInfo v15", got, layout(false, 24, false))

	old := *msg
	old.Protocol = 17
	old.MapCRC = 0xcafef00d
	old.MapMD5 = [16]byte{}
	old.IsReplay = false
	got = bitbuf.NewWriter(0)
	if err := Write(got, &old, &Context{ProtocolVersion: 17}); err != nil {
		t.Fatal(err)
	}
	assertSame(t, "svc_ServerInfo v17", got, layout(false, 17, true))
	roundTrip(t, ServerToClient(), &Context{ProtocolVersion: 17}, &old, 1)
}

func TestGoldenPacketEntitiesHeader(t *testing.T) {
	msg := &SvcPacketEntities{MaxEntries: 1000, IsDelta: true, DeltaFrom: 4000, Baseline: 1, UpdatedEntries: 12, UpdateBaseline: true, Data: payload(10)}
	want := bitbuf.NewWriter(0)
	want.WriteBits(protocol.SvcPacketEntities, 6)
	want.WriteBits(1000, 11)
	want.WriteBit(true)
	want.WriteInt32(4000)
	want.WriteBits(1, 1)
	want.WriteBits(12, 11)
	want.WriteBits(10, 20)
	want.WriteBit(true)
	want.WriteBitsFromBytes(msg.Data.Data, 10)
	got := bitbuf.NewWriter(0)
	if err := Write(got, msg, nil); err != nil {
		t.Fatal(err)
	}
	assertSame(t, "svc_PacketEntities", got, want)

	full := &SvcPacketEntities{MaxEntries: 5, DeltaFrom: -1, UpdatedEntries: 5}
	r := bitbuf.NewWriter(0)
	if err := Write(r, full, nil); err != nil {
		t.Fatal(err)
	}
	// type + max + delta flag + baseline + updated + length + update flag
	if n := r.BitsWritten(); n != 6+11+1+1+11+20+1 {
		t.Fatalf("full update header is %d bits", n)
	}
}

func TestGoldenClientInfo(t *testing.T) {
	msg := &ClcClientInfo{ServerCount: 3, SendTableCRC: 0x01020304, FriendsID: 77, FriendsName: "bob",
		CustomFiles: [4]uint32{0, 0x11223344, 0, 0}, IsReplay: true}
	layout := func(replay bool) *bitbuf.Writer {
		w := bitbuf.NewWriter(0)
		w.WriteBits(protocol.ClcClientInfo, 6)
		w.WriteInt32(3)
		w.WriteUint32(0x01020304)
		w.WriteBit(false)
		w.WriteUint32(77)
		w.WriteString("bob")
		w.WriteBit(false)
		w.WriteBit(true)
		w.WriteUint32(0x11223344)
		w.WriteBit(false)
		w.WriteBit(false)
		if replay {
			w.WriteBit(true)
		}
		return w
	}
	for _, replay := range []bool{true, false} {
		got := bitbuf.NewWriter(0)
		if err := Write(got, msg, &Context{ProtocolVersion: 24, ReplayEnabled: replay}); err != nil {
			t.Fatal(err)
		}
		assertSame(t, "clc_ClientInfo", got, layout(replay))
	}
}

func TestCreateStringTableVersions(t *testing.T) {
	base := SvcCreateStringTable{TableName: "userinfo", MaxEntries: 128, NumEntries: 3, Data: payload(500)}

	enc := func(m Message, ctx *Context) *bitbuf.Writer {
		w := bitbuf.NewWriter(0)
		if err := Write(w, m, ctx); err != nil {
			t.Fatal(err)
		}
		return w
	}
	headerBits := func(w *bitbuf.Writer) int { return w.BitsWritten() - 500 }

	v24 := enc(&base, nil)
	// type, name, max entries, log2(128)+1 entry bits, varint(500)=2 bytes, fixed flag, compressed flag
	if got, want := headerBits(v24), 6+8*9+16+8+16+1+1; got != want {
		t.Errorf("v24 header %d bits, want %d", got, want)
	}
	v23 := enc(&base, &Context{ProtocolVersion: 23})
	if got, want := headerBits(v23), 6+8*9+16+8+20+1+1; got != want {
		t.Errorf("v23 header %d bits, want %d", got, want)
	}
	v14 := enc(&base, &Context{ProtocolVersion: 14})
	if got, want := headerBits(v14), 6+8*9+16+8+20+1; got != want {
		t.Errorf("v14 header %d bits, want %d", got, want)
	}
	roundTrip(t, ServerToClient(), &Context{ProtocolVersion: 23}, &base, 2)
	roundTrip(t, ServerToClient(), &Context{ProtocolVersion: 14}, &base, 5)

	files := base
	files.IsFilenames = true
	w := enc(&files, nil)
	r := w.Reader()
	r.ReadBits(6)
	if c := r.ReadUint8(); c != ':' {
		t.Fatalf("filename table must be prefixed with ':', got %q", c)
	}
	if s := r.ReadString(); s != "userinfo" {
		t.Fatalf("name %q", s)
	}
}

func TestTempEntitiesAndPrefetchVersions(t *testing.T) {
	te := &SvcTempEntities{NumEntries: 2, Data: payload(100)}
	w := bitbuf.NewWriter(0)
	if err := Write(w, te, &Context{ProtocolVersion: 23}); err != nil {
		t.Fatal(err)
	}
	if n := w.BitsWritten(); n != 6+8+17+100 {
		t.Fatalf("v23 temp entities %d bits", n)
	}
	roundTrip(t, ServerToClient(), &Context{ProtocolVersion: 23}, te, 1)

	pf := &SvcPrefetch{SoundIndex: 8000}
	for _, c := range []struct {
		version, bits int
	}{{24, 14}, {23, 14}, {22, 13}} {
		w := bitbuf.NewWriter(0)
		if err := Write(w, pf, &Context{ProtocolVersion: c.version}); err != nil {
			t.Fatal(err)
		}
		if w.BitsWritten() != 6+c.bits {
			t.Errorf("prefetch v%d: %d bits", c.version, w.BitsWritten())
		}
		roundTrip(t, ServerToClient(), &Context{ProtocolVersion: c.version}, pf, 0)
	}
	if err := Write(bitbuf.NewWriter(0), &SvcPrefetch{SoundIndex: 1 << 13}, &Context{ProtocolVersion: 22}); err == nil {
		t.Error("expected range error for 13-bit sound index")
	}
}

func TestVoiceInitLegacy(t *testing.T) {
	legacy := func(codec string, quality uint8) *SvcVoiceInit {
		w := bitbuf.NewWriter(0)
		w.WriteString(codec)
		w.WriteUint8(quality)
		m := &SvcVoiceInit{}
		if err := m.Decode(w.Reader(), nil); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if m := legacy("VAUDIO_CELT", 5); m.SampleRate != 22050 || m.Quality != 5 {
		t.Errorf("celt legacy: %+v", m)
	}
	if m := legacy("vaudio_speex", 3); m.SampleRate != 11025 {
		t.Errorf("speex legacy: %+v", m)
	}
	// Legacy messages re-encode without a sample rate.
	roundTrip(t, ServerToClient(), nil, &SvcVoiceInit{Codec: "vaudio_speex", Quality: 3, SampleRate: 11025}, 0)

	// The zero Quality encodes the current layout.
	w := bitbuf.NewWriter(0)
	if err := (&SvcVoiceInit{Codec: "steam", SampleRate: 24000}).Encode(w, nil); err != nil {
		t.Fatal(err)
	}
	m := &SvcVoiceInit{}
	if err := m.Decode(w.Reader(), nil); err != nil || m.Quality != 255 || m.SampleRate != 24000 {
		t.Fatalf("current layout: %+v %v", m, err)
	}
}

func TestFileCRCCheckEncoding(t *testing.T) {
	m := &ClcFileCRCCheck{PathID: "MOD", Filename: "Sounds\\vo\\hello.wav", MD5: md5Of(4)}
	w := bitbuf.NewWriter(0)
	if err := m.Encode(w, nil); err != nil {
		t.Fatal(err)
	}
	r := w.Reader()
	r.ReadBit()
	if code := r.ReadBits(2); code != 2 {
		t.Fatalf("path id code %d", code)
	}
	if code := r.ReadBits(3); code != 3 {
		t.Fatalf("prefix code %d", code)
	}
	if v := r.ReadUint8(); v != 1 {
		t.Fatalf("version marker %d", v)
	}
	if s := r.ReadString(); s != "vo\\hello.wav" {
		t.Fatalf("filename %q", s)
	}
	got := &ClcFileCRCCheck{}
	if err := got.Decode(w.Reader(), nil); err != nil {
		t.Fatal(err)
	}
	if got.Filename != "sounds/vo\\hello.wav" || got.PathID != "MOD" {
		t.Fatalf("decoded %+v", got)
	}

	// Old layout: no version marker, CRC + IO CRC + hash type.
	old := bitbuf.NewWriter(0)
	old.WriteBit(false)
	old.WriteBits(0, 2)
	old.WriteString("DEFAULT_WRITE_PATH")
	old.WriteBits(1, 3)
	old.WriteString("brick/wall.vmt")
	old.WriteUint32(0xaaaa)
	old.WriteUint32(0xbbbb)
	old.WriteUint32(1)
	dec := &ClcFileCRCCheck{}
	if err := dec.Decode(old.Reader(), nil); err != nil {
		t.Fatal(err)
	}
	want := &ClcFileCRCCheck{PathID: "DEFAULT_WRITE_PATH", Filename: "materials/brick/wall.vmt", CRCIOs: 0xbbbb,
		FileHashType: 1, Legacy: true, LegacyCRC: 0xaaaa}
	if !reflect.DeepEqual(dec, want) {
		t.Fatalf("legacy decode\n got  %+v\n want %+v", dec, want)
	}

	bad := bitbuf.NewWriter(0)
	bad.WriteBit(false)
	bad.WriteBits(3, 2) // only codes 1 and 2 are defined
	if err := (&ClcFileCRCCheck{}).Decode(bad.Reader(), nil); err == nil {
		t.Fatal("expected invalid path id error")
	}
}

func TestSaveReplayKeepsFourBits(t *testing.T) {
	w := bitbuf.NewWriter(0)
	if err := Write(w, &ClcSaveReplay{Filename: "a", StartSendByte: 0x1234}, nil); err != nil {
		t.Fatal(err)
	}
	if n := w.BitsWritten(); n != 6+16+4+32 {
		t.Fatalf("%d bits", n)
	}
	m, err := Read(w.Reader(), ClientToServer(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.(*ClcSaveReplay).StartSendByte; got != 4 {
		t.Fatalf("start byte %d, want low 4 bits", got)
	}
}

func TestListenEventsMask(t *testing.T) {
	m := &ClcListenEvents{}
	m.Set(0)
	m.Set(33)
	m.Set(511)
	m.Set(512) // ignored
	m.Set(-1)  // ignored
	if !m.IsSet(0) || !m.IsSet(33) || !m.IsSet(511) || m.IsSet(1) || m.IsSet(512) {
		t.Fatal("mask bits")
	}
	if m.Mask[0] != 1 || m.Mask[1] != 2 || m.Mask[15] != 1<<31 {
		t.Fatalf("mask words %x", m.Mask)
	}
}

func TestReadAllStream(t *testing.T) {
	msgs := []Message{
		&NetTick{Tick: 5},
		&SvcPrint{Text: "hi"},
		&NetNOP{},
		&SvcPacketEntities{MaxEntries: 10, IsDelta: true, DeltaFrom: 4, UpdatedEntries: 1, Data: payload(77)},
		&NetSignonState{State: 6, SpawnCount: 2},
	}
	w := bitbuf.NewWriter(0)
	for _, m := range msgs {
		if err := Write(w, m, nil); err != nil {
			t.Fatal(err)
		}
	}
	w.WriteBits(0x1f, 5) // trailing pad bits, too short for a message
	got, err := ReadAll(w.Reader(), ServerToClient(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []Message{msgs[0], msgs[1], msgs[3], msgs[4]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestReadAllErrors(t *testing.T) {
	w := bitbuf.NewWriter(0)
	if err := Write(w, &SvcPrint{Text: "first"}, nil); err != nil {
		t.Fatal(err)
	}
	w.WriteBits(16, 6) // svc_HLTV is not registered
	w.WriteUint32(0)
	got, err := ReadAll(w.Reader(), ServerToClient(), nil)
	var unknown *UnknownMessageError
	if !errors.As(err, &unknown) || unknown.Type != 16 {
		t.Fatalf("expected unknown message 16, got %v", err)
	}
	if len(got) != 1 || got[0].(*SvcPrint).Text != "first" {
		t.Fatalf("messages before the error: %v", got)
	}

	// Truncated payload.
	w = bitbuf.NewWriter(0)
	if err := Write(w, &SvcUserMessage{MsgType: 1, Data: payload(500)}, nil); err != nil {
		t.Fatal(err)
	}
	r := bitbuf.NewReaderBits(w.Bytes(), w.BitsWritten()-10)
	if _, err := ReadAll(r, ServerToClient(), nil); !errors.Is(err, bitbuf.ErrOverflow) || !strings.Contains(err.Error(), "svc_UserMessage") {
		t.Fatalf("expected overflow in svc_UserMessage, got %v", err)
	}

	// Truncated fixed fields.
	w = bitbuf.NewWriter(0)
	if err := Write(w, &SvcServerInfo{Protocol: 24, GameDir: "tf"}, nil); err != nil {
		t.Fatal(err)
	}
	r = bitbuf.NewReaderBits(w.Bytes(), 60)
	if _, err := Read(r, ServerToClient(), nil); !errors.Is(err, bitbuf.ErrOverflow) {
		t.Fatalf("expected overflow, got %v", err)
	}
}

func TestEncodeRangeErrors(t *testing.T) {
	cases := []Message{
		&SvcUserMessage{Data: payload(2048)},
		&SvcGameEvent{Data: payload(4096)},
		&SvcSetView{EntityIndex: 2048},
		&SvcPacketEntities{Baseline: 2},
		&NetSetConVar{ConVars: make([]ConVar, 256)},
		&SvcSounds{Reliable: true, Data: payload(256)},
		&SvcUpdateStringTable{TableID: 32},
		&ClcMove{NewCommands: 16},
		&ClcRespondCvarValue{Status: 8},
		&SvcCmdKeyValues{},
		&SvcUserMessage{Data: Bits{Data: []byte{1}, N: 9}},
		&SvcCreateStringTable{MaxEntries: 4, NumEntries: 8},
	}
	for _, m := range cases {
		if err := Write(bitbuf.NewWriter(0), m, nil); err == nil {
			t.Errorf("%s: expected encode error for %+v", m.Name(), m)
		}
	}
}

func TestKeyValuesLengthValidation(t *testing.T) {
	w := bitbuf.NewWriter(0)
	w.WriteInt32(100)
	w.WriteBytes([]byte{1, 2, 3})
	if err := (&SvcCmdKeyValues{}).Decode(w.Reader(), nil); err == nil {
		t.Fatal("expected length error")
	}
}

func TestRegistryCloneIsolation(t *testing.T) {
	orig := ServerToClient()
	clone := orig.Clone()
	type custom struct{ SvcPrint }
	clone.Register(protocol.SvcPrint, func() Message { return &custom{} })
	clone.Register(40, func() Message { return &NetNOP{} })
	if m, _ := orig.New(protocol.SvcPrint); reflect.TypeOf(m) != reflect.TypeOf(&SvcPrint{}) {
		t.Fatalf("original registry changed: %T", m)
	}
	if _, ok := orig.New(40); ok {
		t.Fatal("original registry gained a type")
	}
	if m, _ := clone.New(protocol.SvcPrint); reflect.TypeOf(m) != reflect.TypeOf(&custom{}) {
		t.Fatalf("clone did not take override: %T", m)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for out of range type")
		}
	}()
	clone.Register(64, func() Message { return &NetNOP{} })
}

func TestTickQuantisation(t *testing.T) {
	w := bitbuf.NewWriter(0)
	if err := (&NetTick{Tick: 1, HostFrameTime: 10, HostFrameTimeStdDev: -1}).Encode(w, nil); err != nil {
		t.Fatal(err)
	}
	m := &NetTick{}
	if err := m.Decode(w.Reader(), nil); err != nil {
		t.Fatal(err)
	}
	if m.HostFrameTime != float32(65535)/100000 || m.HostFrameTimeStdDev != 0 {
		t.Fatalf("clamping: %+v", m)
	}
}

func TestBitsFromWriter(t *testing.T) {
	w := bitbuf.NewWriter(0)
	w.WriteBits(0x5, 3)
	w.WriteUint8(0xff)
	b := BitsFromWriter(w)
	w.Reset()
	w.WriteUint32(0)
	if b.N != 11 || b.Reader().ReadBits(3) != 5 {
		t.Fatalf("bits %+v", b)
	}
}
