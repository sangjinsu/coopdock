package transport

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"net/http/httptest"
	"reflect"
	"roomserver/internal/game"
	"roomserver/internal/protocol"
	"roomserver/internal/room"
	"sync"
	"testing"
	"time"
)

func sendID(t *testing.T, c *client, op, id string, payload any) {
	t.Helper()
	b, e := json.Marshal(protocol.Envelope{Version: 1, Op: op, RequestID: &id, Payload: payload})
	if e != nil {
		t.Fatal(e)
	}
	if e = c.c.Write(context.Background(), websocket.MessageText, b); e != nil {
		t.Fatal(e)
	}
}
func actionPayload(match string, seq uint64, x, z float64) any {
	return map[string]any{"match_id": match, "input_seq": seq, "action": map[string]any{"type": "move", "direction": map[string]float64{"x": x, "y": 0, "z": z}}}
}

func startedSnapshot(t *testing.T, payload json.RawMessage) room.Snapshot {
	t.Helper()
	var fields map[string]json.RawMessage
	var state room.Snapshot
	if json.Unmarshal(payload, &fields) != nil || len(fields) != 1 || json.Unmarshal(fields["snapshot"], &state) != nil || state.MatchID == nil {
		t.Fatal("game.started must contain a full payload.snapshot")
	}
	return state
}

// Both self-start and another member's event use the source-spec wrapper.
func TestGameStartedPayloadSnapshot(t *testing.T) {
	_, _, _, dial := setup(t, DefaultConfig())
	h, c := dial(), dial()
	created := snapshot(t, h.success("room.create", map[string]any{}))
	h.read()
	c.success("room.join", map[string]any{"room_number": created.RoomNumber})
	c.read()
	h.read()
	h.send("room.start", map[string]any{})
	response := h.read()
	if response.Op != "room.start" || response.Error != nil {
		t.Fatalf("start response: %+v", response)
	}
	want := snapshot(t, response)
	for _, client := range []*client{h, c} {
		event := client.read()
		if event.Op != "game.started" || event.ID != nil {
			t.Fatalf("start event: %+v", event)
		}
		if got := startedSnapshot(t, event.Payload); !reflect.DeepEqual(got, want) {
			t.Fatal("started capture differs from response")
		}
		paired := client.read()
		var got room.Snapshot
		if paired.Op != "room.state" || json.Unmarshal(paired.Payload, &got) != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("paired room.state capture differs")
		}
	}
}
func assertStart(t *testing.T, c *client) room.Snapshot {
	t.Helper()
	c.send("room.start", map[string]any{})
	response := c.read()
	if response.Op != "room.start" || response.Error != nil {
		t.Fatalf("start response: %+v", response)
	}
	v := snapshot(t, response)
	if v.MatchID == nil || v.ServerTick != 0 || v.Phase != "PLAYING" {
		t.Fatal("invalid start state")
	}
	for _, op := range []string{"game.started", "room.state"} {
		f := c.read()
		var state room.Snapshot
		if op == "game.started" {
			state = startedSnapshot(t, f.Payload)
		} else if json.Unmarshal(f.Payload, &state) != nil {
			t.Fatal("room.state decode")
		}
		if f.Op != op || f.ID != nil || state.SnapshotSeq != v.SnapshotSeq {
			t.Fatalf("start batch: %+v", f)
		}
	}
	return v
}

