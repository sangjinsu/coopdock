package admin

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"roomserver/internal/observability"
	"roomserver/internal/room"
	"sort"
	"strconv"
	"strings"
	"time"
)

type handler struct {
	enabled                     bool
	usernameDigest, tokenDigest [sha256.Size]byte
	collector                   *observability.Collector
	now                         func() time.Time
}

func New(c Config, collector *observability.Collector) (http.Handler, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	if c.Enabled() && collector == nil {
		return nil, errors.New("Invalid admin collector.")
	}
	return &handler{enabled: c.Enabled(), usernameDigest: sha256.Sum256([]byte("viewer")), tokenDigest: sha256.Sum256([]byte(c.ViewerToken)), collector: collector, now: time.Now}, nil
}

const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'"

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	if !h.enabled {
		writeError(w, r, http.StatusNotFound)
		return
	}
	if !h.authenticated(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="coopdock-admin", charset="UTF-8"`)
		writeError(w, r, http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodPost && isOperatorPath(r.URL.Path) {
		writeError(w, r, http.StatusForbidden)
		return
	}
	asset, static := webAssets[r.URL.Path]
	api := r.URL.Path == "/admin/api/status" || r.URL.Path == "/admin/api/rooms"
	if !static && !api {
		writeError(w, r, http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, r, http.StatusMethodNotAllowed)
		return
	}
	if static {
		writeBody(w, r, http.StatusOK, asset.contentType, asset.body)
		return
	}
	var response any
	if r.URL.Path == "/admin/api/rooms" {
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			writeError(w, r, http.StatusBadRequest)
			return
		}
		q, err := parseRoomsQuery(values)
		if err != nil {
			writeError(w, r, http.StatusBadRequest)
			return
		}
		response = roomsResponse(h.collector.Load(), q, h.now())
	} else {
		response = statusResponse(h.collector.Load(), h.now())
	}
	body, err := json.Marshal(response)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError)
		return
	}
	writeBody(w, r, http.StatusOK, "application/json; charset=utf-8", append(body, '\n'))
}
func isOperatorPath(path string) bool {
	if path == "/admin/api/server/drain" {
		return true
	}
	parts := strings.Split(path, "/")
	return len(parts) == 6 && parts[1] == "admin" && parts[2] == "api" && parts[3] == "rooms" && parts[4] != "" && parts[4] != "." && parts[4] != ".." && (parts[5] == "kick" || parts[5] == "close")
}
func writeError(w http.ResponseWriter, r *http.Request, status int) {
	writeBody(w, r, status, "text/plain; charset=utf-8", []byte(http.StatusText(status)+"\n"))
}
func writeBody(w http.ResponseWriter, r *http.Request, status int, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

type StatusResponse struct {
	ServerInstanceID         string              `json:"server_instance_id"`
	State                    observability.State `json:"state"`
	Ready                    bool                `json:"ready"`
	ActiveConnections        int                 `json:"active_connections"`
	RoomMemberCount          int                 `json:"room_member_count"`
	RoomCount                int                 `json:"room_count"`
	WaitingRoomCount         int                 `json:"waiting_room_count"`
	PlayingRoomCount         int                 `json:"playing_room_count"`
	StartedAt                string              `json:"started_at"`
	UptimeSeconds            int64               `json:"uptime_seconds"`
	HeapAllocBytes           uint64              `json:"heap_alloc_bytes"`
	Goroutines               int                 `json:"goroutines"`
	CPUPercent               *float64            `json:"cpu_percent"`
	RTTMillis                *float64            `json:"rtt_ms"`
	ClientDisplayDelayMillis *float64            `json:"client_display_delay_ms"`
	UpdatedAt                string              `json:"updated_at"`
	AgeMillis                int64               `json:"age_ms"`
	Stale                    bool                `json:"stale"`
	Generation               uint64              `json:"generation"`
}
type RoomResponse struct {
	RoomID      string  `json:"room_id"`
	RoomNumber  string  `json:"room_number"`
	Visibility  string  `json:"visibility"`
	Phase       string  `json:"phase"`
	Capacity    int     `json:"capacity"`
	MemberCount int     `json:"member_count"`
	HostID      string  `json:"host_id"`
	MatchID     *string `json:"match_id"`
	UpdatedAt   string  `json:"updated_at"`
	AgeMillis   int64   `json:"age_ms"`
	Stale       bool    `json:"stale"`
}
type RoomsResponse struct {
	Total        int            `json:"total"`
	MatchedTotal int            `json:"matched_total"`
	Offset       int            `json:"offset"`
	Limit        int            `json:"limit"`
	Rooms        []RoomResponse `json:"rooms"`
	UpdatedAt    string         `json:"updated_at"`
	AgeMillis    int64          `json:"age_ms"`
	Stale        bool           `json:"stale"`
	Generation   uint64         `json:"generation"`
}
type roomsQuery struct {
	Limit, Offset                   int
	NumberPrefix, Phase, Visibility string
}

func parseRoomsQuery(values url.Values) (roomsQuery, error) {
	q := roomsQuery{Limit: 100, Phase: "all", Visibility: "all"}
	invalid := errors.New("Invalid rooms query.")
	for key, all := range values {
		if len(all) != 1 {
			return roomsQuery{}, invalid
		}
		value := all[0]
		switch key {
		case "limit", "offset":
			if !decimal(value) {
				return roomsQuery{}, invalid
			}
			n, err := strconv.Atoi(value)
			if err != nil {
				return roomsQuery{}, invalid
			}
			if key == "limit" {
				if n < 1 || n > 100 {
					return roomsQuery{}, invalid
				}
				q.Limit = n
			} else {
				if n < 0 || n > 4096 {
					return roomsQuery{}, invalid
				}
				q.Offset = n
			}
		case "number_prefix":
			if len(value) > 6 || value != "" && !decimal(value) {
				return roomsQuery{}, invalid
			}
			q.NumberPrefix = value
		case "phase":
			if value != "all" && value != "WAITING" && value != "PLAYING" {
				return roomsQuery{}, invalid
			}
			q.Phase = value
		case "visibility":
			if value != "all" && value != "public" && value != "private" {
				return roomsQuery{}, invalid
			}
			q.Visibility = value
		default:
			return roomsQuery{}, invalid
		}
	}
	return q, nil
}
func decimal(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}
func timestamp(v time.Time) string { return v.UTC().Format(time.RFC3339) }
func statusResponse(v observability.Snapshot, now time.Time) StatusResponse {
	age := observability.AgeMillis(now, v.UpdatedAt)
	uptime := int64(now.Sub(v.StartedAt) / time.Second)
	if uptime < 0 {
		uptime = 0
	}
	return StatusResponse{ServerInstanceID: v.ServerInstanceID, State: v.State, Ready: v.Ready, ActiveConnections: v.ActiveConnections, RoomMemberCount: v.RoomMemberCount, RoomCount: v.RoomCount, WaitingRoomCount: v.WaitingRoomCount, PlayingRoomCount: v.PlayingRoomCount, StartedAt: timestamp(v.StartedAt), UptimeSeconds: uptime, HeapAllocBytes: v.HeapAllocBytes, Goroutines: v.Goroutines, UpdatedAt: timestamp(v.UpdatedAt), AgeMillis: age, Stale: age >= observability.StaleAfter.Milliseconds(), Generation: v.Generation}
}
func roomsResponse(v observability.Snapshot, q roomsQuery, now time.Time) RoomsResponse {
	rows := v.Rooms()
	total := len(rows)
	filtered := rows[:0]
	for _, row := range rows {
		if strings.HasPrefix(row.RoomNumber, q.NumberPrefix) && (q.Phase == "all" || q.Phase == row.Phase) && (q.Visibility == "all" || q.Visibility == row.Visibility) {
			filtered = append(filtered, row)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].RoomNumber == filtered[j].RoomNumber {
			return filtered[i].RoomID < filtered[j].RoomID
		}
		return filtered[i].RoomNumber < filtered[j].RoomNumber
	})
	age := observability.AgeMillis(now, v.UpdatedAt)
	response := RoomsResponse{Total: total, MatchedTotal: len(filtered), Offset: q.Offset, Limit: q.Limit, Rooms: []RoomResponse{}, UpdatedAt: timestamp(v.UpdatedAt), AgeMillis: age, Stale: age >= observability.StaleAfter.Milliseconds(), Generation: v.Generation}
	if q.Offset >= len(filtered) {
		return response
	}
	for _, row := range filtered[q.Offset:min(q.Offset+q.Limit, len(filtered))] {
		age := observability.AgeMillis(now, row.UpdatedAt)
		response.Rooms = append(response.Rooms, RoomResponse{RoomID: row.RoomID, RoomNumber: row.RoomNumber, Visibility: row.Visibility, Phase: row.Phase, Capacity: row.Capacity, MemberCount: row.MemberCount, HostID: row.HostID, MatchID: row.MatchID, UpdatedAt: timestamp(row.UpdatedAt), AgeMillis: age, Stale: age >= observability.StaleAfter.Milliseconds()})
	}
	return response
}

var _ func(*observability.Collector) observability.Snapshot = (*observability.Collector).Load
var _ func(observability.Snapshot) []room.OperationsSummary = observability.Snapshot.Rooms
var _ func(time.Time, time.Time) int64 = observability.AgeMillis
