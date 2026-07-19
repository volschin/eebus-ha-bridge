package usecases

import (
	"errors"
	"testing"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

func TestHydraulicTemperaturesPrefersHeatPumpApplianceOnAmbiguity(t *testing.T) {
	flowFeature := mocks.NewFeatureRemoteInterface(t)
	flowFeature.On("DataCopy", model.FunctionTypeMeasurementDescriptionListData).Return(
		&model.MeasurementDescriptionListDataType{MeasurementDescriptionData: []model.MeasurementDescriptionDataType{
			{MeasurementId: ptr(model.MeasurementIdType(1)), ScopeType: ptr(model.ScopeTypeTypeFlowTemperature), Unit: ptr(model.UnitOfMeasurementTypedegC)},
		}},
	)
	flowFeature.On("DataCopy", model.FunctionTypeMeasurementListData).Return(
		&model.MeasurementListDataType{MeasurementData: []model.MeasurementDataType{
			{MeasurementId: ptr(model.MeasurementIdType(1)), Value: model.NewScaledNumberType(52.5)},
		}},
	)

	heatPumpEntity := mocks.NewEntityRemoteInterface(t)
	heatPumpEntity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeMeasurement, model.RoleTypeServer).Return(flowFeature)
	heatPumpEntity.On("Address").Return(&model.EntityAddressType{})
	heatPumpEntity.On("EntityType").Return(model.EntityTypeTypeHeatPumpAppliance)
	heatPumpEntity.On("Features").Return([]spineapi.FeatureRemoteInterface(nil))

	registry := eebus.NewDeviceRegistry()
	registry.AddDevice("test-ski", eebus.DeviceInfo{SKI: "test-ski"})
	registry.UpsertObservation("test-ski", nil, heatPumpEntity, "monitoring")

	h := NewHydraulicTemperatures(nil, registry, false)
	value, err := h.FlowTemperature("test-ski")
	if err != nil {
		t.Fatalf("FlowTemperature() error = %v", err)
	}
	if value != 52.5 {
		t.Errorf("FlowTemperature() = %v, want 52.5", value)
	}
}

func TestHydraulicTemperaturesReturnUnavailableWithoutScope(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	h := NewHydraulicTemperatures(nil, registry, false)
	if _, err := h.ReturnTemperature("unknown-ski"); err == nil {
		t.Fatal("ReturnTemperature() error = nil, want an error for an unknown SKI")
	}
}

func TestHydraulicTemperaturesSetupAndPublishBothScopes(t *testing.T) {
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeMeasurementDescriptionListData).Return(
		&model.MeasurementDescriptionListDataType{MeasurementDescriptionData: []model.MeasurementDescriptionDataType{
			{MeasurementId: ptr(model.MeasurementIdType(1)), ScopeType: ptr(model.ScopeTypeTypeFlowTemperature), Unit: ptr(model.UnitOfMeasurementTypedegC)},
			{MeasurementId: ptr(model.MeasurementIdType(2)), ScopeType: ptr(model.ScopeTypeTypeReturnTemperature), Unit: ptr(model.UnitOfMeasurementTypedegF)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeMeasurementListData).Return(
		&model.MeasurementListDataType{MeasurementData: []model.MeasurementDataType{
			{MeasurementId: ptr(model.MeasurementIdType(1)), Value: model.NewScaledNumberType(50)},
			{MeasurementId: ptr(model.MeasurementIdType(2)), Value: model.NewScaledNumberType(104)},
		}},
	)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeMeasurement, model.RoleTypeServer).Return(feature)
	entity.On("Address").Return(&model.EntityAddressType{})
	entity.On("EntityType").Return(model.EntityTypeTypeHeatPumpAppliance)
	entity.On("Features").Return([]spineapi.FeatureRemoteInterface(nil))
	registry := eebus.NewDeviceRegistry()
	registry.UpsertObservation(testValidUsecaseSKI, nil, entity, "monitoring")
	bus := eebus.NewEventBus()
	events := bus.Subscribe()
	defer bus.Unsubscribe(events)
	hydraulic := NewHydraulicTemperatures(bus, registry, true)
	hydraulic.Setup(clientUsecaseLocalEntity(t))
	hydraulic.HandleEvent(spineapi.EventPayload{
		Ski: testValidUsecaseSKI, Entity: entity, EventType: spineapi.EventTypeDataChange,
		ChangeType: spineapi.ElementChangeUpdate, Data: &model.MeasurementListDataType{},
	})
	for _, want := range []eebus.EventType{
		eebus.EventTypeMonitoringFlowTemperatureUpdated,
		eebus.EventTypeMonitoringReturnTemperatureUpdated,
	} {
		select {
		case event := <-events:
			if event.Type != want {
				t.Fatalf("event = %+v, want %q", event, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for %q", want)
		}
	}
	if value, err := (FlowTemperatureReader{hydraulic}).Temperature(testValidUsecaseSKI); err != nil || value != 50 {
		t.Fatalf("flow adapter = (%g, %v)", value, err)
	}
	if value, err := (ReturnTemperatureReader{hydraulic}).Temperature(testValidUsecaseSKI); err != nil || value != 40 {
		t.Fatalf("return adapter = (%g, %v)", value, err)
	}

	hydraulic.HandleEvent(spineapi.EventPayload{})
	hydraulic.HandleEvent(spineapi.EventPayload{Entity: entity, EventType: spineapi.EventTypeDataChange, ChangeType: spineapi.ElementChangeUpdate, Data: "other"})
	(&HydraulicTemperatures{}).HandleEvent(spineapi.EventPayload{Entity: entity, EventType: spineapi.EventTypeDataChange, ChangeType: spineapi.ElementChangeUpdate, Data: &model.MeasurementListDataType{}})
	(&HydraulicTemperatures{}).Setup(nil)
}

func TestHydraulicTemperatureUnitConversions(t *testing.T) {
	tests := []struct {
		value float64
		unit  model.UnitOfMeasurementType
		want  float64
	}{
		{20, "", 20},
		{293.15, model.UnitOfMeasurementTypeK, 20},
		{68, model.UnitOfMeasurementTypedegF, 20},
	}
	for _, test := range tests {
		got, err := convertToCelsius(test.value, test.unit)
		if err != nil || got < test.want-0.0001 || got > test.want+0.0001 {
			t.Fatalf("convertToCelsius(%g, %q) = (%g, %v), want %g", test.value, test.unit, got, err, test.want)
		}
	}
	if _, err := convertToCelsius(1, model.UnitOfMeasurementType("unsupported")); !errors.Is(err, ErrHydraulicTemperatureUnavailable) {
		t.Fatalf("unsupported unit error = %v", err)
	}
}
