package client

import (
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"time"

	"github.com/galaco/sourcenet/datatable"
	"github.com/galaco/sourcenet/entity"
	"github.com/galaco/sourcenet/gameevent"
	"github.com/galaco/sourcenet/message"
	"github.com/galaco/sourcenet/protocol"
	"github.com/galaco/sourcenet/stringtable"
)

// Engine tick interval limits (MINIMUM_TICK_INTERVAL / MAXIMUM_TICK_INTERVAL).
const (
	minTickInterval = 0.001
	maxTickInterval = 0.1
)

// handleMessage applies a server message to the client state. It returns
// false when the rest of the packet must be skipped, as the engine does when
// a Process* handler fails.
func (c *Client) handleMessage(m message.Message) bool {
	switch m := m.(type) {
	case *message.NetDisconnect:
		c.shutdown(m.Reason, false)
		return false
	case *message.NetFile:
		if m.Requested {
			// The server wants one of our files (custom sprays); we have none.
			c.sendReliable(&message.NetFile{TransferID: m.TransferID, Filename: m.Filename, Requested: false})
		} else {
			c.emit(EventFileDenied{Name: m.Filename, TransferID: m.TransferID})
		}
	case *message.NetTick:
		c.serverTick = m.Tick
		c.clientTick = m.Tick
	case *message.NetStringCmd:
		c.emit(EventStringCmd{Command: m.Command})
	case *message.NetSetConVar:
		for _, cv := range m.ConVars {
			c.replicated[cv.Name] = cv.Value
		}
		c.emit(EventConVars{ConVars: m.ConVars})
	case *message.NetSignonState:
		ok := c.setSignonState(int(m.State), m.SpawnCount)
		if int(m.State) == protocol.SignonConnected && !c.playback {
			// The server restarted the signon (level change or Reconnect)
			// and the channel was cleared: the rest of this packet belongs
			// to the old session (CNetChan m_bClearedDuringProcessing).
			return false
		}
		return ok
	case *message.SvcPrint:
		c.emit(EventPrint{Text: m.Text})
	case *message.SvcServerInfo:
		return c.handleServerInfo(m)
	case *message.SvcSendTable:
		t, err := datatable.ReadSendTable(m.Data.Reader())
		if err != nil {
			c.emit(EventError{Err: fmt.Errorf("client: send table: %w", err)})
			return false
		}
		t.NeedsDecoder = m.NeedsDecoder
		c.recvTables.AddTable(t)
	case *message.SvcClassInfo:
		return c.handleClassInfo(m)
	case *message.SvcSetPause:
		c.paused = m.Paused
	case *message.SvcSetPauseTimed:
		c.paused = m.Paused
	case *message.SvcCreateStringTable:
		return c.handleCreateStringTable(m)
	case *message.SvcUpdateStringTable:
		t, changed, err := c.stringTables.Update(m.TableID, m.ChangedEntries, m.Data.Reader())
		if err != nil {
			c.emit(EventError{Err: fmt.Errorf("client: update string table %d: %w", m.TableID, err)})
			return false
		}
		c.stringTableChanged(t, false, changed)
	case *message.SvcSetView:
		c.viewEntity = m.EntityIndex
	case *message.SvcGameEventList:
		l, err := gameevent.ParseList(m.Data.Reader(), m.NumEvents)
		if err != nil {
			c.emit(EventError{Err: fmt.Errorf("client: game event list: %w", err)})
			return false
		}
		c.gameEvents = l
		c.sendReliable(&message.ClcListenEvents{Mask: l.ListenMask(c.cfg.ListenEvents...)})
	case *message.SvcGameEvent:
		if c.gameEvents == nil {
			break
		}
		ev, err := c.gameEvents.Decode(m.Data.Reader())
		if err != nil {
			c.emit(EventError{Err: fmt.Errorf("client: game event: %w", err)})
			break
		}
		c.emit(EventGameEvent{Event: ev})
	case *message.SvcUserMessage:
		name, decoded, err := c.userMsgs.Decode(int(m.MsgType), m.Data.Reader())
		if err != nil {
			c.emit(EventError{Err: fmt.Errorf("client: user message %d (%s): %w", m.MsgType, name, err)})
		}
		c.emit(EventUserMessage{ID: int(m.MsgType), Name: name, Message: decoded, Data: m.Data})
	case *message.SvcPacketEntities:
		return c.handlePacketEntities(m)
	case *message.SvcTempEntities:
		if c.entities == nil {
			break
		}
		te, err := c.entities.DecodeTempEntities(m.NumEntries, m.Data.Reader())
		if err != nil {
			c.emit(EventError{Err: fmt.Errorf("client: temp entities: %w", err)})
			break
		}
		c.emit(EventTempEntities{Entities: te})
	case *message.SvcGetCvarValue:
		resp := &message.ClcRespondCvarValue{Cookie: m.Cookie, CvarName: m.CvarName}
		if v, ok := c.userInfo[m.CvarName]; ok {
			resp.Status, resp.CvarValue = message.CvarQueryValueIntact, v
		} else if v, ok := c.replicated[m.CvarName]; ok {
			resp.Status, resp.CvarValue = message.CvarQueryValueIntact, v
		} else {
			resp.Status = message.CvarQueryNotFound
		}
		c.sendReliable(resp)
	}
	return true
}

