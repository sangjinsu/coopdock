package transport

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"net/http"
	"net/http/httptest"
	"roomserver/internal/protocol"
	"roomserver/internal/room"
	"strings"
	"sync"
	"testing"
	"time"
)

type frame struct {
	Version int             `json:"version"`
	Op      string          `json:"op"`
	ID      *string         `json:"request_id"`
	Payload json.RawMessage `json:"payload"`
	Error   *struct {
		Code string `json:"code"`
	} `json:"error"`
}
type client struct {
	t  *testing.T
	c  *websocket.Conn
	id string
}

func (c *client) read() frame {
	c.t.Helper()
	ctx, x := context.WithTimeout(context.Background(), 3*time.Second)
	defer x()
	_, b, e := c.c.Read(ctx)
	if e != nil {
		c.t.Fatal(e)
	}
	var f frame
	if e = json.Unmarshal(b, &f); e != nil {
		c.t.Fatal(e)
	}
	if f.Version != 1 {
		c.t.Fatal("version")
	}
	for _, s := range []string{`"password"`, `"hash"`, `"salt"`, `secret987`} {
		if strings.Contains(string(b), s) {
			c.t.Fatalf("secret on wire: %s", s)
		}
	}
	return f
}
func (c *client) send(op string, p any) {
	c.t.Helper()
	b, e := json.Marshal(map[string]any{"version": 1, "op": op, "request_id": op, "payload": p, "error": nil})
	if e != nil {
		c.t.Fatal(e)
	}
	if e = c.c.Write(context.Background(), websocket.MessageText, b); e != nil {
		c.t.Fatal(e)
	}
}
func (c *client) response(op string) frame {
	c.t.Helper()
	for {
		f := c.read()
		if f.Op == op && f.ID != nil {
			return f
		}
	}
}
func (c *client) success(op string, p any) frame {
	c.send(op, p)
	f := c.response(op)
	if f.Error != nil {
		c.t.Fatalf("%s: %s", op, f.Error.Code)
	}
	return f
}
func snapshot(t *testing.T, f frame) room.Snapshot {
	t.Helper()
	var p struct {
		Snapshot room.Snapshot `json:"snapshot"`
	}
	if e := json.Unmarshal(f.Payload, &p); e != nil {
		t.Fatal(e)
	}
	return p.Snapshot
}
func setup(t *testing.T, cfg Config) (*Server, *room.Manager, *httptest.Server, func() *client) {
	t.Helper()
	rc := room.DefaultConfig()
	rc.ArgonMemory = 8
	rc.ArgonTime = 1
	m, e := room.NewManager(rc)
	if e != nil {
		t.Fatal(e)
	}
	s, e := New(m, cfg)
	if e != nil {
		t.Fatal(e)
	}
	h := httptest.NewServer(s.Handler())
	t.Cleanup(func() { s.Close(context.Background()); h.Close() })
	dial := func() *client {
		c, _, e := websocket.Dial(context.Background(), h.URL+"/ws", nil)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { c.CloseNow() })
		v := &client{t: t, c: c}
		f := v.read()
		if f.Op != "session.guest" || f.ID != nil || f.Error != nil {
			t.Fatalf("guest: %+v", f)
		}
		var p struct {
			ID string `json:"session_id"`
		}
		json.Unmarshal(f.Payload, &p)
		v.id = p.ID
		if v.id == "" {
			t.Fatal("empty guest")
		}
		return v
	}
	return s, m, h, dial
}
func TestSixClientsJoinAndSeventhRejected(t *testing.T) {
	for _, visibility := range []string{"public", "private"} {
		t.Run(visibility, func(t *testing.T) {
			_, _, _, dial := setup(t, DefaultConfig())
			h := dial()
			p := map[string]any{"visibility": visibility}
			if visibility == "private" {
				p["password"] = "secret987"
			}
			v := snapshot(t, h.success("room.create", p))
			if v.Capacity != 6 || len(v.RoomNumber) != 6 || v.Phase != "WAITING" || v.GameState != nil || v.MatchID != nil {
				t.Fatalf("snapshot %+v", v)
			}
			for i := 0; i < 6; i++ {
				g := dial()
				p := map[string]any{"room_number": v.RoomNumber}
				if visibility == "private" {
					p["password"] = "secret987"
				}
				g.send("room.join", p)
				f := g.read()
				if f.Op != "room.join" {
					t.Fatalf("event before response: %+v", f)
				}
				if i < 5 {
					if f.Error != nil || len(snapshot(t, f).Members) != i+2 {
						t.Fatalf("join %+v", f)
					}
				} else if f.Error == nil || f.Error.Code != "ROOM_FULL" {
					t.Fatalf("seventh %+v", f)
				}
			}
		})
	}
}
func TestPrivatePasswordRoundTrip(t *testing.T) {
	_, _, _, dial := setup(t, DefaultConfig())
	h, g := dial(), dial()
	v := snapshot(t, h.success("room.create", map[string]any{"visibility": "private", "password": "secret987"}))
	g.send("room.list", map[string]any{})
	f := g.response("room.list")
	if string(f.Payload) != `{"rooms":[]}` {
		t.Fatalf("private list %s", f.Payload)
	}
	for _, pair := range []struct {
		p    any
		code string
	}{{map[string]any{"room_number": v.RoomNumber}, "PASSWORD_REQUIRED"}, {map[string]any{"room_number": v.RoomNumber, "password": "wrong123"}, "PASSWORD_INVALID"}} {
		g.send("room.join", pair.p)
		f := g.response("room.join")
		if f.Error == nil || f.Error.Code != pair.code {
			t.Fatalf("expected %s: %+v", pair.code, f)
		}
	}
	g.success("room.join", map[string]any{"room_number": v.RoomNumber, "password": "secret987"})
}
func TestLastSeatWSRace(t *testing.T) {
	_, _, _, dial := setup(t, DefaultConfig())
	h := dial()
	v := snapshot(t, h.success("room.create", map[string]any{"capacity": 2}))
	a, b := dial(), dial()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, c := range []*client{a, b} {
		wg.Add(1)
		go func(c *client) {
			defer wg.Done()
			<-start
			c.send("room.join", map[string]any{"room_number": v.RoomNumber})
		}(c)
	}
	close(start)
	wg.Wait()
	success, full := 0, 0
	for _, c := range []*client{a, b} {
		f := c.read()
		if f.Op != "room.join" {
			t.Fatal("event before response")
		}
		if f.Error == nil {
			success++
		} else if f.Error.Code == "ROOM_FULL" {
			full++
		}
	}
	if success != 1 || full != 1 {
		t.Fatalf("success %d full %d", success, full)
	}
}
func TestInitialResponseBeforeRoomState(t *testing.T) {
	_, _, _, dial := setup(t, DefaultConfig())
	h, g := dial(), dial()
	v := snapshot(t, h.success("room.create", map[string]any{}))
	for i := 0; i < 20; i++ {
		g.send("room.join", map[string]any{"room_number": v.RoomNumber})
		f := g.read()
		if f.Op != "room.join" || f.Error != nil {
			t.Fatalf("first frame %+v", f)
		}
		rv := snapshot(t, f)
		f = g.read()
		if f.Op != "room.state" || f.ID != nil {
			t.Fatalf("initial event %+v", f)
		}
		var state room.Snapshot
		json.Unmarshal(f.Payload, &state)
		if state.Revision != rv.Revision {
			t.Fatal("initial revision")
		}
		g.success("room.leave", map[string]any{})
	}
}
func TestDisconnectHostAndEmptyRoom(t *testing.T) {
	_, m, _, dial := setup(t, DefaultConfig())
	h, g := dial(), dial()
	v := snapshot(t, h.success("room.create", map[string]any{}))
	g.success("room.join", map[string]any{"room_number": v.RoomNumber})
	g.read()
	h.c.CloseNow()
	for {
		f := g.read()
		var state room.Snapshot
		json.Unmarshal(f.Payload, &state)
		if f.Op == "room.state" && state.HostID == g.id && len(state.Members) == 1 {
			break
		}
	}
	g.c.CloseNow()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ls, e := m.ListPublic(context.Background())
		if e == nil && len(ls) == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("empty room retained")
}
func TestHTTPOriginStrictFramesAndShutdown(t *testing.T) {
	s, _, h, dial := setup(t, DefaultConfig())
	for _, path := range []string{"/healthz", "/readyz"} {
		r, e := http.Get(h.URL + path)
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			t.Fatal(r.Status)
		}
	}
	r, e := http.Get(h.URL + "/admin")
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != 404 {
		t.Fatal("admin exposed")
	}
	header := http.Header{"Origin": []string{"https://evil.example"}}
	c, resp, e := websocket.Dial(context.Background(), h.URL+"/ws", &websocket.DialOptions{HTTPHeader: header})
	if c != nil {
		c.CloseNow()
	}
	if e == nil || resp.StatusCode != 403 {
		t.Fatal("foreign origin accepted")
	}
	g := dial()
	for _, b := range []string{`{`, `{"version":1,"op":"room.create","request_id":"a","payload":{"capacity":0},"error":null}`, `{"version":1,"op":"room.start","request_id":"a","payload":{},"error":null}`} {
		if e = g.c.Write(context.Background(), websocket.MessageText, []byte(b)); e != nil {
			t.Fatal(e)
		}
		f := g.read()
		if f.Error == nil || f.Error.Code != "INVALID_MESSAGE" {
			t.Fatalf("invalid frame %+v", f)
		}
	}
	if e = s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	r, e = http.Get(h.URL + "/readyz")
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != 503 {
		t.Fatal("ready after close")
	}
	ctx, x := context.WithTimeout(context.Background(), time.Second)
	defer x()
	if _, _, e = g.c.Read(ctx); e == nil {
		t.Fatal("socket survived shutdown")
	}
}

