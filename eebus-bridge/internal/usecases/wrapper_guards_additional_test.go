package usecases

import (
	"errors"
	"testing"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	cemohpcf "github.com/enbility/eebus-go/usecases/cem/ohpcf"
	eglpc "github.com/enbility/eebus-go/usecases/eg/lpc"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

func TestLPCBeforeSetupReturnsInitializationDefaults(t *testing.T) {
	w := NewLPCWrapper(nil, nil, false)
	w.Setup(nil)
	if w.UseCase() != nil || w.IsHeartbeatRunning() || w.IsHeartbeatWithinDuration(nil) {
		t.Fatal("LPC wrapper reported initialized state before Setup")
	}
	if got := w.CompatibleEntityForScenario("ab:cd", 1); got.Entity != nil || got.Ambiguous() {
		t.Fatalf("scenario resolution = %+v, want empty", got)
	}

	checks := []struct {
		name string
		call func() error
	}{
		{"consumption limit", func() error { _, err := w.ConsumptionLimit(nil); return err }},
		{"write consumption limit", func() error { return w.WriteConsumptionLimit(nil, ucapi.LoadLimit{}) }},
		{"failsafe power", func() error { _, err := w.FailsafeConsumptionActivePowerLimit(nil); return err }},
		{"write failsafe power", func() error { return w.WriteFailsafeConsumptionActivePowerLimit(nil, 1) }},
		{"failsafe duration", func() error { _, err := w.FailsafeDurationMinimum(nil); return err }},
		{"write failsafe duration", func() error { return w.WriteFailsafeDurationMinimum(nil, time.Hour) }},
		{"start heartbeat", func() error { return w.StartHeartbeat("ab:cd") }},
		{"stop heartbeat", w.StopHeartbeat},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); !errors.Is(err, errLPCNotInitialized) {
				t.Fatalf("error = %v, want %v", err, errLPCNotInitialized)
			}
		})
	}
}

func TestOHPCFBeforeSetupReturnsInitializationErrors(t *testing.T) {
	w := NewOHPCFWrapper(nil, nil, false)
	w.Setup(nil)
	if w.UseCase() != nil {
		t.Fatal("OHPCF wrapper returned a use case before Setup")
	}
	if got := w.CompatibleEntity("ab:cd"); got.Entity != nil || got.Ambiguous() {
		t.Fatalf("entity resolution = %+v, want empty", got)
	}
	checks := []struct {
		name string
		call func() error
	}{
		{"available", func() error { _, err := w.OptionalPowerConsumptionAvailable(nil); return err }},
		{"estimate", func() error { _, err := w.RequestedPowerEstimate(nil); return err }},
		{"maximum", func() error { _, err := w.RequestedPowerMax(nil); return err }},
		{"stoppable", func() error { _, err := w.ConsumptionIsStoppable(nil); return err }},
		{"pausable", func() error { _, err := w.ConsumptionIsPausable(nil); return err }},
		{"state", func() error { _, err := w.ConsumptionState(nil); return err }},
		{"start time", func() error { _, err := w.ConsumptionStartTime(nil); return err }},
		{"minimal run", func() error { _, err := w.MinimalRunDuration(nil); return err }},
		{"minimal pause", func() error { _, err := w.MinimalPauseDuration(nil); return err }},
		{"schedule", func() error { return w.Schedule(nil, time.Now()) }},
		{"pause", func() error { return w.Pause(nil) }},
		{"resume", func() error { return w.Resume(nil) }},
		{"abort", func() error { return w.Abort(nil) }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); !errors.Is(err, errOHPCFNotInitialized) {
				t.Fatalf("error = %v, want %v", err, errOHPCFNotInitialized)
			}
		})
	}
}

