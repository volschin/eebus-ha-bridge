package usecases_test

import (
	"testing"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	mamrt "github.com/enbility/eebus-go/usecases/ma/mrt"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
)

func TestRoomMonitoringWrapperPublishesEvents(t *testing.T) {
	tests := []struct {
		name string
		in   eebusapi.EventType
		want eebus.EventType
	}{
		{name: "temperature", in: mamrt.DataUpdateTemperature, want: eebus.EventTypeRoomTemperatureUpdated},
		{name: "support", in: mamrt.UseCaseSupportUpdate, want: eebus.EventTypeRoomMonitoringSupportUpdated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus := eebus.NewEventBus()
			wrapper := usecases.NewRoomMonitoringWrapper(bus, nil, false)
			ch := bus.Subscribe()
			defer bus.Unsubscribe(ch)

			wrapper.HandleEvent("test-ski", nil, nil, tt.in)
			select {
			case event := <-ch:
				if event.SKI != "TESTSKI" || event.Type != tt.want {
					t.Fatalf("event = %+v", event)
				}
			case <-time.After(time.Second):
				t.Fatal("timeout waiting for room monitoring event")
			}
		})
	}
}

func TestRoomMonitoringWrapperIgnoresUnknownEvent(t *testing.T) {
	bus := eebus.NewEventBus()
	wrapper := usecases.NewRoomMonitoringWrapper(bus, nil, false)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	wrapper.HandleEvent("test-ski", nil, nil, eebusapi.EventType("unknown"))
	select {
	case event := <-ch:
		t.Fatalf("unexpected event = %+v", event)
	case <-time.After(20 * time.Millisecond):
	}
}
