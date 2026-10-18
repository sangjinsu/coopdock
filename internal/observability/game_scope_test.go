package observability

import (
	"context"
	"roomserver/internal/room"
	"testing"
	"time"
)

func TestGameScopeOperationsSortTotalsAndClone(t *testing.T) {
	match := "match"
	rows := []room.OperationsSummary{{GameID: "game-b", RoomNumber: "000001", RoomID: "000", Phase: "PLAYING", MemberCount: 3, MatchID: &match}, {GameID: "game-a", RoomNumber: "999999", RoomID: "z2", Phase: "WAITING", MemberCount: 1}, {GameID: "game-a", RoomNumber: "999999", RoomID: "z1", Phase: "WAITING", MemberCount: 2}}
	c, err := New(context.Background(), Source{Rooms: func() room.OperationsSnapshot { return room.OperationsSnapshot{Ready: true, Rooms: rows} }, Transport: func() (int, bool) { return 6, false }}, Config{Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	v := c.Load()
	got := v.Rooms()
	if v.RoomCount != 3 || v.RoomMemberCount != 6 || v.WaitingRoomCount != 2 || v.PlayingRoomCount != 1 || v.ActiveConnections != 6 || !v.Ready {
		t.Fatal("global operations totals", v)
	}
	if len(got) != 3 || got[0].RoomID != "z1" || got[1].RoomID != "z2" || got[2].RoomID != "000" {
		t.Fatal("tuple sort", got)
	}
	got[0].GameID = "mutated"
	*got[2].MatchID = "mutated"
	after := c.Load().Rooms()
	if after[0].GameID != "game-a" || *after[2].MatchID != "match" {
		t.Fatal("operations cache alias")
	}
}
