package main

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"net/http"
	"net/http/httptest"
	"roomserver/internal/room"
	"roomserver/internal/transport"
	"strings"
	"testing"
	"time"
)

func TestGameScopeCheckerEndpoint(t *testing.T) {
	for _, tc := range []struct{ base, id, want string }{{"ws://example.com/ws", "game-a", "ws://example.com/ws?game_id=game-a"}, {"wss://example.com/ws", "0_-", "wss://example.com/ws?game_id=0_-"}} {
		got, err := checkerURL(tc.base, tc.id)
		if err != nil || got != tc.want {
			t.Errorf("endpoint=%q err=%v", got, err)
		}
	}
	for _, base := range []string{"ws://host/ws?game_id=a", "ws://host/ws?", "ws://host/ws#x", "ws://host/ws#", "http://host/ws", "ws:///ws", "ws://user:pass@host/ws"} {
		if _, err := checkerURL(base, "default"); err == nil {
			t.Errorf("accepted base %q", base)
		}
	}
	for _, id := range []string{"", "A", "a&game_id=b", "가"} {
		if _, err := checkerURL("ws://host/ws", id); err == nil {
			t.Error("invalid game accepted", id)
		}
	}
}
func TestGameScopeCheckerGuestAndSnapshotBinding(t *testing.T) {
	for _, mode := range []string{"wrong-guest", "missing-guest", "wrong-snapshot", "missing-snapshot"} {
		t.Run(mode, func(t *testing.T) {
			h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer c.CloseNow()
				ctx := r.Context()
				guest := map[string]string{"session_id": "test-session", "game_id": "game-a"}
				if mode == "wrong-guest" {
					guest["game_id"] = "game-b"
				}
				if mode == "missing-guest" {
					delete(guest, "game_id")
				}
				b, _ := json.Marshal(map[string]any{"version": 1, "op": "session.guest", "request_id": nil, "payload": guest, "error": nil})
				if c.Write(ctx, websocket.MessageText, b) != nil {
					return
				}
				_, data, err := c.Read(ctx)
				if err != nil {
					return
				}
				var req reply
				json.Unmarshal(data, &req)
				row := map[string]any{"game_id": "game-b", "room_id": "b", "room_number": "004271"}
				if mode == "missing-snapshot" {
					delete(row, "game_id")
				}
				b, _ = json.Marshal(map[string]any{"version": 1, "op": req.Op, "request_id": req.ID, "payload": map[string]any{"rooms": []any{row}}, "error": nil})
				c.Write(ctx, websocket.MessageText, b)
			}))
			defer h.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			c, err := connect(ctx, "ws"+strings.TrimPrefix(h.URL, "http")+"/ws?game_id=game-a")
			if strings.Contains(mode, "guest") {
				if err == nil {
					c.conn.CloseNow()
					t.Fatal("checker accepted false guest binding")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer c.conn.CloseNow()
			_, err = c.request(ctx, "room.list", map[string]any{})
			if err == nil {
				t.Fatal("checker accepted foreign/missing snapshot binding")
			}
		})
	}
}
func namespaceFixture(t *testing.T) string {
	t.Helper()
	cfg := room.DefaultConfig()
	cfg.GameIDs = []string{"game-a", "game-b"}
	cfg.ArgonMemory = 8
	cfg.ArgonTime = 1
	m, err := room.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := transport.New(m, transport.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	h := httptest.NewServer(s.Handler())
	t.Cleanup(func() { s.Close(context.Background()); h.Close() })
	return "ws" + strings.TrimPrefix(h.URL, "http") + "/ws"
}
func TestGameScopeCheckerActualWebSockets(t *testing.T) {
	base := namespaceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := runNamespaceChecks(ctx, base, "game-a", "game-b"); err != nil {
		t.Fatal(err)
	}
}
func TestGameScopeCheckerNegativeCrossJoin(t *testing.T) {
	upstream := namespaceFixture(t)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer client.CloseNow()
		server, _, err := websocket.Dial(r.Context(), upstream+"?"+r.URL.RawQuery, nil)
		if err != nil {
			return
		}
		defer server.CloseNow()
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer cancel()
			for {
				kind, b, err := client.Read(ctx)
				if err != nil {
					return
				}
				if server.Write(ctx, kind, b) != nil {
					return
				}
			}
		}()
		defer func() { cancel(); client.CloseNow(); server.CloseNow(); <-done }()
		for {
			kind, b, err := server.Read(ctx)
			if err != nil {
				return
			}
			var v reply
			if json.Unmarshal(b, &v) == nil && v.Op == "room.join" && v.Error != nil && v.Error.Code == "ROOM_NOT_FOUND" {
				v.Error = nil
				v.Payload = json.RawMessage(`{"snapshot":{"game_id":"game-b"}}`)
				b, _ = json.Marshal(v)
			}
			if client.Write(ctx, kind, b) != nil {
				return
			}
		}
	}))
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	err := runNamespaceChecks(ctx, "ws"+strings.TrimPrefix(h.URL, "http")+"/ws", "game-a", "game-b")
	if err == nil || !strings.Contains(err.Error(), "ROOM_NOT_FOUND") {
		t.Fatalf("checker failed to reject injected cross-join success: %v", err)
	}
}
