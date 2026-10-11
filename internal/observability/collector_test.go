package observability

import (
	"context"
	"errors"
	"roomserver/internal/room"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCollectorInitialCache(t *testing.T) {
	clock := newManualClock()
	cfg := clock.config()
	at := clock.now()
	var samples, reads atomic.Int32
	cfg.sampleRuntime = func() (uint64, int) { samples.Add(1); return 2048, 3 }
	match := "match-1"
	sourceRooms := []room.OperationsSummary{
		{RoomID: "r2", RoomNumber: "000002", Visibility: "public", Phase: "PLAYING", Capacity: 6, MemberCount: 3, MatchID: &match, UpdatedAt: at},
		{RoomID: "r1", RoomNumber: "000001", Visibility: "private", Phase: "WAITING", Capacity: 6, MemberCount: 2, UpdatedAt: at},
	}
	source := Source{Rooms: func() room.OperationsSnapshot {
		reads.Add(1)
		return room.OperationsSnapshot{Ready: true, Rooms: sourceRooms}
	}, Transport: func() (int, bool) { return 3, false }}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := New(ctx, source, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	v := c.Load()
	if v.ServerInstanceID == "" || v.State != Running || !v.Ready || v.Generation != 1 || v.RoomCount != 2 || v.WaitingRoomCount != 1 || v.PlayingRoomCount != 1 || v.RoomMemberCount != 5 || v.ActiveConnections != 3 || v.HeapAllocBytes != 2048 || v.Goroutines != 3 || v.StartedAt != at || v.UpdatedAt != at {
		t.Fatalf("invalid initial cache: %+v", v)
	}
	rooms := v.Rooms()
	if len(rooms) != 2 || rooms[0].RoomID != "r1" || rooms[1].RoomID != "r2" {
		t.Fatalf("unsorted rooms: %+v", rooms)
	}
	rooms[0].RoomID = "changed"
	*rooms[1].MatchID = "changed"
	sourceRooms[0].RoomID = "changed-source"
	match = "changed-source"
	copy := c.Load().Rooms()
	if copy[0].RoomID != "r1" || copy[1].RoomID != "r2" || *copy[1].MatchID != "match-1" {
		t.Fatal("shared rooms backing or match identity")
	}
	for i := 0; i < 100; i++ {
		c.Load().Rooms()
	}
	if samples.Load() != 1 || reads.Load() != 1 {
		t.Fatal("Load invoked source or runtime sampling")
	}
}

func TestAgeMillisClamp(t *testing.T) {
	at := time.Now()
	for _, v := range []struct {
		delta time.Duration
		want  int64
	}{{-time.Second, 0}, {4999 * time.Millisecond, 4999}, {5000 * time.Millisecond, 5000}} {
		if got := AgeMillis(at.Add(v.delta), at); got != v.want {
			t.Fatalf("age=%d, want %d", got, v.want)
		}
	}
}

// Manual tickers keep source collection and the fixed runtime sampler independent.
type manualClock struct {
	mu                            sync.Mutex
	at                            time.Time
	sourceTicks, runtimeTicks     chan time.Time
	sourceStopped, runtimeStopped chan struct{}
}

func newManualClock() *manualClock {
	return &manualClock{at: time.Now(), sourceTicks: make(chan time.Time, 8), runtimeTicks: make(chan time.Time, 8), sourceStopped: make(chan struct{}), runtimeStopped: make(chan struct{})}
}
func (f *manualClock) now() time.Time          { f.mu.Lock(); defer f.mu.Unlock(); return f.at }
func (f *manualClock) advance(d time.Duration) { f.mu.Lock(); f.at = f.at.Add(d); f.mu.Unlock() }
func (f *manualClock) config() Config {
	cfg := DefaultConfig()
	cfg.Interval = 10 * time.Millisecond
	cfg.now = f.now
	cfg.newTicker = func(d time.Duration) (<-chan time.Time, func()) {
		if d == time.Second {
			return f.runtimeTicks, func() { close(f.runtimeStopped) }
		}
		return f.sourceTicks, func() { close(f.sourceStopped) }
	}
	return cfg
}
func awaitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("signal timeout")
	}
}
func awaitGeneration(t *testing.T, c *Collector, want uint64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if c.Load().Generation == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("generation=%d, want %d", c.Load().Generation, want)
}

