package usecases

import (
	"testing"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/stretchr/testify/mock"
)

type writtenHvacCommand struct {
	cmd model.CmdType
}

func hvacWriteHarness(
	t *testing.T,
	remote *mocks.FeatureRemoteInterface,
) (*mocks.EntityLocalInterface, *mocks.EntityRemoteInterface, *writtenHvacCommand) {
	return hvacWriteHarnessWithErrno(t, remote, 0)
}

func hvacWriteHarnessWithErrno(
	t *testing.T,
	remote *mocks.FeatureRemoteInterface,
	errno model.ErrorNumberType,
) (*mocks.EntityLocalInterface, *mocks.EntityRemoteInterface, *writtenHvacCommand) {
	return hvacWriteHarnessWithResult(t, remote, &errno)
}

func hvacWriteHarnessWithResult(
	t *testing.T,
	remote *mocks.FeatureRemoteInterface,
	errno *model.ErrorNumberType,
) (*mocks.EntityLocalInterface, *mocks.EntityRemoteInterface, *writtenHvacCommand) {
	t.Helper()
	localAddress := &model.FeatureAddressType{}
	local := mocks.NewFeatureLocalInterface(t)
	local.On("Address").Return(localAddress)
	response := local.On("AddResponseCallback", mock.Anything, mock.Anything)
	if errno != nil {
		response.Run(func(args mock.Arguments) {
			callback := args.Get(1).(func(spineapi.ResponseMessage))
			callback(spineapi.ResponseMessage{Data: &model.ResultDataType{ErrorNumber: errno}})
		})
	}
	response.Return(nil)
	local.On("RequestRemoteData", mock.Anything, mock.Anything, mock.Anything, remote).
		Return(ptr(model.MsgCounterType(10)), (*model.ErrorType)(nil)).Maybe()

	written := &writtenHvacCommand{}
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
