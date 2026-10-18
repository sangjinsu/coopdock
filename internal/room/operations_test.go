package room

import (
	"context"
	"reflect"
	"runtime"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"roomserver/internal/game"
)

func awaitOperations(t *testing.T, m *Manager, matches func(OperationsSnapshot) bool) OperationsSnapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		v := m.OperationsSnapshot()
		if matches(v) {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatalf("operations summary did not converge: %+v", v)
		}
		runtime.Gosched()
	}
}

func TestOperationsSummaryClone(t *testing.T) {
	id := "match-one"
	original := OperationsSummary{RoomNumber: "001284", MatchID: &id}
	copied := original.clone()
	*copied.MatchID = "changed"
	if *original.MatchID != "match-one" {
		t.Fatal("shared match pointer")
	}
	if (OperationsSummary{}).clone().MatchID != nil {
		t.Fatal("waiting clone acquired a match")
	}
}

func TestOperationsPublishDoesNotCapture(t *testing.T) {
	m := setup(t, DefaultConfig())
	r := newActor(m, "default", "001284", CreateOptions{Capacity: 6, Visibility: "private"}, nil)
	r.revision, r.snapshotSeq = 7, 13
	now := time.Now()
	r.publishOperations(now)
	if r.revision != 7 || r.snapshotSeq != 13 {
		t.Fatal("operations changed game sequence")
	}
	v := r.operations.Load()
	if v == nil || v.RoomNumber != "001284" || v.Visibility != "private" || v.Capacity != 6 || v.Phase != "WAITING" || v.MatchID != nil || v.UpdatedAt != now {
		t.Fatalf("missing detached allowlist summary: %+v", v)
	}
}

func TestOperationsPublishedPrivateAllowlist(t *testing.T) {
	wantFields := []string{"GameID", "RoomID", "RoomNumber", "Visibility", "Phase", "Capacity", "MemberCount", "HostID", "MatchID", "UpdatedAt"}
	typ := reflect.TypeOf(OperationsSummary{})
	fields := make([]string, typ.NumField())
	for i := range fields {
		fields[i] = typ.Field(i).Name
	}
	sort.Strings(fields)
	sort.Strings(wantFields)
	if !reflect.DeepEqual(fields, wantFields) {
		t.Fatalf("operations field allowlist: %v", fields)
	}
	cfg := DefaultConfig()
	cfg.hash = (&barrier{}).hash
	var number int
	cfg.number = func() (string, error) {
		number++
		if number == 1 {
			return "001284", nil
		}
		return "000029", nil
	}
	m := setup(t, cfg)
	publicHost := connect(t, m, "public-host")
	public := create(t, m, publicHost, CreateOptions{Capacity: 2, Visibility: "public"})
	privateHost := connect(t, m, "private-host")
	private := create(t, m, privateHost, CreateOptions{Capacity: 3, Visibility: "private", Password: str("secret")})
	guest := connect(t, m, "private-guest")
	if _, err := m.Join(context.Background(), guest, JoinOptions{RoomNumber: private.RoomNumber, Password: str("secret")}); err != nil {
		t.Fatal(err)
	}
	started := startMatch(t, m, privateHost)
	v := awaitOperations(t, m, func(v OperationsSnapshot) bool {
		if !v.Ready || len(v.Rooms) != 2 {
			return false
		}
		for _, r := range v.Rooms {
			if r.RoomID == private.RoomID {
				return r.Phase == "PLAYING" && r.MemberCount == 2
			}
		}
		return false
	})
	for _, r := range v.Rooms {
		switch r.RoomID {
		case public.RoomID:
			if r.RoomNumber != "001284" || r.Visibility != "public" || r.Capacity != 2 || r.MemberCount != 1 || r.HostID != publicHost.ID() || r.Phase != "WAITING" || r.MatchID != nil || r.UpdatedAt.IsZero() {
				t.Fatalf("public summary mismatch: %+v", r)
			}
		case private.RoomID:
			if r.RoomNumber != "000029" || r.Visibility != "private" || r.Capacity != 3 || r.HostID != privateHost.ID() || r.MatchID == nil || *r.MatchID != *started.MatchID || r.UpdatedAt.IsZero() {
				t.Fatalf("private summary mismatch: %+v", r)
			}
		default:
			t.Fatalf("unexpected room: %+v", r)
		}
	}
	listed, err := listForTest(t, m, context.Background())
	if err != nil || len(listed) != 1 || listed[0].RoomID != public.RoomID {
		t.Fatalf("private room leaked into public list: %+v, %v", listed, err)
	}
	if err := m.Leave(context.Background(), privateHost); err != nil {
		t.Fatal(err)
	}
	awaitOperations(t, m, func(v OperationsSnapshot) bool {
		for _, r := range v.Rooms {
			if r.RoomID == private.RoomID {
				return r.HostID == guest.ID() && r.MemberCount == 1 && r.Phase == "PLAYING"
			}
		}
		return false
	})
}

