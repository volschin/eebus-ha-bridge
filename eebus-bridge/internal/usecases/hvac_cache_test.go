package usecases

import (
	"testing"

	"github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
)

// These fail-closed cases used to be covered by the deleted DHW cache tests,
// which exercised the same helpers before DHW's own resolution moved to
// eebus-go. hvacSystemFunction, operationModesForSystem and operationModeType
// remain in use by RoomHeatingSystemFunction, so their negative paths still
// need direct coverage here.
func TestHvacCacheHelpersFailClosedOnMissingData(t *testing.T) {
	missing := mocks.NewFeatureRemoteInterface(t)
	missing.On("DataCopy", model.FunctionTypeHvacSystemFunctionListData).Return(nil)
	missing.On("DataCopy", model.FunctionTypeHvacSystemFunctionOperationModeRelationListData).Return(nil)
	missing.On("DataCopy", model.FunctionTypeHvacOperationModeDescriptionListData).Return(nil)

	if _, ok := hvacSystemFunction(missing, 3); ok {
		t.Fatal("hvacSystemFunction() accepted missing data")
	}
	if _, _, ok := operationModesForSystem(missing, 3); ok {
		t.Fatal("operationModesForSystem() accepted missing data")
	}
	if _, ok := operationModeType(missing, 0); ok {
		t.Fatal("operationModeType() accepted missing data")
	}
}

func TestHvacCacheHelpersRejectUnmatchedData(t *testing.T) {
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeHvacSystemFunctionListData).Return(
		&model.HvacSystemFunctionListDataType{},
	)
	feature.On("DataCopy", model.FunctionTypeHvacSystemFunctionOperationModeRelationListData).Return(
		&model.HvacSystemFunctionOperationModeRelationListDataType{},
	)
	feature.On("DataCopy", model.FunctionTypeHvacOperationModeDescriptionListData).Return(
		&model.HvacOperationModeDescriptionListDataType{},
	)

	if _, ok := hvacSystemFunction(feature, 3); ok {
		t.Fatal("hvacSystemFunction() accepted an unmatched list")
	}
	if _, _, ok := operationModesForSystem(feature, 3); ok {
		t.Fatal("operationModesForSystem() accepted an empty relation list")
	}
	if _, ok := operationModeType(feature, 0); ok {
		t.Fatal("operationModeType() accepted an unmatched list")
	}
}

// A related system function whose operation-mode IDs don't resolve to a
// description must fail closed rather than silently drop the unresolved mode.
func TestOperationModesForSystemFailsClosedOnUnresolvedModeType(t *testing.T) {
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeHvacSystemFunctionOperationModeRelationListData).Return(
		&model.HvacSystemFunctionOperationModeRelationListDataType{
			HvacSystemFunctionOperationModeRelationData: []model.HvacSystemFunctionOperationModeRelationDataType{
				{SystemFunctionId: ptr(model.HvacSystemFunctionIdType(3)), OperationModeId: []model.HvacOperationModeIdType{0}},
			},
		},
	)
	feature.On("DataCopy", model.FunctionTypeHvacOperationModeDescriptionListData).Return(
		&model.HvacOperationModeDescriptionListDataType{},
	)

	if _, _, ok := operationModesForSystem(feature, 3); ok {
		t.Fatal("operationModesForSystem() accepted an unresolved mode type")
	}
}
