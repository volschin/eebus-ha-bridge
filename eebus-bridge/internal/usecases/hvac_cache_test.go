package usecases

import (
	"reflect"
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

func TestOperationModesForSystemDeduplicatesTypesAndWithholdsAmbiguousWriteIDs(t *testing.T) {
	tests := []struct {
		name      string
		relations [][]model.HvacOperationModeIdType
		wantModes []string
		wantIDs   map[string]model.HvacOperationModeIdType
	}{
		{
			name:      "unique types",
			relations: [][]model.HvacOperationModeIdType{{0, 1, 2}},
			wantModes: []string{"off", "on", "auto"},
			wantIDs:   map[string]model.HvacOperationModeIdType{"off": 0, "on": 1, "auto": 2},
		},
		{
			name:      "same ID repeated",
			relations: [][]model.HvacOperationModeIdType{{0, 1, 1, 2}},
			wantModes: []string{"off", "on", "auto"},
			wantIDs:   map[string]model.HvacOperationModeIdType{"off": 0, "on": 1, "auto": 2},
		},
		{
			name:      "same type has distinct IDs",
			relations: [][]model.HvacOperationModeIdType{{0, 1}, {3, 2}},
			wantModes: []string{"off", "on", "auto"},
			wantIDs:   map[string]model.HvacOperationModeIdType{"off": 0, "auto": 2},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			feature := mocks.NewFeatureRemoteInterface(t)
			relationData := make([]model.HvacSystemFunctionOperationModeRelationDataType, 0, len(test.relations))
			for _, ids := range test.relations {
				relationData = append(relationData, model.HvacSystemFunctionOperationModeRelationDataType{
					SystemFunctionId: ptr(model.HvacSystemFunctionIdType(3)),
					OperationModeId:  ids,
				})
			}
			feature.On("DataCopy", model.FunctionTypeHvacSystemFunctionOperationModeRelationListData).Return(
				&model.HvacSystemFunctionOperationModeRelationListDataType{
					HvacSystemFunctionOperationModeRelationData: relationData,
				},
			)
			feature.On("DataCopy", model.FunctionTypeHvacOperationModeDescriptionListData).Return(
				&model.HvacOperationModeDescriptionListDataType{
					HvacOperationModeDescriptionData: []model.HvacOperationModeDescriptionDataType{
						{OperationModeId: ptr(model.HvacOperationModeIdType(0)), OperationModeType: ptr(model.HvacOperationModeTypeTypeOff)},
						{OperationModeId: ptr(model.HvacOperationModeIdType(1)), OperationModeType: ptr(model.HvacOperationModeTypeTypeOn)},
						{OperationModeId: ptr(model.HvacOperationModeIdType(2)), OperationModeType: ptr(model.HvacOperationModeTypeTypeAuto)},
						{OperationModeId: ptr(model.HvacOperationModeIdType(3)), OperationModeType: ptr(model.HvacOperationModeTypeTypeOn)},
					},
				},
			)

			modes, ids, ok := operationModesForSystem(feature, 3)
			if !ok {
				t.Fatal("operationModesForSystem() failed")
			}
			if !reflect.DeepEqual(modes, test.wantModes) {
				t.Errorf("modes = %v, want %v", modes, test.wantModes)
			}
			if !reflect.DeepEqual(ids, test.wantIDs) {
				t.Errorf("write IDs = %v, want %v", ids, test.wantIDs)
			}
		})
	}
}
