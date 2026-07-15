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
		if evt.Type != eebus.EventTypeLPCLimitUpdated {
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
		if evt.Type != eebus.EventTypeLPCFailsafePowerUpdated {
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
		if evt.Type != eebus.EventTypeLPCFailsafeDurationUpdated {
			t.Errorf("Type = %q, want lpc.failsafe_duration_updated", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for LPC failsafe duration event")
	}
}

func TestLPCHeartbeatEventRouting(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	lpcWrapper := usecases.NewLPCWrapper(bus, nil, false)
	lpcWrapper.HandleEvent("ski-4", nil, nil, eglpc.DataUpdateHeartbeat)

	select {
	case evt := <-ch:
		if evt.Type != eebus.EventTypeLPCHeartbeatUpdated {
			t.Errorf("Type = %q, want lpc.heartbeat_updated", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for LPC heartbeat event")
	}
}

func TestLPCHeartbeatWithoutLocalEntity(t *testing.T) {
	// Without Setup() the wrapper has no local entity; heartbeat operations must
	// fail gracefully rather than panic.
	lpcWrapper := usecases.NewLPCWrapper(eebus.NewEventBus(), nil, false)

	if err := lpcWrapper.StartHeartbeat(""); err == nil {
		t.Error("StartHeartbeat without local entity should return an error")
	}
	if err := lpcWrapper.StopHeartbeat(); err == nil {
		t.Error("StopHeartbeat without local entity should return an error")
	}
	if lpcWrapper.IsHeartbeatRunning() {
		t.Error("IsHeartbeatRunning should be false without local entity")
	}
	if lpcWrapper.IsHeartbeatWithinDuration(nil) {
		t.Error("IsHeartbeatWithinDuration should be false without local entity")
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
