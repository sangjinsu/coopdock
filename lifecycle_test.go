package coopdock

import (
	"bufio"
	"context"
	"errors"
	"github.com/coder/websocket"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sangjinsu/coopdock/internal/admin"
	"github.com/sangjinsu/coopdock/internal/observability"
	"github.com/sangjinsu/coopdock/internal/room"
	"github.com/sangjinsu/coopdock/internal/transport"
)

func waitSignal(t *testing.T, c <-chan struct{}) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(3 * time.Second):
		t.Fatal("signal timeout")
	}
}
func TestNewValidationBeforeWorkers(t *testing.T) {
	for _, bad := range []func(*Config){func(c *Config) { c.Room.MaxRooms = 0 }, func(c *Config) { c.Transport.IngressQueue = 0 }, func(c *Config) { c.Admin.ViewerToken = "secret" }} {
		cfg := DefaultConfig()
		bad(&cfg)
		d := defaultDependencies()
		starts := 0
		d.manager = func(c room.Config) (*room.Manager, error) { starts++; return room.NewManager(c) }
		d.collector = func(ctx context.Context, s observability.Source, c observability.Config) (*observability.Collector, error) {
			starts++
			return observability.NewOwned(ctx, s, c)
		}
		s, err := newWithDependencies(cfg, d)
		if s != nil {
			_ = s.Shutdown(context.Background())
		}
		if err == nil || starts != 0 {
			t.Errorf("validation after workers: err=%v starts=%d", err, starts)
		}
	}
}
func TestNewRollback(t *testing.T) {
	sentinel := errors.New("injected construction failure")
	for _, stage := range []string{"transport", "collector", "admin"} {
		t.Run(stage, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Admin.ViewerToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			d := defaultDependencies()
			var m *room.Manager
			var tr *transport.Server
			var col *observability.Collector
			d.manager = func(c room.Config) (*room.Manager, error) { var err error; m, err = room.NewManager(c); return m, err }
			d.transport = func(manager *room.Manager, c transport.Config) (*transport.Server, error) {
				if stage == "transport" {
					return nil, sentinel
				}
				var err error
				tr, err = transport.New(manager, c)
				return tr, err
			}
			d.collector = func(ctx context.Context, source observability.Source, c observability.Config) (*observability.Collector, error) {
				var err error
				col, err = observability.NewOwned(ctx, source, c)
				if err != nil {
					return col, err
				}
				if stage == "collector" {
					return col, sentinel
				}
				return col, nil
			}
			d.dashboard = func(c admin.Config, col *observability.Collector) (http.Handler, error) { return nil, sentinel }
			server, err := newWithDependencies(cfg, d)
			if server != nil {
				_ = server.Shutdown(context.Background())
				t.Fatal("failed construction returned server")
			}
			if !errors.Is(err, sentinel) {
				t.Fatalf("lost error: %v", err)
			}
			if m == nil || m.Ready() {
				t.Fatal("manager still accepting after rollback")
			}
			if err := m.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if tr != nil {
				active, stopping := tr.LiveStatus()
				if active != 0 || !stopping {
					t.Fatal("transport rollback incomplete")
				}
			}
			if col != nil {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if err := col.Close(ctx); err != nil {
					t.Fatalf("collector worker left by rollback: %v", err)
				}
			}
		})
	}
}

