package room

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func setup(t *testing.T, cfg Config) *Manager {
	t.Helper()
	m, e := NewManager(cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := m.Close(context.Background()); e != nil {
			t.Error(e)
		}
	})
	return m
}
func connect(t *testing.T, m *Manager, peer string) *Session {
	t.Helper()
	s, e := m.Connect(peer)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func create(t *testing.T, m *Manager, s *Session, o CreateOptions) Snapshot {
	t.Helper()
	v, e := m.Create(context.Background(), s, o)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func str(s string) *string { return &s }
func code(t *testing.T, e error, w string) {
	t.Helper()
	if CodeOf(e) != w {
		t.Fatalf("got %v, want %s", e, w)
	}
}
func TestRoomCapacity(t *testing.T) {
	m := setup(t, DefaultConfig())
	r := create(t, m, connect(t, m, "host"), CreateOptions{Capacity: 1})
	_, e := m.Join(context.Background(), connect(t, m, "guest"), JoinOptions{RoomNumber: r.RoomNumber})
	code(t, e, "ROOM_FULL")
}
func TestCreateValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		o    CreateOptions
		want string
	}{
		{"default", CreateOptions{}, ""}, {"negative", CreateOptions{Capacity: -1}, "INVALID_MESSAGE"}, {"seven", CreateOptions{Capacity: 7}, "INVALID_MESSAGE"},
		{"publicempty", CreateOptions{Password: str("")}, "INVALID_MESSAGE"}, {"publicpassword", CreateOptions{Password: str("abcd")}, "INVALID_MESSAGE"},
		{"privatemissing", CreateOptions{Visibility: "private"}, "INVALID_MESSAGE"}, {"short", CreateOptions{Visibility: "private", Password: str("abc")}, "INVALID_MESSAGE"},
		{"long", CreateOptions{Visibility: "private", Password: str(strings.Repeat("가", 65))}, "INVALID_MESSAGE"}, {"four", CreateOptions{Visibility: "private", Password: str("가나다라")}, ""},
		{"sixtyfour", CreateOptions{Visibility: "private", Password: str(strings.Repeat("가", 64))}, ""}, {"spaces", CreateOptions{Visibility: "private", Password: str(" 가 나 ")}, ""},
		{"invalidutf8", CreateOptions{Visibility: "private", Password: str("abcd\xff")}, "INVALID_MESSAGE"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.hash = (&barrier{}).hash
			m := setup(t, cfg)
			r, e := m.Create(context.Background(), connect(t, m, "host"), tt.o)
			code(t, e, tt.want)
			if tt.want == "" && tt.o.Capacity == 0 && r.Capacity != 6 {
				t.Fatal(r.Capacity)
			}
		})
	}
}
func TestPrivateAdmission(t *testing.T) {
	cfg := DefaultConfig()
	cfg.hash = (&barrier{}).hash
	m := setup(t, cfg)
	r := create(t, m, connect(t, m, "host"), CreateOptions{Visibility: "private", Password: str(" 가 나 ")})
	g := connect(t, m, "guest")
	missingNumber := "000000"
	if r.RoomNumber == missingNumber {
		missingNumber = "000001"
	}
	for _, tt := range []struct {
		o JoinOptions
		w string
	}{{JoinOptions{RoomNumber: "１２３４５６"}, "INVALID_MESSAGE"}, {JoinOptions{RoomNumber: "abc123"}, "INVALID_MESSAGE"}, {JoinOptions{RoomNumber: missingNumber}, "ROOM_NOT_FOUND"}, {JoinOptions{RoomNumber: r.RoomNumber}, "PASSWORD_REQUIRED"}, {JoinOptions{RoomNumber: r.RoomNumber, Password: str("가 나 ")}, "PASSWORD_INVALID"}, {JoinOptions{RoomNumber: r.RoomNumber, Password: str(" 가 나 ")}, ""}} {
		_, e := m.Join(context.Background(), g, tt.o)
		code(t, e, tt.w)
	}
	ls, e := m.ListPublic(context.Background())
	if e != nil || len(ls) != 0 {
		t.Fatal(ls, e)
	}
}

