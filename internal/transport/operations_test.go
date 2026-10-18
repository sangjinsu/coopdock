package transport

import (
	"context"
	"errors"
	"net/http"
	"github.com/sangjinsu/coopdock/internal/room"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func assertLiveStatus(t *testing.T, s *Server, want int, stopping bool) {
	t.Helper()
	n, closing := s.LiveStatus()
	if n != want || closing != stopping {
		t.Fatalf("live status=(%d,%v), want (%d,%v)", n, closing, want, stopping)
	}
}

func TestLiveStatusZeroBeforeConnections(t *testing.T) {
	m, err := room.NewManager(room.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(m, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	assertLiveStatus(t, s, 0, false)
	if _, err := m.Connect("pre-upgrade"); err != nil {
		t.Fatal(err)
	}
	assertLiveStatus(t, s, 0, false)
}

func TestLiveStatusAcceptanceAndLeave(t *testing.T) {
	s, _, h, _ := setup(t, DefaultConfig())
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	registered := make(chan *peer, 1)
	s.mu.Lock()
	s.beforeAccept = func() { close(entered); <-release }
	s.afterRegister = func(p *peer) { registered <- p }
	s.mu.Unlock()
	type result struct {
		c   *websocket.Conn
		err error
	}
	dialed := make(chan result, 1)
	go func() { c, _, err := websocket.Dial(context.Background(), h.URL+"/ws", nil); dialed <- result{c, err} }()
	waitDone(t, entered)
	assertLiveStatus(t, s, 0, false)
	releaseOnce.Do(func() { close(release) })
	r := <-dialed
	if r.err != nil {
		t.Fatal(r.err)
	}
	defer r.c.CloseNow()
	p := <-registered
	c := &client{t: t, c: r.c}
	c.read()
	assertLiveStatus(t, s, 1, false)
	c.success("room.create", map[string]any{})
	c.success("room.leave", map[string]any{})
	assertLiveStatus(t, s, 1, false)
	p.stop()
	p.stop()
	assertLiveStatus(t, s, 0, false)
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertLiveStatus(t, s, 0, true)
}

func TestLiveStatusFailedAccept(t *testing.T) {
	s, _, h, _ := setup(t, DefaultConfig())
	r, err := http.Get(h.URL + "/ws")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode == http.StatusSwitchingProtocols {
		t.Fatal("unexpected upgrade")
	}
	assertLiveStatus(t, s, 0, false)
}

func TestLiveStatusClosingBeforeRegistration(t *testing.T) {
	s, _, h, _ := setup(t, DefaultConfig())
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	s.mu.Lock()
	s.beforeAccept = func() { close(entered); <-release }
	s.mu.Unlock()
	dialed := make(chan struct{})
	go func() {
		defer close(dialed)
		c, _, _ := websocket.Dial(context.Background(), h.URL+"/ws", nil)
		if c != nil {
			c.CloseNow()
		}
	}()
	waitDone(t, entered)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := s.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close error: %v", err)
	}
	assertLiveStatus(t, s, 0, true)
	releaseOnce.Do(func() { close(release) })
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitDone(t, dialed)
	assertLiveStatus(t, s, 0, true)
}

func TestLiveStatusConcurrentStopAndClose(t *testing.T) {
	for _, order := range []string{"stop-first", "close-first", "simultaneous"} {
		t.Run(order, func(t *testing.T) {
			for i := 0; i < 100; i++ {
				func() {
					s, _, h, _ := setup(t, DefaultConfig())
					registered := make(chan *peer, 1)
					release := make(chan struct{})
					var releaseOnce sync.Once
					t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
					s.mu.Lock()
					s.afterRegister = func(p *peer) { registered <- p; <-release }
					s.mu.Unlock()
					c, _, err := websocket.Dial(context.Background(), h.URL+"/ws", nil)
					if err != nil {
						t.Fatal(err)
					}
					p := <-registered
					assertLiveStatus(t, s, 1, false)
					stopped := make(chan struct{})
					closed := make(chan error, 1)
					switch order {
					case "stop-first":
						p.stop()
						close(stopped)
						go func() { closed <- s.Close(context.Background()) }()
					case "close-first":
						go func() { closed <- s.Close(context.Background()) }()
						waitDone(t, p.ctx.Done())
						go func() { p.stop(); close(stopped) }()
					default:
						go func() { p.stop(); close(stopped) }()
						go func() { closed <- s.Close(context.Background()) }()
					}
					waitDone(t, stopped)
					s.mu.Lock()
					registeredCount := len(s.peers)
					s.mu.Unlock()
					if registeredCount != 1 {
						t.Fatalf("registry cleanup completed before blocked accept: %d", registeredCount)
					}
					n, _ := s.LiveStatus()
					if n != 0 {
						t.Fatalf("iteration %d: count=%d", i, n)
					}
					releaseOnce.Do(func() { close(release) })
					if err := <-closed; err != nil {
						t.Fatal(err)
					}
					p.stop()
					assertLiveStatus(t, s, 0, true)
					c.CloseNow()
					h.Close()
				}()
			}
		})
	}
}
