package eebus

import (
	"sync"
	"sync/atomic"
	"time"
)

type eventSubscriber struct {
	ski            string
	droppedTotal   uint64
	pendingDropped uint64
}

// EventBus provides fan-out event distribution to multiple subscribers.
type EventBus struct {
	mu                      sync.RWMutex
	subscribers             map[chan Event]*eventSubscriber
	revisions               map[string]uint64
	droppedByDevice         map[string]uint64
	resyncsByDevice         map[string]uint64
	droppedUnresolvedEvents atomic.Uint64
}

type EventTransportSnapshot struct {
	Revision         uint64
	DroppedEvents    uint64
	ResyncCount      uint64
	UnresolvedEvents uint64
}

func NewEventBus() *EventBus {
	return &EventBus{
		subscribers:     make(map[chan Event]*eventSubscriber),
		revisions:       make(map[string]uint64),
		droppedByDevice: make(map[string]uint64),
		resyncsByDevice: make(map[string]uint64),
	}
}

// Subscribe returns a channel that receives published events.
// Buffer size 64 to avoid blocking publishers on slow consumers.
func (b *EventBus) Subscribe() chan Event {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subscribers[ch] = &eventSubscriber{}
	b.mu.Unlock()
	return ch
}

// SubscribeWithRevision atomically subscribes and snapshots the current
// revision for a device. Events published after the snapshot therefore always
// have a greater revision and cannot overtake a stream's initial envelope.
func (b *EventBus) SubscribeWithRevision(ski string) (chan Event, uint64) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	ski = NormalizeSKI(ski)
	b.subscribers[ch] = &eventSubscriber{ski: ski}
	revision := b.revisions[ski]
	b.mu.Unlock()
	return ch, revision
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
	b.mu.Lock()
	defer b.mu.Unlock()
	b.revisions[evt.SKI]++
	evt.Revision = b.revisions[evt.SKI]
	evt.OccurredAt = time.Now().UTC()
	for ch, subscriber := range b.subscribers {
		if subscriber.ski != "" && subscriber.ski != evt.SKI {
			continue
		}
		select {
		case ch <- evt:
		default:
			subscriber.droppedTotal++
			subscriber.pendingDropped++
			b.droppedByDevice[evt.SKI]++
		}
	}
}

// TakePendingResync returns one coalesced resync marker before a scoped
// subscriber reads more buffered events. A gRPC sender calls this immediately
// after each successful write; when backpressure clears, the marker is thus
// delivered without waiting for another domain event to be published.
func (b *EventBus) TakePendingResync(ch chan Event) (Event, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	subscriber, ok := b.subscribers[ch]
	if !ok || subscriber.ski == "" || subscriber.pendingDropped == 0 {
		return Event{}, false
	}
	event := Event{
		SKI:        subscriber.ski,
		Type:       EventTypeResyncRequired,
		Revision:   b.revisions[subscriber.ski],
		OccurredAt: time.Now().UTC(),
		Dropped:    subscriber.pendingDropped,
	}
	subscriber.pendingDropped = 0
	b.resyncsByDevice[subscriber.ski]++
	return event, true
}

// SubscriberDroppedEvents returns the number of events dropped for one live
// subscriber. It is intended for diagnostics and deterministic contract tests.
func (b *EventBus) SubscriberDroppedEvents(ch chan Event) uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	subscriber, ok := b.subscribers[ch]
	if !ok {
		return 0
	}
	return subscriber.droppedTotal
}

// Revision returns the latest published revision for a canonical device SKI.
func (b *EventBus) Revision(ski string) uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.revisions[NormalizeSKI(ski)]
}

// DroppedUnresolvedEvents returns the number of publish attempts rejected
// because no canonical device SKI was supplied.
func (b *EventBus) DroppedUnresolvedEvents() uint64 {
	return b.droppedUnresolvedEvents.Load()
}

// Diagnostics returns durable device-scoped event transport counters. Unlike
// SubscriberDroppedEvents it remains meaningful after a stream reconnects.
func (b *EventBus) Diagnostics(ski string) EventTransportSnapshot {
	b.mu.RLock()
	defer b.mu.RUnlock()
	ski = NormalizeSKI(ski)
	return EventTransportSnapshot{
		Revision:         b.revisions[ski],
		DroppedEvents:    b.droppedByDevice[ski],
		ResyncCount:      b.resyncsByDevice[ski],
		UnresolvedEvents: b.droppedUnresolvedEvents.Load(),
	}
}
