package room

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sangjinsu/coopdock/internal/game"
)

func awaitGameEvent(t *testing.T, s *Session, op string) Event {
	t.Helper()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case e := <-s.Events():
			if e.Op == op {
				return e
			}
		case <-timeout.C:
			t.Fatalf("missing %s", op)
		}
	}
}

type manualClock struct {
	ticks, snapshots         chan time.Time
	tickStops, snapshotStops atomic.Int32
}

func realtimeSetup(t *testing.T, edit func(*Config)) (*Manager, *manualClock) {
	t.Helper()
	c := &manualClock{ticks: make(chan time.Time, 16), snapshots: make(chan time.Time, 16)}
	cfg := DefaultConfig()
	if edit != nil {
		edit(&cfg)
	}
	cfg.newTicker = func(d time.Duration) (<-chan time.Time, func()) {
		if d == time.Second/time.Duration(cfg.TickHz) {
			return c.ticks, func() { c.tickStops.Add(1) }
		}
		return c.snapshots, func() { c.snapshotStops.Add(1) }
	}
	return setup(t, cfg), c
}
func startMatch(t *testing.T, m *Manager, s *Session) Snapshot {
	t.Helper()
	v, e := m.Start(context.Background(), s)
	if e != nil {
		t.Fatal(e)
	}
	e2 := awaitGameEvent(t, s, "game.started")
	if e2.Snapshot.MatchID == nil || *e2.Snapshot.MatchID != *v.MatchID {
		t.Fatal("started snapshot mismatch")
	}
	return v
}
func move(v Snapshot, seq uint64, x, z float64) Input {
	return Input{MatchID: *v.MatchID, InputSeq: seq, Action: game.Action{Type: "move", Direction: game.Vec3{X: x, Z: z}}}
}
func latest(t *testing.T, c *manualClock, s *Session) Snapshot {
	t.Helper()
	c.snapshots <- time.Now()
	select {
	case <-s.SnapshotReady():
	case <-time.After(2 * time.Second):
		t.Fatal("missing snapshot")
	}
	v, ok := s.TakeSnapshot()
	if !ok {
		t.Fatal("missing capture")
	}
	return v
}
func submit(t *testing.T, m *Manager, s *Session, id string, in Input) {
	t.Helper()
	if e := m.SubmitInput(context.Background(), s, id, in); e != nil {
		t.Fatal(e)
	}
}
func TestInputAckAfterTickAndOverflow(t *testing.T) {
	m, c := realtimeSetup(t, func(cfg *Config) { cfg.InputQueue = 1; cfg.InputsPerTick = 1 })
	h := connect(t, m, "host")
	create(t, m, h, CreateOptions{})
	v := startMatch(t, m, h)
	submit(t, m, h, "a", move(v, 1, 0, 1))
	code(t, m.SubmitInput(context.Background(), h, "b", move(v, 2, 0, 1)), "SERVER_BUSY")
	select {
	case e := <-h.Events():
		if e.Op == "game.action" {
			t.Fatal("ACK before tick")
		}
	default:
	}
	c.ticks <- time.Now()
	e := awaitGameEvent(t, h, "game.action")
	if e.Err != nil || e.Ack == nil || e.Ack.InputSeq != 1 || e.Ack.ServerTick != 1 || e.RequestID != "a" {
		t.Fatalf("bad completion: %+v", e)
	}
	state := latest(t, c, h)
	if state.ServerTick != 1 || state.LastProcessedInputSeq[h.ID()] != 1 || math.Abs(state.GameState.Players[0].Position.Z-5.0/30) > 1e-12 {
		t.Fatalf("inconsistent capture %+v", state)
	}
	// A completed receipt returns its credit.
	submit(t, m, h, "c", move(v, 2, 0, 0))
	c.ticks <- time.Now()
	awaitGameEvent(t, h, "game.action")
}

type endingHandler struct {
	game.GameHandler
	ticks int
	endAt int
}

