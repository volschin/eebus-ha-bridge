package usecases

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	usecase "github.com/enbility/eebus-go/usecases/usecase"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

const roomHeatingUseCaseSupportUpdate eebusapi.EventType = "bridge-room-heating-temperature-support-update"

var (
	ErrRoomHeatingDataUnavailable = errors.New("room heating setpoint data unavailable")
	ErrRoomHeatingNotWritable     = errors.New("room heating setpoint is not writable")
	ErrRoomHeatingOutOfRange      = errors.New("room heating setpoint is outside the advertised range")
	ErrRoomHeatingInvalidStep     = errors.New("room heating setpoint does not match the advertised step")
	ErrRoomHeatingRejected        = errors.New("room heating setpoint write rejected by device")
)

// RoomHeatingSetpoint is the current room-heating target and the constraints
// advertised by the remote HVACRoom Setpoint server.
type RoomHeatingSetpoint struct {
	Value    float64
	Minimum  float64
	Maximum  float64
	Step     float64
	Writable bool
}

// RoomHeatingTemperature implements the Configuration of Room Heating
// Temperature client role.
type RoomHeatingTemperature struct {
	*usecase.UseCaseBase
	localEntity spineapi.EntityLocalInterface
	bus         *eebus.EventBus
	registry    *eebus.DeviceRegistry
	debug       bool
}

// NewRoomHeatingTemperature creates the Configuration Appliance client use case.
func NewRoomHeatingTemperature(
	localEntity spineapi.EntityLocalInterface,
	bus *eebus.EventBus,
	registry *eebus.DeviceRegistry,
	debug bool,
) *RoomHeatingTemperature {
	r := &RoomHeatingTemperature{
		localEntity: localEntity,
		bus:         bus,
		registry:    registry,
		debug:       debug,
	}
	r.UseCaseBase = usecase.NewUseCaseBase(
		localEntity,
		model.UseCaseActorTypeConfigurationAppliance,
		model.UseCaseNameTypeConfigurationOfRoomHeatingTemperature,
		"1.0.0",
		"release",
		[]eebusapi.UseCaseScenario{{
			Scenario:       model.UseCaseScenarioSupportType(1),
			Mandatory:      true,
			ServerFeatures: []model.FeatureTypeType{model.FeatureTypeTypeSetpoint},
		}},
		r.handleUseCaseEvent,
		roomHeatingUseCaseSupportUpdate,
		[]model.UseCaseActorType{model.UseCaseActorTypeHVACRoom},
		[]model.EntityTypeType{model.EntityTypeTypeHvacRoom},
		false,
	)
	_ = localEntity.Device().Events().Subscribe(r)
	return r
}

// UseCase returns this use case for Service.AddUseCase.
func (r *RoomHeatingTemperature) UseCase() eebusapi.UseCaseInterface { return r }

// AddFeatures creates the local Setpoint client required by scenario 1.
func (r *RoomHeatingTemperature) AddFeatures() error {
	if r.localEntity == nil {
		return errors.New("room heating local entity is nil")
	}
	if feature := r.localEntity.GetOrAddFeature(model.FeatureTypeTypeSetpoint, model.RoleTypeClient); feature == nil {
		return errors.New("could not add room heating Setpoint client feature")
	}
	return nil
}

// HandleEvent establishes the Setpoint relationship and turns cache updates
// into bridge events consumed by the room-heating gRPC stream.
func (r *RoomHeatingTemperature) HandleEvent(payload spineapi.EventPayload) {
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
	case *model.SetpointDescriptionListDataType, *model.SetpointConstraintsListDataType:
		r.request(payload.Entity, model.FunctionTypeSetpointListData)
	case *model.SetpointListDataType:
		if _, err := r.State(payload.Entity); err == nil && r.bus != nil {
			r.bus.Publish(eebus.Event{SKI: payload.Ski, Type: eebus.EventTypeRoomHeatingSetpointUpdated})
		}
	}
}

