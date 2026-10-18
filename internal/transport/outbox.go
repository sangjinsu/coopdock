package transport

import (
	"context"
	"github.com/sangjinsu/coopdock/internal/protocol"
	"sync"
)

type outbox struct {
	mu            sync.Mutex
	reliableQueue []protocol.Envelope
	latest        *protocol.Envelope
	epoch         uint64
	wake          chan struct{}
	capacity      int
}

func newOutbox(capacity int) *outbox {
	return &outbox{capacity: capacity, epoch: 1, wake: make(chan struct{}, 1)}
}
func (o *outbox) reliable(clearLatest bool, messages ...protocol.Envelope) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.reliableQueue)+len(messages) > o.capacity {
		return false
	}
	if clearLatest {
		o.latest = nil
		o.epoch++
	}
	o.reliableQueue = append(o.reliableQueue, messages...)
	select {
	case o.wake <- struct{}{}:
	default:
	}
	return true
}
func (o *outbox) snapshot(v protocol.Envelope) {
	o.mu.Lock()
	o.latest = &v
	o.mu.Unlock()
	select {
	case o.wake <- struct{}{}:
	default:
	}
}
func (o *outbox) next(ctx context.Context) (protocol.Envelope, uint64, bool) {
	for {
		if ctx.Err() != nil {
			return protocol.Envelope{}, 0, false
		}
		o.mu.Lock()
		if len(o.reliableQueue) > 0 {
			v := o.reliableQueue[0]
			o.reliableQueue[0] = protocol.Envelope{}
			o.reliableQueue = o.reliableQueue[1:]
			o.mu.Unlock()
			return v, 0, true
		}
		if o.latest != nil {
			v := *o.latest
			o.latest = nil
			e := o.epoch
			o.mu.Unlock()
			return v, e, true
		}
		o.mu.Unlock()
		select {
		case <-ctx.Done():
			return protocol.Envelope{}, 0, false
		case <-o.wake:
		}
	}
}

// This lock protects only the write-start linearization point, never socket I/O.
func (o *outbox) beginSnapshotWrite(epoch uint64) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return epoch == o.epoch && len(o.reliableQueue) == 0
}