func (h *endingHandler) Tick(dt float64) (game.Outcome, error) {
	out, err := h.GameHandler.Tick(dt)
	h.ticks++
	if h.ticks == h.endAt {
		out.EndReason = "test_complete"
	}
	return out, err
}
func TestMatchRestartAndStaleMembership(t *testing.T) {
	m, c := realtimeSetup(t, func(cfg *Config) {
		cfg.HandlerFactory = func() game.GameHandler { return &endingHandler{GameHandler: game.NewMovement(), endAt: 2} }
	})
	h := connect(t, m, "host")
	base := create(t, m, h, CreateOptions{})
	g := connect(t, m, "guest")
	if _, e := m.Join(context.Background(), g, JoinOptions{RoomNumber: base.RoomNumber}); e != nil {
		t.Fatal(e)
	}
	v := startMatch(t, m, h)
	awaitGameEvent(t, g, "game.started")
	submit(t, m, h, "five", move(v, 5, 1, 0))
	c.ticks <- time.Now()
	awaitGameEvent(t, h, "game.action")
	c.ticks <- time.Now()
	ended := awaitGameEvent(t, h, "game.ended")
	if ended.Ended == nil || ended.Ended.ServerTick != 2 || ended.Ended.Reason != "test_complete" || ended.Snapshot.Phase != "WAITING" || ended.Snapshot.MatchID != nil || ended.Snapshot.ServerTick != 0 || ended.Snapshot.LastProcessedInputSeq[h.ID()] != 5 {
		t.Fatalf("bad end %+v", ended)
	}
	next := startMatch(t, m, h)
	if *next.MatchID == *v.MatchID || next.ServerTick != 0 || next.LastProcessedInputSeq[h.ID()] != 5 || next.SnapshotSeq <= ended.Snapshot.SnapshotSeq {
		t.Fatal("restart reset identity or sequence")
	}
	code(t, m.SubmitInput(context.Background(), h, "old", move(v, 6, 1, 0)), "STALE_MATCH")
	code(t, m.SubmitInput(context.Background(), h, "dup", move(next, 5, 1, 0)), "INVALID_INPUT_SEQ")
	submit(t, m, h, "six", move(next, 6, 1, 0))
	c.ticks <- time.Now()
	awaitGameEvent(t, h, "game.action")
	// Accepted old membership input must not survive leave and rejoin.
	submit(t, m, g, "old-membership", move(next, 10, 0, 1))
	if e := m.Leave(context.Background(), g); e != nil {
		t.Fatal(e)
	}
	canceled := awaitGameEvent(t, g, "game.action")
	code(t, canceled.Err, "INPUT_CANCELED")
	c.ticks <- time.Now()
	awaitGameEvent(t, h, "game.ended")
	if _, e := m.Join(context.Background(), g, JoinOptions{RoomNumber: base.RoomNumber}); e != nil {
		t.Fatal(e)
	}
	third := startMatch(t, m, h)
	c.ticks <- time.Now()
	state := latest(t, c, h)
	if state.GameState.Players[1].Velocity != (game.Vec3{}) || state.LastProcessedInputSeq[g.ID()] != 0 || state.MatchID == nil || *state.MatchID != *third.MatchID {
		t.Fatal("old receipt changed rejoined player")
	}
}
func TestRejectedSequenceIsNotConsumed(t *testing.T) {
	m, c := realtimeSetup(t, nil)
	h := connect(t, m, "host")
	create(t, m, h, CreateOptions{})
	v := startMatch(t, m, h)
	bad := move(v, 9, 2, 0)
	code(t, m.SubmitInput(context.Background(), h, "bad", bad), "INVALID_MESSAGE")
	stale := move(v, 9, 1, 0)
	stale.MatchID = "stale"
	code(t, m.SubmitInput(context.Background(), h, "stale", stale), "STALE_MATCH")
	submit(t, m, h, "eight", move(v, 8, 1, 0))
	c.ticks <- time.Now()
	awaitGameEvent(t, h, "game.action")
	code(t, m.SubmitInput(context.Background(), h, "seven", move(v, 7, 1, 0)), "INVALID_INPUT_SEQ")
	submit(t, m, h, "ten", move(v, 10, 1, 0))
	submit(t, m, h, "eleven", move(v, 11, 0, 1))
	c.ticks <- time.Now()
	a, b := awaitGameEvent(t, h, "game.action"), awaitGameEvent(t, h, "game.action")
	if a.RequestID != "ten" || b.RequestID != "eleven" || a.Ack.ServerTick != 2 || b.Ack.ServerTick != 2 {
		t.Fatal("FIFO batch did not ACK at same tick")
	}
	state := latest(t, c, h)
	if state.LastProcessedInputSeq[h.ID()] != 11 || state.GameState.Players[0].Velocity != (game.Vec3{Z: 5}) {
		t.Fatal("last setter did not win")
	}
}
func TestInputDeadlineAndEndCancel(t *testing.T) {
	m, c := realtimeSetup(t, func(cfg *Config) {
		cfg.InputQueue = 2
		cfg.InputsPerTick = 1
		cfg.HandlerFactory = func() game.GameHandler { return &endingHandler{GameHandler: game.NewMovement(), endAt: 2} }
	})
	h := connect(t, m, "host")
	create(t, m, h, CreateOptions{})
	v := startMatch(t, m, h)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if e := m.SubmitInput(ctx, h, "expired", move(v, 9, 1, 0)); e != nil {
		t.Fatal(e)
	}
	<-ctx.Done()
	c.ticks <- time.Unix(0, 0)
	code(t, awaitGameEvent(t, h, "game.action").Err, "REQUEST_TIMEOUT")
	submit(t, m, h, "eight", move(v, 8, 1, 0))
	submit(t, m, h, "pending", move(v, 10, 0, 1))
	c.ticks <- time.Now()
	a, b := awaitGameEvent(t, h, "game.action"), awaitGameEvent(t, h, "game.action")
	if a.Ack == nil || a.Ack.InputSeq != 8 || a.Ack.ServerTick != 2 {
		t.Fatal("expired input consumed sequence")
	}
	code(t, b.Err, "INPUT_CANCELED")
	awaitGameEvent(t, h, "game.ended")
	next := startMatch(t, m, h)
	submit(t, m, h, "credit1", move(next, 9, 1, 0))
	submit(t, m, h, "credit2", move(next, 10, 1, 0))
}

