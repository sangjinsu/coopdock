package loadtest

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"

	"github.com/sangjinsu/coopdock/internal/game"
	"github.com/sangjinsu/coopdock/internal/room"
)

type binding struct {
	gameID, roomID, matchID string
	members                 []string
}

func validateSnapshot(s room.Snapshot, b binding) error {
	if s.GameID != b.gameID || s.RoomID != b.roomID || s.MatchID == nil || *s.MatchID != b.matchID || s.Phase != "PLAYING" {
		return errors.New("snapshot game/room/match/phase binding mismatch")
	}
	if len(s.Members) != len(b.members) || s.GameState == nil || len(s.GameState.Players) != len(b.members) || len(s.LastProcessedInputSeq) != len(b.members) {
		return errors.New("snapshot member/player/processed sequence set mismatch")
	}
	expected := make(map[string]bool, len(b.members))
	for _, id := range b.members {
		expected[id] = true
	}
	members, players := make(map[string]bool, len(b.members)), make(map[string]bool, len(b.members))
	for _, m := range s.Members {
		if !expected[m.SessionID] || members[m.SessionID] {
			return errors.New("snapshot member binding mismatch")
		}
		members[m.SessionID] = true
	}
	for _, p := range s.GameState.Players {
		if !expected[p.SessionID] || players[p.SessionID] {
			return errors.New("snapshot player binding mismatch")
		}
		for _, v := range []float64{p.Position.X, p.Position.Y, p.Position.Z, p.Velocity.X, p.Velocity.Y, p.Velocity.Z} {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return errors.New("non-finite player state")
			}
		}
		players[p.SessionID] = true
	}
	if !expected[s.HostID] {
		return errors.New("snapshot host binding mismatch")
	}
	for id := range s.LastProcessedInputSeq {
		if !expected[id] {
			return errors.New("snapshot processed sequence binding mismatch")
		}
	}
	return nil
}

type inputPending struct {
	match    string
	sequence uint64
}

type ackTracker struct{ sequence, tick uint64 }

func (a *ackTracker) validate(id, pendingID string, p inputPending, ack room.ActionAck) error {
	if id != pendingID || ack.MatchID != p.match || ack.InputSeq != p.sequence || ack.InputSeq != a.sequence+1 || ack.ServerTick == 0 || ack.ServerTick < a.tick {
		return errors.New("input ACK request/match/sequence/FIFO/tick mismatch")
	}
	a.sequence, a.tick = ack.InputSeq, ack.ServerTick
	return nil
}

type captureKey struct {
	gameID, roomID, matchID  string
	revision, tick, sequence uint64
}

type capture struct {
	canonical    []byte
	periodicBits uint64
}

type captureWindow struct {
	limit     int
	fullBits  uint64
	entries   map[captureKey]*capture
	order     []captureKey
	evictions uint64
}

func newCaptureWindow(limit, players int) *captureWindow {
	return &captureWindow{limit: limit, fullBits: (1 << players) - 1, entries: make(map[captureKey]*capture)}
}

func canonicalSnapshot(s room.Snapshot) ([]byte, error) {
	// Object keys are canonicalized by encoding/json; array order is part of the snapshot.
	return json.Marshal(s)
}

func (w *captureWindow) observe(s room.Snapshot, client int, measuredPeriodic bool) (bool, error) {
	data, err := canonicalSnapshot(s)
	if err != nil {
		return false, err
	}
	if s.MatchID == nil {
		return false, errors.New("capture match missing")
	}
	key := captureKey{s.GameID, s.RoomID, *s.MatchID, s.Revision, s.ServerTick, s.SnapshotSeq}
	entry := w.entries[key]
	if entry == nil {
		if len(w.order) == w.limit {
			delete(w.entries, w.order[0])
			w.order = w.order[1:]
			w.evictions++
		}
		entry = &capture{canonical: data}
		w.entries[key] = entry
		w.order = append(w.order, key)
	} else if !bytes.Equal(entry.canonical, data) {
		return false, errors.New("same capture has different canonical full snapshot")
	}
	before := entry.periodicBits
	if measuredPeriodic {
		entry.periodicBits |= 1 << client
	}
	return before != w.fullBits && entry.periodicBits == w.fullBits, nil
}

func playerByID(s room.Snapshot, id string) game.Player {
	for _, p := range s.GameState.Players {
		if p.SessionID == id {
			return p
		}
	}
	return game.Player{}
}

func samePosition(a, b game.Vec3) bool {
	return math.Abs(a.X-b.X) < 1e-9 && math.Abs(a.Y-b.Y) < 1e-9 && math.Abs(a.Z-b.Z) < 1e-9
}
