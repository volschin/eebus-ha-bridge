package grpc

import (
	"context"

	spineapi "github.com/enbility/spine-go/api"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type dhwController interface {
	CompatibleEntity(string) eebus.EntityResolution
	State(spineapi.EntityRemoteInterface) (usecases.DHWSetpoint, error)
	Write(context.Context, spineapi.EntityRemoteInterface, float64) error
}

type dhwSysFnController interface {
	CompatibleEntity(string) eebus.EntityResolution
	State(spineapi.EntityRemoteInterface) (usecases.DHWSystemFunctionState, error)
	WriteBoost(context.Context, spineapi.EntityRemoteInterface, bool) error
	WriteOperationMode(context.Context, spineapi.EntityRemoteInterface, string) error
}

// DHWService exposes the DHWCircuit target temperature over gRPC.
type DHWService struct {
	pb.UnimplementedDHWServiceServer
	dhw      dhwController
	dhwSysFn dhwSysFnController
	bus      *eebus.EventBus
	registry *eebus.DeviceRegistry
}

func NewDHWService(dhw dhwController, dhwSysFn dhwSysFnController, bus *eebus.EventBus, registries ...*eebus.DeviceRegistry) *DHWService {
	var registry *eebus.DeviceRegistry
	if len(registries) > 0 {
		registry = registries[0]
	}
	return &DHWService{dhw: dhw, dhwSysFn: dhwSysFn, bus: bus, registry: registry}
}

func (s *DHWService) GetDHWSetpoint(_ context.Context, req *pb.DeviceRequest) (*pb.DHWSetpoint, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	entity, err := s.resolveEntity(req.Ski)
	if err != nil {
		return nil, err
	}
	state, err := s.dhw.State(entity)
	if s.registry != nil {
		s.registry.RecordCapabilityRead(req.Ski, eebus.CapabilityDHW, err)
	}
	if err != nil {
		return nil, mapDHWError("reading DHW setpoint", err)
	}
	return convertDHWSetpoint(state), nil
}

func (s *DHWService) SetDHWSetpoint(ctx context.Context, req *pb.SetDHWSetpointRequest) (*pb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if err := requireWriteSKI(req.Ski); err != nil {
		return nil, err
	}
	entity, err := s.resolveEntity(req.Ski)
	if err != nil {
		return nil, err
	}
	if err := s.dhw.Write(ctx, entity, req.ValueCelsius); err != nil {
		return nil, mapDHWError("writing DHW setpoint", err)
	}
	return &pb.Empty{}, nil
}

func (s *DHWService) GetDHWSystemFunction(_ context.Context, req *pb.DeviceRequest) (*pb.DHWSystemFunctionState, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	entity, err := s.resolveSysFnEntity(req.Ski)
	if err != nil {
		return nil, err
	}
	state, err := s.dhwSysFn.State(entity)
	if s.registry != nil {
		s.registry.RecordCapabilityRead(req.Ski, eebus.CapabilityDHWSystemFunction, err)
	}
	if err != nil {
		return nil, mapDHWError("reading DHW system function", err)
	}
	return convertDHWSystemFunctionState(state), nil
}

func (s *DHWService) SetDHWBoost(ctx context.Context, req *pb.SetDHWBoostRequest) (*pb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if err := requireWriteSKI(req.Ski); err != nil {
		return nil, err
	}
	entity, err := s.resolveSysFnEntity(req.Ski)
	if err != nil {
		return nil, err
	}
	if err := s.dhwSysFn.WriteBoost(ctx, entity, req.Active); err != nil {
		return nil, mapDHWError("writing DHW boost", err)
	}
	return &pb.Empty{}, nil
}

func (s *DHWService) SetDHWOperationMode(ctx context.Context, req *pb.SetDHWOperationModeRequest) (*pb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if err := requireWriteSKI(req.Ski); err != nil {
		return nil, err
	}
	entity, err := s.resolveSysFnEntity(req.Ski)
	if err != nil {
		return nil, err
	}
	if err := s.dhwSysFn.WriteOperationMode(ctx, entity, req.Mode); err != nil {
		return nil, mapDHWError("writing DHW operation mode", err)
	}
	return &pb.Empty{}, nil
}

func (s *DHWService) SubscribeDHWEvents(req *pb.DeviceRequest, stream pb.DHWService_SubscribeDHWEventsServer) error {
	return subscribeFilteredEvents(s.bus, req, stream.Context(), stream.Send, func(evt eebus.Event) (*pb.DHWEvent, bool) {
		var eventType pb.DHWEventType
		switch evt.Type {
		case eebus.EventTypeDHWUseCaseSupportUpdated:
			eventType = pb.DHWEventType_DHW_EVENT_SUPPORT_UPDATED
		case eebus.EventTypeDHWSetpointUpdated:
			eventType = pb.DHWEventType_DHW_EVENT_SETPOINT_UPDATED
		default:
			return nil, false
		}
		event := &pb.DHWEvent{Ski: evt.SKI, EventType: eventType}
		if entity, err := s.resolveEntity(evt.SKI); err == nil {
			if state, err := s.dhw.State(entity); err == nil {
				event.Setpoint = convertDHWSetpoint(state)
			}
		}
		return event, true
	})
}

func (s *DHWService) SubscribeDHWSystemFunctionEvents(
	req *pb.DeviceRequest,
	stream pb.DHWService_SubscribeDHWSystemFunctionEventsServer,
) error {
	return subscribeFilteredEvents(s.bus, req, stream.Context(), stream.Send, func(evt eebus.Event) (*pb.DHWSystemFunctionEvent, bool) {
		var eventType pb.DHWSystemFunctionEventType
		switch evt.Type {
		case eebus.EventTypeDHWSystemFunctionSupportUpdated:
			eventType = pb.DHWSystemFunctionEventType_DHW_SYSTEM_FUNCTION_EVENT_SUPPORT_UPDATED
		case eebus.EventTypeDHWSystemFunctionUpdated:
			eventType = pb.DHWSystemFunctionEventType_DHW_SYSTEM_FUNCTION_EVENT_STATE_UPDATED
		default:
			return nil, false
		}
		event := &pb.DHWSystemFunctionEvent{Ski: evt.SKI, EventType: eventType}
		if entity, err := s.resolveSysFnEntity(evt.SKI); err == nil {
			if state, err := s.dhwSysFn.State(entity); err == nil {
				event.State = convertDHWSystemFunctionState(state)
			}
		}
		return event, true
	})
}

func (s *DHWService) resolveEntity(ski string) (spineapi.EntityRemoteInterface, error) {
	if _, err := normalizeReadSKI(ski); err != nil {
		return nil, err
	}
	if s.dhw == nil {
		return nil, status.Error(codes.Unavailable, "DHW temperature use case not initialized")
	}
	return resolveCompatibleEntity(ski, "DHWCircuit", eebus.CapabilityDHW, s.registry, s.dhw.CompatibleEntity)
}

func (s *DHWService) resolveSysFnEntity(ski string) (spineapi.EntityRemoteInterface, error) {
	if _, err := normalizeReadSKI(ski); err != nil {
		return nil, err
	}
	if s.dhwSysFn == nil {
		return nil, status.Error(codes.Unavailable, "DHW system function use case not initialized")
	}
	return resolveCompatibleEntity(
		ski,
		"DHWCircuit",
		eebus.CapabilityDHWSystemFunction,
		s.registry,
		s.dhwSysFn.CompatibleEntity,
	)
}

func convertDHWSetpoint(state usecases.DHWSetpoint) *pb.DHWSetpoint {
	return &pb.DHWSetpoint{
		ValueCelsius: state.Value,
		MinCelsius:   state.Minimum,
		MaxCelsius:   state.Maximum,
		StepCelsius:  state.Step,
		Writable:     state.Writable,
	}
}

func convertDHWSystemFunctionState(state usecases.DHWSystemFunctionState) *pb.DHWSystemFunctionState {
	return &pb.DHWSystemFunctionState{
		BoostStatus:    convertDHWBoostStatus(state.BoostStatus),
		BoostWritable:  state.BoostWritable,
		OperationMode:  state.OperationMode,
		AvailableModes: state.AvailableModes,
		ModeWritable:   state.ModeWritable,
	}
}

func convertDHWBoostStatus(status string) pb.DHWBoostStatus {
	switch status {
	case "inactive":
		return pb.DHWBoostStatus_DHW_BOOST_STATUS_INACTIVE
	case "active":
		return pb.DHWBoostStatus_DHW_BOOST_STATUS_ACTIVE
	case "running":
		return pb.DHWBoostStatus_DHW_BOOST_STATUS_RUNNING
	case "finished":
		return pb.DHWBoostStatus_DHW_BOOST_STATUS_FINISHED
	default:
		return pb.DHWBoostStatus_DHW_BOOST_STATUS_UNSPECIFIED
	}
}

var dhwErrorClasses = usecaseErrorClasses{
	invalidArgument:    []error{usecases.ErrDHWOutOfRange, usecases.ErrDHWInvalidStep, usecases.ErrDHWSysFnInvalidMode},
	failedPrecondition: []error{usecases.ErrDHWNotWritable, usecases.ErrDHWRejected, usecases.ErrDHWSysFnNotWritable, usecases.ErrDHWSysFnRejected},
	unavailable:        []error{usecases.ErrDHWDataUnavailable, usecases.ErrDHWSysFnDataUnavailable},
}

func mapDHWError(action string, err error) error {
	return mapUsecaseError(action, err, dhwErrorClasses)
}
