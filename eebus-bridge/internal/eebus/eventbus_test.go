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
	}
	bus.Publish(evt)

	select {
	case received := <-ch:
		if received.SKI != "TESTSKI" {
			t.Errorf("SKI = %q, want TESTSKI (Publish normalizes)", received.SKI)
		}
		if received.Type != "test-event" {
			t.Errorf("Type = %q, want test-event", received.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestEventBusPublishNormalizesSKI(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	bus.Publish(eebus.Event{SKI: " test:ski 123 ", Type: "evt"})

	select {
	case evt := <-ch:
		if evt.SKI != "TESTSKI123" {
			t.Errorf("SKI = %q, want TESTSKI123", evt.SKI)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestEventBusDropsAndCountsEmptySKI(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	bus.Publish(eebus.Event{Type: "device-event"})

	if got := bus.DroppedUnresolvedEvents(); got != 1 {
		t.Fatalf("DroppedUnresolvedEvents() = %d, want 1", got)
	}
	select {
	case evt := <-ch:
		t.Fatalf("received unresolved event %+v", evt)
	case <-time.After(50 * time.Millisecond):
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

func TestEventBusAssignsMonotonicDeviceRevisions(t *testing.T) {
	bus := eebus.NewEventBus()
	ch, initial := bus.SubscribeWithRevision("test:ski")
	defer bus.Unsubscribe(ch)
	if initial != 0 {
		t.Fatalf("initial revision = %d, want 0", initial)
	}

	bus.Publish(eebus.Event{SKI: "test-ski", Type: "first"})
	bus.Publish(eebus.Event{SKI: "test-ski", Type: "second"})
	first := <-ch
	second := <-ch
	if first.Revision != 1 || second.Revision != 2 {
		t.Fatalf("revisions = %d, %d, want 1, 2", first.Revision, second.Revision)
	}
	if first.OccurredAt.IsZero() || second.OccurredAt.Before(first.OccurredAt) {
		t.Fatalf("event times = %v, %v", first.OccurredAt, second.OccurredAt)
	}
	if got := bus.Revision(" TEST SKI "); got != 2 {
		t.Fatalf("Revision() = %d, want 2", got)
	}
}

func TestDeviceSubscriptionReceivesOnlyItsSKI(t *testing.T) {
	bus := eebus.NewEventBus()
	ch, _ := bus.SubscribeWithRevision("device-a")
	defer bus.Unsubscribe(ch)

	bus.Publish(eebus.Event{SKI: "device-b", Type: "other"})
	bus.Publish(eebus.Event{SKI: "device-a", Type: "target"})
	event := <-ch
	if event.SKI != "DEVICEA" || event.Type != "target" || event.Revision != 1 {
		t.Fatalf("scoped event = %+v", event)
	}
	if len(ch) != 0 {
		t.Fatalf("scoped subscription received %d extra events", len(ch))
	}
}

func TestEventBusSignalsOneResyncAfterSubscriberDrop(t *testing.T) {
	bus := eebus.NewEventBus()
	ch, _ := bus.SubscribeWithRevision("test-ski")
	defer bus.Unsubscribe(ch)

	for range 65 {
		bus.Publish(eebus.Event{SKI: "test-ski", Type: "data"})
	}
	if got := bus.SubscriberDroppedEvents(ch); got != 1 {
		t.Fatalf("drops before recovery = %d, want 1", got)
	}
	resync, ok := bus.TakePendingResync(ch)
	if !ok {
		t.Fatal("pending resync was not returned")
	}
	if resync.Revision != 65 || resync.Dropped != 1 {
		t.Fatalf("resync = %+v, want revision 65 covering 1 drop", resync)
	}
	if _, duplicate := bus.TakePendingResync(ch); duplicate {
		t.Fatal("same drop produced more than one resync")
	}
	if got := bus.SubscriberDroppedEvents(ch); got != 1 {
		t.Fatalf("total drops = %d, want 1", got)
	}
	diagnostics := bus.Diagnostics("test-ski")
	if diagnostics.Revision != 65 || diagnostics.DroppedEvents != 1 || diagnostics.ResyncCount != 1 {
		t.Fatalf("Diagnostics() = %+v, want revision/drop/resync 65/1/1", diagnostics)
	}

	<-ch // make the subscriber writable again
	bus.Publish(eebus.Event{SKI: "test-ski", Type: "after-resync"})
	for len(ch) > 1 {
		<-ch
	}
	event := <-ch
	if event.Type != "after-resync" || event.Revision != 66 {
		t.Fatalf("event after resync = %+v, want revision 66", event)
	}
}
