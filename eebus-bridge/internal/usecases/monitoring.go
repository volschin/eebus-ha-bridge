package usecases

import (
	"errors"
	"fmt"
	"log"
	"strings"

	eebusapi "github.com/enbility/eebus-go/api"
	"github.com/enbility/eebus-go/features/client"
	mampc "github.com/enbility/eebus-go/usecases/ma/mpc"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

// MonitoringWrapper wraps the eebus-go MPC (Monitoring of Power Consumption) use case
// and routes events to the internal EventBus.
type MonitoringWrapper struct {
	uc          *mampc.MPC
	bus         *eebus.EventBus
	registry    *eebus.DeviceRegistry
	localEntity spineapi.EntityLocalInterface
	debug       bool
}

var errMonitoringNotInitialized = errors.New("monitoring use case not initialized")

type GenericMeasurement struct {
	Type       string
	Value      float64
	Unit       string
	EntityType string
	EntityAddr string
	RawType    string
	RawScope   string
}

// NewMonitoringWrapper creates a new MonitoringWrapper. Call Setup() before using the use case.
func NewMonitoringWrapper(bus *eebus.EventBus, registry *eebus.DeviceRegistry, debugEvents bool) *MonitoringWrapper {
	return &MonitoringWrapper{bus: bus, registry: registry, debug: debugEvents}
}

// Setup initialises the underlying eebus-go MPC use case for the given local entity.
func (w *MonitoringWrapper) Setup(localEntity spineapi.EntityLocalInterface) {
	if localEntity == nil {
		return
	}
	w.localEntity = localEntity
	w.uc = mampc.NewMPC(localEntity, w.HandleEvent)
}

// UseCase returns the underlying eebus-go MPC use case (may be nil before Setup).
func (w *MonitoringWrapper) UseCase() *mampc.MPC {
	return w.uc
}

// HandleEvent is the api.EntityEventCallback passed to eebus-go. It translates
// eebus-go event types to internal EventBus events.
func (w *MonitoringWrapper) HandleEvent(ski string, device spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event eebusapi.EventType) {
	if w.debug {
		log.Printf(
			"[DEBUG] EEBUS monitoring event received: ski=%s event=%s has_device=%t has_entity=%t",
			ski,
			event,
			device != nil,
			entity != nil,
		)
	}

	if w.debug {
		eebus.DefaultUseCaseDiscovery().LogOnce(ski, device)
	}

	if w.registry != nil {
		w.registry.UpsertObservation(ski, device, entity, "monitoring")
		enrichDeviceClassification(w.registry, w.localEntity, ski, device, entity)
	}

	var eventType eebus.EventType
	switch event {
	case mampc.DataUpdatePower:
		eventType = eebus.EventTypeMonitoringPowerUpdated
	case mampc.DataUpdatePowerPerPhase:
		eventType = eebus.EventTypeMonitoringPowerPerPhaseUpdated
	case mampc.DataUpdateEnergyConsumed:
		eventType = eebus.EventTypeMonitoringEnergyConsumedUpdated
	case mampc.DataUpdateEnergyProduced:
		eventType = eebus.EventTypeMonitoringEnergyProducedUpdated
	case mampc.DataUpdateCurrentsPerPhase:
		eventType = eebus.EventTypeMonitoringCurrentsPerPhaseUpdated
	case mampc.DataUpdateVoltagePerPhase:
		eventType = eebus.EventTypeMonitoringVoltagePerPhaseUpdated
	case mampc.DataUpdateFrequency:
		eventType = eebus.EventTypeMonitoringFrequencyUpdated
	case mampc.UseCaseSupportUpdate:
		eventType = eebus.EventTypeMonitoringUseCaseSupportUpdated
	default:
		return
	}
	if w.bus != nil {
		w.bus.Publish(eebus.Event{SKI: ski, Type: eventType})
	}
}

// Power returns the total momentary active power for the given remote entity.
func (w *MonitoringWrapper) Power(entity spineapi.EntityRemoteInterface) (float64, error) {
	if w.uc == nil {
		return 0, errMonitoringNotInitialized
	}
	return w.uc.Power(entity)
}

// PowerPerPhase returns the phase-specific momentary active power.
func (w *MonitoringWrapper) PowerPerPhase(entity spineapi.EntityRemoteInterface) ([]float64, error) {
	if w.uc == nil {
		return nil, errMonitoringNotInitialized
	}
	return w.uc.PowerPerPhase(entity)
}

// EnergyConsumed returns the total consumed energy.
func (w *MonitoringWrapper) EnergyConsumed(entity spineapi.EntityRemoteInterface) (float64, error) {
	if w.uc == nil {
		return 0, errMonitoringNotInitialized
	}
	return w.uc.EnergyConsumed(entity)
}

// EnergyProduced returns the total produced/fed-in energy.
func (w *MonitoringWrapper) EnergyProduced(entity spineapi.EntityRemoteInterface) (float64, error) {
	if w.uc == nil {
		return 0, errMonitoringNotInitialized
	}
	return w.uc.EnergyProduced(entity)
}

// CurrentPerPhase returns the phase-specific momentary current.
func (w *MonitoringWrapper) CurrentPerPhase(entity spineapi.EntityRemoteInterface) ([]float64, error) {
	if w.uc == nil {
		return nil, errMonitoringNotInitialized
	}
	return w.uc.CurrentPerPhase(entity)
}

// VoltagePerPhase returns the phase-specific voltage.
func (w *MonitoringWrapper) VoltagePerPhase(entity spineapi.EntityRemoteInterface) ([]float64, error) {
	if w.uc == nil {
		return nil, errMonitoringNotInitialized
	}
	return w.uc.VoltagePerPhase(entity)
}

// Frequency returns the power network frequency.
func (w *MonitoringWrapper) Frequency(entity spineapi.EntityRemoteInterface) (float64, error) {
	if w.uc == nil {
		return 0, errMonitoringNotInitialized
	}
	return w.uc.Frequency(entity)
}

func (w *MonitoringWrapper) CompatibleEntity(ski string) spineapi.EntityRemoteInterface {
	if w.uc == nil {
		return nil
	}
	return compatibleEntity(w.uc.RemoteEntitiesScenarios(), ski)
}

func (w *MonitoringWrapper) GenericMeasurements(ski string) ([]GenericMeasurement, error) {
	if w.uc == nil {
		return nil, errMonitoringNotInitialized
	}
	if w.registry == nil {
		return nil, errors.New("device registry not initialized")
	}

	entities := w.registry.Entities(ski)
	if len(entities) == 0 && ski == "" {
		for _, device := range w.registry.ListDevices() {
			entities = append(entities, w.registry.Entities(device.SKI)...)
		}
	}

	var out []GenericMeasurement
	for _, entityInfo := range entities {
		if entityInfo.Entity == nil || !hasMeasurementServer(entityInfo.Features) {
			continue
		}
		measurements, err := w.measurementsForEntity(entityInfo)
		if err != nil {
			if w.debug {
				log.Printf("[DEBUG] EEBUS generic measurement read failed: ski=%s entity=%s/%s err=%v", ski, entityInfo.Address, entityInfo.Type, err)
			}
			continue
		}
		out = append(out, measurements...)
	}
	if len(out) == 0 {
		return nil, eebusapi.ErrDataNotAvailable
	}
	return out, nil
}

func (w *MonitoringWrapper) measurementsForEntity(entityInfo eebus.EntityInfo) ([]GenericMeasurement, error) {
	meas, err := client.NewMeasurement(w.localEntity, entityInfo.Entity)
	if err != nil {
		return nil, err
	}
	descriptions, err := meas.GetDescriptionsForFilter(model.MeasurementDescriptionDataType{})
	if err != nil {
		return nil, err
	}

	var out []GenericMeasurement
	for _, desc := range descriptions {
		if desc.MeasurementId == nil {
			continue
		}
		data, err := meas.GetDataForId(*desc.MeasurementId)
		if err != nil || data == nil || data.Value == nil {
			continue
		}
		if data.ValueState != nil && *data.ValueState != model.MeasurementValueStateTypeNormal {
			continue
		}
		typ := classifyGenericMeasurement(entityInfo, desc)
		unit := ""
		if desc.Unit != nil {
			unit = string(*desc.Unit)
		}
		out = append(out, GenericMeasurement{
			Type:       typ,
			Value:      data.Value.GetValue(),
			Unit:       unit,
			EntityType: entityInfo.Type,
			EntityAddr: entityInfo.Address,
			RawType:    stringPtrValue(desc.MeasurementType),
			RawScope:   stringPtrValue(desc.ScopeType),
		})
	}
	return out, nil
}

func hasMeasurementServer(features []string) bool {
	for _, feature := range features {
		if feature == "Measurement/server" {
			return true
		}
	}
	return false
}

func classifyGenericMeasurement(entity eebus.EntityInfo, desc model.MeasurementDescriptionDataType) string {
	measurementType := stringPtrValue(desc.MeasurementType)
	scope := stringPtrValue(desc.ScopeType)
	entityType := strings.ToLower(entity.Type)

	switch scope {
	case string(model.ScopeTypeTypeDhwTemperature):
		return "dhw_temperature"
	case string(model.ScopeTypeTypeRoomAirTemperature):
		return "room_temperature"
	case string(model.ScopeTypeTypeOutsideAirTemperature):
		return "outdoor_temperature"
	case string(model.ScopeTypeTypeFlowTemperature):
		return "flow_temperature"
	case string(model.ScopeTypeTypeReturnTemperature):
		return "return_temperature"
	case string(model.ScopeTypeTypeComponentTemperature):
		if entityType == "compressor" {
			return "compressor_temperature"
		}
	}

	if measurementType == string(model.MeasurementTypeTypeTemperature) {
		switch entityType {
		case "dhwcircuit":
			return "dhw_temperature"
		case "hvacroom":
			return "room_temperature"
		case "temperaturesensor":
			return "outdoor_temperature"
		case "compressor":
			return "compressor_temperature"
		}
	}
	if measurementType == string(model.MeasurementTypeTypePower) && entityType == "compressor" {
		return "compressor_power"
	}

	rawType := measurementType
	if rawType == "" {
		rawType = "unknown"
	}
	rawScope := scope
	if rawScope == "" {
		rawScope = "unspecified"
	}
	rawEntity := strings.ToLower(entity.Type)
	if rawEntity == "" {
		rawEntity = "entity"
	}
	return fmt.Sprintf("raw_%s_%s_%s", rawEntity, rawType, rawScope)
}

func stringPtrValue[T ~string](ptr *T) string {
	if ptr == nil {
		return ""
	}
	return string(*ptr)
}
