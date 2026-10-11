package room

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"golang.org/x/crypto/argon2"
	"unicode/utf8"
)

type credential struct{ salt, hash []byte }

func validPassword(p *string) bool {
	return p != nil && utf8.ValidString(*p) && utf8.RuneCountInString(*p) >= 4 && utf8.RuneCountInString(*p) <= 64
}
func (c Config) derive(p, s []byte) []byte {
	if c.hash != nil {
		return c.hash(p, s)
	}
	return argon2.IDKey(p, s, c.ArgonTime, c.ArgonMemory, c.ArgonThreads, c.HashBytes)
}

// Running derivations retain their worker slot until IDKey actually returns.
type hashJob struct {
	ctx            context.Context
	password, salt []byte
	result         chan []byte
}

func (m *Manager) hashWorker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.stop:
			return
		case j := <-m.hashJobs:
			if j.ctx.Err() != nil {
				clear(j.password)
				continue
			}
			m.mu.Lock()
			closing := m.closing
			m.mu.Unlock()
			if closing {
				clear(j.password)
				continue
			}
			h := m.cfg.derive(j.password, j.salt)
			clear(j.password)
			j.result <- h
		}
	}
}
func (m *Manager) compute(ctx context.Context, s *Session, p string, salt []byte) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	j := hashJob{ctx: ctx, password: []byte(p), salt: salt, result: make(chan []byte, 1)}
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		clear(j.password)
		return nil, failure("SERVER_CLOSING")
	}
	select {
	case m.hashJobs <- j:
		m.mu.Unlock()
	default:
		m.mu.Unlock()
		clear(j.password)
		return nil, failure("SERVER_BUSY")
	}
	select {
	case v := <-j.result:
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return v, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.done:
		return nil, failure("SESSION_CLOSED")
	case <-m.stop:
		return nil, failure("SERVER_CLOSING")
	}
}
func (m *Manager) passwordContext(ctx context.Context, s *Session, p string) (*credential, error) {
	salt := make([]byte, m.cfg.SaltBytes)
	if _, e := rand.Read(salt); e != nil {
		return nil, failure("SERVER_BUSY")
	}
	h, e := m.compute(ctx, s, p, salt)
	if e != nil {
		return nil, e
	}
	return &credential{salt: salt, hash: h}, nil
}
func (m *Manager) verifyContext(ctx context.Context, s *Session, p string, c *credential) (bool, error) {
	h, e := m.compute(ctx, s, p, c.salt)
	if e != nil {
		return false, e
	}
	return subtle.ConstantTimeCompare(h, c.hash) == 1, nil
}
