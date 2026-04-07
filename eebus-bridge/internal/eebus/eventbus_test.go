package eebus_test

import (
	"testing"
	"time"

	"github.com/volschin/eebus-bridge/internal/eebus"
)

func TestEventBusSubscribeAndPublish(t *testing.T) {
	bus := eebus.NewEventBus()

	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	evt := eebus.Event{
		SKI:  "test-ski",
		Type: "test-event",
		Data: map[string]any{"power": 1500.0},
	}
	bus.Publish(evt)

	select {
	case received := <-ch:
		if received.SKI != "test-ski" {
			t.Errorf("SKI = %q, want test-ski", received.SKI)
		}
		if received.Type != "test-event" {
			t.Errorf("Type = %q, want test-event", received.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestEventBusFanOut(t *testing.T) {
	bus := eebus.NewEventBus()

	ch1 := bus.Subscribe()
	ch2 := bus.Subscribe()
	defer bus.Unsubscribe(ch1)
	defer bus.Unsubscribe(ch2)

	bus.Publish(eebus.Event{SKI: "ski", Type: "evt"})

	for i, ch := range []<-chan eebus.Event{ch1, ch2} {
		select {
		case evt := <-ch:
			if evt.Type != "evt" {
				t.Errorf("subscriber %d: Type = %q, want evt", i, evt.Type)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timeout", i)
		}
	}
}

func TestEventBusUnsubscribe(t *testing.T) {
	bus := eebus.NewEventBus()

	ch := bus.Subscribe()
	bus.Unsubscribe(ch)

	// Publishing after unsubscribe should not block
	bus.Publish(eebus.Event{SKI: "ski", Type: "evt"})
}

func TestEventBusSlowSubscriberDoesNotBlock(t *testing.T) {
	bus := eebus.NewEventBus()

	_ = bus.Subscribe() // never read from this
	ch2 := bus.Subscribe()
	defer bus.Unsubscribe(ch2)

	// Publish should not block even though ch1 is not consumed
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			bus.Publish(eebus.Event{SKI: "ski", Type: "evt"})
		}
		close(done)
	}()

	select {
	case <-done:
		// OK — publishing didn't block
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on slow subscriber")
	}
}