func TestOperationsConfigRoomBound(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxRooms = 4097
	m, err := NewManager(cfg)
	if m != nil {
		defer m.Close(context.Background())
	}
	if err == nil {
		t.Fatal("unbounded operations registry accepted")
	}
	cfg.MaxRooms = 4096
	m = setup(t, cfg)
	if !m.OperationsSnapshot().Ready {
		t.Fatal("maximum supported room bound not ready")
	}
}

type operationsClock struct {
	ticks   chan time.Time
	now     atomic.Int64
	stopped atomic.Int32
}

func newOperationsClock(t *testing.T, cfg *Config) *operationsClock {
	t.Helper()
	c := &operationsClock{ticks: make(chan time.Time, 1)}
	c.now.Store(100)
	cfg.newOperationsTicker = func(d time.Duration) (<-chan time.Time, func()) {
		if d != time.Second {
			t.Errorf("wrong operations interval: %v", d)
		}
		return c.ticks, func() { c.stopped.Add(1) }
	}
	cfg.operationsNow = func() time.Time { return time.Unix(c.now.Load(), 0) }
	return c
}

func TestOperationsHeartbeatIndependent(t *testing.T) {
	var operations *operationsClock
	m, clock := realtimeSetup(t, func(cfg *Config) {
		operations = newOperationsClock(t, cfg)
		cfg.HandlerFactory = func() game.GameHandler {
			return &endingHandler{GameHandler: game.NewMovement(), endAt: 1}
		}
	})
	host := connect(t, m, "host")
	created := create(t, m, host, CreateOptions{Capacity: 1})
	initial := m.OperationsSnapshot()
	if len(initial.Rooms) != 1 || initial.Rooms[0].UpdatedAt != time.Unix(100, 0) {
		t.Fatalf("initial summary timestamp: %+v", initial)
	}
	operations.now.Store(101)
	operations.ticks <- time.Unix(1, 0)
	awaitOperations(t, m, func(v OperationsSnapshot) bool {
		return len(v.Rooms) == 1 && v.Rooms[0].Phase == "WAITING" && v.Rooms[0].UpdatedAt == time.Unix(101, 0)
	})
	listed, err := listForTest(t, m, context.Background())
	if err != nil || len(listed) != 1 || listed[0].Revision != created.Revision || listed[0].SnapshotSeq != created.SnapshotSeq+2 {
		t.Fatalf("waiting heartbeat captured game state: %+v, %v", listed, err)
	}
	started := startMatch(t, m, host)
	operations.now.Store(102)
	operations.ticks <- time.Unix(2, 0)
	awaitOperations(t, m, func(v OperationsSnapshot) bool {
		return len(v.Rooms) == 1 && v.Rooms[0].Phase == "PLAYING" && v.Rooms[0].MatchID != nil && *v.Rooms[0].MatchID == *started.MatchID && v.Rooms[0].UpdatedAt == time.Unix(102, 0)
	})
	clock.ticks <- time.Unix(3, 0)
	awaitGameEvent(t, host, "game.ended")
	awaitOperations(t, m, func(v OperationsSnapshot) bool {
		return len(v.Rooms) == 1 && v.Rooms[0].Phase == "WAITING" && v.Rooms[0].MatchID == nil
	})
	if operations.stopped.Load() != 0 {
		t.Fatal("match end stopped operations heartbeat")
	}
	operations.now.Store(103)
	operations.ticks <- time.Unix(4, 0)
	awaitOperations(t, m, func(v OperationsSnapshot) bool {
		return len(v.Rooms) == 1 && v.Rooms[0].UpdatedAt == time.Unix(103, 0)
	})
	if err := m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if operations.stopped.Load() != 1 {
		t.Fatalf("operations stop count: %d", operations.stopped.Load())
	}
	v := m.OperationsSnapshot()
	if v.Ready || len(v.Rooms) != 0 {
		t.Fatalf("closed manager retained rooms/readiness: %+v", v)
	}
}

