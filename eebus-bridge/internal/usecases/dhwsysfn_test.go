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

func TestDHWSystemFunctionStateResolvesBoostAndModes(t *testing.T) {
	feature := dhwSysFnFeature(t, true, true, nil)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeHvac, model.RoleTypeServer).Return(feature)

	state, err := (&DHWSystemFunction{}).State(entity)
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if state.BoostStatus != "inactive" || !state.BoostWritable || state.OperationMode != "auto" ||
		!state.ModeWritable || len(state.AvailableModes) != 3 || state.AvailableModes[0] != "auto" ||
		state.AvailableModes[1] != "on" || state.AvailableModes[2] != "off" {
		t.Fatalf("State() = %+v", state)
	}
}

func TestDHWSystemFunctionStateFailsClosedForAmbiguousDHWFunction(t *testing.T) {
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeHvacSystemFunctionDescriptionListData).Return(
		&model.HvacSystemFunctionDescriptionListDataType{HvacSystemFunctionDescriptionData: []model.HvacSystemFunctionDescriptionDataType{
			{SystemFunctionId: ptr(model.HvacSystemFunctionIdType(0)), SystemFunctionType: ptr(model.HvacSystemFunctionTypeTypeDhw)},
			{SystemFunctionId: ptr(model.HvacSystemFunctionIdType(1)), SystemFunctionType: ptr(model.HvacSystemFunctionTypeTypeDhw)},
		}},
	)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeHvac, model.RoleTypeServer).Return(feature)

	_, err := (&DHWSystemFunction{}).State(entity)
	if !errors.Is(err, ErrDHWSysFnDataUnavailable) {
		t.Fatalf("State() error = %v, want ErrDHWSysFnDataUnavailable", err)
	}
}

func TestDHWSystemFunctionWriteBoostUpdatesFullOverrunList(t *testing.T) {
	feature := dhwSysFnFeature(t, true, true, nil)
	local, entity, written := dhwSysFnWriteHarness(t, feature)
	dhw := &DHWSystemFunction{localEntity: local}

	if err := dhw.WriteBoost(context.Background(), entity, true); err != nil {
		t.Fatalf("WriteBoost() error = %v", err)
	}
	if *written.cmd.HvacOverrunListData.HvacOverrunData[0].OverrunStatus != model.HvacOverrunStatusTypeFinished ||
		*written.cmd.HvacOverrunListData.HvacOverrunData[1].OverrunStatus != model.HvacOverrunStatusTypeActive {
		t.Fatalf("written overrun data = %+v", written.cmd.HvacOverrunListData)
	}
}

func TestDHWSystemFunctionWriteOperationModeResolvesModeIDFromType(t *testing.T) {
	feature := dhwSysFnFeature(t, true, true, nil)
	local, entity, written := dhwSysFnWriteHarness(t, feature)
	dhw := &DHWSystemFunction{localEntity: local}

	if err := dhw.WriteOperationMode(context.Background(), entity, "off"); err != nil {
		t.Fatalf("WriteOperationMode() error = %v", err)
	}
	if *written.cmd.HvacSystemFunctionListData.HvacSystemFunctionData[1].CurrentOperationModeId != model.HvacOperationModeIdType(2) {
		t.Fatalf("written system function data = %+v", written.cmd.HvacSystemFunctionListData)
	}

	err := dhw.WriteOperationMode(context.Background(), entity, "eco")
	if !errors.Is(err, ErrDHWSysFnInvalidMode) {
		t.Fatalf("WriteOperationMode() error = %v, want ErrDHWSysFnInvalidMode", err)
	}
}

