package usecases

import (
	"context"
	"errors"
	"testing"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/stretchr/testify/mock"
)

func TestDHWStateUsesScopedSetpointAndDeviceConstraints(t *testing.T) {
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeSetpointDescriptionListData).Return(
		&model.SetpointDescriptionListDataType{SetpointDescriptionData: []model.SetpointDescriptionDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeDhwTemperature)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointListData).Return(
		&model.SetpointListDataType{SetpointData: []model.SetpointDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(46)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointConstraintsListData).Return(
		&model.SetpointConstraintsListDataType{SetpointConstraintsData: []model.SetpointConstraintsDataType{
			{
				SetpointId:       ptr(model.SetpointIdType(1)),
				SetpointRangeMin: model.NewScaledNumberType(35),
				SetpointRangeMax: model.NewScaledNumberType(70),
				SetpointStepSize: model.NewScaledNumberType(1),
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

	state, err := (&DHWTemperature{}).State(entity)
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if state.Value != 46 || state.Minimum != 35 || state.Maximum != 70 || state.Step != 1 || !state.Writable {
		t.Errorf("State() = %+v", state)
	}
}

func TestDHWStateFailsClosedWithoutConstraints(t *testing.T) {
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeSetpointDescriptionListData).Return(
		&model.SetpointDescriptionListDataType{SetpointDescriptionData: []model.SetpointDescriptionDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeDhwTemperature)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointListData).Return(
		&model.SetpointListDataType{SetpointData: []model.SetpointDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(46)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointConstraintsListData).Return(nil)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).Return(feature)

	_, err := (&DHWTemperature{}).State(entity)
	if !errors.Is(err, ErrDHWDataUnavailable) {
		t.Fatalf("State() error = %v, want ErrDHWDataUnavailable", err)
	}
}

func TestDHWWriteUpdatesScopedEntryAndAwaitsAcceptance(t *testing.T) {
	description := &model.SetpointDescriptionListDataType{SetpointDescriptionData: []model.SetpointDescriptionDataType{
		{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeDhwTemperature)},
	}}
	current := &model.SetpointListDataType{SetpointData: []model.SetpointDataType{
		{SetpointId: ptr(model.SetpointIdType(0))},
		{SetpointId: ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(46)},
	}}
	constraints := &model.SetpointConstraintsListDataType{SetpointConstraintsData: []model.SetpointConstraintsDataType{
		{
			SetpointId:       ptr(model.SetpointIdType(1)),
			SetpointRangeMin: model.NewScaledNumberType(35),
			SetpointRangeMax: model.NewScaledNumberType(70),
			SetpointStepSize: model.NewScaledNumberType(1),
		},
	}}
	operation := mocks.NewOperationsInterface(t)
	operation.On("Write").Return(true)
	operation.On("Read").Return(true)
	operations := map[model.FunctionType]spineapi.OperationsInterface{
		model.FunctionTypeSetpointListData: operation,
	}
	remoteAddress := &model.FeatureAddressType{}
	remote := mocks.NewFeatureRemoteInterface(t)
	remote.On("DataCopy", model.FunctionTypeSetpointDescriptionListData).Return(description)
	remote.On("DataCopy", model.FunctionTypeSetpointListData).Return(current)
	remote.On("DataCopy", model.FunctionTypeSetpointConstraintsListData).Return(constraints)
	remote.On("Operations").Return(operations)
	remote.On("Address").Return(remoteAddress)
	remote.On("String").Return("remote setpoint")

	localAddress := &model.FeatureAddressType{}
	local := mocks.NewFeatureLocalInterface(t)
	local.On("Address").Return(localAddress)
	local.On("AddResponseCallback", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		callback := args.Get(1).(func(spineapi.ResponseMessage))
		callback(spineapi.ResponseMessage{Data: &model.ResultDataType{ErrorNumber: ptr(model.ErrorNumberType(0))}})
	}).Return(nil)
	local.On("RequestRemoteData", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(ptr(model.MsgCounterType(10)), (*model.ErrorType)(nil))

	var written *model.SetpointListDataType
	counter := model.MsgCounterType(9)
	sender := mocks.NewSenderInterface(t)
	sender.On("Write", localAddress, remoteAddress, mock.Anything).Run(func(args mock.Arguments) {
		written = args.Get(2).(model.CmdType).SetpointListData
	}).Return(&counter, nil)
	device := mocks.NewDeviceRemoteInterface(t)
	device.On("Sender").Return(sender)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).Return(remote)
	entity.On("Device").Return(device)
	localEntity := mocks.NewEntityLocalInterface(t)
	localEntity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeClient).Return(local)

	dhw := &DHWTemperature{localEntity: localEntity}
	if err := dhw.Write(context.Background(), entity, 47); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if written == nil || len(written.SetpointData) != 2 || written.SetpointData[1].Value.GetValue() != 47 {
		t.Fatalf("written setpoint data = %+v, want DHW value 47", written)
	}
}

func TestDHWWriteRejectsOffStepValueBeforeSending(t *testing.T) {
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeSetpointDescriptionListData).Return(
		&model.SetpointDescriptionListDataType{SetpointDescriptionData: []model.SetpointDescriptionDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeDhwTemperature)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointListData).Return(
		&model.SetpointListDataType{SetpointData: []model.SetpointDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(46)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointConstraintsListData).Return(
		&model.SetpointConstraintsListDataType{SetpointConstraintsData: []model.SetpointConstraintsDataType{
			{
				SetpointId:       ptr(model.SetpointIdType(1)),
				SetpointRangeMin: model.NewScaledNumberType(35),
				SetpointRangeMax: model.NewScaledNumberType(70),
				SetpointStepSize: model.NewScaledNumberType(1),
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

	err := (&DHWTemperature{}).Write(context.Background(), entity, 46.5)
	if !errors.Is(err, ErrDHWInvalidStep) {
		t.Fatalf("Write() error = %v, want ErrDHWInvalidStep", err)
	}
}

func TestDHWWriteValidationGuards(t *testing.T) {
	tests := []struct {
		name  string
		state DHWSetpoint
		value float64
		want  error
	}{
		{
			name: "read only",
			want: ErrDHWNotWritable,
		},
		{
			name:  "out of range",
			state: DHWSetpoint{Minimum: 35, Maximum: 70, Step: 1, Writable: true},
			value: 71,
			want:  ErrDHWOutOfRange,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateDHWSetpointWrite(test.state, test.value); !errors.Is(err, test.want) {
				t.Fatalf("validateDHWSetpointWrite() error = %v, want %v", err, test.want)
			}
		})
	}
}