type operationsBarrierHandler struct {
	game.GameHandler
	entered   chan struct{}
	release   chan struct{}
	completed atomic.Int32
}

func (h *operationsBarrierHandler) Tick(dt float64) (game.Outcome, error) {
	if h.completed.Load() == 0 {
		close(h.entered)
		<-h.release
	}
	h.completed.Add(1)
	return h.GameHandler.Tick(dt)
}

func TestOperationsHeartbeatBlockedActor(t *testing.T) {
	handler := &operationsBarrierHandler{GameHandler: game.NewMovement(), entered: make(chan struct{}), release: make(chan struct{})}
	var operations *operationsClock
	m, clock := realtimeSetup(t, func(cfg *Config) {
		operations = newOperationsClock(t, cfg)
		cfg.HandlerFactory = func() game.GameHandler { return handler }
	})
	// Release the actor before manager cleanup even when an assertion fails.
	t.Cleanup(func() {
		select {
		case <-handler.release:
		default:
			close(handler.release)
		}
	})
	host := connect(t, m, "host")
	created := create(t, m, host, CreateOptions{Capacity: 1})
	started := startMatch(t, m, host)
	m.mu.Lock()
	r := m.rooms[roomKey{"default", created.RoomNumber}]
	m.mu.Unlock()
	clock.ticks <- time.Unix(1, 0)
	select {
	case <-handler.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("actor did not enter handler barrier")
	}
	revision, seq := r.revision, r.snapshotSeq
	operations.now.Store(101)
	operations.ticks <- time.Unix(1, 0)
	for i := 0; i < 20; i++ {
		v := m.OperationsSnapshot()
		if len(v.Rooms) != 1 || v.Rooms[0].UpdatedAt != time.Unix(100, 0) || v.Rooms[0].MatchID == nil || *v.Rooms[0].MatchID != *started.MatchID || r.revision != revision || r.snapshotSeq != seq {
			t.Fatalf("blocked actor summary mutated or captured: %+v", v)
		}
	}
	close(handler.release)
	awaitOperations(t, m, func(v OperationsSnapshot) bool {
		return len(v.Rooms) == 1 && v.Rooms[0].UpdatedAt == time.Unix(101, 0)
	})
}

