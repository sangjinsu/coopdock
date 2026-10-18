package coopdock

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// These boundaries catch missing upper limits and unsafe zero-to-default coercion.
func TestConfigValidation(t *testing.T) {
	if err := (Config{}).Validate(); err == nil {
		t.Error("zero config accepted")
	}
	for _, tc := range []struct {
		name     string
		min, max int64
	}{
		{"Room.TickHz", 1, 120}, {"Room.SnapshotHz", 1, 120}, {"Room.InputQueue", 1, 4096}, {"Room.InputsPerTick", 1, 4096},
		{"Room.MaxRooms", 1, 4096}, {"Room.MaxSessions", 1, 24576}, {"Room.NumberRetries", 1, 1024},
		{"Room.ActorQueue", 1, 4096}, {"Room.CleanupQueue", 6, 4096}, {"Room.EventQueue", 1, 4096}, {"Room.ListLimit", 1, 1000},
		{"Room.HashWorkers", 1, 16}, {"Room.HashQueue", 1, 256}, {"Room.IPFailureLimit", 1, 10000}, {"Room.SessionFailureLimit", 1, 10000}, {"Room.RoomFailureLimit", 1, 10000}, {"Room.MaxFailureKeys", 3, 65536},
		{"Room.FailureTTL", 1, int64(time.Hour)}, {"Room.RequestTimeout", 1, int64(time.Minute)}, {"Room.ShutdownTimeout", 1, int64(time.Minute)},
		{"Room.ArgonMemory", 19456, 65536}, {"Transport.MessageLimit", 256, 1048576}, {"Transport.IngressQueue", 1, 4096}, {"Transport.OutboundQueue", 1, 4096},
		{"Transport.ReadTimeout", 1, int64(24 * time.Hour)}, {"Transport.WriteTimeout", 1, int64(time.Minute)}, {"Transport.RequestTimeout", 1, int64(time.Minute)}, {"Transport.ShutdownTimeout", 1, int64(time.Minute)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, v := range []int64{tc.min - 1, tc.min, tc.max, tc.max + 1} {
				c := DefaultConfig()
				c.Room.SnapshotHz = 1
				c.Room.TickHz = 120
				c.Room.InputsPerTick = 1
				c.Room.InputQueue = 4096
				names := strings.Split(tc.name, ".")
				field := reflect.ValueOf(&c).Elem().FieldByName(names[0]).FieldByName(names[1])
				if field.Kind() == reflect.Uint32 {
					field.SetUint(uint64(v))
				} else {
					field.SetInt(v)
				}
				err := c.Validate()
				valid := v >= tc.min && v <= tc.max
				if (err == nil) != valid {
					t.Errorf("value %d: valid=%v error=%v", v, valid, err)
				}
			}
		})
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Room.SnapshotHz = c.Room.TickHz + 1 }, func(c *Config) { c.Room.InputsPerTick = c.Room.InputQueue + 1 },
		func(c *Config) { c.Room.GameIDs = nil }, func(c *Config) { c.Room.GameIDs = []string{"default", "default"} },
		func(c *Config) { c.Room.GameIDs = []string{" bad"} }, func(c *Config) { c.Room.GameIDs = []string{"Upper"} },
		func(c *Config) {
			c.Room.GameIDs = make([]string, 33)
			for i := range c.Room.GameIDs {
				c.Room.GameIDs[i] = fmt.Sprintf("game-%d", i)
			}
		},
		func(c *Config) { c.Transport.Origins = []string{"*"} }, func(c *Config) { c.Transport.Origins = []string{"https://example.com"} },
		func(c *Config) { c.Transport.Origins = []string{"example.com:65536"} }, func(c *Config) { c.Transport.Origins = []string{"bad host"} },
		func(c *Config) { c.Admin.ViewerToken = "secret" }, func(c *Config) { c.Admin.ViewerToken = strings.Repeat("a", 257) },
		func(c *Config) { c.Admin.ViewerToken = strings.Repeat("a", 32) + " " },
	} {
		c := DefaultConfig()
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("invalid config accepted: room=%+v transport=%+v", c.Room, c.Transport)
		} else if c.Admin.ViewerToken != "" && strings.Contains(err.Error(), c.Admin.ViewerToken) {
			t.Error("token leaked")
		}
	}
	c := DefaultConfig()
	c.Room.GameIDs = make([]string, 32)
	for i := range c.Room.GameIDs {
		c.Room.GameIDs[i] = fmt.Sprintf("game-%d", i)
	}
	c.Transport.Origins = []string{"localhost:8080", "[::1]:443", "example.com"}
	c.Admin.ViewerToken = strings.Repeat("a", 256)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestConfigDefaultsIndependent(t *testing.T) {
	a, b := DefaultConfig(), DefaultConfig()
	if len(a.Room.GameIDs) != 1 || a.Room.GameIDs[0] != "default" {
		t.Fatal("legacy default missing")
	}
	a.Room.GameIDs[0] = "changed"
	if b.Room.GameIDs[0] != "default" {
		t.Fatal("default slices shared")
	}
}
