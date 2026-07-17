package grpc

import (
	"testing"
	"time"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

type deviceStateTemperatureReader struct {
	value float64
	err   error
}

func (r deviceStateTemperatureReader) Temperature(string) (float64, error) {
	return r.value, r.err
}

func TestDeviceStateEnvelopeAttachesBestEffortPayload(t *testing.T) {
	bus := eebus.NewEventBus()
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice(testValidSKI, eebus.DeviceInfo{})
	hvacService := NewHVACService(
		nil,
		nil,
		deviceStateTemperatureReader{value: 20.5},
		bus,
		registry,
	)
	service := NewDeviceService(
		eebus.NewCallbacks(bus, false),
		bus,
		"local-ski",
		registry,
		nil,
		WithDeviceStatePayloads(DeviceStatePayloadSources{HVAC: hvacService}),
	)

	event := service.deviceStateEnvelope(eebus.Event{
		SKI:        testValidSKI,
		Type:       eebus.EventTypeRoomTemperatureUpdated,
		Revision:   42,
		OccurredAt: time.Now(),
	})

	hvac := event.GetHvac()
	state := hvac.GetState()
	if event.Revision != 42 ||
		hvac.GetEventType() != pb.RoomHeatingEventType_ROOM_HEATING_EVENT_CURRENT_TEMPERATURE_UPDATED ||
		state == nil ||
		state.GetCurrentTemperatureCelsius() != 20.5 {
		t.Fatalf("device state envelope = %+v", event)
	}
}
