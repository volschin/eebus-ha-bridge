package usecases

import (
	"errors"
	"testing"
	"time"

	shipcert "github.com/enbility/ship-go/cert"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/config"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

const testValidUsecaseSKI = "682f708ceba5df9adcb9e6787ea911d9fc3ac490"

func clientUsecaseLocalEntity(t *testing.T) spineapi.EntityLocalInterface {
	t.Helper()
	certificate, err := shipcert.CreateCertificate("test", "test", "DE", "client-usecases")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{EEBUS: config.EEBUSConfig{
		Port: 49877, Vendor: "Test", Brand: "Test", Model: "Test", Serial: "client-usecases",
	}}
	bridge, err := eebus.NewBridgeService(cfg, certificate, eebus.NewEventBus())
	if err != nil {
		t.Fatal(err)
	}
	if err := bridge.Setup(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Shutdown)
	return bridge.LocalEntity()
}

func TestClientUsecaseConstructorsAndFeatures(t *testing.T) {
	dhw := NewDHWTemperature(clientUsecaseLocalEntity(t), eebus.NewEventBus(), eebus.NewDeviceRegistry(), true)
	if dhw.UseCase() == nil {
		t.Fatal("UseCase() returned nil")
	}
	if err := dhw.AddFeatures(); err != nil {
		t.Fatalf("AddFeatures() error = %v", err)
	}
	if dhw.localSetpointFeature() == nil {
		t.Fatal("local Setpoint client feature was not available after AddFeatures")
	}
}

func TestClientUsecaseAddFeaturesRejectsMissingLocalEntity(t *testing.T) {
	if err := (&DHWTemperature{}).AddFeatures(); err == nil {
		t.Fatal("AddFeatures() succeeded without a local entity")
	}
}

func TestClientUsecaseUnavailableGuards(t *testing.T) {
	if _, err := (&DHWTemperature{}).State(nil); !errors.Is(err, ErrDHWDataUnavailable) {
		t.Fatalf("DHW State() error = %v", err)
	}
}

func setpointEventFeature(t *testing.T, scope model.ScopeTypeType, value float64) *mocks.FeatureRemoteInterface {
	t.Helper()
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeSetpointDescriptionListData).Return(
		&model.SetpointDescriptionListDataType{SetpointDescriptionData: []model.SetpointDescriptionDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(scope)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointListData).Return(
		&model.SetpointListDataType{SetpointData: []model.SetpointDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(value)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointConstraintsListData).Return(
		&model.SetpointConstraintsListDataType{SetpointConstraintsData: []model.SetpointConstraintsDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), SetpointRangeMin: model.NewScaledNumberType(5), SetpointRangeMax: model.NewScaledNumberType(70), SetpointStepSize: model.NewScaledNumberType(0.5)},
		}},
	)
	operation := mocks.NewOperationsInterface(t)
	operation.On("Write").Return(true)
	feature.On("Operations").Return(map[model.FunctionType]spineapi.OperationsInterface{
		model.FunctionTypeSetpointListData: operation,
	})
	return feature
}

func TestDHWTemperatureRoutesCacheAndSupportEvents(t *testing.T) {
	bus := eebus.NewEventBus()
	events := bus.Subscribe()
	defer bus.Unsubscribe(events)
	dhw := NewDHWTemperature(clientUsecaseLocalEntity(t), bus, nil, false)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("EntityType").Return(model.EntityTypeTypeDHWCircuit)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).
		Return(setpointEventFeature(t, model.ScopeTypeTypeDhwTemperature, 48))

	dhw.HandleEvent(spineapi.EventPayload{
		Ski: testValidUsecaseSKI, Entity: entity, EventType: spineapi.EventTypeDataChange,
		ChangeType: spineapi.ElementChangeUpdate, Data: &model.SetpointListDataType{},
	})
	dhw.handleUseCaseEvent(testValidUsecaseSKI, nil, nil, "support")

	for _, want := range []eebus.EventType{
		eebus.EventTypeDHWSetpointUpdated,
		eebus.EventTypeDHWUseCaseSupportUpdated,
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

	dhw.HandleEvent(spineapi.EventPayload{Entity: entity})
	dhw.HandleEvent(spineapi.EventPayload{})
}

func TestClientUsecaseRefreshAndResolutionGuards(t *testing.T) {
	dhw := NewDHWTemperature(clientUsecaseLocalEntity(t), nil, nil, false)
	dhw.Refresh(nil)
	missingSetpoint := mocks.NewEntityRemoteInterface(t)
	missingSetpoint.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).Return(nil)
	dhw.connect(missingSetpoint)
	if dhw.CompatibleEntity("").Entity != nil {
		t.Fatal("uninitialized use case resolved an entity")
	}
}