func dhwSysFnFeature(t *testing.T, overrunWrite, systemWrite bool, overrunChangeable *bool) *mocks.FeatureRemoteInterface {
	t.Helper()
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeHvacSystemFunctionDescriptionListData).Return(
		&model.HvacSystemFunctionDescriptionListDataType{HvacSystemFunctionDescriptionData: []model.HvacSystemFunctionDescriptionDataType{
			{SystemFunctionId: ptr(model.HvacSystemFunctionIdType(0)), SystemFunctionType: ptr(model.HvacSystemFunctionTypeTypeHeating)},
			{SystemFunctionId: ptr(model.HvacSystemFunctionIdType(3)), SystemFunctionType: ptr(model.HvacSystemFunctionTypeTypeDhw)},
		}},
	).Maybe()
	feature.On("DataCopy", model.FunctionTypeHvacSystemFunctionListData).Return(
		&model.HvacSystemFunctionListDataType{HvacSystemFunctionData: []model.HvacSystemFunctionDataType{
			{SystemFunctionId: ptr(model.HvacSystemFunctionIdType(0)), CurrentOperationModeId: ptr(model.HvacOperationModeIdType(9))},
			{
				SystemFunctionId:            ptr(model.HvacSystemFunctionIdType(3)),
				CurrentOperationModeId:      ptr(model.HvacOperationModeIdType(0)),
				IsOperationModeIdChangeable: ptr(true),
			},
		}},
	).Maybe()
	feature.On("DataCopy", model.FunctionTypeHvacOperationModeDescriptionListData).Return(
		&model.HvacOperationModeDescriptionListDataType{HvacOperationModeDescriptionData: []model.HvacOperationModeDescriptionDataType{
			{OperationModeId: ptr(model.HvacOperationModeIdType(0)), OperationModeType: ptr(model.HvacOperationModeTypeTypeAuto)},
			{OperationModeId: ptr(model.HvacOperationModeIdType(1)), OperationModeType: ptr(model.HvacOperationModeTypeTypeOn)},
			{OperationModeId: ptr(model.HvacOperationModeIdType(2)), OperationModeType: ptr(model.HvacOperationModeTypeTypeOff)},
			{OperationModeId: ptr(model.HvacOperationModeIdType(9)), OperationModeType: ptr(model.HvacOperationModeTypeTypeEco)},
		}},
	).Maybe()
	feature.On("DataCopy", model.FunctionTypeHvacSystemFunctionOperationModeRelationListData).Return(
		&model.HvacSystemFunctionOperationModeRelationListDataType{HvacSystemFunctionOperationModeRelationData: []model.HvacSystemFunctionOperationModeRelationDataType{
			{SystemFunctionId: ptr(model.HvacSystemFunctionIdType(3)), OperationModeId: []model.HvacOperationModeIdType{0, 1, 2}},
		}},
	).Maybe()
	feature.On("DataCopy", model.FunctionTypeHvacOverrunDescriptionListData).Return(
		&model.HvacOverrunDescriptionListDataType{HvacOverrunDescriptionData: []model.HvacOverrunDescriptionDataType{
			{
				OverrunId:                ptr(model.HvacOverrunIdType(4)),
				OverrunType:              ptr(model.HvacOverrunTypeTypeParty),
				AffectedSystemFunctionId: []model.HvacSystemFunctionIdType{3},
			},
			{
				OverrunId:                ptr(model.HvacOverrunIdType(7)),
				OverrunType:              ptr(model.HvacOverrunTypeTypeOneTimeDhw),
				AffectedSystemFunctionId: []model.HvacSystemFunctionIdType{3},
			},
		}},
	).Maybe()
	feature.On("DataCopy", model.FunctionTypeHvacOverrunListData).Return(
		&model.HvacOverrunListDataType{HvacOverrunData: []model.HvacOverrunDataType{
			{OverrunId: ptr(model.HvacOverrunIdType(4)), OverrunStatus: ptr(model.HvacOverrunStatusTypeFinished)},
			{
				OverrunId:                 ptr(model.HvacOverrunIdType(7)),
				OverrunStatus:             ptr(model.HvacOverrunStatusTypeInactive),
				IsOverrunStatusChangeable: overrunChangeable,
			},
		}},
	).Maybe()
	overrunOperation := mocks.NewOperationsInterface(t)
	overrunOperation.On("Write").Return(overrunWrite).Maybe()
	overrunOperation.On("Read").Return(true).Maybe()
	systemOperation := mocks.NewOperationsInterface(t)
	systemOperation.On("Write").Return(systemWrite).Maybe()
	systemOperation.On("Read").Return(true).Maybe()
	feature.On("Operations").Return(map[model.FunctionType]spineapi.OperationsInterface{
		model.FunctionTypeHvacOverrunListData:            overrunOperation,
		model.FunctionTypeHvacSystemFunctionListData:     systemOperation,
		model.FunctionTypeHvacOverrunDescriptionListData: overrunOperation,
	}).Maybe()
	feature.On("Address").Return(&model.FeatureAddressType{}).Maybe()
	feature.On("String").Return("remote hvac").Maybe()
	return feature
}

type writtenDhwSysFn struct {
	cmd model.CmdType
}

func dhwSysFnWriteHarness(
	t *testing.T,
	remote *mocks.FeatureRemoteInterface,
) (*mocks.EntityLocalInterface, *mocks.EntityRemoteInterface, *writtenDhwSysFn) {
	t.Helper()
	localAddress := &model.FeatureAddressType{}
	local := mocks.NewFeatureLocalInterface(t)
	local.On("Address").Return(localAddress)
	local.On("AddResponseCallback", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		callback := args.Get(1).(func(spineapi.ResponseMessage))
		callback(spineapi.ResponseMessage{Data: &model.ResultDataType{ErrorNumber: ptr(model.ErrorNumberType(0))}})
	}).Return(nil)
	local.On("RequestRemoteData", mock.Anything, mock.Anything, mock.Anything, remote).
		Return(ptr(model.MsgCounterType(10)), (*model.ErrorType)(nil))

	written := &writtenDhwSysFn{}
	counter := model.MsgCounterType(9)
	sender := mocks.NewSenderInterface(t)
	sender.On("Write", localAddress, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		written.cmd = args.Get(2).(model.CmdType)
	}).Return(&counter, nil)
	device := mocks.NewDeviceRemoteInterface(t)
	device.On("Sender").Return(sender)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeHvac, model.RoleTypeServer).Return(remote)
	entity.On("Device").Return(device)
	localEntity := mocks.NewEntityLocalInterface(t)
	localEntity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeHvac, model.RoleTypeClient).Return(local)
	return localEntity, entity, written
}
