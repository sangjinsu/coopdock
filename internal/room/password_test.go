package room

import (
	"bytes"
	"context"
	"crypto/sha256"
	"sync/atomic"
	"testing"
	"time"
)

func TestRealArgon2id(t *testing.T) {
	m := setup(t, DefaultConfig())
	session := connect(t, m, "argon")
	a, e := m.passwordContext(context.Background(), session, " 가 나 ")
	if e != nil {
		t.Fatal(e)
	}
	b, e := m.passwordContext(context.Background(), session, " 가 나 ")
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Equal(a.salt, b.salt) || bytes.Equal(a.hash, b.hash) || len(a.salt) != 16 || len(a.hash) != 32 {
		t.Fatal("salt/hash dimensions or uniqueness")
	}
	exact, exactErr := m.verifyContext(context.Background(), session, " 가 나 ", a)
	different, differentErr := m.verifyContext(context.Background(), session, "가 나 ", a)
	if !exact || different || exactErr != nil || differentErr != nil {
		t.Fatal("exact password comparison")
	}
}

type barrier struct {
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (b *barrier) hash(p, s []byte) []byte {
	if b.armed.Load() {
		b.entered <- struct{}{}
		<-b.release
	}
	x := sha256.Sum256(append(append([]byte{}, p...), s...))
	return x[:]
}
func barrierManager(t *testing.T) (*Manager, *barrier, Snapshot, *Session) {
	t.Helper()
	b := &barrier{entered: make(chan struct{}, 16), release: make(chan struct{})}
	c := DefaultConfig()
	c.hash = b.hash
	m := setup(t, c)
	h := connect(t, m, "host")
	r := create(t, m, h, CreateOptions{Visibility: "private", Password: str("abcd")})
	b.armed.Store(true)
	t.Cleanup(func() {
		select {
		case <-b.release:
		default:
			close(b.release)
		}
	})
	return m, b, r, h
}
func await(t *testing.T, c <-chan struct{}) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(time.Second):
		t.Fatal("barrier did not arrive")
	}
}
func awaitErr(t *testing.T, c <-chan error) error {
	t.Helper()
	select {
	case e := <-c:
		return e
	case <-time.After(time.Second):
		t.Fatal("operation did not return")
		return nil
	}
}
func joinAsync(m *Manager, ctx context.Context, s *Session, n string) <-chan error {
	ch := make(chan error, 1)
	go func() { _, e := m.Join(ctx, s, JoinOptions{RoomNumber: n, Password: str("abcd")}); ch <- e }()
	return ch
}
func TestCanceledHashCannotJoin(t *testing.T) {
	for _, mode := range []string{"timeout", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			m, b, r, _ := barrierManager(t)
			s := connect(t, m, "s")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			res := joinAsync(m, ctx, s, r.RoomNumber)
			await(t, b.entered)
			if mode == "timeout" {
				cancel()
			} else {
				m.Disconnect(s)
			}
			e := awaitErr(t, res)
			if mode == "timeout" {
				code(t, e, "REQUEST_TIMEOUT")
			} else {
				code(t, e, "SESSION_CLOSED")
			}
			close(b.release)
			b.armed.Store(false)
			s.mu.Lock()
			if s.room != nil || s.joining {
				t.Error("canceled membership")
			}
			s.mu.Unlock()
		})
	}
}
func TestOldGenerationCannotReplaceNewJoin(t *testing.T) {
	m, b, r, _ := barrierManager(t)
	other := create(t, m, connect(t, m, "other"), CreateOptions{})
	s := connect(t, m, "s")
	ctx, cancel := context.WithCancel(context.Background())
	res := joinAsync(m, ctx, s, r.RoomNumber)
	await(t, b.entered)
	cancel()
	code(t, awaitErr(t, res), "REQUEST_TIMEOUT")
	_, e := m.Join(context.Background(), s, JoinOptions{RoomNumber: other.RoomNumber})
	if e != nil {
		t.Fatal(e)
	}
	close(b.release)
	s.mu.Lock()
	if s.room == nil || s.room.id != other.RoomID {
		t.Error("new membership overwritten")
	}
	s.mu.Unlock()
}
func TestNumberReuseBindsRoomID(t *testing.T) {
	c := DefaultConfig()
	c.number = func() (string, error) { return "000001", nil }
	b := &barrier{entered: make(chan struct{}, 2), release: make(chan struct{})}
	c.hash = b.hash
	m := setup(t, c)
	h := connect(t, m, "h")
	r := create(t, m, h, CreateOptions{Visibility: "private", Password: str("abcd")})
	b.armed.Store(true)
	t.Cleanup(func() {
		select {
		case <-b.release:
		default:
			close(b.release)
		}
	})
	s := connect(t, m, "s")
	res := joinAsync(m, context.Background(), s, r.RoomNumber)
	await(t, b.entered)
	m.Disconnect(h)
	newRoom := create(t, m, connect(t, m, "new"), CreateOptions{})
	if newRoom.RoomID == r.RoomID {
		t.Fatal("room ID reused")
	}
	close(b.release)
	code(t, awaitErr(t, res), "ROOM_CLOSED")
	ls, _ := listForTest(t, m, context.Background())
	if len(ls) != 1 || len(ls[0].Members) != 1 {
		t.Fatal(ls)
	}
}
func TestCloseRejectsPendingAdmission(t *testing.T) {
	for _, operation := range []string{"join", "create"} {
		t.Run(operation, func(t *testing.T) {
			m, b, r, _ := barrierManager(t)
			s := connect(t, m, "s")
			var res <-chan error
			if operation == "join" {
				res = joinAsync(m, context.Background(), s, r.RoomNumber)
			} else {
				ch := make(chan error, 1)
				go func() {
					_, e := m.Create(context.Background(), s, CreateOptions{Visibility: "private", Password: str("abcd")})
					ch <- e
				}()
				res = ch
			}
			await(t, b.entered)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			e := m.Close(ctx)
			if e == nil {
				t.Fatal("Close returned while actual hash still running")
			}
			if m.Ready() {
				t.Fatal("admission remains open")
			}
			pendingErr := awaitErr(t, res)
			if c := CodeOf(pendingErr); c != "SERVER_CLOSING" && c != "SESSION_CLOSED" {
				t.Fatal(pendingErr)
			}
			close(b.release)
			if e := m.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestHashQueueFull(t *testing.T) {
	c := DefaultConfig()
	c.HashWorkers = 1
	c.HashQueue = 1
	b := &barrier{entered: make(chan struct{}, 4), release: make(chan struct{})}
	c.hash = b.hash
	m := setup(t, c)
	r := create(t, m, connect(t, m, "h"), CreateOptions{Visibility: "private", Password: str("abcd")})
	b.armed.Store(true)
	t.Cleanup(func() {
		select {
		case <-b.release:
		default:
			close(b.release)
		}
	})
	first := joinAsync(m, context.Background(), connect(t, m, "one"), r.RoomNumber)
	await(t, b.entered)
	second := joinAsync(m, context.Background(), connect(t, m, "two"), r.RoomNumber)
	deadline := time.Now().Add(time.Second)
	for len(m.hashJobs) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(m.hashJobs) != 1 {
		t.Fatal("queue not filled")
	}
	_, e := m.Join(context.Background(), connect(t, m, "three"), JoinOptions{RoomNumber: r.RoomNumber, Password: str("abcd")})
	code(t, e, "SERVER_BUSY")
	close(b.release)
	if e := awaitErr(t, first); e != nil {
		t.Fatal(e)
	}
	if e := awaitErr(t, second); e != nil {
		t.Fatal(e)
	}
}
func TestFailureLimitsExpire(t *testing.T) {
	for _, kind := range []string{"IP", "session", "room"} {
		t.Run(kind, func(t *testing.T) {
			c := DefaultConfig()
			c.IPFailureLimit = 100
			c.SessionFailureLimit = 100
			c.RoomFailureLimit = 100
			switch kind {
			case "IP":
				c.IPFailureLimit = 1
			case "session":
				c.SessionFailureLimit = 1
			case "room":
				c.RoomFailureLimit = 1
			}
			var now atomic.Int64
			now.Store(time.Now().UnixNano())
			c.now = func() time.Time { return time.Unix(0, now.Load()) }
			c.hash = func(p, s []byte) []byte { x := sha256.Sum256(append(append([]byte{}, p...), s...)); return x[:] }
			m := setup(t, c)
			r := create(t, m, connect(t, m, "host"), CreateOptions{Visibility: "private", Password: str("abcd")})
			s := connect(t, m, "peer")
			_, e := m.Join(context.Background(), s, JoinOptions{RoomNumber: r.RoomNumber, Password: str("wrong")})
			code(t, e, "PASSWORD_INVALID")
			if kind != "session" {
				s = connect(t, m, "peer")
				if kind == "room" {
					s = connect(t, m, "different")
				}
			}
			_, e = m.Join(context.Background(), s, JoinOptions{RoomNumber: r.RoomNumber, Password: str("abcd")})
			code(t, e, "RATE_LIMITED")
			now.Add(int64(c.FailureTTL))
			_, e = m.Join(context.Background(), s, JoinOptions{RoomNumber: r.RoomNumber, Password: str("abcd")})
			if e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestCreateCancellationReleasesNumber(t *testing.T) {
	m, b, _, _ := barrierManager(t)
	s := connect(t, m, "s")
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan error, 1)
	go func() { _, e := m.Create(ctx, s, CreateOptions{Visibility: "private", Password: str("abcd")}); ch <- e }()
	await(t, b.entered)
	cancel()
	code(t, awaitErr(t, ch), "REQUEST_TIMEOUT")
	other := create(t, m, s, CreateOptions{})
	if other.RoomID == "" {
		t.Fatal(other)
	}
	close(b.release)
	m.mu.Lock()
	n := len(m.rooms)
	m.mu.Unlock()
	if n != 2 {
		t.Fatal(n)
	}
}

func TestQueuedCancellationSkipsHashAndRunningPermitStaysHeld(t *testing.T) {
	c := DefaultConfig()
	c.HashWorkers = 1
	c.HashQueue = 1
	var calls atomic.Int64
	b := &barrier{entered: make(chan struct{}, 8), release: make(chan struct{})}
	c.hash = func(p, s []byte) []byte { calls.Add(1); return b.hash(p, s) }
	m := setup(t, c)
	r := create(t, m, connect(t, m, "h"), CreateOptions{Visibility: "private", Password: str("abcd")})
	b.armed.Store(true)
	t.Cleanup(func() {
		select {
		case <-b.release:
		default:
			close(b.release)
		}
	})
	ctx1, cancel1 := context.WithCancel(context.Background())
	first := joinAsync(m, ctx1, connect(t, m, "one"), r.RoomNumber)
	await(t, b.entered)
	cancel1()
	code(t, awaitErr(t, first), "REQUEST_TIMEOUT")
	ctx2, cancel2 := context.WithCancel(context.Background())
	second := joinAsync(m, ctx2, connect(t, m, "two"), r.RoomNumber)
	deadline := time.Now().Add(time.Second)
	for len(m.hashJobs) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(m.hashJobs) != 1 {
		t.Fatal("queue not filled")
	}
	cancel2()
	code(t, awaitErr(t, second), "REQUEST_TIMEOUT")
	_, e := m.Join(context.Background(), connect(t, m, "three"), JoinOptions{RoomNumber: r.RoomNumber, Password: str("abcd")})
	code(t, e, "SERVER_BUSY")
	b.armed.Store(false)
	close(b.release)
	deadline = time.Now().Add(time.Second)
	for len(m.hashJobs) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	_, e = m.Join(context.Background(), connect(t, m, "four"), JoinOptions{RoomNumber: r.RoomNumber, Password: str("abcd")})
	if e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 3 {
		t.Fatalf("canceled queued hash ran: calls=%d", calls.Load())
	}
}
func TestFailureKeysStayBoundedAndRoomReuseIsIndependent(t *testing.T) {
	c := DefaultConfig()
	c.MaxFailureKeys = 3
	c.RoomFailureLimit = 1
	c.IPFailureLimit = 100
	c.SessionFailureLimit = 100
	c.number = func() (string, error) { return "000007", nil }
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	c.now = func() time.Time { return time.Unix(0, now.Load()) }
	c.hash = func(p, s []byte) []byte { x := sha256.Sum256(append(append([]byte{}, p...), s...)); return x[:] }
	m := setup(t, c)
	h := connect(t, m, "h")
	r := create(t, m, h, CreateOptions{Visibility: "private", Password: str("abcd")})
	s := connect(t, m, "s")
	_, e := m.Join(context.Background(), s, JoinOptions{RoomNumber: r.RoomNumber, Password: str("wrong")})
	code(t, e, "PASSWORD_INVALID")
	m.Disconnect(h)
	r = create(t, m, connect(t, m, "newhost"), CreateOptions{Visibility: "private", Password: str("abcd")})
	_, e = m.Join(context.Background(), s, JoinOptions{RoomNumber: r.RoomNumber, Password: str("abcd")})
	if e != nil {
		t.Fatal("old room limiter affected reused number", e)
	}
	if e = m.Leave(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	_, e = m.Join(context.Background(), connect(t, m, "another"), JoinOptions{RoomNumber: r.RoomNumber, Password: str("wrong")})
	code(t, e, "PASSWORD_INVALID")
	m.limits.mu.Lock()
	n := len(m.limits.entries)
	m.limits.mu.Unlock()
	if n > 3 {
		t.Fatal(n)
	}
	_, e = m.Join(context.Background(), s, JoinOptions{RoomNumber: r.RoomNumber, Password: str("abcd")})
	code(t, e, "RATE_LIMITED")
	now.Add(int64(c.FailureTTL))
	_, e = m.Join(context.Background(), s, JoinOptions{RoomNumber: r.RoomNumber, Password: str("abcd")})
	if e != nil {
		t.Fatal(e)
	}
}

func TestShutdownDropsQueuedPasswords(t *testing.T) {
	c := DefaultConfig()
	c.HashWorkers = 1
	c.HashQueue = 1
	b := &barrier{entered: make(chan struct{}, 4), release: make(chan struct{})}
	c.hash = b.hash
	m := setup(t, c)
	r := create(t, m, connect(t, m, "h"), CreateOptions{Visibility: "private", Password: str("abcd")})
	b.armed.Store(true)
	t.Cleanup(func() {
		select {
		case <-b.release:
		default:
			close(b.release)
		}
	})
	first := joinAsync(m, context.Background(), connect(t, m, "one"), r.RoomNumber)
	await(t, b.entered)
	second := joinAsync(m, context.Background(), connect(t, m, "two"), r.RoomNumber)
	deadline := time.Now().Add(time.Second)
	for len(m.hashJobs) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(m.hashJobs) != 1 {
		t.Fatal("queue not filled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if e := m.Close(ctx); e == nil {
		t.Fatal("running work not tracked")
	}
	_ = awaitErr(t, first)
	_ = awaitErr(t, second)
	close(b.release)
	if e := m.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	if len(m.hashJobs) != 0 {
		t.Fatal("shutdown retained plaintext queue jobs")
	}
}

func TestPendingJoinSeesRoomClosedAfterLastHostLeaves(t *testing.T) {
	m, b, r, h := barrierManager(t)
	s := connect(t, m, "pending")
	res := joinAsync(m, context.Background(), s, r.RoomNumber)
	await(t, b.entered)
	m.Disconnect(h)
	close(b.release)
	code(t, awaitErr(t, res), "ROOM_CLOSED")
	_, err := m.Join(context.Background(), connect(t, m, "newguest"), JoinOptions{RoomNumber: r.RoomNumber, Password: str("abcd")})
	code(t, err, "ROOM_NOT_FOUND")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.room != nil || s.joining {
		t.Fatal("closed room left a membership or reservation")
	}
}
