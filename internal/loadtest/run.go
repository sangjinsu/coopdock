// Package loadtest drives the existing WebSocket protocol without changing server settings.
package loadtest

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sangjinsu/coopdock/internal/game"
	"github.com/sangjinsu/coopdock/internal/room"
)

// Config has no implicit zero-value defaults. Start with DefaultConfig.
type Config struct {
	URL                                                                  string
	GameIDs                                                              []string
	Rooms, Players, InputHz                                              int
	Duration, SetupTimeout, DrainTimeout, CleanupTimeout, RequestTimeout time.Duration
	MaxPending, SampleLimit, CaptureLimit                                int
}

func DefaultConfig() Config {
	return Config{URL: "ws://127.0.0.1:8080/ws", GameIDs: []string{"default"}, Rooms: 10, Players: 6, InputHz: 20, Duration: 10 * time.Second,
		SetupTimeout: 30 * time.Second, DrainTimeout: 10 * time.Second, CleanupTimeout: 10 * time.Second, RequestTimeout: 5 * time.Second,
		MaxPending: 256, SampleLimit: 10000, CaptureLimit: 128}
}

func (c Config) Validate() error {
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Hostname() == "" || u.User != nil || strings.ContainsAny(c.URL, "?#") {
		return errors.New("invalid WebSocket base URL (ws/wss, no credentials/query/fragment)")
	}
	if len(c.GameIDs) < 1 || len(c.GameIDs) > 32 {
		return errors.New("game-ids requires 1..32 unique valid game IDs")
	}
	seen := make(map[string]bool, len(c.GameIDs))
	for _, id := range c.GameIDs {
		if !room.ValidGameID(id) || seen[id] {
			return errors.New("game-ids requires unique valid game IDs")
		}
		seen[id] = true
	}
	if c.Rooms < 1 || c.Rooms > 512 || c.Players < 1 || c.Players > 6 || c.Rooms*c.Players > 3072 {
		return errors.New("rooms must be 1..512 and players 1..6 (at most 3072 clients)")
	}
	if c.InputHz < 1 || c.InputHz > 120 || c.Duration <= 0 || c.Duration > time.Hour {
		return errors.New("input-hz must be 1..120 and duration must be positive and at most 1h")
	}
	for _, v := range []time.Duration{c.SetupTimeout, c.DrainTimeout, c.CleanupTimeout, c.RequestTimeout} {
		if v <= 0 || v > 5*time.Minute {
			return errors.New("timeouts must be positive and at most 5m")
		}
	}
	if c.MaxPending < 1 || c.MaxPending > 4096 || c.MaxPending*c.Rooms*c.Players > 1048576 {
		return errors.New("max-pending must be 1..4096; combined pending capacity must not exceed 1048576")
	}
	if c.SampleLimit < 1 || c.SampleLimit > 100000 || c.SampleLimit*c.Rooms > 2000000 {
		return errors.New("sample-limit must be 1..100000 and rooms * sample-limit must not exceed 2000000")
	}
	if c.CaptureLimit < 2 || c.CaptureLimit > 4096 || c.CaptureLimit*c.Rooms > 65536 {
		return errors.New("capture-limit must be 2..4096 and rooms * capture-limit must not exceed 65536")
	}
	return nil
}

// Conditions preserves the requested games separately from ActiveGameIDs.
type Conditions struct {
	URL            string   `json:"url"`
	GameIDs        []string `json:"game_ids"`
	Rooms          int      `json:"rooms"`
	PlayersPerRoom int      `json:"players_per_room"`
	Duration       string   `json:"duration"`
	InputHz        int      `json:"input_hz"`
	SetupTimeout   string   `json:"setup_timeout"`
	DrainTimeout   string   `json:"drain_timeout"`
	CleanupTimeout string   `json:"cleanup_timeout"`
	RequestTimeout string   `json:"request_timeout"`
	MaxPending     int      `json:"max_pending_per_client"`
	SampleLimit    int      `json:"sample_limit_per_distribution"`
	CaptureLimit   int      `json:"capture_limit_per_room"`
	ResourceLimits string   `json:"resource_limits"`
}

