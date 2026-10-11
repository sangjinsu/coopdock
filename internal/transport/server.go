// Package transport serves room entry over bounded WebSocket connections.
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/coder/websocket"
	"net"
	"net/http"
	"net/url"
	"roomserver/internal/protocol"
	"roomserver/internal/room"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Origins                                                    []string
	MessageLimit                                               int64
	IngressQueue, OutboundQueue                                int
	ReadTimeout, WriteTimeout, RequestTimeout, ShutdownTimeout time.Duration
}

func DefaultConfig() Config {
	return Config{MessageLimit: 16 * 1024, IngressQueue: 16, OutboundQueue: 64, ReadTimeout: 120 * time.Second, WriteTimeout: 5 * time.Second, RequestTimeout: 5 * time.Second, ShutdownTimeout: 10 * time.Second}
}
func (c Config) Validate() error {
	if c.MessageLimit < 256 || c.MessageLimit > 1024*1024 || c.IngressQueue < 1 || c.IngressQueue > 4096 || c.OutboundQueue < 1 || c.OutboundQueue > 4096 || c.ReadTimeout <= 0 || c.ReadTimeout > 24*time.Hour || c.WriteTimeout <= 0 || c.WriteTimeout > time.Minute || c.RequestTimeout <= 0 || c.RequestTimeout > time.Minute || c.ShutdownTimeout <= 0 || c.ShutdownTimeout > time.Minute {
		return errors.New("Invalid transport configuration.")
	}
	for _, v := range c.Origins {
		if !validOrigin(v) {
			return errors.New("Invalid transport origins.")
		}
	}
	return nil
}

type Server struct {
	manager   *room.Manager
	cfg       Config
	mux       *http.ServeMux
	mu        sync.Mutex
	closing   bool
	peers     map[*peer]struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
	closed    chan struct{}
	// Package-private barriers exercise lifecycle boundaries in integration tests.
	beforeAccept  func()
	beforeProcess func(context.Context, protocol.Request)
	afterRequest  func(context.Context, protocol.Request)
	beforeWrite   func(context.Context, protocol.Envelope)
	afterRegister func(*peer)
}

