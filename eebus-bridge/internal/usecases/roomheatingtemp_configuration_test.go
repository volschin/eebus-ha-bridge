package usecases

import (
	"context"
	"errors"
	"testing"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	cacrht "github.com/enbility/eebus-go/usecases/ca/crht"
	ucmocks "github.com/enbility/eebus-go/usecases/mocks"
	spineapi "github.com/enbility/spine-go/api"
	spinemocks "github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

func TestUpstreamRoomHeatingTemperatureConfigurationSelectsCRHT(t *testing.T) {
	facade := NewUpstreamRoomHeatingTemperatureConfiguration(
		clientUsecaseLocalEntity(t),
		eebus.NewEventBus(),
		nil,
		false,
	)
	client, ok := facade.UseCase().(*cacrht.CRHT)
	if !ok {
		t.Fatalf("UseCase() = %T, want *crht.CRHT", facade.UseCase())
	}
	if client.EventCB == nil {
		t.Fatal("upstream CRHT has no event callback")
	}
	legacy, ok := facade.writer.(*RoomHeatingTemperature)
	if !ok {
		t.Fatalf("writer = %T, want *RoomHeatingTemperature", facade.writer)
	}
	if legacy.UseCaseBase != nil || legacy.bus != nil || legacy.registry != nil {
		t.Fatalf("legacy writer retained negotiation or event state: %+v", legacy)
	}
}

type phase4RoomHeatingTemperatureWriter struct {
	entity spineapi.EntityRemoteInterface
	value  float64
	err    error
}

func (w *phase4RoomHeatingTemperatureWriter) Write(
	_ context.Context,
	entity spineapi.EntityRemoteInterface,
	value float64,
) error {
	w.entity = entity
	w.value = value
	return w.err
}

func TestCRHTConfigurationFacadeMapsUpstreamStateAndRetainsSelectedWriter(t *testing.T) {
	device := spinemocks.NewDeviceRemoteInterface(t)
	device.EXPECT().Ski().Return("ab:cd").Maybe()
	entity := spinemocks.NewEntityRemoteInterface(t)
	entity.EXPECT().Device().Return(device).Maybe()
	client := ucmocks.NewCaCRHTInterface(t)
	client.EXPECT().RemoteEntitiesScenarios().Return([]eebusapi.RemoteEntityScenarios{{
		Entity: entity, Scenarios: []uint{1},
	}})
	client.EXPECT().State(entity).Return(ucapi.RoomHeatingSetpointState{
		Id:           7,
		Value:        21,
		MinValue:     5,
		MaxValue:     30,
		StepSize:     0.5,
		IsActive:     true,
		IsChangeable: false,
		IsWritable:   true,
	}, nil)
	writer := &phase4RoomHeatingTemperatureWriter{}
	facade := newCRHTConfigurationFacade(
		client,
		crhtEntityResolver{client: client},
		upstreamRoomHeatingTemperatureReader{client: client},
		writer,
	)

	resolution := facade.CompatibleEntity("ABCD")
	if resolution.Entity != entity || resolution.DeviceCount != 1 {
		t.Fatalf("CompatibleEntity() = %+v, want upstream CRHT entity", resolution)
	}
	state, err := facade.State(entity)
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	want := (RoomHeatingSetpoint{Value: 21, Minimum: 5, Maximum: 30, Step: 0.5, Writable: true})
	if state != want {
		t.Fatalf("State() = %+v, want %+v", state, want)
	}
	if err := facade.Write(context.Background(), entity, 21.5); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if writer.entity != entity || writer.value != 21.5 {
		t.Fatalf("writer entity/value = %p/%v, want %p/21.5", writer.entity, writer.value, entity)
	}
}

func TestUpstreamRoomHeatingTemperatureReaderMapsIncompleteState(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCRHTInterface(t)
	client.EXPECT().State(entity).Return(ucapi.RoomHeatingSetpointState{}, eebusapi.ErrDataNotAvailable)

	_, err := (upstreamRoomHeatingTemperatureReader{client: client}).State(entity)
	if !errors.Is(err, ErrRoomHeatingDataUnavailable) {
		t.Fatalf("State() error = %v, want ErrRoomHeatingDataUnavailable", err)
	}
	if _, err := (upstreamRoomHeatingTemperatureReader{}).State(entity); !errors.Is(err, ErrRoomHeatingDataUnavailable) {
		t.Fatalf("nil reader State() error = %v", err)
	}
}

type phase4RoomHeatingTemperatureReader struct {
	state RoomHeatingSetpoint
	err   error
}

func (r *phase4RoomHeatingTemperatureReader) State(
	spineapi.EntityRemoteInterface,
) (RoomHeatingSetpoint, error) {
	return r.state, r.err
}

func TestCRHTConfigurationFacadeMapsSupportAndCompleteDataEventsOnce(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)
	reader := &phase4RoomHeatingTemperatureReader{err: ErrRoomHeatingDataUnavailable}
	facade := &CRHTConfigurationFacade{reader: reader, bus: bus}

	facade.HandleEvent("ab:cd", nil, nil, cacrht.DataUpdateSetpoints)
	select {
	case event := <-ch:
		t.Fatalf("incomplete state published event %+v", event)
	default:
	}

	reader.err = nil
	reader.state = RoomHeatingSetpoint{Value: 21, Minimum: 5, Maximum: 30, Step: 0.5}
	facade.HandleEvent("ab:cd", nil, nil, cacrht.DataUpdateSetpointConstraints)
	assertRoomHeatingTemperatureEvent(t, ch, eebus.EventTypeRoomHeatingSetpointUpdated)

	facade.HandleEvent("ab:cd", nil, nil, cacrht.UseCaseSupportUpdate)
	assertRoomHeatingTemperatureEvent(t, ch, eebus.EventTypeRoomHeatingUseCaseSupportUpdated)

	select {
	case event := <-ch:
		t.Fatalf("callback published duplicate event %+v", event)
	default:
	}
}

