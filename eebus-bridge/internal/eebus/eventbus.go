package eebus

import (
	"sync"
	"sync/atomic"
)

// EventBus provides fan-out event distribution to multiple subscribers.
type EventBus struct {
	mu                      sync.RWMutex
	subscribers             map[chan Event]struct{}
	droppedUnresolvedEvents atomic.Uint64
}

func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[chan Event]struct{}),
	}
}

// Subscribe returns a channel that receives published events.
// Buffer size 64 to avoid blocking publishers on slow consumers.
func (b *EventBus) Subscribe() chan Event {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel and closes it.
func (b *EventBus) Unsubscribe(ch chan Event) {
	b.mu.Lock()
	if _, ok := b.subscribers[ch]; ok {
		delete(b.subscribers, ch)
		close(ch)
	}
	b.mu.Unlock()
}

// Publish sends an event to all subscribers. Non-blocking: drops events
// for subscribers whose buffer is full.
//
// evt.SKI is normalized here so every subscriber sees a canonical SKI.
// Device-scoped events without a resolvable device identity are dropped at
// this boundary, never broadcast as wildcards, and included in
// DroppedUnresolvedEvents. EventTypeDiscoveryUpdated is exempt: it reports on
// the whole visible-services list, not a single device, so it has no SKI to
// resolve in the first place.
func (b *EventBus) Publish(evt Event) {
	evt.SKI = NormalizeSKI(evt.SKI)
	if evt.SKI == "" && evt.Type != EventTypeDiscoveryUpdated {
		b.droppedUnresolvedEvents.Add(1)
		return
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subscribers {
		select {
		case ch <- evt:
		default:
			// subscriber too slow, drop event
		}
	}
}

// DroppedUnresolvedEvents returns the number of publish attempts rejected
// because no canonical device SKI was supplied.
func (b *EventBus) DroppedUnresolvedEvents() uint64 {
	return b.droppedUnresolvedEvents.Load()
}
