package usecases

import (
	"errors"
	"log"

	eebusapi "github.com/enbility/eebus-go/api"
	mamdt "github.com/enbility/eebus-go/usecases/ma/mdt"
	mamot "github.com/enbility/eebus-go/usecases/ma/mot"
	mamrt "github.com/enbility/eebus-go/usecases/ma/mrt"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

var (
	errDHWMonitoringNotInitialized     = errors.New("DHW monitoring use case not initialized")
	errRoomMonitoringNotInitialized    = errors.New("room monitoring use case not initialized")
	errOutdoorMonitoringNotInitialized = errors.New("outdoor monitoring use case not initialized")
)

// temperatureUseCase is the shape shared by eebus-go's MA-* temperature
// monitoring clients (MDT, MRT, MOT).
type temperatureUseCase interface {
	eebusapi.UseCaseInterface
	Temperature(entity spineapi.EntityRemoteInterface, unit model.UnitOfMeasurementType) (float64, error)
	RemoteEntitiesScenarios() []eebusapi.RemoteEntityScenarios
}

// TemperatureMonitoringWrapper wraps one of eebus-go's MA-* temperature
// monitoring use cases (MDT/MRT/MOT) and routes its updates through the
// bridge event bus. The public constructors differ only in the underlying
// eebus-go client and the event vocabulary they publish.
type TemperatureMonitoringWrapper struct {
	uc       temperatureUseCase
	bus      *eebus.EventBus
	registry *eebus.DeviceRegistry
	debug    bool

	logLabel          string
	registryTag       string
	newUseCase        func(spineapi.EntityLocalInterface, eebusapi.EntityEventCallback) temperatureUseCase
	dataEvent         eebusapi.EventType
	supportEvent      eebusapi.EventType
	publishData       string
	publishSupport    string
	errNotInitialized error
}

// NewDHWMonitoringWrapper wraps Monitoring of DHW Temperature (MDT).
func NewDHWMonitoringWrapper(
	bus *eebus.EventBus,
	registry *eebus.DeviceRegistry,
	debug bool,
) *TemperatureMonitoringWrapper {
	return &TemperatureMonitoringWrapper{
		bus: bus, registry: registry, debug: debug,
		logLabel:    "DHW monitoring",
		registryTag: "dhw_monitoring",
		newUseCase: func(le spineapi.EntityLocalInterface, cb eebusapi.EntityEventCallback) temperatureUseCase {
			return mamdt.NewMDT(le, cb)
		},
		dataEvent:         mamdt.DataUpdateTemperature,
		supportEvent:      mamdt.UseCaseSupportUpdate,
		publishData:       "dhw.temperature_updated",
		publishSupport:    "dhw.monitoring_support_updated",
		errNotInitialized: errDHWMonitoringNotInitialized,
	}
}

// NewRoomMonitoringWrapper wraps Monitoring of Room Temperature (MRT).
func NewRoomMonitoringWrapper(
	bus *eebus.EventBus,
	registry *eebus.DeviceRegistry,
	debug bool,
) *TemperatureMonitoringWrapper {
	return &TemperatureMonitoringWrapper{
		bus: bus, registry: registry, debug: debug,
		logLabel:    "room monitoring",
		registryTag: "room_temperature_monitoring",
		newUseCase: func(le spineapi.EntityLocalInterface, cb eebusapi.EntityEventCallback) temperatureUseCase {
			return mamrt.NewMRT(le, cb)
		},
		dataEvent:         mamrt.DataUpdateTemperature,
		supportEvent:      mamrt.UseCaseSupportUpdate,
		publishData:       "room.temperature_updated",
		publishSupport:    "room.monitoring_support_updated",
		errNotInitialized: errRoomMonitoringNotInitialized,
	}
}

// NewOutdoorMonitoringWrapper wraps Monitoring of Outdoor Temperature (MOT).
func NewOutdoorMonitoringWrapper(
	bus *eebus.EventBus,
	registry *eebus.DeviceRegistry,
	debug bool,
) *TemperatureMonitoringWrapper {
	return &TemperatureMonitoringWrapper{
		bus: bus, registry: registry, debug: debug,
		logLabel:    "outdoor monitoring",
		registryTag: "outdoor_temperature_monitoring",
		newUseCase: func(le spineapi.EntityLocalInterface, cb eebusapi.EntityEventCallback) temperatureUseCase {
			return mamot.NewMOT(le, cb)
		},
		dataEvent:         mamot.DataUpdateTemperature,
		supportEvent:      mamot.UseCaseSupportUpdate,
		publishData:       "outdoor.temperature_updated",
		publishSupport:    "outdoor.monitoring_support_updated",
		errNotInitialized: errOutdoorMonitoringNotInitialized,
	}
}

func (w *TemperatureMonitoringWrapper) Setup(localEntity spineapi.EntityLocalInterface) {
	if localEntity == nil {
		return
	}
	w.uc = w.newUseCase(localEntity, w.HandleEvent)
}

func (w *TemperatureMonitoringWrapper) UseCase() eebusapi.UseCaseInterface { return w.uc }

func (w *TemperatureMonitoringWrapper) HandleEvent(
	ski string,
	device spineapi.DeviceRemoteInterface,
	entity spineapi.EntityRemoteInterface,
	event eebusapi.EventType,
) {
	if w.debug {
		log.Printf("[DEBUG] EEBUS %s event received: ski=%s event=%s", w.logLabel, ski, event)
	}
	if w.registry != nil {
		w.registry.UpsertObservation(ski, device, entity, w.registryTag)
	}

	var eventType string
	switch event {
	case w.dataEvent:
		eventType = w.publishData
	case w.supportEvent:
		eventType = w.publishSupport
	default:
		return
	}
	if w.bus != nil {
		w.bus.Publish(eebus.Event{SKI: ski, Type: eventType})
	}
}

// Temperature returns the negotiated temperature reading in degrees Celsius.
func (w *TemperatureMonitoringWrapper) Temperature(ski string) (float64, error) {
	if w.uc == nil {
		return 0, w.errNotInitialized
	}
	entity := w.CompatibleEntity(ski)
	if entity == nil {
		return 0, eebusapi.ErrDataNotAvailable
	}
	return w.uc.Temperature(entity, model.UnitOfMeasurementTypedegC)
}

func (w *TemperatureMonitoringWrapper) CompatibleEntity(ski string) spineapi.EntityRemoteInterface {
	if w.uc == nil {
		return nil
	}
	return compatibleEntity(w.uc.RemoteEntitiesScenarios(), ski)
}
