package coopdock

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sangjinsu/coopdock/internal/admin"
	"github.com/sangjinsu/coopdock/internal/observability"
	"github.com/sangjinsu/coopdock/internal/room"
	"github.com/sangjinsu/coopdock/internal/transport"
)

// Server owns one independent set of rooms, sessions, connections, and workers.
// Construct it with New. The zero value is not usable.
type Server struct {
	handler         http.Handler
	transport       *transport.Server
	collector       *observability.Collector
	shutdownTimeout time.Duration
	stopOnce        sync.Once
	done            chan struct{}
}

type dependencies struct {
	manager   func(room.Config) (*room.Manager, error)
	transport func(*room.Manager, transport.Config) (*transport.Server, error)
	collector func(context.Context, observability.Source, observability.Config) (*observability.Collector, error)
	dashboard func(admin.Config, *observability.Collector) (http.Handler, error)
}

func defaultDependencies() dependencies {
	return dependencies{room.NewManager, transport.New, observability.NewOwned, admin.New}
}

// New validates all settings before starting workers and copies configuration slices.
// It opens no listener and reads no environment variables or process signals.
// An enabled dashboard has a valid initial cache before New returns.
func New(c Config) (*Server, error) { return newWithDependencies(c, defaultDependencies()) }

func newWithDependencies(c Config, d dependencies) (result *Server, err error) {
	if err = c.Validate(); err != nil {
		return nil, err
	}
	var m *room.Manager
	s := &Server{shutdownTimeout: c.Transport.ShutdownTimeout, done: make(chan struct{})}
	defer func() {
		if err == nil {
			return
		}
		// Each acquired service remains owned even when its constructor reports failure.
		if s.transport != nil {
			s.beginShutdown()
			<-s.done
		} else if m != nil {
			for m.Close(context.Background()) != nil {
			}
		}
	}()
	m, err = d.manager(c.Room.internal())
	if err != nil {
		return nil, err
	}
	s.transport, err = d.transport(m, c.Transport.internal())
	if err != nil {
		return nil, err
	}
	if c.Admin.ViewerToken != "" {
		startup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		s.collector, err = d.collector(startup, observability.Source{Rooms: m.OperationsSnapshot, Transport: s.transport.LiveStatus}, observability.DefaultConfig())
		cancel()
		if err != nil {
			return nil, err
		}
	}
	dashboard, err := d.dashboard(admin.Config{ViewerToken: c.Admin.ViewerToken}, s.collector)
	if err != nil {
		return nil, err
	}
	public := s.transport.Handler()
	s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Dispatch before ServeMux can canonicalize an admin-prefixed path.
		if r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/") {
			dashboard.ServeHTTP(w, r)
			return
		}
		public.ServeHTTP(w, r)
	})
	return s, nil
}

// Handler returns the stable HTTP handler for this instance. The host owns its
// listener and must dispatch raw admin paths before any outer mux normalization.
// Dashboard assets use absolute /admin paths even when this handler is mounted.
func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) beginShutdown() {
	s.stopOnce.Do(func() {
		if s.collector != nil {
			s.collector.MarkStopping()
		}
		// Close establishes the admission fence synchronously, even for a canceled caller.
		// No cleanup completion is awaited inside Once; each caller has its own deadline.
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		_ = s.transport.Close(canceled)
		if s.collector != nil {
			_ = s.collector.Close(canceled)
		}
		go func() {
			for s.transport.Close(context.Background()) != nil {
			}
			if s.collector != nil {
				_ = s.collector.Close(context.Background())
			}
			close(s.done)
		}()
	})
}

// Shutdown closes this instance's admission and waits for its owned resources.
// It never closes the host HTTP server or listener. Supply a non-nil context.
// Waiting is bounded by ctx and Transport.ShutdownTimeout; cleanup continues after
// a timeout or cancellation. A subsequent call can wait again. Once cleanup is
// complete, all subsequent calls return nil, including calls with canceled contexts.
func (s *Server) Shutdown(ctx context.Context) error {
	s.beginShutdown()
	select {
	case <-s.done:
		return nil
	default:
	}
	waiting, cancel := context.WithTimeout(ctx, s.shutdownTimeout)
	defer cancel()
	select {
	case <-s.done:
		return nil
	case <-waiting.Done():
		select {
		case <-s.done:
			return nil
		default:
			return waiting.Err()
		}
	}
}