// A blocked real collector makes completion observably distinct from stop initiation.
func blockedCollectorServer(t *testing.T) (*Server, func()) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Admin.ViewerToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cfg.Transport.ShutdownTimeout = 2 * time.Second
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	releaseFn := func() { once.Do(func() { close(release) }) }
	t.Cleanup(releaseFn)
	d := defaultDependencies()
	d.collector = func(ctx context.Context, source observability.Source, c observability.Config) (*observability.Collector, error) {
		calls := 0
		original := source.Rooms
		source.Rooms = func() room.OperationsSnapshot {
			calls++
			if calls == 2 {
				close(entered)
				<-release
			}
			return original()
		}
		c.Interval = time.Millisecond
		return observability.NewOwned(ctx, source, c)
	}
	s, err := newWithDependencies(cfg, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		releaseFn()
		if err := s.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	waitSignal(t, entered)
	return s, releaseFn
}
func TestShutdownLifecycle(t *testing.T) {
	s, release := blockedCollectorServer(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Shutdown(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled first call: %v", err)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/ws", nil))
	if w.Code != 503 {
		t.Fatalf("admission alive after canceled return: %d", w.Code)
	}
	longDone := make(chan error, 1)
	go func() { longDone <- s.Shutdown(context.Background()) }()
	short, cancelShort := context.WithCancel(context.Background())
	shortDone := make(chan error, 1)
	go func() { shortDone <- s.Shutdown(short) }()
	cancelShort()
	select {
	case err := <-shortDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("caller stuck in once cleanup")
	}
	select {
	case err := <-longDone:
		t.Fatalf("cleanup returned before worker completed: %v", err)
	default:
	}
	deadline, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	if err := s.Shutdown(deadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("independent deadline: %v", err)
	}
	release()
	select {
	case err := <-longDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("background cleanup did not finish")
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Shutdown(canceled); err != nil {
		t.Fatalf("completed canceled retry: %v", err)
	}
}
func TestNewStartupFailureWaitsForCollector(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Admin.ViewerToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	d := defaultDependencies()
	entered, release := make(chan struct{}), make(chan struct{})
	startupFailed := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var collector *observability.Collector
	d.collector = func(_ context.Context, source observability.Source, c observability.Config) (*observability.Collector, error) {
		source.Rooms = func() room.OperationsSnapshot { close(entered); <-release; return room.OperationsSnapshot{Ready: true} }
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { <-entered; cancel() }()
		var err error
		collector, err = observability.NewOwned(ctx, source, c)
		close(startupFailed)
		return collector, err
	}
	type result struct {
		s   *Server
		err error
	}
	done := make(chan result, 1)
	go func() { s, err := newWithDependencies(cfg, d); done <- result{s, err} }()
	waitSignal(t, entered)
	waitSignal(t, startupFailed)
	once.Do(func() { close(release) })
	select {
	case r := <-done:
		if r.s != nil || !errors.Is(r.err, context.Canceled) {
			t.Fatalf("startup result: %v %v", r.s, r.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("rollback blocked")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if collector == nil {
		t.Fatal("no owned collector")
	}
	if err := collector.Close(ctx); err != nil {
		t.Fatalf("startup leaked collector: %v", err)
	}
}

func TestShutdownConfiguredTimeoutContinuesCleanup(t *testing.T) {
	s, release := blockedCollectorServer(t)
	s.shutdownTimeout = 20 * time.Millisecond
	result := make(chan error, 1)
	go func() { result <- s.Shutdown(context.Background()) }()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("configured timeout: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("configured shutdown bound ignored")
	}
	release()
	waitSignal(t, s.done)
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Preserve the real writer's Hijacker while pausing an accepted HTTP upgrade.
type pausedUpgradeWriter struct {
	http.ResponseWriter
	entered, release chan struct{}
	once             sync.Once
}

func (w *pausedUpgradeWriter) Header() http.Header {
	w.once.Do(func() { close(w.entered); <-w.release })
	return w.ResponseWriter.Header()
}
func (w *pausedUpgradeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.ResponseWriter.(http.Hijacker).Hijack()
}
func TestShutdownPendingUpgrade(t *testing.T) {
	s, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseFn := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() {
		releaseFn()
		if err := s.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(returned)
		s.Handler().ServeHTTP(&pausedUpgradeWriter{ResponseWriter: w, entered: entered, release: release}, r)
	}))
	defer host.Close()
	defer releaseFn()
	dialDone := make(chan struct{})
	go func() {
		defer close(dialDone)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		c, _, _ := websocket.Dial(ctx, "ws"+strings.TrimPrefix(host.URL, "http")+"/ws", nil)
		if c != nil {
			_ = c.CloseNow()
		}
	}()
	waitSignal(t, entered)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("pending accept shutdown: %v", err)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/ws", nil))
	if w.Code != 503 {
		t.Fatal("new admission allowed")
	}
	releaseFn()
	waitSignal(t, returned)
	waitSignal(t, dialDone)
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if active, stopping := s.transport.LiveStatus(); active != 0 || !stopping {
		t.Fatalf("pending upgrade leaked: %d %v", active, stopping)
	}
}
