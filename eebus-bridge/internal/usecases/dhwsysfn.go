package usecases

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	usecase "github.com/enbility/eebus-go/usecases/usecase"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

const dhwSysFnUseCaseSupportUpdate eebusapi.EventType = "bridge-dhw-system-function-support-update"

var (
	ErrDHWSysFnDataUnavailable = errors.New("DHW system function data unavailable")
	ErrDHWSysFnNotWritable     = errors.New("DHW system function is not writable")
	ErrDHWSysFnInvalidMode     = errors.New("DHW operation mode is not advertised")
	ErrDHWSysFnRejected        = errors.New("DHW system function write rejected by device")
)

// DHWSystemFunctionState is the current DHW boost state and operation mode
// resolved from the remote DHWCircuit HVAC server metadata.
type DHWSystemFunctionState struct {
	BoostStatus    string
	BoostWritable  bool
	OperationMode  string
	AvailableModes []string
	ModeWritable   bool
}

// DHWSystemFunction implements the Configuration of DHW System Function client
// role for one-time DHW overrun/boost and DHW operation mode.
type DHWSystemFunction struct {
	*usecase.UseCaseBase
	localEntity spineapi.EntityLocalInterface
	bus         *eebus.EventBus
	registry    *eebus.DeviceRegistry
	debug       bool
}

// NewDHWSystemFunction creates the Configuration Appliance client use case.
func NewDHWSystemFunction(
	localEntity spineapi.EntityLocalInterface,
	bus *eebus.EventBus,
	registry *eebus.DeviceRegistry,
	debug bool,
) *DHWSystemFunction {
	d := &DHWSystemFunction{
		localEntity: localEntity,
		bus:         bus,
		registry:    registry,
		debug:       debug,
	}
	d.UseCaseBase = usecase.NewUseCaseBase(
		localEntity,
		model.UseCaseActorTypeConfigurationAppliance,
		model.UseCaseNameTypeConfigurationOfDhwSystemFunction,
		"1.0.0",
		"release",
		[]eebusapi.UseCaseScenario{
			{
				Scenario:       model.UseCaseScenarioSupportType(1),
				Mandatory:      true,
				ServerFeatures: []model.FeatureTypeType{model.FeatureTypeTypeHvac},
			},
			{
				Scenario:       model.UseCaseScenarioSupportType(2),
				Mandatory:      false,
				ServerFeatures: []model.FeatureTypeType{model.FeatureTypeTypeHvac},
			},
			{
				Scenario:       model.UseCaseScenarioSupportType(3),
				Mandatory:      false,
				ServerFeatures: []model.FeatureTypeType{model.FeatureTypeTypeHvac},
			},
		},
		d.handleUseCaseEvent,
		dhwSysFnUseCaseSupportUpdate,
		[]model.UseCaseActorType{model.UseCaseActorTypeDHWCircuit},
		[]model.EntityTypeType{model.EntityTypeTypeDHWCircuit},
		false,
	)
	_ = localEntity.Device().Events().Subscribe(d)
	return d
}

// UseCase returns this use case for Service.AddUseCase.
func (d *DHWSystemFunction) UseCase() eebusapi.UseCaseInterface { return d }

// AddFeatures creates the local HVAC client required by the use case.
func (d *DHWSystemFunction) AddFeatures() error {
	if d.localEntity == nil {
		return errors.New("DHW system function local entity is nil")
	}
	if feature := d.localEntity.GetOrAddFeature(model.FeatureTypeTypeHvac, model.RoleTypeClient); feature == nil {
		return errors.New("could not add DHW HVAC client feature")
	}
	return nil
}

