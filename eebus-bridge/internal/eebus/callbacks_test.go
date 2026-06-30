package eebus_test

import (
	"testing"
	"time"

	shipapi "github.com/enbility/ship-go/api"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

func TestCallbacksDispatchConnect(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	cb := eebus.NewCallbacks(bus, false)
	cb.RemoteServiceConnected(nil, shipapi.ServiceIdentity{SKI: "test-ski-123"})

	select {
	case evt := <-ch:
		// SKI is normalized (uppercased, whitespace stripped) before dispatch.
		if evt.SKI != "TEST-SKI-123" {
			t.Errorf("SKI = %q, want TEST-SKI-123", evt.SKI)
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

	cb := eebus.NewCallbacks(bus, false)
	cb.RemoteServiceDisconnected(nil, shipapi.ServiceIdentity{SKI: "test-ski-456"})

	select {
	case evt := <-ch:
		if evt.Type != "device.disconnected" {
			t.Errorf("Type = %q, want device.disconnected", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestCallbacksVisibleServicesUpdated(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	cb := eebus.NewCallbacks(bus, false)
	cb.VisibleRemoteMdnsServicesUpdated(nil, []shipapi.RemoteMdnsService{{Ski: "abc"}})

	select {
	case evt := <-ch:
		if evt.Type != "discovery.updated" {
			t.Errorf("Type = %q, want discovery.updated", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}

	if got := cb.DiscoveredServices(); len(got) != 1 || got[0].Ski != "abc" {
		t.Errorf("DiscoveredServices() = %+v, want one entry with Ski=abc", got)
	}
}
