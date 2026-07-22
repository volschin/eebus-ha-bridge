package usecases

import (
	"context"
	"errors"
	"fmt"
	"log"

	eebusapi "github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	mamrhsf "github.com/enbility/eebus-go/usecases/ma/mrhsf"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

var errRoomHeatingSystemFunctionMonitoringNotInitialized = errors.New("room heating system function monitoring use case not initialized")

type roomHeatingSystemFunctionMonitoringUseCase interface {
	eebusapi.UseCaseInterface
	OperationModes(spineapi.EntityRemoteInterface) ([]ucapi.HvacOperationModeType, error)
	CurrentOperationMode(spineapi.EntityRemoteInterface) (ucapi.HvacOperationModeType, error)
	RemoteEntitiesScenarios() []eebusapi.RemoteEntityScenarios
}

// RoomHeatingSystemFunctionMonitoring wraps eebus-go's MRHSF client and owns
// room-heating operation-mode reads and user-visible state events.
type RoomHeatingSystemFunctionMonitoring struct {
	uc       roomHeatingSystemFunctionMonitoringUseCase
	bus      *eebus.EventBus
	registry *eebus.DeviceRegistry
	debug    bool
}

func NewRoomHeatingSystemFunctionMonitoring(
	bus *eebus.EventBus,
	registry *eebus.DeviceRegistry,
	debug bool,
) *RoomHeatingSystemFunctionMonitoring {
	return &RoomHeatingSystemFunctionMonitoring{bus: bus, registry: registry, debug: debug}
}

func (w *RoomHeatingSystemFunctionMonitoring) Setup(localEntity spineapi.EntityLocalInterface) {
	if localEntity == nil {
		return
	}
	w.uc = mamrhsf.NewMRHSF(localEntity, w.HandleEvent)
}

func (w *RoomHeatingSystemFunctionMonitoring) UseCase() eebusapi.UseCaseInterface { return w.uc }

func (w *RoomHeatingSystemFunctionMonitoring) HandleEvent(
	ski string,
	device spineapi.DeviceRemoteInterface,
	entity spineapi.EntityRemoteInterface,
	event eebusapi.EventType,
) {
	if w.debug {
		log.Printf("[DEBUG] EEBUS room heating system function monitoring event received: ski=%s event=%s", ski, event)
	}

	var eventType eebus.EventType
	switch event {
	case mamrhsf.UseCaseSupportUpdate:
		eventType = eebus.EventTypeRoomHeatingSystemFunctionSupportUpdated
		recordCapabilitySupport(
			w.registry,
			ski,
			device,
			entity,
			w.CompatibleEntity(observationSKI(ski, device)),
			"room_heating_system_function_monitoring",
			eebus.CapabilityRoomHeating,
		)
	case mamrhsf.DataUpdateOperationMode:
		eventType = eebus.EventTypeRoomHeatingSystemFunctionUpdated
		if w.registry != nil {
			w.registry.UpsertObservation(ski, device, entity, "room_heating_system_function_monitoring")
		}
	default:
		return
	}
	if w.bus != nil {
		w.bus.Publish(eebus.Event{SKI: ski, Type: eventType})
	}
}

func (w *RoomHeatingSystemFunctionMonitoring) CompatibleEntity(ski string) eebus.EntityResolution {
	if w.uc == nil {
		return eebus.EntityResolution{}
	}
	return compatibleEntity(w.uc.RemoteEntitiesScenarios(), ski)
}

func (w *RoomHeatingSystemFunctionMonitoring) State(
	entity spineapi.EntityRemoteInterface,
) (RoomHeatingSystemFunctionState, error) {
	if w.uc == nil {
		return RoomHeatingSystemFunctionState{}, errRoomHeatingSystemFunctionMonitoringNotInitialized
	}
	modes, err := w.uc.OperationModes(entity)
	if err != nil {
		return RoomHeatingSystemFunctionState{}, fmt.Errorf("%w: operation modes: %v", ErrRoomHeatingSysFnDataUnavailable, err)
	}
	current, err := w.uc.CurrentOperationMode(entity)
	if err != nil {
		return RoomHeatingSystemFunctionState{}, fmt.Errorf("%w: current operation mode: %v", ErrRoomHeatingSysFnDataUnavailable, err)
	}
	return RoomHeatingSystemFunctionState{
		OperationMode:  string(current),
		AvailableModes: uniqueOperationModes(modes),
	}, nil
}

type roomHeatingSystemFunctionReader interface {
	CompatibleEntity(string) eebus.EntityResolution
	State(spineapi.EntityRemoteInterface) (RoomHeatingSystemFunctionState, error)
}

type roomHeatingSystemFunctionWriter interface {
	CompatibleEntity(string) eebus.EntityResolution
	State(spineapi.EntityRemoteInterface) (RoomHeatingSystemFunctionState, error)
	WriteOperationMode(context.Context, spineapi.EntityRemoteInterface, string) error
}

// RoomHeatingSystemFunctionAdapter combines MRHSF reads with the legacy CRHSF
// configuration path. The two independently negotiated entities are composed
// by normalized device SKI so a stale monitoring entity is never used for a
// configuration write after reconnect.
type RoomHeatingSystemFunctionAdapter struct {
	monitoring    roomHeatingSystemFunctionReader
	configuration roomHeatingSystemFunctionWriter
}

func NewRoomHeatingSystemFunctionAdapter(
	monitoring roomHeatingSystemFunctionReader,
	configuration roomHeatingSystemFunctionWriter,
) *RoomHeatingSystemFunctionAdapter {
	return &RoomHeatingSystemFunctionAdapter{monitoring: monitoring, configuration: configuration}
}

func (a *RoomHeatingSystemFunctionAdapter) CompatibleEntity(ski string) eebus.EntityResolution {
	if a == nil || a.monitoring == nil {
		return eebus.EntityResolution{}
	}
	return a.monitoring.CompatibleEntity(ski)
}

func (a *RoomHeatingSystemFunctionAdapter) State(
	entity spineapi.EntityRemoteInterface,
) (RoomHeatingSystemFunctionState, error) {
	if a == nil || a.monitoring == nil {
		return RoomHeatingSystemFunctionState{}, ErrRoomHeatingSysFnDataUnavailable
	}
	state, err := a.monitoring.State(entity)
	if err != nil {
		return RoomHeatingSystemFunctionState{}, err
	}
	configurationEntity := a.configurationEntity(entity)
	if configurationEntity == nil {
		return state, nil
	}
	configurationState, err := a.configuration.State(configurationEntity)
	if err == nil {
		state.ModeWritable = configurationState.ModeWritable
	}
	return state, nil
}

func (a *RoomHeatingSystemFunctionAdapter) WriteOperationMode(
	ctx context.Context,
	entity spineapi.EntityRemoteInterface,
	mode string,
) error {
	configurationEntity := a.configurationEntity(entity)
	if configurationEntity == nil {
		return ErrRoomHeatingSysFnNotWritable
	}
	return a.configuration.WriteOperationMode(ctx, configurationEntity, mode)
}

func (a *RoomHeatingSystemFunctionAdapter) configurationEntity(
	entity spineapi.EntityRemoteInterface,
) spineapi.EntityRemoteInterface {
	if a == nil || a.configuration == nil || entity == nil {
		return nil
	}
	device := entity.Device()
	if device == nil {
		return nil
	}
	return a.configuration.CompatibleEntity(device.Ski()).Entity
}
