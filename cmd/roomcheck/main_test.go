package main

import (
	"context"
	"net/http/httptest"
	"github.com/sangjinsu/coopdock/internal/room"
	"github.com/sangjinsu/coopdock/internal/transport"
	"testing"
)

func TestChecksRealWebSocketServer(t *testing.T) {
	cfg := room.DefaultConfig()
	cfg.ArgonMemory = 8
	cfg.ArgonTime = 1
	m, e := room.NewManager(cfg)
	if e != nil {
		t.Fatal(e)
	}
	s, e := transport.New(m, transport.DefaultConfig())
	if e != nil {
		t.Fatal(e)
	}
	h := httptest.NewServer(s.Handler())
	defer h.Close()
	defer s.Close(context.Background())
	if e = runChecks(context.Background(), h.URL+"/ws", 2); e != nil {
		t.Fatal(e)
	}
}
func TestChecksFailForUnreachableServer(t *testing.T) {
	if e := runChecks(context.Background(), "ws://127.0.0.1:1/ws", 1); e == nil {
		t.Fatal("false success")
	}
}
