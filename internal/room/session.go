package room

import "sync"

type Session struct {
	manager    *Manager
	id, peer   string
	gameID     string
	mu         sync.Mutex
	generation uint64
	// Membership identity survives reservation invalidation until its actor removes it.
	membershipToken       uint64
	joining, closed       bool
	room                  *actor
	done                  chan struct{}
	events                chan Event
	lastProcessedInputSeq uint64
	latest                *Snapshot
	snapshotReady         chan struct{}
}

func (s *Session) GameID() string        { return s.gameID }
func (s *Session) ID() string            { return s.id }
func (s *Session) Done() <-chan struct{} { return s.done }
func (s *Session) Events() <-chan Event  { return s.events }

func (s *Session) Membership() (string, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.room == nil {
		return "", 0
	}
	return s.room.id, s.membershipToken
}
func (s *Session) SnapshotReady() <-chan struct{} { return s.snapshotReady }
func (s *Session) TakeSnapshot() (Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.latest == nil {
		return Snapshot{}, false
	}
	v := s.latest.clone()
	s.latest = nil
	return v, true
}
