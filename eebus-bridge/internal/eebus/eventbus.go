package eebus

import "sync"

// Event represents an internal event from eebus-go callbacks.
type Event struct {
	SKI  string
	Type string
	Data map[string]any
}

// EventBus provides fan-out event distribution to multiple subscribers.
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[chan Event]struct{}
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
func (b *EventBus) Publish(evt Event) {
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
