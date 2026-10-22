package loadtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/sangjinsu/coopdock/internal/game"
	"github.com/sangjinsu/coopdock/internal/protocol"
	"github.com/sangjinsu/coopdock/internal/room"
)

type message struct {
	Version int             `json:"version"`
	Op      string          `json:"op"`
	ID      *string         `json:"request_id"`
	Payload json.RawMessage `json:"payload"`
	Error   *protocol.Error `json:"error"`
}

type response struct {
	message message
	err     error
}
type pending struct {
	op             string
	input          inputPending
	sent           time.Time
	measured, zero bool
	result         chan response
	writeContext   context.Context
	removalProbe   bool
}

type client struct {
	run                                    *runner
	room                                   *roomRun
	index                                  int
	conn                                   *websocket.Conn
	writeMu                                sync.Mutex
	requestSequence, inputSequence         uint64
	highestSubmittedSequence               uint64
	lastProcessed                          map[string]uint64
	removalProbing                         bool
	sessionID                              string
	pending                                map[string]*pending
	ack                                    ackTracker
	guest                                  chan error
	done                                   chan struct{}
	ready, joined                          bool
	window                                 measurementWindow
	lastPeriodicSequence, lastPeriodicTick uint64
	measuredSent                           uint64
	zeroSequence, zeroTick                 uint64
	zeroAck, stopped, stable               bool
	stoppedPosition                        game.Vec3
	stoppedTick, stoppedSnapshot           uint64
}

func decodePayload(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid payload: %w", err)
	}
	return nil
}

func (r *runner) connect(ctx context.Context, rr *roomRun, index int) (*client, error) {
	u, _ := url.Parse(r.config.URL)
	u.RawQuery = url.Values{"game_id": {rr.binding.gameID}}.Encode()
	conn, _, err := websocket.Dial(ctx, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("WebSocket dial: %w", err)
	}
	conn.SetReadLimit(256 * 1024)
	cl := &client{run: r, room: rr, index: index, conn: conn, pending: make(map[string]*pending), guest: make(chan error, 1), done: make(chan struct{})}
	r.mu.Lock()
	r.clients = append(r.clients, cl)
	rr.clients = append(rr.clients, cl)
	r.mu.Unlock()
	go cl.read()
	select {
	case err := <-cl.guest:
		return cl, err
	case <-cl.done:
		return cl, errors.New("connection closed before valid guest")
	case <-ctx.Done():
		return cl, ctx.Err()
	}
}

// A request timeout only bounds the caller; it never cancels the persistent Read.
func (c *client) request(ctx context.Context, op string, payload any) (message, error) {
	return c.requestPending(ctx, op, payload, &pending{})
}

func (c *client) requestPending(ctx context.Context, op string, payload any, p *pending) (message, error) {
	waiting, cancel := context.WithTimeout(ctx, c.run.config.RequestTimeout)
	defer cancel()
	if err := waiting.Err(); err != nil {
		return message{}, err
	}
	result := make(chan response, 1)
	p.op, p.result, p.writeContext = op, result, waiting
	if err := c.send(op, payload, p); err != nil {
		return message{}, err
	}
	select {
	case v := <-result:
		return v.message, v.err
	case <-c.done:
		return message{}, errors.New("connection reader stopped while awaiting response")
	case <-waiting.Done():
		c.run.mu.Lock()
		if errors.Is(waiting.Err(), context.DeadlineExceeded) {
			c.run.total.values.Timeouts++
			c.room.metrics.values.Timeouts++
		}
		c.run.mu.Unlock()
		return message{}, waiting.Err()
	}
}

func (c *client) sendAction(ctx context.Context, direction game.Vec3, zero bool) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	r := c.run
	r.mu.Lock()
	c.inputSequence++
	seq := c.inputSequence
	match := c.room.binding.matchID
	if zero {
		c.zeroSequence = seq
	}
	r.mu.Unlock()
	payload := map[string]any{"match_id": match, "input_seq": seq, "action": game.Action{Type: "move", Direction: direction}}
	return c.sendLocked("game.action", payload, &pending{op: "game.action", input: inputPending{match: match, sequence: seq}, zero: zero, writeContext: ctx})
}

func (c *client) send(op string, payload any, p *pending) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.sendLocked(op, payload, p)
}