type hookHandler struct {
	game.GameHandler
	leaveError bool
	tickError  bool
	applyError bool
	pulse      bool
}

func (h *hookHandler) OnLeave(id string) (game.Outcome, error) {
	out, e := h.GameHandler.OnLeave(id)
	if h.leaveError {
		return out, errors.New("leave failed")
	}
	return out, e
}
func (h *hookHandler) Tick(dt float64) (game.Outcome, error) {
	out, e := h.GameHandler.Tick(dt)
	if h.tickError {
		return out, errors.New("tick failed")
	}
	if h.pulse {
		h.pulse = false
		out.Events = []game.Event{{Type: "pulse", SessionID: h.GameHandler.Snapshot().Players[0].SessionID}}
	}
	return out, e
}
func (h *hookHandler) Apply(id string, a game.Action) error {
	if h.applyError {
		return errors.New("apply failed")
	}
	return h.GameHandler.Apply(id, a)
}
func TestPlayingLeaveAndTickerShutdown(t *testing.T) {
	for _, leaveError := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "hook-error"}[leaveError], func(t *testing.T) {
			m, c := realtimeSetup(t, func(cfg *Config) {
				cfg.HandlerFactory = func() game.GameHandler { return &hookHandler{GameHandler: game.NewMovement(), leaveError: leaveError} }
			})
			h := connect(t, m, "host")
			base := create(t, m, h, CreateOptions{})
			g := connect(t, m, "guest")
			if _, e := m.Join(context.Background(), g, JoinOptions{RoomNumber: base.RoomNumber}); e != nil {
				t.Fatal(e)
			}
			startMatch(t, m, h)
			awaitGameEvent(t, g, "game.started")
			if e := m.Leave(context.Background(), h); e != nil {
				t.Fatal(e)
			}
			ls, e := listForTest(t, m, context.Background())
			if e != nil || len(ls) != 1 {
				t.Fatal(ls, e)
			}
			v := ls[0]
			if v.HostID != g.ID() || len(v.Members) != 1 {
				t.Fatal("membership restored after hook")
			}
			if leaveError {
				if v.Phase != "WAITING" || v.GameState != nil {
					t.Fatal("hook error did not end")
				}
			} else {
				if v.Phase != "PLAYING" || len(v.GameState.Players) != 1 || v.GameState.Players[0].SessionID != g.ID() {
					t.Fatal("player not removed")
				}
			}
			if e := m.Leave(context.Background(), g); e != nil {
				t.Fatal(e)
			}
			if e := m.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
			if c.tickStops.Load() != 1 || c.snapshotStops.Load() != 1 {
				t.Fatal("tickers did not stop exactly once")
			}
			ls, e = m.ListPublic(context.Background(), g)
			if CodeOf(e) != "SESSION_CLOSED" || len(ls) != 0 {
				t.Fatal("closed session listed rooms", ls, e)
			}
			// Stopped ticker channels deliberately remain open.
			c.ticks <- time.Now()
			c.snapshots <- time.Now()
		})
	}
}
func TestNoCatchUpAndAtomicSnapshot(t *testing.T) {
	m, c := realtimeSetup(t, nil)
	h := connect(t, m, "host")
	create(t, m, h, CreateOptions{})
	v := startMatch(t, m, h)
	submit(t, m, h, "one", move(v, 1, 1, 0))
	c.ticks <- time.Unix(0, 0)
	awaitGameEvent(t, h, "game.action")
	first := latest(t, c, h)
	if first.ServerTick != 1 || math.Abs(first.GameState.Players[0].Position.X-5.0/30) > 1e-12 {
		t.Fatal("caught up old timestamp")
	}
	submit(t, m, h, "two", move(v, 2, 1, 0))
	c.ticks <- time.Now()
	awaitGameEvent(t, h, "game.action")
	second := latest(t, c, h)
	second.GameState.Players[0].Position.X = 99
	second.LastProcessedInputSeq[h.ID()] = 99
	if first.ServerTick != 1 || first.LastProcessedInputSeq[h.ID()] != 1 || math.Abs(first.GameState.Players[0].Position.X-5.0/30) > 1e-12 {
		t.Fatal("snapshot mutated")
	}
	third := latest(t, c, h)
	if third.LastProcessedInputSeq[h.ID()] != 2 || math.Abs(third.GameState.Players[0].Position.X-10.0/30) > 1e-12 {
		t.Fatal("snapshot leaked into actor")
	}
}
func TestOneShotEventOrder(t *testing.T) {
	m, c := realtimeSetup(t, func(cfg *Config) {
		cfg.HandlerFactory = func() game.GameHandler { return &hookHandler{GameHandler: game.NewMovement(), pulse: true} }
	})
	h := connect(t, m, "host")
	create(t, m, h, CreateOptions{})
	v := startMatch(t, m, h)
	submit(t, m, h, "one", move(v, 1, 1, 0))
	c.ticks <- time.Now()
	// Latest snapshot replacement must preserve the queued reliable completion and event.
	latest(t, c, h)
	latest(t, c, h)
	a := awaitGameEvent(t, h, "game.action")
	e := awaitGameEvent(t, h, "game.event")
	if a.Ack == nil || e.GameEvent == nil || e.GameEvent.EventID == "" || e.GameEvent.Type != "pulse" || e.GameEvent.SessionID != h.ID() || e.GameEvent.ServerTick != a.Ack.ServerTick {
		t.Fatal("bad event ordering or identity")
	}
	latest(t, c, h)
	latest(t, c, h)
	submit(t, m, h, "two", move(v, 2, 1, 0))
	c.ticks <- time.Now()
	awaitGameEvent(t, h, "game.action")
	select {
	case e := <-h.Events():
		if e.Op == "game.event" {
			t.Fatal("duplicate event")
		}
	default:
	}
}

