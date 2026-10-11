package room

import (
	"context"
	"crypto/rand"
	"roomserver/internal/game"
	"time"
)

type result struct {
	snap Snapshot
	err  error
}
type command struct {
	s      *Session
	g      uint64
	list   bool
	start  bool
	ctx    context.Context
	result chan result
}
type memberEntry struct {
	session *Session
	token   uint64
}
type cleanup struct {
	s      *Session
	roomID string
	token  uint64
	done   chan struct{}
}
type actor struct {
	manager                *Manager
	id, number, visibility string
	capacity               int
	published              bool
	password               *credential
	members                []memberEntry
	revision, snapshotSeq  uint64
	commands               chan command
	cleanups               chan cleanup
	done                   chan struct{}
	handler                game.GameHandler
	state                  *game.State
	matchID                string
	serverTick             uint64
	ticks, snapshots       <-chan time.Time
	stopTick, stopSnapshot func()
	inputs                 chan receipt
	inputSlots             chan struct{}
	pending                []receipt
	publicationDepth       int
	flushingSlow           bool
	slowMembers            []memberEntry
}

func newActor(m *Manager, n string, o CreateOptions, p *credential) *actor {
	return &actor{manager: m, id: rand.Text(), number: n, visibility: o.Visibility, capacity: o.Capacity, password: p, commands: make(chan command, m.cfg.ActorQueue), cleanups: make(chan cleanup, m.cfg.CleanupQueue), done: make(chan struct{}), inputs: make(chan receipt, m.cfg.InputQueue), inputSlots: make(chan struct{}, m.cfg.InputQueue)}
}

// capture is actor-owned; handler state is cached outside the admission gate.
func (r *actor) snapshot() Snapshot {
	r.snapshotSeq++
	v := Snapshot{RoomID: r.id, RoomNumber: r.number, Visibility: r.visibility, Capacity: r.capacity, Phase: "WAITING", Members: []Member{}, Revision: r.revision, SnapshotSeq: r.snapshotSeq, LastProcessedInputSeq: map[string]uint64{}}
	if r.matchID != "" {
		id := r.matchID
		v.MatchID = &id
		v.Phase = "PLAYING"
		v.ServerTick = r.serverTick
		state := r.state.Clone()
		v.GameState = &state
	}
	for _, entry := range r.members {
		s := entry.session
		v.Members = append(v.Members, Member{s.id})
		s.mu.Lock()
		v.LastProcessedInputSeq[s.id] = s.lastProcessedInputSeq
		s.mu.Unlock()
	}
	if len(r.members) > 0 {
		v.HostID = r.members[0].session.id
	}
	return v
}
func (r *actor) cleanup(s *Session, token uint64) {
	c := cleanup{s: s, roomID: r.id, token: token, done: make(chan struct{})}
	select {
	case r.cleanups <- c:
	case <-r.done:
		return
	}
	select {
	case <-c.done:
	case <-r.done:
	}
}

// Cleanup is bound to committed membership, not reservation generation.
func (r *actor) remove(s *Session, roomID string, token uint64) bool {
	m := r.manager
	m.mu.Lock()
	s.mu.Lock()
	if roomID != r.id || token == 0 || s.room != r || s.membershipToken != token {
		s.mu.Unlock()
		m.mu.Unlock()
		return false
	}
	removed := false
	for i, entry := range r.members {
		if entry.session == s && entry.token == token {
			r.members = append(r.members[:i], r.members[i+1:]...)
			r.revision++
			s.room = nil
			s.membershipToken = 0
			s.latest = nil
			removed = true
			break
		}
	}
	s.mu.Unlock()
	m.mu.Unlock()
	if !removed {
		return false
	}
	// Keep canceled receipts, all leave events, and the explicit end in one publication scope.
	r.publicationDepth++
	defer r.finishPublication()
	r.cancelInputs(func(in receipt) bool { return in.s == s && in.token == token })
	if r.handler != nil {
		out, err := r.handler.OnLeave(s.id)
		if err != nil {
			r.endMatch("GAME_ERROR")
		} else {
			state := r.handler.Snapshot().Clone()
			r.state = &state
			r.gameEvents(out.Events)
			if out.EndReason != "" {
				r.endMatch(out.EndReason)
			}
		}
	}
	return true
}
func (r *actor) closeSlow(entry memberEntry) {
	if r.publicationDepth > 0 {
		// Nested tick/leave publication defers membership hooks until the outer scope finishes.
		for _, queued := range r.slowMembers {
			if queued == entry {
				return
			}
		}
		r.slowMembers = append(r.slowMembers, entry)
		return
	}

	s := entry.session
	m := r.manager
	m.mu.Lock()
	s.mu.Lock()
	if s.room != nil && (s.room != r || s.membershipToken != entry.token) {
		s.mu.Unlock()
		m.mu.Unlock()
		return
	}
	if !s.closed {
		s.closed = true
		s.generation++
		s.joining = false
		close(s.done)
		delete(m.sessions, s.id)
	}
	s.mu.Unlock()
	m.mu.Unlock()
	r.remove(s, r.id, entry.token)
}
func (r *actor) finishPublication() {
	r.publicationDepth--
	if r.publicationDepth != 0 || r.flushingSlow {
		return
	}
	r.flushingSlow = true
	changed := false
	// Removal can publish another outcome and queue more slow members. Drain iteratively.
	for len(r.slowMembers) > 0 {
		slow := r.slowMembers
		r.slowMembers = nil
		changed = true
		for _, entry := range slow {
			r.closeSlow(entry)
		}
	}
	r.flushingSlow = false
	if changed && len(r.members) > 0 {
		r.broadcast()
	}
}

