package loadtest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	coopdock "github.com/sangjinsu/coopdock"
	"github.com/sangjinsu/coopdock/internal/game"
	"github.com/sangjinsu/coopdock/internal/protocol"
	"github.com/sangjinsu/coopdock/internal/room"
)

func shortConfig(endpoint string) Config {
	c := DefaultConfig()
	c.URL, c.Rooms, c.Players, c.Duration = endpoint, 1, 1, 300*time.Millisecond
	c.SetupTimeout, c.DrainTimeout, c.CleanupTimeout, c.RequestTimeout = 3*time.Second, time.Second, 2*time.Second, time.Second
	return c
}

func TestConfigRejectsInvalidBeforeConnecting(t *testing.T) {
	for _, change := range []func(*Config){
		func(c *Config) { c.URL = "http://localhost/ws" },
		func(c *Config) { c.URL = "ws://user:secret@localhost/ws" },
		func(c *Config) { c.URL = "ws://localhost/ws?game_id=other" },
		func(c *Config) { c.GameIDs = []string{"not valid"} },
		func(c *Config) { c.GameIDs = []string{"one", "one"} },
		func(c *Config) { c.Rooms = 0 },
		func(c *Config) { c.Players = 7 },
		func(c *Config) { c.Duration = 0 },
		func(c *Config) { c.InputHz = 0 },
		func(c *Config) { c.MaxPending = 0 },
	} {
		c := DefaultConfig()
		change(&c)
		report, err := Run(context.Background(), c)
		if err == nil || report.Success || report.Cleanup.ConnectionsClosed != 0 {
			t.Fatalf("invalid config reached a connection: %+v, %v", report, err)
		}
	}
}