func conditions(c Config) Conditions {
	endpoint := c.URL
	if parsed, err := url.Parse(endpoint); err != nil {
		endpoint = ""
	} else {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		endpoint = parsed.String()
	}
	return Conditions{endpoint, append([]string(nil), c.GameIDs...), c.Rooms, c.Players, c.Duration.String(), c.InputHz, c.SetupTimeout.String(), c.DrainTimeout.String(), c.CleanupTimeout.String(), c.RequestTimeout.String(), c.MaxPending, c.SampleLimit, c.CaptureLimit,
		"rooms<=512; clients<=3072; input_hz<=120; duration<=1h; timeouts<=5m; pending/client<=4096, combined<=1048576; samples/distribution<=100000, rooms*samples<=2000000; captures/room<=4096, rooms*captures<=65536"}
}

type Metrics struct {
	InputsSent         uint64       `json:"inputs_sent"`
	InputACKs          uint64       `json:"input_acks"`
	UnconfirmedInputs  uint64       `json:"unconfirmed_inputs"`
	ZeroSent           uint64       `json:"zero_inputs_sent"`
	ZeroACKs           uint64       `json:"zero_input_acks"`
	Snapshots          uint64       `json:"snapshots_received"`
	SequenceGaps       uint64       `json:"snapshot_sequence_gaps"`
	ServerErrors       uint64       `json:"server_errors"`
	Timeouts           uint64       `json:"timeouts"`
	Disconnects        uint64       `json:"disconnects"`
	PendingOverflows   uint64       `json:"pending_overflows"`
	InputsPerSecond    float64      `json:"inputs_per_second"`
	ACKsPerSecond      float64      `json:"input_acks_per_second"`
	SnapshotsPerSecond float64      `json:"snapshots_per_second"`
	ACKRTT             Distribution `json:"input_ack_rtt"`
	SnapshotInterval   Distribution `json:"snapshot_arrival_interval"`
}

type CleanupReport struct {
	Attempted         bool     `json:"attempted"`
	LeaveACKs         int      `json:"leave_acks"`
	RoomsRemoved      int      `json:"rooms_removed"`
	ConnectionsClosed int      `json:"connections_closed"`
	ReadersStopped    int      `json:"readers_stopped"`
	PendingRequests   int      `json:"pending_requests"`
	Errors            []string `json:"errors"`
}

type RoomReport struct {
	GameID            string  `json:"game_id"`
	RoomID            string  `json:"room_id"`
	RoomNumber        string  `json:"room_number"`
	MatchID           string  `json:"match_id"`
	Players           int     `json:"players"`
	CommonCaptures    uint64  `json:"common_periodic_captures"`
	CaptureEvictions  uint64  `json:"capture_window_evictions"`
	PlayersMoved      int     `json:"players_moved"`
	PlayersStopped    int     `json:"players_stopped"`
	TickHz            float64 `json:"observed_tick_hz"`
	TickDelta         uint64  `json:"observed_tick_delta"`
	TickWindowSeconds float64 `json:"tick_observation_seconds"`
	Metrics           Metrics `json:"metrics"`
}

type Report struct {
	Success            bool          `json:"success"`
	StartedAt          time.Time     `json:"started_at"`
	Conditions         Conditions    `json:"conditions"`
	ActiveGameIDs      []string      `json:"active_game_ids"`
	SetupSeconds       float64       `json:"setup_seconds"`
	MeasurementSeconds float64       `json:"measurement_seconds"`
	Total              Metrics       `json:"total"`
	Rooms              []RoomReport  `json:"rooms"`
	Cleanup            CleanupReport `json:"cleanup"`
	Errors             []string      `json:"errors"`
}

type metricState struct {
	values         Metrics
	rtt, intervals *samples
}

