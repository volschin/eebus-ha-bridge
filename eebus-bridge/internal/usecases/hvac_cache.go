package usecases

import (
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

// These generic HVAC cache helpers remain for the bridge-local room-heating
// implementation. DHW CDSF state and capability resolution live in eebus-go.
func hvacServer(entity spineapi.EntityRemoteInterface) spineapi.FeatureRemoteInterface {
	if entity == nil {
		return nil
	}
	return entity.FeatureOfTypeAndRole(model.FeatureTypeTypeHvac, model.RoleTypeServer)
}

func hvacSystemFunction(
	feature spineapi.FeatureRemoteInterface,
	id model.HvacSystemFunctionIdType,
) (model.HvacSystemFunctionDataType, bool) {
	data, ok := feature.DataCopy(model.FunctionTypeHvacSystemFunctionListData).(*model.HvacSystemFunctionListDataType)
	if !ok || data == nil {
		return model.HvacSystemFunctionDataType{}, false
	}
	for _, entry := range data.HvacSystemFunctionData {
		if entry.SystemFunctionId != nil && *entry.SystemFunctionId == id {
			return entry, true
		}
	}
	return model.HvacSystemFunctionDataType{}, false
}

func operationModesForSystem(
	feature spineapi.FeatureRemoteInterface,
	systemID model.HvacSystemFunctionIdType,
) ([]string, map[string]model.HvacOperationModeIdType, bool) {
	data, ok := feature.DataCopy(model.FunctionTypeHvacSystemFunctionOperationModeRelationListData).(*model.HvacSystemFunctionOperationModeRelationListDataType)
	if !ok || data == nil {
		return nil, nil, false
	}
	var modeIDs []model.HvacOperationModeIdType
	for _, relation := range data.HvacSystemFunctionOperationModeRelationData {
		if relation.SystemFunctionId != nil && *relation.SystemFunctionId == systemID {
			modeIDs = append(modeIDs, relation.OperationModeId...)
		}
	}
	if len(modeIDs) == 0 {
		return nil, nil, false
	}
	modes := make([]string, 0, len(modeIDs))
	idsForType := make(map[string][]model.HvacOperationModeIdType, len(modeIDs))
	for _, id := range modeIDs {
		modeType, ok := operationModeType(feature, id)
		if !ok {
			return nil, nil, false
		}
		typeName := string(modeType)
		ids := idsForType[typeName]
		duplicateID := false
		for _, knownID := range ids {
			if knownID == id {
				duplicateID = true
				break
			}
		}
		if duplicateID {
			continue
		}
		if len(ids) == 0 {
			modes = append(modes, typeName)
		}
		idsForType[typeName] = append(ids, id)
	}

	// A type remains readable when a device advertises it through multiple
	// IDs, but it is not safe to choose one of those IDs for a write.
	idForType := make(map[string]model.HvacOperationModeIdType, len(idsForType))
	for modeType, ids := range idsForType {
		if len(ids) == 1 {
			idForType[modeType] = ids[0]
		}
	}
	return modes, idForType, true
}

func operationModeType(
	feature spineapi.FeatureRemoteInterface,
	id model.HvacOperationModeIdType,
) (model.HvacOperationModeTypeType, bool) {
	data, ok := feature.DataCopy(model.FunctionTypeHvacOperationModeDescriptionListData).(*model.HvacOperationModeDescriptionListDataType)
	if !ok || data == nil {
		return "", false
	}
	for _, description := range data.HvacOperationModeDescriptionData {
		if description.OperationModeId != nil && *description.OperationModeId == id && description.OperationModeType != nil {
			return *description.OperationModeType, true
		}
	}
	return "", false
}

func boolPtrNotFalse(value *bool) bool {
	return value == nil || *value
}