// A canceled transport request after admission must not cancel the copied receipt.
func TestAcceptedInputSurvivesCallerCancel(t *testing.T) {
	m, c := realtimeSetup(t, nil)
	h := connect(t, m, "host")
	create(t, m, h, CreateOptions{})
	v := startMatch(t, m, h)
	ctx, cancel := context.WithCancel(context.Background())
	if err := m.SubmitInput(ctx, h, "accepted", move(v, 1, 1, 0)); err != nil {
		t.Fatal(err)
	}
	cancel()
	c.ticks <- time.Now()
	e := awaitGameEvent(t, h, "game.action")
	if e.Err != nil || e.Ack == nil || e.Ack.InputSeq != 1 {
		t.Fatal("accepted input depended on caller context")
	}
}
func TestInputCreditIncludesPendingFIFO(t *testing.T) {
	m, c := realtimeSetup(t, func(cfg *Config) { cfg.InputQueue = 2; cfg.InputsPerTick = 1 })
	h := connect(t, m, "host")
	create(t, m, h, CreateOptions{})
	v := startMatch(t, m, h)
	submit(t, m, h, "one", move(v, 1, 1, 0))
	submit(t, m, h, "two", move(v, 2, 0, 1))
	// Listing is an actor barrier: both receipts may have moved to pending, but retain credit.
	if _, err := listForTest(t, m, context.Background()); err != nil {
		t.Fatal(err)
	}
	code(t, m.SubmitInput(context.Background(), h, "overflow", move(v, 3, 0, 0)), "SERVER_BUSY")
	c.ticks <- time.Now()
	a := awaitGameEvent(t, h, "game.action")
	if a.Ack.InputSeq != 1 {
		t.Fatal("FIFO order changed")
	}
	submit(t, m, h, "three", move(v, 3, 0, 0))
	code(t, m.SubmitInput(context.Background(), h, "overflow2", move(v, 4, 1, 0)), "SERVER_BUSY")
	c.ticks <- time.Now()
	b := awaitGameEvent(t, h, "game.action")
	c.ticks <- time.Now()
	d := awaitGameEvent(t, h, "game.action")
	if b.Ack.InputSeq != 2 || d.Ack.InputSeq != 3 || b.Ack.ServerTick != 2 || d.Ack.ServerTick != 3 {
		t.Fatal("pending credits or FIFO were lost")
	}
}
func TestBatchDuplicateAndBackwardSequence(t *testing.T) {
	m, c := realtimeSetup(t, nil)
	h := connect(t, m, "host")
	create(t, m, h, CreateOptions{})
	v := startMatch(t, m, h)
	for _, in := range []Input{move(v, 10, 1, 0), move(v, 10, 0, 1), move(v, 9, 0, 1), move(v, 11, 0, 1)} {
		submit(t, m, h, "batch", in)
	}
	c.ticks <- time.Now()
	a := awaitGameEvent(t, h, "game.action")
	b := awaitGameEvent(t, h, "game.action")
	d := awaitGameEvent(t, h, "game.action")
	e := awaitGameEvent(t, h, "game.action")
	if a.Ack == nil || a.Ack.InputSeq != 10 || e.Ack == nil || e.Ack.InputSeq != 11 || e.Ack.ServerTick != 1 {
		t.Fatal("valid batch sequence did not commit")
	}
	code(t, b.Err, "INVALID_INPUT_SEQ")
	code(t, d.Err, "INVALID_INPUT_SEQ")
	state := latest(t, c, h)
	if state.LastProcessedInputSeq[h.ID()] != 11 || state.GameState.Players[0].Velocity != (game.Vec3{Z: 5}) {
		t.Fatal("invalid duplicate mutated state")
	}
}

type startBarrierHandler struct {
	game.GameHandler
	entered  chan struct{}
	release  chan struct{}
	startErr error
}

