package usecases

import (
	"errors"
	"log"

	eebusapi "github.com/enbility/eebus-go/api"
	mamot "github.com/enbility/eebus-go/usecases/ma/mot"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

var errOutdoorMonitoringNotInitialized = errors.New("outdoor monitoring use case not initialized")

// OutdoorMonitoringWrapper wraps eebus-go's Monitoring of Outdoor Temperature
// (MOT) use case and routes its updates through the bridge event bus.
type OutdoorMonitoringWrapper struct {
	uc       *mamot.MOT
	bus      *eebus.EventBus
	registry *eebus.DeviceRegistry
	debug    bool
}

func NewOutdoorMonitoringWrapper(
	bus *eebus.EventBus,
	registry *eebus.DeviceRegistry,
	debug bool,
) *OutdoorMonitoringWrapper {
	return &OutdoorMonitoringWrapper{bus: bus, registry: registry, debug: debug}
}

func (w *OutdoorMonitoringWrapper) Setup(localEntity spineapi.EntityLocalInterface) {
	if localEntity == nil {
		return
	}
	w.uc = mamot.NewMOT(localEntity, w.HandleEvent)
}

func (w *OutdoorMonitoringWrapper) UseCase() *mamot.MOT { return w.uc }

func (w *OutdoorMonitoringWrapper) HandleEvent(
	ski string,
	device spineapi.DeviceRemoteInterface,
	entity spineapi.EntityRemoteInterface,
	event eebusapi.EventType,
) {
	if w.debug {
		log.Printf("[DEBUG] EEBUS outdoor monitoring event received: ski=%s event=%s", ski, event)
	}
	if w.registry != nil {
		w.registry.UpsertObservation(ski, device, entity, "outdoor_temperature_monitoring")
	}

	var eventType string
	switch event {
	case mamot.DataUpdateTemperature:
		eventType = "outdoor.temperature_updated"
	case mamot.UseCaseSupportUpdate:
		eventType = "outdoor.monitoring_support_updated"
	default:
		return
	}
	if w.bus != nil {
		w.bus.Publish(eebus.Event{SKI: ski, Type: eventType})
	}
}

// Temperature returns the negotiated outdoor temperature in degrees Celsius.
func (w *OutdoorMonitoringWrapper) Temperature(ski string) (float64, error) {
	if w.uc == nil {
		return 0, errOutdoorMonitoringNotInitialized
	}
	entity := w.CompatibleEntity(ski)
	if entity == nil {
		return 0, eebusapi.ErrDataNotAvailable
	}
	return w.uc.Temperature(entity, model.UnitOfMeasurementTypedegC)
}

func (w *OutdoorMonitoringWrapper) CompatibleEntity(ski string) spineapi.EntityRemoteInterface {
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