func TestRunMultiroomIntegration(t *testing.T) {
	settings := coopdock.DefaultConfig()
	settings.Room.GameIDs = []string{"game-a", "game-b"}
	server, err := coopdock.New(settings)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(func() {
		httpServer.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	c := shortConfig("ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws")
	c.GameIDs, c.Rooms, c.Players, c.Duration = settings.Room.GameIDs, 2, 2, 450*time.Millisecond
	report, err := Run(context.Background(), c)
	if err != nil {
		t.Fatalf("real server: %v; report=%+v", err, report)
	}
	if !report.Success || len(report.Rooms) != 2 || len(report.ActiveGameIDs) != 2 || report.Total.InputsSent < 20 || report.Total.InputACKs != report.Total.InputsSent || report.Total.ACKRTT.Observations != report.Total.InputsSent {
		t.Fatalf("incomplete measured population: %+v", report)
	}
	for _, r := range report.Rooms {
		if r.CommonCaptures == 0 || r.PlayersMoved != 2 || r.PlayersStopped != 2 || r.Metrics.ZeroACKs != 2 || r.TickHz <= 0 || r.Metrics.SnapshotInterval.Observations == 0 {
			t.Fatalf("room never proved shared movement and stop: %+v", r)
		}
	}
	if report.Cleanup.RoomsRemoved != 2 || report.Cleanup.LeaveACKs != 4 || report.Cleanup.ConnectionsClosed != 4 || report.Cleanup.ReadersStopped != 4 || report.Cleanup.PendingRequests != 0 {
		t.Fatalf("cleanup: %+v", report.Cleanup)
	}
}

func TestRunRejectsAckWithoutMovement(t *testing.T) {
	endpoint, _ := controlledServer(t, false, true, 0)
	report, err := Run(context.Background(), shortConfig(endpoint))
	if err == nil || report.Success || report.Total.InputACKs == 0 || len(report.Rooms) != 1 || report.Rooms[0].CommonCaptures == 0 || !strings.Contains(err.Error(), "movement") {
		t.Fatalf("ACK-only nonmoving server accepted: %+v, %v", report, err)
	}
}

func TestRunRejectsMissingSnapshots(t *testing.T) {
	endpoint, _ := controlledServer(t, true, false, 0)
	report, err := Run(context.Background(), shortConfig(endpoint))
	if err == nil || report.Success || len(report.Rooms) != 1 || report.Rooms[0].CommonCaptures != 0 || !strings.Contains(err.Error(), "periodic") {
		t.Fatalf("start capture substituted for periodic snapshots: %+v, %v", report, err)
	}
}

func TestRunCancellationCleanup(t *testing.T) {
	endpoint, active := controlledServer(t, true, true, 0)
	c := shortConfig(endpoint)
	c.Duration = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	began := time.Now()
	report, err := Run(ctx, c)
	if !errors.Is(err, context.DeadlineExceeded) || report.Success || time.Since(began) > 3*time.Second {
		t.Fatalf("cancellation was not bounded: %+v, %v", report, err)
	}
	if report.Cleanup.LeaveACKs != 1 || report.Cleanup.RoomsRemoved != 1 || report.Cleanup.ConnectionsClosed != report.Cleanup.ReadersStopped {
		t.Fatalf("cancellation skipped independent cleanup: %+v", report.Cleanup)
	}
	deadline := time.After(time.Second)
	for active.Load() != 0 {
		select {
		case <-deadline:
			t.Fatal("owned WebSocket remained open")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestRunDrainedACKsRemainInPopulation(t *testing.T) {
	endpoint, _ := controlledServer(t, true, true, 90*time.Millisecond)
	c := shortConfig(endpoint)
	c.Duration = 260 * time.Millisecond
	report, err := Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if report.Total.InputsSent < 5 || report.Total.InputACKs != report.Total.InputsSent || report.Total.ACKRTT.Observations != report.Total.InputsSent || report.Total.ACKRTT.MaxMS < 80 {
		t.Fatalf("slow tail ACKs excluded from measured inputs: %+v", report.Total)
	}
}

func TestRunReportsConfiguredAndActiveGamesSeparately(t *testing.T) {
	endpoint, _ := controlledServer(t, true, true, 0)
	c := shortConfig(endpoint)
	c.GameIDs = []string{"game-a", "game-b"}
	report, err := Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Conditions.GameIDs) != 2 || len(report.ActiveGameIDs) != 1 || report.ActiveGameIDs[0] != "game-a" {
		t.Fatalf("configured games treated as exercised games: %+v", report)
	}
}

// The controlled wire server can deliberately violate movement or snapshot behavior.
type controlledState struct {
	active atomic.Int64
	joins  atomic.Int64
	leaves atomic.Int64
}

func controlledServer(t *testing.T, movement, snapshots bool, ackDelay time.Duration, modes ...string) (string, *atomic.Int64) {
	t.Helper()
	endpoint, state := controlledServerWithState(t, movement, snapshots, ackDelay, modes...)
	return endpoint, &state.active
}

func controlledServerWithState(t *testing.T, movement, snapshots bool, ackDelay time.Duration, modes ...string) (string, *controlledState) {
	t.Helper()
	state := &controlledState{}
	active := &state.active
	hasMode := func(want string) bool {
		for _, mode := range modes {
			if mode == want {
				return true
			}
		}
		return false
	}
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			return
		}
		active.Add(1)
		defer active.Add(-1)
		defer conn.CloseNow()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		send := func(op string, id *string, payload any) bool {
			data, _ := json.Marshal(protocol.Envelope{Version: 1, Op: op, RequestID: id, Payload: payload})
			return conn.Write(ctx, websocket.MessageText, data) == nil
		}
		sendError := func(op string, id *string, code string) bool {
			data, _ := json.Marshal(protocol.Envelope{Version: 1, Op: op, RequestID: id, Error: &protocol.Error{Code: code, Message: "Request failed."}})
			return conn.Write(ctx, websocket.MessageText, data) == nil
		}
		gameID := request.URL.Query().Get("game_id")
		if !send("session.guest", nil, map[string]string{"session_id": "one", "game_id": gameID}) {
			return
		}
		incoming := make(chan protocol.Request, 16)
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer close(incoming)
			for {
				_, data, err := conn.Read(ctx)
				if err != nil {
					return
				}
				msg, err := protocol.Decode(data)
				if err != nil {
					return
				}
				select {
				case incoming <- msg:
				case <-ctx.Done():
					return
				}
			}
		}()
		defer func() { cancel(); conn.CloseNow(); <-done }()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		match := "match-a"
		s := room.Snapshot{GameID: gameID, RoomID: "room-a", RoomNumber: "123456", Capacity: 1, Visibility: "public", Phase: "WAITING", HostID: "one", Members: []room.Member{{SessionID: "one"}}, Revision: 1, LastProcessedInputSeq: map[string]uint64{"one": 0}}
		playing, exists := false, false
		zeroApplied := false
		stoppedFrames := 0
		type delayed struct {
			id  *string
			ack room.ActionAck
			at  time.Time
		}
		queue := []delayed{}
		for {
			select {
			case msg, ok := <-incoming:
				if !ok {
					return
				}
				switch msg.Op {
				case "room.create":
					exists = true
					if !send(msg.Op, msg.ID, map[string]any{"snapshot": s}) {
						return
					}
				case "room.start":
					playing = true
					s.Phase = "PLAYING"
					s.MatchID = &match
					s.GameState = &game.State{Players: []game.Player{{SessionID: "one"}}}
					s.SnapshotSeq = 1
					s.Revision++
					if !send(msg.Op, msg.ID, map[string]any{"snapshot": s}) || !send("game.started", nil, map[string]any{"snapshot": s}) {
						return
					}
				case "game.action":
					s.LastProcessedInputSeq["one"] = msg.Input.InputSeq
					isZero := msg.Input.Action.Direction == (game.Vec3{})
					if isZero {
						zeroApplied = true
						stoppedFrames = 0
					}
					if movement && !(isZero && hasMode("ignore-zero")) {
						d := msg.Input.Action.Direction
						s.GameState.Players[0].Velocity = game.Vec3{X: d.X * 5, Y: d.Y * 5, Z: d.Z * 5}
					}
					tick := s.ServerTick
					if tick == 0 {
						tick = 1
					}
					if !(isZero && hasMode("drop-zero-ack")) {
						queue = append(queue, delayed{msg.ID, room.ActionAck{MatchID: match, InputSeq: msg.Input.InputSeq, ServerTick: tick}, time.Now().Add(ackDelay)})
					}
				case "room.join":
					state.joins.Add(1)
					if !exists {
						if !sendError(msg.Op, msg.ID, "ROOM_NOT_FOUND") {
							return
						}
					} else {
						if !send(msg.Op, msg.ID, map[string]any{"snapshot": s}) {
							return
						}
					}
				case "room.leave":
					leaveCount := state.leaves.Add(1)
					playing = false
					exists = leaveCount == 1 && (hasMode("retain-room-on-leave") || hasMode("reuse-room-on-leave"))
					if exists && hasMode("reuse-room-on-leave") {
						s.RoomID = "reused-room"
					}
					if !send(msg.Op, msg.ID, struct{}{}) {
						return
					}
				case "room.list":
					rooms := []room.Snapshot{}
					if exists && !hasMode("hide-rooms") {
						rooms = append(rooms, s)
					}
					if !send(msg.Op, msg.ID, map[string]any{"rooms": rooms}) {
						return
					}
				}
			case now := <-ticker.C:
				if playing {
					s.ServerTick++
					s.SnapshotSeq++
					if movement {
						p := &s.GameState.Players[0]
						p.Position.X += p.Velocity.X * .01
						p.Position.Y += p.Velocity.Y * .01
						p.Position.Z += p.Velocity.Z * .01
						if zeroApplied && hasMode("drift-after-zero") {
							stoppedFrames++
							if stoppedFrames > 1 {
								p.Position.X++
							}
						}
					}
				}
				for len(queue) > 0 && !now.Before(queue[0].at) {
					item := queue[0]
					queue = queue[1:]
					if !send("game.action", item.id, item.ack) {
						return
					}
				}
				if playing && snapshots {
					wire := s
					wire.LastProcessedInputSeq = map[string]uint64{"one": s.LastProcessedInputSeq["one"]}
					if !zeroApplied && wire.LastProcessedInputSeq["one"] > 0 {
						if hasMode("processed-future") {
							wire.LastProcessedInputSeq["one"] += 100
						}
						if hasMode("processed-regresses") && s.ServerTick%2 == 0 {
							wire.LastProcessedInputSeq["one"] = 0
						}
					}
					if !send("game.snapshot", nil, wire) {
						return
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}))
	t.Cleanup(h.Close)
	return "ws" + strings.TrimPrefix(h.URL, "http"), state
}

func TestCanceledRequestDoesNotWrite(t *testing.T) {
	endpoint, _ := controlledServer(t, true, true, 0)
	config := shortConfig(endpoint)
	lifetime, cancel := context.WithCancel(context.Background())
	r := &runner{config: config, ctx: lifetime, cancel: cancel, total: newMetricState(config.SampleLimit), failed: make(chan struct{}), changed: make(chan struct{}, 1)}
	rr := &roomRun{binding: binding{gameID: "default"}, captures: newCaptureWindow(config.CaptureLimit, 1), origins: make(map[string]game.Vec3), moved: make(map[string]bool), metrics: newMetricState(config.SampleLimit)}
	cl, err := r.connect(context.Background(), rr, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		r.mu.Lock()
		r.closing = true
		r.mu.Unlock()
		cancel()
		cl.conn.CloseNow()
		<-cl.done
	})
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := cl.request(canceled, "room.create", map[string]any{"capacity": 1, "visibility": "public"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("already canceled request: %v", err)
	}
	result, err := cl.request(context.Background(), "room.list", struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Rooms []room.Snapshot `json:"rooms"`
	}
	if err := json.Unmarshal(result.Payload, &list); err != nil || len(list.Rooms) != 0 {
		t.Fatalf("an already canceled request still created a room: %+v, %v", list, err)
	}
}

func TestRunRejectsUnconfirmedFinalZero(t *testing.T) {
	endpoint, _ := controlledServer(t, true, true, 0, "drop-zero-ack")
	c := shortConfig(endpoint)
	c.DrainTimeout = 150 * time.Millisecond
	report, err := Run(context.Background(), c)
	if err == nil || report.Success || report.Total.ZeroSent != 1 || report.Total.ZeroACKs != 0 || report.Total.Timeouts == 0 || report.Cleanup.PendingRequests != 1 || !strings.Contains(err.Error(), "final zero") {
		t.Fatalf("missing final zero ACK was hidden: %+v, %v", report, err)
	}
}

func TestRunRejectsZeroACKWithoutStop(t *testing.T) {
	endpoint, _ := controlledServer(t, true, true, 0, "ignore-zero")
	report, err := Run(context.Background(), shortConfig(endpoint))
	if err == nil || report.Success || report.Total.ZeroACKs != 1 || !strings.Contains(err.Error(), "velocity") {
		t.Fatalf("zero ACK substituted for stopped state: %+v, %v", report, err)
	}
}

func TestRunRejectsDriftAfterZero(t *testing.T) {
	endpoint, _ := controlledServer(t, true, true, 0, "drift-after-zero")
	report, err := Run(context.Background(), shortConfig(endpoint))
	if err == nil || report.Success || report.Total.ZeroACKs != 1 || !strings.Contains(err.Error(), "position changed") {
		t.Fatalf("zero velocity substituted for later stable position: %+v, %v", report, err)
	}
}

func TestRunPendingBoundFailsRatherThanSkippingInputs(t *testing.T) {
	endpoint, _ := controlledServer(t, true, true, 200*time.Millisecond)
	c := shortConfig(endpoint)
	c.MaxPending = 1
	report, err := Run(context.Background(), c)
	if err == nil || report.Success || report.Total.PendingOverflows == 0 || report.Total.UnconfirmedInputs == 0 {
		t.Fatalf("pending overflow was silently skipped: %+v, %v", report, err)
	}
}

func TestInvalidURLCredentialsAreNotReported(t *testing.T) {
	c := DefaultConfig()
	c.URL = "ws://user:do-not-log-secret@localhost/ws"
	report, err := Run(context.Background(), c)
	data, encodeErr := json.Marshal(report)
	if err == nil || encodeErr != nil || strings.Contains(string(data), "do-not-log-secret") {
		t.Fatalf("invalid endpoint leaked URL credentials: %s, %v", data, err)
	}
}

func TestRunRejectsHiddenRemainingRoom(t *testing.T) {
	for _, mode := range []string{"retain-room-on-leave", "reuse-room-on-leave"} {
		t.Run(mode, func(t *testing.T) {
			endpoint, state := controlledServerWithState(t, true, true, 0, mode, "hide-rooms")
			report, err := Run(context.Background(), shortConfig(endpoint))
			if err == nil || report.Success || report.Cleanup.RoomsRemoved != 0 || len(report.Cleanup.Errors) == 0 {
				t.Fatalf("hidden room was declared removed after fake leave ACK: %+v, %v", report, err)
			}
			if state.joins.Load() != 1 || state.leaves.Load() != 2 {
				t.Fatalf("successful absence probe was not immediately left: joins=%d leaves=%d", state.joins.Load(), state.leaves.Load())
			}
			if mode == "reuse-room-on-leave" && !strings.Contains(strings.Join(report.Cleanup.Errors, " "), "reused") {
				t.Fatalf("reused room number was not distinguished: %+v", report.Cleanup)
			}
			if report.Cleanup.ConnectionsClosed != 1 || report.Cleanup.ReadersStopped != 1 {
				t.Fatalf("failed probe did not reclaim owned reader: %+v", report.Cleanup)
			}
			deadline := time.After(time.Second)
			for state.active.Load() != 0 {
				select {
				case <-deadline:
					t.Fatal("absence probe left its socket open")
				case <-time.After(time.Millisecond):
				}
			}
		})
	}
}

func TestRunRejectsProcessedSequenceCorruption(t *testing.T) {
	for _, mode := range []string{"processed-future", "processed-regresses"} {
		t.Run(mode, func(t *testing.T) {
			endpoint, _ := controlledServer(t, true, true, 0, mode)
			report, err := Run(context.Background(), shortConfig(endpoint))
			if err == nil || report.Success || !strings.Contains(err.Error(), "processed sequence") {
				t.Fatalf("corrupt processed sequence accepted: %+v, %v", report, err)
			}
			if report.Cleanup.ConnectionsClosed != 1 || report.Cleanup.ReadersStopped != 1 {
				t.Fatalf("corrupt stream leaked reader: %+v", report.Cleanup)
			}
		})
	}
}

func TestSnapshotProcessedBoundsPerMember(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		value    uint64
	}{
		{"local-future", "one", 2}, {"peer-future", "two", 2}, {"local-regression", "one", 0}, {"peer-regression", "two", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &runner{config: DefaultConfig(), total: newMetricState(10)}
			rr := &roomRun{binding: fixtureBinding(), captures: newCaptureWindow(4, 2), origins: make(map[string]game.Vec3), moved: make(map[string]bool), metrics: newMetricState(10)}
			local := &client{run: r, room: rr, sessionID: "one", highestSubmittedSequence: 1}
			peer := &client{run: r, room: rr, index: 1, sessionID: "two", highestSubmittedSequence: 1}
			rr.clients = []*client{local, peer}
			state := fixtureSnapshot()
			state.LastProcessedInputSeq = map[string]uint64{"one": 0, "two": 0}
			if err := local.acceptSnapshotLocked(state, "game.started", time.Now()); err != nil {
				t.Fatalf("setup zero must be allowed: %v", err)
			}
			state = fixtureSnapshot()
			state.SnapshotSeq++
			state.ServerTick++
			if err := local.acceptSnapshotLocked(state, "game.snapshot", time.Now()); err != nil {
				t.Fatalf("exact submitted bound must be allowed: %v", err)
			}
			state.SnapshotSeq++
			state.ServerTick++
			state.LastProcessedInputSeq[tc.id] = tc.value
			if err := local.acceptSnapshotLocked(state, "game.snapshot", time.Now()); err == nil {
				t.Fatal("snapshot did not validate processed seq for every room member")
			}
		})
	}
}

func TestRunRemovalProofWithTruncatedList(t *testing.T) {
	settings := coopdock.DefaultConfig()
	settings.Room.ListLimit = 1
	server, err := coopdock.New(settings)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(func() {
		httpServer.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	c := shortConfig("ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws")
	c.Rooms = 2
	report, err := Run(context.Background(), c)
	if err != nil || !report.Success || report.Cleanup.RoomsRemoved != 2 || report.Total.ServerErrors != 0 || report.Cleanup.PendingRequests != 0 {
		t.Fatalf("valid removal probe depended on list pagination or counted expected absence as error: %+v, %v", report, err)
	}
}
