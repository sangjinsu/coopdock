package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"github.com/sangjinsu/coopdock/internal/observability"
	"github.com/sangjinsu/coopdock/internal/room"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testCollector(t *testing.T, rooms []room.OperationsSummary) *observability.Collector {
	t.Helper()
	c, e := observability.New(context.Background(), observability.Source{Rooms: func() room.OperationsSnapshot { return room.OperationsSnapshot{Ready: true, Rooms: rooms} }, Transport: func() (int, bool) { return 3, false }}, observability.Config{Interval: time.Hour})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if e := c.Close(ctx); e != nil {
			t.Error(e)
		}
	})
	return c
}
func adminRequest(h http.Handler, method, path, auth string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	switch auth {
	case "valid":
		r.SetBasicAuth("viewer", testToken)
	case "wrong-user":
		r.SetBasicAuth("operator", testToken)
	case "wrong-token":
		r.SetBasicAuth("viewer", "wrong")
	default:
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func assertSecurity(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	for k, v := range map[string]string{"Cache-Control": "no-store", "Content-Security-Policy": "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'", "X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY"} {
		if w.Header().Get(k) != v {
			t.Errorf("%s missing", k)
		}
	}
}
func TestAdminAuthAndRoutes(t *testing.T) {
	c := testCollector(t, nil)
	active, e := New(Config{ViewerToken: testToken}, c)
	if e != nil {
		t.Fatal(e)
	}
	disabled, e := New(Config{}, nil)
	if e != nil {
		t.Fatal(e)
	}
	paths := []string{"/admin", "/admin/", "/admin/assets/dashboard.css", "/admin/assets/dashboard.js", "/admin/api/status", "/admin/api/rooms", "/admin/api/rooms/id", "/admin/api/commands", "/admin//api/status", "/admin/./api/status", "/admin/assets/../index.html"}
	for _, p := range paths {
		for _, method := range []string{"GET", "HEAD", "POST"} {
			w := adminRequest(disabled, method, p, "valid")
			if w.Code != 404 {
				t.Errorf("disabled %s %s=%d", method, p, w.Code)
			}
			assertSecurity(t, w)
			if method == "HEAD" && w.Body.Len() != 0 {
				t.Errorf("disabled HEAD body")
			}
		}
		for _, auth := range []string{"", "wrong-user", "wrong-token", "Basic !!!", "Basic \x00\xff"} {
			w := adminRequest(active, "GET", p, auth)
			if w.Code != 401 || w.Header().Get("WWW-Authenticate") != `Basic realm="coopdock-admin", charset="UTF-8"` {
				t.Errorf("auth %s=%d", p, w.Code)
			}
			assertSecurity(t, w)
		}
	}
	for i, p := range paths {
		want := 404
		if i < 6 {
			want = 200
		}
		for _, method := range []string{"GET", "HEAD"} {
			w := adminRequest(active, method, p, "valid")
			if w.Code != want {
				t.Errorf("%s %s=%d want %d", method, p, w.Code, want)
			}
			assertSecurity(t, w)
			if method == "HEAD" && w.Body.Len() != 0 {
				t.Errorf("HEAD %s body", p)
			}
		}
		if i < 6 {
			w := adminRequest(active, "PUT", p, "valid")
			if w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" {
				t.Errorf("unsupported method %s=%d", p, w.Code)
			}
			assertSecurity(t, w)
		}
	}
	for _, p := range []string{"/admin/api/rooms/id/kick", "/admin/api/rooms/id/close", "/admin/api/server/drain"} {
		w := adminRequest(active, "POST", p, "valid")
		if w.Code != 403 {
			t.Errorf("operator %s=%d", p, w.Code)
		}
		assertSecurity(t, w)
	}
	if _, e := New(Config{ViewerToken: testToken}, nil); e == nil {
		t.Fatal("active nil collector accepted")
	}
	if _, e := New(Config{ViewerToken: "bad"}, c); e == nil {
		t.Fatal("invalid config accepted")
	}
}

func TestRoomsQueryBounds(t *testing.T) {
	for _, raw := range []string{"limit=0", "limit=101", "limit=", "limit=1&limit=2", "offset=-1", "offset=4097", "offset=1e2", "number_prefix=１２", "number_prefix=1234567", "phase=waiting", "visibility=secret", "extra=x", "limit=%ZZ", "offset=+1", "offset=1.0", "offset=99999999999999999999999999999", "phase=all&phase=all", "number_prefix=1&number_prefix=1", "visibility=all&visibility=all", "offset=0&offset=0", "number_prefix=%FF", "limit=1;offset=0", "phase=", "visibility="} {
		values, e := url.ParseQuery(raw)
		if e == nil {
			_, e = parseRoomsQuery(values)
		}
		if e == nil {
			t.Errorf("accepted invalid query %s", raw)
		}
	}
	q, e := parseRoomsQuery(url.Values{})
	if e != nil || q.Limit != 100 || q.Offset != 0 || q.Phase != "all" || q.Visibility != "all" || q.NumberPrefix != "" {
		t.Fatal("invalid defaults")
	}
	q, e = parseRoomsQuery(url.Values{"limit": {"001"}, "offset": {"04096"}, "number_prefix": {"001"}, "phase": {"WAITING"}, "visibility": {"private"}})
	if e != nil || q.Limit != 1 || q.Offset != 4096 || q.NumberPrefix != "001" {
		t.Fatal("decimal bounds/leading zeros rejected")
	}
}
func decodeObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if e := json.Unmarshal(body, &v); e != nil {
		t.Fatal(e)
	}
	return v
}
func assertKeys(t *testing.T, v map[string]any, keys string) {
	t.Helper()
	got := make([]string, 0, len(v))
	for k := range v {
		got = append(got, k)
	}
	want := strings.Fields(keys)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("keys=%v want %v", got, want)
	}
}