// HandleEvent establishes the HVAC relationship and turns cache updates into
// bridge events consumed by the DHW system-function gRPC stream.
func (d *DHWSystemFunction) HandleEvent(payload spineapi.EventPayload) {
	if payload.Entity == nil || !d.IsCompatibleEntityType(payload.Entity) {
		return
	}
	if payload.EventType == spineapi.EventTypeEntityChange && payload.ChangeType == spineapi.ElementChangeAdd {
		d.connect(payload.Entity)
		return
	}
	if payload.EventType != spineapi.EventTypeDataChange || payload.ChangeType != spineapi.ElementChangeUpdate {
		return
	}

	switch payload.Data.(type) {
	case *model.HvacSystemFunctionDescriptionListDataType,
		*model.HvacOperationModeDescriptionListDataType,
		*model.HvacSystemFunctionOperationModeRelationListDataType,
		*model.HvacOverrunDescriptionListDataType:
		if _, err := d.State(payload.Entity); err == nil && d.bus != nil {
			d.bus.Publish(eebus.Event{SKI: payload.Ski, Type: "dhwsysfn.use_case_support_updated"})
		}
	case *model.HvacSystemFunctionListDataType, *model.HvacOverrunListDataType:
		if _, err := d.State(payload.Entity); err == nil && d.bus != nil {
			d.bus.Publish(eebus.Event{SKI: payload.Ski, Type: "dhwsysfn.updated"})
		}
	}
}

func (d *DHWSystemFunction) handleUseCaseEvent(
	ski string,
	device spineapi.DeviceRemoteInterface,
	entity spineapi.EntityRemoteInterface,
	_ eebusapi.EventType,
) {
	if d.registry != nil {
		d.registry.UpsertObservation(ski, device, entity, "dhw_system_function")
	}
	if d.bus != nil {
		d.bus.Publish(eebus.Event{SKI: ski, Type: "dhwsysfn.use_case_support_updated"})
	}
}

func (d *DHWSystemFunction) connect(entity spineapi.EntityRemoteInterface) {
	remote := hvacServer(entity)
	local := d.localHvacFeature()
	if remote == nil || local == nil {
		return
	}
	if !local.HasSubscriptionToRemote(remote.Address()) {
		if _, err := local.SubscribeToRemote(remote.Address()); err != nil && d.debug {
			log.Printf("[DHWSYSFN] HVAC subscription failed: %s", err.String())
		}
	}
	if !local.HasBindingToRemote(remote.Address()) {
		if _, err := local.BindToRemote(remote.Address()); err != nil && d.debug {
			log.Printf("[DHWSYSFN] HVAC binding failed: %s", err.String())
		}
	}
	d.Refresh(entity)
}

// Refresh requests current HVAC metadata and values.
func (d *DHWSystemFunction) Refresh(entity spineapi.EntityRemoteInterface) {
	for _, function := range []model.FunctionType{
		model.FunctionTypeHvacSystemFunctionDescriptionListData,
		model.FunctionTypeHvacSystemFunctionListData,
		model.FunctionTypeHvacOperationModeDescriptionListData,
		model.FunctionTypeHvacSystemFunctionOperationModeRelationListData,
		model.FunctionTypeHvacOverrunDescriptionListData,
		model.FunctionTypeHvacOverrunListData,
	} {
		d.request(entity, function)
	}
}

func (d *DHWSystemFunction) request(entity spineapi.EntityRemoteInterface, function model.FunctionType) {
	remote := hvacServer(entity)
	local := d.localHvacFeature()
	if remote == nil || local == nil {
		return
	}
	operation := remote.Operations()[function]
	if operation == nil || !operation.Read() {
		return
	}
	if _, err := local.RequestRemoteData(function, nil, nil, remote); err != nil && d.debug {
		log.Printf("[DHWSYSFN] requesting %s failed: %s", function, err.String())
	}
}

// CompatibleEntity returns the negotiated DHWCircuit for a device SKI.
func (d *DHWSystemFunction) CompatibleEntity(ski string) spineapi.EntityRemoteInterface {
	return compatibleEntity(d.RemoteEntitiesScenarios(), ski)
}

