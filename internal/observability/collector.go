// Package observability publishes immutable operational caches outside HTTP requests.
package observability

import (
	"context"
	"crypto/rand"
	"errors"
	"roomserver/internal/room"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

var _ func(*room.Manager) room.OperationsSnapshot = (*room.Manager).OperationsSnapshot

const StaleAfter = 5 * time.Second

type State string

const (
	Running  State = "RUNNING"
	Stopping State = "STOPPING"
)

type Source struct {
	Rooms     func() room.OperationsSnapshot
	Transport func() (activeConnections int, stopping bool)
}
type Config struct {
	Interval      time.Duration
	now           func() time.Time
	newTicker     func(time.Duration) (<-chan time.Time, func())
	sampleRuntime func() (heapAllocBytes uint64, goroutines int)
}

func DefaultConfig() Config { return Config{Interval: time.Second} }

type Snapshot struct {
	ServerInstanceID                              string
	State                                         State
	Ready                                         bool
	ActiveConnections, RoomMemberCount            int
	RoomCount, WaitingRoomCount, PlayingRoomCount int
	StartedAt, UpdatedAt                          time.Time
	HeapAllocBytes                                uint64
	Goroutines                                    int
	Generation                                    uint64
	rooms                                         []room.OperationsSummary
}

func cloneRooms(rooms []room.OperationsSummary) []room.OperationsSummary {
	result := make([]room.OperationsSummary, len(rooms))
	copy(result, rooms)
	for i := range result {
		if result[i].MatchID != nil {
			id := *result[i].MatchID
			result[i].MatchID = &id
		}
	}
	return result
}
func (v Snapshot) Rooms() []room.OperationsSummary { return cloneRooms(v.rooms) }
func AgeMillis(now, updated time.Time) int64 {
	age := now.Sub(updated) / time.Millisecond
	if age < 0 {
		return 0
	}
	return int64(age)
}

type runtimeSample struct {
	heapAllocBytes uint64
	goroutines     int
}
type Collector struct {
	cache                   atomic.Pointer[Snapshot]
	runtime                 atomic.Pointer[runtimeSample]
	stopping                atomic.Bool
	cancel                  context.CancelFunc
	cancelOnce              sync.Once
	publicationMu           sync.Mutex
	sourceDone, runtimeDone chan struct{}
}

func New(startupCtx context.Context, source Source, cfg Config) (*Collector, error) {
	if source.Rooms == nil || source.Transport == nil || cfg.Interval <= 0 {
		return nil, errors.New("Invalid observability configuration.")
	}
	if err := startupCtx.Err(); err != nil {
		return nil, err
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	if cfg.newTicker == nil {
		cfg.newTicker = func(d time.Duration) (<-chan time.Time, func()) {
			ticker := time.NewTicker(d)
			return ticker.C, ticker.Stop
		}
	}
	if cfg.sampleRuntime == nil {
		cfg.sampleRuntime = func() (uint64, int) {
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			return stats.HeapAlloc, runtime.NumGoroutine()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Collector{cancel: cancel, sourceDone: make(chan struct{}), runtimeDone: make(chan struct{})}
	identity, started := rand.Text(), cfg.now()
	sampleReady, cacheReady := make(chan struct{}), make(chan struct{})
	go c.sample(ctx, cfg, sampleReady)
	go c.collect(ctx, source, cfg, identity, started, sampleReady, cacheReady)
	select {
	case <-startupCtx.Done():
		c.requestCancel()
		return nil, startupCtx.Err()
	case <-cacheReady:
		if err := startupCtx.Err(); err != nil {
			c.requestCancel()
			return nil, err
		}
		return c, nil
	}
}

func (c *Collector) sample(ctx context.Context, cfg Config, ready chan<- struct{}) {
	defer close(c.runtimeDone)
	ticks, stop := cfg.newTicker(time.Second)
	defer stop()
	first := true
	for {
		if ctx.Err() != nil {
			return
		}
		heap, goroutines := cfg.sampleRuntime()
		if ctx.Err() != nil {
			return
		}
		c.publicationMu.Lock()
		if ctx.Err() != nil {
			c.publicationMu.Unlock()
			return
		}
		c.runtime.Store(&runtimeSample{heap, goroutines})
		if first {
			close(ready)
			first = false
		}
		c.publicationMu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}
func (c *Collector) collect(ctx context.Context, source Source, cfg Config, identity string, started time.Time, sampleReady <-chan struct{}, ready chan<- struct{}) {
	defer close(c.sourceDone)
	ticks, stop := cfg.newTicker(cfg.Interval)
	defer stop()
	select {
	case <-ctx.Done():
		return
	case <-sampleReady:
	}
	var generation uint64
	for {
		if ctx.Err() != nil {
			return
		}
		rooms := source.Rooms()
		if ctx.Err() != nil {
			return
		}
		connections, transportStopping := source.Transport()
		if ctx.Err() != nil {
			return
		}
		sample := c.runtime.Load()
		generation++
		v := Snapshot{ServerInstanceID: identity, State: Running, Ready: rooms.Ready && !transportStopping,
			ActiveConnections: connections, StartedAt: started, UpdatedAt: cfg.now(), Generation: generation,
			HeapAllocBytes: sample.heapAllocBytes, Goroutines: sample.goroutines, rooms: cloneRooms(rooms.Rooms)}
		sort.Slice(v.rooms, func(i, j int) bool {
			if v.rooms[i].GameID != v.rooms[j].GameID {
				return v.rooms[i].GameID < v.rooms[j].GameID
			}
			if v.rooms[i].RoomNumber == v.rooms[j].RoomNumber {
				return v.rooms[i].RoomID < v.rooms[j].RoomID
			}
			return v.rooms[i].RoomNumber < v.rooms[j].RoomNumber
		})
		v.RoomCount = len(v.rooms)
		for _, r := range v.rooms {
			v.RoomMemberCount += r.MemberCount
			switch r.Phase {
			case "WAITING":
				v.WaitingRoomCount++
			case "PLAYING":
				v.PlayingRoomCount++
			}
		}
		// Serialize publication with cancellation, without holding a gate across callbacks.
		c.publicationMu.Lock()
		if ctx.Err() != nil {
			c.publicationMu.Unlock()
			return
		}
		c.cache.Store(&v)
		if generation == 1 {
			close(ready)
		}
		c.publicationMu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}
func (c *Collector) Load() Snapshot {
	var v Snapshot
	if latest := c.cache.Load(); latest != nil {
		v = *latest
	}
	if c.stopping.Load() {
		v.State = Stopping
		v.Ready = false
	}
	return v
}
func (c *Collector) MarkStopping() { c.stopping.Store(true) }
func (c *Collector) requestCancel() {
	c.cancelOnce.Do(func() {
		c.publicationMu.Lock()
		c.cancel()
		c.publicationMu.Unlock()
	})
}
func (c *Collector) Close(ctx context.Context) error {
	c.requestCancel()
	for _, done := range []<-chan struct{}{c.sourceDone, c.runtimeDone} {
		select {
		case <-done:
			continue
		default:
		}
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
