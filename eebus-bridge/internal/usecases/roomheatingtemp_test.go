package usecases

import (
	"context"
	"errors"
	"testing"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
)

func TestRoomHeatingStateUsesScopedSetpointAndDeviceConstraints(t *testing.T) {
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeSetpointDescriptionListData).Return(
		&model.SetpointDescriptionListDataType{SetpointDescriptionData: []model.SetpointDescriptionDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeRoomAirTemperature)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointListData).Return(
		&model.SetpointListDataType{SetpointData: []model.SetpointDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(21)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointConstraintsListData).Return(
		&model.SetpointConstraintsListDataType{SetpointConstraintsData: []model.SetpointConstraintsDataType{
			{
				SetpointId:       ptr(model.SetpointIdType(1)),
				SetpointRangeMin: model.NewScaledNumberType(5),
				SetpointRangeMax: model.NewScaledNumberType(30),
				SetpointStepSize: model.NewScaledNumberType(0.5),
			},
		}},
	)
	operation := mocks.NewOperationsInterface(t)
	operation.On("Write").Return(true)
	feature.On("Operations").Return(map[model.FunctionType]spineapi.OperationsInterface{
		model.FunctionTypeSetpointListData: operation,
	})

	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).Return(feature)

	state, err := (&RoomHeatingTemperature{}).State(entity)
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if state.Value != 21 || state.Minimum != 5 || state.Maximum != 30 || state.Step != 0.5 || !state.Writable {
		t.Errorf("State() = %+v", state)
	}
}

func TestRoomHeatingStateFailsClosedWithoutConstraints(t *testing.T) {
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeSetpointDescriptionListData).Return(
		&model.SetpointDescriptionListDataType{SetpointDescriptionData: []model.SetpointDescriptionDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeRoomAirTemperature)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointListData).Return(
		&model.SetpointListDataType{SetpointData: []model.SetpointDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(21)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointConstraintsListData).Return(nil)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).Return(feature)

	_, err := (&RoomHeatingTemperature{}).State(entity)
	if !errors.Is(err, ErrRoomHeatingDataUnavailable) {
		t.Fatalf("State() error = %v, want ErrRoomHeatingDataUnavailable", err)
	}
}

func TestRoomHeatingWriteRejectsOutOfRangeValue(t *testing.T) {
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeSetpointDescriptionListData).Return(
		&model.SetpointDescriptionListDataType{SetpointDescriptionData: []model.SetpointDescriptionDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeRoomAirTemperature)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointListData).Return(
		&model.SetpointListDataType{SetpointData: []model.SetpointDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(21)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointConstraintsListData).Return(
		&model.SetpointConstraintsListDataType{SetpointConstraintsData: []model.SetpointConstraintsDataType{
			{
				SetpointId:       ptr(model.SetpointIdType(1)),
				SetpointRangeMin: model.NewScaledNumberType(5),
				SetpointRangeMax: model.NewScaledNumberType(30),
				SetpointStepSize: model.NewScaledNumberType(0.5),
			},
		}},
	)
	operation := mocks.NewOperationsInterface(t)
	operation.On("Write").Return(true)
	feature.On("Operations").Return(map[model.FunctionType]spineapi.OperationsInterface{
		model.FunctionTypeSetpointListData: operation,
	})
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).Return(feature)

	err := (&RoomHeatingTemperature{}).Write(context.Background(), entity, 35)
	if !errors.Is(err, ErrRoomHeatingOutOfRange) {
		t.Fatalf("Write() error = %v, want ErrRoomHeatingOutOfRange", err)
	}
}
