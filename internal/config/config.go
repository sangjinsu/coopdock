// Package config loads bounded, validated runtime settings.
package config

import (
	"fmt"
	"github.com/sangjinsu/coopdock"
	"github.com/sangjinsu/coopdock/internal/admin"
	"github.com/sangjinsu/coopdock/internal/room"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	coopdock.Config
	Address string
}

func Load() (Config, error) {
	c := Config{Config: coopdock.DefaultConfig(), Address: "127.0.0.1:8080"}
	c.Admin.ViewerToken = os.Getenv("ADMIN_VIEWER_TOKEN")
	if e := (admin.Config{ViewerToken: c.Admin.ViewerToken}).Validate(); e != nil {
		return c, e
	}
	if raw, ok := os.LookupEnv("GAME_IDS"); ok {
		ids := strings.Split(raw, ",")
		seen := map[string]bool{}
		if len(ids) < 1 || len(ids) > 32 {
			return c, fmt.Errorf("Invalid GAME_IDS.")
		}
		for i, id := range ids {
			id = strings.TrimSpace(id)
			if !room.ValidGameID(id) || seen[id] {
				return c, fmt.Errorf("Invalid GAME_IDS.")
			}
			seen[id] = true
			ids[i] = id
		}
		c.Room.GameIDs = ids
	}
	if v, ok := os.LookupEnv("HTTP_ADDR"); ok {
		c.Address = v
	}
	host, port, e := net.SplitHostPort(c.Address)
	if e != nil || host == "" || strings.ContainsAny(host, " \t\r\n*/?#@") {
		return c, fmt.Errorf("Invalid HTTP_ADDR.")
	}
	n, e := strconv.Atoi(port)
	if e != nil || n < 1 || n > 65535 {
		return c, fmt.Errorf("Invalid HTTP_ADDR.")
	}
	ints := []struct {
		k        string
		p        *int
		min, max int
	}{
		{"ROOM_TICK_HZ", &c.Room.TickHz, 1, 120}, {"ROOM_SNAPSHOT_HZ", &c.Room.SnapshotHz, 1, 120}, {"ROOM_INPUT_QUEUE", &c.Room.InputQueue, 1, 4096}, {"ROOM_INPUTS_PER_TICK", &c.Room.InputsPerTick, 1, 4096},
		{"ROOM_MAX_ROOMS", &c.Room.MaxRooms, 1, 4096}, {"ROOM_MAX_SESSIONS", &c.Room.MaxSessions, 1, 24576}, {"ROOM_NUMBER_RETRIES", &c.Room.NumberRetries, 1, 1024}, {"ROOM_ACTOR_QUEUE", &c.Room.ActorQueue, 1, 4096}, {"ROOM_CLEANUP_QUEUE", &c.Room.CleanupQueue, 6, 4096}, {"ROOM_EVENT_QUEUE", &c.Room.EventQueue, 1, 4096}, {"ROOM_LIST_LIMIT", &c.Room.ListLimit, 1, 1000}, {"ROOM_HASH_WORKERS", &c.Room.HashWorkers, 1, 16}, {"ROOM_HASH_QUEUE", &c.Room.HashQueue, 1, 256}, {"ROOM_IP_FAILURE_LIMIT", &c.Room.IPFailureLimit, 1, 10000}, {"ROOM_SESSION_FAILURE_LIMIT", &c.Room.SessionFailureLimit, 1, 10000}, {"ROOM_ROOM_FAILURE_LIMIT", &c.Room.RoomFailureLimit, 1, 10000}, {"ROOM_MAX_FAILURE_KEYS", &c.Room.MaxFailureKeys, 3, 65536}, {"WS_INGRESS_QUEUE", &c.Transport.IngressQueue, 1, 4096}, {"WS_OUTBOUND_QUEUE", &c.Transport.OutboundQueue, 1, 4096},
	}
	for _, v := range ints {
		if s, ok := os.LookupEnv(v.k); ok {
			n, e := strconv.Atoi(s)
			if e != nil || n < v.min || n > v.max {
				return c, fmt.Errorf("Invalid %s.", v.k)
			}
			*v.p = n
		}
	}
	if c.Room.SnapshotHz > c.Room.TickHz {
		return c, fmt.Errorf("Invalid ROOM_SNAPSHOT_HZ.")
	}
	if c.Room.InputsPerTick > c.Room.InputQueue {
		return c, fmt.Errorf("Invalid ROOM_INPUTS_PER_TICK.")
	}
	if s, ok := os.LookupEnv("WS_MESSAGE_BYTES"); ok {
		n, e := strconv.ParseInt(s, 10, 64)
		if e != nil || n < 256 || n > 1048576 {
			return c, fmt.Errorf("Invalid WS_MESSAGE_BYTES.")
		}
		c.Transport.MessageLimit = n
	}
	if s, ok := os.LookupEnv("ROOM_ARGON_MEMORY_KIB"); ok {
		n, e := strconv.ParseUint(s, 10, 32)
		if e != nil || n < 19456 || n > 65536 {
			return c, fmt.Errorf("Invalid ROOM_ARGON_MEMORY_KIB.")
		}
		c.Room.ArgonMemory = uint32(n)
	}
	durations := []struct {
		k   string
		p   *time.Duration
		max time.Duration
	}{{"ROOM_FAILURE_TTL", &c.Room.FailureTTL, time.Hour}, {"ROOM_REQUEST_TIMEOUT", &c.Room.RequestTimeout, time.Minute}, {"ROOM_SHUTDOWN_TIMEOUT", &c.Room.ShutdownTimeout, time.Minute}, {"WS_READ_TIMEOUT", &c.Transport.ReadTimeout, 24 * time.Hour}, {"WS_WRITE_TIMEOUT", &c.Transport.WriteTimeout, time.Minute}, {"WS_REQUEST_TIMEOUT", &c.Transport.RequestTimeout, time.Minute}, {"WS_SHUTDOWN_TIMEOUT", &c.Transport.ShutdownTimeout, time.Minute}}
	for _, v := range durations {
		if s, ok := os.LookupEnv(v.k); ok {
			d, e := time.ParseDuration(s)
			if e != nil || d <= 0 || d > v.max {
				return c, fmt.Errorf("Invalid %s.", v.k)
			}
			*v.p = d
		}
	}
	if raw, ok := os.LookupEnv("WS_ORIGINS"); ok {
		if strings.TrimSpace(raw) == "" {
			return c, fmt.Errorf("Invalid WS_ORIGINS.")
		}
		for _, s := range strings.Split(raw, ",") {
			s = strings.TrimSpace(s)
			if s == "" || strings.ContainsAny(s, "*/?#@") || strings.Contains(s, "://") {
				return c, fmt.Errorf("Invalid WS_ORIGINS.")
			}
			c.Transport.Origins = append(c.Transport.Origins, s)
		}
	}
	if e := c.Config.Validate(); e != nil {
		return c, e
	}
	return c, nil
}