func TestRequestDeadlineDuringHashCannotReplaceRejoin(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RequestTimeout = 250 * time.Millisecond
	cfg.HashWorkers = 1
	b := &barrier{entered: make(chan struct{}, 1), release: make(chan struct{})}
	cfg.hash = b.hash
	m := setup(t, cfg)
	t.Cleanup(func() {
		select {
		case <-b.release:
		default:
			close(b.release)
		}
	})
	private := create(t, m, connect(t, m, "private-host"), CreateOptions{Visibility: "private", Password: str("abcd")})
	host := connect(t, m, "public-host")
	public := create(t, m, host, CreateOptions{})
	guest := connect(t, m, "guest")
	b.armed.Store(true)
	pending := joinAsync(m, context.Background(), guest, private.RoomNumber)
	await(t, b.entered)
	err := awaitErr(t, pending)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("server request deadline returned %v, want context deadline exceeded", err)
	}
	code(t, err, "REQUEST_TIMEOUT")
	guest.mu.Lock()
	reserved := guest.room != nil || guest.joining
	guest.mu.Unlock()
	if reserved {
		t.Fatal("deadline left a membership or reservation")
	}
	joined, err := m.Join(context.Background(), guest, JoinOptions{RoomNumber: public.RoomNumber})
	if err != nil || len(joined.Members) != 2 || joined.HostID != host.ID() {
		t.Fatalf("rejoin failed: %+v %v", joined, err)
	}
	guest.mu.Lock()
	generation, token, currentRoom := guest.generation, guest.membershipToken, guest.room
	guest.mu.Unlock()
	b.armed.Store(false)
	close(b.release)
	// A successful private join on the single worker proves the expired hash has returned.
	if _, err := m.Join(context.Background(), connect(t, m, "probe"), JoinOptions{RoomNumber: private.RoomNumber, Password: str("abcd")}); err != nil {
		t.Fatal("worker did not finish the late hash before processing the next join", err)
	}
	rooms, err := m.ListPublic(context.Background())
	if err != nil || len(rooms) != 1 {
		t.Fatal(rooms, err)
	}
	got := rooms[0]
	if got.RoomID != public.RoomID || len(got.Members) != 2 || got.Members[1].SessionID != guest.ID() || got.HostID != host.ID() || got.Revision != joined.Revision {
		t.Fatalf("late hash changed the new membership: got=%+v joined=%+v", got, joined)
	}
	guest.mu.Lock()
	defer guest.mu.Unlock()
	if guest.room != currentRoom || guest.generation != generation || guest.membershipToken != token || guest.joining {
		t.Fatal("late hash changed the current session membership")
	}
}

func TestHostSuccessionAndEmptyRemoval(t *testing.T) {
	m := setup(t, DefaultConfig())
	h := connect(t, m, "h")
	r := create(t, m, h, CreateOptions{})
	a := connect(t, m, "a")
	b := connect(t, m, "b")
	for _, s := range []*Session{a, b} {
		_, e := m.Join(context.Background(), s, JoinOptions{RoomNumber: r.RoomNumber})
		if e != nil {
			t.Fatal(e)
		}
	}
	if e := m.Leave(context.Background(), h); e != nil {
		t.Fatal(e)
	}
	ls, _ := m.ListPublic(context.Background())
	if len(ls) != 1 || ls[0].HostID != a.ID() {
		t.Fatal(ls)
	}
	m.Disconnect(a)
	m.Disconnect(b)
	_, e := m.Join(context.Background(), h, JoinOptions{RoomNumber: r.RoomNumber})
	code(t, e, "ROOM_NOT_FOUND")
}
func TestLastSeatConcurrentJoin(t *testing.T) {
	m := setup(t, DefaultConfig())
	r := create(t, m, connect(t, m, "h"), CreateOptions{})
	for i := 0; i < 4; i++ {
		_, e := m.Join(context.Background(), connect(t, m, "existing"), JoinOptions{RoomNumber: r.RoomNumber})
		if e != nil {
			t.Fatal(e)
		}
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		s := connect(t, m, "race")
		go func() {
			<-start
			_, e := m.Join(context.Background(), s, JoinOptions{RoomNumber: r.RoomNumber})
			results <- e
		}()
	}
	close(start)
	counts := map[string]int{}
	for i := 0; i < 2; i++ {
		counts[CodeOf(<-results)]++
	}
	if counts[""] != 1 || counts["ROOM_FULL"] != 1 {
		t.Fatal(counts)
	}
	ls, _ := m.ListPublic(context.Background())
	if len(ls[0].Members) != 6 {
		t.Fatal(ls)
	}
}
func TestOneSessionConcurrentRooms(t *testing.T) {
	m := setup(t, DefaultConfig())
	a := create(t, m, connect(t, m, "a"), CreateOptions{})
	b := create(t, m, connect(t, m, "b"), CreateOptions{})
	s := connect(t, m, "s")
	var wg sync.WaitGroup
	res := make(chan error, 2)
	start := make(chan struct{})
	for _, r := range []Snapshot{a, b} {
		wg.Go(func() {
			<-start
			_, e := m.Join(context.Background(), s, JoinOptions{RoomNumber: r.RoomNumber})
			res <- e
		})
	}
	close(start)
	wg.Wait()
	c := map[string]int{}
	for i := 0; i < 2; i++ {
		c[CodeOf(<-res)]++
	}
	if c[""] != 1 || c["ALREADY_IN_ROOM"] != 1 {
		t.Fatal(c)
	}
}
func TestSnapshotsContainNoSecrets(t *testing.T) {
	cfg := DefaultConfig()
	cfg.hash = (&barrier{}).hash
	m := setup(t, cfg)
	h := connect(t, m, "h")
	secret := "secretword"
	r := create(t, m, h, CreateOptions{Visibility: "private", Password: &secret})
	r.Members[0].SessionID = "mutated"
	r.LastProcessedInputSeq["x"] = 7
	s := connect(t, m, "s")
	v, e := m.Join(context.Background(), s, JoinOptions{RoomNumber: r.RoomNumber, Password: &secret})
	if e != nil {
		t.Fatal(e)
	}
	if v.Members[0].SessionID != h.ID() || len(v.LastProcessedInputSeq) != 2 || v.LastProcessedInputSeq[h.ID()] != 0 || v.LastProcessedInputSeq[s.ID()] != 0 || v.LastProcessedInputSeq["x"] != 0 {
		t.Fatal(v)
	}
	for _, snap := range []Snapshot{v, (<-h.Events()).Snapshot} {
		raw, _ := json.Marshal(snap)
		for _, bad := range []string{secret, "password", "hash", "salt", "mutated"} {
			if strings.Contains(string(raw), bad) {
				t.Fatal(string(raw))
			}
		}
	}
}

