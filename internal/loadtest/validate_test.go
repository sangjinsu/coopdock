package loadtest

import (
	"testing"

	"github.com/sangjinsu/coopdock/internal/game"
	"github.com/sangjinsu/coopdock/internal/room"
)

func fixtureSnapshot() room.Snapshot {
	match := "match-a"
	return room.Snapshot{GameID: "game-a", RoomID: "room-a", RoomNumber: "123456", Capacity: 2, Visibility: "public", Phase: "PLAYING", MatchID: &match, HostID: "one", Members: []room.Member{{SessionID: "one"}, {SessionID: "two"}}, Revision: 3, ServerTick: 10, SnapshotSeq: 5, GameState: &game.State{Players: []game.Player{{SessionID: "one"}, {SessionID: "two"}}}, LastProcessedInputSeq: map[string]uint64{"one": 1, "two": 1}}
}

func fixtureBinding() binding {
	return binding{gameID: "game-a", roomID: "room-a", matchID: "match-a", members: []string{"one", "two"}}
}

func TestSnapshotBinding(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*room.Snapshot)
	}{
		{"game", func(s *room.Snapshot) { s.GameID = "game-b" }},
		{"room", func(s *room.Snapshot) { s.RoomID = "room-b" }},
		{"match", func(s *room.Snapshot) { v := "match-b"; s.MatchID = &v }},
		{"member", func(s *room.Snapshot) { s.Members[1].SessionID = "outsider" }},
		{"player", func(s *room.Snapshot) { s.GameState.Players[1].SessionID = "outsider" }},
		{"processed", func(s *room.Snapshot) { delete(s.LastProcessedInputSeq, "two") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixtureSnapshot()
			tc.change(&s)
			if err := validateSnapshot(s, fixtureBinding()); err == nil {
				t.Fatal("foreign or incomplete snapshot accepted")
			}
		})
	}
	if err := validateSnapshot(fixtureSnapshot(), fixtureBinding()); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureMismatch(t *testing.T) {
	w := newCaptureWindow(4, 2)
	s := fixtureSnapshot()
	if common, err := w.observe(s, 0, true); err != nil || common {
		t.Fatalf("first observer: %v, %v", common, err)
	}
	s = fixtureSnapshot()
	s.GameState.Players[0].Position.Z = 4
	if _, err := w.observe(s, 1, true); err == nil {
		t.Fatal("same capture with changed position accepted")
	}
}

func TestCaptureCanonicalAndBounds(t *testing.T) {
	w := newCaptureWindow(2, 2)
	s := fixtureSnapshot()
	if _, err := w.observe(s, 0, true); err != nil {
		t.Fatal(err)
	}
	s.LastProcessedInputSeq = map[string]uint64{"two": 1, "one": 1}
	if common, err := w.observe(s, 1, true); err != nil || !common {
		t.Fatalf("canonical full capture: %v, %v", common, err)
	}
	for seq := uint64(6); seq <= 10; seq++ {
		s.SnapshotSeq = seq
		if _, err := w.observe(s, 0, true); err != nil {
			t.Fatal(err)
		}
	}
	if len(w.entries) > 2 || w.evictions != 4 {
		t.Fatalf("capture window is not bounded: %d entries, %d evictions", len(w.entries), w.evictions)
	}
}

func TestAckValidation(t *testing.T) {
	p := inputPending{match: "match-a", sequence: 1}
	for _, tc := range []struct {
		name, id string
		ack      room.ActionAck
	}{
		{"request", "different", room.ActionAck{MatchID: "match-a", InputSeq: 1, ServerTick: 10}},
		{"sequence", "one", room.ActionAck{MatchID: "match-a", InputSeq: 2, ServerTick: 10}},
		{"match", "one", room.ActionAck{MatchID: "match-b", InputSeq: 1, ServerTick: 10}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var a ackTracker
			if err := a.validate(tc.id, "one", p, tc.ack); err == nil {
				t.Fatal("mismatched ACK accepted")
			}
		})
	}
	var a ackTracker
	if err := a.validate("one", "one", p, room.ActionAck{MatchID: "match-a", InputSeq: 1, ServerTick: 10}); err != nil {
		t.Fatal(err)
	}
	p.sequence = 2
	if err := a.validate("two", "two", p, room.ActionAck{MatchID: "match-a", InputSeq: 2, ServerTick: 10}); err != nil {
		t.Fatalf("same tick must be allowed: %v", err)
	}
	p.sequence = 3
	if err := a.validate("three", "three", p, room.ActionAck{MatchID: "match-a", InputSeq: 3, ServerTick: 9}); err == nil {
		t.Fatal("decreasing ACK tick accepted")
	}
	var fifo ackTracker
	p.sequence = 2
	if err := fifo.validate("two", "two", p, room.ActionAck{MatchID: "match-a", InputSeq: 2, ServerTick: 10}); err == nil {
		t.Fatal("out-of-order individual ACK accepted")
	}
}

func TestCaptureArrayOrderMismatch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*room.Snapshot)
	}{
		{"members", func(s *room.Snapshot) { s.Members[0], s.Members[1] = s.Members[1], s.Members[0] }},
		{"players", func(s *room.Snapshot) {
			s.GameState.Players[0], s.GameState.Players[1] = s.GameState.Players[1], s.GameState.Players[0]
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newCaptureWindow(4, 2)
			if _, err := w.observe(fixtureSnapshot(), 0, true); err != nil {
				t.Fatal(err)
			}
			s := fixtureSnapshot()
			tc.change(&s)
			if _, err := w.observe(s, 1, true); err == nil {
				t.Fatal("same capture accepted reordered full-snapshot array")
			}
		})
	}
}
