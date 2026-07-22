package usecases

import (
	"context"
	"fmt"
	"log"

	eebusapi "github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	cacrht "github.com/enbility/eebus-go/usecases/ca/crht"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

type caCRHTClient interface {
	eebusapi.UseCaseInterface
	RemoteEntitiesScenarios() []eebusapi.RemoteEntityScenarios
	State(spineapi.EntityRemoteInterface) (ucapi.RoomHeatingSetpointState, error)
}

type roomHeatingTemperatureEntityResolver interface {
	CompatibleEntity(string) eebus.EntityResolution
}

type roomHeatingTemperatureStateReader interface {
	State(spineapi.EntityRemoteInterface) (RoomHeatingSetpoint, error)
}

type roomHeatingTemperatureWriter interface {
	Write(context.Context, spineapi.EntityRemoteInterface, float64) error
}

// CRHTConfigurationFacade maps eebus-go's complete CRHT state into the
// bridge's stable room-heating contract. Phase 4 deliberately retains the
// bridge writer as the sole write strategy while upstream owns negotiation,
// cache population, reads and events.
type CRHTConfigurationFacade struct {
	useCase  eebusapi.UseCaseInterface
	resolver roomHeatingTemperatureEntityResolver
	reader   roomHeatingTemperatureStateReader
	writer   roomHeatingTemperatureWriter
	bus      *eebus.EventBus
	registry *eebus.DeviceRegistry
	debug    bool
}

func newCRHTConfigurationFacade(
	useCase eebusapi.UseCaseInterface,
	resolver roomHeatingTemperatureEntityResolver,
	reader roomHeatingTemperatureStateReader,
	writer roomHeatingTemperatureWriter,
) *CRHTConfigurationFacade {
	return &CRHTConfigurationFacade{
		useCase:  useCase,
		resolver: resolver,
		reader:   reader,
		writer:   writer,
	}
}

// NewUpstreamRoomHeatingTemperatureConfiguration selects eebus-go CRHT as the
// source of room-heating setpoint state. The legacy writer is isolated from
// negotiation and events until Phase 5 resolves auto/off write semantics.
func NewUpstreamRoomHeatingTemperatureConfiguration(
	localEntity spineapi.EntityLocalInterface,
	bus *eebus.EventBus,
	registry *eebus.DeviceRegistry,
	debug bool,
) *CRHTConfigurationFacade {
	facade := &CRHTConfigurationFacade{bus: bus, registry: registry, debug: debug}
	if localEntity == nil {
		return facade
	}
	client := cacrht.NewCRHT(localEntity, facade.HandleEvent)
	facade.useCase = client
	facade.resolver = crhtEntityResolver{client: client}
	facade.reader = upstreamRoomHeatingTemperatureReader{client: client}
	facade.writer = newLegacyRoomHeatingTemperatureStrategy(localEntity, debug)
	return facade
}

// UseCase returns eebus-go's CRHT use case for service registration.
func (f *CRHTConfigurationFacade) UseCase() eebusapi.UseCaseInterface {
	if f == nil {
		return nil
	}
	return f.useCase
}

func (f *CRHTConfigurationFacade) CompatibleEntity(ski string) eebus.EntityResolution {
	if f == nil || f.resolver == nil {
		return eebus.EntityResolution{}
	}
	return f.resolver.CompatibleEntity(ski)
}

func (f *CRHTConfigurationFacade) State(
	entity spineapi.EntityRemoteInterface,
) (RoomHeatingSetpoint, error) {
	if f == nil || f.reader == nil {
		return RoomHeatingSetpoint{}, ErrRoomHeatingDataUnavailable
	}
	return f.reader.State(entity)
}

func (f *CRHTConfigurationFacade) Write(
	ctx context.Context,
	entity spineapi.EntityRemoteInterface,
	value float64,
) error {
	if f == nil || f.writer == nil {
		return ErrRoomHeatingNotWritable
	}
	return f.writer.Write(ctx, entity, value)
}

// HandleEvent maps CRHT's value, constraint and support callbacks onto the
// existing bridge event contract. A data event is only published once the
// combined upstream state is complete, avoiding a transient zero/partial
// snapshot while initial value and constraint responses converge.
func (f *CRHTConfigurationFacade) HandleEvent(
	ski string,
	device spineapi.DeviceRemoteInterface,
	entity spineapi.EntityRemoteInterface,
	event eebusapi.EventType,
) {
	if f == nil {
		return
	}
	if f.debug {
		log.Printf("[DEBUG] EEBUS room heating temperature event received: ski=%s event=%s", ski, event)
	}

	var eventType eebus.EventType
	switch event {
	case cacrht.UseCaseSupportUpdate:
		eventType = eebus.EventTypeRoomHeatingUseCaseSupportUpdated
		recordCapabilitySupport(
			f.registry,
			ski,
			device,
			entity,
			f.CompatibleEntity(observationSKI(ski, device)),
			"room_heating_temperature",
			eebus.CapabilityRoomHeating,
		)
	case cacrht.DataUpdateSetpoints, cacrht.DataUpdateSetpointConstraints:
		if _, err := f.State(entity); err != nil {
			return
		}
		eventType = eebus.EventTypeRoomHeatingSetpointUpdated
		if f.registry != nil {
			f.registry.UpsertObservation(ski, device, entity, "room_heating_temperature")
		}
	default:
		return
	}
	if f.bus != nil {
		f.bus.Publish(eebus.Event{SKI: ski, Type: eventType})
	}
}

type crhtEntityResolver struct {
	client caCRHTClient
}

func (r crhtEntityResolver) CompatibleEntity(ski string) eebus.EntityResolution {
	if r.client == nil {
		return eebus.EntityResolution{}
	}
	return compatibleEntity(r.client.RemoteEntitiesScenarios(), ski)
}

type upstreamRoomHeatingTemperatureReader struct {
	client caCRHTClient
}

func (r upstreamRoomHeatingTemperatureReader) State(
	entity spineapi.EntityRemoteInterface,
) (RoomHeatingSetpoint, error) {
	if r.client == nil || entity == nil {
		return RoomHeatingSetpoint{}, ErrRoomHeatingDataUnavailable
	}
	state, err := r.client.State(entity)
	if err != nil {
		return RoomHeatingSetpoint{}, fmt.Errorf("%w: %v", ErrRoomHeatingDataUnavailable, err)
	}
	return RoomHeatingSetpoint{
		Value:    state.Value,
		Minimum:  state.MinValue,
		Maximum:  state.MaxValue,
		Step:     state.StepSize,
		Writable: state.IsWritable,
	}, nil
}