func (r *RoomHeatingTemperature) handleUseCaseEvent(
	ski string,
	device spineapi.DeviceRemoteInterface,
	entity spineapi.EntityRemoteInterface,
	_ eebusapi.EventType,
) {
	if r.registry != nil {
		recordCapabilitySupport(
			r.registry, ski, device, entity, r.CompatibleEntity(observationSKI(ski, device)),
			"room_heating_temperature", eebus.CapabilityRoomHeating,
		)
	}
	if r.bus != nil {
		r.bus.Publish(eebus.Event{SKI: ski, Type: eebus.EventTypeRoomHeatingUseCaseSupportUpdated})
	}
}

func (r *RoomHeatingTemperature) connect(entity spineapi.EntityRemoteInterface) {
	remote := entity.FeatureOfTypeAndRole(model.FeatureTypeTypeSetpoint, model.RoleTypeServer)
	local := r.localSetpointFeature()
	if remote == nil || local == nil {
		return
	}
	if !local.HasSubscriptionToRemote(remote.Address()) {
		if _, err := local.SubscribeToRemote(remote.Address()); err != nil && r.debug {
			log.Printf("[ROOMHEATING] Setpoint subscription failed: %s", err.String())
		}
	}
	if !local.HasBindingToRemote(remote.Address()) {
		if _, err := local.BindToRemote(remote.Address()); err != nil && r.debug {
			log.Printf("[ROOMHEATING] Setpoint binding failed: %s", err.String())
		}
	}
	r.Refresh(entity)
}

// Refresh requests current value and metadata. Responses update the remote
// feature cache asynchronously and are surfaced through HandleEvent.
func (r *RoomHeatingTemperature) Refresh(entity spineapi.EntityRemoteInterface) {
	for _, function := range []model.FunctionType{
		model.FunctionTypeSetpointDescriptionListData,
		model.FunctionTypeSetpointConstraintsListData,
		model.FunctionTypeSetpointListData,
	} {
		r.request(entity, function)
	}
}

func (r *RoomHeatingTemperature) request(entity spineapi.EntityRemoteInterface, function model.FunctionType) {
	remote := entity.FeatureOfTypeAndRole(model.FeatureTypeTypeSetpoint, model.RoleTypeServer)
	local := r.localSetpointFeature()
	if remote == nil || local == nil {
		return
	}
	operation := remote.Operations()[function]
	if operation == nil || !operation.Read() {
		return
	}
	if _, err := local.RequestRemoteData(function, nil, nil, remote); err != nil && r.debug {
		log.Printf("[ROOMHEATING] requesting %s failed: %s", function, err.String())
	}
}

// CompatibleEntity returns the negotiated HVACRoom for a device SKI.
func (r *RoomHeatingTemperature) CompatibleEntity(ski string) eebus.EntityResolution {
	return compatibleEntity(r.RemoteEntitiesScenarios(), ski)
}

// State reads the room-heating target and its complete constraints from the
// remote cache. Missing or partial metadata fails closed.
func (r *RoomHeatingTemperature) State(entity spineapi.EntityRemoteInterface) (RoomHeatingSetpoint, error) {
	remote := setpointServer(entity)
	if remote == nil {
		return RoomHeatingSetpoint{}, ErrRoomHeatingDataUnavailable
	}
	id, ok := roomHeatingSetpointID(remote)
	if !ok {
		return RoomHeatingSetpoint{}, ErrRoomHeatingDataUnavailable
	}
	value, ok := setpointValue(remote, id)
	if !ok {
		return RoomHeatingSetpoint{}, ErrRoomHeatingDataUnavailable
	}
	minimum, maximum, step, ok := setpointRange(remote, id)
	if !ok {
		return RoomHeatingSetpoint{}, ErrRoomHeatingDataUnavailable
	}
	operation := remote.Operations()[model.FunctionTypeSetpointListData]
	return RoomHeatingSetpoint{
		Value:    value,
		Minimum:  minimum,
		Maximum:  maximum,
		Step:     step,
		Writable: operation != nil && operation.Write(),
	}, nil
}