func TestOHPCFRoutesEveryDataAndSupportEvent(t *testing.T) {
	tests := []struct {
		name string
		in   eebusapi.EventType
		want eebus.EventType
	}{
		{"support", cemohpcf.UseCaseSupportUpdate, eebus.EventTypeOHPCFUseCaseSupportUpdated},
		{"stoppable", cemohpcf.DataUpdateConsumptionIsStoppable, eebus.EventTypeOHPCFConsumptionStoppableUpdated},
		{"pausable", cemohpcf.DataUpdateConsumptionIsPausable, eebus.EventTypeOHPCFConsumptionPausableUpdated},
		{"start time", cemohpcf.DataUpdateConsumptionStartTime, eebus.EventTypeOHPCFConsumptionStartTimeUpdated},
		{"estimate", cemohpcf.DataUpdateRequestedPowerEstimate, eebus.EventTypeOHPCFRequestedPowerEstimateUpdated},
		{"maximum", cemohpcf.DataUpdateRequestedPowerMax, eebus.EventTypeOHPCFRequestedPowerMaxUpdated},
		{"minimal run", cemohpcf.DataUpdateMinimalRunDuration, eebus.EventTypeOHPCFMinimalRunDurationUpdated},
		{"minimal pause", cemohpcf.DataUpdateMinimalPauseDuration, eebus.EventTypeOHPCFMinimalPauseDurationUpdated},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bus := eebus.NewEventBus()
			channel := bus.Subscribe()
			defer bus.Unsubscribe(channel)
			w := NewOHPCFWrapper(bus, nil, false)
			w.HandleEvent("ab:cd", nil, nil, test.in)
			select {
			case event := <-channel:
				if event.Type != test.want || event.SKI != "ABCD" {
					t.Fatalf("event = %+v, want type %q", event, test.want)
				}
			case <-time.After(time.Second):
				t.Fatal("timeout waiting for event")
			}
		})
	}
	w := NewOHPCFWrapper(nil, nil, true)
	w.HandleEvent("ab:cd", nil, nil, cemohpcf.DataUpdateConsumptionState)
	w.HandleEvent("ab:cd", nil, nil, "unknown")
}

func TestLPCRoutesEventsWithoutBusAndIgnoresUnknown(t *testing.T) {
	w := NewLPCWrapper(nil, nil, true)
	for _, event := range []eebusapi.EventType{
		eglpc.DataUpdateLimit,
		eglpc.DataUpdateFailsafeConsumptionActivePowerLimit,
		eglpc.DataUpdateFailsafeDurationMinimum,
		eglpc.DataUpdateHeartbeat,
		eglpc.UseCaseSupportUpdate,
		"unknown",
	} {
		w.HandleEvent("ab:cd", nil, nil, event)
	}
}

func TestTemperatureMonitoringBeforeSetupAndSupportEvents(t *testing.T) {
	tests := []struct {
		name string
		new  func(*eebus.EventBus, *eebus.DeviceRegistry, bool) *TemperatureMonitoringWrapper
		want eebus.EventType
	}{
		{"DHW", NewDHWMonitoringWrapper, eebus.EventTypeDHWMonitoringSupportUpdated},
		{"room", NewRoomMonitoringWrapper, eebus.EventTypeRoomMonitoringSupportUpdated},
		{"outdoor", NewOutdoorMonitoringWrapper, eebus.EventTypeOutdoorMonitoringSupportUpdated},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bus := eebus.NewEventBus()
			channel := bus.Subscribe()
			defer bus.Unsubscribe(channel)
			w := test.new(bus, nil, true)
			w.Setup(nil)
			if w.UseCase() != nil {
				t.Fatal("UseCase before Setup is not nil")
			}
			if _, err := w.Temperature("ab:cd"); !errors.Is(err, w.errNotInitialized) {
				t.Fatalf("Temperature error = %v, want %v", err, w.errNotInitialized)
			}
			if got := w.CompatibleEntity("ab:cd"); got.Entity != nil || got.Ambiguous() {
				t.Fatalf("CompatibleEntity = %+v, want empty", got)
			}
			w.HandleEvent("ab:cd", nil, nil, w.supportEvent)
			select {
			case event := <-channel:
				if event.Type != test.want || event.SKI != "ABCD" {
					t.Fatalf("event = %+v, want %q", event, test.want)
				}
			case <-time.After(time.Second):
				t.Fatal("timeout waiting for support event")
			}
			w.HandleEvent("ab:cd", nil, nil, "unknown")
		})
	}
}
