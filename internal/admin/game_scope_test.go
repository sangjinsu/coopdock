package admin

import (
	"reflect"
	"roomserver/internal/room"
	"testing"
	"time"
)

func TestGameScopeRoomsExactFilter(t *testing.T) {
	rows := []room.OperationsSummary{{GameID: "game-b", RoomNumber: "004271", RoomID: "000", UpdatedAt: time.Now()}, {GameID: "game-a", RoomNumber: "004271", RoomID: "z2", UpdatedAt: time.Now()}, {GameID: "game-a", RoomNumber: "004271", RoomID: "z1", UpdatedAt: time.Now()}}
	c := testCollector(t, rows)
	h, err := New(Config{ViewerToken: testToken}, c)
	if err != nil {
		t.Fatal(err)
	}
	before := c.Load().Rooms()
	for _, tc := range []struct {
		query           string
		status, matched int
		ids             []string
	}{{"", 200, 3, []string{"z1", "z2", "000"}}, {"?game_id=game-a&limit=1&offset=1", 200, 2, []string{"z2"}}, {"?game_id=game", 200, 0, nil}, {"?game_id=unknown", 200, 0, nil}, {"?game_id=", 400, 0, nil}, {"?game_id=A", 400, 0, nil}, {"?game_id=game-a&game%5fid=game-b", 400, 0, nil}} {
		w := adminRequest(h, "GET", "/admin/api/rooms"+tc.query, "valid")
		if w.Code != tc.status {
			t.Errorf("%s=%d", tc.query, w.Code)
			continue
		}
		if w.Code != 200 {
			continue
		}
		v := decodeObject(t, w.Body.Bytes())
		if v["total"] != float64(3) || v["matched_total"] != float64(tc.matched) {
			t.Errorf("filter totals: %s", w.Body.String())
		}
		rs := v["rooms"].([]any)
		if len(rs) != len(tc.ids) {
			t.Fatalf("rows=%v", rs)
		}
		for i, r := range rs {
			row := r.(map[string]any)
			if row["room_id"] != tc.ids[i] || row["game_id"] == nil {
				t.Error("scope/sort identity missing", row)
			}
		}
	}
	if !reflect.DeepEqual(before, c.Load().Rooms()) {
		t.Fatal("filter mutated cache")
	}
}
