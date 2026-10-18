package coopdock_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/sangjinsu/coopdock"
)

const viewerToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func newServer(t *testing.T, cfg coopdock.Config) *coopdock.Server {
	t.Helper()
	s, err := coopdock.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s
}
func request(h http.Handler, path string, auth bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	if auth {
		r.SetBasicAuth("viewer", viewerToken)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestPublicNoEnvironment(t *testing.T) {
	for k, v := range map[string]string{"HTTP_ADDR": "invalid", "GAME_IDS": "bad space", "WS_ORIGINS": "*", "WS_INGRESS_QUEUE": "0", "WS_READ_TIMEOUT": "0s", "ADMIN_VIEWER_TOKEN": "short"} {
		t.Setenv(k, v)
	}
	cfg := coopdock.DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	s := newServer(t, cfg)
	if w := request(s.Handler(), "/ws", false); w.Code != 426 {
		t.Fatalf("bare websocket contract=%d", w.Code)
	}
	if w := request(s.Handler(), "/admin", false); w.Code != 404 {
		t.Fatalf("env enabled admin=%d", w.Code)
	}
}
func TestHandlerAdminRouting(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := coopdock.DefaultConfig()
		if enabled {
			cfg.Admin.ViewerToken = viewerToken
		}
		s := newServer(t, cfg)
		for _, path := range []string{"/admin", "/admin/", "/admin/api/status", "/admin//api/status", "/admin/./api/status", "/admin/../healthz", "/admin/assets/../dashboard.css"} {
			for _, auth := range []bool{false, true} {
				w := request(s.Handler(), path, auth)
				want := 404
				if enabled && !auth {
					want = 401
				}
				if enabled && auth && (path == "/admin" || path == "/admin/" || path == "/admin/api/status") {
					want = 200
				}
				if w.Code != want || w.Header().Get("Location") != "" || w.Header().Get("Cache-Control") != "no-store" {
					t.Errorf("enabled=%v auth=%v path=%s status=%d headers=%v", enabled, auth, path, w.Code, w.Header())
				}
			}
		}
		if w := request(s.Handler(), "/administrator", false); w.Code != 404 {
			t.Errorf("administrator=%d", w.Code)
		}
		if enabled {
			var v map[string]any
			w := request(s.Handler(), "/admin/api/status", true)
			if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
				t.Fatal(err)
			}
			if v["ready"] != true || v["server_instance_id"] == "" || v["generation"].(float64) < 1 {
				t.Fatalf("uninitialized cache: %v", v)
			}
		}
	}
}
func TestShutdownDiagnostics(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := coopdock.DefaultConfig()
		if enabled {
			cfg.Admin.ViewerToken = viewerToken
		}
		s := newServer(t, cfg)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := s.Shutdown(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		for path, want := range map[string]int{"/healthz": 200, "/readyz": 503, "/ws": 503, "/ws?game_id=unknown": 404, "/ws?bad=x": 400} {
			if w := request(s.Handler(), path, false); w.Code != want {
				t.Errorf("%s=%d want %d", path, w.Code, want)
			}
		}
		if err := s.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := s.Shutdown(ctx); err != nil {
			t.Errorf("completed canceled retry: %v", err)
		}
		w := request(s.Handler(), "/admin/api/status", true)
		if !enabled {
			if w.Code != 404 {
				t.Error("disabled admin available")
			}
			continue
		}
		var v map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if v["state"] != "STOPPING" || v["ready"] != false {
			t.Fatalf("shutdown diagnostics: %v", v)
		}
	}
}

type wsClient struct {
	conn      *websocket.Conn
	ctx       context.Context
	seq       int
	sessionID string
}

