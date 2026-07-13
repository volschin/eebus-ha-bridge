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

const roomHeatingSysFnUseCaseSupportUpdate eebusapi.EventType = "bridge-room-heating-system-function-support-update"

var (
	ErrRoomHeatingSysFnDataUnavailable = errors.New("room heating system function data unavailable")
	ErrRoomHeatingSysFnNotWritable     = errors.New("room heating system function is not writable")
	ErrRoomHeatingSysFnInvalidMode     = errors.New("room heating operation mode is not advertised")
	ErrRoomHeatingSysFnRejected        = errors.New("room heating system function write rejected by device")
)

// RoomHeatingSystemFunctionState is the current room-heating operation mode
// resolved from the remote HVACRoom HVAC server metadata.
type RoomHeatingSystemFunctionState struct {
	OperationMode  string
	AvailableModes []string
	ModeWritable   bool
}

// RoomHeatingSystemFunction implements the Configuration of Room Heating
// System Function client role.
type RoomHeatingSystemFunction struct {
	*usecase.UseCaseBase
	localEntity spineapi.EntityLocalInterface
	bus         *eebus.EventBus
	registry    *eebus.DeviceRegistry
	debug       bool
}

// NewRoomHeatingSystemFunction creates the Configuration Appliance client use case.
func NewRoomHeatingSystemFunction(
	localEntity spineapi.EntityLocalInterface,
	bus *eebus.EventBus,
	registry *eebus.DeviceRegistry,
	debug bool,
) *RoomHeatingSystemFunction {
	r := &RoomHeatingSystemFunction{
		localEntity: localEntity,
		bus:         bus,
		registry:    registry,
		debug:       debug,
	}
	r.UseCaseBase = usecase.NewUseCaseBase(
		localEntity,
		model.UseCaseActorTypeConfigurationAppliance,
		model.UseCaseNameTypeConfigurationOfRoomHeatingSystemFunction,
		"1.0.0",
		"release",
		[]eebusapi.UseCaseScenario{{
			Scenario:       model.UseCaseScenarioSupportType(1),
			Mandatory:      true,
			ServerFeatures: []model.FeatureTypeType{model.FeatureTypeTypeHvac},
		}},
		r.handleUseCaseEvent,
		roomHeatingSysFnUseCaseSupportUpdate,
		[]model.UseCaseActorType{model.UseCaseActorTypeHVACRoom},
		[]model.EntityTypeType{model.EntityTypeTypeHvacRoom},
		false,
	)
	_ = localEntity.Device().Events().Subscribe(r)
	return r
}

// UseCase returns this use case for Service.AddUseCase.
func (r *RoomHeatingSystemFunction) UseCase() eebusapi.UseCaseInterface { return r }

// AddFeatures creates the local HVAC client required by the use case.
func (r *RoomHeatingSystemFunction) AddFeatures() error {
	if r.localEntity == nil {
		return errors.New("room heating system function local entity is nil")
	}
	if feature := r.localEntity.GetOrAddFeature(model.FeatureTypeTypeHvac, model.RoleTypeClient); feature == nil {
		return errors.New("could not add room heating HVAC client feature")
	}
	return nil
}

// HandleEvent establishes the HVAC relationship and turns cache updates into
// bridge events consumed by the room-heating gRPC stream.
func (r *RoomHeatingSystemFunction) HandleEvent(payload spineapi.EventPayload) {
	if payload.Entity == nil || !r.IsCompatibleEntityType(payload.Entity) {
		return
	}
	if payload.EventType == spineapi.EventTypeEntityChange && payload.ChangeType == spineapi.ElementChangeAdd {
		r.connect(payload.Entity)
		return
	}
	if payload.EventType != spineapi.EventTypeDataChange || payload.ChangeType != spineapi.ElementChangeUpdate {
		return
	}

	switch payload.Data.(type) {
	case *model.HvacSystemFunctionDescriptionListDataType,
		*model.HvacOperationModeDescriptionListDataType,
		*model.HvacSystemFunctionOperationModeRelationListDataType:
		if _, err := r.State(payload.Entity); err == nil && r.bus != nil {
			r.bus.Publish(eebus.Event{SKI: payload.Ski, Type: "roomheatingsysfn.use_case_support_updated"})
		}
	case *model.HvacSystemFunctionListDataType:
		if _, err := r.State(payload.Entity); err == nil && r.bus != nil {
			r.bus.Publish(eebus.Event{SKI: payload.Ski, Type: "roomheatingsysfn.updated"})
		}
	}
}

