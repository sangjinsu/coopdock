package coopdock

import (
	"errors"
	"github.com/sangjinsu/coopdock/internal/admin"
	"github.com/sangjinsu/coopdock/internal/room"
	"github.com/sangjinsu/coopdock/internal/transport"
	"time"
)

// Config configures an independent CoopDock instance.
type Config struct {
	Room      RoomConfig
	Transport TransportConfig
	Admin     AdminConfig
}

// AdminConfig enables the dashboard when ViewerToken is nonempty.
type AdminConfig struct{ ViewerToken string }

// RoomConfig controls room, session, input, and password resource limits.
type RoomConfig struct {
	GameIDs                                                               []string
	TickHz, SnapshotHz, InputQueue, InputsPerTick                         int
	MaxRooms, MaxSessions, NumberRetries                                  int
	ActorQueue, CleanupQueue, EventQueue, ListLimit                       int
	HashWorkers, HashQueue                                                int
	FailureTTL                                                            time.Duration
	IPFailureLimit, SessionFailureLimit, RoomFailureLimit, MaxFailureKeys int
	RequestTimeout, ShutdownTimeout                                       time.Duration
	ArgonMemory                                                           uint32
}

// TransportConfig controls WebSocket origins, queues, and timeouts.
type TransportConfig struct {
	Origins                                                    []string
	MessageLimit                                               int64
	IngressQueue, OutboundQueue                                int
	ReadTimeout, WriteTimeout, RequestTimeout, ShutdownTimeout time.Duration
}

// DefaultConfig returns independent settings with the legacy default game enabled.
// Environment variables are never read by the package.
func DefaultConfig() Config {
	r, t := room.DefaultConfig(), transport.DefaultConfig()
	return Config{Room: RoomConfig{GameIDs: append([]string(nil), r.GameIDs...), TickHz: r.TickHz, SnapshotHz: r.SnapshotHz, InputQueue: r.InputQueue, InputsPerTick: r.InputsPerTick, MaxRooms: r.MaxRooms, MaxSessions: r.MaxSessions, NumberRetries: r.NumberRetries, ActorQueue: r.ActorQueue, CleanupQueue: r.CleanupQueue, EventQueue: r.EventQueue, ListLimit: r.ListLimit, HashWorkers: r.HashWorkers, HashQueue: r.HashQueue, FailureTTL: r.FailureTTL, IPFailureLimit: r.IPFailureLimit, SessionFailureLimit: r.SessionFailureLimit, RoomFailureLimit: r.RoomFailureLimit, MaxFailureKeys: r.MaxFailureKeys, RequestTimeout: r.RequestTimeout, ShutdownTimeout: r.ShutdownTimeout, ArgonMemory: r.ArgonMemory}, Transport: TransportConfig{Origins: append([]string(nil), t.Origins...), MessageLimit: t.MessageLimit, IngressQueue: t.IngressQueue, OutboundQueue: t.OutboundQueue, ReadTimeout: t.ReadTimeout, WriteTimeout: t.WriteTimeout, RequestTimeout: t.RequestTimeout, ShutdownTimeout: t.ShutdownTimeout}}
}
func (c RoomConfig) internal() room.Config {
	r := room.DefaultConfig()
	r.GameIDs = append([]string(nil), c.GameIDs...)
	r.TickHz = c.TickHz
	r.SnapshotHz = c.SnapshotHz
	r.InputQueue = c.InputQueue
	r.InputsPerTick = c.InputsPerTick
	r.MaxRooms = c.MaxRooms
	r.MaxSessions = c.MaxSessions
	r.NumberRetries = c.NumberRetries
	r.ActorQueue = c.ActorQueue
	r.CleanupQueue = c.CleanupQueue
	r.EventQueue = c.EventQueue
	r.ListLimit = c.ListLimit
	r.HashWorkers = c.HashWorkers
	r.HashQueue = c.HashQueue
	r.FailureTTL = c.FailureTTL
	r.IPFailureLimit = c.IPFailureLimit
	r.SessionFailureLimit = c.SessionFailureLimit
	r.RoomFailureLimit = c.RoomFailureLimit
	r.MaxFailureKeys = c.MaxFailureKeys
	r.RequestTimeout = c.RequestTimeout
	r.ShutdownTimeout = c.ShutdownTimeout
	r.ArgonMemory = c.ArgonMemory
	return r
}
func (c TransportConfig) internal() transport.Config {
	return transport.Config{Origins: append([]string(nil), c.Origins...), MessageLimit: c.MessageLimit, IngressQueue: c.IngressQueue, OutboundQueue: c.OutboundQueue, ReadTimeout: c.ReadTimeout, WriteTimeout: c.WriteTimeout, RequestTimeout: c.RequestTimeout, ShutdownTimeout: c.ShutdownTimeout}
}

// Validate checks all limits without starting workers or changing the configuration.
// A zero Config is invalid; start with DefaultConfig and override selected fields.
func (c Config) Validate() error {
	r := c.Room
	for _, v := range []struct{ value, min, max int }{
		{r.TickHz, 1, 120}, {r.SnapshotHz, 1, 120}, {r.InputQueue, 1, 4096}, {r.InputsPerTick, 1, 4096},
		{r.MaxRooms, 1, 4096}, {r.MaxSessions, 1, 24576}, {r.NumberRetries, 1, 1024},
		{r.ActorQueue, 1, 4096}, {r.CleanupQueue, 6, 4096}, {r.EventQueue, 1, 4096}, {r.ListLimit, 1, 1000},
		{r.HashWorkers, 1, 16}, {r.HashQueue, 1, 256}, {r.IPFailureLimit, 1, 10000}, {r.SessionFailureLimit, 1, 10000}, {r.RoomFailureLimit, 1, 10000}, {r.MaxFailureKeys, 3, 65536},
	} {
		if v.value < v.min || v.value > v.max {
			return errors.New("Invalid room configuration.")
		}
	}
	if r.SnapshotHz > r.TickHz || r.InputsPerTick > r.InputQueue || r.ArgonMemory < 19456 || r.ArgonMemory > 65536 {
		return errors.New("Invalid room configuration.")
	}
	for _, v := range []struct{ value, max time.Duration }{{r.FailureTTL, time.Hour}, {r.RequestTimeout, time.Minute}, {r.ShutdownTimeout, time.Minute}} {
		if v.value <= 0 || v.value > v.max {
			return errors.New("Invalid room configuration.")
		}
	}
	if len(r.GameIDs) < 1 || len(r.GameIDs) > 32 {
		return errors.New("Invalid GAME_IDS.")
	}
	seen := make(map[string]bool, len(r.GameIDs))
	for _, id := range r.GameIDs {
		if !room.ValidGameID(id) || seen[id] {
			return errors.New("Invalid GAME_IDS.")
		}
		seen[id] = true
	}
	if err := c.Transport.internal().Validate(); err != nil {
		return err
	}
	return (admin.Config{ViewerToken: c.Admin.ViewerToken}).Validate()
}
