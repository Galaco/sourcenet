package client

import (
	"log/slog"
	"time"

	"github.com/galaco/sourcenet/game"
	"github.com/galaco/sourcenet/usercmd"
)

// Engine defaults (baseclientstate.cpp, cl_main.cpp).
const (
	DefaultRetryInterval = 6 * time.Second  // cl_resend
	DefaultMaxRetries    = 4                // CL_CONNECTION_RETRIES
	DefaultTimeout       = 30 * time.Second // cl_timeout
	DefaultCmdRate       = 30               // cl_cmdrate
	signonSendInterval   = 200 * time.Millisecond
	queryRetryInterval   = 1500 * time.Millisecond
	queryAttempts        = 3
	cmdBackup            = 90 // MULTIPLAYER_BACKUP
	maxTicksPerUpdate    = 8
	// productVersionAny disables the server's version check: the engine
	// skips it when either side reports the internal build "2000".
	productVersionAny = "2000"
)

// Config configures a Client. Only Game is commonly set; the zero value of
// every other field selects the engine default.
type Config struct {
	// Game supplies game-specific behaviour. Nil uses game.Generic().
	Game *game.Profile
	// Name is the player name (the "name" userinfo convar).
	Name string
	// Password is the server password, if any.
	Password string
	// ProductVersion overrides Game.ProductVersion. When both are empty the
	// client queries the server's version with A2S_INFO before connecting.
	ProductVersion string
	// UserInfo holds extra userinfo convars sent on connect (rate,
	// cl_updaterate, cl_cmdrate, cl_interp, ...). Defaults are filled in for
	// anything missing.
	UserInfo map[string]string
	// Auth produces connect credentials. Nil uses OfflineAuth{}.
	Auth Authenticator
	// FriendsID and FriendsName are sent in clc_ClientInfo (Steam account
	// id and persona name).
	FriendsID   uint32
	FriendsName string
	// ListenEvents names the game events to subscribe to. Nil subscribes to
	// all events the server announces.
	ListenEvents []string
	// CreateMove fills in each new user command (view angles, movement,
	// buttons). CommandNumber and TickCount are already set. Nil sends
	// empty commands.
	CreateMove func(cmd *usercmd.UserCmd)
	// RetryInterval and MaxRetries control connection attempts.
	RetryInterval time.Duration
	MaxRetries    int
	// Timeout disconnects after this long without packets once fully
	// connected.
	Timeout time.Duration
	// MaxRoutable is the largest datagram before splitting (net_maxroutable).
	MaxRoutable int
	// CompressPackets compresses large outgoing packets (net_compresspackets).
	CompressPackets bool
	// OnEvent, when set, receives every event as it happens in addition to
	// the PollEvents queue.
	OnEvent func(Event)
	// Logger receives diagnostics. Nil discards them.
	Logger *slog.Logger
}

func defaultUserInfo() map[string]string {
	return map[string]string{
		"rate":               "80000",
		"cl_updaterate":      "66",
		"cl_cmdrate":         "66",
		"cl_interp":          "0.1",
		"cl_interp_ratio":    "2",
		"cl_lagcompensation": "1",
		"cl_predict":         "1",
		"cl_predictweapons":  "1",
		"cl_language":        "english",
		"closecaption":       "0",
	}
}
