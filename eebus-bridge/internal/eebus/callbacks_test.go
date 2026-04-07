package eebus_test

import (
	"testing"
	"time"

	"github.com/volschin/eebus-bridge/internal/eebus"
)

func TestCallbacksDispatchConnect(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	cb := eebus.NewCallbacks(bus)
	cb.RemoteSKIConnected(nil, "test-ski-123")

	select {
	case evt := <-ch:
		if evt.SKI != "test-ski-123" {
			t.Errorf("SKI = %q, want test-ski-123", evt.SKI)
		}
		if evt.Type != "device.connected" {
			t.Errorf("Type = %q, want device.connected", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for connect event")
	}
}

func TestCallbacksDispatchDisconnect(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	cb := eebus.NewCallbacks(bus)
	cb.RemoteSKIDisconnected(nil, "test-ski-456")

	select {
	case evt := <-ch:
		if evt.Type != "device.disconnected" {
			t.Errorf("Type = %q, want device.disconnected", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestCallbacksAllowWaitingForTrust(t *testing.T) {
	bus := eebus.NewEventBus()
	cb := eebus.NewCallbacks(bus)

	if !cb.AllowWaitingForTrust("any-ski") {
		t.Error("AllowWaitingForTrust should return true")
	}
}
