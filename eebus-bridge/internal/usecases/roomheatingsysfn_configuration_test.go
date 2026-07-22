package usecases

import (
	"context"
	"errors"
	"testing"

	eebusapi "github.com/enbility/eebus-go/api"
	cacrhsf "github.com/enbility/eebus-go/usecases/ca/crhsf"
	ucmocks "github.com/enbility/eebus-go/usecases/mocks"
	spineapi "github.com/enbility/spine-go/api"
	spinemocks "github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

func TestUpstreamRoomHeatingSystemFunctionConfigurationSelectsCRHSF(t *testing.T) {
	facade := NewUpstreamRoomHeatingSystemFunctionConfiguration(clientUsecaseLocalEntity(t))
	client, ok := facade.UseCase().(*cacrhsf.CRHSF)
	if !ok {
		t.Fatalf("UseCase() = %T, want *crhsf.CRHSF", facade.UseCase())
	}
	if client.EventCB != nil {
		t.Fatal("upstream CRHSF has an event callback; MRHSF must remain the sole state-event owner")
	}
	writer, ok := facade.operationModeWriter.(*upstreamRoomHeatingOperationModeWriter)
	if !ok {
		t.Fatalf("operationModeWriter = %T, want *upstreamRoomHeatingOperationModeWriter", facade.operationModeWriter)
	}
	if writer.client != client {
		t.Fatalf("writer client = %T, want facade CRHSF client", writer.client)
	}
	if _, ok := writer.inspector.(bridgeRoomHeatingSystemFunctionCapabilityInspector); !ok {
		t.Fatalf("writer inspector = %T, want bridge capability inspector", writer.inspector)
	}
}

type phase2RoomHeatingCapabilityInspector struct {
	state  RoomHeatingSystemFunctionState
	entity spineapi.EntityRemoteInterface
	err    error
}

func (i *phase2RoomHeatingCapabilityInspector) State(
	entity spineapi.EntityRemoteInterface,
) (RoomHeatingSystemFunctionState, error) {
	i.entity = entity
	return i.state, i.err
}

type phase2RoomHeatingWriter struct {
	entity spineapi.EntityRemoteInterface
	mode   string
	err    error
}

func (w *phase2RoomHeatingWriter) WriteOperationMode(
	_ context.Context,
	entity spineapi.EntityRemoteInterface,
	mode string,
) error {
	w.entity = entity
	w.mode = mode
	return w.err
}

func TestCRHSFConfigurationFacadeComposesUpstreamEntityAndSelectedStrategies(t *testing.T) {
	device := spinemocks.NewDeviceRemoteInterface(t)
	device.EXPECT().Ski().Return("ab:cd").Maybe()
	entity := spinemocks.NewEntityRemoteInterface(t)
	entity.EXPECT().Device().Return(device).Maybe()
	client := ucmocks.NewCaCRHSFInterface(t)
	client.EXPECT().RemoteEntitiesScenarios().Return([]eebusapi.RemoteEntityScenarios{{
		Entity: entity, Scenarios: []uint{1},
	}})
	inspector := &phase2RoomHeatingCapabilityInspector{
		state: RoomHeatingSystemFunctionState{ModeWritable: true},
	}
	writer := &phase2RoomHeatingWriter{}
	facade := newCRHSFConfigurationFacade(
		client,
		crhsfEntityResolver{useCase: client},
		inspector,
		writer,
	)

	resolution := facade.CompatibleEntity("ABCD")
	if resolution.Entity != entity || resolution.DeviceCount != 1 {
		t.Fatalf("CompatibleEntity() = %+v, want upstream CRHSF entity", resolution)
	}
	state, err := facade.State(entity)
	if err != nil || !state.ModeWritable || inspector.entity != entity {
		t.Fatalf("State() = %+v, %v; inspector entity = %p", state, err, inspector.entity)
	}
	if err := facade.WriteOperationMode(context.Background(), entity, "on"); err != nil {
		t.Fatalf("WriteOperationMode() error = %v", err)
	}
	if writer.entity != entity || writer.mode != "on" {
		t.Fatalf("writer entity/mode = %p/%q, want upstream entity/on", writer.entity, writer.mode)
	}
	if facade.UseCase() != client {
		t.Fatalf("UseCase() = %T, want injected upstream client", facade.UseCase())
	}
}

func TestBridgeRoomHeatingCapabilityInspectorPreservesUnavailableAndReadOnly(t *testing.T) {
	for _, test := range []struct {
		name           string
		writeOperation bool
		changeable     *bool
		missingData    bool
		wantWritable   bool
		wantError      error
	}{
		{
			name: "writable", writeOperation: true, wantWritable: true,
		},
		{
			name: "write operation missing",
		},
		{
			name: "explicitly not changeable", writeOperation: true, changeable: ptr(false),
		},
		{
			name: "cache incomplete", missingData: true, wantError: ErrRoomHeatingSysFnDataUnavailable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			feature := spinemocks.NewFeatureRemoteInterface(t)
			if test.missingData {
				feature.EXPECT().DataCopy(model.FunctionTypeHvacSystemFunctionDescriptionListData).Return(nil)
			} else {
				id := model.HvacSystemFunctionIdType(1)
				feature.EXPECT().DataCopy(model.FunctionTypeHvacSystemFunctionDescriptionListData).Return(
					&model.HvacSystemFunctionDescriptionListDataType{HvacSystemFunctionDescriptionData: []model.HvacSystemFunctionDescriptionDataType{{
						SystemFunctionId: &id, SystemFunctionType: ptr(model.HvacSystemFunctionTypeTypeHeating),
					}}},
				)
				feature.EXPECT().DataCopy(model.FunctionTypeHvacSystemFunctionListData).Return(
					&model.HvacSystemFunctionListDataType{HvacSystemFunctionData: []model.HvacSystemFunctionDataType{{
						SystemFunctionId: &id, IsOperationModeIdChangeable: test.changeable,
					}}},
				)
				operation := spinemocks.NewOperationsInterface(t)
				operation.EXPECT().Write().Return(test.writeOperation)
				feature.EXPECT().Operations().Return(map[model.FunctionType]spineapi.OperationsInterface{
					model.FunctionTypeHvacSystemFunctionListData: operation,
				})
			}
			entity := spinemocks.NewEntityRemoteInterface(t)
			entity.EXPECT().FeatureOfTypeAndRole(model.FeatureTypeTypeHvac, model.RoleTypeServer).Return(feature)
			state, err := (bridgeRoomHeatingSystemFunctionCapabilityInspector{}).State(entity)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("State() error = %v, want %v", err, test.wantError)
			}
			if state.ModeWritable != test.wantWritable {
				t.Fatalf("State() = %+v, want writable=%t", state, test.wantWritable)
			}
			if state.OperationMode != "" || len(state.AvailableModes) != 0 {
				t.Fatalf("State() leaked read ownership: %+v", state)
			}
		})
	}
}

