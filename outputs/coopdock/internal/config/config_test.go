package config

import "testing"

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
