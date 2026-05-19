package usecases_test

import (
	"testing"
	"time"

	eglpc "github.com/enbility/eebus-go/usecases/eg/lpc"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
)

func TestLPCEventRouting(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	lpcWrapper := usecases.NewLPCWrapper(bus, nil, false)

	// Simulate an eebus-go event callback directly (no SPINE entity needed).
	lpcWrapper.HandleEvent("test-ski", nil, nil, eglpc.DataUpdateLimit)

	select {
	case evt := <-ch:
		if evt.SKI != "test-ski" {
			t.Errorf("SKI = %q, want test-ski", evt.SKI)
		}
		if evt.Type != "lpc.limit_updated" {
			t.Errorf("Type = %q, want lpc.limit_updated", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for LPC event")
	}
}

func TestLPCFailsafePowerEventRouting(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	lpcWrapper := usecases.NewLPCWrapper(bus, nil, false)
	lpcWrapper.HandleEvent("ski-2", nil, nil, eglpc.DataUpdateFailsafeConsumptionActivePowerLimit)

	select {
	case evt := <-ch:
		if evt.Type != "lpc.failsafe_power_updated" {
			t.Errorf("Type = %q, want lpc.failsafe_power_updated", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for LPC failsafe power event")
	}
}

func TestLPCFailsafeDurationEventRouting(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	lpcWrapper := usecases.NewLPCWrapper(bus, nil, false)
	lpcWrapper.HandleEvent("ski-3", nil, nil, eglpc.DataUpdateFailsafeDurationMinimum)

	select {
	case evt := <-ch:
		if evt.Type != "lpc.failsafe_duration_updated" {
			t.Errorf("Type = %q, want lpc.failsafe_duration_updated", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for LPC failsafe duration event")
	}
}

func TestLPCUnknownEventIgnored(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	lpcWrapper := usecases.NewLPCWrapper(bus, nil, false)
	lpcWrapper.HandleEvent("ski-x", nil, nil, "unknown-event-type")

	select {
	case evt := <-ch:
		t.Errorf("unexpected event published: %+v", evt)
	case <-time.After(50 * time.Millisecond):
		// expected: no event published
	}
}