func (c *client) sendLocked(op string, payload any, p *pending) error {
	r := c.run
	if p.writeContext != nil && p.writeContext.Err() != nil {
		return p.writeContext.Err()
	}
	select {
	case <-c.done:
		return errors.New("cannot send on a stopped connection")
	default:
	}
	r.mu.Lock()
	if len(c.pending) >= r.config.MaxPending {
		r.total.values.PendingOverflows++
		c.room.metrics.values.PendingOverflows++
		r.mu.Unlock()
		return errors.New("pending request capacity exceeded; input was not silently skipped")
	}
	c.requestSequence++
	id := fmt.Sprintf("load-%d", c.requestSequence)
	p.sent = time.Now()
	p.measured = op == "game.action" && !p.zero && !r.start.IsZero() && !p.sent.Before(r.start) && p.sent.Before(r.end)
	c.pending[id] = p
	// Register before Write: another reader may see this input applied before Write returns.
	if op == "game.action" {
		c.highestSubmittedSequence = p.input.sequence
	}
	r.mu.Unlock()
	data, err := json.Marshal(protocol.Envelope{Version: 1, Op: op, RequestID: &id, Payload: payload})
	if err == nil {
		writeContext := r.ctx
		if p.writeContext != nil {
			writeContext = p.writeContext
		}
		writing, cancel := context.WithTimeout(writeContext, r.config.RequestTimeout)
		err = c.conn.Write(writing, websocket.MessageText, data)
		cancel()
	}
	if err != nil {
		return fmt.Errorf("WebSocket write %s: %w", op, err)
	}
	r.mu.Lock()
	if p.measured {
		r.total.values.InputsSent++
		c.room.metrics.values.InputsSent++
		c.measuredSent++
	}
	if p.zero {
		r.total.values.ZeroSent++
		c.room.metrics.values.ZeroSent++
	}
	r.notifyLocked()
	r.mu.Unlock()
	return nil
}

func (c *client) read() {
	defer close(c.done)
	defer c.conn.CloseNow()
	for {
		kind, data, err := c.conn.Read(c.run.ctx)
		if err != nil {
			c.run.mu.Lock()
			if !c.run.cleaning && !c.run.closing && c.run.ctx.Err() == nil {
				c.run.total.values.Disconnects++
				c.room.metrics.values.Disconnects++
				c.run.failLocked(fmt.Errorf("room %d client %d unexpected disconnect: %w", c.room.index+1, c.index+1, err))
			}
			c.run.mu.Unlock()
			return
		}
		var v message
		var fields map[string]json.RawMessage
		if kind != websocket.MessageText || json.Unmarshal(data, &v) != nil || json.Unmarshal(data, &fields) != nil || len(fields) != 5 || v.Version != 1 {
			c.run.fail(errors.New("invalid response envelope"))
			return
		}
		for _, key := range []string{"version", "op", "request_id", "payload", "error"} {
			if _, ok := fields[key]; !ok {
				c.run.fail(errors.New("response envelope field missing"))
				return
			}
		}
		c.run.mu.Lock()
		err = c.consumeLocked(v, time.Now())
		if err != nil {
			c.run.failLocked(fmt.Errorf("room %d client %d: %w", c.room.index+1, c.index+1, err))
		}
		c.run.notifyLocked()
		c.run.mu.Unlock()
		if err != nil {
			return
		}
	}
}