// State resolves DHW boost and operation mode from the remote HVAC cache.
func (d *DHWSystemFunction) State(entity spineapi.EntityRemoteInterface) (DHWSystemFunctionState, error) {
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

// WriteBoost activates or deactivates the one-time DHW overrun.
func (d *DHWSystemFunction) WriteBoost(ctx context.Context, entity spineapi.EntityRemoteInterface, active bool) error {
	state, err := d.State(entity)
	if err != nil {
		return err
	}
	if !state.BoostWritable {
		return ErrDHWSysFnNotWritable
	}
	remote := hvacServer(entity)
	local := d.localHvacFeature()
	if remote == nil || local == nil {
		return ErrDHWSysFnDataUnavailable
	}
	resolved, err := resolveDHWSystemFunction(remote)
	if err != nil {
		return err
	}
	data, ok := remote.DataCopy(model.FunctionTypeHvacOverrunListData).(*model.HvacOverrunListDataType)
	if !ok || data == nil {
		return ErrDHWSysFnDataUnavailable
	}
	entries := make([]model.HvacOverrunDataType, len(data.HvacOverrunData))
	copy(entries, data.HvacOverrunData)
	status := model.HvacOverrunStatusTypeInactive
	if active {
		status = model.HvacOverrunStatusTypeActive
	}
	found := false
	for index := range entries {
		if entries[index].OverrunId != nil && *entries[index].OverrunId == resolved.overrunID {
			entries[index].OverrunStatus = &status
			found = true
			break
		}
	}
	if !found {
		return ErrDHWSysFnDataUnavailable
	}
	return d.write(ctx, entity, remote, local, model.CmdType{
		HvacOverrunListData: &model.HvacOverrunListDataType{HvacOverrunData: entries},
	}, model.FunctionTypeHvacOverrunListData, "DHW boost")
}

// WriteOperationMode switches the DHW operation mode by device-advertised type.
func (d *DHWSystemFunction) WriteOperationMode(ctx context.Context, entity spineapi.EntityRemoteInterface, modeType string) error {
	state, err := d.State(entity)
	if err != nil {
		return err
	}
	if !state.ModeWritable {
		return ErrDHWSysFnNotWritable
	}
	remote := hvacServer(entity)
	local := d.localHvacFeature()
	if remote == nil || local == nil {
		return ErrDHWSysFnDataUnavailable
	}
	resolved, err := resolveDHWSystemFunction(remote)
	if err != nil {
		return err
	}
	id, ok := resolved.modeIDForType[modeType]
	if !ok {
		return fmt.Errorf("%w: %s", ErrDHWSysFnInvalidMode, modeType)
	}
	data, ok := remote.DataCopy(model.FunctionTypeHvacSystemFunctionListData).(*model.HvacSystemFunctionListDataType)
	if !ok || data == nil {
		return ErrDHWSysFnDataUnavailable
	}
	entries := make([]model.HvacSystemFunctionDataType, len(data.HvacSystemFunctionData))
	copy(entries, data.HvacSystemFunctionData)
	found := false
	for index := range entries {
		if entries[index].SystemFunctionId != nil && *entries[index].SystemFunctionId == resolved.systemID {
			entries[index].CurrentOperationModeId = &id
			found = true
			break
		}
	}
	if !found {
		return ErrDHWSysFnDataUnavailable
	}
	return d.write(ctx, entity, remote, local, model.CmdType{
		HvacSystemFunctionListData: &model.HvacSystemFunctionListDataType{HvacSystemFunctionData: entries},
	}, model.FunctionTypeHvacSystemFunctionListData, "DHW operation mode")
}

func (d *DHWSystemFunction) write(
	ctx context.Context,
	entity spineapi.EntityRemoteInterface,
	remote spineapi.FeatureRemoteInterface,
	local spineapi.FeatureLocalInterface,
	cmd model.CmdType,
	refresh model.FunctionType,
	label string,
) error {
	counter, err := entity.Device().Sender().Write(local.Address(), remote.Address(), cmd)
	if err != nil {
		return fmt.Errorf("sending %s: %w", label, err)
	}
	if counter == nil {
		return fmt.Errorf("sending %s returned no message counter", label)
	}
	result := make(chan model.ResultDataType, 1)
	if err := local.AddResponseCallback(*counter, func(message spineapi.ResponseMessage) {
		if data, ok := message.Data.(*model.ResultDataType); ok && data != nil {
			result <- *data
		}
	}); err != nil {
		return fmt.Errorf("waiting for %s result: %w", label, err)
	}

	timer := time.NewTimer(dhwWriteTimeout)
	defer timer.Stop()
	select {
	case response := <-result:
		if response.ErrorNumber != nil && *response.ErrorNumber != 0 {
			return fmt.Errorf("%w: %s error=%d", ErrDHWSysFnRejected, label, *response.ErrorNumber)
		}
		d.request(entity, refresh)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("timed out waiting for %s result", label)
	}
}

func (d *DHWSystemFunction) localHvacFeature() spineapi.FeatureLocalInterface {
	if d.localEntity == nil {
		return nil
	}
	return d.localEntity.FeatureOfTypeAndRole(model.FeatureTypeTypeHvac, model.RoleTypeClient)
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