func TestBridgeRoomHeatingCapabilityInspectorRejectsIncompleteCache(t *testing.T) {
	heatingID := model.HvacSystemFunctionIdType(1)
	otherID := model.HvacSystemFunctionIdType(2)
	heatingDescriptions := &model.HvacSystemFunctionDescriptionListDataType{
		HvacSystemFunctionDescriptionData: []model.HvacSystemFunctionDescriptionDataType{{
			SystemFunctionId:   &heatingID,
			SystemFunctionType: ptr(model.HvacSystemFunctionTypeTypeHeating),
		}},
	}
	tests := []struct {
		name         string
		missingHVAC  bool
		descriptions *model.HvacSystemFunctionDescriptionListDataType
		data         *model.HvacSystemFunctionListDataType
	}{
		{name: "missing HVAC feature", missingHVAC: true},
		{name: "no heating function", descriptions: &model.HvacSystemFunctionDescriptionListDataType{}},
		{name: "missing function data", descriptions: heatingDescriptions},
		{
			name:         "no matching function data",
			descriptions: heatingDescriptions,
			data: &model.HvacSystemFunctionListDataType{HvacSystemFunctionData: []model.HvacSystemFunctionDataType{{
				SystemFunctionId: &otherID,
			}}},
		},
		{
			name:         "duplicate matching function data",
			descriptions: heatingDescriptions,
			data: &model.HvacSystemFunctionListDataType{HvacSystemFunctionData: []model.HvacSystemFunctionDataType{
				{SystemFunctionId: &heatingID},
				{SystemFunctionId: &heatingID},
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entity := spinemocks.NewEntityRemoteInterface(t)
			if test.missingHVAC {
				entity.EXPECT().FeatureOfTypeAndRole(model.FeatureTypeTypeHvac, model.RoleTypeServer).Return(nil)
			} else {
				feature := spinemocks.NewFeatureRemoteInterface(t)
				entity.EXPECT().FeatureOfTypeAndRole(model.FeatureTypeTypeHvac, model.RoleTypeServer).Return(feature)
				feature.EXPECT().DataCopy(model.FunctionTypeHvacSystemFunctionDescriptionListData).Return(test.descriptions)
				if len(test.descriptions.HvacSystemFunctionDescriptionData) == 1 {
					feature.EXPECT().DataCopy(model.FunctionTypeHvacSystemFunctionListData).Return(test.data)
				}
			}

			if _, err := (bridgeRoomHeatingSystemFunctionCapabilityInspector{}).State(entity); !errors.Is(err, ErrRoomHeatingSysFnDataUnavailable) {
				t.Fatalf("State() error = %v, want data unavailable", err)
			}
		})
	}
}

func TestCRHSFConfigurationFacadeFailsClosedWhenIncomplete(t *testing.T) {
	var nilFacade *CRHSFConfigurationFacade
	if nilFacade.UseCase() != nil {
		t.Fatal("nil facade returned a use case")
	}
	if resolution := nilFacade.CompatibleEntity("ABCD"); resolution != (eebus.EntityResolution{}) {
		t.Fatalf("CompatibleEntity() = %+v", resolution)
	}
	if _, err := nilFacade.State(nil); !errors.Is(err, ErrRoomHeatingSysFnDataUnavailable) {
		t.Fatalf("State() error = %v, want data unavailable", err)
	}
	if err := nilFacade.WriteOperationMode(context.Background(), nil, "on"); !errors.Is(err, ErrRoomHeatingSysFnNotWritable) {
		t.Fatalf("WriteOperationMode() error = %v, want not writable", err)
	}

	empty := NewUpstreamRoomHeatingSystemFunctionConfiguration(nil)
	if empty.UseCase() != nil {
		t.Fatal("nil local entity initialized CRHSF")
	}
	if resolution := empty.CompatibleEntity("ABCD"); resolution != (eebus.EntityResolution{}) {
		t.Fatalf("empty CompatibleEntity() = %+v", resolution)
	}
	if _, err := empty.State(nil); !errors.Is(err, ErrRoomHeatingSysFnDataUnavailable) {
		t.Fatalf("empty State() error = %v, want data unavailable", err)
	}
	if err := empty.WriteOperationMode(context.Background(), nil, "on"); !errors.Is(err, ErrRoomHeatingSysFnNotWritable) {
		t.Fatalf("empty WriteOperationMode() error = %v, want not writable", err)
	}
	if resolution := (crhsfEntityResolver{}).CompatibleEntity("ABCD"); resolution != (eebus.EntityResolution{}) {
		t.Fatalf("empty resolver = %+v", resolution)
	}
	if _, err := (bridgeRoomHeatingSystemFunctionCapabilityInspector{}).State(nil); !errors.Is(err, ErrRoomHeatingSysFnDataUnavailable) {
		t.Fatalf("empty inspector error = %v", err)
	}
}