func dial(t *testing.T, url, origin string) *wsClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	opts := &websocket.DialOptions{}
	if origin != "" {
		opts.HTTPHeader = http.Header{"Origin": []string{origin}}
	}
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(url, "http"), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	c := &wsClient{conn: conn, ctx: ctx}
	v := c.read(t)
	if v["op"] != "session.guest" || v["version"] != float64(1) {
		t.Fatalf("guest=%v", v)
	}
	c.sessionID = v["payload"].(map[string]any)["session_id"].(string)
	return c
}
func (c *wsClient) read(t *testing.T) map[string]any {
	t.Helper()
	_, raw, err := c.conn.Read(c.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func (c *wsClient) rpc(t *testing.T, op string, payload map[string]any) map[string]any {
	t.Helper()
	c.seq++
	id := strings.Repeat("r", c.seq)
	raw, err := json.Marshal(map[string]any{"version": 1, "op": op, "request_id": id, "payload": payload, "error": nil})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.conn.Write(c.ctx, websocket.MessageText, raw); err != nil {
		t.Fatal(err)
	}
	for {
		v := c.read(t)
		if v["request_id"] == id {
			return v
		}
	}
}
func good(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	if v["error"] != nil {
		t.Fatalf("RPC error: %v", v)
	}
	p, ok := v["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload: %v", v)
	}
	return p
}

// Read authoritative state, not merely the accepted action receipt.
func (c *wsClient) moved(t *testing.T) {
	t.Helper()
	for {
		v := c.read(t)
		if v["op"] != "game.snapshot" {
			continue
		}
		snapshot := good(t, v)
		if snapshot["last_processed_input_seq"].(map[string]any)[c.sessionID] != float64(1) {
			continue
		}
		for _, raw := range snapshot["game_state"].(map[string]any)["players"].([]any) {
			p := raw.(map[string]any)
			if p["session_id"] == c.sessionID && p["position"].(map[string]any)["x"].(float64) > 0 && p["velocity"].(map[string]any)["x"].(float64) == 5 {
				return
			}
		}
	}
}
func TestConfigOwnership(t *testing.T) {
	cfg := coopdock.DefaultConfig()
	cfg.Room.GameIDs = []string{"game-a"}
	cfg.Transport.Origins = []string{"allowed.example"}
	s := newServer(t, cfg)
	cfg.Room.GameIDs[0] = "game-b"
	cfg.Transport.Origins[0] = "blocked.example"
	host := httptest.NewServer(s.Handler())
	defer host.Close()
	dial(t, host.URL+"/ws?game_id=game-a", "https://allowed.example")
	if w := request(s.Handler(), "/ws?game_id=game-b", false); w.Code != 404 {
		t.Fatalf("mutated game policy=%d", w.Code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, res, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(host.URL, "http")+"/ws?game_id=game-a", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"https://blocked.example"}}})
	if conn != nil {
		_ = conn.CloseNow()
	}
	if err == nil || res == nil || res.StatusCode != 403 {
		t.Fatalf("mutated origins accepted: %v %v", res, err)
	}
}
func TestPublicGameContract(t *testing.T) {
	cfg := coopdock.DefaultConfig()
	cfg.Room.GameIDs = []string{"game-a", "game-b"}
	s := newServer(t, cfg)
	host := httptest.NewServer(s.Handler())
	defer host.Close()
	for path, want := range map[string]int{"/ws": 400, "/ws?game_id=game-a&game_id=game-b": 400, "/ws?game_id=unknown": 404} {
		if w := request(s.Handler(), path, false); w.Code != want {
			t.Errorf("%s=%d", path, w.Code)
		}
	}
	a := dial(t, host.URL+"/ws?game_id=game-a", "")
	b := dial(t, host.URL+"/ws?game_id=game-b", "")
	joiner := dial(t, host.URL+"/ws?game_id=game-a", "")
	if v := a.rpc(t, "room.create", map[string]any{"capacity": 2, "visibility": "public", "game_id": "game-b"}); v["error"] == nil {
		t.Fatal("caller override accepted")
	}
	created := good(t, a.rpc(t, "room.create", map[string]any{"capacity": 2, "visibility": "private", "password": "good-password"}))["snapshot"].(map[string]any)
	if created["game_id"] != "game-a" {
		t.Fatal(created)
	}
	number := created["room_number"]
	if v := good(t, b.rpc(t, "room.list", map[string]any{}))["rooms"].([]any); len(v) != 0 {
		t.Fatal("cross-game list")
	}
	if v := b.rpc(t, "room.join", map[string]any{"room_number": number, "password": "good-password"}); v["error"] == nil {
		t.Fatal("cross-game join")
	}
	if v := joiner.rpc(t, "room.join", map[string]any{"room_number": number, "password": "wrong"}); v["error"] == nil {
		t.Fatal("private password ignored")
	}
	bstate := good(t, b.rpc(t, "room.create", map[string]any{"capacity": 1, "visibility": "public"}))["snapshot"].(map[string]any)
	if bstate["game_id"] != "game-b" {
		t.Fatal("B create namespace")
	}
	if listed := good(t, a.rpc(t, "room.list", map[string]any{}))["rooms"].([]any); len(listed) != 0 {
		t.Fatal("A saw B or private room")
	}
	if listed := good(t, b.rpc(t, "room.list", map[string]any{}))["rooms"].([]any); len(listed) != 1 || listed[0].(map[string]any)["game_id"] != "game-b" {
		t.Fatal("B list namespace")
	}
	good(t, joiner.rpc(t, "room.join", map[string]any{"room_number": number, "password": "good-password"}))
	state := good(t, a.rpc(t, "room.start", map[string]any{}))["snapshot"].(map[string]any)
	good(t, a.rpc(t, "game.action", map[string]any{"match_id": state["match_id"], "input_seq": 1, "action": map[string]any{"type": "move", "direction": map[string]any{"x": 1, "y": 0, "z": 0}}}))
	a.moved(t)
	good(t, joiner.rpc(t, "room.leave", map[string]any{}))
}
func TestIndependentServers(t *testing.T) {
	cfg := coopdock.DefaultConfig()
	cfg.Room.MaxRooms = 1
	cfg.Room.MaxSessions = 1
	one := newServer(t, cfg)
	cfg.Room.GameIDs = []string{"game-b"}
	two := newServer(t, cfg)
	own := http.NewServeMux()
	own.HandleFunc("GET /own-health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for prefix, h := range map[string]http.Handler{"/one": one.Handler(), "/two": two.Handler()} {
			if strings.HasPrefix(r.URL.Path, prefix+"/") {
				http.StripPrefix(prefix, h).ServeHTTP(w, r)
				return
			}
		}
		own.ServeHTTP(w, r)
	}))
	defer host.Close()
	a := dial(t, host.URL+"/one/ws", "")
	b := dial(t, host.URL+"/two/ws?game_id=game-b", "")
	first := good(t, a.rpc(t, "room.create", map[string]any{"capacity": 1, "visibility": "public"}))["snapshot"].(map[string]any)
	second := good(t, b.rpc(t, "room.create", map[string]any{"capacity": 1, "visibility": "public"}))["snapshot"].(map[string]any)
	if first["room_id"] == second["room_id"] {
		t.Fatal("shared room")
	}
	if err := one.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w := request(two.Handler(), "/ws", false); w.Code != 400 {
		t.Fatal("allowlist leaked")
	}
	rooms := good(t, b.rpc(t, "room.list", map[string]any{}))["rooms"].([]any)
	if len(rooms) != 1 {
		t.Fatal(rooms)
	}
	state := good(t, b.rpc(t, "room.start", map[string]any{}))["snapshot"].(map[string]any)
	good(t, b.rpc(t, "game.action", map[string]any{"match_id": state["match_id"], "input_seq": 1, "action": map[string]any{"type": "move", "direction": map[string]any{"x": 1, "y": 0, "z": 0}}}))
	b.moved(t)
	res, err := host.Client().Get(host.URL + "/own-health")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("host route closed")
	}
}
