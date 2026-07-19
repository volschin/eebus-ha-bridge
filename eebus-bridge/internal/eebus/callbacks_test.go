package eebus_test

import (
	"errors"
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
		if evt.SKI != "TESTSKI123" {
			t.Errorf("SKI = %q, want TESTSKI123", evt.SKI)
		}
		if evt.Type != eebus.EventTypeDeviceConnected {
			t.Errorf("Type = %q, want device.connected", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for connect event")
	}
}

func TestCallbacksConnectAddsDeviceToRegistry(t *testing.T) {
	bus := eebus.NewEventBus()
	clock := &fakeClock{now: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)}
	reg := eebus.NewDeviceRegistryWithClock(clock)
	cb := eebus.NewCallbacks(bus, false)
	cb.SetRegistry(reg)

	cb.RemoteServiceConnected(nil, shipapi.ServiceIdentity{SKI: "test-ski-123"})

	if _, ok := reg.GetDevice("test-ski-123"); !ok {
		t.Fatal("connected remote service was not added to registry")
	}
	clock.Advance(3 * time.Minute)
	if got := reg.StaleDevices(10*time.Minute, 2*time.Minute); len(got) != 1 || got[0] != "TESTSKI123" {
		t.Errorf("connect callback did not mark device connected: StaleDevices() = %v", got)
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
		if evt.Type != eebus.EventTypeDeviceDisconnected {
			t.Errorf("Type = %q, want device.disconnected", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestCallbacksDisconnectClearsCachedEntities(t *testing.T) {
	bus := eebus.NewEventBus()
	clock := &fakeClock{now: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)}
	reg := eebus.NewDeviceRegistryWithClock(clock)
	reg.AddDevice("test-ski-456", eebus.DeviceInfo{
		Brand:          "Vaillant",
		RemoteEntities: []spineapi.EntityRemoteInterface{nil},
	})
	reg.MarkConnected("test-ski-456")

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
	clock.Advance(24 * time.Hour)
	if got := reg.StaleDevices(10*time.Minute, 2*time.Minute); len(got) != 0 {
		t.Errorf("disconnect callback left device monitored: StaleDevices() = %v", got)
	}
}

func TestCallbacksTrustRemovedClearsCachedEntitiesAndPublishes(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	clock := &fakeClock{now: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)}
	reg := eebus.NewDeviceRegistryWithClock(clock)
	reg.AddDevice("test-ski-789", eebus.DeviceInfo{
		Brand:          "Vaillant",
		RemoteEntities: []spineapi.EntityRemoteInterface{nil},
	})
	reg.MarkConnected("test-ski-789")

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
	clock.Advance(24 * time.Hour)
	if got := reg.StaleDevices(10*time.Minute, 2*time.Minute); len(got) != 0 {
		t.Errorf("trust-removal callback left device monitored: StaleDevices() = %v", got)
	}

	select {
	case evt := <-ch:
		if evt.Type != eebus.EventTypeDeviceTrustRemoved {
			t.Errorf("Type = %q, want device.trust_removed", evt.Type)
		}
		if evt.SKI != "TESTSKI789" {
			t.Errorf("SKI = %q, want TESTSKI789", evt.SKI)
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
		if evt.Type != eebus.EventTypeDiscoveryUpdated {
			t.Errorf("Type = %q, want discovery.updated", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
	if got := bus.DroppedUnresolvedEvents(); got != 0 {
		t.Errorf("DroppedUnresolvedEvents() = %d, want 0", got)
	}

	if got := cb.DiscoveredServices(); len(got) != 1 || got[0].Ski != "abc" {
		t.Errorf("DiscoveredServices() = %+v, want one entry with Ski=abc", got)
	}
}

func TestCallbacksPairingAndTrustUpdates(t *testing.T) {
	bus := eebus.NewEventBus()
	events := bus.Subscribe()
	defer bus.Unsubscribe(events)
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice("ab:cd", eebus.DeviceInfo{})
	callbacks := eebus.NewCallbacks(bus, true)
	callbacks.SetRegistry(registry)
	identity := shipapi.ServiceIdentity{SKI: "ab:cd"}

	callbacks.ServiceUpdated(identity)
	callbacks.ServicePairingDetailUpdate(identity, nil)
	select {
	case event := <-events:
		if event.Type != eebus.EventTypePairingUpdated || event.SKI != "ABCD" {
			t.Fatalf("pairing event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for pairing event")
	}

	callbacks.ServiceAutoTrusted(nil, identity)
	if health, ok := registry.DeviceHealth("ab:cd"); !ok || !health.Trusted {
		t.Fatalf("trusted health = (%+v, %t)", health, ok)
	}
	callbacks.ServiceAutoTrustFailed(nil, identity, errors.New("pairing failed"))
	if health, ok := registry.DeviceHealth("ab:cd"); !ok || health.Trusted {
		t.Fatalf("failed-trust health = (%+v, %t)", health, ok)
	}
	callbacks.ServiceAutoTrustRemoved(nil, identity, "revoked")
	select {
	case event := <-events:
		if event.Type != eebus.EventTypeDeviceTrustRemoved {
			t.Fatalf("trust removal event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for trust removal event")
	}
}