func (c *client) consumeLocked(v message, at time.Time) error {
	r := c.run
	if c.sessionID == "" {
		var guest struct {
			SessionID string `json:"session_id"`
			GameID    string `json:"game_id"`
		}
		if v.Op != "session.guest" || v.ID != nil || v.Error != nil || decodePayload(v.Payload, &guest) != nil || guest.SessionID == "" || guest.GameID != c.room.binding.gameID {
			return errors.New("invalid guest or game binding")
		}
		c.sessionID = guest.SessionID
		c.guest <- nil
		return nil
	}
	if v.ID == nil {
		if v.Error != nil {
			return errors.New("unsolicited server error")
		}
		switch v.Op {
		case "room.state", "game.snapshot", "game.started":
			return c.snapshotMessageLocked(v, at)
		case "game.ended":
			if r.cleaning {
				return nil
			}
			return errors.New("match ended during load run")
		case "game.event":
			var event room.GameEvent
			if decodePayload(v.Payload, &event) != nil || event.MatchID != c.room.binding.matchID {
				return errors.New("game event match binding mismatch")
			}
			return nil
		default:
			return fmt.Errorf("unexpected event %q", v.Op)
		}
	}
	p := c.pending[*v.ID]
	if p == nil || p.op != v.Op {
		return errors.New("response request_id/op does not match pending request")
	}
	var resultErr error
	if v.Error != nil {
		if !p.removalProbe || v.Error.Code != "ROOM_NOT_FOUND" {
			r.total.values.ServerErrors++
			c.room.metrics.values.ServerErrors++
			resultErr = fmt.Errorf("%s returned server error %s", v.Op, v.Error.Code)
		}
	} else {
		switch v.Op {
		case "game.action":
			var ack room.ActionAck
			if err := decodePayload(v.Payload, &ack); err != nil {
				return err
			}
			if err := c.ack.validate(*v.ID, *v.ID, p.input, ack); err != nil {
				return err
			}
			if p.measured {
				elapsed := at.Sub(p.sent)
				r.total.rtt.add(elapsed)
				c.room.metrics.rtt.add(elapsed)
				r.total.values.InputACKs++
				c.room.metrics.values.InputACKs++
			}
			if p.zero {
				c.zeroAck = true
				c.zeroTick = ack.ServerTick
				r.total.values.ZeroACKs++
				c.room.metrics.values.ZeroACKs++
			}
		case "room.create", "room.join", "room.start":
			if p.removalProbe {
				c.joined = true
				break
			}
			var wrapper struct {
				Snapshot room.Snapshot `json:"snapshot"`
			}
			if err := decodePayload(v.Payload, &wrapper); err != nil {
				return err
			}
			s := wrapper.Snapshot
			if s.GameID != c.room.binding.gameID || s.RoomID == "" || len(s.RoomNumber) != 6 || s.Capacity != r.config.Players || s.Visibility != "public" {
				return errors.New("room response binding/capacity/visibility mismatch")
			}
			if v.Op == "room.create" {
				c.room.binding.roomID = s.RoomID
				c.room.number = s.RoomNumber
			}
			if s.RoomID != c.room.binding.roomID {
				return errors.New("room response belongs to another room")
			}
			if v.Op == "room.start" {
				if s.MatchID == nil || *s.MatchID == "" {
					return errors.New("start match missing")
				}
				if c.room.binding.matchID != "" && c.room.binding.matchID != *s.MatchID {
					return errors.New("start match mismatch")
				}
				c.room.binding.matchID = *s.MatchID
			}
			c.joined = true
			if err := c.acceptSnapshotLocked(s, v.Op, at); err != nil {
				return err
			}
		case "room.leave":
			c.joined = false
		case "room.list":
			var listed struct {
				Rooms []room.Snapshot `json:"rooms"`
			}
			if err := decodePayload(v.Payload, &listed); err != nil {
				return err
			}
			if listed.Rooms == nil {
				return errors.New("room list missing")
			}
			for _, s := range listed.Rooms {
				if s.GameID != c.room.binding.gameID {
					return errors.New("room list game binding mismatch")
				}
			}
		default:
			return fmt.Errorf("unexpected response %q", v.Op)
		}
	}
	delete(c.pending, *v.ID)
	if p.result != nil {
		p.result <- response{v, resultErr}
	}
	if resultErr != nil && !r.cleaning {
		return resultErr
	}
	return nil
}

func (c *client) snapshotMessageLocked(v message, at time.Time) error {
	var s room.Snapshot
	if v.Op == "game.started" {
		var wrapper struct {
			Snapshot room.Snapshot `json:"snapshot"`
		}
		if err := decodePayload(v.Payload, &wrapper); err != nil {
			return err
		}
		s = wrapper.Snapshot
	} else if err := decodePayload(v.Payload, &s); err != nil {
		return err
	}
	return c.acceptSnapshotLocked(s, v.Op, at)
}

