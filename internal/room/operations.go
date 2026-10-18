package room

import (
	"sort"
	"time"
)

type OperationsSummary struct {
	GameID                                string
	RoomID, RoomNumber, Visibility, Phase string
	Capacity, MemberCount                 int
	HostID                                string
	MatchID                               *string
	UpdatedAt                             time.Time
}

type OperationsSnapshot struct {
	Ready bool
	Rooms []OperationsSummary
}

func (m *Manager) OperationsSnapshot() OperationsSnapshot {
	m.mu.Lock()
	v := OperationsSnapshot{Ready: !m.closing, Rooms: []OperationsSummary{}}
	refs := make([]*actor, 0, len(m.rooms))
	for _, r := range m.rooms {
		if r.published {
			refs = append(refs, r)
		}
	}
	m.mu.Unlock()
	seen := make(map[string]struct{}, len(refs))
	for _, r := range refs {
		summary := r.operations.Load()
		if summary == nil {
			continue
		}
		if _, exists := seen[summary.RoomID]; exists {
			continue
		}
		seen[summary.RoomID] = struct{}{}
		v.Rooms = append(v.Rooms, summary.clone())
	}
	sort.Slice(v.Rooms, func(i, j int) bool {
		a, b := v.Rooms[i], v.Rooms[j]
		if a.GameID != b.GameID {
			return a.GameID < b.GameID
		}
		if a.RoomNumber != b.RoomNumber {
			return a.RoomNumber < b.RoomNumber
		}
		return a.RoomID < b.RoomID
	})
	return v
}

// publishOperations reads actor-owned primitives without capturing game state.
func (r *actor) publishOperations(now time.Time) {
	v := OperationsSummary{GameID: r.gameID, RoomID: r.id, RoomNumber: r.number,
		Visibility: r.visibility, Phase: "WAITING", Capacity: r.capacity,
		MemberCount: len(r.members), UpdatedAt: now}
	if len(r.members) > 0 {
		v.HostID = r.members[0].session.id
	}
	if r.matchID != "" {
		id := r.matchID
		v.MatchID = &id
		v.Phase = "PLAYING"
	}
	r.operations.Store(&v)
}

func (v OperationsSummary) clone() OperationsSummary {
	if v.MatchID != nil {
		id := *v.MatchID
		v.MatchID = &id
	}
	return v
}