func (r *RoomHeatingSystemFunction) handleUseCaseEvent(
	ski string,
	device spineapi.DeviceRemoteInterface,
	entity spineapi.EntityRemoteInterface,
	_ eebusapi.EventType,
) {
	if r.registry != nil {
		r.registry.UpsertObservation(ski, device, entity, "room_heating_system_function")
	}
	if r.bus != nil {
		r.bus.Publish(eebus.Event{SKI: ski, Type: "roomheatingsysfn.use_case_support_updated"})
	}
}

func (r *RoomHeatingSystemFunction) connect(entity spineapi.EntityRemoteInterface) {
	remote := hvacServer(entity)
	local := r.localHvacFeature()
	if remote == nil || local == nil {
		return
	}
	if !local.HasSubscriptionToRemote(remote.Address()) {
		if _, err := local.SubscribeToRemote(remote.Address()); err != nil && r.debug {
			log.Printf("[ROOMHEATINGSYSFN] HVAC subscription failed: %s", err.String())
		}
	}
	if !local.HasBindingToRemote(remote.Address()) {
		if _, err := local.BindToRemote(remote.Address()); err != nil && r.debug {
			log.Printf("[ROOMHEATINGSYSFN] HVAC binding failed: %s", err.String())
		}
	}
	r.Refresh(entity)
}

// Refresh requests current HVAC metadata and values.
func (r *RoomHeatingSystemFunction) Refresh(entity spineapi.EntityRemoteInterface) {
	for _, function := range []model.FunctionType{
		model.FunctionTypeHvacSystemFunctionDescriptionListData,
		model.FunctionTypeHvacSystemFunctionListData,
		model.FunctionTypeHvacOperationModeDescriptionListData,
		model.FunctionTypeHvacSystemFunctionOperationModeRelationListData,
	} {
		r.request(entity, function)
	}
}

func (r *RoomHeatingSystemFunction) request(entity spineapi.EntityRemoteInterface, function model.FunctionType) {
	remote := hvacServer(entity)
	local := r.localHvacFeature()
	if remote == nil || local == nil {
		return
	}
	operation := remote.Operations()[function]
	if operation == nil || !operation.Read() {
		return
	}
	if _, err := local.RequestRemoteData(function, nil, nil, remote); err != nil && r.debug {
		log.Printf("[ROOMHEATINGSYSFN] requesting %s failed: %s", function, err.String())
	}
}

// CompatibleEntity returns the negotiated HVACRoom for a device SKI.
func (r *RoomHeatingSystemFunction) CompatibleEntity(ski string) spineapi.EntityRemoteInterface {
	want := eebus.NormalizeSKI(ski)
	for _, remote := range r.RemoteEntitiesScenarios() {
		entity := remote.Entity
		if entity == nil || entity.Device() == nil {
			continue
		}
		if want == "" || eebus.NormalizeSKI(entity.Device().Ski()) == want {
			return entity
		}
	}
	return nil
}

// State resolves the room-heating operation mode from the remote HVAC cache.
func (r *RoomHeatingSystemFunction) State(entity spineapi.EntityRemoteInterface) (RoomHeatingSystemFunctionState, error) {
	remote := hvacServer(entity)
	if remote == nil {
		return RoomHeatingSystemFunctionState{}, ErrRoomHeatingSysFnDataUnavailable
	}
	resolved, err := resolveRoomHeatingSystemFunction(remote)
	if err != nil {
		return RoomHeatingSystemFunctionState{}, err
	}
	systemOp := remote.Operations()[model.FunctionTypeHvacSystemFunctionListData]
	return RoomHeatingSystemFunctionState{
		OperationMode:  string(resolved.currentModeType),
		AvailableModes: resolved.availableModeTypes,
		ModeWritable: systemOp != nil && systemOp.Write() &&
			boolPtrNotFalse(resolved.system.IsOperationModeIdChangeable),
	}, nil
}

