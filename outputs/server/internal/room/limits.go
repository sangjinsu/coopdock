package room

import (
	"sync"
	"time"
)

type failureEntry struct {
	count   int
	expires time.Time
}
type failureLimits struct {
	mu             sync.Mutex
	cfg            Config
	entries        map[string]failureEntry
	saturatedUntil time.Time
}

func newFailureLimits(c Config) *failureLimits {
	return &failureLimits{cfg: c, entries: map[string]failureEntry{}}
}
func (l *failureLimits) now() time.Time {
	if l.cfg.now != nil {
		return l.cfg.now()
	}
	return time.Now()
}
func (l *failureLimits) prune(now time.Time) {
	for k, v := range l.entries {
		if !now.Before(v.expires) {
			delete(l.entries, k)
		}
	}
}
func (l *failureLimits) keys(s *Session, r string) []string {
	return []string{"ip:" + s.peer, "session:" + s.id, "room:" + r}
}
func (l *failureLimits) check(s *Session, r string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.prune(now)
	if now.Before(l.saturatedUntil) {
		return failure("RATE_LIMITED")
	}
	thresholds := []int{l.cfg.IPFailureLimit, l.cfg.SessionFailureLimit, l.cfg.RoomFailureLimit}
	for i, k := range l.keys(s, r) {
		if l.entries[k].count >= thresholds[i] {
			return failure("RATE_LIMITED")
		}
	}
	return nil
}
func (l *failureLimits) record(s *Session, r string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.prune(now)
	for _, k := range l.keys(s, r) {
		v, exists := l.entries[k]
		if !exists {
			if len(l.entries) >= l.cfg.MaxFailureKeys {
				l.saturatedUntil = now.Add(l.cfg.FailureTTL)
				continue
			}
			v.expires = now.Add(l.cfg.FailureTTL)
		}
		v.count++
		l.entries[k] = v
	}
}
