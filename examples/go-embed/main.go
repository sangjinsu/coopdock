// Command go-embed hosts CoopDock alongside an application's HTTP routes.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sangjinsu/coopdock"
)

// Dispatch before the host mux can normalize raw admin paths.
func embeddingHandler(host http.Handler, service *coopdock.Server, adminEnabled bool) http.Handler {
	handler := service.Handler()
	mounted := http.StripPrefix("/coopdock", handler)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/coopdock/") {
			mounted.ServeHTTP(w, r)
			return
		}
		// The dashboard uses absolute /admin assets and API URLs.
		if adminEnabled && (r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/")) {
			handler.ServeHTTP(w, r)
			return
		}
		host.ServeHTTP(w, r)
	})
}

// HTTP shutdown alone does not close hijacked WebSocket connections.
func closeServices(ctx context.Context, service *coopdock.Server, host *http.Server) error {
	coreErr := service.Shutdown(ctx)
	httpErr := host.Shutdown(ctx)
	return errors.Join(coreErr, httpErr)
}

func run() error {
	cfg := coopdock.DefaultConfig()
	// Configure values here; the library does not read environment variables.
	// cfg.Room.GameIDs = []string{"default", "game-a", "game-b"}
	// Admin is disabled by default. Load a secret in the host before opting in.
	service, err := coopdock.New(cfg)
	if err != nil {
		return err
	}
	host := http.NewServeMux()
	host.HandleFunc("GET /own-health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	server := &http.Server{
		Addr: "127.0.0.1:8080", Handler: embeddingHandler(host, service, cfg.Admin.ViewerToken != ""),
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second,
	}
	signals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	listenErr := make(chan error, 1)
	go func() { listenErr <- server.ListenAndServe() }()
	log.Printf("Embedding host listening on %s; CoopDock WebSocket /coopdock/ws", server.Addr)
	select {
	case err = <-listenErr:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	case <-signals.Done():
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return errors.Join(err, closeServices(ctx, service, server))
}

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
