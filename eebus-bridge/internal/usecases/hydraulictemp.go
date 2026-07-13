package usecases

import (
	"errors"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

var ErrHydraulicTemperatureUnavailable = errors.New("hydraulic temperature data unavailable")

// HydraulicTemperatures reads flow/return temperature from the standardized
// Measurement/server feature already exposed by the entity the generic
// MonitoringWrapper negotiates. It does not register a separate EEBUS use case.
type HydraulicTemperatures struct {
	bus         *eebus.EventBus
	registry    *eebus.DeviceRegistry
	localEntity spineapi.EntityLocalInterface
	debug       bool
}

func NewHydraulicTemperatures(
	bus *eebus.EventBus,
	registry *eebus.DeviceRegistry,
	debug bool,
) *HydraulicTemperatures {
	return &HydraulicTemperatures{bus: bus, registry: registry, debug: debug}
}

// Setup subscribes to the local device's raw event bus so Measurement/server
// cache changes are observed independently of MPC's semantic event filtering.
func (h *HydraulicTemperatures) Setup(localEntity spineapi.EntityLocalInterface) {
	if localEntity == nil {
		return
	}
	h.localEntity = localEntity
	_ = localEntity.Device().Events().Subscribe(h)
}

// HandleEvent republishes valid flow and return cache updates as typed events.
func (h *HydraulicTemperatures) HandleEvent(payload spineapi.EventPayload) {
	if payload.Entity == nil || payload.EventType != spineapi.EventTypeDataChange ||
		payload.ChangeType != spineapi.ElementChangeUpdate {
		return
	}
	if _, ok := payload.Data.(*model.MeasurementListDataType); !ok {
		return
	}
	if h.registry == nil || h.bus == nil {
		return
	}
	ski := eebus.NormalizeSKI(payload.Ski)
	if _, err := h.FlowTemperature(ski); err == nil {
		h.bus.Publish(eebus.Event{SKI: ski, Type: "monitoring.flow_temperature_updated"})
	}
	if _, err := h.ReturnTemperature(ski); err == nil {
		h.bus.Publish(eebus.Event{SKI: ski, Type: "monitoring.return_temperature_updated"})
	}
}

// FlowTemperature returns the flowTemperature-scoped measurement in Celsius.
func (h *HydraulicTemperatures) FlowTemperature(ski string) (float64, error) {
	return h.scopedTemperature(ski, model.ScopeTypeTypeFlowTemperature)
}

// ReturnTemperature returns the returnTemperature-scoped measurement in Celsius.
func (h *HydraulicTemperatures) ReturnTemperature(ski string) (float64, error) {
	return h.scopedTemperature(ski, model.ScopeTypeTypeReturnTemperature)
}

func (h *HydraulicTemperatures) scopedTemperature(ski string, scope model.ScopeTypeType) (float64, error) {
	if h.registry == nil {
		return 0, ErrHydraulicTemperatureUnavailable
	}
	var preferred spineapi.EntityRemoteInterface
	var fallback spineapi.EntityRemoteInterface
	for _, info := range h.registry.Entities(ski) {
		if info.Entity == nil {
			continue
		}
		feature := info.Entity.FeatureOfTypeAndRole(model.FeatureTypeTypeMeasurement, model.RoleTypeServer)
		if feature == nil {
			continue
		}
		if _, _, ok := measurementIDForScope(feature, scope); !ok {
			continue
		}
		if info.Type == string(model.EntityTypeTypeHeatPumpAppliance) {
			preferred = info.Entity
			break
		}
		if fallback == nil {
			fallback = info.Entity
		}
	}
	entity := preferred
	if entity == nil {
		entity = fallback
	}
	if entity == nil {
		return 0, ErrHydraulicTemperatureUnavailable
	}
	feature := entity.FeatureOfTypeAndRole(model.FeatureTypeTypeMeasurement, model.RoleTypeServer)
	unit, id, ok := measurementIDForScope(feature, scope)
	if !ok {
		return 0, ErrHydraulicTemperatureUnavailable
	}
	value, ok := measurementValue(feature, id)
	if !ok {
		return 0, ErrHydraulicTemperatureUnavailable
	}
	return convertToCelsius(value, unit)
}

func measurementIDForScope(
	feature spineapi.FeatureRemoteInterface,
	scope model.ScopeTypeType,
) (model.UnitOfMeasurementType, model.MeasurementIdType, bool) {
	data, ok := feature.DataCopy(model.FunctionTypeMeasurementDescriptionListData).(*model.MeasurementDescriptionListDataType)
	if !ok || data == nil {
		return "", 0, false
	}
	for _, description := range data.MeasurementDescriptionData {
		if description.MeasurementId != nil && description.ScopeType != nil && *description.ScopeType == scope {
			unit := model.UnitOfMeasurementType("")
			if description.Unit != nil {
				unit = *description.Unit
			}
			return unit, *description.MeasurementId, true
		}
	}
	return "", 0, false
}

func measurementValue(feature spineapi.FeatureRemoteInterface, id model.MeasurementIdType) (float64, bool) {
	data, ok := feature.DataCopy(model.FunctionTypeMeasurementListData).(*model.MeasurementListDataType)
	if !ok || data == nil {
		return 0, false
	}
	for _, entry := range data.MeasurementData {
		if entry.MeasurementId == nil || *entry.MeasurementId != id || entry.Value == nil {
			continue
		}
		if entry.ValueState != nil && *entry.ValueState != model.MeasurementValueStateTypeNormal {
			return 0, false
		}
		return entry.Value.GetValue(), true
	}
	return 0, false
}

func convertToCelsius(value float64, unit model.UnitOfMeasurementType) (float64, error) {
	switch unit {
	case model.UnitOfMeasurementTypedegC, "":
		return value, nil
	case model.UnitOfMeasurementTypedegF:
		return (value - 32) / 1.8, nil
	case model.UnitOfMeasurementTypeK:
		return value - 273.15, nil
	default:
		return 0, ErrHydraulicTemperatureUnavailable
	}
}

// FlowTemperatureReader adapts HydraulicTemperatures to MonitoringService.
type FlowTemperatureReader struct{ *HydraulicTemperatures }

func (r FlowTemperatureReader) Temperature(ski string) (float64, error) {
	return r.FlowTemperature(ski)
}

// ReturnTemperatureReader adapts HydraulicTemperatures to MonitoringService.
type ReturnTemperatureReader struct{ *HydraulicTemperatures }

func (r ReturnTemperatureReader) Temperature(ski string) (float64, error) {
	return r.ReturnTemperature(ski)
}
