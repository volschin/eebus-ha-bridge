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

const (
	dhwUseCaseSupportUpdate eebusapi.EventType = "bridge-dhw-temperature-support-update"
	dhwWriteTimeout                            = 10 * time.Second
)

var (
	ErrDHWDataUnavailable = errors.New("DHW setpoint data unavailable")
	ErrDHWNotWritable     = errors.New("DHW setpoint is not writable")
	ErrDHWOutOfRange      = errors.New("DHW setpoint is outside the advertised range")
	ErrDHWInvalidStep     = errors.New("DHW setpoint does not match the advertised step")
)

// DHWSetpoint is the current domestic-hot-water target and the constraints
// advertised by the remote DHWCircuit Setpoint server.
type DHWSetpoint struct {
	Value    float64
	Minimum  float64
	Maximum  float64
	Step     float64
	Writable bool
}

// DHWTemperature implements the Configuration of DHW Temperature client role.
// It intentionally covers only scenario 1 (read/write DHW target temperature),
// the path validated against the Vaillant VR940 by the HVAC probe in PR #91.
type DHWTemperature struct {
	*usecase.UseCaseBase
	localEntity spineapi.EntityLocalInterface
	bus         *eebus.EventBus
	registry    *eebus.DeviceRegistry
	debug       bool
}

// NewDHWTemperature creates the Configuration Appliance client use case.
func NewDHWTemperature(
	localEntity spineapi.EntityLocalInterface,
	bus *eebus.EventBus,
	registry *eebus.DeviceRegistry,
	debug bool,
) *DHWTemperature {
	d := &DHWTemperature{
		localEntity: localEntity,
		bus:         bus,
		registry:    registry,
		debug:       debug,
	}
	d.UseCaseBase = usecase.NewUseCaseBase(
		localEntity,
		model.UseCaseActorTypeConfigurationAppliance,
		model.UseCaseNameTypeConfigurationOfDhwTemperature,
		"1.0.0",
		"release",
		[]eebusapi.UseCaseScenario{{
			Scenario:       model.UseCaseScenarioSupportType(1),
			Mandatory:      true,
			ServerFeatures: []model.FeatureTypeType{model.FeatureTypeTypeSetpoint},
		}},
		d.handleUseCaseEvent,
		dhwUseCaseSupportUpdate,
		[]model.UseCaseActorType{model.UseCaseActorTypeDHWCircuit},
		[]model.EntityTypeType{model.EntityTypeTypeDHWCircuit},
		false,
	)
	_ = localEntity.Device().Events().Subscribe(d)
	return d
}

// UseCase returns this use case for Service.AddUseCase.
func (d *DHWTemperature) UseCase() eebusapi.UseCaseInterface { return d }

// AddFeatures creates the local Setpoint client required by scenario 1.
func (d *DHWTemperature) AddFeatures() error {
	if d.localEntity == nil {
		return errors.New("DHW local entity is nil")
	}
	if feature := d.localEntity.GetOrAddFeature(model.FeatureTypeTypeSetpoint, model.RoleTypeClient); feature == nil {
		return errors.New("could not add DHW Setpoint client feature")
	}
	return nil
}

// HandleEvent establishes the Setpoint relationship and turns cache updates
// into bridge events consumed by the DHW gRPC stream.
func (d *DHWTemperature) HandleEvent(payload spineapi.EventPayload) {
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
	case *model.SetpointDescriptionListDataType, *model.SetpointConstraintsListDataType:
		d.request(payload.Entity, model.FunctionTypeSetpointListData)
	case *model.SetpointListDataType:
		if _, err := d.State(payload.Entity); err == nil && d.bus != nil {
			d.bus.Publish(eebus.Event{SKI: payload.Ski, Type: eebus.EventTypeDHWSetpointUpdated})
		}
	}
}

func (d *DHWTemperature) handleUseCaseEvent(
	ski string,
	device spineapi.DeviceRemoteInterface,
	entity spineapi.EntityRemoteInterface,
	_ eebusapi.EventType,
) {
	if d.registry != nil {
		d.registry.UpsertObservation(ski, device, entity, "dhw_temperature")
	}
	if d.bus != nil {
		d.bus.Publish(eebus.Event{SKI: ski, Type: eebus.EventTypeDHWUseCaseSupportUpdated})
	}
}

