package usecases

import (
	"errors"
	"log"

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
			d.bus.Publish(eebus.Event{SKI: payload.Ski, Type: eebus.EventTypeDHWSystemFunctionSupportUpdated})
		}
	case *model.HvacSystemFunctionListDataType, *model.HvacOverrunListDataType:
		if _, err := d.State(payload.Entity); err == nil && d.bus != nil {
			d.bus.Publish(eebus.Event{SKI: payload.Ski, Type: eebus.EventTypeDHWSystemFunctionUpdated})
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
		recordCapabilitySupport(
			d.registry, ski, device, entity, d.CompatibleEntity(observationSKI(ski, device)),
			"dhw_system_function", eebus.CapabilityDHWSystemFunction,
		)
	}
	if d.bus != nil {
		d.bus.Publish(eebus.Event{SKI: ski, Type: eebus.EventTypeDHWSystemFunctionSupportUpdated})
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
	requestRemoteFeatureData(entity, hvacServer, d.localHvacFeature, function, d.debug, "DHWSYSFN")
}

// CompatibleEntity returns the negotiated DHWCircuit for a device SKI.
func (d *DHWSystemFunction) CompatibleEntity(ski string) eebus.EntityResolution {
	return compatibleEntity(d.RemoteEntitiesScenarios(), ski)
}

// State resolves DHW boost and operation mode from the remote HVAC cache.
func (d *DHWSystemFunction) State(entity spineapi.EntityRemoteInterface) (DHWSystemFunctionState, error) {
	return cachedDHWSystemFunctionCapabilityInspector{}.State(entity)
}

func (d *DHWSystemFunction) localHvacFeature() spineapi.FeatureLocalInterface {
	if d.localEntity == nil {
		return nil
	}
	return d.localEntity.FeatureOfTypeAndRole(model.FeatureTypeTypeHvac, model.RoleTypeClient)
}