// An acceptance-only success must not replace the post-tick authoritative ACK.
func TestWebSocketMovement(t *testing.T) {
	_, _, _, dial := setup(t, DefaultConfig())
	c := dial()
	c.success("room.create", map[string]any{})
	c.read()
	started := assertStart(t, c)
	c.send("game.action", actionPayload(*started.MatchID, 1, 0, 1))
	ack := c.response("game.action")
	var a room.ActionAck
	if ack.Error != nil || json.Unmarshal(ack.Payload, &a) != nil || a.ServerTick == 0 || a.InputSeq != 1 || a.MatchID != *started.MatchID {
		t.Fatalf("invalid ACK: %+v", ack)
	}
	for {
		f := c.read()
		if f.Op != "game.snapshot" {
			continue
		}
		var state room.Snapshot
		if err := json.Unmarshal(f.Payload, &state); err != nil {
			t.Fatal(err)
		}
		if state.LastProcessedInputSeq[c.id] < 1 {
			continue
		}
		if state.ServerTick < a.ServerTick || state.GameState == nil || state.GameState.Players[0].Position.Z <= 0 {
			t.Fatal("input not reflected")
		}
		break
	}
	c.send("game.action", actionPayload(*started.MatchID, 2, 0, 0))
	stop := c.response("game.action")
	if stop.Error != nil {
		t.Fatal("stop rejected")
	}
	for {
		f := c.read()
		if f.Op != "game.snapshot" {
			continue
		}
		var state room.Snapshot
		json.Unmarshal(f.Payload, &state)
		if state.LastProcessedInputSeq[c.id] < 2 {
			continue
		}
		if state.GameState.Players[0].Velocity != (game.Vec3{}) {
			t.Fatal("stop did not clear velocity")
		}
		break
	}
}

type gatedHandler struct {
	game.GameHandler
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	end     bool
}

func (g *gatedHandler) Tick(dt float64) (game.Outcome, error) {
	g.once.Do(func() { close(g.entered); <-g.release })
	out, err := g.GameHandler.Tick(dt)
	if g.end {
		out.EndReason = "finished"
	}
	return out, err
}
func setupMovement(t *testing.T, handler game.GameHandler) (*Server, *room.Manager, func() *client) {
	t.Helper()
	rc := room.DefaultConfig()
	rc.ArgonMemory = 8
	rc.ArgonTime = 1
	rc.HandlerFactory = func() game.GameHandler { return handler }
	m, e := room.NewManager(rc)
	if e != nil {
		t.Fatal(e)
	}
	s, e := New(m, DefaultConfig())
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
		var p struct {
			ID string `json:"session_id"`
		}
		json.Unmarshal(f.Payload, &p)
		v.id = p.ID
		return v
	}
	return s, m, dial
}