func assertRoomHeatingTemperatureEvent(
	t *testing.T,
	ch <-chan eebus.Event,
	want eebus.EventType,
) {
	t.Helper()
	select {
	case event := <-ch:
		if event.SKI != "ABCD" || event.Type != want {
			t.Fatalf("event = %+v, want ski ABCD type %s", event, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", want)
	}
}

func TestCRHTConfigurationFacadeRecordsObservationsAndIgnoresUnknownEvents(t *testing.T) {
	bus := eebus.NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)
	registry := eebus.NewDeviceRegistry()
	device := spinemocks.NewDeviceRemoteInterface(t)
	device.EXPECT().Ski().Return("ab:cd").Maybe()
	entity := spinemocks.NewEntityRemoteInterface(t)
	entity.EXPECT().Device().Return(device).Maybe()
	entity.EXPECT().Address().Return(&model.EntityAddressType{}).Maybe()
	entity.EXPECT().EntityType().Return(model.EntityTypeTypeHvacRoom).Maybe()
	entity.EXPECT().Features().Return(nil).Maybe()
	facade := &CRHTConfigurationFacade{
		reader:   &phase4RoomHeatingTemperatureReader{state: RoomHeatingSetpoint{Value: 21}},
		bus:      bus,
		registry: registry,
		debug:    true,
	}

	facade.HandleEvent("ab:cd", device, entity, cacrht.DataUpdateSetpoints)
	assertRoomHeatingTemperatureEvent(t, ch, eebus.EventTypeRoomHeatingSetpointUpdated)
	if entities := registry.Entities("ABCD"); len(entities) == 0 {
		t.Fatal("data update did not record a registry observation")
	}

	facade.HandleEvent("ab:cd", device, entity, eebusapi.EventType("bridge-unknown-event"))
	select {
	case event := <-ch:
		t.Fatalf("unknown event published %+v", event)
	default:
	}

	var nilFacade *CRHTConfigurationFacade
	nilFacade.HandleEvent("ab:cd", device, entity, cacrht.DataUpdateSetpoints)
}

func TestCRHTConfigurationFacadeFailsClosedWhenIncomplete(t *testing.T) {
	var nilFacade *CRHTConfigurationFacade
	if nilFacade.UseCase() != nil {
		t.Fatal("nil facade returned a use case")
	}
	if resolution := nilFacade.CompatibleEntity("ABCD"); resolution != (eebus.EntityResolution{}) {
		t.Fatalf("CompatibleEntity() = %+v", resolution)
	}
	if _, err := nilFacade.State(nil); !errors.Is(err, ErrRoomHeatingDataUnavailable) {
		t.Fatalf("State() error = %v", err)
	}
	if err := nilFacade.Write(context.Background(), nil, 21); !errors.Is(err, ErrRoomHeatingNotWritable) {
		t.Fatalf("Write() error = %v", err)
	}

	empty := NewUpstreamRoomHeatingTemperatureConfiguration(nil, nil, nil, false)
	if empty.UseCase() != nil {
		t.Fatal("nil local entity initialized CRHT")
	}
	if _, err := empty.State(nil); !errors.Is(err, ErrRoomHeatingDataUnavailable) {
		t.Fatalf("empty State() error = %v", err)
	}
	if err := empty.Write(context.Background(), nil, 21); !errors.Is(err, ErrRoomHeatingNotWritable) {
		t.Fatalf("empty Write() error = %v", err)
	}
	if resolution := (crhtEntityResolver{}).CompatibleEntity("ABCD"); resolution != (eebus.EntityResolution{}) {
		t.Fatalf("empty resolver = %+v", resolution)
	}
}
