package usecases_test

import (
	"testing"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	mamot "github.com/enbility/eebus-go/usecases/ma/mot"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
)

func TestOutdoorMonitoringWrapperPublishesEvents(t *testing.T) {
	tests := []struct {
		name string
		in   eebusapi.EventType
		want eebus.EventType
	}{
		{name: "temperature", in: mamot.DataUpdateTemperature, want: eebus.EventTypeOutdoorTemperatureUpdated},
		{name: "support", in: mamot.UseCaseSupportUpdate, want: eebus.EventTypeOutdoorMonitoringSupportUpdated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus := eebus.NewEventBus()
			wrapper := usecases.NewOutdoorMonitoringWrapper(bus, nil, false)
			ch := bus.Subscribe()
			defer bus.Unsubscribe(ch)

			wrapper.HandleEvent("test-ski", nil, nil, tt.in)
			select {
			case event := <-ch:
				if event.SKI != "test-ski" || event.Type != tt.want {
					t.Fatalf("event = %+v", event)
				}
			case <-time.After(time.Second):
				t.Fatal("timeout waiting for outdoor monitoring event")
			}
		})
	}
}

func TestOutdoorMonitoringWrapperIgnoresUnknownEvent(t *testing.T) {
	bus := eebus.NewEventBus()
	wrapper := usecases.NewOutdoorMonitoringWrapper(bus, nil, false)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	wrapper.HandleEvent("test-ski", nil, nil, eebusapi.EventType("unknown"))
	select {
	case event := <-ch:
		t.Fatalf("unexpected event = %+v", event)
	case <-time.After(20 * time.Millisecond):
	}
}
