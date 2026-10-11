package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"roomserver/internal/config"
	"roomserver/internal/room"
	"roomserver/internal/transport"
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
	m, e := room.NewManager(cfg.Room)
	if e != nil {
		return e
	}
	s, e := transport.New(m, cfg.Transport)
	if e != nil {
		m.Close(context.Background())
		return e
	}
	h := &http.Server{Addr: cfg.Address, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
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
	closeErr := s.Close(shutdown)
	httpErr := h.Shutdown(shutdown)
	if listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
		return listenErr
	}
	if closeErr != nil {
		return closeErr
	}
	return httpErr
}
