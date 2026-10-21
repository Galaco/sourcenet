// Package client implements a Source Engine game client: the out-of-band
// connection handshake, the netchannel signon sequence, string tables, game
// events, user messages, entity updates and user commands.
//
// The client is designed to be embedded in a game loop. It never calls back
// from other goroutines: all processing happens inside Update, which the host
// calls every frame with the current time. Update drains received datagrams,
// handles timers (connection retries, timeouts, command rate) and sends
// packets. What happened is reported as Events and through per-message
// handlers registered with Handle.
//
//	c := client.New(client.Config{Game: game.TF2(), Name: "player"})
//	if err := c.Connect("127.0.0.1:27015"); err != nil { ... }
//	for running {
//		c.Update(time.Now())
//		for _, ev := range c.PollEvents() { ... }
//	}
package client

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"sort"
	"strconv"
	"time"

	"github.com/galaco/sourcenet/bitbuf"
	"github.com/galaco/sourcenet/connectionless"
	"github.com/galaco/sourcenet/datatable"
	"github.com/galaco/sourcenet/entity"
	"github.com/galaco/sourcenet/game"
	"github.com/galaco/sourcenet/gameevent"
	"github.com/galaco/sourcenet/message"
	"github.com/galaco/sourcenet/netchan"
	"github.com/galaco/sourcenet/packet"
	"github.com/galaco/sourcenet/protocol"
	"github.com/galaco/sourcenet/stringtable"
	"github.com/galaco/sourcenet/usercmd"
	"github.com/galaco/sourcenet/usermessage"
)

// State is the connection phase.
type State int

// Connection phases.
const (
	StateDisconnected State = iota
	StateQuerying           // fetching the server's product version
	StateChallenging        // exchanging challenge and connect packets
	StateConnected          // netchannel established, see SignonState
)

func (s State) String() string {
	switch s {
	case StateDisconnected:
		return "disconnected"
	case StateQuerying:
		return "querying"
	case StateChallenging:
		return "challenging"
	case StateConnected:
		return "connected"
	}
	return "unknown"
}

// ErrNotConnected is returned when sending without a netchannel.
var ErrNotConnected = errors.New("client: not connected")

// ErrNoSendTables is reported (as an EventError) when the game profile has
// no send tables or CRC. Retail servers (sv_sendtables 0) will disconnect
// the client with "Server uses different class tables"; set
// Profile.DataTables, e.g. with game.DataTablesFromDemo.
var ErrNoSendTables = errors.New("client: game profile has no send tables; the server will reject the client's send table CRC")

// Client is a connection to one game server. It is not safe for concurrent
// use; call all methods from the goroutine driving Update.
type Client struct {
	cfg      Config
	profile  *game.Profile
	msgCtx   *message.Context
	registry *message.Registry
	userMsgs *usermessage.Registry
	codec    usercmd.Codec
	auth     Authenticator
	log      *slog.Logger
	userInfo map[string]string

	transport Transport
	decoder   *packet.Decoder
	encoder   *packet.Encoder
	now       time.Time

	state           State
	signon          int
	productVersion  string
	clientChallenge uint32
	serverChallenge uint32
	attempts        int
	lastAttempt     time.Time
	queryChallenge  uint32

	ch           *netchan.Channel
	serverInfo   *message.SvcServerInfo
	serverCount  int32
	serverTick   int32
	clientTick   int32
	deltaTick    int32
	tickInterval time.Duration
	tickClock    time.Time
	nextCmdTime  time.Time

	lastOutgoingCommand int32
	chokedCommands      int
	commands            [cmdBackup]usercmd.UserCmd
	fileRequest         uint32

	stringTables *stringtable.Container
	gameEvents   *gameevent.List
	recvTables   *datatable.Tables
	tables       *datatable.Tables
	entities     *entity.State
	replicated   map[string]string
	paused       bool
	viewEntity   int

	handlers map[int][]func(message.Message)
	events   []Event

	// playback is set when replaying a demo: nothing is sent and signon
	// ordering checks are skipped, as during engine demo playback.
	playback   bool
	demoTables *datatable.Tables
}

