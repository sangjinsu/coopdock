package main

import (
	"context"
	"errors"
	"github.com/sangjinsu/coopdock"
	"github.com/sangjinsu/coopdock/internal/config"
	"log"
	"net/http"
	"os"
	"os/signal"
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
	s, e := coopdock.New(cfg.Config)
	if e != nil {
		return e
	}
	h := &http.Server{Addr: cfg.Address, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
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
	closeErr := closeServices(shutdown, s, h)
	if errors.Is(listenErr, http.ErrServerClosed) {
		listenErr = nil
	}
	return errors.Join(listenErr, closeErr)
}

type service interface{ Shutdown(context.Context) error }

func closeServices(ctx context.Context, s service, h *http.Server) error {
	var errs []error
	if s != nil {
		errs = append(errs, s.Shutdown(ctx))
	}
	if h != nil {
		errs = append(errs, h.Shutdown(ctx))
	}
	return errors.Join(errs...)
}