const statusKeys = "server_instance_id state ready active_connections room_member_count room_count waiting_room_count playing_room_count started_at uptime_seconds heap_alloc_bytes goroutines cpu_percent rtt_ms client_display_delay_ms updated_at age_ms stale generation"
const roomsKeys = "total matched_total offset limit rooms updated_at age_ms stale generation"
const rowKeys = "game_id room_id room_number visibility phase capacity member_count host_id match_id updated_at age_ms stale"

func TestRoomsFiltersBeforePagination(t *testing.T) {
	at := time.Date(2026, 10, 11, 12, 0, 0, 0, time.FixedZone("KST", 9*3600))
	rows := make([]room.OperationsSummary, 129)
	for i := range rows {
		rows[i] = room.OperationsSummary{RoomID: fmt.Sprintf("r%03d", 128-i), RoomNumber: fmt.Sprintf("%06d", i), Visibility: "public", Phase: "WAITING", Capacity: 5, MemberCount: 2, HostID: "호스트", UpdatedAt: at}
		if i%2 == 0 {
			rows[i].Visibility = "private"
		}
		if i%3 == 0 {
			rows[i].Phase = "PLAYING"
		}
	}
	// Equal numbers must use room identity as the tie breaker.
	rows[128].RoomNumber = rows[127].RoomNumber
	match := "match-value"
	rows[0].MatchID = &match
	c := testCollector(t, rows)
	h0, e := New(Config{ViewerToken: testToken}, c)
	if e != nil {
		t.Fatal(e)
	}
	h := h0.(*handler)
	now := c.Load().UpdatedAt.Add(5 * time.Second)
	calls := 0
	h.now = func() time.Time { calls++; return now }
	for _, tc := range []struct {
		query                  string
		matched, count, offset int
	}{{"", 129, 100, 0}, {"?offset=100", 129, 29, 100}, {"?offset=129", 129, 0, 129}, {"?number_prefix=00012", 9, 9, 0}, {"?visibility=private&phase=PLAYING&limit=1&offset=20", 22, 1, 20}} {
		w := adminRequest(h, "GET", "/admin/api/rooms"+tc.query, "valid")
		if w.Code != 200 {
			t.Fatalf("rooms=%d", w.Code)
		}
		v := decodeObject(t, w.Body.Bytes())
		assertKeys(t, v, roomsKeys)
		if v["total"] != float64(129) || v["matched_total"] != float64(tc.matched) || v["offset"] != float64(tc.offset) {
			t.Errorf("incorrect counts for %s", tc.query)
		}
		result, ok := v["rooms"].([]any)
		if !ok || len(result) != tc.count {
			t.Fatalf("rooms array %s=%v", tc.query, v["rooms"])
		}
		for i, r := range result {
			row := r.(map[string]any)
			assertKeys(t, row, rowKeys)
			if row["host_id"] != "호스트" || row["updated_at"] != "2026-10-11T03:00:00Z" || row["stale"] != true {
				t.Error("UTF-8/UTC/row stale mismatch")
			}
			if i > 0 {
				prev := result[i-1].(map[string]any)
				if prev["room_number"].(string) > row["room_number"].(string) || prev["room_number"] == row["room_number"] && prev["room_id"].(string) > row["room_id"].(string) {
					t.Error("unstable sort")
				}
			}
		}
		if v["age_ms"] != float64(5000) || v["stale"] != true {
			t.Error("cache stale boundary mismatch")
		}
		if strings.Contains(w.Body.String(), testToken) || strings.Contains(w.Body.String(), "private-password-marker") {
			t.Error("secret leaked")
		}
	}
	if calls != 5 {
		t.Errorf("now called %d times", calls)
	}
	w := adminRequest(h, "GET", "/admin/api/status", "valid")
	v := decodeObject(t, w.Body.Bytes())
	assertKeys(t, v, statusKeys)
	for _, key := range []string{"cpu_percent", "rtt_ms", "client_display_delay_ms"} {
		if v[key] != nil {
			t.Errorf("%s must be null", key)
		}
	}
	if v["room_count"] != float64(129) || v["room_member_count"] != float64(258) || v["active_connections"] != float64(3) || v["age_ms"] != float64(5000) {
		t.Error("status cache values mismatch")
	}
	original := c.Load().Rooms()
	_ = roomsResponse(c.Load(), roomsQuery{Limit: 100, Phase: "WAITING", Visibility: "public"}, now)
	if !reflect.DeepEqual(original, c.Load().Rooms()) {
		t.Fatal("filter mutated cache")
	}
	empty := testCollector(t, nil)
	emptyHandler, _ := New(Config{ViewerToken: testToken}, empty)
	emptyBody := adminRequest(emptyHandler, "GET", "/admin/api/rooms", "valid").Body.String()
	if !strings.Contains(emptyBody, `"rooms":[]`) {
		t.Error("empty rooms must be []")
	}
	for _, raw := range []string{"limit=%ZZ", "limit=1;offset=0", "extra=x", "limit=1&limit=2"} {
		for _, method := range []string{"GET", "HEAD"} {
			w := adminRequest(h, method, "/admin/api/rooms?"+raw, "valid")
			if w.Code != 400 {
				t.Errorf("invalid HTTP query %s=%d", raw, w.Code)
			}
			assertSecurity(t, w)
			if method == "HEAD" && w.Body.Len() != 0 {
				t.Error("error HEAD body")
			}
		}
	}
}
func TestResponseClockBoundaries(t *testing.T) {
	at := time.Now()
	v := observability.Snapshot{StartedAt: at, UpdatedAt: at}
	for _, delta := range []time.Duration{-time.Second, 4999 * time.Millisecond, 5000 * time.Millisecond} {
		s := statusResponse(v, at.Add(delta))
		r := roomsResponse(v, roomsQuery{Limit: 100}, at.Add(delta))
		wantAge := int64(delta / time.Millisecond)
		if wantAge < 0 {
			wantAge = 0
		}
		if s.AgeMillis != wantAge || r.AgeMillis != wantAge || s.Stale != (delta >= 5*time.Second) || r.Stale != s.Stale {
			t.Error("clock boundary mismatch")
		}
		if delta < 0 && s.UptimeSeconds != 0 {
			t.Error("negative uptime")
		}
	}
}
func TestAdminReadsOnlyCache(t *testing.T) {
	var calls atomic.Int32
	blocked, release := make(chan struct{}), make(chan struct{})
	c, e := observability.New(context.Background(), observability.Source{Rooms: func() room.OperationsSnapshot {
		n := calls.Add(1)
		if n == 2 {
			close(blocked)
			<-release
		}
		rows := []room.OperationsSummary{{RoomID: "closed-later", RoomNumber: "000001", UpdatedAt: time.Now()}}
		if n >= 2 {
			rows = nil
		}
		return room.OperationsSnapshot{Ready: true, Rooms: rows}
	}, Transport: func() (int, bool) { return 7, false }}, observability.Config{Interval: 10 * time.Millisecond})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if e := c.Close(ctx); e != nil {
			t.Error(e)
		}
	}()
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("source did not block")
	}
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	h0, _ := New(Config{ViewerToken: testToken}, c)
	h := h0.(*handler)
	old := c.Load()
	h.now = func() time.Time { return old.UpdatedAt.Add(6 * time.Second) }
	started := time.Now()
	for i := 0; i < 100; i++ {
		path := "/admin/api/status"
		if i%2 == 1 {
			path = "/admin/api/rooms"
		}
		w := adminRequest(h, "GET", path, "valid")
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		v := decodeObject(t, w.Body.Bytes())
		if v["generation"] != float64(old.Generation) || v["age_ms"] != float64(6000) || v["stale"] != true {
			t.Fatal("blocked source cache changed")
		}
		if path == "/admin/api/status" {
			if v["active_connections"] != float64(7) || v["room_count"] != float64(1) || v["heap_alloc_bytes"] != float64(old.HeapAllocBytes) || v["goroutines"] != float64(old.Goroutines) {
				t.Fatal("HTTP resampled cached fields")
			}
		} else {
			rows := v["rooms"].([]any)
			if v["total"] != float64(1) || len(rows) != 1 || rows[0].(map[string]any)["room_id"] != "closed-later" {
				t.Fatal("HTTP replaced blocked source rows")
			}
		}
	}
	if calls.Load() != 2 || time.Since(started) > time.Second {
		t.Fatal("HTTP waited for or called source")
	}
	close(release)
	released = true
	deadline := time.Now().Add(time.Second)
	for c.Load().Generation == old.Generation && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	fresh := c.Load()
	if fresh.Generation <= old.Generation {
		t.Fatal("no fresh recovery")
	}
	h.now = func() time.Time { return fresh.UpdatedAt }
	w := adminRequest(h, "GET", "/admin/api/rooms", "valid")
	v := decodeObject(t, w.Body.Bytes())
	if v["stale"] != false || v["total"] != float64(0) || len(v["rooms"].([]any)) != 0 {
		t.Fatal("closed room remained")
	}
}

func TestPrivateRoomPasswordIsExcludedFromAdminJSON(t *testing.T) {
	m, e := room.NewManager(room.DefaultConfig())
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if e := m.Close(ctx); e != nil {
			t.Error(e)
		}
	}()
	host, e := m.Connect("127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	password := "private-password-marker"
	private, e := m.Create(context.Background(), host, room.CreateOptions{Capacity: 2, Visibility: "private", Password: &password})
	if e != nil {
		t.Fatal(e)
	}
	summary := m.OperationsSnapshot()
	encoded, e := json.Marshal(summary)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(encoded), password) {
		t.Fatal("summary leaked password")
	}
	c := testCollector(t, summary.Rooms)
	h, _ := New(Config{ViewerToken: testToken}, c)
	w := adminRequest(h, "GET", "/admin/api/rooms?visibility=private", "valid")
	v := decodeObject(t, w.Body.Bytes())
	rows := v["rooms"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["room_id"] != private.RoomID {
		t.Fatal("private room hidden")
	}
	if strings.Contains(w.Body.String(), password) {
		t.Fatal("API leaked password")
	}
	assertKeys(t, rows[0].(map[string]any), rowKeys)
}
