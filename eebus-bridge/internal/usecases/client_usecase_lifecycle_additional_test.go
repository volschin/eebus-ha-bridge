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
	local := clientUsecaseLocalEntity(t)
	bus := eebus.NewEventBus()
	registry := eebus.NewDeviceRegistry()

	dhwTemperature := NewDHWTemperature(local, bus, registry, true)
	roomTemperature := NewRoomHeatingTemperature(local, bus, registry, true)
	roomSystemFunction := NewRoomHeatingSystemFunction(local, bus, registry, true)

	checks := []struct {
		name       string
		useCaseNil bool
		add        func() error
	}{
		{"DHW temperature", dhwTemperature.UseCase() == nil, dhwTemperature.AddFeatures},
		{"room temperature", roomTemperature.UseCase() == nil, roomTemperature.AddFeatures},
		{"room system function", roomSystemFunction.UseCase() == nil, roomSystemFunction.AddFeatures},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if check.useCaseNil {
				t.Fatal("UseCase() returned nil")
			}
			if err := check.add(); err != nil {
				t.Fatalf("AddFeatures() error = %v", err)
			}
		})
	}

	if dhwTemperature.localSetpointFeature() == nil || roomTemperature.localSetpointFeature() == nil ||
		roomSystemFunction.localHvacFeature() == nil {
		t.Fatal("local client features were not available after AddFeatures")
	}
}

func TestClientUsecaseAddFeaturesRejectsMissingLocalEntity(t *testing.T) {
	checks := []struct {
		name string
		call func() error
	}{
		{"DHW temperature", (&DHWTemperature{}).AddFeatures},
		{"room temperature", (&RoomHeatingTemperature{}).AddFeatures},
		{"room system function", (&RoomHeatingSystemFunction{}).AddFeatures},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); err == nil {
				t.Fatal("AddFeatures() succeeded without a local entity")
			}
		})
	}
}

