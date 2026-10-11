package room

import "crypto/rand"

type result struct {
	snap Snapshot
	err  error
}
type command struct {
	s      *Session
	g      uint64
	list   bool
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
	revision               uint64
	commands               chan command
	cleanups               chan cleanup
	done                   chan struct{}
}

func newActor(m *Manager, n string, o CreateOptions, p *credential) *actor {
	return &actor{manager: m, id: rand.Text(), number: n, visibility: o.Visibility, capacity: o.Capacity, password: p, commands: make(chan command, m.cfg.ActorQueue), cleanups: make(chan cleanup, m.cfg.CleanupQueue), done: make(chan struct{})}
}
func (r *actor) snapshot() Snapshot {
	v := Snapshot{RoomID: r.id, RoomNumber: r.number, Visibility: r.visibility, Capacity: r.capacity, Phase: "WAITING", Members: []Member{}, Revision: r.revision, LastProcessedInputSeq: map[string]uint64{}}
	for _, entry := range r.members {
		v.Members = append(v.Members, Member{entry.session.id})
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

// Cleanup is bound to the committed membership, not the current reservation generation.
func (r *actor) remove(s *Session, roomID string, token uint64) bool {
	m := r.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if roomID != r.id || token == 0 || s.room != r || s.membershipToken != token {
		return false
	}
	for i, entry := range r.members {
		if entry.session == s && entry.token == token {
			r.members = append(r.members[:i], r.members[i+1:]...)
			r.revision++
			s.room = nil
			s.membershipToken = 0
			return true
		}
	}
	return false
}
func (r *actor) broadcast() {
	for {
		v := r.snapshot()
		var slow []memberEntry
		for _, entry := range r.members {
			s := entry.session
			select {
			case s.events <- v.clone():
			default:
				slow = append(slow, entry)
			}
		}
		if len(slow) == 0 {
			return
		}
		for _, entry := range slow {
			s := entry.session
			m := r.manager
			m.mu.Lock()
			s.mu.Lock()
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
	}
}
func (r *actor) admit(c command) result {
	m := r.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	s := c.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.closing {
		return result{err: failure("SERVER_CLOSING")}
	}
	if m.rooms[r.number] != r {
		return result{err: failure("ROOM_CLOSED")}
	}
	if s.closed || s.manager != m || m.sessions[s.id] != s {
		return result{err: failure("SESSION_CLOSED")}
	}
	if s.generation != c.g || !s.joining || s.room != nil {
		return result{err: failure("ALREADY_IN_ROOM")}
	}
	if len(r.members) >= r.capacity {
		return result{err: failure("ROOM_FULL")}
	}
	r.published = true
	r.members = append(r.members, memberEntry{session: s, token: c.g})
	s.room = r
	s.membershipToken = c.g
	s.joining = false
	r.revision++
	return result{snap: r.snapshot()}
}
func (r *actor) run() {
	defer r.manager.wg.Done()
	defer close(r.done)
	for {
		var c cleanup
		select {
		case c = <-r.cleanups:
			if r.remove(c.s, c.roomID, c.token) {
				r.broadcast()
			}
			close(c.done)
		default:
			select {
			case c = <-r.cleanups:
				if r.remove(c.s, c.roomID, c.token) {
					r.broadcast()
				}
				close(c.done)
			case v := <-r.commands:
				if v.list {
					v.result <- result{snap: r.snapshot()}
				} else {
					x := r.admit(v)
					if x.err == nil && r.manager.cfg.afterCommit != nil {
						r.manager.cfg.afterCommit()
					}
					v.result <- x
					if x.err == nil {
						r.broadcast()
					}
				}
			}
		}
		if len(r.members) == 0 {
			m := r.manager
			m.mu.Lock()
			if m.rooms[r.number] == r {
				delete(m.rooms, r.number)
			}
			m.mu.Unlock()
			return
		}
	}
}