func (c *Client) handleServerInfo(m *message.SvcServerInfo) bool {
	if int(m.Protocol) != c.msgCtx.ProtocolVersion {
		c.shutdown(fmt.Sprintf("Server returned version %d, expected %d", m.Protocol, c.msgCtx.ProtocolVersion), false)
		return false
	}
	// A remote server must be multiplayer; a spoofed single player server
	// info would disable the engine's security checks.
	minClients := uint8(2)
	if c.playback {
		minClients = 1
	}
	if m.MaxClients < minClients || int(m.MaxClasses) < 1 || int(m.MaxClasses) > protocol.MaxServerClasses {
		c.shutdown("Bad server info", false)
		return false
	}
	if m.TickInterval < minTickInterval || m.TickInterval > maxTickInterval {
		c.shutdown(fmt.Sprintf("Interval_per_tick %f out of range", m.TickInterval), false)
		return false
	}
	c.serverInfo = m
	c.serverCount = m.ServerCount
	c.tickInterval = secondsToDuration(m.TickInterval)
	c.viewEntity = int(m.PlayerSlot) + 1
	c.deltaTick = -1
	c.stringTables.Reset()
	c.gameEvents = nil
	c.recvTables = datatable.NewTables()
	c.tables = nil
	c.entities = nil
	c.emit(EventServerInfo{Info: m})
	return true
}

func (c *Client) handleClassInfo(m *message.SvcClassInfo) bool {
	var tables *datatable.Tables
	if m.CreateOnClient {
		t, err := c.clientTables()
		if err != nil {
			c.emit(EventError{Err: err})
		}
		if t == nil {
			if c.playback {
				// Demos may record their data tables after the class info.
				return true
			}
			c.log.Warn("server expects client-side data tables but the game profile has none; entities will not be decoded")
			return true
		}
		tables = t
	} else {
		classes := make([]datatable.ClassInfo, len(m.Classes))
		for i, cl := range m.Classes {
			classes[i] = datatable.ClassInfo{ID: cl.ClassID, Name: cl.ClassName, DataTableName: cl.DataTableName}
		}
		c.recvTables.SetClasses(classes)
		if err := c.recvTables.Link(); err != nil {
			c.emit(EventError{Err: fmt.Errorf("client: link data tables: %w", err)})
			return false
		}
		tables = c.recvTables
	}
	c.tables = tables
	c.entities = c.newEntityState(tables)
	return true
}

// newEntityState creates entity state using the class ID width the server
// announced in svc_ServerInfo (Q_log2(MaxClasses)+1).
func (c *Client) newEntityState(tables *datatable.Tables) *entity.State {
	st := entity.NewState(tables, c.instanceBaseline)
	if c.serverInfo != nil && c.serverInfo.MaxClasses > 0 {
		st.SetClassBits(bits.Len(uint(c.serverInfo.MaxClasses)))
	}
	return st
}

// clientTables returns the tables used when the server expects the client
// to know them: a demo's recorded tables during playback, otherwise the
// game profile's.
func (c *Client) clientTables() (*datatable.Tables, error) {
	if c.demoTables != nil {
		return c.demoTables, nil
	}
	return c.profile.Tables()
}

