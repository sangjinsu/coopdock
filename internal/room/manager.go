package room

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"github.com/sangjinsu/coopdock/internal/game"
	"sort"
	"sync"
	"time"
)

type roomKey struct{ gameID, number string }

type Manager struct {
	cfg Config
	// The admission gate is always acquired before a session lock.
	mu        sync.Mutex
	closing   bool
	rooms     map[roomKey]*actor
	games     map[string]struct{}
	sessions  map[string]*Session
	wg        sync.WaitGroup
	closeOnce sync.Once
	hashJobs  chan hashJob
	stop      chan struct{}
	limits    *failureLimits
	closed    chan struct{}
}

func NewManager(c Config) (*Manager, error) {
	if c.TickHz < 1 || c.TickHz > 120 || c.SnapshotHz < 1 || c.SnapshotHz > c.TickHz || c.InputQueue < 1 || c.InputQueue > 4096 || c.InputsPerTick < 1 || c.InputsPerTick > c.InputQueue || c.MaxRooms < 1 || c.MaxRooms > 4096 || c.MaxSessions < 1 || c.NumberRetries < 1 || c.ActorQueue < 1 || c.CleanupQueue < 6 || c.EventQueue < 1 || c.ListLimit < 1 || c.HashWorkers < 1 || c.HashQueue < 1 || c.FailureTTL <= 0 || c.IPFailureLimit < 1 || c.SessionFailureLimit < 1 || c.RoomFailureLimit < 1 || c.MaxFailureKeys < 3 || c.RequestTimeout <= 0 || c.ShutdownTimeout <= 0 || c.ArgonThreads < 1 || c.ArgonMemory < 8*uint32(c.ArgonThreads) || c.ArgonTime < 1 || c.SaltBytes < 16 || c.HashBytes < 32 {
		return nil, failure("INVALID_MESSAGE")
	}
	games := make(map[string]struct{}, len(c.GameIDs))
	if len(c.GameIDs) < 1 || len(c.GameIDs) > 32 {
		return nil, failure("INVALID_MESSAGE")
	}
	for _, id := range c.GameIDs {
		if !ValidGameID(id) {
			return nil, failure("INVALID_MESSAGE")
		}
		if _, exists := games[id]; exists {
			return nil, failure("INVALID_MESSAGE")
		}
		games[id] = struct{}{}
	}
	c.GameIDs = append([]string(nil), c.GameIDs...)
	if c.HandlerFactory == nil {
		c.HandlerFactory = game.NewMovement
	}
	if c.newTicker == nil {
		c.newTicker = func(d time.Duration) (<-chan time.Time, func()) { t := time.NewTicker(d); return t.C, t.Stop }
	}
	if c.newOperationsTicker == nil {
		c.newOperationsTicker = func(d time.Duration) (<-chan time.Time, func()) { t := time.NewTicker(d); return t.C, t.Stop }
	}
	if c.operationsNow == nil {
		c.operationsNow = time.Now
	}
	m := &Manager{cfg: c, rooms: map[roomKey]*actor{}, games: games, sessions: map[string]*Session{}, closed: make(chan struct{}), stop: make(chan struct{}), hashJobs: make(chan hashJob, c.HashQueue), limits: newFailureLimits(c)}
	for i := 0; i < c.HashWorkers; i++ {
		m.wg.Add(1)
		go m.hashWorker()
	}
	return m, nil
}
func (m *Manager) Ready() bool                           { m.mu.Lock(); defer m.mu.Unlock(); return !m.closing }
func (m *Manager) Connect(peer string) (*Session, error) { return m.ConnectGame(peer, "default") }
func (m *Manager) ConnectGame(peer, gameID string) (*Session, error) {
	if !m.HasGame(gameID) {
		return nil, failure("INVALID_MESSAGE")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return nil, failure("SERVER_CLOSING")
	}
	if len(m.sessions) >= m.cfg.MaxSessions {
		return nil, failure("SERVER_BUSY")
	}
	s := &Session{manager: m, id: rand.Text(), peer: peer, gameID: gameID, done: make(chan struct{}), events: make(chan Event, m.cfg.EventQueue), snapshotReady: make(chan struct{}, 1)}
	m.sessions[s.id] = s
	return s, nil
}
func (m *Manager) reserve(s *Session) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return 0, failure("SERVER_CLOSING")
	}
	if s == nil || s.manager != m {
		return 0, failure("SESSION_CLOSED")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || m.sessions[s.id] != s {
		return 0, failure("SESSION_CLOSED")
	}
	if s.room != nil || s.joining {
		return 0, failure("ALREADY_IN_ROOM")
	}
	s.generation++
	s.joining = true
	return s.generation, nil
}
func (m *Manager) cancel(s *Session, g uint64) {
	m.mu.Lock()
	s.mu.Lock()
	var r *actor
	var token uint64
	if s.generation == g {
		s.generation++
		s.joining = false
		r = s.room
		token = s.membershipToken
	}
	s.mu.Unlock()
	m.mu.Unlock()
	if r != nil {
		if m.cfg.beforeCancelCleanup != nil {
			m.cfg.beforeCancelCleanup()
		}
		r.cleanup(s, token)
	}
}
func (m *Manager) finish(ctx context.Context, s *Session, g uint64, r *actor) (Snapshot, error) {
	if ctx.Err() != nil {
		m.cancel(s, g)
		return Snapshot{}, ctx.Err()
	}
	res := make(chan result, 1)
	c := command{s: s, g: g, result: res}
	select {
	case r.commands <- c:
	case <-ctx.Done():
		m.cancel(s, g)
		return Snapshot{}, ctx.Err()
	case <-r.done:
		m.cancel(s, g)
		return Snapshot{}, failure("ROOM_CLOSED")
	default:
		m.cancel(s, g)
		return Snapshot{}, failure("SERVER_BUSY")
	}
	select {
	case v := <-res:
		if ctx.Err() != nil {
			m.cancel(s, g)
			return Snapshot{}, ctx.Err()
		}
		if v.err != nil {
			m.cancel(s, g)
		}
		return v.snap, v.err
	case <-ctx.Done():
		m.cancel(s, g)
		return Snapshot{}, ctx.Err()
	case <-r.done:
		select {
		case v := <-res:
			m.cancel(s, g)
			return v.snap, v.err
		default:
			m.cancel(s, g)
			return Snapshot{}, failure("ROOM_CLOSED")
		}
	}
}
func (m *Manager) request(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, m.cfg.RequestTimeout)
}
func (m *Manager) Create(ctx context.Context, s *Session, o CreateOptions) (Snapshot, error) {
	ctx, cancel := m.request(ctx)
	defer cancel()
	if o.Capacity == 0 {
		o.Capacity = 6
	}
	if o.Visibility == "" {
		o.Visibility = "public"
	}
	if o.Capacity < 1 || o.Capacity > 6 || (o.Visibility != "public" && o.Visibility != "private") || (o.Visibility == "public" && o.Password != nil) || (o.Visibility == "private" && !validPassword(o.Password)) {
		return Snapshot{}, failure("INVALID_MESSAGE")
	}
	g, e := m.reserve(s)
	if e != nil {
		return Snapshot{}, e
	}
	var p *credential
	if o.Visibility == "private" {
		p, e = m.passwordContext(ctx, s, *o.Password)
		if e != nil {
			m.cancel(s, g)
			return Snapshot{}, e
		}
	}
	if ctx.Err() != nil {
		m.cancel(s, g)
		return Snapshot{}, ctx.Err()
	}
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		m.cancel(s, g)
		return Snapshot{}, failure("SERVER_CLOSING")
	}
	if len(m.rooms) >= m.cfg.MaxRooms {
		m.mu.Unlock()
		m.cancel(s, g)
		return Snapshot{}, failure("SERVER_BUSY")
	}
	var n string
	for i := 0; i < m.cfg.NumberRetries; i++ {
		n, e = m.nextNumber()
		if e != nil {
			break
		}
		if m.rooms[roomKey{s.gameID, n}] == nil {
			break
		}
		n = ""
	}
	if e != nil || !validNumber(n) {
		m.mu.Unlock()
		m.cancel(s, g)
		return Snapshot{}, failure("SERVER_BUSY")
	}
	r := newActor(m, s.gameID, n, o, p)
	m.rooms[roomKey{s.gameID, n}] = r
	m.wg.Add(1)
	go r.run()
	m.mu.Unlock()
	if m.cfg.beforeSubmit != nil {
		m.cfg.beforeSubmit()
	}
	v, e := m.finish(ctx, s, g, r)
	if e != nil {
		r.cleanup(s, g)
	}
	return v, e
}
func (m *Manager) nextNumber() (string, error) {
	if m.cfg.number != nil {
		return m.cfg.number()
	}
	n, e := rand.Int(rand.Reader, big.NewInt(1000000))
	if e != nil {
		return "", e
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}
func validNumber(n string) bool {
	if len(n) != 6 {
		return false
	}
	for _, c := range n {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func (m *Manager) Join(ctx context.Context, s *Session, o JoinOptions) (Snapshot, error) {
	ctx, cancel := m.request(ctx)
	defer cancel()
	if !validNumber(o.RoomNumber) || (o.Password != nil && !validPassword(o.Password)) {
		return Snapshot{}, failure("INVALID_MESSAGE")
	}
	g, e := m.reserve(s)
	if e != nil {
		return Snapshot{}, e
	}
	m.mu.Lock()
	r := m.rooms[roomKey{s.gameID, o.RoomNumber}]
	if r != nil && (!r.published || r.gameID != s.gameID) {
		r = nil
	}
	m.mu.Unlock()
	if r == nil {
		m.cancel(s, g)
		return Snapshot{}, failure("ROOM_NOT_FOUND")
	}
	if r.password != nil {
		if o.Password == nil {
			m.cancel(s, g)
			return Snapshot{}, failure("PASSWORD_REQUIRED")
		}
		if e := m.limits.check(s, r.id); e != nil {
			m.cancel(s, g)
			return Snapshot{}, e
		}
		matched, err := m.verifyContext(ctx, s, *o.Password, r.password)
		if err != nil {
			m.cancel(s, g)
			return Snapshot{}, err
		}
		if !matched {
			m.limits.record(s, r.id)
			m.cancel(s, g)
			return Snapshot{}, failure("PASSWORD_INVALID")
		}
	}
	return m.finish(ctx, s, g, r)
}
func (m *Manager) Leave(ctx context.Context, s *Session) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	m.mu.Lock()
	if s == nil || s.manager != m {
		m.mu.Unlock()
		return failure("SESSION_CLOSED")
	}
	s.mu.Lock()
	if s.closed || m.sessions[s.id] != s {
		s.mu.Unlock()
		m.mu.Unlock()
		return failure("SESSION_CLOSED")
	}
	r := s.room
	token := s.membershipToken
	s.generation++
	s.joining = false
	if r == nil || r.gameID != s.gameID || m.rooms[roomKey{r.gameID, r.number}] != r {
		s.mu.Unlock()
		m.mu.Unlock()
		return failure("NOT_IN_ROOM")
	}
	s.mu.Unlock()
	m.mu.Unlock()
	if r == nil || r.gameID != s.gameID {
		return failure("NOT_IN_ROOM")
	}
	r.cleanup(s, token)
	return nil
}
func (m *Manager) Disconnect(s *Session) {
	m.mu.Lock()
	if s == nil || s.manager != m {
		m.mu.Unlock()
		return
	}
	s.mu.Lock()
	if m.sessions[s.id] != s {
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
	r := s.room
	token := s.membershipToken
	s.mu.Unlock()
	m.mu.Unlock()
	if r != nil && r.gameID == s.gameID {
		r.cleanup(s, token)
	}
}
func (m *Manager) ListPublic(ctx context.Context, s *Session) ([]Snapshot, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	m.mu.Lock()
	if err := m.listSessionError(s); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	rs := make([]*actor, 0, len(m.rooms))
	for _, r := range m.rooms {
		if r.gameID == s.gameID && r.published && r.visibility == "public" {
			rs = append(rs, r)
		}
	}
	m.mu.Unlock()
	sort.Slice(rs, func(i, j int) bool { return rs[i].number < rs[j].number })
	out := []Snapshot{}
	for _, r := range rs {
		res := make(chan result, 1)
		select {
		case r.commands <- command{s: s, list: true, result: res}:
		case <-r.done:
			continue
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return nil, failure("SERVER_BUSY")
		}
		select {
		case v := <-res:
			if v.err == nil {
				out = append(out, v.snap)
			} else if CodeOf(v.err) != "ROOM_CLOSED" {
				return nil, v.err
			}
		case <-r.done:
			continue
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if len(out) >= m.cfg.ListLimit {
			break
		}
	}
	m.mu.Lock()
	err := m.listSessionError(s)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return out, nil
}
func (m *Manager) Close(ctx context.Context) error {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closing = true
		close(m.stop)
		ss := make([]*Session, 0, len(m.sessions))
		for _, s := range m.sessions {
			ss = append(ss, s)
		}
		m.mu.Unlock()
		go func() {
			for _, s := range ss {
				m.Disconnect(s)
			}
			m.wg.Wait()
			for {
				select {
				case j := <-m.hashJobs:
					clear(j.password)
				default:
					close(m.closed)
					return
				}
			}
		}()
	})
	ctx, cancel := context.WithTimeout(ctx, m.cfg.ShutdownTimeout)
	defer cancel()
	select {
	case <-m.closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// listSessionError requires the admission gate and preserves gate-before-session ordering.
func (m *Manager) listSessionError(s *Session) error {
	if s == nil || s.manager != m {
		return failure("SESSION_CLOSED")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || m.sessions[s.id] != s {
		return failure("SESSION_CLOSED")
	}
	if m.closing {
		return failure("SERVER_CLOSING")
	}
	return nil
}
func (m *Manager) HasGame(gameID string) bool { _, ok := m.games[gameID]; return ok }
func ValidGameID(gameID string) bool {
	if len(gameID) < 1 || len(gameID) > 64 {
		return false
	}
	for i := 0; i < len(gameID); i++ {
		c := gameID[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			continue
		}
		if i > 0 && (c == '_' || c == '-') {
			continue
		}
		return false
	}
	return true
}

// LegacyDefault reports only the sole-default compatibility configuration.
func (m *Manager) LegacyDefault() bool { return len(m.games) == 1 && m.HasGame("default") }