func newMetricState(limit int) metricState {
	return metricState{rtt: newSamples(limit), intervals: newSamples(limit)}
}
func (m *metricState) report(seconds float64) Metrics {
	v := m.values
	v.UnconfirmedInputs = v.InputsSent - v.InputACKs
	v.ACKRTT, v.SnapshotInterval = m.rtt.summary(), m.intervals.summary()
	if seconds > 0 {
		v.InputsPerSecond = float64(v.InputsSent) / seconds
		v.ACKsPerSecond = float64(v.InputACKs) / seconds
		v.SnapshotsPerSecond = float64(v.Snapshots) / seconds
	}
	return v
}

type roomRun struct {
	index    int
	binding  binding
	number   string
	clients  []*client
	captures *captureWindow
	common   uint64
	origins  map[string]game.Vec3
	moved    map[string]bool
	metrics  metricState
}

type runner struct {
	mu                 sync.Mutex
	config             Config
	ctx                context.Context
	cancel             context.CancelFunc
	rooms              []*roomRun
	clients            []*client
	total              metricState
	start, end         time.Time
	measurementSeconds float64
	cleaning, closing  bool
	failure            error
	errors             []string
	failed             chan struct{}
	changed            chan struct{}
}

func (r *runner) notifyLocked() {
	select {
	case r.changed <- struct{}{}:
	default:
	}
}
func (r *runner) failLocked(err error) {
	if err == nil {
		return
	}
	if len(r.errors) < 32 {
		r.errors = append(r.errors, err.Error())
	}
	if r.failure == nil {
		r.failure = err
		close(r.failed)
	}
	r.notifyLocked()
}
func (r *runner) fail(err error) { r.mu.Lock(); r.failLocked(err); r.mu.Unlock() }
func (r *runner) wait(ctx context.Context, ready func() bool) error {
	for {
		r.mu.Lock()
		if r.failure != nil {
			err := r.failure
			r.mu.Unlock()
			return err
		}
		ok := ready()
		r.mu.Unlock()
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				r.mu.Lock()
				r.total.values.Timeouts++
				for _, rr := range r.rooms {
					timedOut := false
					for _, cl := range rr.clients {
						if !cl.ready || len(cl.pending) > 0 || cl.zeroSequence > 0 && !cl.stable {
							timedOut = true
						}
					}
					if timedOut {
						rr.metrics.values.Timeouts++
					}
				}
				r.mu.Unlock()
			}
			return ctx.Err()
		case <-r.failed:
		case <-r.changed:
		}
	}
}