func TestRoomSystemFunctionRoutesSupportAndValueUpdates(t *testing.T) {
	bus := eebus.NewEventBus()
	channel := bus.Subscribe()
	defer bus.Unsubscribe(channel)
	room := NewRoomHeatingSystemFunction(clientUsecaseLocalEntity(t), bus, nil, false)
	feature := newRoomHeatingSysFnFeature(t, 0, true)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("EntityType").Return(model.EntityTypeTypeHvacRoom)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeHvac, model.RoleTypeServer).Return(feature)

	tests := []struct {
		data any
		want eebus.EventType
	}{
		{&model.HvacOperationModeDescriptionListDataType{}, eebus.EventTypeRoomHeatingSystemFunctionSupportUpdated},
		{&model.HvacSystemFunctionListDataType{}, eebus.EventTypeRoomHeatingSystemFunctionUpdated},
	}
	for _, test := range tests {
		room.HandleEvent(spineapi.EventPayload{
			Ski: testValidUsecaseSKI, Entity: entity, EventType: spineapi.EventTypeDataChange,
			ChangeType: spineapi.ElementChangeUpdate, Data: test.data,
		})
		select {
		case event := <-channel:
			if event.Type != test.want || event.SKI != eebus.NormalizeSKI(testValidUsecaseSKI) {
				t.Fatalf("event = %+v, want type %q", event, test.want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for %q", test.want)
		}
	}
	room.HandleEvent(spineapi.EventPayload{Entity: entity, EventType: spineapi.EventTypeDataChange, ChangeType: spineapi.ElementChangeAdd})
	room.HandleEvent(spineapi.EventPayload{})
}

func TestRoomSystemFunctionUseCaseEventsPublishSupport(t *testing.T) {
	bus := eebus.NewEventBus()
	channel := bus.Subscribe()
	defer bus.Unsubscribe(channel)
	local := clientUsecaseLocalEntity(t)
	room := NewRoomHeatingSystemFunction(local, bus, nil, false)

	room.handleUseCaseEvent(testValidUsecaseSKI, nil, nil, roomHeatingSysFnUseCaseSupportUpdate)
	select {
	case event := <-channel:
		if event.Type != eebus.EventTypeRoomHeatingSystemFunctionSupportUpdated {
			t.Fatalf("event = %+v, want room-heating support update", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for room-heating support update")
	}

	(&RoomHeatingSystemFunction{}).handleUseCaseEvent(testValidUsecaseSKI, nil, nil, roomHeatingSysFnUseCaseSupportUpdate)
}

func TestClientUsecaseUnavailableGuards(t *testing.T) {
	if _, err := (&DHWTemperature{}).State(nil); !errors.Is(err, ErrDHWDataUnavailable) {
		t.Fatalf("DHW State() error = %v", err)
	}
	if _, err := (&RoomHeatingTemperature{}).State(nil); !errors.Is(err, ErrRoomHeatingDataUnavailable) {
		t.Fatalf("room State() error = %v", err)
	}
	if _, err := (&RoomHeatingSystemFunction{}).State(nil); !errors.Is(err, ErrRoomHeatingSysFnDataUnavailable) {
		t.Fatalf("room system State() error = %v", err)
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

func TestTemperatureUsecasesRouteCacheAndSupportEvents(t *testing.T) {
	bus := eebus.NewEventBus()
	events := bus.Subscribe()
	defer bus.Unsubscribe(events)
	local := clientUsecaseLocalEntity(t)
	dhw := NewDHWTemperature(local, bus, nil, false)
	room := NewRoomHeatingTemperature(local, bus, nil, false)

	dhwEntity := mocks.NewEntityRemoteInterface(t)
	dhwEntity.On("EntityType").Return(model.EntityTypeTypeDHWCircuit)
	dhwEntity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).
		Return(setpointEventFeature(t, model.ScopeTypeTypeDhwTemperature, 48))
	roomEntity := mocks.NewEntityRemoteInterface(t)
	roomEntity.On("EntityType").Return(model.EntityTypeTypeHvacRoom)
	roomEntity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).
		Return(setpointEventFeature(t, model.ScopeTypeTypeRoomAirTemperature, 21))

	dhw.HandleEvent(spineapi.EventPayload{
		Ski: testValidUsecaseSKI, Entity: dhwEntity, EventType: spineapi.EventTypeDataChange,
		ChangeType: spineapi.ElementChangeUpdate, Data: &model.SetpointListDataType{},
	})
	room.HandleEvent(spineapi.EventPayload{
		Ski: testValidUsecaseSKI, Entity: roomEntity, EventType: spineapi.EventTypeDataChange,
		ChangeType: spineapi.ElementChangeUpdate, Data: &model.SetpointListDataType{},
	})
	dhw.handleUseCaseEvent(testValidUsecaseSKI, nil, nil, "support")
	room.handleUseCaseEvent(testValidUsecaseSKI, nil, nil, "support")

	for _, want := range []eebus.EventType{
		eebus.EventTypeDHWSetpointUpdated,
		eebus.EventTypeRoomHeatingSetpointUpdated,
		eebus.EventTypeDHWUseCaseSupportUpdated,
		eebus.EventTypeRoomHeatingUseCaseSupportUpdated,
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

	dhw.HandleEvent(spineapi.EventPayload{Entity: dhwEntity})
	room.HandleEvent(spineapi.EventPayload{Entity: roomEntity})
	dhw.HandleEvent(spineapi.EventPayload{})
	room.HandleEvent(spineapi.EventPayload{})
}

func TestClientUsecaseRefreshAndResolutionGuards(t *testing.T) {
	local := clientUsecaseLocalEntity(t)
	dhw := NewDHWTemperature(local, nil, nil, false)
	room := NewRoomHeatingTemperature(local, nil, nil, false)
	roomSystem := NewRoomHeatingSystemFunction(local, nil, nil, false)

	dhw.Refresh(nil)
	room.Refresh(nil)
	roomSystem.Refresh(nil)
	roomSystem.connect(nil)

	missingSetpoint := mocks.NewEntityRemoteInterface(t)
	missingSetpoint.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).Return(nil)
	dhw.connect(missingSetpoint)
	room.connect(missingSetpoint)

	if dhw.CompatibleEntity("").Entity != nil || room.CompatibleEntity("").Entity != nil ||
		roomSystem.CompatibleEntity("").Entity != nil {
		t.Fatal("uninitialized use case resolved an entity")
	}
}

func TestRoomHeatingReconnectReestablishesRelationsAndRefreshesCaches(t *testing.T) {
	tests := []struct {
		name      string
		feature   model.FeatureTypeType
		functions []model.FunctionType
		connect   func(spineapi.EntityLocalInterface, spineapi.EntityRemoteInterface)
	}{
		{
			name:    "temperature",
			feature: model.FeatureTypeTypeSetpoint,
			functions: []model.FunctionType{
				model.FunctionTypeSetpointDescriptionListData,
				model.FunctionTypeSetpointConstraintsListData,
				model.FunctionTypeSetpointListData,
			},
			connect: func(local spineapi.EntityLocalInterface, remote spineapi.EntityRemoteInterface) {
				(&RoomHeatingTemperature{localEntity: local}).connect(remote)
			},
		},
		{
			name:    "system function",
			feature: model.FeatureTypeTypeHvac,
			functions: []model.FunctionType{
				model.FunctionTypeHvacSystemFunctionDescriptionListData,
				model.FunctionTypeHvacSystemFunctionListData,
				model.FunctionTypeHvacOperationModeDescriptionListData,
				model.FunctionTypeHvacSystemFunctionOperationModeRelationListData,
			},
			connect: func(local spineapi.EntityLocalInterface, remote spineapi.EntityRemoteInterface) {
				(&RoomHeatingSystemFunction{localEntity: local}).connect(remote)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			address := &model.FeatureAddressType{}
			operation := mocks.NewOperationsInterface(t)
			operation.On("Read").Return(true)
			operations := make(map[model.FunctionType]spineapi.OperationsInterface, len(test.functions))
			for _, function := range test.functions {
				operations[function] = operation
			}
			remoteFeature := mocks.NewFeatureRemoteInterface(t)
			remoteFeature.On("Address").Return(address)
			remoteFeature.On("Operations").Return(operations)
			remoteFeature.On("String").Return("remote room-heating feature").Maybe()

			localFeature := mocks.NewFeatureLocalInterface(t)
			localFeature.On("HasSubscriptionToRemote", address).Return(false)
			localFeature.On("SubscribeToRemote", address).
				Return(ptr(model.MsgCounterType(1)), (*model.ErrorType)(nil)).Once()
			localFeature.On("HasBindingToRemote", address).Return(false)
			localFeature.On("BindToRemote", address).
				Return(ptr(model.MsgCounterType(2)), (*model.ErrorType)(nil)).Once()
			for _, function := range test.functions {
				localFeature.On("RequestRemoteData", function, nil, nil, remoteFeature).
					Return(ptr(model.MsgCounterType(3)), (*model.ErrorType)(nil)).Once()
			}

			remoteEntity := mocks.NewEntityRemoteInterface(t)
			remoteEntity.On("FeatureOfTypeAndRole", test.feature, model.RoleTypeServer).Return(remoteFeature)
			localEntity := mocks.NewEntityLocalInterface(t)
			localEntity.On("FeatureOfTypeAndRole", test.feature, model.RoleTypeClient).Return(localFeature)

			test.connect(localEntity, remoteEntity)
			localFeature.AssertNumberOfCalls(t, "RequestRemoteData", len(test.functions))
			localFeature.AssertCalled(t, "SubscribeToRemote", address)
			localFeature.AssertCalled(t, "BindToRemote", address)
		})
	}
}