func TestUnpublishedCreateCannotBeListedOrJoined(t *testing.T) {
	c := DefaultConfig()
	entered := make(chan struct{})
	release := make(chan struct{})
	c.beforeSubmit = func() { close(entered); <-release }
	m := setup(t, c)
	h := connect(t, m, "h")
	res := make(chan result, 1)
	go func() { v, e := m.Create(context.Background(), h, CreateOptions{}); res <- result{v, e} }()
	await(t, entered)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	ls, e := m.ListPublic(context.Background())
	if e != nil || len(ls) != 0 {
		t.Errorf("unpublished room visible: %v %v", ls, e)
	}
	m.mu.Lock()
	var number string
	for n := range m.rooms {
		number = n
	}
	m.mu.Unlock()
	_, e = m.Join(context.Background(), connect(t, m, "g"), JoinOptions{RoomNumber: number})
	code(t, e, "ROOM_NOT_FOUND")
	close(release)
	x := <-res
	if x.err != nil || x.snap.HostID != h.ID() {
		t.Fatal(x)
	}
}
func TestTimeoutAfterCommitCleansBeforeRejoin(t *testing.T) {
	c := DefaultConfig()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var armed atomic.Bool
	c.afterCommit = func() {
		if armed.Load() {
			entered <- struct{}{}
			<-release
		}
	}
	m := setup(t, c)
	a := create(t, m, connect(t, m, "a"), CreateOptions{})
	b := create(t, m, connect(t, m, "b"), CreateOptions{})
	s := connect(t, m, "s")
	armed.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() { _, e := m.Join(ctx, s, JoinOptions{RoomNumber: a.RoomNumber}); res <- e }()
	await(t, entered)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	cancel()
	_, e := m.Join(context.Background(), s, JoinOptions{RoomNumber: b.RoomNumber})
	code(t, e, "ALREADY_IN_ROOM")
	armed.Store(false)
	close(release)
	code(t, awaitErr(t, res), "REQUEST_TIMEOUT")
	_, e = m.Join(context.Background(), s, JoinOptions{RoomNumber: b.RoomNumber})
	if e != nil {
		t.Fatal(e)
	}
	ls, _ := m.ListPublic(context.Background())
	for _, r := range ls {
		if r.RoomID == a.RoomID && len(r.Members) != 1 {
			t.Fatal(ls)
		}
	}
}
func TestEventOverflowCleansActorWithoutDeadlock(t *testing.T) {
	c := DefaultConfig()
	c.EventQueue = 1
	m := setup(t, c)
	h := connect(t, m, "h")
	r := create(t, m, h, CreateOptions{})
	s := connect(t, m, "s")
	_, e := m.Join(context.Background(), s, JoinOptions{RoomNumber: r.RoomNumber})
	if e != nil {
		t.Fatal(e)
	}
	await(t, h.Done())
	ls, e := m.ListPublic(context.Background())
	if e != nil || len(ls) != 0 {
		t.Fatal(ls, e)
	}
	await(t, s.Done())
}
func TestForeignSessionAndResourceBounds(t *testing.T) {
	c := DefaultConfig()
	c.MaxSessions = 2
	c.MaxRooms = 1
	c.number = func() (string, error) { return "000009", nil }
	m := setup(t, c)
	foreign := setup(t, DefaultConfig())
	s := connect(t, foreign, "foreign")
	_, e := m.Create(context.Background(), s, CreateOptions{})
	code(t, e, "SESSION_CLOSED")
	h := connect(t, m, "h")
	r := create(t, m, h, CreateOptions{})
	if r.RoomNumber != "000009" {
		t.Fatal(r)
	}
	g := connect(t, m, "g")
	_, e = m.Connect("extra")
	code(t, e, "SERVER_BUSY")
	_, e = m.Create(context.Background(), g, CreateOptions{})
	code(t, e, "SERVER_BUSY")
	m.Disconnect(h)
	create(t, m, g, CreateOptions{})
}