func (d *DHWTemperature) connect(entity spineapi.EntityRemoteInterface) {
	remote := entity.FeatureOfTypeAndRole(model.FeatureTypeTypeSetpoint, model.RoleTypeServer)
	local := d.localSetpointFeature()
	if remote == nil || local == nil {
		return
	}
	if !local.HasSubscriptionToRemote(remote.Address()) {
		if _, err := local.SubscribeToRemote(remote.Address()); err != nil && d.debug {
			log.Printf("[DHW] Setpoint subscription failed: %s", err.String())
		}
	}
	if !local.HasBindingToRemote(remote.Address()) {
		if _, err := local.BindToRemote(remote.Address()); err != nil && d.debug {
			log.Printf("[DHW] Setpoint binding failed: %s", err.String())
		}
	}
	d.Refresh(entity)
}

// Refresh requests current value and metadata. Responses update the remote
// feature cache asynchronously and are surfaced through HandleEvent.
func (d *DHWTemperature) Refresh(entity spineapi.EntityRemoteInterface) {
	for _, function := range []model.FunctionType{
		model.FunctionTypeSetpointDescriptionListData,
		model.FunctionTypeSetpointConstraintsListData,
		model.FunctionTypeSetpointListData,
	} {
		d.request(entity, function)
	}
}

func (d *DHWTemperature) request(entity spineapi.EntityRemoteInterface, function model.FunctionType) {
	remote := entity.FeatureOfTypeAndRole(model.FeatureTypeTypeSetpoint, model.RoleTypeServer)
	local := d.localSetpointFeature()
	if remote == nil || local == nil {
		return
	}
	operation := remote.Operations()[function]
	if operation == nil || !operation.Read() {
		return
	}
	if _, err := local.RequestRemoteData(function, nil, nil, remote); err != nil && d.debug {
		log.Printf("[DHW] requesting %s failed: %s", function, err.String())
	}
}

// CompatibleEntity returns the negotiated DHWCircuit for a device SKI.
func (d *DHWTemperature) CompatibleEntity(ski string) spineapi.EntityRemoteInterface {
	return compatibleEntity(d.RemoteEntitiesScenarios(), ski)
}

// State reads the DHW target and its complete constraints from the remote cache.
// Missing or partial metadata fails closed so Home Assistant never invents a
// writable range for physical temperature control.
func (d *DHWTemperature) State(entity spineapi.EntityRemoteInterface) (DHWSetpoint, error) {
	remote := setpointServer(entity)
	if remote == nil {
		return DHWSetpoint{}, ErrDHWDataUnavailable
	}
	id, ok := dhwSetpointID(remote)
	if !ok {
		return DHWSetpoint{}, ErrDHWDataUnavailable
	}
	value, ok := setpointValue(remote, id)
	if !ok {
		return DHWSetpoint{}, ErrDHWDataUnavailable
	}
	minimum, maximum, step, ok := setpointRange(remote, id)
	if !ok {
		return DHWSetpoint{}, ErrDHWDataUnavailable
	}
	operation := remote.Operations()[model.FunctionTypeSetpointListData]
	return DHWSetpoint{
		Value:    value,
		Minimum:  minimum,
		Maximum:  maximum,
		Step:     step,
		Writable: operation != nil && operation.Write(),
	}, nil
}

