package usecases

import (
	"errors"
	"log"

	eebusapi "github.com/enbility/eebus-go/api"
	mamdt "github.com/enbility/eebus-go/usecases/ma/mdt"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

var errDHWMonitoringNotInitialized = errors.New("DHW monitoring use case not initialized")

// DHWMonitoringWrapper wraps eebus-go's Monitoring of DHW Temperature (MDT)
// use case and routes its updates through the bridge event bus.
type DHWMonitoringWrapper struct {
	uc       *mamdt.MDT
	bus      *eebus.EventBus
	registry *eebus.DeviceRegistry
	debug    bool
}

func NewDHWMonitoringWrapper(
	bus *eebus.EventBus,
	registry *eebus.DeviceRegistry,
	debug bool,
) *DHWMonitoringWrapper {
	return &DHWMonitoringWrapper{bus: bus, registry: registry, debug: debug}
}

func (w *DHWMonitoringWrapper) Setup(localEntity spineapi.EntityLocalInterface) {
	if localEntity == nil {
		return
	}
	w.uc = mamdt.NewMDT(localEntity, w.HandleEvent)
}

func (w *DHWMonitoringWrapper) UseCase() *mamdt.MDT { return w.uc }

func (w *DHWMonitoringWrapper) HandleEvent(
	ski string,
	device spineapi.DeviceRemoteInterface,
	entity spineapi.EntityRemoteInterface,
	event eebusapi.EventType,
) {
	if w.debug {
		log.Printf("[DEBUG] EEBUS DHW monitoring event received: ski=%s event=%s", ski, event)
	}
	if w.registry != nil {
		w.registry.UpsertObservation(ski, device, entity, "dhw_monitoring")
	}

	var eventType string
	switch event {
	case mamdt.DataUpdateTemperature:
		eventType = "dhw.temperature_updated"
	case mamdt.UseCaseSupportUpdate:
		eventType = "dhw.monitoring_support_updated"
	default:
		return
	}
	if w.bus != nil {
		w.bus.Publish(eebus.Event{SKI: ski, Type: eventType})
	}
}

// Temperature returns the negotiated DHW circuit temperature in degrees Celsius.
func (w *DHWMonitoringWrapper) Temperature(ski string) (float64, error) {
	if w.uc == nil {
		return 0, errDHWMonitoringNotInitialized
	}
	entity := w.CompatibleEntity(ski)
	if entity == nil {
		return 0, eebusapi.ErrDataNotAvailable
	}
	return w.uc.Temperature(entity, model.UnitOfMeasurementTypedegC)
}

func (w *DHWMonitoringWrapper) CompatibleEntity(ski string) spineapi.EntityRemoteInterface {
	if w.uc == nil {
		return nil
	}
	return compatibleEntity(w.uc.RemoteEntitiesScenarios(), ski)
}