func TestCollectorRuntimeSamplerIndependent(t *testing.T) {
	clock := newManualClock()
	cfg := clock.config()
	sampled := make(chan struct{}, 8)
	var samples atomic.Int32
	cfg.sampleRuntime = func() (uint64, int) { n := samples.Add(1); sampled <- struct{}{}; return uint64(n) * 1024, int(n) }
	c, err := New(context.Background(), Source{Rooms: func() room.OperationsSnapshot { return room.OperationsSnapshot{Ready: true} }, Transport: func() (int, bool) { return 0, false }}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	awaitSignal(t, sampled)
	for i := 0; i < 100; i++ {
		c.Load()
	}
	if samples.Load() != 1 {
		t.Fatal("Load sampled runtime")
	}
	clock.runtimeTicks <- clock.now()
	awaitSignal(t, sampled)
	deadline := time.Now().Add(time.Second)
	for c.runtime.Load().heapAllocBytes != 2048 {
		if time.Now().After(deadline) {
			t.Fatal("runtime sample publication timeout")
		}
		time.Sleep(time.Millisecond)
	}
	clock.sourceTicks <- clock.now()
	awaitGeneration(t, c, 2)
	if v := c.Load(); v.HeapAllocBytes != 2048 || v.Goroutines != 2 {
		t.Fatalf("did not use latest runtime sample: %+v", v)
	}
}

func TestCollectorReadinessAndRemoval(t *testing.T) {
	clock := newManualClock()
	cfg := clock.config()
	cfg.sampleRuntime = func() (uint64, int) { return 0, 0 }
	var call atomic.Int32
	c, err := New(context.Background(), Source{Rooms: func() room.OperationsSnapshot {
		if call.Add(1) == 1 {
			return room.OperationsSnapshot{Ready: true, Rooms: []room.OperationsSummary{{RoomID: "old", Phase: "WAITING", MemberCount: 2}}}
		}
		return room.OperationsSnapshot{Ready: true}
	}, Transport: func() (int, bool) { return 4, true }}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	if c.Load().Ready {
		t.Fatal("stopping transport ready")
	}
	clock.sourceTicks <- clock.now()
	awaitGeneration(t, c, 2)
	if v := c.Load(); v.RoomCount != 0 || v.RoomMemberCount != 0 || len(v.Rooms()) != 0 || v.ActiveConnections != 4 {
		t.Fatalf("removed rooms persisted: %+v", v)
	}
}

func TestCollectorInvalidConfiguration(t *testing.T) {
	good := Source{Rooms: func() room.OperationsSnapshot { return room.OperationsSnapshot{} }, Transport: func() (int, bool) { return 0, false }}
	for _, change := range []func(*Source, *Config){func(s *Source, c *Config) { s.Rooms = nil }, func(s *Source, c *Config) { s.Transport = nil }, func(s *Source, c *Config) { c.Interval = 0 }} {
		source, cfg := good, DefaultConfig()
		change(&source, &cfg)
		c, err := New(context.Background(), source, cfg)
		if err == nil {
			if c != nil {
				c.Close(context.Background())
			}
			t.Fatal("invalid configuration accepted")
		}
	}
}

func TestCollectorCloseCanceledCaller(t *testing.T) {
	c, err := New(context.Background(), Source{Rooms: func() room.OperationsSnapshot { return room.OperationsSnapshot{Ready: true} }, Transport: func() (int, bool) { return 0, false }}, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = c.Close(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCollectorStaleAndBoundedClose(t *testing.T) {
	clock := newManualClock()
	cfg := clock.config()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var calls, samples atomic.Int32
	cfg.sampleRuntime = func() (uint64, int) { samples.Add(1); return 2048, 3 }
	c, err := New(context.Background(), Source{Rooms: func() room.OperationsSnapshot {
		if calls.Add(1) == 2 {
			close(entered)
			<-release
		}
		return room.OperationsSnapshot{Ready: true, Rooms: []room.OperationsSummary{{RoomID: "r1", Phase: "WAITING", MemberCount: 2, UpdatedAt: clock.now()}}}
	}, Transport: func() (int, bool) { return 3, false }}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	initial := c.Load()
	clock.sourceTicks <- clock.now()
	awaitSignal(t, entered)
	clock.advance(6 * time.Second)
	for i := 0; i < 5; i++ {
		clock.sourceTicks <- clock.now()
	}
	if v := c.Load(); v.UpdatedAt != initial.UpdatedAt || v.Generation != initial.Generation || AgeMillis(clock.now(), v.UpdatedAt) != 6000 {
		t.Fatalf("blocked source refreshed cache: %+v", v)
	}
	if calls.Load() != 2 {
		t.Fatal("spawned concurrent source callers")
	}
	clock.runtimeTicks <- clock.now()
	deadline := time.Now().Add(time.Second)
	for samples.Load() != 2 {
		if time.Now().After(deadline) {
			t.Fatal("runtime sampler blocked behind source")
		}
		time.Sleep(time.Millisecond)
	}
	if c.Load().Generation != initial.Generation {
		t.Fatal("runtime sample alone refreshed source cache")
	}
	c.MarkStopping()
	if v := c.Load(); v.State != Stopping || v.Ready || v.UpdatedAt != initial.UpdatedAt {
		t.Fatalf("invalid stopping overlay: %+v", v)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked Close error: %v", err)
	}
	awaitSignal(t, clock.runtimeStopped)
	beforeSamples := samples.Load()
	clock.runtimeTicks <- clock.now()
	if samples.Load() != beforeSamples {
		t.Fatal("sampler remained active after cancellation")
	}
	releaseOnce.Do(func() { close(release) })
	if err := c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, clock.sourceStopped)
	if v := c.Load(); v.Generation != initial.Generation || v.UpdatedAt != initial.UpdatedAt || v.State != Stopping || v.Ready {
		t.Fatalf("late source result published: %+v", v)
	}
	if calls.Load() != 2 {
		t.Fatal("collected again after cancellation")
	}
}

func TestCollectorStartupDeadline(t *testing.T) {
	for _, blocked := range []string{"rooms", "transport", "runtime"} {
		t.Run(blocked, func(t *testing.T) {
			clock := newManualClock()
			cfg := clock.config()
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			cfg.sampleRuntime = func() (uint64, int) {
				if blocked == "runtime" {
					close(entered)
					<-release
				}
				return 2048, 3
			}
			source := Source{Rooms: func() room.OperationsSnapshot {
				if blocked == "rooms" {
					close(entered)
					<-release
				}
				return room.OperationsSnapshot{Ready: true}
			}, Transport: func() (int, bool) {
				if blocked == "transport" {
					close(entered)
					<-release
				}
				return 0, false
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			type result struct {
				c   *Collector
				err error
			}
			completed := make(chan result, 1)
			go func() { c, err := New(ctx, source, cfg); completed <- result{c, err} }()
			awaitSignal(t, entered)
			select {
			case r := <-completed:
				if r.c != nil || !errors.Is(r.err, context.DeadlineExceeded) {
					t.Fatalf("startup result: %v, %v", r.c, r.err)
				}
			case <-time.After(time.Second):
				t.Fatal("startup deadline not bounded")
			}
			releaseOnce.Do(func() { close(release) })
			awaitSignal(t, clock.sourceStopped)
			awaitSignal(t, clock.runtimeStopped)
		})
	}
}

func TestCollectorStoppingOverlaySurvivesCollection(t *testing.T) {
	clock := newManualClock()
	cfg := clock.config()
	cfg.sampleRuntime = func() (uint64, int) { return 0, 0 }
	c, err := New(context.Background(), Source{Rooms: func() room.OperationsSnapshot { return room.OperationsSnapshot{Ready: true} }, Transport: func() (int, bool) { return 1, false }}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	initial := c.Load()
	c.MarkStopping()
	c.MarkStopping()
	if v := c.Load(); v.State != Stopping || v.Ready || v.UpdatedAt != initial.UpdatedAt {
		t.Fatal("stop overlay refreshed timestamp")
	}
	clock.advance(time.Second)
	clock.sourceTicks <- clock.now()
	awaitGeneration(t, c, 2)
	if v := c.Load(); v.State != Stopping || v.Ready {
		t.Fatal("collection reverted stopping state")
	}
}

func TestCollectorFixedStaleThreshold(t *testing.T) {
	at := time.Now()
	for _, v := range []struct {
		age   time.Duration
		stale bool
	}{{4999 * time.Millisecond, false}, {5000 * time.Millisecond, true}} {
		stale := AgeMillis(at.Add(v.age), at) >= int64(StaleAfter/time.Millisecond)
		if stale != v.stale {
			t.Fatalf("age=%v stale=%v, want %v", v.age, stale, v.stale)
		}
	}
}

func TestCollectorRoomReadiness(t *testing.T) {
	cfg := DefaultConfig()
	cfg.sampleRuntime = func() (uint64, int) { return 0, 0 }
	c, err := New(context.Background(), Source{Rooms: func() room.OperationsSnapshot { return room.OperationsSnapshot{Ready: false} }, Transport: func() (int, bool) { return 1, false }}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	if c.Load().Ready {
		t.Fatal("unready room source reported ready")
	}
}
