package eebus_test

import (
	"testing"
	"time"

	shipapi "github.com/enbility/ship-go/api"
	spineapi "github.com/enbility/spine-go/api"
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

func TestCallbacksDisconnectClearsCachedEntities(t *testing.T) {
	bus := eebus.NewEventBus()
	reg := eebus.NewDeviceRegistry()
	reg.AddDevice("test-ski-456", eebus.DeviceInfo{
		Brand:          "Vaillant",
		RemoteEntities: []spineapi.EntityRemoteInterface{nil},
	})

	cb := eebus.NewCallbacks(bus, false)
	cb.SetRegistry(reg)
	cb.RemoteServiceDisconnected(nil, shipapi.ServiceIdentity{SKI: "test-ski-456"})

	info, ok := reg.GetDevice("test-ski-456")
	if !ok {
		t.Fatal("device metadata removed on disconnect; only entities should be cleared")
	}
	if len(info.RemoteEntities) != 0 {
		t.Errorf("cached entities not cleared on disconnect: got %d", len(info.RemoteEntities))
	}
	if info.Brand != "Vaillant" {
		t.Error("classification metadata must survive disconnect")
	}
}

func TestCallbacksTrustRemovedClearsCachedEntitiesAndPublishes(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	reg := eebus.NewDeviceRegistry()
	reg.AddDevice("test-ski-789", eebus.DeviceInfo{
		Brand:          "Vaillant",
		RemoteEntities: []spineapi.EntityRemoteInterface{nil},
	})

	cb := eebus.NewCallbacks(bus, false)
	cb.SetRegistry(reg)
	cb.ServiceAutoTrustRemoved(nil, shipapi.ServiceIdentity{SKI: "test-ski-789"}, "remote revoked")

	info, ok := reg.GetDevice("test-ski-789")
	if !ok {
		t.Fatal("device metadata removed on trust removal; only entities should be cleared")
	}
	if len(info.RemoteEntities) != 0 {
		t.Errorf("cached entities not cleared on trust removal: got %d", len(info.RemoteEntities))
	}

	select {
	case evt := <-ch:
		if evt.Type != "device.trust_removed" {
			t.Errorf("Type = %q, want device.trust_removed", evt.Type)
		}
		if evt.SKI != "TEST-SKI-789" {
			t.Errorf("SKI = %q, want TEST-SKI-789", evt.SKI)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for trust_removed event")
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