// New returns a disconnected client.
func New(cfg Config) *Client {
	p := cfg.Game
	if p == nil {
		p = game.Generic()
	}
	c := &Client{
		cfg:      cfg,
		profile:  p,
		msgCtx:   p.MessageContext(),
		registry: p.ServerMessages(),
		userMsgs: p.UserMessageRegistry(),
		codec:    p.UserCmdCodec(),
		auth:     cfg.Auth,
		log:      cfg.Logger,
		handlers: make(map[int][]func(message.Message)),
	}
	if c.auth == nil {
		c.auth = OfflineAuth{}
	}
	if c.log == nil {
		c.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if c.cfg.RetryInterval <= 0 {
		c.cfg.RetryInterval = DefaultRetryInterval
	}
	if c.cfg.MaxRetries <= 0 {
		c.cfg.MaxRetries = DefaultMaxRetries
	}
	if c.cfg.Timeout <= 0 {
		c.cfg.Timeout = DefaultTimeout
	}
	c.userInfo = defaultUserInfo()
	for k, v := range cfg.UserInfo {
		c.userInfo[k] = v
	}
	name := cfg.Name
	if name == "" {
		name = "unnamed"
	}
	c.userInfo["name"] = name
	if cfg.MaxRoutable > 0 {
		// Tell the server how large our datagrams may be before splitting.
		c.userInfo["net_maxroutable"] = strconv.Itoa(cfg.MaxRoutable)
	}
	if len(c.cfg.FriendsName) >= protocol.MaxPlayerNameLength {
		c.cfg.FriendsName = c.cfg.FriendsName[:protocol.MaxPlayerNameLength-1]
	}
	return c
}

// Connect dials a server over UDP and starts connecting. Progress is driven
// by Update.
func (c *Client) Connect(addr string) error {
	t, err := DialUDP(addr)
	if err != nil {
		return err
	}
	c.ConnectTransport(t)
	return nil
}

// ConnectTransport starts connecting over an existing transport, which the
// client owns from now on.
func (c *Client) ConnectTransport(t Transport) {
	if c.transport != nil {
		c.Disconnect("Connecting to another server")
	}
	c.transport = t
	c.decoder = packet.NewDecoder()
	c.encoder = &packet.Encoder{MaxRoutable: c.cfg.MaxRoutable, Compress: c.cfg.CompressPackets}
	c.clientChallenge = (rand.Uint32N(0x1000) << 16) | rand.Uint32N(0x10000)
	c.attempts = 0
	c.lastAttempt = time.Time{}
	c.productVersion = c.cfg.ProductVersion
	if c.productVersion == "" {
		c.productVersion = c.profile.ProductVersion
	}
	c.signon = protocol.SignonChallenge
	if c.productVersion == "" {
		c.state = StateQuerying
	} else {
		c.state = StateChallenging
	}
}

// Handle registers fn to be called with every server message of msgType,
// after the client's own processing. Handlers run inside Update.
func (c *Client) Handle(msgType int, fn func(message.Message)) {
	c.handlers[msgType] = append(c.handlers[msgType], fn)
}

// PollEvents returns and clears the queued events.
func (c *Client) PollEvents() []Event {
	ev := c.events
	c.events = nil
	return ev
}

func (c *Client) emit(e Event) {
	if c.cfg.OnEvent != nil {
		c.cfg.OnEvent(e)
	}
	c.events = append(c.events, e)
}

// State returns the connection phase.
func (c *Client) State() State { return c.state }

// SignonState returns the signon state (protocol.Signon*).
func (c *Client) SignonState() int { return c.signon }

// Active reports whether the client is fully connected and in game.
func (c *Client) Active() bool { return c.signon == protocol.SignonFull }

// ServerInfo returns the current map's server info, or nil.
func (c *Client) ServerInfo() *message.SvcServerInfo { return c.serverInfo }

// PlayerSlot returns the local player's slot; the player entity index is
// PlayerSlot()+1.
func (c *Client) PlayerSlot() int {
	if c.serverInfo == nil {
		return -1
	}
	return int(c.serverInfo.PlayerSlot)
}

// ServerTick returns the latest server tick.
func (c *Client) ServerTick() int32 { return c.serverTick }

// TickInterval returns the server tick interval.
func (c *Client) TickInterval() time.Duration { return c.tickInterval }

// Paused reports whether the server is paused.
func (c *Client) Paused() bool { return c.paused }

// ViewEntity returns the entity the view is attached to.
func (c *Client) ViewEntity() int { return c.viewEntity }

// StringTables returns the connection's string tables.
func (c *Client) StringTables() *stringtable.Container { return c.stringTables }

// GameEvents returns the game event descriptors, or nil before they arrive.
func (c *Client) GameEvents() *gameevent.List { return c.gameEvents }

// Entities returns the entity state, or nil when entities are not decoded
// (no data tables available).
func (c *Client) Entities() *entity.State { return c.entities }

// Tables returns the data tables in use, or nil.
func (c *Client) Tables() *datatable.Tables { return c.tables }

// ReplicatedConVars returns the convars replicated by the server.
func (c *Client) ReplicatedConVars() map[string]string { return c.replicated }

// Channel returns the netchannel (for statistics), or nil.
func (c *Client) Channel() *netchan.Channel { return c.ch }

// Players returns the parsed userinfo table entries keyed by player slot.
func (c *Client) Players() map[int]stringtable.PlayerInfo {
	out := make(map[int]stringtable.PlayerInfo)
	if c.stringTables == nil {
		return out
	}
	t := c.stringTables.ByName(protocol.TableUserInfo)
	if t == nil {
		return out
	}
	for i, e := range t.Entries {
		if len(e.UserData) == 0 {
			continue
		}
		if pi, err := stringtable.ParsePlayerInfo(e.UserData, c.profile.ReplayEnabled); err == nil {
			out[i] = pi
		}
	}
	return out
}

// Update processes received datagrams, runs timers and sends packets. Call
// it every frame. It returns an error only when the transport failed.
func (c *Client) Update(now time.Time) error {
	c.now = now
	if c.transport == nil {
		return nil
	}
	for c.transport != nil {
		d, ok := c.transport.Poll()
		if !ok {
			break
		}
		c.receive(now, d)
	}
	if ut, ok := c.transport.(*UDPTransport); ok && ut.Err() != nil {
		err := ut.Err()
		c.shutdown(fmt.Sprintf("Network error: %v", err), false)
		return err
	}

	switch c.state {
	case StateQuerying:
		c.updateQuery(now)
	case StateChallenging:
		c.updateChallenge(now)
	case StateConnected:
		c.updateConnected(now)
	}
	return nil
}

// Disconnect ends the connection, telling the server why.
func (c *Client) Disconnect(reason string) {
	if c.state == StateDisconnected {
		return
	}
	if reason == "" {
		reason = "Disconnect by user."
	}
	if c.ch != nil {
		c.ch.Clear()
		_ = message.Write(c.ch.Unreliable(), &message.NetDisconnect{Reason: reason}, c.msgCtx)
		c.transmit(c.now)
	}
	c.shutdown(reason, false)
}

func (c *Client) shutdown(reason string, rejected bool) {
	if c.state == StateDisconnected {
		return
	}
	c.auth.Cancel()
	c.state = StateDisconnected
	c.signon = protocol.SignonNone
	c.ch = nil
	if c.transport != nil {
		_ = c.transport.Close()
		c.transport = nil
	}
	c.emit(EventDisconnected{Reason: reason, Rejected: rejected})
}

func (c *Client) sendConnectionless(p connectionless.Packet) {
	if err := c.transport.Send(p.Marshal()); err != nil {
		c.log.Debug("send connectionless", "err", err)
	}
}

func (c *Client) receive(now time.Time, datagram []byte) {
	pkt, ok, err := c.decoder.Decode(now, datagram)
	if err != nil {
		c.log.Debug("bad datagram", "err", err)
		return
	}
	if !ok {
		return
	}
	if connectionless.IsConnectionless(pkt) {
		c.handleConnectionless(now, pkt)
		return
	}
	if c.ch == nil {
		return
	}
	in, err := c.ch.ProcessPacket(now, pkt)
	if in == nil {
		if err != nil && !errors.Is(err, netchan.ErrStalePacket) && !errors.Is(err, netchan.ErrFragmentOrder) {
			c.log.Debug("packet rejected", "err", err)
		}
		return
	}
	c.processIncoming(in)
	if err != nil {
		// A reliable block was lost after the peer considered it delivered;
		// the session state can no longer be trusted.
		c.shutdown(fmt.Sprintf("Reliable data corrupted: %v", err), false)
	}
}

// processIncoming dispatches a packet's payloads in engine order.
func (c *Client) processIncoming(in *netchan.Incoming) {
	for _, r := range in.Reliable {
		if !c.processMessages(r) {
			return
		}
	}
	for _, f := range in.Files {
		c.emit(EventFileReceived{File: f})
	}
	if in.Unreliable != nil {
		c.processMessages(in.Unreliable)
	}
}

func (c *Client) processMessages(r *bitbuf.Reader) bool {
	msgs, err := message.ReadAll(r, c.registry, c.msgCtx)
	for _, m := range msgs {
		if c.state == StateDisconnected {
			return false
		}
		if !c.dispatch(m) {
			return false
		}
	}
	if err != nil {
		c.emit(EventError{Err: err})
		return false
	}
	return true
}

func (c *Client) dispatch(m message.Message) bool {
	ok := c.handleMessage(m)
	for _, fn := range c.handlers[m.Type()] {
		fn(m)
	}
	return ok
}

// --- out of band handshake ---

func (c *Client) updateQuery(now time.Time) {
	if !c.lastAttempt.IsZero() && now.Sub(c.lastAttempt) < queryRetryInterval {
		return
	}
	if c.attempts >= queryAttempts {
		c.log.Info("server did not answer A2S_INFO, connecting without product version")
		c.productVersion = productVersionAny
		c.state = StateChallenging
		c.attempts = 0
		c.lastAttempt = time.Time{}
		c.updateChallenge(now)
		return
	}
	c.attempts++
	c.lastAttempt = now
	req := &connectionless.InfoRequest{}
	if c.queryChallenge != 0 {
		req.Challenge, req.HasChallenge = c.queryChallenge, true
	}
	c.sendConnectionless(req)
}

func (c *Client) updateChallenge(now time.Time) {
	if !c.lastAttempt.IsZero() && now.Sub(c.lastAttempt) < c.cfg.RetryInterval {
		return
	}
	if c.attempts >= c.cfg.MaxRetries {
		c.shutdown(fmt.Sprintf("Connection failed after %d retries", c.cfg.MaxRetries), false)
		return
	}
	c.attempts++
	c.lastAttempt = now
	c.emit(EventConnecting{Attempt: c.attempts})
	c.sendConnectionless(&connectionless.GetChallenge{ClientChallenge: c.clientChallenge})
}

func (c *Client) handleConnectionless(now time.Time, pkt []byte) {
	p, err := connectionless.Parse(pkt)
	if err != nil {
		c.log.Debug("bad connectionless packet", "err", err)
		return
	}
	switch p := p.(type) {
	case *connectionless.QueryChallenge:
		if c.state == StateQuerying {
			c.queryChallenge = p.Challenge
			c.lastAttempt = time.Time{}
			c.updateQuery(now)
		}
	case *connectionless.Info:
		if c.state == StateQuerying {
			c.productVersion = p.Version
			if c.productVersion == "" {
				c.productVersion = productVersionAny
			}
			c.state = StateChallenging
			c.attempts = 0
			c.lastAttempt = time.Time{}
			c.updateChallenge(now)
		}
	case *connectionless.Challenge:
		if c.state != StateChallenging || p.ClientChallenge != c.clientChallenge {
			return
		}
		c.sendConnect(now, p)
	case *connectionless.Accept:
		if c.state != StateChallenging || p.Legacy || p.ClientChallenge != c.clientChallenge {
			return
		}
		c.fullConnect(now)
	case *connectionless.Reject:
		if c.state != StateChallenging || p.ClientChallenge != c.clientChallenge {
			return
		}
		c.shutdown(p.Reason, true)
	}
}

func (c *Client) sendConnect(now time.Time, chal *connectionless.Challenge) {
	creds, err := c.auth.Credentials(AuthRequest{
		Protocol:      chal.AuthProtocol,
		ServerSteamID: chal.ServerSteamID,
		Secure:        chal.Secure,
		ServerAddr:    c.transport.RemoteAddr(),
	})
	if err != nil {
		c.shutdown(err.Error(), false)
		return
	}
	c.serverChallenge = chal.ServerChallenge
	c.lastAttempt = now
	c.sendConnectionless(&connectionless.Connect{
		Protocol:        int32(c.msgCtx.ProtocolVersion),
		AuthProtocol:    creds.Protocol,
		ServerChallenge: chal.ServerChallenge,
		ClientChallenge: c.clientChallenge,
		Name:            c.userInfo["name"],
		Password:        c.cfg.Password,
		ProductVersion:  c.productVersion,
		CDKeyHash:       creds.CDKeyHash,
		SteamCookie:     creds.SteamCookie,
	})
}

// fullConnect establishes the netchannel (CBaseClientState::FullConnect).
func (c *Client) fullConnect(now time.Time) {
	rate, _ := strconv.Atoi(c.userInfo["rate"])
	c.ch = netchan.New(now, netchan.Config{Challenge: c.serverChallenge, Rate: rate})
	c.state = StateConnected
	c.deltaTick = -1
	c.nextCmdTime = now
	c.stringTables = stringtable.NewContainer()
	c.replicated = make(map[string]string)
	c.emit(EventConnected{})
	c.setSignonState(protocol.SignonConnected, -1)
}

// --- signon ---

// setSignonState applies a signon state announced by the server (or reached
// locally) and acknowledges it (CClientState::SetSignonState).
func (c *Client) setSignonState(state int, count int32) bool {
	if state < protocol.SignonNone || state > protocol.SignonChangeLevel {
		c.emit(EventError{Err: fmt.Errorf("client: received signon %d", state)})
		return false
	}
	if c.playback {
		c.signon = state
		c.emit(EventSignonState{State: state})
		return true
	}
	if state > protocol.SignonConnected && state <= c.signon {
		c.emit(EventError{Err: fmt.Errorf("client: received signon %d when at %d", state, c.signon)})
		return false
	}
	if count != c.serverCount && count != -1 && c.serverCount != -1 && c.serverInfo != nil {
		c.emit(EventError{Err: fmt.Errorf("client: received wrong spawn count %d when at %d", count, c.serverCount)})
		return false
	}
	c.signon = state
	c.emit(EventSignonState{State: state})

	switch state {
	case protocol.SignonConnected:
		c.ch.Clear()
		c.ch.SetTimeout(netchan.SignonTimeout)
		c.sendReliable(&message.NetSetConVar{ConVars: c.userInfoConVars()})
	case protocol.SignonNew:
		crc, err := c.profile.TableCRC()
		if err != nil {
			c.emit(EventError{Err: err})
		} else if crc == 0 {
			c.emit(EventError{Err: ErrNoSendTables})
		}
		c.sendReliable(&message.ClcClientInfo{
			ServerCount:  c.serverCount,
			SendTableCRC: crc,
			FriendsID:    c.cfg.FriendsID,
			FriendsName:  c.cfg.FriendsName,
		})
		count = c.serverCount
	case protocol.SignonFull:
		c.ch.SetTimeout(c.cfg.Timeout)
	case protocol.SignonChangeLevel:
		c.ch.SetTimeout(netchan.SignonTimeout)
	}
	if state >= protocol.SignonConnected {
		c.sendReliable(&message.NetSignonState{State: uint8(state), SpawnCount: count})
	}
	return true
}

func (c *Client) userInfoConVars() []message.ConVar {
	keys := make([]string, 0, len(c.userInfo))
	for k := range c.userInfo {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	vars := make([]message.ConVar, 0, len(keys))
	for _, k := range keys {
		vars = append(vars, message.ConVar{Name: k, Value: c.userInfo[k]})
	}
	return vars
}

// --- sending ---

func (c *Client) sendReliable(m message.Message) {
	if err := c.Send(m, true); err != nil {
		c.emit(EventError{Err: err})
	}
}

// Send queues a message for the server on the reliable or unreliable stream.
// It goes out with the next packet.
func (c *Client) Send(m message.Message, reliable bool) error {
	if c.playback {
		return nil
	}
	if c.ch == nil {
		return ErrNotConnected
	}
	w := c.ch.Unreliable()
	if reliable {
		w = c.ch.Reliable()
	}
	return message.Write(w, m, c.msgCtx)
}

// ErrCommandTooLong is returned for console commands the server would
// reject; it drops clients sending commands of 1024 bytes or more.
var ErrCommandTooLong = errors.New("client: command longer than 1023 bytes")

// SendStringCmd sends a console command to the server, e.g. "say hello" or
// "jointeam red".
func (c *Client) SendStringCmd(cmd string) error {
	if len(cmd) > 1023 {
		return ErrCommandTooLong
	}
	return c.Send(&message.NetStringCmd{Command: cmd}, true)
}

// SetUserInfo changes a userinfo convar and tells the server when connected.
// Servers only track convars announced when the connection was made, so set
// new names in Config.UserInfo before connecting.
func (c *Client) SetUserInfo(name, value string) error {
	c.userInfo[name] = value
	if c.ch == nil {
		return nil
	}
	return c.Send(&message.NetSetConVar{ConVars: []message.ConVar{{Name: name, Value: value}}}, true)
}

// RequestFile asks the server to send a file over the netchannel file
// stream. The result arrives as EventFileReceived or EventFileDenied.
func (c *Client) RequestFile(name string) (uint32, error) {
	c.fileRequest++
	id := c.fileRequest
	return id, c.Send(&message.NetFile{TransferID: id, Filename: name, Requested: true}, true)
}

// ForceFullUpdate asks the server for a full (non delta) entity update.
func (c *Client) ForceFullUpdate() {
	c.deltaTick = -1
}

func (c *Client) transmit(now time.Time) int32 {
	seq := c.ch.OutSequence()
	pkt, err := c.ch.Transmit(now)
	if err != nil {
		if errors.Is(err, netchan.ErrReliableOverflow) {
			c.shutdown("Reliable channel overflowed", false)
		}
		return seq
	}
	datagrams, err := c.encoder.Encode(pkt)
	if err != nil {
		c.emit(EventError{Err: err})
		return seq
	}
	for _, d := range datagrams {
		if err := c.transport.Send(d); err != nil {
			c.log.Debug("send", "err", err)
		}
	}
	return seq
}

// --- connected loop ---

func (c *Client) updateConnected(now time.Time) {
	if c.ch.IsTimedOut(now) {
		c.shutdown("Server connection timed out", false)
		return
	}
	if c.signon < protocol.SignonFull {
		// During signon the engine sends 5 packets a second to acknowledge
		// reliable data.
		if !now.Before(c.nextCmdTime) && c.ch.CanPacket(now) {
			c.lastOutgoingCommand = c.transmit(now)
			c.nextCmdTime = now.Add(signonSendInterval)
		}
		return
	}
	c.runTicks(now)
}

// runTicks creates one user command per elapsed server tick and sends them
// at the command rate (CL_Move).
func (c *Client) runTicks(now time.Time) {
	interval := c.tickInterval
	if interval <= 0 {
		interval = 15 * time.Millisecond
	}
	if c.tickClock.IsZero() || now.Sub(c.tickClock) > interval*maxTicksPerUpdate {
		c.tickClock = now.Add(-interval)
	}
	ticks := 0
	for !c.tickClock.Add(interval).After(now) && ticks < maxTicksPerUpdate {
		c.tickClock = c.tickClock.Add(interval)
		ticks++
	}
	for i := 0; i < ticks; i++ {
		c.clientTick++
		final := i == ticks-1
		c.createMove()
		send := final && !now.Before(c.nextCmdTime) && c.ch.CanPacket(now)
		if !send {
			c.ch.SetChoked()
			c.chokedCommands++
			continue
		}
		c.sendMove(now)
	}
}

func (c *Client) cmdRate() int {
	if v, err := strconv.Atoi(c.userInfo["cl_cmdrate"]); err == nil && v > 0 {
		return v
	}
	return DefaultCmdRate
}

func (c *Client) createMove() {
	n := c.lastOutgoingCommand + int32(c.chokedCommands) + 1
	cmd := usercmd.UserCmd{CommandNumber: n, TickCount: c.clientTick}
	if c.cfg.CreateMove != nil {
		c.cfg.CreateMove(&cmd)
	}
	cmd.CommandNumber = n
	cmd.RandomSeed = usercmd.RandomSeed(n)
	c.commands[int(n)%cmdBackup] = cmd
}

func (c *Client) command(n int32) usercmd.UserCmd {
	if n <= 0 {
		return usercmd.UserCmd{}
	}
	cmd := c.commands[int(n)%cmdBackup]
	if cmd.CommandNumber != n {
		return usercmd.UserCmd{}
	}
	return cmd
}

func (c *Client) sendMove(now time.Time) {
	next := c.lastOutgoingCommand + int32(c.chokedCommands) + 1
	newCmds, backup := usercmd.ClampCounts(1+c.chokedCommands, 2)
	total := newCmds + backup
	history := make([]usercmd.UserCmd, 0, total)
	for n := next - int32(total) + 1; n <= next; n++ {
		history = append(history, c.command(n))
	}
	data, nbits := usercmd.BuildMove(c.codec, history, newCmds, backup)
	_ = message.Write(c.ch.Unreliable(), &message.ClcMove{
		NewCommands:    newCmds,
		BackupCommands: backup,
		Data:           message.Bits{Data: data, N: nbits},
	}, c.msgCtx)
	_ = message.Write(c.ch.Unreliable(), &message.NetTick{
		Tick:          c.deltaTick,
		HostFrameTime: float32(c.tickInterval.Seconds()),
	}, c.msgCtx)

	c.lastOutgoingCommand = c.transmit(now)
	c.chokedCommands = 0

	cmdInterval := time.Second / time.Duration(c.cmdRate())
	maxDelta := min(c.tickInterval, cmdInterval)
	delta := now.Sub(c.nextCmdTime)
	if delta < 0 {
		delta = 0
	}
	if delta > maxDelta {
		delta = maxDelta
	}
	c.nextCmdTime = now.Add(cmdInterval - delta)
}
