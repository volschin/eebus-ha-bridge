package eebus

import "testing"

type recordingRemoteTrustService struct {
	registry                      *DeviceRegistry
	events                        chan Event
	registerCalls                 []string
	unregisterCalls               []string
	devicePresentDuringUnregister bool
	eventCountDuringUnregister    int
}

func (s *recordingRemoteTrustService) RegisterRemoteSKI(ski string) {
	s.registerCalls = append(s.registerCalls, ski)
}

func (s *recordingRemoteTrustService) UnregisterRemoteSKI(ski string) {
	s.unregisterCalls = append(s.unregisterCalls, ski)
	_, s.devicePresentDuringUnregister = s.registry.GetDevice(ski)
	s.eventCountDuringUnregister = len(s.events)
}

func TestTrustControllerUnregisterOrderAndCleanup(t *testing.T) {
	const ski = "682f708ceba5df9adcb9e6787ea911d9fc3ac490"
	registry := NewDeviceRegistry()
	registry.AddDevice(ski, DeviceInfo{Brand: "test device"})
	bus := NewEventBus()
	events := bus.Subscribe()
	defer bus.Unsubscribe(events)
	bridge := &recordingRemoteTrustService{registry: registry, events: events}
	controller := &TrustController{bridge: bridge, registry: registry, bus: bus}

	if err := controller.UnregisterSKI(ski); err != nil {
		t.Fatalf("UnregisterSKI: %v", err)
	}

	wantSKI := NormalizeSKI(ski)
	if len(bridge.unregisterCalls) != 1 || bridge.unregisterCalls[0] != ski {
		t.Fatalf("UnregisterRemoteSKI calls = %v, want [%s]", bridge.unregisterCalls, ski)
	}
	if !bridge.devicePresentDuringUnregister {
		t.Error("device was removed before BridgeService.UnregisterRemoteSKI")
	}
	if bridge.eventCountDuringUnregister != 0 {
		t.Errorf("event count during BridgeService.UnregisterRemoteSKI = %d, want 0", bridge.eventCountDuringUnregister)
	}
	if _, ok := registry.GetDevice(ski); ok {
		t.Fatal("device remains in registry after UnregisterSKI")
	}
	if devices := registry.ListDevices(); len(devices) != 0 {
		t.Fatalf("ListDevices after UnregisterSKI = %v, want empty", devices)
	}

	select {
	case event := <-events:
		if event.Type != EventTypeDeviceTrustRemoved || event.SKI != wantSKI {
			t.Errorf("event = %+v, want type=%s ski=%s", event, EventTypeDeviceTrustRemoved, wantSKI)
		}
	default:
		t.Fatal("EventTypeDeviceTrustRemoved was not published after registry cleanup")
	}
	select {
	case event := <-events:
		t.Fatalf("unexpected second event: %+v", event)
	default:
	}
}
