package transport

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/coder/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"github.com/sangjinsu/coopdock/internal/room"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGameScopeHandshake(t *testing.T) {
	for _, ids := range [][]string{{"default"}, {"game-a"}, {"default", "game-a"}} {
		t.Run(strings.Join(ids, ","), func(t *testing.T) {
			rc := room.DefaultConfig()
			rc.GameIDs = ids
			rc.MaxSessions = 1
			m, err := room.NewManager(rc)
			if err != nil {
				t.Fatal(err)
			}
			s, err := New(m, DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			h := httptest.NewServer(s.Handler())
			defer h.Close()
			cases := []struct {
				raw    string
				status int
				body   string
			}{{"?game_id=", 400, "Invalid game selection.\n"}, {"?game_id=GAME-A", 400, "Invalid game selection.\n"}, {"?game_id=unknown", 404, "Game was not found.\n"}, {"?game_id=default&game_id=default", 400, "Invalid game selection.\n"}, {"?game_id=default&game%5Fid=default", 400, "Invalid game selection.\n"}, {"?game_id=default&x=1", 400, "Invalid game selection.\n"}, {"?game_id=default&broken=%ZZ", 400, "Invalid game selection.\n"}, {"?game_id=default;foo=bar", 400, "Invalid game selection.\n"}, {"?game_id=" + strings.Repeat("a", 1025), 400, "Invalid game selection.\n"}}
			if len(ids) != 1 || ids[0] != "default" {
				cases = append(cases, struct {
					raw    string
					status int
					body   string
				}{"", 400, "Invalid game selection.\n"})
			}
			for _, tc := range cases {
				res, err := http.Get(h.URL + "/ws" + tc.raw)
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(res.Body)
				res.Body.Close()
				if res.StatusCode != tc.status || string(body) != tc.body {
					t.Errorf("%q status=%d body=%q", tc.raw, res.StatusCode, body)
				}
				if n, _ := s.LiveStatus(); n != 0 {
					t.Fatal("failed handshake counted connection", n)
				}
				guest, err := m.ConnectGame("probe", ids[0])
				if err != nil {
					t.Fatal("failed handshake leaked session", err)
				}
				m.Disconnect(guest)
			}
		})
	}
}

func gameServer(t *testing.T) (*Server, *room.Manager, *httptest.Server) {
	t.Helper()
	rc := room.DefaultConfig()
	rc.GameIDs = []string{"game-a", "game-b"}
	rc.ArgonMemory = 8
	rc.ArgonTime = 1
	m, err := room.NewManager(rc)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(m, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	h := httptest.NewServer(s.Handler())
	t.Cleanup(func() { s.Close(context.Background()); h.Close() })
	return s, m, h
}
func dialGame(t *testing.T, h *httptest.Server, gameID string) *client {
	t.Helper()
	c, _, err := websocket.Dial(context.Background(), h.URL+"/ws?game_id="+gameID, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	v := &client{t: t, c: c}
	f := v.read()
	var p struct {
		ID     string `json:"session_id"`
		GameID string `json:"game_id"`
	}
	if json.Unmarshal(f.Payload, &p) != nil || f.Op != "session.guest" || p.GameID != gameID || p.ID == "" {
		t.Fatal("guest binding", f)
	}
	v.id = p.ID
	return v
}
func scopedResponse(t *testing.T, c *client, gameID, op string, payload any, allowed map[string]bool) frame {
	t.Helper()
	c.send(op, payload)
	for {
		f := c.read()
		if f.Error == nil {
			var state room.Snapshot
			switch f.Op {
			case "room.state", "game.snapshot":
				json.Unmarshal(f.Payload, &state)
			case "room.create", "room.join", "room.start", "game.started", "game.ended":
				state = snapshot(t, f)
			case "game.event":
				var event room.GameEvent
				if json.Unmarshal(f.Payload, &event) != nil || !allowed[event.SessionID] {
					t.Fatal("cross-game event", string(f.Payload))
				}
			}
			if state.RoomID != "" && state.GameID != gameID {
				t.Fatalf("cross-game %s snapshot %s != %s", f.Op, state.GameID, gameID)
			}
		}
		if f.ID != nil && f.Op == op {
			return f
		}
	}
}
func TestGameScopeActualWebSocketsAndShutdown(t *testing.T) {
	s, m, h := gameServer(t)
	a, ag, b, bg := dialGame(t, h, "game-a"), dialGame(t, h, "game-a"), dialGame(t, h, "game-b"), dialGame(t, h, "game-b")
	allowedA := map[string]bool{a.id: true, ag.id: true}
	allowedB := map[string]bool{b.id: true, bg.id: true}
	av := snapshot(t, scopedResponse(t, a, "game-a", "room.create", map[string]any{}, allowedA))
	bv := snapshot(t, scopedResponse(t, b, "game-b", "room.create", map[string]any{}, allowedB))
	for _, pair := range []struct {
		c          *client
		id, number string
		allowed    map[string]bool
	}{{ag, "game-a", av.RoomNumber, allowedA}, {bg, "game-b", bv.RoomNumber, allowedB}} {
		f := scopedResponse(t, pair.c, pair.id, "room.join", map[string]any{"room_number": pair.number}, pair.allowed)
		if f.Error != nil {
			t.Fatal(f.Error)
		}
	}
	for _, pair := range []struct {
		c       *client
		id      string
		allowed map[string]bool
	}{{a, "game-a", allowedA}, {b, "game-b", allowedB}} {
		for _, op := range []string{"room.create", "room.join", "room.start", "room.list", "room.leave", "game.action"} {
			p := map[string]any{"game_id": "game-b"}
			if op == "room.join" {
				p["room_number"] = av.RoomNumber
			}
			if op == "game.action" {
				p["match_id"] = "match"
				p["input_seq"] = 1
				p["action"] = map[string]any{"type": "move", "direction": map[string]int{"x": 0, "y": 0, "z": 1}}
			}
			f := scopedResponse(t, pair.c, pair.id, op, p, pair.allowed)
			if f.Error == nil || f.Error.Code != "INVALID_MESSAGE" {
				t.Fatalf("override %s: %+v", op, f)
			}
		}
		started := snapshot(t, scopedResponse(t, pair.c, pair.id, "room.start", map[string]any{}, pair.allowed))
		if started.MatchID == nil {
			t.Fatal("start missing match")
		}
		for seq, z := range []int{1, 0} {
			f := scopedResponse(t, pair.c, pair.id, "game.action", map[string]any{"match_id": *started.MatchID, "input_seq": seq + 1, "action": map[string]any{"type": "move", "direction": map[string]int{"x": 0, "y": 0, "z": z}}}, pair.allowed)
			if f.Error != nil {
				t.Fatal(f.Error)
			}
			var ack room.ActionAck
			json.Unmarshal(f.Payload, &ack)
			if ack.InputSeq != uint64(seq+1) || ack.ServerTick == 0 {
				t.Fatal("real tick ACK missing", ack)
			}
		}
	}
	scopedResponse(t, a, "game-a", "room.leave", map[string]any{}, allowedA)
	f := scopedResponse(t, ag, "game-a", "room.list", map[string]any{}, allowedA)
	var listed struct {
		Rooms []room.Snapshot `json:"rooms"`
	}
	json.Unmarshal(f.Payload, &listed)
	if len(listed.Rooms) != 1 || listed.Rooms[0].HostID != ag.id || listed.Rooms[0].GameID != "game-a" {
		t.Fatal("host succession", listed)
	}
	f = scopedResponse(t, b, "game-b", "room.list", map[string]any{}, allowedB)
	json.Unmarshal(f.Payload, &listed)
	if len(listed.Rooms) != 1 || listed.Rooms[0].HostID != b.id || len(listed.Rooms[0].Members) != 2 || listed.Rooms[0].GameID != "game-b" {
		t.Fatal("B changed during A leave", listed)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n, stopping := s.LiveStatus(); n != 0 || !stopping {
		t.Fatal("shutdown connections", n, stopping)
	}
	if v := m.OperationsSnapshot(); v.Ready || len(v.Rooms) != 0 {
		t.Fatal("shutdown left a namespace actor", v)
	}
	for _, id := range []string{"game-a", "game-b"} {
		if _, err := m.ConnectGame("closed", id); room.CodeOf(err) != "SERVER_CLOSING" {
			t.Fatal(err)
		}
	}
	res, err := http.Get(h.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 503 {
		t.Fatal("ready after all-game shutdown", res.StatusCode)
	}
}
func TestGameScopeAcceptCloseRace(t *testing.T) {
	for _, id := range []string{"game-a", "game-b"} {
		t.Run(id, func(t *testing.T) {
			s, _, h := gameServer(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			s.mu.Lock()
			s.beforeAccept = func() { close(entered); <-release }
			s.mu.Unlock()
			dialed := make(chan struct{})
			go func() {
				defer close(dialed)
				c, _, _ := websocket.Dial(context.Background(), h.URL+"/ws?game_id="+id, nil)
				if c != nil {
					c.CloseNow()
				}
			}()
			waitDone(t, entered)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if err := s.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("close did not wait for accepting namespace", err)
			}
			once.Do(func() { close(release) })
			if err := s.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			waitDone(t, dialed)
			assertLiveStatus(t, s, 0, true)
		})
	}
}
func TestGameScopeRawQueryLimitAndFailedUpgrade(t *testing.T) {
	rc := room.DefaultConfig()
	rc.GameIDs = []string{"game-a", "game-b"}
	rc.MaxSessions = 1
	m, err := room.NewManager(rc)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(m, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	for _, tc := range []struct {
		raw  string
		want int
	}{{"game_id=game-a" + strings.Repeat("&", 1010), 426}, {"game_id=game-a" + strings.Repeat("&", 1011), 400}, {"game%5Fid=game%2Da", 426}, {"game_id=game-b", 426}, {"=a", 400}, {"game_id=%FF", 400}} {
		r := httptest.NewRequest("GET", "http://localhost/ws?"+tc.raw, nil)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("raw length %d status=%d want=%d", len(tc.raw), w.Code, tc.want)
		}
		probe, err := m.ConnectGame("probe", "game-a")
		if err != nil {
			t.Fatal("failed upgrade left a session", err)
		}
		m.Disconnect(probe)
		if n, _ := s.LiveStatus(); n != 0 {
			t.Fatal("failed upgrade count", n)
		}
	}
}
