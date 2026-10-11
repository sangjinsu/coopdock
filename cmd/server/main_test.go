package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"roomserver/internal/admin"
	"roomserver/internal/config"
	"roomserver/internal/observability"
	"roomserver/internal/room"
	"roomserver/internal/transport"
	"strings"
	"syscall"
	"testing"
	"time"
)

const viewerToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func services(t *testing.T) (*room.Manager, *transport.Server) {
	t.Helper()
	m, e := room.NewManager(room.DefaultConfig())
	if e != nil {
		t.Fatal(e)
	}
	s, e := transport.New(m, transport.DefaultConfig())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if e := s.Close(ctx); e != nil {
			t.Error(e)
		}
	})
	return m, s
}
func collectServices(t *testing.T, m *room.Manager, s *transport.Server) *observability.Collector {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, e := observability.New(ctx, observability.Source{Rooms: m.OperationsSnapshot, Transport: s.LiveStatus}, observability.DefaultConfig())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if e := c.Close(ctx); e != nil {
			t.Error(e)
		}
	})
	return c
}
func request(h http.Handler, method, path string, auth bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if auth {
		r.SetBasicAuth("viewer", viewerToken)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestBuildHandlerRouting(t *testing.T) {
	m, s := services(t)
	for _, enabled := range []bool{false, true} {
		cfg := config.Config{}
		var c *observability.Collector
		if enabled {
			cfg.Admin = admin.Config{ViewerToken: viewerToken}
			c = collectServices(t, m, s)
		}
		h, e := buildHandler(cfg, s, c)
		if e != nil {
			t.Fatal(e)
		}
		for _, path := range []string{"/healthz", "/readyz", "/ws", "/administrator"} {
			w := request(h, "GET", path, false)
			want := 200
			if path == "/ws" {
				want = 426
			}
			if path == "/administrator" {
				want = 404
			}
			if w.Code != want {
				t.Errorf("public %s=%d want %d", path, w.Code, want)
			}
		}
		for _, path := range []string{"/admin", "/admin/", "/admin/api/status", "/admin//api/status", "/admin/./api/status", "/admin/../healthz", "/admin/assets/../dashboard.css"} {
			for _, auth := range []bool{false, true} {
				w := request(h, "GET", path, auth)
				want := 404
				if enabled && !auth {
					want = 401
				}
				if enabled && auth && (path == "/admin" || path == "/admin/" || path == "/admin/api/status") {
					want = 200
				}
				if w.Code != want || w.Header().Get("Location") != "" {
					t.Errorf("admin enabled=%v auth=%v %s=%d want %d", enabled, auth, path, w.Code, want)
				}
				if w.Header().Get("Cache-Control") != "no-store" {
					t.Error("admin prefix lost protection")
				}
			}
		}
		if enabled {
			w := request(h, "GET", "/admin/api/status", true)
			var v map[string]any
			if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
				t.Fatal(e)
			}
			if v["generation"] != float64(1) || v["ready"] != true || v["server_instance_id"] == "" {
				t.Error("handler available before first valid cache")
			}
		}
	}
	if _, e := buildHandler(config.Config{Admin: admin.Config{ViewerToken: viewerToken}}, s, nil); e == nil {
		t.Error("active nil cache accepted")
	}
}

func TestCloseServicesStopsAllAfterEarlierError(t *testing.T) {
	m, s := services(t)
	entered, release := make(chan struct{}), make(chan struct{})
	calls := 0
	c, e := observability.New(context.Background(), observability.Source{Rooms: func() room.OperationsSnapshot {
		calls++
		if calls == 2 {
			close(entered)
			<-release
		}
		return m.OperationsSnapshot()
	}, Transport: s.LiveStatus}, observability.Config{Interval: time.Millisecond})
	if e != nil {
		t.Fatal(e)
	}
	released := false
	defer func() {
		if !released {
			close(release)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if e := c.Close(ctx); e != nil {
			t.Error(e)
		}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("source did not block")
	}
	requestEntered, requestRelease := make(chan struct{}), make(chan struct{})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestEntered)
		<-requestRelease
		w.WriteHeader(200)
	}))
	defer httpServer.Close()
	defer close(requestRelease)
	go func() {
		response, err := httpServer.Client().Get(httpServer.URL)
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	select {
	case <-requestEntered:
	case <-time.After(time.Second):
		t.Fatal("HTTP request did not block")
	}
	called := make(chan struct{})
	httpServer.Config.RegisterOnShutdown(func() {
		if v := c.Load(); v.State != observability.Stopping || v.Ready {
			t.Error("HTTP shutdown preceded stopping overlay")
		}
		_, stopping := s.LiveStatus()
		if !stopping {
			t.Error("HTTP shutdown preceded transport close")
		}
		close(called)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e = closeServices(ctx, s, httpServer.Config, c)
	// The blocked HTTP request and source both return cancellation errors.
	if !errors.Is(e, context.Canceled) || strings.Count(e.Error(), context.Canceled.Error()) < 2 {
		t.Errorf("errors not joined: %v", e)
	}
	if v := c.Load(); v.State != observability.Stopping || v.Ready {
		t.Error("collector not marked stopping")
	}
	_, stopping := s.LiveStatus()
	if !stopping {
		t.Error("transport close skipped")
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Error("HTTP shutdown skipped after earlier error")
	}
	generation := c.Load().Generation
	close(release)
	released = true
	time.Sleep(20 * time.Millisecond)
	if c.Load().Generation != generation {
		t.Error("collector close skipped or late source republished")
	}
}
func TestCloseServicesNilCollector(t *testing.T) {
	_, s := services(t)
	called := make(chan struct{})
	h := &http.Server{}
	h.RegisterOnShutdown(func() { close(called) })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e := closeServices(ctx, s, h, nil); e != nil {
		t.Fatal(e)
	}
	_, stopping := s.LiveStatus()
	if !stopping {
		t.Error("transport not stopped")
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Error("HTTP shutdown skipped")
	}
}

func TestRunProcess(t *testing.T) {
	if os.Getenv("COOPDOCK_SERVER_FIXTURE") != "1" {
		return
	}
	if e := run(); e != nil {
		t.Fatal(e)
	}
}
func TestRunInitialCacheAndShutdown(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			listener, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			address := listener.Addr().String()
			if e := listener.Close(); e != nil {
				t.Fatal(e)
			}
			child := exec.Command(os.Args[0], "-test.run=^TestRunProcess$")
			for _, value := range os.Environ() {
				if !strings.HasPrefix(value, "HTTP_ADDR=") && !strings.HasPrefix(value, "ADMIN_VIEWER_TOKEN=") && !strings.HasPrefix(value, "COOPDOCK_SERVER_FIXTURE=") {
					child.Env = append(child.Env, value)
				}
			}
			child.Env = append(child.Env, "COOPDOCK_SERVER_FIXTURE=1", "HTTP_ADDR="+address)
			if enabled {
				child.Env = append(child.Env, "ADMIN_VIEWER_TOKEN="+viewerToken)
			} else {
				child.Env = append(child.Env, "ADMIN_VIEWER_TOKEN=")
			}
			var output bytes.Buffer
			child.Stdout = &output
			child.Stderr = &output
			if e := child.Start(); e != nil {
				t.Fatal(e)
			}
			waited := false
			defer func() {
				if !waited {
					_ = child.Process.Kill()
					_ = child.Wait()
				}
			}()
			client := &http.Client{Timeout: time.Second}
			deadline := time.Now().Add(5 * time.Second)
			var response *http.Response
			for time.Now().Before(deadline) {
				r, _ := http.NewRequest("GET", "http://"+address+"/admin/api/status", nil)
				r.SetBasicAuth("viewer", viewerToken)
				response, e = client.Do(r)
				if e == nil {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if e != nil {
				t.Fatalf("server never listened: %v", e)
			}
			body, e := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if e != nil {
				t.Fatal(e)
			}
			if enabled {
				var v map[string]any
				if e := json.Unmarshal(body, &v); e != nil {
					t.Fatal(e)
				}
				if response.StatusCode != 200 || v["generation"].(float64) < 1 || v["ready"] != true || v["updated_at"] == "" || v["heap_alloc_bytes"].(float64) <= 0 {
					t.Error("listening without initialized cache/sample")
				}
			} else {
				if response.StatusCode != 404 {
					t.Error("disabled route available")
				}
			}
			if e := child.Process.Signal(syscall.SIGTERM); e != nil {
				t.Fatal(e)
			}
			done := make(chan error, 1)
			go func() { done <- child.Wait() }()
			select {
			case e := <-done:
				waited = true
				if e != nil {
					t.Errorf("shutdown failed: %v", e)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("shutdown timeout")
			}
			if strings.Contains(output.String(), viewerToken) {
				t.Fatal("token logged")
			}
		})
	}
}
func TestRunListenFailureReturnsAndCleansUp(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	t.Setenv("HTTP_ADDR", listener.Addr().String())
	t.Setenv("ADMIN_VIEWER_TOKEN", viewerToken)
	started := time.Now()
	if e := run(); e == nil || !strings.Contains(e.Error(), "address already in use") {
		t.Fatalf("listen failure omitted: %v", e)
	}
	if time.Since(started) > time.Second {
		t.Error("listen cleanup not bounded")
	}
}
