package main

import (
	"context"
	"net/http/httptest"
	"roomserver/internal/game"
	"roomserver/internal/room"
	"roomserver/internal/transport"
	"strings"
	"testing"
	"time"
)

func TestMovementChecker(t *testing.T) {
	cfg := room.DefaultConfig()
	// A nondefault rate catches a checker that assumes the server runs at 30 Hz.
	cfg.TickHz, cfg.SnapshotHz = 20, 10
	m, err := room.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := transport.New(m, transport.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	h := httptest.NewServer(s.Handler())
	defer h.Close()
	defer s.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = runMovementChecks(ctx, h.URL+"/ws"); err != nil {
		t.Fatal(err)
	}
	// Closed clients must remove their memberships and the final empty room.
	deadline := time.Now().Add(time.Second)
	for {
		rooms, e := m.ListPublic(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if len(rooms) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("movement room remains after disconnect")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestMovementCheckerUnreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runMovementChecks(ctx, "ws://127.0.0.1:1/ws"); err == nil {
		t.Fatal("false success for unreachable server")
	}
}

type noMovementHandler struct{ game.GameHandler }

func (h noMovementHandler) Apply(id string, action game.Action) error {
	action.Direction = game.Vec3{}
	return h.GameHandler.Apply(id, action)
}

func TestMovementCheckerRejectsAcknowledgedButStationaryPlayer(t *testing.T) {
	cfg := room.DefaultConfig()
	cfg.HandlerFactory = func() game.GameHandler { return noMovementHandler{game.NewMovement()} }
	m, err := room.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := transport.New(m, transport.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	h := httptest.NewServer(s.Handler())
	defer h.Close()
	defer s.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = runMovementChecks(ctx, h.URL+"/ws"); err == nil || !strings.Contains(err.Error(), "양의 Z 이동") {
		t.Fatalf("ACK without movement must fail the movement assertion: %v", err)
	}
}

func TestMovementSnapshotComparison(t *testing.T) {
	fixture := func() room.Snapshot {
		id := "match"
		return room.Snapshot{RoomID: "room", MatchID: &id, Phase: "PLAYING", Revision: 4, ServerTick: 2, SnapshotSeq: 9, Members: []room.Member{{SessionID: "a"}, {SessionID: "b"}}, GameState: &game.State{Players: []game.Player{{SessionID: "a", Position: game.Vec3{X: 1, Y: 2, Z: 3}}}}, LastProcessedInputSeq: map[string]uint64{"a": 1, "b": 0}}
	}
	if err := compareMovementSnapshots([]room.Snapshot{fixture(), fixture()}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*room.Snapshot)
	}{
		{"match", func(s *room.Snapshot) { id := "other"; s.MatchID = &id }},
		{"tick", func(s *room.Snapshot) { s.ServerTick++ }},
		{"revision", func(s *room.Snapshot) { s.Revision++ }},
		{"capture", func(s *room.Snapshot) { s.SnapshotSeq++ }},
		{"position", func(s *room.Snapshot) { s.GameState.Players[0].Position.Z++ }},
		{"velocity", func(s *room.Snapshot) { s.GameState.Players[0].Velocity.Z = 5 }},
		{"member order", func(s *room.Snapshot) { s.Members[0], s.Members[1] = s.Members[1], s.Members[0] }},
		{"input sequence", func(s *room.Snapshot) { s.LastProcessedInputSeq["a"]++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := fixture(), fixture()
			tc.change(&b)
			if err := compareMovementSnapshots([]room.Snapshot{a, b}); err == nil {
				t.Fatal("accepted different public snapshots")
			}
		})
	}
	if err := compareMovementSnapshots(nil); err == nil {
		t.Fatal("accepted empty captures")
	}
}
