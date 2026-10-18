package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sangjinsu/coopdock"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

const viewerToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type failedShutdown struct{ called bool }

func (s *failedShutdown) Shutdown(ctx context.Context) error {
	s.called = true
	return errors.New("core shutdown failed")
}
func TestCloseServicesStopsHTTPAfterCoreError(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release }))
	defer host.Close()
	defer close(release)
	go func() {
		response, err := host.Client().Get(host.URL)
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request did not enter")
	}
	called := make(chan struct{})
	host.Config.RegisterOnShutdown(func() { close(called) })
	core := &failedShutdown{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := closeServices(ctx, core, host.Config)
	if !core.called || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "core shutdown failed") {
		t.Fatalf("shutdown results not joined: %v", err)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("HTTP shutdown skipped")
	}
}
func TestCloseServicesPublicCore(t *testing.T) {
	core, err := coopdock.New(coopdock.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer core.Shutdown(context.Background())
	h := &http.Server{}
	called := make(chan struct{})
	h.RegisterOnShutdown(func() { close(called) })
	if err := closeServices(context.Background(), core, h); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	core.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/readyz", nil))
	if w.Code != 503 {
		t.Fatal("core not stopped")
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("HTTP shutdown skipped")
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
