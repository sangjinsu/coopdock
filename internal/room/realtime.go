package room

import (
	"context"
	"crypto/rand"
	"github.com/sangjinsu/coopdock/internal/game"
	"time"
)

type receipt struct {
	s         *Session
	roomID    string
	token     uint64
	requestID string
	deadline  time.Time
	in        Input
}

func (m *Manager) Start(ctx context.Context, s *Session) (Snapshot, error) {
	ctx, cancel := m.request(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return Snapshot{}, failure("SERVER_CLOSING")
	}
	if s == nil || s.manager != m {
		m.mu.Unlock()
		return Snapshot{}, failure("SESSION_CLOSED")
	}
	s.mu.Lock()
	if s.closed || m.sessions[s.id] != s {
		s.mu.Unlock()
		m.mu.Unlock()
		return Snapshot{}, failure("SESSION_CLOSED")
	}
	r, token := s.room, s.membershipToken
	s.mu.Unlock()
	if r == nil || r.gameID != s.gameID || m.rooms[roomKey{r.gameID, r.number}] != r {
		m.mu.Unlock()
		return Snapshot{}, failure("NOT_IN_ROOM")
	}
	res := make(chan result, 1)
	select {
	case r.commands <- command{s: s, g: token, start: true, ctx: ctx, result: res}:
	default:
		m.mu.Unlock()
		return Snapshot{}, failure("SERVER_BUSY")
	}
	m.mu.Unlock()
	select {
	case v := <-res:
		return v.snap, v.err
	case <-ctx.Done():
		// A committed result is published under this same gate before cancellation can win.
		m.mu.Lock()
		defer m.mu.Unlock()
		select {
		case v := <-res:
			return v.snap, v.err
		default:
			return Snapshot{}, ctx.Err()
		}
	case <-r.done:
		select {
		case v := <-res:
			return v.snap, v.err
		default:
			return Snapshot{}, failure("ROOM_CLOSED")
		}
	}
}
func (m *Manager) SubmitInput(ctx context.Context, s *Session, requestID string, in Input) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if in.InputSeq == 0 || in.InputSeq > MaxInputSeq {
		return failure("INVALID_INPUT_SEQ")
	}
	// Typed core callers receive the same direction validation as wire callers.
	if err := game.NewMovement().Validate(in.Action); err != nil {
		return failure("INVALID_MESSAGE")
	}
	now := time.Now()
	deadline := now.Add(m.cfg.RequestTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return failure("SERVER_CLOSING")
	}
	if s == nil || s.manager != m {
		return failure("SESSION_CLOSED")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || m.sessions[s.id] != s {
		return failure("SESSION_CLOSED")
	}
	r := s.room
	if r == nil || r.gameID != s.gameID || m.rooms[roomKey{r.gameID, r.number}] != r {
		return failure("NOT_IN_ROOM")
	}
	if r.matchID == "" {
		return failure("GAME_NOT_STARTED")
	}
	if in.MatchID != r.matchID {
		return failure("STALE_MATCH")
	}
	if in.InputSeq <= s.lastProcessedInputSeq {
		return failure("INVALID_INPUT_SEQ")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case r.inputSlots <- struct{}{}:
	default:
		return failure("SERVER_BUSY")
	}
	inReceipt := receipt{s: s, roomID: r.id, token: s.membershipToken, requestID: requestID, deadline: deadline, in: in}
	// Both operations are nonblocking while registry identity is protected by the gate.
	select {
	case r.inputs <- inReceipt:
		return nil
	default:
		<-r.inputSlots
		return failure("SERVER_BUSY")
	}
}
func (r *actor) startError(c command) error {
	m := r.manager
	s := c.s
	switch {
	case m.closing:
		return failure("SERVER_CLOSING")
	case m.rooms[roomKey{r.gameID, r.number}] != r:
		return failure("ROOM_CLOSED")
	case s.closed || m.sessions[s.id] != s:
		return failure("SESSION_CLOSED")
	case s.manager != m || s.gameID != r.gameID || s.room != r || s.membershipToken != c.g:
		return failure("NOT_IN_ROOM")
	case len(r.members) == 0 || r.members[0].session != s:
		return failure("NOT_HOST")
	case r.matchID != "":
		return failure("ROOM_IN_PROGRESS")
	case c.ctx.Err() != nil:
		return c.ctx.Err()
	}
	return nil
}
func (r *actor) start(c command) {
	m := r.manager
	m.mu.Lock()
	c.s.mu.Lock()
	err := r.startError(c)
	c.s.mu.Unlock()
	m.mu.Unlock()
	if err != nil {
		c.result <- result{err: err}
		return
	}
	// Handler callbacks never run under manager or session locks.
	handler := m.cfg.HandlerFactory()
	if handler == nil {
		c.result <- result{err: failure("GAME_ERROR")}
		return
	}
	ids := make([]string, len(r.members))
	for i, e := range r.members {
		ids[i] = e.session.id
	}
	if err := handler.Start(ids); err != nil {
		c.result <- result{err: failure("GAME_ERROR")}
		return
	}
	state := handler.Snapshot().Clone()
	m.mu.Lock()
	c.s.mu.Lock()
	err = r.startError(c)
	c.s.mu.Unlock()
	if err != nil {
		m.mu.Unlock()
		c.result <- result{err: err}
		return
	}
	r.handler = handler
	r.state = &state
	r.matchID = rand.Text()
	r.serverTick = 0
	r.revision++
	v := r.snapshot()
	c.result <- result{snap: v.clone()}
	m.mu.Unlock()
	r.publishOperations(m.cfg.operationsNow())
	r.ticks, r.stopTick = m.cfg.newTicker(time.Second / time.Duration(m.cfg.TickHz))
	r.snapshots, r.stopSnapshot = m.cfg.newTicker(time.Second / time.Duration(m.cfg.SnapshotHz))
	r.broadcastEvent(Event{Op: "game.started", Snapshot: v})
}
func (r *actor) drainInputs() {
	n := len(r.inputs)
	for i := 0; i < n; i++ {
		r.pending = append(r.pending, <-r.inputs)
	}
}
func (r *actor) validateReceipt(in receipt, high map[*Session]uint64) error {
	s := in.s
	m := r.manager
	m.mu.Lock()
	s.mu.Lock()
	defer s.mu.Unlock()
	defer m.mu.Unlock()
	if s.closed || s.manager != m || s.gameID != r.gameID || m.rooms[roomKey{r.gameID, r.number}] != r || m.sessions[s.id] != s || s.room != r || s.membershipToken != in.token || in.roomID != r.id {
		return failure("INPUT_CANCELED")
	}
	if r.matchID == "" {
		return failure("INPUT_CANCELED")
	}
	if in.in.MatchID != r.matchID {
		return failure("STALE_MATCH")
	}
	seq := s.lastProcessedInputSeq
	if high[s] > seq {
		seq = high[s]
	}
	if in.in.InputSeq == 0 || in.in.InputSeq > MaxInputSeq || in.in.InputSeq <= seq {
		return failure("INVALID_INPUT_SEQ")
	}
	if !time.Now().Before(in.deadline) {
		return failure("REQUEST_TIMEOUT")
	}
	return nil
}
func (r *actor) complete(in receipt, ack *ActionAck, err error) {
	<-r.inputSlots
	e := Event{Op: "game.action", MembershipToken: in.token, RequestID: in.requestID, Ack: ack, Err: err}
	if !r.send(in.s, e) {
		r.closeSlow(memberEntry{session: in.s, token: in.token})
	}
}
func (r *actor) cancelInputs(match func(receipt) bool) {
	r.drainInputs()
	pending := r.pending
	r.pending = nil
	var canceled []receipt
	for _, in := range pending {
		if match(in) {
			canceled = append(canceled, in)
		} else {
			r.pending = append(r.pending, in)
		}
	}
	for _, in := range canceled {
		r.complete(in, nil, failure("INPUT_CANCELED"))
	}
}
func (r *actor) tick() {
	if r.handler == nil {
		return
	}
	// Defer overflow membership hooks until this tick's complete publication boundary.
	r.publicationDepth++
	defer r.finishPublication()
	r.drainInputs()
	n := min(len(r.pending), r.manager.cfg.InputsPerTick)
	batch := append([]receipt(nil), r.pending[:n]...)
	r.pending = append([]receipt(nil), r.pending[n:]...)
	high := map[*Session]uint64{}
	type completion struct {
		in  receipt
		err error
	}
	var completed []completion
	var applied []receipt
	for _, in := range batch {
		err := r.validateReceipt(in, high)
		if err == nil && r.handler.Validate(in.in.Action) != nil {
			err = failure("INVALID_MESSAGE")
		}
		if err == nil && r.handler.Apply(in.s.id, in.in.Action) != nil {
			err = failure("GAME_ERROR")
		}
		if err == nil {
			high[in.s] = in.in.InputSeq
			applied = append(applied, in)
		}
		completed = append(completed, completion{in: in, err: err})
	}
	out, err := r.handler.Tick(1 / float64(r.manager.cfg.TickHz))
	if err != nil {
		for _, c := range completed {
			if c.err == nil {
				c.err = failure("GAME_ERROR")
			}
			r.complete(c.in, nil, c.err)
		}
		r.endMatch("GAME_ERROR")
		return
	}
	r.serverTick++
	// Tick success commits sequence numbers together before releasing completions.
	for _, in := range applied {
		in.s.mu.Lock()
		in.s.lastProcessedInputSeq = in.in.InputSeq
		in.s.mu.Unlock()
	}
	if out.Changed || len(applied) > 0 {
		r.revision++
	}
	state := r.handler.Snapshot().Clone()
	r.state = &state
	match, tick := r.matchID, r.serverTick
	for _, c := range completed {
		var ack *ActionAck
		if c.err == nil {
			ack = &ActionAck{MatchID: match, InputSeq: c.in.in.InputSeq, ServerTick: tick}
		}
		r.complete(c.in, ack, c.err)
	}
	if r.handler == nil {
		return
	}
	r.gameEvents(out.Events)
	if out.EndReason != "" {
		r.endMatch(out.EndReason)
	}
}
func (r *actor) gameEvents(events []game.Event) {
	for _, e := range events {
		if r.matchID == "" {
			return
		}
		v := &GameEvent{EventID: rand.Text(), MatchID: r.matchID, ServerTick: r.serverTick, Type: e.Type, SessionID: e.SessionID}
		r.broadcastEvent(Event{Op: "game.event", GameEvent: v})
	}
}
func (r *actor) stopTickers() {
	if r.stopTick != nil {
		r.stopTick()
		r.stopTick = nil
	}
	if r.stopSnapshot != nil {
		r.stopSnapshot()
		r.stopSnapshot = nil
	}
	r.ticks = nil
	r.snapshots = nil
}
func (r *actor) endMatch(reason string) {
	if r.handler == nil {
		return
	}
	ended := &Ended{MatchID: r.matchID, Reason: reason, ServerTick: r.serverTick}
	r.stopTickers()
	m := r.manager
	m.mu.Lock()
	r.handler = nil
	r.state = nil
	r.matchID = ""
	r.serverTick = 0
	r.revision++
	m.mu.Unlock()
	r.cancelInputs(func(receipt) bool { return true })
	v := r.snapshot()
	r.broadcastEvent(Event{Op: "game.ended", Snapshot: v, Ended: ended})
	r.publishOperations(m.cfg.operationsNow())
}
func (r *actor) publishSnapshot() {
	if r.handler == nil {
		return
	}
	v := r.snapshot()
	for _, entry := range r.members {
		s := entry.session
		s.mu.Lock()
		if !s.closed && s.room == r && s.membershipToken == entry.token {
			snap := v.clone()
			s.latest = &snap
			select {
			case s.snapshotReady <- struct{}{}:
			default:
			}
		}
		s.mu.Unlock()
	}
}