func (r *actor) send(s *Session, e Event) bool {
	e.Snapshot = e.Snapshot.clone()
	if e.Ack != nil {
		v := *e.Ack
		e.Ack = &v
	}
	if e.Ended != nil {
		v := *e.Ended
		e.Ended = &v
	}
	if e.GameEvent != nil {
		v := *e.GameEvent
		e.GameEvent = &v
	}
	select {
	case s.events <- e:
		return true
	default:
		return false
	}
}
func (r *actor) broadcastEvent(e Event) {
	var slow []memberEntry
	for _, entry := range r.members {
		e.MembershipToken = entry.token
		if !r.send(entry.session, e) {
			slow = append(slow, entry)
		}
	}
	for _, entry := range slow {
		r.closeSlow(entry)
	}
	if len(slow) > 0 && len(r.members) > 0 && r.publicationDepth == 0 && !r.flushingSlow {
		r.broadcast()
	}
}
func (r *actor) broadcast() {
	// Overflow cleanup runs directly on this actor; it must not wait on its own queue.
	for {
		v := r.snapshot()
		var slow []memberEntry
		for _, entry := range r.members {
			if !r.send(entry.session, Event{Op: "room.state", Snapshot: v, MembershipToken: entry.token}) {
				slow = append(slow, entry)
			}
		}
		if len(slow) == 0 {
			return
		}
		for _, entry := range slow {
			r.closeSlow(entry)
		}
		if len(r.members) == 0 || r.publicationDepth > 0 {
			return
		}
	}
}
func (r *actor) admit(c command) result {
	m := r.manager
	m.mu.Lock()
	s := c.s
	s.mu.Lock()
	var err error
	switch {
	case m.closing:
		err = failure("SERVER_CLOSING")
	case m.rooms[r.number] != r:
		err = failure("ROOM_CLOSED")
	case s.closed || s.manager != m || m.sessions[s.id] != s:
		err = failure("SESSION_CLOSED")
	case s.generation != c.g || !s.joining || s.room != nil:
		err = failure("ALREADY_IN_ROOM")
	case r.matchID != "":
		err = failure("ROOM_IN_PROGRESS")
	case len(r.members) >= r.capacity:
		err = failure("ROOM_FULL")
	}
	if err == nil {
		r.published = true
		r.members = append(r.members, memberEntry{session: s, token: c.g})
		s.room = r
		s.membershipToken = c.g
		s.joining = false
		r.revision++
	}
	s.mu.Unlock()
	m.mu.Unlock()
	if err != nil {
		return result{err: err}
	}
	return result{snap: r.snapshot()}
}
func (r *actor) handleCleanup(c cleanup) {
	if r.remove(c.s, c.roomID, c.token) {
		if len(r.members) > 0 {
			r.broadcast()
		}
	}
	close(c.done)
}
func (r *actor) handleCommand(c command) {
	if c.list {
		c.result <- result{snap: r.snapshot()}
		return
	}
	if c.start {
		r.start(c)
		return
	}
	x := r.admit(c)
	if x.err == nil && r.manager.cfg.afterCommit != nil {
		r.manager.cfg.afterCommit()
	}
	c.result <- x
	if x.err == nil {
		r.broadcast()
	}
}
func (r *actor) run() {
	defer r.manager.wg.Done()
	defer func() {
		// Registry removal and input admission share the gate, preventing orphan enqueue.
		m := r.manager
		m.mu.Lock()
		if m.rooms[r.number] == r {
			delete(m.rooms, r.number)
		}
		m.mu.Unlock()
		r.stopTickers()
		r.cancelInputs(func(receipt) bool { return true })
		for len(r.members) > 0 {
			entry := r.members[0]
			r.remove(entry.session, r.id, entry.token)
		}
		close(r.done)
	}()
	for {
		// Cleanup and already-ready simulation ticks precede ordinary commands.
		select {
		case c := <-r.cleanups:
			r.handleCleanup(c)
		default:
			select {
			case <-r.ticks:
				r.tick()
			default:
				select {
				case c := <-r.cleanups:
					r.handleCleanup(c)
				case <-r.ticks:
					r.tick()
				case <-r.snapshots:
					r.publishSnapshot()
				case in := <-r.inputs:
					r.pending = append(r.pending, in)
				case c := <-r.commands:
					r.handleCommand(c)
				case <-r.manager.stop:
					return
				}
			}
		}
		if len(r.members) == 0 {
			return
		}
	}
}