// Write validates against device-provided constraints, sends the complete
// SetpointListData required by the VR940, and waits for the device result.
func (r *RoomHeatingTemperature) Write(ctx context.Context, entity spineapi.EntityRemoteInterface, value float64) error {
	state, err := r.State(entity)
	if err != nil {
		return err
	}
	if !state.Writable {
		return ErrRoomHeatingNotWritable
	}
	if !isFinite(value) || value < state.Minimum || value > state.Maximum {
		return fmt.Errorf("%w: %.3f not in [%.3f, %.3f]", ErrRoomHeatingOutOfRange, value, state.Minimum, state.Maximum)
	}
	steps := math.Round((value - state.Minimum) / state.Step)
	if math.Abs(state.Minimum+steps*state.Step-value) > 1e-6 {
		return fmt.Errorf("%w: %.3f with step %.3f", ErrRoomHeatingInvalidStep, value, state.Step)
	}

	remote := setpointServer(entity)
	local := r.localSetpointFeature()
	if remote == nil || local == nil {
		return ErrRoomHeatingDataUnavailable
	}
	data, ok := remote.DataCopy(model.FunctionTypeSetpointListData).(*model.SetpointListDataType)
	if !ok || data == nil {
		return ErrRoomHeatingDataUnavailable
	}
	id, ok := roomHeatingSetpointID(remote)
	if !ok {
		return ErrRoomHeatingDataUnavailable
	}
	entries := make([]model.SetpointDataType, len(data.SetpointData))
	copy(entries, data.SetpointData)
	found := false
	for index := range entries {
		if entries[index].SetpointId != nil && *entries[index].SetpointId == id {
			entries[index].Value = model.NewScaledNumberType(value)
			found = true
			break
		}
	}
	if !found {
		return ErrRoomHeatingDataUnavailable
	}

	counter, err := entity.Device().Sender().Write(
		local.Address(),
		remote.Address(),
		model.CmdType{SetpointListData: &model.SetpointListDataType{SetpointData: entries}},
	)
	if err != nil {
		return fmt.Errorf("sending room heating setpoint: %w", err)
	}
	if counter == nil {
		return errors.New("sending room heating setpoint returned no message counter")
	}
	result := make(chan model.ResultDataType, 1)
	if err := local.AddResponseCallback(*counter, func(message spineapi.ResponseMessage) {
		if data, ok := message.Data.(*model.ResultDataType); ok && data != nil {
			result <- *data
		}
	}); err != nil {
		return fmt.Errorf("waiting for room heating setpoint result: %w", err)
	}

	timer := time.NewTimer(dhwWriteTimeout)
	defer timer.Stop()
	select {
	case response := <-result:
		if response.ErrorNumber != nil && *response.ErrorNumber != 0 {
			return fmt.Errorf("%w: error=%d", ErrRoomHeatingRejected, *response.ErrorNumber)
		}
		r.request(entity, model.FunctionTypeSetpointListData)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("timed out waiting for room heating setpoint result")
	}
}

func (r *RoomHeatingTemperature) localSetpointFeature() spineapi.FeatureLocalInterface {
	if r.localEntity == nil {
		return nil
	}
	return r.localEntity.FeatureOfTypeAndRole(model.FeatureTypeTypeSetpoint, model.RoleTypeClient)
}

func roomHeatingSetpointID(feature spineapi.FeatureRemoteInterface) (model.SetpointIdType, bool) {
	data, ok := feature.DataCopy(model.FunctionTypeSetpointDescriptionListData).(*model.SetpointDescriptionListDataType)
	if !ok || data == nil {
		return 0, false
	}
	for _, description := range data.SetpointDescriptionData {
		if description.SetpointId != nil && description.ScopeType != nil &&
			*description.ScopeType == model.ScopeTypeTypeRoomAirTemperature {
			return *description.SetpointId, true
		}
	}
	return 0, false
}
