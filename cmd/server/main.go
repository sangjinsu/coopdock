package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"roomserver/internal/admin"
	"roomserver/internal/config"
	"roomserver/internal/observability"
	"roomserver/internal/room"
	"roomserver/internal/transport"
	"strings"
	"syscall"
	"time"
)

func main() {
	if e := run(); e != nil {
		log.Print(e)
		os.Exit(1)
	}
}
func run() error {
	cfg, e := config.Load()
	if e != nil {
		return e
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	m, e := room.NewManager(cfg.Room)
	if e != nil {
		return e
	}
	s, e := transport.New(m, cfg.Transport)
	if e != nil {
		shutdown, stop := context.WithTimeout(context.Background(), cfg.Transport.ShutdownTimeout)
		defer stop()
		return errors.Join(e, m.Close(shutdown))
	}
	var collector *observability.Collector
	if cfg.Admin.Enabled() {
		startup, stop := context.WithTimeout(ctx, 5*time.Second)
		collector, e = observability.New(startup, observability.Source{Rooms: m.OperationsSnapshot, Transport: s.LiveStatus}, observability.DefaultConfig())
		stop()
		if e != nil {
			shutdown, stop := context.WithTimeout(context.Background(), cfg.Transport.ShutdownTimeout)
			defer stop()
			return errors.Join(e, closeServices(shutdown, s, nil, nil))
		}
	}
	handler, e := buildHandler(cfg, s, collector)
	if e != nil {
		shutdown, stop := context.WithTimeout(context.Background(), cfg.Transport.ShutdownTimeout)
		defer stop()
		return errors.Join(e, closeServices(shutdown, s, nil, collector))
	}
	h := &http.Server{Addr: cfg.Address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	errs := make(chan error, 1)
	go func() { errs <- h.ListenAndServe() }()
	log.Printf("Room entry server listening on %s", cfg.Address)
	var listenErr error
	select {
	case <-ctx.Done():
	case listenErr = <-errs:
	}
	shutdown, stop := context.WithTimeout(context.Background(), cfg.Transport.ShutdownTimeout)
	defer stop()
	closeErr := closeServices(shutdown, s, h, collector)
	if errors.Is(listenErr, http.ErrServerClosed) {
		listenErr = nil
	}
	return errors.Join(listenErr, closeErr)
}
func buildHandler(cfg config.Config, s *transport.Server, c *observability.Collector) (http.Handler, error) {
	dashboard, e := admin.New(cfg.Admin, c)
	if e != nil {
		return nil, e
	}
	public := s.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Dispatch before ServeMux can canonicalize an admin-prefixed path.
		if r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/") {
			dashboard.ServeHTTP(w, r)
			return
		}
		public.ServeHTTP(w, r)
	}), nil
}
func closeServices(ctx context.Context, s *transport.Server, h *http.Server, c *observability.Collector) error {
	if c != nil {
		c.MarkStopping()
	}
	var errs []error
	if s != nil {
		errs = append(errs, s.Close(ctx))
	}
	if h != nil {
		errs = append(errs, h.Shutdown(ctx))
	}
	if c != nil {
		errs = append(errs, c.Close(ctx))
	}
	return errors.Join(errs...)
}
