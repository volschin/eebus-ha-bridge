package usecases

import (
	"errors"
	"log"

	eebusapi "github.com/enbility/eebus-go/api"
	mamrt "github.com/enbility/eebus-go/usecases/ma/mrt"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

var errRoomMonitoringNotInitialized = errors.New("room monitoring use case not initialized")

// RoomMonitoringWrapper wraps eebus-go's Monitoring of Room Temperature (MRT)
// use case and routes its updates through the bridge event bus.
type RoomMonitoringWrapper struct {
	uc       *mamrt.MRT
	bus      *eebus.EventBus
	registry *eebus.DeviceRegistry
	debug    bool
}

func NewRoomMonitoringWrapper(
	bus *eebus.EventBus,
	registry *eebus.DeviceRegistry,
	debug bool,
) *RoomMonitoringWrapper {
	return &RoomMonitoringWrapper{bus: bus, registry: registry, debug: debug}
}

func (w *RoomMonitoringWrapper) Setup(localEntity spineapi.EntityLocalInterface) {
	if localEntity == nil {
		return
	}
	w.uc = mamrt.NewMRT(localEntity, w.HandleEvent)
}

func (w *RoomMonitoringWrapper) UseCase() *mamrt.MRT { return w.uc }

func (w *RoomMonitoringWrapper) HandleEvent(
	ski string,
	device spineapi.DeviceRemoteInterface,
	entity spineapi.EntityRemoteInterface,
	event eebusapi.EventType,
) {
	if w.debug {
		log.Printf("[DEBUG] EEBUS room monitoring event received: ski=%s event=%s", ski, event)
	}
	if w.registry != nil {
		w.registry.UpsertObservation(ski, device, entity, "room_temperature_monitoring")
	}

	var eventType string
	switch event {
	case mamrt.DataUpdateTemperature:
		eventType = "room.temperature_updated"
	case mamrt.UseCaseSupportUpdate:
		eventType = "room.monitoring_support_updated"
	default:
		return
	}
	if w.bus != nil {
		w.bus.Publish(eebus.Event{SKI: ski, Type: eventType})
	}
}

// Temperature returns the negotiated room temperature in degrees Celsius.
func (w *RoomMonitoringWrapper) Temperature(ski string) (float64, error) {
	if w.uc == nil {
		return 0, errRoomMonitoringNotInitialized
	}
	entity := w.CompatibleEntity(ski)
	if entity == nil {
		return 0, eebusapi.ErrDataNotAvailable
	}
	return w.uc.Temperature(entity, model.UnitOfMeasurementTypedegC)
}

func (w *RoomMonitoringWrapper) CompatibleEntity(ski string) spineapi.EntityRemoteInterface {
	if w.uc == nil {
		return nil
	}
	want := eebus.NormalizeSKI(ski)
	for _, remote := range w.uc.RemoteEntitiesScenarios() {
		entity := remote.Entity
		if entity == nil || entity.Device() == nil {
			continue
		}
		if want == "" || eebus.NormalizeSKI(entity.Device().Ski()) == want {
			return entity
		}
	}
	return nil
}
