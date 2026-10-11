package game

const MoveSpeed = 5.0
const SpawnSpacing = 2.0

type Vec3 struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

type Action struct {
	Type      string `json:"type"`
	Direction Vec3   `json:"direction"`
}

type Player struct {
	SessionID string `json:"session_id"`
	Position  Vec3   `json:"position"`
	Velocity  Vec3   `json:"velocity"`
}

type State struct {
	Players []Player `json:"players"`
}

func (s State) Clone() State {
	return State{Players: append([]Player{}, s.Players...)}
}

type Event struct {
	Type      string
	SessionID string
}

type Outcome struct {
	Changed   bool
	Events    []Event
	EndReason string
}

type GameHandler interface {
	Start(sessionIDs []string) error
	Validate(action Action) error
	Apply(sessionID string, action Action) error
	Tick(dtSeconds float64) (Outcome, error)
	OnLeave(sessionID string) (Outcome, error)
	Snapshot() State
}