// A real writer marker proves neither input was acknowledged before the blocked tick.
func TestPipelinedActionsDoNotWaitForAck(t *testing.T) {
	g := &gatedHandler{GameHandler: game.NewMovement(), entered: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	s, _, dial := setupMovement(t, g)
	t.Cleanup(func() { release.Do(func() { close(g.release) }) })
	c := dial()
	c.success("room.create", map[string]any{})
	c.read()
	v := assertStart(t, c)
	waitDone(t, g.entered)
	dispatched := make(chan string, 2)
	s.mu.Lock()
	s.afterRequest = func(_ context.Context, r protocol.Request) {
		if r.Op == "game.action" {
			dispatched <- *r.ID
		}
	}
	s.mu.Unlock()
	sendID(t, c, "game.action", "one", actionPayload(*v.MatchID, 1, 1, 0))
	sendID(t, c, "game.action", "two", actionPayload(*v.MatchID, 2, 0, 1))
	for _, want := range []string{"one", "two"} {
		select {
		case got := <-dispatched:
			if got != want {
				t.Fatal("dispatch FIFO")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("processor waited for ACK")
		}
	}
	sendID(t, c, "room.unknown", "marker", map[string]any{})
	for {
		f := c.read()
		if f.Op == "game.action" {
			t.Fatal("input ACK preceded actual tick")
		}
		if f.ID != nil && *f.ID == "marker" {
			break
		}
	}
	release.Do(func() { close(g.release) })
	for i, want := range []string{"one", "two"} {
		f := c.response("game.action")
		var ack room.ActionAck
		if f.ID == nil || *f.ID != want || f.Error != nil || json.Unmarshal(f.Payload, &ack) != nil || ack.InputSeq != uint64(i+1) || ack.ServerTick == 0 {
			t.Fatalf("completion correlation: %+v", f)
		}
	}
}

type endingHandler struct {
	game.GameHandler
	end <-chan struct{}
}

func (g *endingHandler) Tick(dt float64) (game.Outcome, error) {
	out, e := g.GameHandler.Tick(dt)
	select {
	case <-g.end:
		out.EndReason = "finished"
		out.Events = []game.Event{{Type: "finished"}}
	default:
	}
	return out, e
}

// Wait on actual queue publication rather than assuming a processing delay.
func waitReliableOp(t *testing.T, p *peer, op string) {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		p.outbound.mu.Lock()
		found := false
		for _, v := range p.outbound.reliableQueue {
			if v.Op == op {
				found = true
			}
		}
		p.outbound.mu.Unlock()
		if found {
			return
		}
		select {
		case <-p.outbound.wake:
		case <-timeout:
			t.Fatalf("%s batch was not published", op)
		}
	}
}
func registeredPeer(t *testing.T, s *Server, dial func() *client) (*client, *peer) {
	t.Helper()
	registered := make(chan *peer, 1)
	s.mu.Lock()
	s.afterRegister = func(p *peer) { registered <- p }
	s.mu.Unlock()
	c := dial()
	p := <-registered
	s.mu.Lock()
	s.afterRegister = nil
	s.mu.Unlock()
	return c, p
}

func TestInitialAndLifecycleFences(t *testing.T) {
	t.Run("end invalidates selected unsent snapshot", func(t *testing.T) {
		end := make(chan struct{})
		s, _, dial := setupMovement(t, &endingHandler{GameHandler: game.NewMovement(), end: end})
		c, p := registeredPeer(t, s, dial)
		c.success("room.create", map[string]any{})
		c.read()
		v := assertStart(t, c)
		selected, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		var free sync.Once
		t.Cleanup(func() { free.Do(func() { close(release) }) })
		s.mu.Lock()
		s.beforeWrite = func(ctx context.Context, v protocol.Envelope) {
			if v.Op == "game.snapshot" {
				once.Do(func() {
					close(selected)
					select {
					case <-release:
					case <-ctx.Done():
					}
				})
			}
		}
		s.mu.Unlock()
		waitDone(t, selected)
		close(end)
		waitReliableOp(t, p, "game.ended")
		// The terminal event and its state occupy adjacent reliable slots atomically.
		p.outbound.mu.Lock()
		queue := append([]protocol.Envelope(nil), p.outbound.reliableQueue...)
		p.outbound.mu.Unlock()
		found := false
		for i, f := range queue {
			if f.Op == "game.ended" {
				found = i+1 < len(queue) && queue[i+1].Op == "room.state"
			}
		}
		if !found {
			t.Fatal("terminal state split from lifecycle event")
		}
		free.Do(func() { close(release) })
		if f := c.read(); f.Op != "game.event" {
			t.Fatalf("old snapshot crossed fence: %s", f.Op)
		}
		f := c.read()
		var ended struct {
			MatchID    string        `json:"match_id"`
			Reason     string        `json:"reason"`
			ServerTick uint64        `json:"server_tick"`
			Snapshot   room.Snapshot `json:"snapshot"`
		}
		if f.Op != "game.ended" || json.Unmarshal(f.Payload, &ended) != nil || ended.MatchID != *v.MatchID || ended.Reason != "finished" || ended.ServerTick == 0 || ended.Snapshot.MatchID != nil || ended.Snapshot.Phase != "WAITING" {
			t.Fatalf("ended wire: %+v", f)
		}
		f = c.read()
		var state room.Snapshot
		if f.Op != "room.state" || json.Unmarshal(f.Payload, &state) != nil || state.SnapshotSeq != ended.Snapshot.SnapshotSeq {
			t.Fatal("terminal paired state")
		}
		next := assertStart(t, c)
		if *next.MatchID == *v.MatchID || next.SnapshotSeq <= ended.Snapshot.SnapshotSeq {
			t.Fatal("restart identity")
		}
		sendID(t, c, "room.unknown", "marker", map[string]any{})
		for {
			f = c.read()
			if f.Op == "game.started" {
				t.Fatal("self-start core event duplicated")
			}
			if f.ID != nil && *f.ID == "marker" {
				break
			}
		}
	})
	t.Run("initial join precedes queued start and snapshots", func(t *testing.T) {
		s, m, _, dial := setup(t, DefaultConfig())
		h, hp := registeredPeer(t, s, dial)
		v := snapshot(t, h.success("room.create", map[string]any{}))
		h.read()
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		t.Cleanup(func() { once.Do(func() { close(release) }) })
		s.mu.Lock()
		s.afterRequest = func(ctx context.Context, r protocol.Request) {
			if r.Op == "room.join" {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
		}
		s.mu.Unlock()
		c := dial()
		c.send("room.join", map[string]any{"room_number": v.RoomNumber})
		waitDone(t, entered)
		started, e := m.Start(context.Background(), hp.session)
		if e != nil {
			t.Fatal(e)
		}
		once.Do(func() { close(release) })
		f := c.read()
		if f.Op != "room.join" || f.Error != nil {
			t.Fatal("snapshot preceded initial response")
		}
		joined := snapshot(t, f)
		f = c.read()
		var state room.Snapshot
		if f.Op != "room.state" || json.Unmarshal(f.Payload, &state) != nil || state.SnapshotSeq != joined.SnapshotSeq {
			t.Fatal("initial state overtaken")
		}
		for _, op := range []string{"game.started", "room.state"} {
			f = c.read()
			if op == "game.started" {
				state = startedSnapshot(t, f.Payload)
			} else if json.Unmarshal(f.Payload, &state) != nil {
				t.Fatal("room.state decode")
			}
			if f.Op != op || state.MatchID == nil || *state.MatchID != *started.MatchID {
				t.Fatal("queued start lifecycle")
			}
		}
	})
}

func TestPlayingAdmissionAndLeave(t *testing.T) {
	_, _, _, dial := setup(t, DefaultConfig())
	h, c, g := dial(), dial(), dial()
	v := snapshot(t, h.success("room.create", map[string]any{"visibility": "private", "password": "secret987"}))
	h.read()
	c.success("room.join", map[string]any{"room_number": v.RoomNumber, "password": "secret987"})
	c.read()
	// Drain the host's join metadata before validating its start batch.
	h.read()
	started := assertStart(t, h)
	for _, op := range []string{"game.started", "room.state"} {
		if f := c.read(); f.Op != op {
			t.Fatal("member start order")
		}
	}
	g.send("room.join", map[string]any{"room_number": v.RoomNumber, "password": "secret987"})
	f := g.response("room.join")
	if f.Error == nil || f.Error.Code != "ROOM_IN_PROGRESS" {
		t.Fatalf("playing admission %+v", f)
	}
	h.c.CloseNow()
	for {
		f = c.read()
		var state room.Snapshot
		if f.Op != "room.state" {
			continue
		}
		json.Unmarshal(f.Payload, &state)
		if state.HostID != c.id {
			continue
		}
		if state.MatchID == nil || *state.MatchID != *started.MatchID || state.GameState == nil || len(state.GameState.Players) != 1 || state.GameState.Players[0].SessionID != c.id {
			t.Fatal("host leave reset ongoing match")
		}
		break
	}
	c.send("game.action", actionPayload(*started.MatchID, 1, 1, 0))
	if f = c.response("game.action"); f.Error != nil {
		t.Fatal("survivor input rejected")
	}
}

func TestDeferredErrorSurvivesLeaveFilter(t *testing.T) {
	g := &gatedHandler{GameHandler: game.NewMovement(), entered: make(chan struct{}), release: make(chan struct{})}
	s, _, dial := setupMovement(t, g)
	var tickFree sync.Once
	t.Cleanup(func() { tickFree.Do(func() { close(g.release) }) })
	c := dial()
	c.success("room.create", map[string]any{})
	c.read()
	v := assertStart(t, c)
	waitDone(t, g.entered)
	leaving, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	s.mu.Lock()
	s.beforeProcess = func(ctx context.Context, r protocol.Request) {
		if r.Op == "room.leave" {
			close(leaving)
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}
	s.mu.Unlock()
	sendID(t, c, "game.action", "deferred", actionPayload(*v.MatchID, 1, 1, 0))
	sendID(t, c, "room.leave", "leave", map[string]any{})
	waitDone(t, leaving)
	// The processor has selected Leave while no tick completion can yet exist.
	tickFree.Do(func() { close(g.release) })
	once.Do(func() { close(release) })
	f := c.read()
	if f.Op != "room.leave" || f.Error != nil {
		t.Fatalf("leave did not precede deferred completion: %+v", f)
	}
	f = c.response("game.action")
	if f.ID == nil || *f.ID != "deferred" {
		t.Fatal("deferred response lost correlation")
	}
	if f.Error != nil && f.Error.Code != "INPUT_CANCELED" {
		t.Fatalf("wrong deferred error %+v", f)
	}
	if f.Error == nil {
		var ack room.ActionAck
		if json.Unmarshal(f.Payload, &ack) != nil || ack.MatchID != *v.MatchID || ack.InputSeq != 1 || ack.ServerTick == 0 {
			t.Fatal("invalid applied ACK")
		}
	}
}

func TestSlowSnapshotReaderIsolation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.OutboundQueue = 4
	s, m, _, dial := setup(t, cfg)
	slow, p := registeredPeer(t, s, dial)
	slow.success("room.create", map[string]any{})
	slow.read()
	v := assertStart(t, slow)
	selected, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var free sync.Once
	vRoomID := v.RoomID
	t.Cleanup(func() { free.Do(func() { close(release) }) })
	s.mu.Lock()
	s.beforeWrite = func(ctx context.Context, v protocol.Envelope) {
		if v.Op == "game.snapshot" && v.Payload.(room.Snapshot).RoomID == vRoomID {
			once.Do(func() {
				close(selected)
				select {
				case <-release:
				case <-ctx.Done():
				}
			})
		}
	}
	s.mu.Unlock()
	waitDone(t, selected)
	for i := 0; i < 100; i++ {
		state := v
		state.SnapshotSeq = uint64(1000 + i)
		p.outbound.snapshot(protocol.Event("game.snapshot", state))
	}
	p.outbound.mu.Lock()
	hasLatest := p.outbound.latest != nil
	queueLen := len(p.outbound.reliableQueue)
	p.outbound.mu.Unlock()
	if !hasLatest || queueLen != 0 || p.ctx.Err() != nil {
		t.Fatal("snapshot pressure consumed reliable capacity or closed peer")
	}
	other := dial()
	other.success("room.create", map[string]any{})
	other.read()
	ov := assertStart(t, other)
	other.send("game.action", actionPayload(*ov.MatchID, 1, 1, 0))
	f := other.response("game.action")
	var ack room.ActionAck
	if f.Error != nil || json.Unmarshal(f.Payload, &ack) != nil || ack.ServerTick == 0 {
		t.Fatal("slow snapshot reader stalled another room")
	}
	dispatched := make(chan struct{})
	s.mu.Lock()
	s.afterRequest = func(_ context.Context, r protocol.Request) {
		if r.ID != nil && *r.ID == "alive" {
			close(dispatched)
		}
	}
	s.mu.Unlock()
	sendID(t, slow, "game.action", "alive", actionPayload(*v.MatchID, 1, 0, 1))
	waitDone(t, dispatched)
	for i := 0; i < 8; i++ {
		b, _ := json.Marshal(protocol.Envelope{Version: 1, Op: "room.list", RequestID: ptrID("pressure"), Payload: struct{}{}})
		slow.c.Write(context.Background(), websocket.MessageText, b)
	}
	waitDone(t, p.ctx.Done())
	waitDone(t, p.session.Done())
	other.send("game.action", actionPayload(*ov.MatchID, 2, 0, 1))
	f = other.response("game.action")
	if f.Error != nil || json.Unmarshal(f.Payload, &ack) != nil || ack.InputSeq != 2 {
		t.Fatal("reliable overflow affected healthy peer")
	}
	// Disconnect cleanup must remove only the saturated peer's room.
	rooms, e := m.ListPublic(context.Background())
	if e != nil || len(rooms) != 1 || rooms[0].RoomID != ov.RoomID {
		t.Fatal("slow peer membership cleanup")
	}
}
func ptrID(s string) *string { return &s }

// Membership, room and match checks must each reject stale reliable publications.
func TestCoreEventMembershipAndMatchFilters(t *testing.T) {
	_, m, _, dial := setup(t, DefaultConfig())
	host := dial()
	v := snapshot(t, host.success("room.create", map[string]any{}))
	host.read()
	session, e := m.Connect("filter-test")
	if e != nil {
		t.Fatal(e)
	}
	joined, e := m.Join(context.Background(), session, room.JoinOptions{RoomNumber: v.RoomNumber})
	if e != nil {
		t.Fatal(e)
	}
	initial := <-session.Events()
	p := &peer{session: session, ctx: context.Background(), outbound: newOutbox(16)}
	p.setMembership(joined)
	started, e := m.Start(context.Background(), session)
	if room.CodeOf(e) != "NOT_HOST" {
		t.Fatal("non-host fixture")
	}
	_ = started
	// Start with the real host session through its WS so the guest sees typed core events.
	host.success("room.start", map[string]any{})
	start := <-session.Events()
	if start.Op != "game.started" {
		t.Fatal("core started event missing")
	}
	if !p.processEvent(start) {
		t.Fatal("valid started rejected")
	}
	for _, op := range []string{"game.started", "room.state"} {
		got, _, ok := p.outbound.next(context.Background())
		if !ok || got.Op != op {
			t.Fatal("valid lifecycle did not expand")
		}
	}
	if !p.processEvent(start) {
		t.Fatal("duplicate start handling")
	}
	badRoom := start
	badRoom.Snapshot.RoomID = "other-room"
	badToken := start
	badToken.MembershipToken++
	staleEvent := room.Event{Op: "game.event", MembershipToken: start.MembershipToken, GameEvent: &room.GameEvent{MatchID: "old-match", Type: "stale"}}
	for _, event := range []room.Event{badRoom, badToken, staleEvent, initial} {
		if !p.processEvent(event) {
			t.Fatal("stale event closed peer")
		}
	}
	p.outbound.mu.Lock()
	n := len(p.outbound.reliableQueue)
	p.outbound.mu.Unlock()
	if n != 0 {
		t.Fatal("stale or duplicate publication leaked")
	}
	if e = m.Leave(context.Background(), session); e != nil {
		t.Fatal(e)
	}
	p.roomID = ""
	p.membershipToken = 0
	p.activeMatch = ""
	id := "completion-after-leave"
	if !p.processEvent(room.Event{Op: "game.action", RequestID: id, MembershipToken: start.MembershipToken, Ack: &room.ActionAck{MatchID: *start.Snapshot.MatchID, InputSeq: 1, ServerTick: 1}}) {
		t.Fatal("completion filtered")
	}
	got, _, ok := p.outbound.next(context.Background())
	if !ok || got.RequestID == nil || *got.RequestID != id {
		t.Fatal("completion request ID missing")
	}
	m.Disconnect(session)
}

// A new self-start result can overtake older queued core events in the processor.
func TestOlderStartCannotRewindActiveMatch(t *testing.T) {
	s, _, _, dial := setup(t, DefaultConfig())
	c, hp := registeredPeer(t, s, dial)
	c.success("room.create", map[string]any{})
	c.read()
	old := assertStart(t, c)
	p := &peer{session: hp.session, ctx: context.Background(), outbound: newOutbox(8)}
	p.setMembership(old)
	p.rememberStart(old)
	newer := old
	match := "newer-match"
	newer.MatchID = &match
	newer.Revision = old.Revision + 2
	newer.SnapshotSeq = old.SnapshotSeq + 2
	p.rememberStart(newer)
	_, token := hp.session.Membership()
	if !p.processEvent(room.Event{Op: "game.started", Snapshot: old, MembershipToken: token}) {
		t.Fatal("stale start closed peer")
	}
	if p.activeMatch != "newer-match" {
		t.Fatal("old start rewound active match")
	}
	p.outbound.mu.Lock()
	n := len(p.outbound.reliableQueue)
	p.outbound.mu.Unlock()
	if n != 0 {
		t.Fatal("overtaken start duplicated lifecycle")
	}
}

func movementPeerFixture(t *testing.T) (*room.Manager, *room.Session, *peer, room.Snapshot) {
	t.Helper()
	cfg := room.DefaultConfig()
	cfg.TickHz, cfg.SnapshotHz = 1, 1
	m, err := room.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	s, err := m.Connect("review")
	if err != nil {
		t.Fatal(err)
	}
	created, err := m.Create(context.Background(), s, room.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p := &peer{session: s, ctx: context.Background(), outbound: newOutbox(16)}
	p.setMembership(created)
	return m, s, p, created
}

func TestMemberLeaveFencesOldSnapshot(t *testing.T) {
	m, s, p, created := movementPeerFixture(t)
	g, err := m.Connect("guest")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Join(context.Background(), g, room.JoinOptions{RoomNumber: created.RoomNumber}); err != nil {
		t.Fatal(err)
	}
	started, err := m.Start(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	p.rememberStart(started)
	// Model a valid snapshot already accepted by the processor but not selected by writer.
	p.outbound.snapshot(protocol.Event("game.snapshot", started))
	if err = m.Leave(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	var leave room.Event
	deadline := time.After(time.Second)
	for leave.Op == "" {
		select {
		case e := <-s.Events():
			if e.Op == "room.state" && e.Snapshot.MatchID != nil && len(e.Snapshot.Members) == 1 {
				leave = e
			}
		case <-deadline:
			t.Fatal("missing leave publication")
		}
	}
	if !p.processEvent(leave) {
		t.Fatal("leave process failed")
	}
	sent, _, ok := p.outbound.next(context.Background())
	if !ok || sent.Op != "room.state" {
		t.Fatal("missing reliable leave state")
	}
	p.outbound.mu.Lock()
	latest := p.outbound.latest
	p.outbound.mu.Unlock()
	if latest != nil {
		old := latest.Payload.(room.Snapshot)
		t.Fatalf("after leave capture %d with %d members, unsent snapshot capture %d with %d members remains deliverable", leave.Snapshot.SnapshotSeq, len(leave.Snapshot.Members), old.SnapshotSeq, len(old.Members))
	}
}

type restartEndingHandler struct {
	game.GameHandler
	end   <-chan struct{}
	ended bool
}

func (h *restartEndingHandler) Tick(dt float64) (game.Outcome, error) {
	out, err := h.GameHandler.Tick(dt)
	select {
	case <-h.end:
		if !h.ended {
			h.ended = true
			out.Events = []game.Event{{Type: "old-terminal"}}
			out.EndReason = "old-finished"
		}
	default:
	}
	return out, err
}
func TestWireRestartPreservesOldTerminal(t *testing.T) {
	end := make(chan struct{})
	srv, m, dial := setupMovement(t, &restartEndingHandler{GameHandler: game.NewMovement(), end: end})
	c := dial()
	c.success("room.create", map[string]any{})
	c.read()
	old := assertStart(t, c)
	selected, release := make(chan struct{}), make(chan struct{})
	srv.mu.Lock()
	srv.beforeProcess = func(ctx context.Context, r protocol.Request) {
		if r.ID != nil && *r.ID == "restart" {
			close(selected)
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}
	srv.mu.Unlock()
	sendID(t, c, "room.start", "restart", map[string]any{})
	waitDone(t, selected)
	close(end)
	timeout := time.Now().Add(time.Second)
	for {
		rooms, err := m.ListPublic(context.Background())
		if err != nil {
			close(release)
			t.Fatal(err)
		}
		if len(rooms) == 1 && rooms[0].Phase == "WAITING" {
			break
		}
		if time.Now().After(timeout) {
			close(release)
			t.Fatal("old match did not end")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	sendID(t, c, "room.unknown", "restart-marker", map[string]any{})
	var ops []string
	for {
		f := c.read()
		if f.ID != nil && *f.ID == "restart-marker" {
			break
		}
		if f.Op == "game.snapshot" {
			continue
		}
		ops = append(ops, f.Op)
		if f.Op == "room.start" {
			now := snapshot(t, f)
			if now.MatchID == nil || *now.MatchID == *old.MatchID {
				t.Fatal("restart did not create fresh match")
			}
		}
	}
	if !reflect.DeepEqual(ops, []string{"game.event", "game.ended", "room.state", "room.start", "game.started", "room.state"}) {
		t.Fatalf("real WS new start overtook and erased queued old terminal chain: first non-snapshot frames=%v", ops)
	}
}

// A capture selected before the leave publication must fail write-start even after reliable drains.
func TestMemberLeaveFencesSelectedAndCoreLatest(t *testing.T) {
	for _, mode := range []string{"selected", "core-latest", "seq-floor"} {
		t.Run(mode, func(t *testing.T) {
			m, s, p, created := movementPeerFixture(t)
			g, err := m.Connect("guest")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.Join(context.Background(), g, room.JoinOptions{RoomNumber: created.RoomNumber}); err != nil {
				t.Fatal(err)
			}
			started, err := m.Start(context.Background(), s)
			if err != nil {
				t.Fatal(err)
			}
			p.rememberStart(started)
			var epoch uint64
			if mode == "selected" {
				p.outbound.snapshot(protocol.Event("game.snapshot", started))
				_, epoch, _ = p.outbound.next(context.Background())
			}
			if mode == "core-latest" {
				select {
				case <-s.SnapshotReady():
				case <-time.After(3 * time.Second):
					t.Fatal("missing core capture")
				}
			}
			if err = m.Leave(context.Background(), g); err != nil {
				t.Fatal(err)
			}
			var leave room.Event
			for leave.Op == "" {
				select {
				case e := <-s.Events():
					if e.Op == "room.state" && e.Snapshot.MatchID != nil && len(e.Snapshot.Members) == 1 {
						leave = e
					}
				case <-time.After(time.Second):
					t.Fatal("missing leave state")
				}
			}
			if mode == "seq-floor" {
				p.snapshotSeq = leave.Snapshot.SnapshotSeq + 10
			}
			floor := p.snapshotSeq
			if !p.processEvent(leave) {
				t.Fatal("leave publication failed")
			}
			got, _, ok := p.outbound.next(context.Background())
			if !ok || got.Op != "room.state" {
				t.Fatal("important state discarded")
			}
			if p.snapshotSeq < leave.Snapshot.SnapshotSeq || p.snapshotSeq < floor {
				t.Error("full state lowered or failed to advance capture floor")
			}
			if mode == "selected" && p.outbound.beginSnapshotWrite(epoch) {
				t.Error("selected pre-leave snapshot remains writable")
			}
			if mode == "core-latest" {
				p.processSnapshot()
				p.outbound.mu.Lock()
				old := p.outbound.latest
				p.outbound.mu.Unlock()
				if old != nil {
					capture := old.Payload.(room.Snapshot)
					if capture.SnapshotSeq <= leave.Snapshot.SnapshotSeq || len(capture.Members) != 1 {
						t.Error("pre-leave core latest reentered outbox after leave state")
					}
				}
			}
		})
	}
}

// A real socket writer must recheck its selected snapshot after the guest leaves.
func TestWireMemberLeaveFencesSelectedSnapshot(t *testing.T) {
	srv, m, _, dial := setup(t, DefaultConfig())
	host, hp := registeredPeer(t, srv, dial)
	created := snapshot(t, host.success("room.create", map[string]any{}))
	host.read()
	guest, err := m.Connect("core-guest")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Join(context.Background(), guest, room.JoinOptions{RoomNumber: created.RoomNumber}); err != nil {
		t.Fatal(err)
	}
	host.read()
	assertStart(t, host)
	selected, release := make(chan struct{}), make(chan struct{})
	var once, free sync.Once
	t.Cleanup(func() { free.Do(func() { close(release) }) })
	srv.mu.Lock()
	srv.beforeWrite = func(ctx context.Context, e protocol.Envelope) {
		if e.Op == "game.snapshot" {
			once.Do(func() {
				close(selected)
				select {
				case <-release:
				case <-ctx.Done():
				}
			})
		}
	}
	srv.mu.Unlock()
	waitDone(t, selected)
	if err = m.Leave(context.Background(), guest); err != nil {
		t.Fatal(err)
	}
	waitReliableOp(t, hp, "room.state")
	free.Do(func() { close(release) })
	f := host.read()
	var state room.Snapshot
	if f.Op != "room.state" || json.Unmarshal(f.Payload, &state) != nil || len(state.Members) != 1 || state.GameState == nil || len(state.GameState.Players) != 1 {
		t.Fatalf("selected old player list crossed leave fence: %+v", f)
	}
}
