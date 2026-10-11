package room

import "sync"

type Session struct {
	manager    *Manager
	id, peer   string
	mu         sync.Mutex
	generation uint64
	// Membership identity survives reservation invalidation until its actor removes it.
	membershipToken uint64
	joining, closed bool
	room            *actor
	done            chan struct{}
	events          chan Snapshot
}

func (s *Session) ID() string              { return s.id }
func (s *Session) Done() <-chan struct{}   { return s.done }
func (s *Session) Events() <-chan Snapshot { return s.events }
