package room

import "time"

type CreateOptions struct {
	Capacity   int
	Visibility string
	Password   *string
}
type JoinOptions struct {
	RoomNumber string
	Password   *string
}
type Member struct {
	SessionID string `json:"session_id"`
}
type Snapshot struct {
	RoomID                string            `json:"room_id"`
	RoomNumber            string            `json:"room_number"`
	Visibility            string            `json:"visibility"`
	Capacity              int               `json:"capacity"`
	MatchID               *string           `json:"match_id"`
	Phase                 string            `json:"phase"`
	HostID                string            `json:"host_id"`
	Members               []Member          `json:"members"`
	Revision              uint64            `json:"revision"`
	GameState             any               `json:"game_state"`
	ServerTick            uint64            `json:"server_tick"`
	SnapshotSeq           uint64            `json:"snapshot_seq"`
	LastProcessedInputSeq map[string]uint64 `json:"last_processed_input_seq"`
}

func (v Snapshot) clone() Snapshot {
	v.Members = append([]Member{}, v.Members...)
	inputs := make(map[string]uint64, len(v.LastProcessedInputSeq))
	for k, n := range v.LastProcessedInputSeq {
		inputs[k] = n
	}
	v.LastProcessedInputSeq = inputs
	if v.MatchID != nil {
		s := *v.MatchID
		v.MatchID = &s
	}
	return v
}

type Config struct {
	MaxRooms, MaxSessions, NumberRetries                                  int
	ActorQueue, CleanupQueue, EventQueue, ListLimit                       int
	HashWorkers, HashQueue                                                int
	FailureTTL                                                            time.Duration
	IPFailureLimit, SessionFailureLimit, RoomFailureLimit, MaxFailureKeys int
	RequestTimeout, ShutdownTimeout                                       time.Duration
	ArgonMemory, ArgonTime                                                uint32
	ArgonThreads                                                          uint8
	SaltBytes, HashBytes                                                  uint32
	// Private hooks allow deterministic interleaving tests without changing public APIs.
	hash         func([]byte, []byte) []byte
	number       func() (string, error)
	now          func() time.Time
	beforeSubmit func()
	afterCommit  func()
}

func DefaultConfig() Config {
	return Config{MaxRooms: 128, MaxSessions: 768, NumberRetries: 64, ActorQueue: 64, CleanupQueue: 6, EventQueue: 64, ListLimit: 100, HashWorkers: 2, HashQueue: 8, FailureTTL: time.Minute, IPFailureLimit: 20, SessionFailureLimit: 5, RoomFailureLimit: 30, MaxFailureKeys: 4096, RequestTimeout: 5 * time.Second, ShutdownTimeout: 10 * time.Second, ArgonMemory: 19456, ArgonTime: 2, ArgonThreads: 1, SaltBytes: 16, HashBytes: 32}
}