// Write validates against device-provided constraints, sends the complete
// SetpointListData required by the VR940, and waits for the device result.
func (d *DHWTemperature) Write(ctx context.Context, entity spineapi.EntityRemoteInterface, value float64) error {
	state, err := d.State(entity)
	if err != nil {
		return err
	}
	if !state.Writable {
		return ErrDHWNotWritable
	}
	if !isFinite(value) || value < state.Minimum || value > state.Maximum {
		return fmt.Errorf("%w: %.3f not in [%.3f, %.3f]", ErrDHWOutOfRange, value, state.Minimum, state.Maximum)
	}
	steps := math.Round((value - state.Minimum) / state.Step)
	if math.Abs(state.Minimum+steps*state.Step-value) > 1e-6 {
		return fmt.Errorf("%w: %.3f with step %.3f", ErrDHWInvalidStep, value, state.Step)
	}

	remote := setpointServer(entity)
	local := d.localSetpointFeature()
	if remote == nil || local == nil {
		return ErrDHWDataUnavailable
	}
	data, ok := remote.DataCopy(model.FunctionTypeSetpointListData).(*model.SetpointListDataType)
	if !ok || data == nil {
		return ErrDHWDataUnavailable
	}
	id, ok := dhwSetpointID(remote)
	if !ok {
		return ErrDHWDataUnavailable
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
		return ErrDHWDataUnavailable
	}

	counter, err := entity.Device().Sender().Write(
		local.Address(),
		remote.Address(),
		model.CmdType{SetpointListData: &model.SetpointListDataType{SetpointData: entries}},
	)
	if err != nil {
		return fmt.Errorf("sending DHW setpoint: %w", err)
	}
	if counter == nil {
		return errors.New("sending DHW setpoint returned no message counter")
	}
	result := make(chan model.ResultDataType, 1)
	if err := local.AddResponseCallback(*counter, func(message spineapi.ResponseMessage) {
		if data, ok := message.Data.(*model.ResultDataType); ok && data != nil {
			result <- *data
		}
	}); err != nil {
		return fmt.Errorf("waiting for DHW setpoint result: %w", err)
	}

	timer := time.NewTimer(dhwWriteTimeout)
	defer timer.Stop()
	select {
	case response := <-result:
		if response.ErrorNumber != nil && *response.ErrorNumber != 0 {
			return fmt.Errorf("DHW setpoint rejected by device: error=%d", *response.ErrorNumber)
		}
		d.request(entity, model.FunctionTypeSetpointListData)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("timed out waiting for DHW setpoint result")
	}
}

func (d *DHWTemperature) localSetpointFeature() spineapi.FeatureLocalInterface {
	if d.localEntity == nil {
		return nil
	}
	return d.localEntity.FeatureOfTypeAndRole(model.FeatureTypeTypeSetpoint, model.RoleTypeClient)
}

func setpointServer(entity spineapi.EntityRemoteInterface) spineapi.FeatureRemoteInterface {
	if entity == nil {
		return nil
	}
	return entity.FeatureOfTypeAndRole(model.FeatureTypeTypeSetpoint, model.RoleTypeServer)
}

func dhwSetpointID(feature spineapi.FeatureRemoteInterface) (model.SetpointIdType, bool) {
	data, ok := feature.DataCopy(model.FunctionTypeSetpointDescriptionListData).(*model.SetpointDescriptionListDataType)
	if !ok || data == nil {
		return 0, false
	}
	for _, description := range data.SetpointDescriptionData {
		if description.SetpointId != nil && description.ScopeType != nil &&
			*description.ScopeType == model.ScopeTypeTypeDhwTemperature {
			return *description.SetpointId, true
		}
	}
	return 0, false
}

func setpointValue(feature spineapi.FeatureRemoteInterface, id model.SetpointIdType) (float64, bool) {
	data, ok := feature.DataCopy(model.FunctionTypeSetpointListData).(*model.SetpointListDataType)
	if !ok || data == nil {
		return 0, false
	}
	for _, entry := range data.SetpointData {
		if entry.SetpointId != nil && *entry.SetpointId == id && entry.Value != nil {
			return entry.Value.GetValue(), true
		}
	}
	return 0, false
}

func setpointRange(feature spineapi.FeatureRemoteInterface, id model.SetpointIdType) (float64, float64, float64, bool) {
	data, ok := feature.DataCopy(model.FunctionTypeSetpointConstraintsListData).(*model.SetpointConstraintsListDataType)
	if !ok || data == nil {
		return 0, 0, 0, false
	}
	for _, constraint := range data.SetpointConstraintsData {
		if constraint.SetpointId == nil || *constraint.SetpointId != id || constraint.SetpointRangeMin == nil ||
			constraint.SetpointRangeMax == nil || constraint.SetpointStepSize == nil {
			continue
		}
		minimum := constraint.SetpointRangeMin.GetValue()
		maximum := constraint.SetpointRangeMax.GetValue()
		step := constraint.SetpointStepSize.GetValue()
		if !isFinite(minimum) || !isFinite(maximum) || !isFinite(step) || step <= 0 || minimum > maximum {
			return 0, 0, 0, false
		}
		return minimum, maximum, step, true
	}
	return 0, 0, 0, false
}

func isFinite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
