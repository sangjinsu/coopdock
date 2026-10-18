package transport

import (
	"context"
	"reflect"
	"github.com/sangjinsu/coopdock/internal/protocol"
	"testing"
)

// A selected but unstarted snapshot must not cross a lifecycle fence.
func TestOutboxFenceAtWriteBoundary(t *testing.T) {
	box := newOutbox(8)
	box.snapshot(protocol.Event("game.snapshot", map[string]int{"n": 1}))
	picked, epoch, ok := box.next(context.Background())
	if !ok || picked.Op != "game.snapshot" || epoch == 0 {
		t.Fatal("not a snapshot")
	}
	if !box.reliable(true, protocol.Event("game.ended", struct{}{}), protocol.Event("room.state", struct{}{})) {
		t.Fatal("enqueue failed")
	}
	if box.beginSnapshotWrite(epoch) {
		t.Fatal("old snapshot crossed fence")
	}
	for _, want := range []string{"game.ended", "room.state"} {
		got, e, ok := box.next(context.Background())
		if !ok || e != 0 || got.Op != want {
			t.Fatal("reliable order")
		}
	}
}

// Coalescing snapshots must preserve all ordered reliable messages.
func TestSnapshotReplacementPreservesReliable(t *testing.T) {
	box := newOutbox(3)
	for i := 0; i < 100; i++ {
		box.snapshot(protocol.Event("game.snapshot", i))
	}
	if !box.reliable(false, protocol.Event("game.action", 1), protocol.Event("game.event", 2), protocol.Event("game.event", 3)) {
		t.Fatal("snapshot consumed reliable capacity")
	}
	for _, want := range []string{"game.action", "game.event", "game.event"} {
		got, e, ok := box.next(context.Background())
		if !ok || e != 0 || got.Op != want {
			t.Fatal("snapshot overtook reliable")
		}
	}
	got, e, ok := box.next(context.Background())
	if !ok || e == 0 || got.Payload != 99 || !box.beginSnapshotWrite(e) {
		t.Fatal("latest snapshot lost")
	}
}

func TestOutboxAtomicBatchAndReliableFence(t *testing.T) {
	box := newOutbox(2)
	box.snapshot(protocol.Event("game.snapshot", 1))
	_, epoch, _ := box.next(context.Background())
	if !box.reliable(false, protocol.Event("ack", 1)) || box.beginSnapshotWrite(epoch) {
		t.Fatal("snapshot started ahead of queued reliable")
	}
	box.snapshot(protocol.Event("game.snapshot", 9))
	box.mu.Lock()
	beforeQueue := append([]protocol.Envelope(nil), box.reliableQueue...)
	beforeLatest, beforeEpoch := *box.latest, box.epoch
	box.mu.Unlock()
	if box.reliable(true, protocol.Event("game.ended", 1), protocol.Event("room.state", 2)) {
		t.Fatal("partial batch accepted")
	}
	box.mu.Lock()
	unchanged := reflect.DeepEqual(box.reliableQueue, beforeQueue) && box.latest != nil && reflect.DeepEqual(*box.latest, beforeLatest) && box.epoch == beforeEpoch
	box.mu.Unlock()
	if !unchanged {
		t.Fatal("rejected batch changed queue/latest/epoch")
	}
	got, _, _ := box.next(context.Background())
	if got.Op != "ack" {
		t.Fatal("atomic failure changed FIFO")
	}
	box.mu.Lock()
	remaining := len(box.reliableQueue)
	box.mu.Unlock()
	if remaining != 0 {
		t.Fatal("failed batch partially published")
	}
}

func TestOutboxStartedSnapshotMayFinishBeforeTransition(t *testing.T) {
	box := newOutbox(2)
	box.snapshot(protocol.Event("game.snapshot", 1))
	picked, epoch, ok := box.next(context.Background())
	if !ok || !box.beginSnapshotWrite(epoch) {
		t.Fatal("snapshot could not start")
	}
	// The sole writer has committed this first write; publication cannot retract it.
	if !box.reliable(true, protocol.Event("game.ended", 2), protocol.Event("room.state", 3)) {
		t.Fatal("transition failed")
	}
	got, e, ok := box.next(context.Background())
	if !ok || e != 0 || picked.Op != "game.snapshot" || got.Op != "game.ended" {
		t.Fatal("started snapshot/transition order")
	}
	got, e, ok = box.next(context.Background())
	if !ok || e != 0 || got.Op != "room.state" {
		t.Fatal("paired state order")
	}
}
