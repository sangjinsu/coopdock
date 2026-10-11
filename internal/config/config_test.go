package config

import (
	"fmt"
	"testing"
)

func TestMovementRateConfig(t *testing.T) {
	t.Setenv("ROOM_TICK_HZ", "20")
	t.Setenv("ROOM_SNAPSHOT_HZ", "21")
	if _, err := Load(); err == nil {
		t.Fatal("snapshot exceeds tick")
	}
	t.Setenv("ROOM_SNAPSHOT_HZ", "10")
	t.Setenv("ROOM_INPUT_QUEUE", "12")
	t.Setenv("ROOM_INPUTS_PER_TICK", "6")
	got, err := Load()
	if err != nil || got.Room.TickHz != 20 || got.Room.SnapshotHz != 10 || got.Room.InputQueue != 12 || got.Room.InputsPerTick != 6 {
		t.Fatalf("settings ignored: %+v, %v", got.Room, err)
	}
}

func TestMovementConfigRejectsInvalidRanges(t *testing.T) {
	for _, key := range []string{"ROOM_TICK_HZ", "ROOM_SNAPSHOT_HZ", "ROOM_INPUT_QUEUE", "ROOM_INPUTS_PER_TICK"} {
		for _, value := range []string{"", "0", "-1", "no-number"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				t.Setenv(key, value)
				if _, err := Load(); err == nil {
					t.Fatal("accepted invalid movement setting")
				}
			})
		}
	}
	for _, pair := range []struct{ key, value string }{{"ROOM_TICK_HZ", "121"}, {"ROOM_SNAPSHOT_HZ", "31"}, {"ROOM_INPUT_QUEUE", "4097"}, {"ROOM_INPUTS_PER_TICK", "257"}} {
		t.Run(pair.key+"/upper", func(t *testing.T) {
			t.Setenv(pair.key, pair.value)
			if _, err := Load(); err == nil {
				t.Fatal("accepted upper bound violation")
			}
		})
	}
	t.Run("budget exceeds queue", func(t *testing.T) {
		t.Setenv("ROOM_INPUT_QUEUE", "4")
		t.Setenv("ROOM_INPUTS_PER_TICK", "5")
		if _, err := Load(); err == nil {
			t.Fatal("accepted budget exceeding queue")
		}
	})
}

func TestLoadRejectsInvalidEnvironment(t *testing.T) {
	for _, p := range []struct{ k, v string }{{"ROOM_MAX_ROOMS", "0"}, {"WS_OUTBOUND_QUEUE", "0"}, {"WS_REQUEST_TIMEOUT", "0s"}, {"HTTP_ADDR", "bad"}, {"WS_ORIGINS", "*"}, {"WS_ORIGINS", "bad host"}, {"WS_ORIGINS", "example.com:99999"}, {"HTTP_ADDR", "bad host:8080"}, {"ROOM_HASH_WORKERS", "10000"}, {"ROOM_ARGON_MEMORY_KIB", "1"}, {"ROOM_MAX_FAILURE_KEYS", "2"}} {
		t.Run(p.k, func(t *testing.T) {
			t.Setenv(p.k, p.v)
			if _, e := Load(); e == nil {
				t.Fatal("accepted invalid env")
			}
		})
	}
}
func TestLoadConfiguresRuntime(t *testing.T) {
	t.Setenv("HTTP_ADDR", "127.0.0.1:9090")
	t.Setenv("ROOM_MAX_ROOMS", "3")
	t.Setenv("WS_OUTBOUND_QUEUE", "8")
	t.Setenv("WS_ORIGINS", "localhost:9000, example.com")
	c, e := Load()
	if e != nil || c.Address != "127.0.0.1:9090" || c.Room.MaxRooms != 3 || c.Transport.OutboundQueue != 8 || len(c.Transport.Origins) != 2 {
		t.Fatalf("runtime settings mismatch: address=%q room_max_rooms=%d ws_outbound_queue=%d ws_origins_count=%d load_failed=%t", c.Address, c.Room.MaxRooms, c.Transport.OutboundQueue, len(c.Transport.Origins), e != nil)
	}
}

func TestAdminViewerEnvironment(t *testing.T) {
	good := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for i, tc := range []struct {
		value string
		valid bool
	}{{"", true}, {good, true}, {good + " ", false}, {" " + good, false}, {"short", false}, {good + "\t", false}, {good + "한", false}} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Setenv("ADMIN_VIEWER_TOKEN", tc.value)
			c, e := Load()
			if (e == nil) != tc.valid {
				t.Fatalf("valid=%v mismatch", tc.valid)
			}
			if e == nil && c.Admin.ViewerToken != tc.value {
				t.Fatal("raw token changed")
			}
			if e != nil && e.Error() != "Invalid ADMIN_VIEWER_TOKEN." {
				t.Fatal("unsafe error")
			}
		})
	}
}
