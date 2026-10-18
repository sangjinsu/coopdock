package main

import (
	"context"
	"roomserver/internal/room"
	"testing"
)

func listForTest(t *testing.T, m *room.Manager, ctx context.Context) ([]room.Snapshot, error) {
	t.Helper()
	observer, err := m.Connect("test-list")
	if err != nil {
		return nil, err
	}
	defer m.Disconnect(observer)
	return m.ListPublic(ctx, observer)
}
