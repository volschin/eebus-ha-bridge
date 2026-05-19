package usecases_test

import (
	"testing"
	"time"

	mampc "github.com/enbility/eebus-go/usecases/ma/mpc"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
)

func TestMonitoringEventRouting(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	monWrapper := usecases.NewMonitoringWrapper(bus, nil, false)

	monWrapper.HandleEvent("test-ski", nil, nil, mampc.DataUpdatePower)

	select {
	case evt := <-ch:
		if evt.Type != "monitoring.power_updated" {
			t.Errorf("Type = %q, want monitoring.power_updated", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for monitoring event")
	}
}

func TestMonitoringEnergyConsumedEventRouting(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	monWrapper := usecases.NewMonitoringWrapper(bus, nil, false)
	monWrapper.HandleEvent("ski-2", nil, nil, mampc.DataUpdateEnergyConsumed)

	select {
	case evt := <-ch:
		if evt.Type != "monitoring.energy_consumed_updated" {
			t.Errorf("Type = %q, want monitoring.energy_consumed_updated", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for monitoring energy consumed event")
	}
}

func TestMonitoringFrequencyEventRouting(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	monWrapper := usecases.NewMonitoringWrapper(bus, nil, false)
	monWrapper.HandleEvent("ski-3", nil, nil, mampc.DataUpdateFrequency)

	select {
	case evt := <-ch:
		if evt.Type != "monitoring.frequency_updated" {
			t.Errorf("Type = %q, want monitoring.frequency_updated", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for monitoring frequency event")
	}
}

func TestMonitoringUnknownEventIgnored(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	monWrapper := usecases.NewMonitoringWrapper(bus, nil, false)
	monWrapper.HandleEvent("ski-x", nil, nil, "unknown-event-type")

	select {
	case evt := <-ch:
		t.Errorf("unexpected event published: %+v", evt)
	case <-time.After(50 * time.Millisecond):
		// expected: no event published
	}
}
