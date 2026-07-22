package usecases

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
)

func newRoomHeatingSysFnFeature(
	t *testing.T,
	currentModeID model.HvacOperationModeIdType,
	writable bool,
) *mocks.FeatureRemoteInterface {
	return newRoomHeatingSysFnFeatureWithModes(t, currentModeID, writable,
		[]model.HvacOperationModeDescriptionDataType{
			{OperationModeId: ptr(model.HvacOperationModeIdType(0)), OperationModeType: ptr(model.HvacOperationModeTypeTypeOff)},
			{OperationModeId: ptr(model.HvacOperationModeIdType(1)), OperationModeType: ptr(model.HvacOperationModeTypeTypeOn)},
			{OperationModeId: ptr(model.HvacOperationModeIdType(2)), OperationModeType: ptr(model.HvacOperationModeTypeTypeAuto)},
		},
		[]model.HvacOperationModeIdType{0, 1, 2},
	)
}

func newRoomHeatingSysFnFeatureWithModes(
	t *testing.T,
	currentModeID model.HvacOperationModeIdType,
	writable bool,
	descriptions []model.HvacOperationModeDescriptionDataType,
	modeIDs []model.HvacOperationModeIdType,
) *mocks.FeatureRemoteInterface {
	t.Helper()
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeHvacSystemFunctionDescriptionListData).Return(
		&model.HvacSystemFunctionDescriptionListDataType{HvacSystemFunctionDescriptionData: []model.HvacSystemFunctionDescriptionDataType{
			{SystemFunctionId: ptr(model.HvacSystemFunctionIdType(0)), SystemFunctionType: ptr(model.HvacSystemFunctionTypeTypeHeating)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeHvacSystemFunctionListData).Return(
		&model.HvacSystemFunctionListDataType{HvacSystemFunctionData: []model.HvacSystemFunctionDataType{
			{SystemFunctionId: ptr(model.HvacSystemFunctionIdType(0)), CurrentOperationModeId: ptr(currentModeID)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeHvacOperationModeDescriptionListData).Return(
		&model.HvacOperationModeDescriptionListDataType{HvacOperationModeDescriptionData: descriptions},
	)
	feature.On("DataCopy", model.FunctionTypeHvacSystemFunctionOperationModeRelationListData).Return(
		&model.HvacSystemFunctionOperationModeRelationListDataType{HvacSystemFunctionOperationModeRelationData: []model.HvacSystemFunctionOperationModeRelationDataType{
			{SystemFunctionId: ptr(model.HvacSystemFunctionIdType(0)), OperationModeId: modeIDs},
		}},
	)
	operation := mocks.NewOperationsInterface(t)
	operation.On("Write").Return(writable)
	operation.On("Read").Return(true).Maybe()
	feature.On("Operations").Return(map[model.FunctionType]spineapi.OperationsInterface{
		model.FunctionTypeHvacSystemFunctionListData: operation,
	})
	feature.On("Address").Return(&model.FeatureAddressType{}).Maybe()
	feature.On("String").Return("remote room HVAC").Maybe()
	return feature
}

func TestRoomHeatingSysFnAmbiguousModeTypeIsReadableButNotWritable(t *testing.T) {
	feature := newRoomHeatingSysFnFeatureWithModes(t, 1, true,
		[]model.HvacOperationModeDescriptionDataType{
			{OperationModeId: ptr(model.HvacOperationModeIdType(0)), OperationModeType: ptr(model.HvacOperationModeTypeTypeOff)},
			{OperationModeId: ptr(model.HvacOperationModeIdType(1)), OperationModeType: ptr(model.HvacOperationModeTypeTypeOn)},
			{OperationModeId: ptr(model.HvacOperationModeIdType(2)), OperationModeType: ptr(model.HvacOperationModeTypeTypeAuto)},
			{OperationModeId: ptr(model.HvacOperationModeIdType(3)), OperationModeType: ptr(model.HvacOperationModeTypeTypeOn)},
		},
		[]model.HvacOperationModeIdType{0, 1, 3, 2},
	)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeHvac, model.RoleTypeServer).Return(feature)

	state, err := (&RoomHeatingSystemFunction{}).State(entity)
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	wantModes := []string{"off", "on", "auto"}
	if len(state.AvailableModes) != len(wantModes) {
		t.Fatalf("AvailableModes = %v, want %v", state.AvailableModes, wantModes)
	}
	for index := range wantModes {
		if state.AvailableModes[index] != wantModes[index] {
			t.Fatalf("AvailableModes = %v, want %v", state.AvailableModes, wantModes)
		}
	}
	if err := (&RoomHeatingSystemFunction{}).WriteOperationMode(context.Background(), entity, "on"); !errors.Is(err, ErrRoomHeatingSysFnInvalidMode) {
		t.Fatalf("WriteOperationMode() error = %v, want ErrRoomHeatingSysFnInvalidMode", err)
	}
}

func TestRoomHeatingSysFnStateResolvesCurrentAndAvailableModes(t *testing.T) {
	feature := newRoomHeatingSysFnFeature(t, 0, true)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeHvac, model.RoleTypeServer).Return(feature)

	state, err := (&RoomHeatingSystemFunction{}).State(entity)
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if state.OperationMode != "off" || !state.ModeWritable {
		t.Errorf("State() = %+v", state)
	}
	if len(state.AvailableModes) != 3 {
		t.Errorf("AvailableModes = %v, want 3 entries", state.AvailableModes)
	}
}

func TestRoomHeatingSysFnMissingChangeableFlagDoesNotHideWrite(t *testing.T) {
	feature := newRoomHeatingSysFnFeature(t, 1, true)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeHvac, model.RoleTypeServer).Return(feature)

	state, err := (&RoomHeatingSystemFunction{}).State(entity)
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if !state.ModeWritable {
		t.Errorf("ModeWritable = false, want true (nil flag must not hide advertised write)")
	}
}

func TestRoomHeatingSysFnWriteRejectsModeNotInRelation(t *testing.T) {
	feature := newRoomHeatingSysFnFeature(t, 0, true)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeHvac, model.RoleTypeServer).Return(feature)

	err := (&RoomHeatingSystemFunction{}).WriteOperationMode(context.Background(), entity, "cool")
	if !errors.Is(err, ErrRoomHeatingSysFnInvalidMode) {
		t.Fatalf("WriteOperationMode() error = %v, want ErrRoomHeatingSysFnInvalidMode", err)
	}
}

func TestRoomHeatingSysFnWriteUpdatesOperationMode(t *testing.T) {
	feature := newRoomHeatingSysFnFeature(t, 0, true)
	local, entity, written := hvacWriteHarness(t, feature)
	room := &RoomHeatingSystemFunction{localEntity: local}

	if err := room.WriteOperationMode(context.Background(), entity, "auto"); err != nil {
		t.Fatalf("WriteOperationMode() error = %v", err)
	}
	entries := written.cmd.HvacSystemFunctionListData.HvacSystemFunctionData
	if len(entries) != 1 || entries[0].CurrentOperationModeId == nil || *entries[0].CurrentOperationModeId != 2 {
		t.Fatalf("written system function data = %+v", entries)
	}
}

func TestRoomHeatingSysFnWriteGuardsAndRejection(t *testing.T) {
	notWritable := newRoomHeatingSysFnFeature(t, 0, false)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeHvac, model.RoleTypeServer).Return(notWritable)
	if err := (&RoomHeatingSystemFunction{}).WriteOperationMode(context.Background(), entity, "on"); !errors.Is(err, ErrRoomHeatingSysFnNotWritable) {
		t.Fatalf("not-writable error = %v", err)
	}

	feature := newRoomHeatingSysFnFeature(t, 0, true)
	local, rejectingEntity, _ := hvacWriteHarnessWithErrno(t, feature, 4)
	room := &RoomHeatingSystemFunction{localEntity: local}
	if err := room.WriteOperationMode(context.Background(), rejectingEntity, "on"); !errors.Is(err, ErrRoomHeatingSysFnRejected) {
		t.Fatalf("rejection error = %v", err)
	}

	missingLocalEntity := mocks.NewEntityRemoteInterface(t)
	missingLocalEntity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeHvac, model.RoleTypeServer).Return(feature)
	if err := (&RoomHeatingSystemFunction{}).WriteOperationMode(context.Background(), missingLocalEntity, "on"); !errors.Is(err, ErrRoomHeatingSysFnDataUnavailable) {
		t.Fatalf("missing-local error = %v", err)
	}
}

func TestRoomHeatingSysFnWriteTimesOutWithoutDeviceResult(t *testing.T) {
	feature := newRoomHeatingSysFnFeature(t, 0, true)
	local, entity, _ := hvacWriteHarnessWithResult(t, feature, nil)
	room := &RoomHeatingSystemFunction{localEntity: local}
	previousTimeout := hvacWriteTimeout
	hvacWriteTimeout = time.Millisecond
	t.Cleanup(func() { hvacWriteTimeout = previousTimeout })

	err := room.WriteOperationMode(context.Background(), entity, "on")
	if err == nil || !strings.Contains(err.Error(), "timed out waiting for room heating operation mode result") {
		t.Fatalf("WriteOperationMode() error = %v, want result timeout", err)
	}
}