// Run keeps readers alive through measurement, ACK drain, zero verification, and cleanup.
func Run(ctx context.Context, c Config) (report Report, result error) {
	report.StartedAt = time.Now().UTC()
	report.Conditions = conditions(c)
	report.Errors = []string{}
	report.ActiveGameIDs = []string{}
	report.Rooms = []RoomReport{}
	if ctx == nil {
		err := errors.New("context must not be nil")
		report.Errors = append(report.Errors, err.Error())
		return report, err
	}
	if err := c.Validate(); err != nil {
		report.Errors = append(report.Errors, err.Error())
		return report, err
	}
	lifetime, cancel := context.WithCancel(context.Background())
	r := &runner{config: c, ctx: lifetime, cancel: cancel, total: newMetricState(c.SampleLimit), failed: make(chan struct{}), changed: make(chan struct{}, 1)}
	for i := 0; i < c.Rooms; i++ {
		r.rooms = append(r.rooms, &roomRun{index: i, binding: binding{gameID: c.GameIDs[i%len(c.GameIDs)]}, captures: newCaptureWindow(c.CaptureLimit, c.Players), origins: make(map[string]game.Vec3), moved: make(map[string]bool), metrics: newMetricState(c.SampleLimit)})
	}
	defer func() {
		if result != nil {
			r.fail(result)
		}
		report.Cleanup = r.cleanup()
		r.mu.Lock()
		defer r.mu.Unlock()
		report.MeasurementSeconds = r.measurementSeconds
		report.Total = r.total.report(r.measurementSeconds)
		active := map[string]bool{}
		for _, rr := range r.rooms {
			v := RoomReport{GameID: rr.binding.gameID, RoomID: rr.binding.roomID, RoomNumber: rr.number, MatchID: rr.binding.matchID, Players: len(rr.clients), CommonCaptures: rr.common, CaptureEvictions: rr.captures.evictions, PlayersMoved: len(rr.moved), Metrics: rr.metrics.report(r.measurementSeconds)}
			if rr.binding.matchID != "" {
				active[rr.binding.gameID] = true
			}
			if len(rr.clients) > 0 {
				w := rr.clients[0].window
				v.TickHz = w.tickHz()
				v.TickDelta = w.tickDelta()
				v.TickWindowSeconds = w.tickSeconds()
			}
			for _, cl := range rr.clients {
				if cl.stable {
					v.PlayersStopped++
				}
			}
			report.Rooms = append(report.Rooms, v)
		}
		for id := range active {
			report.ActiveGameIDs = append(report.ActiveGameIDs, id)
		}
		sort.Strings(report.ActiveGameIDs)
		report.Errors = append(report.Errors, r.errors...)
		if len(report.Cleanup.Errors) > 0 && result == nil {
			result = errors.New("cleanup failed")
			report.Errors = append(report.Errors, result.Error())
		}
		if r.failure != nil && result == nil {
			result = r.failure
		}
		report.Success = result == nil
	}()
	setupBegan := time.Now()
	setupCtx, stopSetup := context.WithTimeout(ctx, c.SetupTimeout)
	var setup sync.WaitGroup
	for _, rr := range r.rooms {
		setup.Add(1)
		go func(rr *roomRun) {
			defer setup.Done()
			if err := r.setup(setupCtx, rr); err != nil {
				r.fail(fmt.Errorf("room %d setup: %w", rr.index+1, err))
				stopSetup()
			}
		}(rr)
	}
	setup.Wait()
	stopSetup()
	report.SetupSeconds = time.Since(setupBegan).Seconds()
	r.mu.Lock()
	setupError := r.failure
	r.mu.Unlock()
	if setupError != nil {
		return report, setupError
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := r.measure(ctx); err != nil {
		return report, err
	}
	drainCtx, stopDrain := context.WithTimeout(ctx, c.DrainTimeout)
	defer stopDrain()
	if err := r.wait(drainCtx, func() bool { return r.inputPendingLocked() == 0 }); err != nil {
		return report, fmt.Errorf("input ACK drain: %w", err)
	}
	r.mu.Lock()
	for _, rr := range r.rooms {
		if rr.common == 0 {
			r.mu.Unlock()
			return report, errors.New("no shared periodic measurement capture")
		}
		if len(rr.moved) != c.Players {
			r.mu.Unlock()
			return report, errors.New("movement was not observed for every player")
		}
		for _, cl := range rr.clients {
			if cl.measuredSent == 0 {
				r.mu.Unlock()
				return report, errors.New("player sent no measurement inputs")
			}
		}
	}
	r.mu.Unlock()
	for _, cl := range r.clients {
		if err := cl.sendAction(drainCtx, game.Vec3{}, true); err != nil {
			return report, err
		}
	}
	if err := r.wait(drainCtx, func() bool {
		for _, cl := range r.clients {
			if !cl.zeroAck || !cl.stable {
				return false
			}
		}
		return r.inputPendingLocked() == 0
	}); err != nil {
		return report, fmt.Errorf("final zero ACK/snapshot/position verification: %w", err)
	}
	return report, nil
}

func (r *runner) setup(ctx context.Context, rr *roomRun) error {
	for i := 0; i < r.config.Players; i++ {
		if _, err := r.connect(ctx, rr, i); err != nil {
			return err
		}
	}
	r.mu.Lock()
	for _, cl := range rr.clients {
		rr.binding.members = append(rr.binding.members, cl.sessionID)
	}
	r.mu.Unlock()
	if _, err := rr.clients[0].request(ctx, "room.create", map[string]any{"capacity": r.config.Players, "visibility": "public"}); err != nil {
		return err
	}
	for _, cl := range rr.clients[1:] {
		if _, err := cl.request(ctx, "room.join", map[string]any{"room_number": rr.number}); err != nil {
			return err
		}
	}
	if _, err := rr.clients[0].request(ctx, "room.start", struct{}{}); err != nil {
		return err
	}
	return r.wait(ctx, func() bool {
		for _, cl := range rr.clients {
			if !cl.ready {
				return false
			}
		}
		return true
	})
}

func (r *runner) inputPendingLocked() int {
	count := 0
	for _, cl := range r.clients {
		for _, p := range cl.pending {
			if p.op == "game.action" {
				count++
			}
		}
	}
	return count
}

func (r *runner) measure(ctx context.Context) error {
	r.mu.Lock()
	r.start = time.Now()
	r.end = r.start.Add(r.config.Duration)
	for _, cl := range r.clients {
		cl.window = measurementWindow{start: r.start, end: r.end, intervals: newSamples(r.config.SampleLimit)}
	}
	end := r.end
	r.mu.Unlock()
	barrier := make(chan struct{})
	var workers sync.WaitGroup
	for _, cl := range r.clients {
		workers.Add(1)
		go func(cl *client) {
			defer workers.Done()
			<-barrier
			ticker := time.NewTicker(time.Second / time.Duration(r.config.InputHz))
			defer ticker.Stop()
			timer := time.NewTimer(time.Until(end))
			defer timer.Stop()
			directions := []game.Vec3{{Z: 1}, {X: 1}, {Y: 1}, {Z: -1}, {X: -1}, {Y: -1}}
			for {
				if time.Now().Before(end) && ctx.Err() == nil {
					if err := cl.sendAction(r.ctx, directions[cl.index%len(directions)], false); err != nil {
						r.fail(err)
						return
					}
				} else {
					return
				}
				select {
				case <-ticker.C:
				case <-timer.C:
					return
				case <-ctx.Done():
					return
				case <-r.failed:
					return
				}
			}
		}(cl)
	}
	close(barrier)
	workers.Wait()
	now := time.Now()
	if now.After(end) {
		now = end
	}
	r.mu.Lock()
	r.measurementSeconds = now.Sub(r.start).Seconds()
	err := r.failure
	r.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (r *runner) cleanup() CleanupReport {
	result := CleanupReport{Attempted: true, Errors: []string{}}
	r.mu.Lock()
	r.cleaning = true
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), r.config.CleanupTimeout)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, rr := range r.rooms {
		wg.Add(1)
		go func(rr *roomRun) {
			defer wg.Done()
			for _, cl := range rr.clients {
				r.mu.Lock()
				joined := cl.joined
				r.mu.Unlock()
				if !joined {
					continue
				}
				if _, err := cl.request(ctx, "room.leave", struct{}{}); err != nil {
					mu.Lock()
					result.Errors = append(result.Errors, fmt.Sprintf("room %d leave: %v", rr.index+1, err))
					mu.Unlock()
				} else {
					mu.Lock()
					result.LeaveACKs++
					mu.Unlock()
				}
			}
			if rr.binding.roomID == "" || len(rr.clients) == 0 {
				return
			}
			err := rr.clients[0].probeRoomAbsent(ctx)
			mu.Lock()
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("room %d removal: %v", rr.index+1, err))
			} else {
				result.RoomsRemoved++
			}
			mu.Unlock()
		}(rr)
	}
	wg.Wait()
	r.mu.Lock()
	r.closing = true
	r.mu.Unlock()
	r.cancel()
	for _, cl := range r.clients {
		cl.conn.CloseNow()
		result.ConnectionsClosed++
	}
	for _, cl := range r.clients {
		<-cl.done
		result.ReadersStopped++
	}
	r.mu.Lock()
	for _, cl := range r.clients {
		result.PendingRequests += len(cl.pending)
	}
	r.mu.Unlock()
	return result
}
