package usecases

import (
	"testing"

	cemohpcf "github.com/enbility/eebus-go/usecases/cem/ohpcf"
	spineapi "github.com/enbility/spine-go/api"
	spinemocks "github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/spine"
	"github.com/stretchr/testify/mock"
)

func TestOHPCFRefreshRequestsSmartEnergyManagementData(t *testing.T) {
	localEntity := spinemocks.NewEntityLocalInterface(t)
	remoteEntity := spinemocks.NewEntityRemoteInterface(t)
	remoteDevice := spinemocks.NewDeviceRemoteInterface(t)
	localFeature := spinemocks.NewFeatureLocalInterface(t)
	remoteFeature := spinemocks.NewFeatureRemoteInterface(t)
	counter := model.MsgCounterType(1)

	localEntity.EXPECT().Device().Return(nil)
	localEntity.EXPECT().FeatureOfTypeAndRole(
		model.FeatureTypeTypeSmartEnergyManagementPs,
		model.RoleTypeClient,
	).Return(localFeature)
	remoteEntity.EXPECT().Device().Return(remoteDevice).Twice()
	remoteDevice.EXPECT().FeatureByEntityTypeAndRole(
		remoteEntity,
		model.FeatureTypeTypeSmartEnergyManagementPs,
		model.RoleTypeServer,
	).Return(remoteFeature)
	remoteFeature.EXPECT().Operations().Return(map[model.FunctionType]spineapi.OperationsInterface{
		model.FunctionTypeSmartEnergyManagementPsData: spine.NewOperations(true, false, false, false),
	})
	remoteFeature.EXPECT().String().Return("remote SmartEnergyManagementPs feature").Maybe()
	localFeature.EXPECT().RequestRemoteData(
		model.FunctionTypeSmartEnergyManagementPsData,
		mock.Anything,
		mock.Anything,
		mock.Anything,
	).Return(&counter, nil)

	wrapper := OHPCFWrapper{uc: &cemohpcf.OHPCF{}, localEntity: localEntity}
	wrapper.Refresh(remoteEntity)
}

func TestOHPCFRefreshHandlesUnavailableSetupAndRequest(t *testing.T) {
	t.Run("before setup", func(t *testing.T) {
		new(OHPCFWrapper).Refresh(nil)
	})

	t.Run("feature setup fails", func(t *testing.T) {
		localEntity := spinemocks.NewEntityLocalInterface(t)
		remoteEntity := spinemocks.NewEntityRemoteInterface(t)
		localEntity.EXPECT().Device().Return(nil)
		remoteEntity.EXPECT().Device().Return(nil)
		localEntity.EXPECT().FeatureOfTypeAndRole(
			model.FeatureTypeTypeSmartEnergyManagementPs,
			model.RoleTypeClient,
		).Return(nil)
		localEntity.EXPECT().FeatureOfTypeAndRole(
			model.FeatureTypeTypeGeneric,
			model.RoleTypeClient,
		).Return(nil)

		wrapper := OHPCFWrapper{uc: &cemohpcf.OHPCF{}, localEntity: localEntity, debug: true}
		wrapper.Refresh(remoteEntity)
	})

	t.Run("request fails", func(t *testing.T) {
		localEntity := spinemocks.NewEntityLocalInterface(t)
		remoteEntity := spinemocks.NewEntityRemoteInterface(t)
		remoteDevice := spinemocks.NewDeviceRemoteInterface(t)
		localFeature := spinemocks.NewFeatureLocalInterface(t)
		remoteFeature := spinemocks.NewFeatureRemoteInterface(t)

		localEntity.EXPECT().Device().Return(nil)
		localEntity.EXPECT().FeatureOfTypeAndRole(
			model.FeatureTypeTypeSmartEnergyManagementPs,
			model.RoleTypeClient,
		).Return(localFeature)
		remoteEntity.EXPECT().Device().Return(remoteDevice).Twice()
		remoteDevice.EXPECT().FeatureByEntityTypeAndRole(
			remoteEntity,
			model.FeatureTypeTypeSmartEnergyManagementPs,
			model.RoleTypeServer,
		).Return(remoteFeature)
		remoteFeature.EXPECT().Operations().Return(map[model.FunctionType]spineapi.OperationsInterface{
			model.FunctionTypeSmartEnergyManagementPsData: spine.NewOperations(true, false, false, false),
		})
		remoteFeature.EXPECT().String().Return("remote SmartEnergyManagementPs feature").Maybe()
		localFeature.EXPECT().RequestRemoteData(
			model.FunctionTypeSmartEnergyManagementPsData,
			mock.Anything,
			mock.Anything,
			mock.Anything,
		).Return(nil, model.NewErrorType(model.ErrorNumberTypeCommandRejected, "rejected"))

		wrapper := OHPCFWrapper{uc: &cemohpcf.OHPCF{}, localEntity: localEntity, debug: true}
		wrapper.Refresh(remoteEntity)
	})
}
