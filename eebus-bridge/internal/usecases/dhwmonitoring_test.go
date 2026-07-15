package usecases_test

import (
	"testing"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	mamdt "github.com/enbility/eebus-go/usecases/ma/mdt"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
)

func TestDHWMonitoringWrapperPublishesTemperatureUpdate(t *testing.T) {
	bus := eebus.NewEventBus()
	wrapper := usecases.NewDHWMonitoringWrapper(bus, nil, false)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	wrapper.HandleEvent("test-ski", nil, nil, mamdt.DataUpdateTemperature)

	select {
	case event := <-ch:
		if event.SKI != "TESTSKI" || event.Type != eebus.EventTypeDHWTemperatureUpdated {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for DHW temperature event")
	}
}

func TestDHWMonitoringWrapperIgnoresUnknownEvent(t *testing.T) {
	bus := eebus.NewEventBus()
	wrapper := usecases.NewDHWMonitoringWrapper(bus, nil, false)
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	wrapper.HandleEvent("test-ski", nil, nil, eebusapi.EventType("unknown"))

	select {
	case event := <-ch:
		t.Fatalf("unexpected event = %+v", event)
	case <-time.After(20 * time.Millisecond):
	}
}
