package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sangjinsu/coopdock"
)

func exampleService(t *testing.T, enabled bool) *coopdock.Server {
	t.Helper()
	cfg := coopdock.DefaultConfig()
	if enabled {
		cfg.Admin.ViewerToken = strings.Repeat("a", 32)
	}
	service, err := coopdock.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return service
}

// A normal mux mount would redirect raw admin paths before authentication.
func TestMountPreservesAdminBoundaryAndHostRoute(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		service := exampleService(t, enabled)
		host := http.NewServeMux()
		host.HandleFunc("GET /own-health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
		handler := embeddingHandler(host, service, enabled)
		for _, tc := range []struct {
			path string
			auth bool
			want int
		}{
			{"/own-health", false, 204}, {"/coopdock/healthz", false, 200}, {"/coopdock/readyz", false, 200},
			{"/coopdock/admin/../healthz", false, 404}, {"/coopdock/admin//api/status", false, 404},
			{"/coopdock/admin/../healthz", true, 404}, {"/coopdock/admin//api/status", true, 404},
			{"/administrator", false, 404},
		} {
			want := tc.want
			if enabled && !tc.auth && strings.HasPrefix(tc.path, "/coopdock/admin/") {
				want = 401
			}
			req := httptest.NewRequest("GET", tc.path, nil)
			if tc.auth {
				req.SetBasicAuth("viewer", strings.Repeat("a", 32))
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != want || w.Header().Get("Location") != "" {
				t.Errorf("enabled=%v %s auth=%v: status=%d location=%q want=%d", enabled, tc.path, tc.auth, w.Code, w.Header().Get("Location"), want)
			}
		}
		for _, path := range []string{"/admin", "/admin/assets/dashboard.css", "/admin/assets/dashboard.js", "/admin/api/status", "/admin/api/rooms"} {
			for _, auth := range []bool{false, true} {
				req := httptest.NewRequest("GET", path, nil)
				if auth {
					req.SetBasicAuth("viewer", strings.Repeat("a", 32))
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				want := 404
				if enabled {
					want = 401
					if auth {
						want = 200
					}
				}
				if w.Code != want || w.Header().Get("Location") != "" {
					t.Errorf("root alias enabled=%v %s auth=%v: status=%d want=%d", enabled, path, auth, w.Code, want)
				}
				if enabled && auth && w.Body.Len() == 0 {
					t.Errorf("empty root asset/API %s", path)
				}
			}
		}
	}
}

// Dropping core shutdown would leave new WebSocket admissions open.
func TestCloseServicesStopsCoreAndHTTP(t *testing.T) {
	service := exampleService(t, false)
	server := httptest.NewServer(embeddingHandler(http.NotFoundHandler(), service, false))
	defer server.Close()
	if err := closeServices(context.Background(), service, server.Config); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{"/readyz": 503, "/ws": 503, "/healthz": 200} {
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != want {
			t.Errorf("after shutdown %s=%d want=%d", path, w.Code, want)
		}
	}
	if response, err := server.Client().Get(server.URL + "/coopdock/healthz"); err == nil {
		response.Body.Close()
		t.Error("host listener is still accepting requests")
	}
}