func TestReadLimitAndBinaryRejected(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MessageLimit = 256
	_, _, _, dial := setup(t, cfg)
	g := dial()
	if e := g.c.Write(context.Background(), websocket.MessageBinary, []byte(`{"version":1,"op":"room.list","request_id":"a","payload":{},"error":null}`)); e != nil {
		t.Fatal(e)
	}
	if f := g.read(); f.Error == nil || f.Error.Code != "INVALID_MESSAGE" {
		t.Fatal("binary accepted")
	}
	if e := g.c.Write(context.Background(), websocket.MessageText, []byte(strings.Repeat("x", 257))); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, e := g.c.Read(ctx); e == nil {
		t.Fatal("oversized frame accepted")
	}
}
func TestExplicitOriginAllowed(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Origins = []string{"allowed.example"}
	_, _, h, _ := setup(t, cfg)
	c, _, e := websocket.Dial(context.Background(), h.URL+"/ws", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"https://allowed.example"}}})
	if e != nil {
		t.Fatal(e)
	}
	defer c.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, e := c.Read(ctx); e != nil {
		t.Fatal(e)
	}
}
func TestConfigRejectsWildcardOrigin(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Origins = []string{"*"}
	if e := cfg.Validate(); e == nil {
		t.Fatal("unsafe wildcard accepted")
	}
}
func waitDone(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("cleanup did not complete")
	}
}
func TestIngressOverflowDuringBlockedPrivateRequest(t *testing.T) {
	cfg := DefaultConfig()
	cfg.IngressQueue = 2
	s, m, _, dial := setup(t, cfg)
	h := dial()
	v := snapshot(t, h.success("room.create", map[string]any{"visibility": "private", "password": "secret987"}))
	started := make(chan struct{})
	s.mu.Lock()
	s.beforeProcess = func(ctx context.Context, r protocol.Request) {
		if r.Op == "room.join" {
			close(started)
			<-ctx.Done()
		}
	}
	s.mu.Unlock()
	registered := make(chan *peer, 1)
	s.mu.Lock()
	s.afterRegister = func(p *peer) { registered <- p }
	s.mu.Unlock()
	g := dial()
	p := <-registered
	g.send("room.join", map[string]any{"room_number": v.RoomNumber, "password": "secret987"})
	waitDone(t, started)
	for i := 0; i < 4; i++ {
		b := []byte(`{"version":1,"op":"room.list","request_id":"a","payload":{},"error":null}`)
		g.c.Write(context.Background(), websocket.MessageText, b)
	}
	waitDone(t, p.session.Done())
	waitDone(t, p.ctx.Done())
	ls, e := m.ListPublic(context.Background())
	if e != nil || len(ls) != 0 {
		t.Fatalf("private exposed %v %v", ls, e)
	}
	g.c.CloseNow()
	h.success("room.list", map[string]any{})
}
func TestSlowWriterOverflowCleansMembership(t *testing.T) {
	cfg := DefaultConfig()
	cfg.OutboundQueue = 2
	s, m, _, dial := setup(t, cfg)
	h := dial()
	v := snapshot(t, h.success("room.create", map[string]any{}))
	registered := make(chan *peer, 1)
	s.mu.Lock()
	s.afterRegister = func(p *peer) { registered <- p }
	s.mu.Unlock()
	g := dial()
	p := <-registered
	g.success("room.join", map[string]any{"room_number": v.RoomNumber})
	g.read()
	started := make(chan struct{})
	s.mu.Lock()
	s.beforeWrite = func(ctx context.Context, v protocol.Envelope) {
		if v.RequestID != nil && *v.RequestID == "slow" {
			select {
			case <-started:
			default:
				close(started)
			}
			<-ctx.Done()
		}
	}
	s.mu.Unlock()
	b := []byte(`{"version":1,"op":"room.list","request_id":"slow","payload":{},"error":null}`)
	if e := g.c.Write(context.Background(), websocket.MessageText, b); e != nil {
		t.Fatal(e)
	}
	waitDone(t, started)
	for i := 0; i < 5; i++ {
		g.c.Write(context.Background(), websocket.MessageText, b)
	}
	waitDone(t, p.session.Done())
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ls, e := m.ListPublic(context.Background())
		if e == nil && len(ls) == 1 && len(ls[0].Members) == 1 {
			h.success("room.list", map[string]any{})
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("slow membership retained")
}
func TestReaderDetectsDisconnectDuringBlockedPrivateRequest(t *testing.T) {
	s, _, _, dial := setup(t, DefaultConfig())
	h := dial()
	v := snapshot(t, h.success("room.create", map[string]any{"visibility": "private", "password": "secret987"}))
	started := make(chan struct{})
	s.mu.Lock()
	s.beforeProcess = func(ctx context.Context, r protocol.Request) {
		if r.Op == "room.join" {
			close(started)
			<-ctx.Done()
		}
	}
	s.mu.Unlock()
	registered := make(chan *peer, 1)
	s.mu.Lock()
	s.afterRegister = func(p *peer) { registered <- p }
	s.mu.Unlock()
	g := dial()
	p := <-registered
	g.send("room.join", map[string]any{"room_number": v.RoomNumber, "password": "secret987"})
	waitDone(t, started)
	g.c.CloseNow()
	waitDone(t, p.session.Done())
	waitDone(t, p.ctx.Done())
	h.success("room.list", map[string]any{})
}
func TestPrivateRequestTimeout(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RequestTimeout = time.Nanosecond
	_, _, _, dial := setup(t, cfg)
	g := dial()
	g.send("room.create", map[string]any{"visibility": "private", "password": "secret987"})
	f := g.response("room.create")
	if f.Error == nil || f.Error.Code != "REQUEST_TIMEOUT" {
		t.Fatalf("timeout %+v", f)
	}
	g.c.CloseNow()
}
func TestShutdownDeadlineContinuesCleanup(t *testing.T) {
	s, m, _, dial := setup(t, DefaultConfig())
	started, release := make(chan struct{}), make(chan struct{})
	s.mu.Lock()
	s.beforeProcess = func(ctx context.Context, r protocol.Request) { close(started); <-release }
	s.mu.Unlock()
	g := dial()
	g.send("room.create", map[string]any{})
	waitDone(t, started)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := s.Close(ctx); e == nil {
		t.Fatal("expired caller accepted")
	}
	if m.Ready() {
		deadline := time.Now().Add(time.Second)
		for m.Ready() && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if m.Ready() {
			t.Fatal("admission remained open")
		}
	}
	close(release)
	if e := s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Connect("127.0.0.1"); room.CodeOf(e) != "SERVER_CLOSING" {
		t.Fatal("new admission after shutdown")
	}
}
func TestCloseWaitsForAcceptRace(t *testing.T) {
	s, _, h, _ := setup(t, DefaultConfig())
	started, release := make(chan struct{}), make(chan struct{})
	s.mu.Lock()
	s.beforeAccept = func() { close(started); <-release }
	s.mu.Unlock()
	dialDone := make(chan struct{})
	go func() {
		defer close(dialDone)
		c, _, _ := websocket.Dial(context.Background(), h.URL+"/ws", nil)
		if c != nil {
			c.CloseNow()
		}
	}()
	waitDone(t, started)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if e := s.Close(ctx); e == nil {
		t.Fatal("close returned while acceptance active")
	}
	close(release)
	if e := s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	waitDone(t, dialDone)
}

func TestPipelinedAdmissionLeaveRejoinInitialEvent(t *testing.T) {
	for _, firstOp := range []string{"room.create", "room.join"} {
		t.Run(firstOp, func(t *testing.T) {
			s, _, _, dial := setup(t, DefaultConfig())
			h := dial()
			hostRoom := snapshot(t, h.success("room.create", map[string]any{}))
			h.read()
			registered := make(chan *peer, 1)
			before, releaseBefore := make(chan struct{}), make(chan struct{})
			after, releaseAfter := make(chan struct{}), make(chan struct{})
			last, releaseLast := make(chan struct{}), make(chan struct{})
			s.mu.Lock()
			s.afterRegister = func(p *peer) { registered <- p }
			s.beforeProcess = func(ctx context.Context, r protocol.Request) {
				if r.ID != nil && *r.ID == "pipe-first" {
					close(before)
					select {
					case <-releaseBefore:
					case <-ctx.Done():
					}
				}
			}
			s.afterRequest = func(ctx context.Context, r protocol.Request) {
				if r.ID == nil {
					return
				}
				switch *r.ID {
				case "pipe-first":
					close(after)
					select {
					case <-releaseAfter:
					case <-ctx.Done():
					}
				case "pipe-last":
					close(last)
					select {
					case <-releaseLast:
					case <-ctx.Done():
					}
				}
			}
			s.mu.Unlock()
			g := dial()
			p := <-registered
			send := func(op, id string, payload any) {
				t.Helper()
				b, e := json.Marshal(protocol.Envelope{Version: 1, Op: op, RequestID: &id, Payload: payload})
				if e != nil {
					t.Fatal(e)
				}
				if e = g.c.Write(context.Background(), websocket.MessageText, b); e != nil {
					t.Fatal(e)
				}
			}
			firstPayload := map[string]any{}
			if firstOp == "room.join" {
				firstPayload["room_number"] = hostRoom.RoomNumber
			}
			send(firstOp, "pipe-first", firstPayload)
			waitDone(t, before)
			send("room.leave", "pipe-leave", map[string]any{})
			send("room.join", "pipe-last", map[string]any{"room_number": hostRoom.RoomNumber})
			// Observe actual reader queue publication; restore the FIFO before dispatch.
			queued := make([]incoming, 0, 2)
			for _, wantID := range []string{"pipe-leave", "pipe-last"} {
				var in incoming
				select {
				case in = <-p.ingress:
				case <-time.After(3 * time.Second):
					t.Fatal("pipelined frame not queued")
				}
				r, e := protocol.Decode(in.data)
				if e != nil || r.ID == nil || *r.ID != wantID {
					t.Fatal("reader FIFO changed")
				}
				queued = append(queued, in)
			}
			for _, in := range queued {
				p.ingress <- in
			}
			close(releaseBefore)
			waitDone(t, after)
			// Hold the real core initial event until after leave/rejoin so the contract
			// cannot accidentally pass because select consumed the core FIFO first.
			var firstState room.Snapshot
			select {
			case firstEvent := <-p.session.Events():
				firstState = firstEvent.Snapshot
			case <-time.After(3 * time.Second):
				t.Fatal("initial core event missing")
			}
			close(releaseAfter)
			firstResponse := g.read()
			if firstResponse.Op != firstOp || firstResponse.ID == nil || *firstResponse.ID != "pipe-first" || firstResponse.Error != nil {
				t.Fatal("first admission response")
			}
			firstSnapshot := snapshot(t, firstResponse)
			assertInitial := func(f frame, want room.Snapshot) {
				t.Helper()
				var got room.Snapshot
				if f.Op != "room.state" || f.ID != nil || f.Error != nil || json.Unmarshal(f.Payload, &got) != nil || got.RoomID != want.RoomID || got.Revision != want.Revision {
					t.Fatalf("initial state must immediately follow admission response: op=%s", f.Op)
				}
			}
			assertInitial(g.read(), firstSnapshot)
			leave := g.read()
			if leave.Op != "room.leave" || leave.ID == nil || *leave.ID != "pipe-leave" || leave.Error != nil {
				t.Fatal("leave response must follow initial event")
			}
			waitDone(t, last)

			// Leave the real rejoin core event queued to verify duplicate suppression.
			s.mu.Lock()
			s.afterRequest = nil
			s.mu.Unlock()
			close(releaseLast)
			rejoined := g.read()
			if rejoined.Op != "room.join" || rejoined.ID == nil || *rejoined.ID != "pipe-last" || rejoined.Error != nil {
				t.Fatal("rejoin response")
			}
			lastSnapshot := snapshot(t, rejoined)
			assertInitial(g.read(), lastSnapshot)
			h.c.CloseNow()
			next := g.read()
			var changed room.Snapshot
			if next.Op != "room.state" || json.Unmarshal(next.Payload, &changed) != nil || changed.RoomID != lastSnapshot.RoomID || changed.Revision <= lastSnapshot.Revision || changed.HostID != g.id || len(changed.Members) != 1 {
				t.Fatal("stale/duplicate initial event or lost subsequent host event")
			}
			if firstState.RoomID != firstSnapshot.RoomID || firstState.Revision != firstSnapshot.Revision {
				t.Fatal("core and response snapshot differ")
			}
		})
	}
}
