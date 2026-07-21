package usecases

import (
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

// cachedDHWSystemFunctionCapabilityInspector is deliberately read-only. It
// derives conservative writeability from cached HVAC data and never sends an
// EEBUS command.
type cachedDHWSystemFunctionCapabilityInspector struct{}

func (cachedDHWSystemFunctionCapabilityInspector) State(
	entity spineapi.EntityRemoteInterface,
) (DHWSystemFunctionState, error) {
	remote := hvacServer(entity)
	if remote == nil {
		return DHWSystemFunctionState{}, ErrDHWSysFnDataUnavailable
	}
	resolved, err := resolveDHWSystemFunction(remote)
	if err != nil {
		return DHWSystemFunctionState{}, err
	}
	overrunOp := remote.Operations()[model.FunctionTypeHvacOverrunListData]
	systemOp := remote.Operations()[model.FunctionTypeHvacSystemFunctionListData]
	return DHWSystemFunctionState{
		BoostStatus:    string(*resolved.overrun.OverrunStatus),
		BoostWritable:  overrunOp != nil && overrunOp.Write() && boolPtrNotFalse(resolved.overrun.IsOverrunStatusChangeable),
		OperationMode:  string(resolved.currentModeType),
		AvailableModes: resolved.availableModeTypes,
		// Like the boost path, a missing changeability flag must not hide a
		// write the device advertises via operations; only explicit false blocks.
		ModeWritable: systemOp != nil && systemOp.Write() &&
			boolPtrNotFalse(resolved.system.IsOperationModeIdChangeable),
	}, nil
}

type resolvedDHWSysFn struct {
	system             model.HvacSystemFunctionDataType
	systemID           model.HvacSystemFunctionIdType
	overrun            model.HvacOverrunDataType
	overrunID          model.HvacOverrunIdType
	currentModeType    model.HvacOperationModeTypeType
	availableModeTypes []string
	modeIDForType      map[string]model.HvacOperationModeIdType
}

func hvacServer(entity spineapi.EntityRemoteInterface) spineapi.FeatureRemoteInterface {
	if entity == nil {
		return nil
	}
	return entity.FeatureOfTypeAndRole(model.FeatureTypeTypeHvac, model.RoleTypeServer)
}

func resolveDHWSystemFunction(feature spineapi.FeatureRemoteInterface) (resolvedDHWSysFn, error) {
	systemID, ok := dhwSystemFunctionID(feature)
	if !ok {
		return resolvedDHWSysFn{}, ErrDHWSysFnDataUnavailable
	}
	system, ok := hvacSystemFunction(feature, systemID)
	if !ok || system.CurrentOperationModeId == nil {
		return resolvedDHWSysFn{}, ErrDHWSysFnDataUnavailable
	}
	overrunID, ok := oneTimeDHWOverrunID(feature, systemID)
	if !ok {
		return resolvedDHWSysFn{}, ErrDHWSysFnDataUnavailable
	}
	overrun, ok := hvacOverrun(feature, overrunID)
	if !ok || overrun.OverrunStatus == nil {
		return resolvedDHWSysFn{}, ErrDHWSysFnDataUnavailable
	}
	modes, idForType, ok := operationModesForSystem(feature, systemID)
	if !ok {
		return resolvedDHWSysFn{}, ErrDHWSysFnDataUnavailable
	}
	currentMode, ok := operationModeType(feature, *system.CurrentOperationModeId)
	if !ok {
		return resolvedDHWSysFn{}, ErrDHWSysFnDataUnavailable
	}
	return resolvedDHWSysFn{
		system:             system,
		systemID:           systemID,
		overrun:            overrun,
		overrunID:          overrunID,
		currentModeType:    currentMode,
		availableModeTypes: modes,
		modeIDForType:      idForType,
	}, nil
}

func dhwSystemFunctionID(feature spineapi.FeatureRemoteInterface) (model.HvacSystemFunctionIdType, bool) {
	data, ok := feature.DataCopy(model.FunctionTypeHvacSystemFunctionDescriptionListData).(*model.HvacSystemFunctionDescriptionListDataType)
	if !ok || data == nil {
		return 0, false
	}
	var found []model.HvacSystemFunctionIdType
	for _, description := range data.HvacSystemFunctionDescriptionData {
		if description.SystemFunctionId != nil && description.SystemFunctionType != nil &&
			*description.SystemFunctionType == model.HvacSystemFunctionTypeTypeDhw {
			found = append(found, *description.SystemFunctionId)
		}
	}
	if len(found) != 1 {
		return 0, false
	}
	return found[0], true
}

func hvacSystemFunction(feature spineapi.FeatureRemoteInterface, id model.HvacSystemFunctionIdType) (model.HvacSystemFunctionDataType, bool) {
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

func oneTimeDHWOverrunID(feature spineapi.FeatureRemoteInterface, systemID model.HvacSystemFunctionIdType) (model.HvacOverrunIdType, bool) {
	data, ok := feature.DataCopy(model.FunctionTypeHvacOverrunDescriptionListData).(*model.HvacOverrunDescriptionListDataType)
	if !ok || data == nil {
		return 0, false
	}
	var found []model.HvacOverrunIdType
	for _, description := range data.HvacOverrunDescriptionData {
		if description.OverrunId == nil || description.OverrunType == nil ||
			*description.OverrunType != model.HvacOverrunTypeTypeOneTimeDhw ||
			!containsSystemFunction(description.AffectedSystemFunctionId, systemID) {
			continue
		}
		found = append(found, *description.OverrunId)
	}
	if len(found) != 1 {
		return 0, false
	}
	return found[0], true
}

func hvacOverrun(feature spineapi.FeatureRemoteInterface, id model.HvacOverrunIdType) (model.HvacOverrunDataType, bool) {
	data, ok := feature.DataCopy(model.FunctionTypeHvacOverrunListData).(*model.HvacOverrunListDataType)
	if !ok || data == nil {
		return model.HvacOverrunDataType{}, false
	}
	for _, entry := range data.HvacOverrunData {
		if entry.OverrunId != nil && *entry.OverrunId == id {
			return entry, true
		}
	}
	return model.HvacOverrunDataType{}, false
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
			modeIDs = relation.OperationModeId
			break
		}
	}
	if len(modeIDs) == 0 {
		return nil, nil, false
	}
	modes := make([]string, 0, len(modeIDs))
	idForType := make(map[string]model.HvacOperationModeIdType, len(modeIDs))
	for _, id := range modeIDs {
		modeType, ok := operationModeType(feature, id)
		if !ok {
			return nil, nil, false
		}
		modes = append(modes, string(modeType))
		idForType[string(modeType)] = id
	}
	return modes, idForType, true
}

func operationModeType(feature spineapi.FeatureRemoteInterface, id model.HvacOperationModeIdType) (model.HvacOperationModeTypeType, bool) {
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

func containsSystemFunction(ids []model.HvacSystemFunctionIdType, want model.HvacSystemFunctionIdType) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func boolPtrNotFalse(value *bool) bool {
	return value == nil || *value
}