func (c *client) acceptSnapshotLocked(s room.Snapshot, op string, at time.Time) error {
	r, rr := c.run, c.room
	if r.cleaning && c.removalProbing {
		if s.GameID != rr.binding.gameID {
			return errors.New("cleanup probe snapshot game binding mismatch")
		}
		return nil
	}
	if s.GameID != rr.binding.gameID || rr.binding.roomID != "" && s.RoomID != rr.binding.roomID {
		return errors.New("snapshot game/room binding mismatch")
	}
	if r.cleaning {
		return nil
	}
	if s.Phase == "WAITING" && !c.ready {
		return nil
	}
	if s.MatchID == nil || *s.MatchID == "" {
		return errors.New("playing snapshot match missing")
	}
	if rr.binding.matchID == "" {
		rr.binding.matchID = *s.MatchID
	}
	if err := validateSnapshot(s, rr.binding); err != nil {
		return err
	}
	for _, sender := range rr.clients {
		sequence := s.LastProcessedInputSeq[sender.sessionID]
		if sequence > sender.highestSubmittedSequence {
			return errors.New("snapshot processed sequence exceeds submitted input")
		}
		if sequence < c.lastProcessed[sender.sessionID] {
			return errors.New("snapshot processed sequence regressed")
		}
	}
	if c.lastProcessed == nil {
		c.lastProcessed = make(map[string]uint64, len(rr.clients))
	}
	for id, sequence := range s.LastProcessedInputSeq {
		c.lastProcessed[id] = sequence
	}
	c.ready = true
	for _, p := range s.GameState.Players {
		if _, exists := rr.origins[p.SessionID]; !exists {
			rr.origins[p.SessionID] = p.Position
		}
	}
	interval, gaps, measured := c.window.observe(op, s, at)
	common, err := rr.captures.observe(s, c.index, measured)
	if err != nil {
		return err
	}
	if common {
		rr.common++
	}
	if measured {
		r.total.values.Snapshots++
		rr.metrics.values.Snapshots++
		r.total.values.SequenceGaps += gaps
		rr.metrics.values.SequenceGaps += gaps
		if interval > 0 {
			r.total.intervals.add(interval)
			rr.metrics.intervals.add(interval)
		}
		for _, p := range s.GameState.Players {
			if !samePosition(rr.origins[p.SessionID], p.Position) {
				rr.moved[p.SessionID] = true
			}
		}
	}
	if op == "game.snapshot" {
		if s.SnapshotSeq <= c.lastPeriodicSequence || s.ServerTick < c.lastPeriodicTick {
			return errors.New("periodic snapshot sequence/tick regressed")
		}
		c.lastPeriodicSequence, c.lastPeriodicTick = s.SnapshotSeq, s.ServerTick
		if c.zeroSequence > 0 && c.zeroAck && s.LastProcessedInputSeq[c.sessionID] >= c.zeroSequence && s.ServerTick >= c.zeroTick {
			p := playerByID(s, c.sessionID)
			if p.Velocity != (game.Vec3{}) {
				return errors.New("final zero processed but velocity is nonzero")
			}
			if !c.stopped {
				c.stopped = true
				c.stoppedPosition = p.Position
				c.stoppedTick = s.ServerTick
				c.stoppedSnapshot = s.SnapshotSeq
			} else if s.ServerTick > c.stoppedTick && s.SnapshotSeq > c.stoppedSnapshot {
				if !samePosition(c.stoppedPosition, p.Position) {
					return errors.New("position changed after final zero")
				}
				c.stable = true
			}
		}
	}
	return nil
}

// A public list may be truncated; only an explicit failed join proves absence.
func (c *client) probeRoomAbsent(ctx context.Context) error {
	defer c.conn.CloseNow()
	c.run.mu.Lock()
	c.removalProbing = true
	c.run.mu.Unlock()
	reply, err := c.requestPending(ctx, "room.join", map[string]any{"room_number": c.room.number}, &pending{removalProbe: true})
	if reply.Error != nil && reply.Error.Code == "ROOM_NOT_FOUND" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("room absence probe requires ROOM_NOT_FOUND: %w", err)
	}
	// A successful probe acquired membership. Release it even if the number was reused.
	_, leaveErr := c.request(ctx, "room.leave", struct{}{})
	var wrapper struct {
		Snapshot room.Snapshot `json:"snapshot"`
	}
	decodeErr := decodePayload(reply.Payload, &wrapper)
	reason := errors.New("room absence probe joined a remaining room")
	if decodeErr != nil {
		reason = fmt.Errorf("room absence probe joined with invalid snapshot: %w", decodeErr)
	} else if wrapper.Snapshot.RoomID != c.room.binding.roomID {
		reason = errors.New("room number was reused; original room removal could not be verified")
	}
	if leaveErr != nil {
		return errors.Join(reason, fmt.Errorf("probe membership leave: %w", leaveErr))
	}
	return reason
}
