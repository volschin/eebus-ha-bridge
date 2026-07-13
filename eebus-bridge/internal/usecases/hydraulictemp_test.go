package usecases

import (
	"testing"

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