// instanceBaseline looks up a class's baseline in the instancebaseline table.
func (c *Client) instanceBaseline(classID int) ([]byte, bool) {
	t := c.stringTables.ByName(protocol.TableInstanceBaseline)
	if t == nil {
		return nil, false
	}
	i := t.Find(strconv.Itoa(classID))
	if i < 0 || t.Entries[i].UserData == nil {
		return nil, false
	}
	return t.Entries[i].UserData, true
}

func (c *Client) handleCreateStringTable(m *message.SvcCreateStringTable) bool {
	t, err := c.stringTables.Create(stringtable.CreateParams{
		Name:              m.TableName,
		MaxEntries:        int(m.MaxEntries),
		NumEntries:        m.NumEntries,
		UserDataFixedSize: m.UserDataFixedSize,
		UserDataSize:      m.UserDataSize,
		UserDataSizeBits:  m.UserDataSizeBits,
		IsFilenames:       m.IsFilenames,
		DataCompressed:    m.DataCompressed,
	}, m.Data.Reader())
	if err != nil {
		c.emit(EventError{Err: fmt.Errorf("client: create string table %s: %w", m.TableName, err)})
		return false
	}
	changed := make([]int, len(t.Entries))
	for i := range changed {
		changed[i] = i
	}
	c.stringTableChanged(t, true, changed)
	return true
}

func (c *Client) stringTableChanged(t *stringtable.Table, created bool, changed []int) {
	if t.Name == protocol.TableInstanceBaseline && c.entities != nil {
		c.entities.InvalidateBaselines()
	}
	c.emit(EventStringTable{Table: t, Created: created, Changed: changed})
}

func (c *Client) handlePacketEntities(m *message.SvcPacketEntities) bool {
	if c.playback && c.signon < protocol.SignonSpawn {
		// SourceTV demos do not record the spawn signon state.
		c.signon = protocol.SignonSpawn
	}
	if c.signon < protocol.SignonSpawn {
		c.emit(EventError{Err: fmt.Errorf("client: packet entities while connecting")})
		return false
	}
	if m.IsDelta && c.deltaTick == -1 {
		// A full update was requested; ignore deltas until it arrives
		// (CClientState::ProcessPacketEntities).
		return true
	}

	if c.entities != nil {
		changes, err := c.entities.Process(c.serverTick, entity.PacketEntities{
			MaxEntries:     m.MaxEntries,
			IsDelta:        m.IsDelta,
			DeltaFrom:      m.DeltaFrom,
			Baseline:       m.Baseline,
			UpdatedEntries: m.UpdatedEntries,
			UpdateBaseline: m.UpdateBaseline,
			Data:           m.Data.Reader(),
		})
		if err != nil {
			// CL_FlushEntityPacket drops the rest of the packet. Ask for an
			// uncompressed update so entity state can be rebuilt.
			c.emit(EventError{Err: fmt.Errorf("client: packet entities: %w", err)})
			c.deltaTick = -1
			return false
		}
		// The baseline is only acknowledged once it has really been copied,
		// otherwise the server and client disagree on the baseline slot.
		if m.UpdateBaseline {
			c.sendReliable(&message.ClcBaselineAck{BaselineTick: c.serverTick, BaselineNr: m.Baseline})
		}
		c.emit(EventEntities{ServerTick: c.serverTick, Full: !m.IsDelta, Changes: changes})
	} else if m.UpdateBaseline {
		c.sendReliable(&message.ClcBaselineAck{BaselineTick: c.serverTick, BaselineNr: m.Baseline})
	}

	if c.signon == protocol.SignonSpawn {
		if m.IsDelta {
			c.emit(EventError{Err: fmt.Errorf("client: delta packet entities while spawning")})
			return false
		}
		c.setSignonState(protocol.SignonFull, c.serverCount)
	}
	if c.deltaTick >= 0 || !m.IsDelta {
		c.deltaTick = c.serverTick
	}
	if !m.IsDelta {
		// Answer a full update immediately so the server can start deltas.
		c.nextCmdTime = c.now
	}
	return true
}

// secondsToDuration converts an engine float time, rounding away float32
// noise (0.015 is not representable) to whole microseconds.
func secondsToDuration(s float32) time.Duration {
	return time.Duration(math.Round(float64(s)*1e6)) * time.Microsecond
}
