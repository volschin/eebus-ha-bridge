package usecases

import (
	"context"
	"errors"
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
	ErrDHWRejected        = errors.New("DHW setpoint write rejected by device")
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
		recordCapabilitySupport(
			d.registry, ski, device, entity, d.CompatibleEntity(observationSKI(ski, device)),
			"dhw_temperature", eebus.CapabilityDHW,
		)
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
	requestRemoteFeatureData(entity, setpointServer, d.localSetpointFeature, function, d.debug, "DHW")
}

// CompatibleEntity returns the negotiated DHWCircuit for a device SKI.
func (d *DHWTemperature) CompatibleEntity(ski string) eebus.EntityResolution {
	return compatibleEntity(d.RemoteEntitiesScenarios(), ski)
}

// State reads the DHW target and its complete constraints from the remote cache.
// Missing or partial metadata fails closed so Home Assistant never invents a
// writable range for physical temperature control.
func (d *DHWTemperature) State(entity spineapi.EntityRemoteInterface) (DHWSetpoint, error) {
	state, _, _, err := readSetpointState(entity, dhwSetpointID, ErrDHWDataUnavailable)
	if err != nil {
		return DHWSetpoint{}, err
	}
	return DHWSetpoint(state), nil
}

// Write validates against device-provided constraints, sends the complete
// SetpointListData required by the VR940, and waits for the device result.
func (d *DHWTemperature) Write(ctx context.Context, entity spineapi.EntityRemoteInterface, value float64) error {
	state, id, remote, err := readSetpointState(entity, dhwSetpointID, ErrDHWDataUnavailable)
	if err != nil {
		return err
	}
	if err := validateSetpointWrite(state, value, ErrDHWNotWritable, ErrDHWOutOfRange, ErrDHWInvalidStep); err != nil {
		return err
	}
	return writeSetpointValue(
		ctx,
		entity,
		remote,
		d.localSetpointFeature(),
		id,
		value,
		"DHW setpoint",
		ErrDHWDataUnavailable,
		ErrDHWRejected,
		func() { d.request(entity, model.FunctionTypeSetpointListData) },
	)
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