func TestOperationsRegistryReuseAndClone(t *testing.T) {
	cfg := DefaultConfig()
	cfg.number = func() (string, error) { return "001284", nil }
	m := setup(t, cfg)
	host := connect(t, m, "host")
	old := create(t, m, host, CreateOptions{Capacity: 1})
	m.mu.Lock()
	oldActor := m.rooms[roomKey{"default", old.RoomNumber}]
	m.mu.Unlock()
	if err := m.Leave(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	select {
	case <-oldActor.done:
	case <-time.After(2 * time.Second):
		t.Fatal("old actor did not close")
	}
	if rooms := m.OperationsSnapshot().Rooms; len(rooms) != 0 {
		t.Fatalf("closed actor retained: %+v", rooms)
	}
	newRoom := create(t, m, host, CreateOptions{Capacity: 1})
	if newRoom.RoomID == old.RoomID || newRoom.RoomNumber != old.RoomNumber {
		t.Fatal("room identity did not change on number reuse")
	}
	started := startMatch(t, m, host)
	v := awaitOperations(t, m, func(v OperationsSnapshot) bool {
		return len(v.Rooms) == 1 && v.Rooms[0].RoomID == newRoom.RoomID && v.Rooms[0].MatchID != nil
	})
	*v.Rooms[0].MatchID = "changed"
	v.Rooms[0].RoomID = old.RoomID
	v.Rooms = append(v.Rooms, OperationsSummary{RoomID: "injected"})
	next := m.OperationsSnapshot()
	if len(next.Rooms) != 1 || next.Rooms[0].RoomID != newRoom.RoomID || next.Rooms[0].MatchID == nil || *next.Rooms[0].MatchID != *started.MatchID {
		t.Fatalf("returned operations snapshot mutated publication: %+v", next)
	}
}

func TestOperationsHeartbeatDoesNotStarveBehindTicks(t *testing.T) {
	handler := &operationsBarrierHandler{GameHandler: game.NewMovement(), entered: make(chan struct{}), release: make(chan struct{})}
	var operations *operationsClock
	var publishedAfter atomic.Int32
	m, clock := realtimeSetup(t, func(cfg *Config) {
		operations = newOperationsClock(t, cfg)
		cfg.operationsNow = func() time.Time {
			now := operations.now.Load()
			if now == 101 {
				publishedAfter.CompareAndSwap(0, handler.completed.Load())
			}
			return time.Unix(now, 0)
		}
		cfg.HandlerFactory = func() game.GameHandler { return handler }
	})
	t.Cleanup(func() {
		select {
		case <-handler.release:
		default:
			close(handler.release)
		}
	})
	host := connect(t, m, "host")
	create(t, m, host, CreateOptions{Capacity: 1})
	startMatch(t, m, host)
	clock.ticks <- time.Unix(1, 0)
	select {
	case <-handler.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("actor did not enter handler barrier")
	}
	// Each queued game tick remains ready when the actor finishes the preceding tick.
	for i := 0; i < 16; i++ {
		clock.ticks <- time.Unix(1, 0)
	}
	operations.now.Store(101)
	operations.ticks <- time.Unix(1, 0)
	close(handler.release)
	awaitOperations(t, m, func(v OperationsSnapshot) bool {
		return len(v.Rooms) == 1 && v.Rooms[0].UpdatedAt == time.Unix(101, 0)
	})
	if n := publishedAfter.Load(); n != 1 {
		t.Fatalf("operations heartbeat starved behind %d completed ticks", n)
	}
}

func TestOperationsUnpublishedHeartbeatPreservesAdmission(t *testing.T) {
	cfg := DefaultConfig()
	operations := newOperationsClock(t, &cfg)
	entered, release := make(chan struct{}), make(chan struct{})
	observed := make(chan struct{}, 1)
	cfg.beforeSubmit = func() { close(entered); <-release }
	cfg.operationsNow = func() time.Time {
		now := operations.now.Load()
		if now == 101 {
			select {
			case observed <- struct{}{}:
			default:
			}
		}
		return time.Unix(now, 0)
	}
	m := setup(t, cfg)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	host := connect(t, m, "host")
	result := make(chan error, 1)
	go func() {
		_, err := m.Create(context.Background(), host, CreateOptions{Capacity: 1})
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("create did not reach admission barrier")
	}
	operations.now.Store(101)
	operations.ticks <- time.Unix(1, 0)
	select {
	case <-observed:
	case <-time.After(2 * time.Second):
		t.Fatal("unpublished actor did not consume operations heartbeat")
	}
	v := m.OperationsSnapshot()
	if !v.Ready || len(v.Rooms) != 0 {
		t.Fatalf("unpublished reservation exposed: %+v", v)
	}
	close(release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("heartbeat closed unpublished actor: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("create did not complete")
	}
	awaitOperations(t, m, func(v OperationsSnapshot) bool { return len(v.Rooms) == 1 && v.Rooms[0].MemberCount == 1 })
}
