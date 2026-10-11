package game

import (
	"errors"
	"math"
)

type movement struct {
	state State
	dirty bool
}

func NewMovement() GameHandler {
	return &movement{}
}

func (m *movement) Start(sessionIDs []string) error {
	m.state.Players = make([]Player, len(sessionIDs))
	for i, sessionID := range sessionIDs {
		m.state.Players[i] = Player{
			SessionID: sessionID,
			Position:  Vec3{X: float64(i) * SpawnSpacing},
		}
	}
	m.dirty = false
	return nil
}

func (m *movement) Validate(action Action) error {
	if action.Type != "move" {
		return errors.New("invalid action type")
	}
	for _, component := range []float64{action.Direction.X, action.Direction.Y, action.Direction.Z} {
		if math.IsNaN(component) || math.IsInf(component, 0) || component < -1 || component > 1 {
			return errors.New("invalid direction")
		}
	}
	return nil
}

func (m *movement) Apply(sessionID string, action Action) error {
	if err := m.Validate(action); err != nil {
		return err
	}
	direction := action.Direction
	n := math.Hypot(math.Hypot(direction.X, direction.Y), direction.Z)
	if n > 1 {
		direction.X /= n
		direction.Y /= n
		direction.Z /= n
	}
	velocity := Vec3{X: direction.X * MoveSpeed, Y: direction.Y * MoveSpeed, Z: direction.Z * MoveSpeed}
	for i := range m.state.Players {
		p := &m.state.Players[i]
		if p.SessionID == sessionID {
			if p.Velocity != velocity {
				p.Velocity = velocity
				m.dirty = true
			}
			return nil
		}
	}
	return errors.New("unknown session")
}

func (m *movement) Tick(dtSeconds float64) (Outcome, error) {
	if dtSeconds <= 0 || math.IsNaN(dtSeconds) || math.IsInf(dtSeconds, 0) {
		return Outcome{}, errors.New("invalid tick duration")
	}
	out := Outcome{Changed: m.dirty}
	for i := range m.state.Players {
		p := &m.state.Players[i]
		if p.Velocity != (Vec3{}) {
			p.Position.X += p.Velocity.X * dtSeconds
			p.Position.Y += p.Velocity.Y * dtSeconds
			p.Position.Z += p.Velocity.Z * dtSeconds
			out.Changed = true
		}
	}
	m.dirty = false
	return out, nil
}

func (m *movement) OnLeave(sessionID string) (Outcome, error) {
	for i, p := range m.state.Players {
		if p.SessionID == sessionID {
			m.state.Players = append(m.state.Players[:i], m.state.Players[i+1:]...)
			return Outcome{Changed: true}, nil
		}
	}
	return Outcome{}, errors.New("unknown session")
}

func (m *movement) Snapshot() State {
	// Return a detached copy so callers cannot mutate the handler.
	return m.state.Clone()
}