func (h *startBarrierHandler) Start(ids []string) error {
	if h.entered != nil {
		h.entered <- struct{}{}
		<-h.release
	}
	if h.startErr != nil {
		return h.startErr
	}
	return h.GameHandler.Start(ids)
}
func TestStartRechecksDeadlineAndDisconnect(t *testing.T) {
	for _, kind := range []string{"deadline", "disconnect"} {
		t.Run(kind, func(t *testing.T) {
			entered := make(chan struct{}, 1)
			release := make(chan struct{})
			m, c := realtimeSetup(t, func(cfg *Config) {
				cfg.HandlerFactory = func() game.GameHandler {
					return &startBarrierHandler{GameHandler: game.NewMovement(), entered: entered, release: release}
				}
			})
			h := connect(t, m, "host")
			create(t, m, h, CreateOptions{})
			ctx, cancel := context.WithCancel(context.Background())
			if kind == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
			}
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := m.Start(ctx, h); result <- err }()
			await(t, entered)
			// A manager operation must complete while the game handler is blocked.
			ready := make(chan bool, 1)
			go func() { ready <- m.Ready() }()
			select {
			case ok := <-ready:
				if !ok {
					t.Fatal("manager unexpectedly closed")
				}
			case <-time.After(time.Second):
				close(release)
				t.Fatal("handler held admission gate")
			}
			var disconnected chan struct{}
			if kind == "disconnect" {
				disconnected = make(chan struct{})
				go func() { m.Disconnect(h); close(disconnected) }()
				await(t, h.Done())
				close(release)
				err := awaitErr(t, result)
				code(t, err, "SESSION_CLOSED")
				await(t, disconnected)
			} else {
				<-ctx.Done()
				err := awaitErr(t, result)
				code(t, err, "REQUEST_TIMEOUT")
				close(release)
			}
			ls, err := listForTest(t, m, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if kind == "deadline" && (len(ls) != 1 || ls[0].Phase != "WAITING" || ls[0].MatchID != nil) {
				t.Fatal("expired start committed")
			}
			if c.tickStops.Load() != 0 || c.snapshotStops.Load() != 0 {
				t.Fatal("failed start created tickers")
			}
		})
	}
}
func TestHandlerFailureDoesNotConsumeSequence(t *testing.T) {
	for _, kind := range []string{"start", "nil", "apply", "tick"} {
		t.Run(kind, func(t *testing.T) {
			m, c := realtimeSetup(t, func(cfg *Config) {
				cfg.HandlerFactory = func() game.GameHandler {
					switch kind {
					case "start":
						return &startBarrierHandler{GameHandler: game.NewMovement(), startErr: errors.New("start failed")}
					case "nil":
						return nil
					default:
						return &hookHandler{GameHandler: game.NewMovement(), applyError: kind == "apply", tickError: kind == "tick"}
					}
				}
			})
			h := connect(t, m, "host")
			create(t, m, h, CreateOptions{})
			if kind == "start" || kind == "nil" {
				_, err := m.Start(context.Background(), h)
				code(t, err, "GAME_ERROR")
				ls, err := listForTest(t, m, context.Background())
				if err != nil || len(ls) != 1 || ls[0].Phase != "WAITING" || ls[0].MatchID != nil {
					t.Fatal("failed start changed phase")
				}
				return
			}
			v := startMatch(t, m, h)
			submit(t, m, h, "failure", move(v, 9, 1, 0))
			c.ticks <- time.Now()
			e := awaitGameEvent(t, h, "game.action")
			code(t, e.Err, "GAME_ERROR")
			if e.Ack != nil {
				t.Fatal("handler failure ACKed")
			}
			ls, err := listForTest(t, m, context.Background())
			if err != nil || len(ls) != 1 || ls[0].LastProcessedInputSeq[h.ID()] != 0 {
				t.Fatal("handler failure consumed seq")
			}
			if kind == "tick" {
				end := awaitGameEvent(t, h, "game.ended")
				if end.Ended.Reason != "GAME_ERROR" || end.Ended.ServerTick != 0 || ls[0].Phase != "WAITING" {
					t.Fatal("tick failure did not end without committed tick")
				}
			}
		})
	}
}
func TestStartAdmissionAndPlayingJoin(t *testing.T) {
	m, _ := realtimeSetup(t, nil)
	h := connect(t, m, "host")
	base := create(t, m, h, CreateOptions{})
	g := connect(t, m, "guest")
	if _, err := m.Join(context.Background(), g, JoinOptions{RoomNumber: base.RoomNumber}); err != nil {
		t.Fatal(err)
	}
	_, err := m.Start(context.Background(), g)
	code(t, err, "NOT_HOST")
	v := startMatch(t, m, h)
	_, err = m.Start(context.Background(), h)
	code(t, err, "ROOM_IN_PROGRESS")
	outsider := connect(t, m, "outsider")
	_, err = m.Join(context.Background(), outsider, JoinOptions{RoomNumber: base.RoomNumber})
	code(t, err, "ROOM_IN_PROGRESS")
	for _, seq := range []uint64{0, 9007199254740992} {
		code(t, m.SubmitInput(context.Background(), h, "invalid", move(v, seq, 1, 0)), "INVALID_INPUT_SEQ")
	}
}
func TestReliableLifecycleSnapshotIsDetached(t *testing.T) {
	m, c := realtimeSetup(t, nil)
	h := connect(t, m, "host")
	create(t, m, h, CreateOptions{})
	v, err := m.Start(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	e := awaitGameEvent(t, h, "game.started")
	if v.SnapshotSeq != e.Snapshot.SnapshotSeq {
		t.Fatal("transition captured different full states")
	}
	e.Snapshot.GameState.Players[0].Position.X = 99
	e.Snapshot.LastProcessedInputSeq[h.ID()] = 99
	if v.GameState.Players[0].Position.X != 0 || v.LastProcessedInputSeq[h.ID()] != 0 {
		t.Fatal("response shared event state")
	}
	current := latest(t, c, h)
	if current.GameState.Players[0].Position.X != 0 || current.LastProcessedInputSeq[h.ID()] != 0 {
		t.Fatal("event mutated actor")
	}
}
func TestStaleMembershipReceiptCannotApplyAfterRejoin(t *testing.T) {
	m, c := realtimeSetup(t, nil)
	h := connect(t, m, "host")
	base := create(t, m, h, CreateOptions{})
	g := connect(t, m, "guest")
	if _, err := m.Join(context.Background(), g, JoinOptions{RoomNumber: base.RoomNumber}); err != nil {
		t.Fatal(err)
	}
	_, oldToken := g.Membership()
	if err := m.Leave(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Join(context.Background(), g, JoinOptions{RoomNumber: base.RoomNumber}); err != nil {
		t.Fatal(err)
	}
	v := startMatch(t, m, h)
	// Model a receipt retained outside its old membership, with a current match to isolate token validation.
	m.mu.Lock()
	g.mu.Lock()
	r := g.room
	r.inputSlots <- struct{}{}
	r.inputs <- receipt{s: g, roomID: r.id, token: oldToken, requestID: "stale", deadline: time.Now().Add(time.Second), in: move(v, 9, 1, 0)}
	g.mu.Unlock()
	m.mu.Unlock()
	c.ticks <- time.Now()
	e := awaitGameEvent(t, g, "game.action")
	code(t, e.Err, "INPUT_CANCELED")
	state := latest(t, c, h)
	if state.LastProcessedInputSeq[g.ID()] != 0 || state.GameState.Players[1].Velocity != (game.Vec3{}) {
		t.Fatal("stale membership affected rejoined player")
	}
}

type leaveEndingHandler struct{ game.GameHandler }

func (h *leaveEndingHandler) OnLeave(id string) (game.Outcome, error) {
	out, err := h.GameHandler.OnLeave(id)
	out.EndReason = "leave_complete"
	return out, err
}
func TestHandlerOnLeaveCanEndMatch(t *testing.T) {
	m, c := realtimeSetup(t, func(cfg *Config) {
		cfg.HandlerFactory = func() game.GameHandler { return &leaveEndingHandler{GameHandler: game.NewMovement()} }
	})
	h := connect(t, m, "host")
	base := create(t, m, h, CreateOptions{})
	g := connect(t, m, "guest")
	if _, err := m.Join(context.Background(), g, JoinOptions{RoomNumber: base.RoomNumber}); err != nil {
		t.Fatal(err)
	}
	v := startMatch(t, m, h)
	awaitGameEvent(t, g, "game.started")
	if err := m.Leave(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	e := awaitGameEvent(t, g, "game.ended")
	if e.Ended == nil || e.Ended.Reason != "leave_complete" || e.Ended.MatchID != *v.MatchID || e.Snapshot.HostID != g.ID() || e.Snapshot.Phase != "WAITING" || e.Snapshot.MatchID != nil {
		t.Fatal("leave outcome did not end remaining match")
	}
	if c.tickStops.Load() != 1 || c.snapshotStops.Load() != 1 {
		t.Fatal("OnLeave end left tickers running")
	}
}
func TestCloseClearsMembershipAndInputCredits(t *testing.T) {
	m, c := realtimeSetup(t, nil)
	h := connect(t, m, "host")
	create(t, m, h, CreateOptions{})
	v := startMatch(t, m, h)
	submit(t, m, h, "pending", move(v, 1, 1, 0))
	h.mu.Lock()
	r := h.room
	h.mu.Unlock()
	if err := m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	id, token := h.Membership()
	if id != "" || token != 0 {
		t.Fatal("closed actor retained membership")
	}
	if len(r.inputSlots) != 0 || len(r.inputs) != 0 || len(r.pending) != 0 {
		t.Fatal("close leaked receipt credit")
	}
	select {
	case <-r.done:
	default:
		t.Fatal("close returned before actor finished")
	}
	code(t, m.SubmitInput(context.Background(), h, "orphan", move(v, 2, 1, 0)), "SERVER_CLOSING")
	if c.tickStops.Load() != 1 || c.snapshotStops.Load() != 1 {
		t.Fatal("close did not stop match tickers")
	}
}

// A failed ACK enqueue must not let OnLeave end a match before healthy peers receive this tick.
type overflowEndingHandler struct {
	game.GameHandler
	leaveCalls atomic.Int32
}

func (h *overflowEndingHandler) Tick(dt float64) (game.Outcome, error) {
	out, err := h.GameHandler.Tick(dt)
	out.Events = append(out.Events, game.Event{Type: "pulse", SessionID: h.GameHandler.Snapshot().Players[0].SessionID})
	return out, err
}
func (h *overflowEndingHandler) OnLeave(id string) (game.Outcome, error) {
	h.leaveCalls.Add(1)
	out, err := h.GameHandler.OnLeave(id)
	out.EndReason = "slow_member_left"
	return out, err
}
func TestTickOverflowPublishesAllAcksAndEventsBeforeLeaveEnd(t *testing.T) {
	handler := &overflowEndingHandler{GameHandler: game.NewMovement()}
	m, c := realtimeSetup(t, func(cfg *Config) { cfg.HandlerFactory = func() game.GameHandler { return handler } })
	h := connect(t, m, "slow-host")
	base := create(t, m, h, CreateOptions{})
	g := connect(t, m, "healthy-guest")
	if _, err := m.Join(context.Background(), g, JoinOptions{RoomNumber: base.RoomNumber}); err != nil {
		t.Fatal(err)
	}
	v := startMatch(t, m, h)
	awaitGameEvent(t, g, "game.started")
	// The actor/list barrier precedes filling A's real reliable FIFO, so no metadata remains in flight.
	if _, err := listForTest(t, m, context.Background()); err != nil {
		t.Fatal(err)
	}
	for len(h.events) < cap(h.events) {
		h.events <- Event{Op: "test.backlog"}
	}
	submit(t, m, h, "host-input", move(v, 1, 1, 0))
	submit(t, m, g, "guest-input", move(v, 1, 0, 1))
	c.ticks <- time.Now()
	want := []string{"game.action", "game.event", "game.ended"}
	received := make([]Event, 0, 3)
	for _, op := range want {
		select {
		case e := <-g.Events():
			if e.Op != op {
				t.Fatalf("tick publication order: got %s, want %s", e.Op, op)
			}
			received = append(received, e)
		case <-time.After(2 * time.Second):
			t.Fatalf("missing %s after overflow", op)
		}
	}
	ack, pulse, end := received[0], received[1], received[2]
	if ack.Err != nil || ack.Ack == nil || ack.Ack.ServerTick != 1 || ack.Ack.InputSeq != 1 || ack.Ack.MatchID != *v.MatchID || ack.RequestID != "guest-input" {
		t.Fatalf("wrong healthy ACK: %+v", ack)
	}
	if pulse.GameEvent == nil || pulse.GameEvent.Type != "pulse" || pulse.GameEvent.SessionID != h.ID() || pulse.GameEvent.MatchID != *v.MatchID || pulse.GameEvent.ServerTick != 1 || pulse.GameEvent.EventID == "" {
		t.Fatalf("lost tick pulse: %+v", pulse)
	}
	if end.Ended == nil || end.Ended.Reason != "slow_member_left" || end.Ended.ServerTick != 1 || end.Ended.MatchID != *v.MatchID || end.Snapshot.Phase != "WAITING" || end.Snapshot.HostID != g.ID() || len(end.Snapshot.Members) != 1 || end.Snapshot.LastProcessedInputSeq[g.ID()] != 1 {
		t.Fatalf("wrong overflow end: %+v", end)
	}
	await(t, h.Done())
	if handler.leaveCalls.Load() != 1 {
		t.Fatal("slow member was removed more than once")
	}
	if c.tickStops.Load() != 1 || c.snapshotStops.Load() != 1 {
		t.Fatal("overflow leave end did not stop tickers exactly once")
	}
	// The list barrier verifies returned credits and completed cleanup, without actor-private scheduling hooks.
	if _, err := listForTest(t, m, context.Background()); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	r := g.room
	g.mu.Unlock()
	if len(r.inputSlots) != 0 {
		t.Fatal("overflow completion leaked receipt credit")
	}
}

type leaveOutcomeHandler struct {
	game.GameHandler
	original string
	reason   string
	calls    atomic.Int32
}

func (h *leaveOutcomeHandler) OnLeave(id string) (game.Outcome, error) {
	h.calls.Add(1)
	out, err := h.GameHandler.OnLeave(id)
	if id == h.original {
		out.Events = []game.Event{{Type: "leave-first", SessionID: id}, {Type: "leave-second", SessionID: id}}
		out.EndReason = h.reason
	} else {
		out.EndReason = "slow-left"
	}
	return out, err
}
func TestLeaveOverflowPreservesOutcomeEvents(t *testing.T) {
	for _, reason := range []string{"", "original-left"} {
		t.Run(reason, func(t *testing.T) {
			handler := &leaveOutcomeHandler{GameHandler: game.NewMovement(), reason: reason}
			m, clock := realtimeSetup(t, func(c *Config) { c.HandlerFactory = func() game.GameHandler { return handler } })
			a := connect(t, m, "leaving")
			base := create(t, m, a, CreateOptions{})
			b, c := connect(t, m, "slow"), connect(t, m, "healthy")
			for _, s := range []*Session{b, c} {
				if _, err := m.Join(context.Background(), s, JoinOptions{RoomNumber: base.RoomNumber}); err != nil {
					t.Fatal(err)
				}
			}
			handler.original = a.ID()
			startMatch(t, m, a)
			awaitGameEvent(t, b, "game.started")
			awaitGameEvent(t, c, "game.started")
			if _, err := listForTest(t, m, context.Background()); err != nil {
				t.Fatal(err)
			}
			for len(b.events) < cap(b.events) {
				b.events <- Event{Op: "backlog"}
			}
			if err := m.Leave(context.Background(), a); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"leave-first", "leave-second"} {
				select {
				case got := <-c.Events():
					if got.Op != "game.event" || got.GameEvent == nil || got.GameEvent.Type != want {
						t.Fatalf("OnLeave publication interrupted: got %s, wanted game.event %s", got.Op, want)
					}
				case <-time.After(time.Second):
					t.Fatalf("missing %s", want)
				}
			}

			ended := awaitGameEvent(t, c, "game.ended")
			wantReason, wantCalls := "slow-left", int32(2)
			if reason != "" {
				wantReason, wantCalls = reason, 1
			}
			if ended.Ended == nil || ended.Ended.Reason != wantReason {
				t.Fatalf("original reason lost: %+v", ended.Ended)
			}
			rooms, err := listForTest(t, m, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(rooms) != 1 || len(rooms[0].Members) != 1 || rooms[0].HostID != c.ID() {
				t.Fatal("overflow cleanup incomplete")
			}
			if handler.calls.Load() != wantCalls {
				t.Fatalf("unexpected reentrant/duplicate hooks: %d", handler.calls.Load())
			}
			if clock.tickStops.Load() != 1 || clock.snapshotStops.Load() != 1 {
				t.Fatal("tickers stopped more than once")
			}
			await(t, b.Done())
		})
	}
}

// Cleanup of two slow peers must not reenter a leave outcome or invoke a hook twice.
type nestedLeaveHandler struct {
	game.GameHandler
	names map[string]string
	calls map[string]int
}

func (h *nestedLeaveHandler) OnLeave(id string) (game.Outcome, error) {
	h.calls[id]++
	out, err := h.GameHandler.OnLeave(id)
	name := h.names[id]
	if name == "last-slow" {
		out.EndReason = "nested-left"
	} else {
		out.Events = []game.Event{{Type: name + "-first"}, {Type: name + "-second"}}
	}
	return out, err
}
func TestNestedLeaveOverflowPreservesPublication(t *testing.T) {
	h := &nestedLeaveHandler{GameHandler: game.NewMovement(), names: map[string]string{}, calls: map[string]int{}}
	m, clock := realtimeSetup(t, func(c *Config) { c.HandlerFactory = func() game.GameHandler { return h } })
	a := connect(t, m, "leaving")
	base := create(t, m, a, CreateOptions{})
	b, c, d := connect(t, m, "slow-b"), connect(t, m, "slow-c"), connect(t, m, "healthy")
	for _, s := range []*Session{b, c, d} {
		if _, err := m.Join(context.Background(), s, JoinOptions{RoomNumber: base.RoomNumber}); err != nil {
			t.Fatal(err)
		}
	}
	h.names[a.ID()], h.names[b.ID()], h.names[c.ID()] = "original", "nested", "last-slow"
	started := startMatch(t, m, a)
	for _, s := range []*Session{b, c, d} {
		awaitGameEvent(t, s, "game.started")
	}
	if _, err := listForTest(t, m, context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Session{b, c} {
		for len(s.events) < cap(s.events) {
			s.events <- Event{Op: "backlog"}
		}
	}
	if err := m.Leave(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"original-first", "original-second", "nested-first", "nested-second"} {
		select {
		case e := <-d.Events():
			if e.Op != "game.event" || e.GameEvent == nil || e.GameEvent.Type != want || e.GameEvent.MatchID != *started.MatchID {
				t.Fatalf("nested publication: got %+v want %s", e, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("missing %s", want)
		}
	}
	ended := awaitGameEvent(t, d, "game.ended")
	if ended.Ended == nil || ended.Ended.Reason != "nested-left" {
		t.Fatal("nested end reason lost")
	}
	rooms, err := listForTest(t, m, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rooms) != 1 || len(rooms[0].Members) != 1 || rooms[0].HostID != d.ID() {
		t.Fatal("nested cleanup incomplete")
	}
	for _, s := range []*Session{a, b, c} {
		if h.calls[s.ID()] != 1 {
			t.Fatal("duplicate leave hook")
		}
	}
	if clock.tickStops.Load() != 1 || clock.snapshotStops.Load() != 1 {
		t.Fatal("duplicate ticker stop")
	}
}

// A deferred cancellation overflow still closes the departed session and returns its input credit.
func TestLeaveCancellationOverflowClosesDepartedSession(t *testing.T) {
	m, _ := realtimeSetup(t, nil)
	a := connect(t, m, "leaving")
	base := create(t, m, a, CreateOptions{})
	b := connect(t, m, "healthy")
	if _, err := m.Join(context.Background(), b, JoinOptions{RoomNumber: base.RoomNumber}); err != nil {
		t.Fatal(err)
	}
	started := startMatch(t, m, a)
	awaitGameEvent(t, b, "game.started")
	if _, err := listForTest(t, m, context.Background()); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	r := a.room
	a.mu.Unlock()
	submit(t, m, a, "pending", move(started, 1, 1, 0))
	for len(a.events) < cap(a.events) {
		a.events <- Event{Op: "backlog"}
	}
	if err := m.Leave(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.Done():
	default:
		t.Fatal("overflowed departed session remained open")
	}
	if len(r.inputSlots) != 0 {
		t.Fatal("cancellation leaked credit")
	}
}