// WriteOperationMode switches the room-heating operation mode by device-advertised type.
func (r *RoomHeatingSystemFunction) WriteOperationMode(
	ctx context.Context,
	entity spineapi.EntityRemoteInterface,
	modeType string,
) error {
	state, err := r.State(entity)
	if err != nil {
		return err
	}
	if !state.ModeWritable {
		return ErrRoomHeatingSysFnNotWritable
	}
	remote := hvacServer(entity)
	if remote == nil {
		return ErrRoomHeatingSysFnDataUnavailable
	}
	resolved, err := resolveRoomHeatingSystemFunction(remote)
	if err != nil {
		return err
	}
	id, ok := resolved.modeIDForType[modeType]
	if !ok {
		return fmt.Errorf("%w: %s", ErrRoomHeatingSysFnInvalidMode, modeType)
	}
	local := r.localHvacFeature()
	if local == nil {
		return ErrRoomHeatingSysFnDataUnavailable
	}
	data, ok := remote.DataCopy(model.FunctionTypeHvacSystemFunctionListData).(*model.HvacSystemFunctionListDataType)
	if !ok || data == nil {
		return ErrRoomHeatingSysFnDataUnavailable
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
		return ErrRoomHeatingSysFnDataUnavailable
	}
	return r.write(ctx, entity, remote, local, model.CmdType{
		HvacSystemFunctionListData: &model.HvacSystemFunctionListDataType{HvacSystemFunctionData: entries},
	}, model.FunctionTypeHvacSystemFunctionListData, "room heating operation mode")
}

func (r *RoomHeatingSystemFunction) write(
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
			return fmt.Errorf("%w: %s error=%d", ErrRoomHeatingSysFnRejected, label, *response.ErrorNumber)
		}
		r.request(entity, refresh)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("timed out waiting for %s result", label)
	}
}

func (r *RoomHeatingSystemFunction) localHvacFeature() spineapi.FeatureLocalInterface {
	if r.localEntity == nil {
		return nil
	}
	return r.localEntity.FeatureOfTypeAndRole(model.FeatureTypeTypeHvac, model.RoleTypeClient)
}

type resolvedRoomHeatingSysFn struct {
	system             model.HvacSystemFunctionDataType
	systemID           model.HvacSystemFunctionIdType
	currentModeType    model.HvacOperationModeTypeType
	availableModeTypes []string
	modeIDForType      map[string]model.HvacOperationModeIdType
}

func resolveRoomHeatingSystemFunction(feature spineapi.FeatureRemoteInterface) (resolvedRoomHeatingSysFn, error) {
	systemID, ok := roomHeatingSystemFunctionID(feature)
	if !ok {
		return resolvedRoomHeatingSysFn{}, ErrRoomHeatingSysFnDataUnavailable
	}
	system, ok := hvacSystemFunction(feature, systemID)
	if !ok || system.CurrentOperationModeId == nil {
		return resolvedRoomHeatingSysFn{}, ErrRoomHeatingSysFnDataUnavailable
	}
	modes, idForType, ok := operationModesForSystem(feature, systemID)
	if !ok {
		return resolvedRoomHeatingSysFn{}, ErrRoomHeatingSysFnDataUnavailable
	}
	currentMode, ok := operationModeType(feature, *system.CurrentOperationModeId)
	if !ok {
		return resolvedRoomHeatingSysFn{}, ErrRoomHeatingSysFnDataUnavailable
	}
	return resolvedRoomHeatingSysFn{
		system:             system,
		systemID:           systemID,
		currentModeType:    currentMode,
		availableModeTypes: modes,
		modeIDForType:      idForType,
	}, nil
}

func roomHeatingSystemFunctionID(feature spineapi.FeatureRemoteInterface) (model.HvacSystemFunctionIdType, bool) {
	data, ok := feature.DataCopy(model.FunctionTypeHvacSystemFunctionDescriptionListData).(*model.HvacSystemFunctionDescriptionListDataType)
	if !ok || data == nil {
		return 0, false
	}
	var found []model.HvacSystemFunctionIdType
	for _, description := range data.HvacSystemFunctionDescriptionData {
		if description.SystemFunctionId != nil && description.SystemFunctionType != nil &&
			*description.SystemFunctionType == model.HvacSystemFunctionTypeTypeHeating {
			found = append(found, *description.SystemFunctionId)
		}
	}
	if len(found) != 1 {
		return 0, false
	}
	return found[0], true
}
