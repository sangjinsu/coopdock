package config

import "testing"

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
		t.Fatalf("%+v %v", c, e)
	}
}
