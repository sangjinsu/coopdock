package game

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

// A repeated move input must replace velocity instead of accumulating movement.
func TestMovementBurstDoesNotAccelerate(t *testing.T) {
	h := NewMovement()
	if err := h.Start([]string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if err := h.Apply("a", Action{Type: "move", Direction: Vec3{X: 1, Y: 1, Z: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.Tick(1.0 / 30); err != nil {
		t.Fatal(err)
	}
	p := h.Snapshot().Players[0]
	distance := math.Hypot(math.Hypot(p.Position.X, p.Position.Y), p.Position.Z)
	if math.Abs(distance-MoveSpeed/30) > 1e-12 {
		t.Fatalf("distance=%g", distance)
	}
	if h.Snapshot().Players[1].Position.X != SpawnSpacing {
		t.Fatal("spawn changed")
	}
	for _, component := range []float64{p.Velocity.X, p.Velocity.Y, p.Velocity.Z} {
		if math.Abs(component-2.886751345948129) > 1e-12 {
			t.Fatalf("normalized component=%g", component)
		}
	}
}

// Omitting any axis range or finite check must reject this test's invalid inputs.
func TestDirectionRejectsInvalid(t *testing.T) {
	h := NewMovement()
	for _, value := range []float64{1.01, -1.01, math.NaN(), math.Inf(1), math.Inf(-1)} {
		for _, direction := range []Vec3{{X: value}, {Y: value}, {Z: value}} {
			if h.Validate(Action{Type: "move", Direction: direction}) == nil {
				t.Errorf("accepted direction=%+v", direction)
			}
		}
	}
	if h.Validate(Action{Type: "teleport"}) == nil {
		t.Fatal("unknown action accepted")
	}
}

// Normalizing a sub-unit direction would incorrectly discard analog magnitude.
func TestMovementPreservesAnalogMagnitude(t *testing.T) {
	h := startedMovement(t, "a")
	if err := h.Apply("a", Action{Type: "move", Direction: Vec3{X: 0.3, Y: -0.4}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Tick(2); err != nil {
		t.Fatal(err)
	}
	p := h.Snapshot().Players[0]
	if p.Velocity != (Vec3{X: 1.5, Y: -2}) || p.Position != (Vec3{X: 3, Y: -4}) {
		t.Fatalf("analog movement=%+v", p)
	}
}

// Resetting velocity after Tick would prevent movement on the second tick.
func TestMovementPersistsAcrossTicks(t *testing.T) {
	h := startedMovement(t, "a")
	if err := h.Apply("a", Action{Type: "move", Direction: Vec3{Z: -1}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		out, err := h.Tick(0.5)
		if err != nil || !out.Changed {
			t.Fatalf("tick %d: outcome=%+v err=%v", i, out, err)
		}
	}
	if got := h.Snapshot().Players[0].Position; got != (Vec3{Z: -5}) {
		t.Fatalf("position=%+v", got)
	}
}

// A stop must zero velocity and remain visible once through Outcome.Changed.
func TestMovementStopReportsChangeOnce(t *testing.T) {
	h := startedMovement(t, "a")
	if err := h.Apply("a", Action{Type: "move", Direction: Vec3{X: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Tick(1); err != nil {
		t.Fatal(err)
	}
	if err := h.Apply("a", Action{Type: "move"}); err != nil {
		t.Fatal(err)
	}
	out, err := h.Tick(1)
	if err != nil || !out.Changed {
		t.Fatalf("stop outcome=%+v err=%v", out, err)
	}
	p := h.Snapshot().Players[0]
	if p.Position != (Vec3{X: 5}) || p.Velocity != (Vec3{}) {
		t.Fatalf("stopped player=%+v", p)
	}
	out, err = h.Tick(1)
	if err != nil || out.Changed {
		t.Fatalf("idle outcome=%+v err=%v", out, err)
	}
	if h.Snapshot().Players[0] != p {
		t.Fatal("stopped player moved")
	}
}

// Start must preserve the supplied order and space each spawn along X.
func TestMovementStartPreservesSpawnOrder(t *testing.T) {
	ids := []string{"c", "a", "b"}
	h := startedMovement(t, ids...)
	ids[0] = "changed"
	want := []Player{
		{SessionID: "c"},
		{SessionID: "a", Position: Vec3{X: 2}},
		{SessionID: "b", Position: Vec3{X: 4}},
	}
	if got := h.Snapshot().Players; !reflect.DeepEqual(got, want) {
		t.Fatalf("players=%+v want=%+v", got, want)
	}
	out, err := h.Tick(1)
	if err != nil || out.Changed {
		t.Fatalf("initial idle outcome=%+v err=%v", out, err)
	}
}

// Mutating a snapshot or Clone must not corrupt the handler or another snapshot.
func TestStateCloneIsIndependent(t *testing.T) {
	h := startedMovement(t, "a", "b")
	first := h.Snapshot()
	second := h.Snapshot()
	clone := first.Clone()
	clone.Players[0].Position.X = 99
	clone.Players[1].SessionID = "changed"
	if first.Players[0].Position.X != 0 || first.Players[1].SessionID != "b" {
		t.Fatal("clone mutated original")
	}
	first.Players[0].Velocity.Y = 99
	first.Players[1].SessionID = "changed"
	if second.Players[0].Velocity.Y != 0 || second.Players[1].SessionID != "b" {
		t.Fatal("snapshot mutated another snapshot")
	}
	if !reflect.DeepEqual(h.Snapshot(), second) {
		t.Fatal("snapshot mutated handler")
	}
}

// Invalid actions and unknown sessions must leave all player fields unchanged.
func TestMovementApplyFailurePreservesState(t *testing.T) {
	h := startedMovement(t, "a")
	if err := h.Apply("a", Action{Type: "move", Direction: Vec3{Y: 1}}); err != nil {
		t.Fatal(err)
	}
	before := h.Snapshot()
	for _, tc := range []struct {
		sessionID string
		action    Action
	}{
		{"a", Action{Type: "teleport"}},
		{"a", Action{Type: "move", Direction: Vec3{X: 2}}},
		{"a", Action{Type: "move", Direction: Vec3{Z: math.NaN()}}},
		{"missing", Action{Type: "move", Direction: Vec3{X: 1}}},
	} {
		if err := h.Apply(tc.sessionID, tc.action); err == nil {
			t.Errorf("accepted session=%q action=%+v", tc.sessionID, tc.action)
		}
		if !reflect.DeepEqual(h.Snapshot(), before) {
			t.Fatal("failed apply changed state")
		}
	}
}

// Invalid dt must neither advance position nor consume a pending velocity change.
func TestMovementTickRejectsInvalidDuration(t *testing.T) {
	h := startedMovement(t, "a")
	if err := h.Apply("a", Action{Type: "move", Direction: Vec3{X: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := h.Apply("a", Action{Type: "move"}); err != nil {
		t.Fatal(err)
	}
	before := h.Snapshot()
	for _, dt := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := h.Tick(dt); err == nil {
			t.Errorf("accepted dt=%v", dt)
		}
		if !reflect.DeepEqual(h.Snapshot(), before) {
			t.Fatal("invalid tick changed state")
		}
	}
	out, err := h.Tick(1)
	if err != nil || !out.Changed {
		t.Fatalf("pending change lost: outcome=%+v err=%v", out, err)
	}
}

// Removing a middle player must retain the remaining players and their positions.
func TestMovementOnLeaveRemovesPlayer(t *testing.T) {
	h := startedMovement(t, "a", "b", "c")
	out, err := h.OnLeave("b")
	if err != nil || !out.Changed {
		t.Fatalf("leave outcome=%+v err=%v", out, err)
	}
	want := []Player{{SessionID: "a"}, {SessionID: "c", Position: Vec3{X: 4}}}
	if got := h.Snapshot().Players; !reflect.DeepEqual(got, want) {
		t.Fatalf("remaining players=%+v", got)
	}
	before := h.Snapshot()
	if _, err := h.OnLeave("missing"); err == nil {
		t.Fatal("unknown leave accepted")
	}
	if !reflect.DeepEqual(h.Snapshot(), before) {
		t.Fatal("failed leave changed state")
	}
	if err := h.Apply("b", Action{Type: "move"}); err == nil {
		t.Fatal("departed player accepted input")
	}
}

// Missing JSON tags would expose Go field names instead of the wire contract.
func TestMovementWireJSONFields(t *testing.T) {
	encoded, err := json.Marshal(Action{Type: "move", Direction: Vec3{X: 0.5, Y: -1, Z: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"type":"move","direction":{"x":0.5,"y":-1,"z":1}}` {
		t.Fatalf("action JSON=%s", encoded)
	}
	encoded, err = json.Marshal(startedMovement(t, "a").Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"players":[{"session_id":"a","position":{"x":0,"y":0,"z":0},"velocity":{"x":0,"y":0,"z":0}}]}` {
		t.Fatalf("state JSON=%s", encoded)
	}
}

func startedMovement(t *testing.T, ids ...string) GameHandler {
	t.Helper()
	h := NewMovement()
	if err := h.Start(ids); err != nil {
		t.Fatal(err)
	}
	return h
}

// Rejecting dt must preserve a moving player's position, velocity, and pending change.
func TestMovementInvalidDurationWhileMoving(t *testing.T) {
	for _, dt := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		h := startedMovement(t, "a")
		if err := h.Apply("a", Action{Type: "move", Direction: Vec3{X: 1}}); err != nil {
			t.Fatal(err)
		}
		before := h.Snapshot()
		if before.Players[0].Velocity != (Vec3{X: 5}) {
			t.Fatal("fixture is not moving")
		}
		if _, err := h.Tick(dt); err == nil {
			t.Fatalf("accepted %v", dt)
		}
		if !reflect.DeepEqual(h.Snapshot(), before) {
			t.Fatalf("invalid dt %v changed moving state", dt)
		}
		if !h.(*movement).dirty {
			t.Fatal("invalid dt consumed pending velocity update")
		}
		out, err := h.Tick(0.2)
		if err != nil || !out.Changed || h.Snapshot().Players[0].Position != (Vec3{X: 1}) {
			t.Fatalf("valid follow-up tick: %+v %v", out, err)
		}
	}
}
