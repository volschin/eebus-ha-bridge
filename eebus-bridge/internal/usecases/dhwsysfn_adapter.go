package usecases

import (
	"context"
	"errors"
	"fmt"
	"log"

	eebusapi "github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	mamdsf "github.com/enbility/eebus-go/usecases/ma/mdsf"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

var errDHWSystemFunctionMonitoringNotInitialized = errors.New("DHW system function monitoring use case not initialized")

type dhwSystemFunctionMonitoringUseCase interface {
	eebusapi.UseCaseInterface
	OperationModes(spineapi.EntityRemoteInterface) ([]ucapi.HvacOperationModeType, error)
	CurrentOperationMode(spineapi.EntityRemoteInterface) (ucapi.HvacOperationModeType, error)
	IsOverrunActive(spineapi.EntityRemoteInterface) (bool, error)
	OverrunStatus(spineapi.EntityRemoteInterface) (model.HvacOverrunStatusType, error)
	RemoteEntitiesScenarios() []eebusapi.RemoteEntityScenarios
}

// DHWSystemFunctionMonitoring wraps eebus-go's MDSF client and owns the
// read/event side of the bridge's DHW system-function capability.
type DHWSystemFunctionMonitoring struct {
	uc       dhwSystemFunctionMonitoringUseCase
	bus      *eebus.EventBus
	registry *eebus.DeviceRegistry
	debug    bool
}

func NewDHWSystemFunctionMonitoring(
	bus *eebus.EventBus,
	registry *eebus.DeviceRegistry,
	debug bool,
) *DHWSystemFunctionMonitoring {
	return &DHWSystemFunctionMonitoring{bus: bus, registry: registry, debug: debug}
}

func (w *DHWSystemFunctionMonitoring) Setup(localEntity spineapi.EntityLocalInterface) {
	if localEntity == nil {
		return
	}
	w.uc = mamdsf.NewMDSF(localEntity, w.HandleEvent)
}

func (w *DHWSystemFunctionMonitoring) UseCase() eebusapi.UseCaseInterface { return w.uc }

func (w *DHWSystemFunctionMonitoring) HandleEvent(
	ski string,
	device spineapi.DeviceRemoteInterface,
	entity spineapi.EntityRemoteInterface,
	event eebusapi.EventType,
) {
	if w.debug {
		log.Printf("[DEBUG] EEBUS DHW system function monitoring event received: ski=%s event=%s", ski, event)
	}

	var eventType eebus.EventType
	switch event {
	case mamdsf.UseCaseSupportUpdate:
		eventType = eebus.EventTypeDHWSystemFunctionSupportUpdated
		recordCapabilitySupport(
			w.registry,
			ski,
			device,
			entity,
			w.CompatibleEntity(observationSKI(ski, device)),
			"dhw_system_function_monitoring",
			eebus.CapabilityDHWSystemFunction,
		)
	case mamdsf.DataUpdateOperationMode, mamdsf.DataUpdateOverrun:
		eventType = eebus.EventTypeDHWSystemFunctionUpdated
		if w.registry != nil {
			w.registry.UpsertObservation(ski, device, entity, "dhw_system_function_monitoring")
		}
	default:
		return
	}
	if w.bus != nil {
		w.bus.Publish(eebus.Event{SKI: ski, Type: eventType})
	}
}

func (w *DHWSystemFunctionMonitoring) CompatibleEntity(ski string) eebus.EntityResolution {
	if w.uc == nil {
		return eebus.EntityResolution{}
	}
	return compatibleEntity(w.uc.RemoteEntitiesScenarios(), ski)
}

func (w *DHWSystemFunctionMonitoring) State(entity spineapi.EntityRemoteInterface) (DHWSystemFunctionState, error) {
	if w.uc == nil {
		return DHWSystemFunctionState{}, errDHWSystemFunctionMonitoringNotInitialized
	}
	modes, err := w.uc.OperationModes(entity)
	if err != nil {
		return DHWSystemFunctionState{}, fmt.Errorf("%w: operation modes: %v", ErrDHWSysFnDataUnavailable, err)
	}
	current, err := w.uc.CurrentOperationMode(entity)
	if err != nil {
		return DHWSystemFunctionState{}, fmt.Errorf("%w: current operation mode: %v", ErrDHWSysFnDataUnavailable, err)
	}

	state := DHWSystemFunctionState{
		OperationMode:  string(current),
		AvailableModes: uniqueOperationModes(modes),
	}
	status, statusErr := w.uc.OverrunStatus(entity)
	active, activeErr := w.uc.IsOverrunActive(entity)
	state.BoostStatus = resolvedDHWBoostStatus(status, statusErr, active, activeErr)
	return state, nil
}

func resolvedDHWBoostStatus(
	status model.HvacOverrunStatusType,
	statusErr error,
	active bool,
	activeErr error,
) string {
	if activeErr == nil {
		if active {
			if statusErr == nil && (status == model.HvacOverrunStatusTypeActive ||
				status == model.HvacOverrunStatusTypeRunning) {
				return string(status)
			}
			return string(model.HvacOverrunStatusTypeActive)
		}
		if statusErr == nil && (status == model.HvacOverrunStatusTypeInactive ||
			status == model.HvacOverrunStatusTypeFinished) {
			return string(status)
		}
		return string(model.HvacOverrunStatusTypeInactive)
	}
	if statusErr == nil {
		return string(status)
	}
	return ""
}

func uniqueOperationModes(modes []ucapi.HvacOperationModeType) []string {
	seen := make(map[ucapi.HvacOperationModeType]struct{}, len(modes))
	result := make([]string, 0, len(modes))
	for _, mode := range modes {
		if _, exists := seen[mode]; exists {
			continue
		}
		seen[mode] = struct{}{}
		result = append(result, string(mode))
	}
	return result
}

type dhwSystemFunctionReader interface {
	CompatibleEntity(string) eebus.EntityResolution
	State(spineapi.EntityRemoteInterface) (DHWSystemFunctionState, error)
}

type dhwSystemFunctionWriter interface {
	CompatibleEntity(string) eebus.EntityResolution
	State(spineapi.EntityRemoteInterface) (DHWSystemFunctionState, error)
	WriteBoost(context.Context, spineapi.EntityRemoteInterface, bool) error
	WriteOperationMode(context.Context, spineapi.EntityRemoteInterface, string) error
}

// DHWSystemFunctionAdapter combines MDSF reads/events with the selected CDSF
// configuration strategies. Writeability is advertised only when CDSF was
// independently negotiated for the same device.
type DHWSystemFunctionAdapter struct {
	monitoring    dhwSystemFunctionReader
	configuration dhwSystemFunctionWriter
}

func NewDHWSystemFunctionAdapter(
	monitoring dhwSystemFunctionReader,
	configuration dhwSystemFunctionWriter,
) *DHWSystemFunctionAdapter {
	return &DHWSystemFunctionAdapter{monitoring: monitoring, configuration: configuration}
}

func (a *DHWSystemFunctionAdapter) CompatibleEntity(ski string) eebus.EntityResolution {
	if a == nil || a.monitoring == nil {
		return eebus.EntityResolution{}
	}
	return a.monitoring.CompatibleEntity(ski)
}

func (a *DHWSystemFunctionAdapter) State(entity spineapi.EntityRemoteInterface) (DHWSystemFunctionState, error) {
	if a == nil || a.monitoring == nil {
		return DHWSystemFunctionState{}, ErrDHWSysFnDataUnavailable
	}
	state, err := a.monitoring.State(entity)
	if err != nil {
		return DHWSystemFunctionState{}, err
	}
	configurationEntity := a.configurationEntity(entity)
	if configurationEntity == nil {
		return state, nil
	}
	configurationState, err := a.configuration.State(configurationEntity)
	if err == nil {
		state.BoostWritable = configurationState.BoostWritable
		state.ModeWritable = configurationState.ModeWritable
	}
	return state, nil
}

func (a *DHWSystemFunctionAdapter) WriteBoost(ctx context.Context, entity spineapi.EntityRemoteInterface, active bool) error {
	configurationEntity := a.configurationEntity(entity)
	if configurationEntity == nil {
		return ErrDHWSysFnNotWritable
	}
	return a.configuration.WriteBoost(ctx, configurationEntity, active)
}

func (a *DHWSystemFunctionAdapter) WriteOperationMode(ctx context.Context, entity spineapi.EntityRemoteInterface, mode string) error {
	configurationEntity := a.configurationEntity(entity)
	if configurationEntity == nil {
		return ErrDHWSysFnNotWritable
	}
	return a.configuration.WriteOperationMode(ctx, configurationEntity, mode)
}

func (a *DHWSystemFunctionAdapter) configurationEntity(entity spineapi.EntityRemoteInterface) spineapi.EntityRemoteInterface {
	if a == nil || a.configuration == nil || entity == nil {
		return nil
	}
	device := entity.Device()
	if device == nil {
		return nil
	}
	return a.configuration.CompatibleEntity(device.Ski()).Entity
}