func TestCanceledCommittedJoinCannotRemoveRejoin(t *testing.T) {
	cfg := DefaultConfig()
	var armed atomic.Bool
	committed, releaseCommit := make(chan struct{}), make(chan struct{})
	cancelCaptured, releaseCancel := make(chan struct{}), make(chan struct{})
	cfg.afterCommit = func() {
		if armed.Load() {
			close(committed)
			<-releaseCommit
		}
	}
	cfg.beforeCancelCleanup = func() {
		if armed.Load() {
			close(cancelCaptured)
			<-releaseCancel
		}
	}
	m := setup(t, cfg)
	t.Cleanup(func() {
		for _, ch := range []chan struct{}{releaseCommit, releaseCancel} {
			select {
			case <-ch:
			default:
				close(ch)
			}
		}
	})
	host := connect(t, m, "host")
	room := create(t, m, host, CreateOptions{})
	guest := connect(t, m, "guest")
	armed.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := make(chan error, 1)
	go func() { _, err := m.Join(ctx, guest, JoinOptions{RoomNumber: room.RoomNumber}); old <- err }()
	await(t, committed)
	guest.mu.Lock()
	oldToken := guest.membershipToken
	guest.mu.Unlock()
	cancel()
	await(t, cancelCaptured)
	armed.Store(false)
	close(releaseCommit)
	if err := m.Leave(context.Background(), guest); err != nil {
		t.Fatal(err)
	}
	joined, err := m.Join(context.Background(), guest, JoinOptions{RoomNumber: room.RoomNumber})
	if err != nil || len(joined.Members) != 2 || joined.HostID != host.ID() {
		t.Fatalf("rejoin failed: %+v %v", joined, err)
	}
	guest.mu.Lock()
	generation := guest.generation
	token := guest.membershipToken
	currentRoom := guest.room
	guest.mu.Unlock()
	if token == 0 || token == oldToken {
		t.Fatal("rejoin reused the previous membership token")
	}
	close(releaseCancel)
	code(t, awaitErr(t, old), "REQUEST_TIMEOUT")
	rooms, err := m.ListPublic(context.Background())
	if err != nil || len(rooms) != 1 {
		t.Fatal(err, rooms)
	}
	got := rooms[0]
	if len(got.Members) != 2 || got.Members[1].SessionID != guest.ID() {
		t.Fatalf("old canceled generation removed newer successful membership: members=%v", got.Members)
	}
	if got.HostID != host.ID() || got.Revision != joined.Revision {
		t.Fatalf("stale cleanup changed host/revision: got=%+v joined=%+v", got, joined)
	}
	guest.mu.Lock()
	defer guest.mu.Unlock()
	if guest.room != currentRoom || guest.generation != generation || guest.membershipToken != token {
		t.Fatal("stale cleanup changed current session membership")
	}
}