func New(m *room.Manager, c Config) (*Server, error) {
	if m == nil {
		return nil, errors.New("Room manager is required.")
	}
	if e := c.Validate(); e != nil {
		return nil, e
	}
	c.Origins = append([]string(nil), c.Origins...)
	s := &Server{manager: m, cfg: c, peers: map[*peer]struct{}{}, closed: make(chan struct{}), mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	s.mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		closing := s.closing
		s.mu.Unlock()
		if closing || !m.Ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	s.mux.HandleFunc("GET /ws", s.accept)
	return s, nil
}
func (s *Server) Handler() http.Handler { return s.mux }
func (s *Server) accept(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		http.Error(w, "Server is closing.", http.StatusServiceUnavailable)
		return
	}
	s.wg.Add(1)
	beforeAccept := s.beforeAccept
	s.mu.Unlock()
	defer s.wg.Done()
	ip, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		ip = r.RemoteAddr
	}
	session, e := s.manager.Connect(ip)
	if e != nil {
		http.Error(w, "Server is unavailable.", http.StatusServiceUnavailable)
		return
	}
	if beforeAccept != nil {
		beforeAccept()
	}
	c, e := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.cfg.Origins})
	if e != nil {
		s.manager.Disconnect(session)
		return
	}
	c.SetReadLimit(s.cfg.MessageLimit)
	ctx, cancel := context.WithCancel(context.Background())
	p := &peer{server: s, conn: c, session: session, ctx: ctx, cancel: cancel, ingress: make(chan incoming, s.cfg.IngressQueue), outbound: make(chan protocol.Envelope, s.cfg.OutboundQueue)}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		p.stop()
		s.manager.Disconnect(session)
		return
	}
	s.peers[p] = struct{}{}
	afterRegister := s.afterRegister
	s.mu.Unlock()
	if afterRegister != nil {
		afterRegister(p)
	}
	p.run()
	s.mu.Lock()
	delete(s.peers, p)
	s.mu.Unlock()
}
func (s *Server) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		ps := make([]*peer, 0, len(s.peers))
		for p := range s.peers {
			ps = append(ps, p)
		}
		s.mu.Unlock()
		for _, p := range ps {
			p.stop()
		}
		go func() {
			// Cleanup continues after the caller's deadline expires.
			for s.manager.Close(context.Background()) != nil {
			}
			s.wg.Wait()
			close(s.closed)
		}()
	})
	ctx, cancel := context.WithTimeout(ctx, s.cfg.ShutdownTimeout)
	defer cancel()
	select {
	case <-s.closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type incoming struct {
	kind websocket.MessageType
	data []byte
}
type peer struct {
	server      *Server
	conn        *websocket.Conn
	session     *room.Session
	ctx         context.Context
	cancel      context.CancelFunc
	stopOnce    sync.Once
	ingress     chan incoming
	outbound    chan protocol.Envelope
	roomID      string
	minRevision uint64
}

func (p *peer) stop() { p.stopOnce.Do(func() { p.cancel(); p.conn.CloseNow() }) }
func (p *peer) enqueue(v protocol.Envelope) bool {
	select {
	case <-p.ctx.Done():
		return false
	default:
	}
	select {
	case p.outbound <- v:
		return true
	default:
		p.stop()
		return false
	}
}
func (p *peer) run() {
	var workers sync.WaitGroup
	workers.Add(3)
	go func() { defer workers.Done(); p.reader() }()
	go func() { defer workers.Done(); p.processor() }()
	go func() { defer workers.Done(); p.writer() }()
	select {
	case <-p.ctx.Done():
	case <-p.session.Done():
		p.stop()
	}
	p.stop()
	p.server.manager.Disconnect(p.session)
	workers.Wait()
}
func (p *peer) reader() {
	defer p.stop()
	for {
		ctx, cancel := context.WithTimeout(p.ctx, p.server.cfg.ReadTimeout)
		kind, data, e := p.conn.Read(ctx)
		cancel()
		if e != nil {
			return
		}
		select {
		case <-p.ctx.Done():
			return
		case p.ingress <- incoming{kind, data}:
		default:
			return
		}
	}
}
func (p *peer) writer() {
	defer p.stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case v := <-p.outbound:
			p.server.mu.Lock()
			hook := p.server.beforeWrite
			p.server.mu.Unlock()
			if hook != nil {
				hook(p.ctx, v)
			}
			if p.ctx.Err() != nil {
				return
			}
			b, e := json.Marshal(v)
			if e != nil {
				return
			}
			ctx, cancel := context.WithTimeout(p.ctx, p.server.cfg.WriteTimeout)
			e = p.conn.Write(ctx, websocket.MessageText, b)
			cancel()
			if e != nil {
				return
			}
		}
	}
}
func (p *peer) processor() {
	defer p.stop()
	p.enqueue(protocol.Event("session.guest", map[string]string{"session_id": p.session.ID()}))
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-p.session.Done():
			return
		case event := <-p.session.Events():
			if event.Op != "room.state" {
				continue
			}
			state := event.Snapshot
			// Ignore stale membership events and the initial snapshot already sent.
			if state.RoomID == p.roomID && state.Revision > p.minRevision {
				if !p.enqueue(protocol.Event("room.state", state)) {
					return
				}
			}
		case in := <-p.ingress:
			r, e := protocol.Decode(in.data)
			if e != nil || in.kind != websocket.MessageText {
				if !p.enqueue(protocol.Invalid(r)) {
					return
				}
				continue
			}
			p.server.mu.Lock()
			hook := p.server.beforeProcess
			p.server.mu.Unlock()
			if hook != nil {
				hook(p.ctx, r)
			}
			if p.ctx.Err() != nil {
				return
			}
			ctx, cancel := context.WithTimeout(p.ctx, p.server.cfg.RequestTimeout)
			var payload any
			var snapshot room.Snapshot
			switch r.Op {
			case "room.create":
				snapshot, e = p.server.manager.Create(ctx, p.session, r.Create)
				payload = map[string]any{"snapshot": snapshot}
			case "room.join":
				snapshot, e = p.server.manager.Join(ctx, p.session, r.Join)
				payload = map[string]any{"snapshot": snapshot}
			case "room.list":
				var rooms []room.Snapshot
				rooms, e = p.server.manager.ListPublic(ctx)
				payload = map[string]any{"rooms": rooms}
			case "room.leave":
				e = p.server.manager.Leave(ctx, p.session)
				payload = struct{}{}
			}
			cancel()
			p.server.mu.Lock()
			afterRequest := p.server.afterRequest
			p.server.mu.Unlock()
			if afterRequest != nil {
				afterRequest(p.ctx, r)
			}
			if p.ctx.Err() != nil {
				return
			}
			if !p.enqueue(protocol.Response(r, payload, e)) {
				return
			}
			if e == nil {
				switch r.Op {
				case "room.create", "room.join":
					if !p.enqueue(protocol.Event("room.state", snapshot)) {
						return
					}
					p.roomID = snapshot.RoomID
					p.minRevision = snapshot.Revision
				case "room.leave":
					p.roomID = ""
					p.minRevision = 0
				}
			}
		}
	}
}

func validOrigin(v string) bool {
	if v == "" || strings.ContainsAny(v, "*/?#@ \t\r\n") || strings.Contains(v, "://") {
		return false
	}
	u, e := url.Parse("https://" + v)
	if e != nil || u.Host != v || u.Hostname() == "" {
		return false
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return false
		}
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-') {
				return false
			}
		}
	}
	return true
}
