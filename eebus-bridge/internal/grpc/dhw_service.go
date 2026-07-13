package grpc

import (
	"context"
	"errors"

	spineapi "github.com/enbility/spine-go/api"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type dhwController interface {
	CompatibleEntity(string) spineapi.EntityRemoteInterface
	State(spineapi.EntityRemoteInterface) (usecases.DHWSetpoint, error)
	Write(context.Context, spineapi.EntityRemoteInterface, float64) error
}

type dhwSysFnController interface {
	CompatibleEntity(string) spineapi.EntityRemoteInterface
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
}

func NewDHWService(dhw dhwController, dhwSysFn dhwSysFnController, bus *eebus.EventBus) *DHWService {
	return &DHWService{dhw: dhw, dhwSysFn: dhwSysFn, bus: bus}
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
	if err != nil {
		return nil, mapDHWError("reading DHW setpoint", err)
	}
	return convertDHWSetpoint(state), nil
}

func (s *DHWService) SetDHWSetpoint(ctx context.Context, req *pb.SetDHWSetpointRequest) (*pb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if req.Ski == "" {
		return nil, status.Error(codes.InvalidArgument, "ski is required for write operations")
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
	if err != nil {
		return nil, mapDHWError("reading DHW system function", err)
	}
	return convertDHWSystemFunctionState(state), nil
}

func (s *DHWService) SetDHWBoost(ctx context.Context, req *pb.SetDHWBoostRequest) (*pb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if req.Ski == "" {
		return nil, status.Error(codes.InvalidArgument, "ski is required for write operations")
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
	if req.Ski == "" {
		return nil, status.Error(codes.InvalidArgument, "ski is required for write operations")
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
	if req == nil {
		return status.Error(codes.InvalidArgument, "request is required")
	}
	ch := s.bus.Subscribe()
	defer s.bus.Unsubscribe(ch)

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return nil
			}
			if req.Ski != "" && eebus.NormalizeSKI(evt.SKI) != eebus.NormalizeSKI(req.Ski) {
				continue
			}
			var eventType pb.DHWEventType
			switch evt.Type {
			case "dhw.use_case_support_updated":
				eventType = pb.DHWEventType_DHW_EVENT_SUPPORT_UPDATED
			case "dhw.setpoint_updated":
				eventType = pb.DHWEventType_DHW_EVENT_SETPOINT_UPDATED
			default:
				continue
			}
			event := &pb.DHWEvent{Ski: evt.SKI, EventType: eventType}
			if entity := s.dhw.CompatibleEntity(evt.SKI); entity != nil {
				if state, err := s.dhw.State(entity); err == nil {
					event.Setpoint = convertDHWSetpoint(state)
				}
			}
			if err := stream.Send(event); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

func (s *DHWService) SubscribeDHWSystemFunctionEvents(
	req *pb.DeviceRequest,
	stream pb.DHWService_SubscribeDHWSystemFunctionEventsServer,
) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "request is required")
	}
	ch := s.bus.Subscribe()
	defer s.bus.Unsubscribe(ch)

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return nil
			}
			if req.Ski != "" && eebus.NormalizeSKI(evt.SKI) != eebus.NormalizeSKI(req.Ski) {
				continue
			}
			var eventType pb.DHWSystemFunctionEventType
			switch evt.Type {
			case "dhwsysfn.use_case_support_updated":
				eventType = pb.DHWSystemFunctionEventType_DHW_SYSTEM_FUNCTION_EVENT_SUPPORT_UPDATED
			case "dhwsysfn.updated":
				eventType = pb.DHWSystemFunctionEventType_DHW_SYSTEM_FUNCTION_EVENT_STATE_UPDATED
			default:
				continue
			}
			event := &pb.DHWSystemFunctionEvent{Ski: evt.SKI, EventType: eventType}
			if s.dhwSysFn != nil {
				if entity := s.dhwSysFn.CompatibleEntity(evt.SKI); entity != nil {
					if state, err := s.dhwSysFn.State(entity); err == nil {
						event.State = convertDHWSystemFunctionState(state)
					}
				}
			}
			if err := stream.Send(event); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

func (s *DHWService) resolveEntity(ski string) (spineapi.EntityRemoteInterface, error) {
	if s.dhw == nil {
		return nil, status.Error(codes.Unavailable, "DHW temperature use case not initialized")
	}
	entity := s.dhw.CompatibleEntity(ski)
	if entity == nil {
		return nil, status.Errorf(codes.NotFound, "no compatible DHWCircuit found for ski %s", ski)
	}
	return entity, nil
}

func (s *DHWService) resolveSysFnEntity(ski string) (spineapi.EntityRemoteInterface, error) {
	if s.dhwSysFn == nil {
		return nil, status.Error(codes.Unavailable, "DHW system function use case not initialized")
	}
	entity := s.dhwSysFn.CompatibleEntity(ski)
	if entity == nil {
		return nil, status.Errorf(codes.NotFound, "no compatible DHWCircuit found for ski %s", ski)
	}
	return entity, nil
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

func mapDHWError(action string, err error) error {
	switch {
	case errors.Is(err, usecases.ErrDHWOutOfRange), errors.Is(err, usecases.ErrDHWInvalidStep),
		errors.Is(err, usecases.ErrDHWSysFnInvalidMode):
		return status.Errorf(codes.InvalidArgument, "%s: %v", action, err)
	case errors.Is(err, usecases.ErrDHWNotWritable), errors.Is(err, usecases.ErrDHWSysFnNotWritable):
		return status.Errorf(codes.FailedPrecondition, "%s: %v", action, err)
	case errors.Is(err, usecases.ErrDHWDataUnavailable), errors.Is(err, usecases.ErrDHWSysFnDataUnavailable):
		return status.Errorf(codes.NotFound, "%s: %v", action, err)
	case errors.Is(err, context.Canceled):
		return status.Errorf(codes.Canceled, "%s: %v", action, err)
	case errors.Is(err, context.DeadlineExceeded):
		return status.Errorf(codes.DeadlineExceeded, "%s: %v", action, err)
	default:
		return status.Errorf(codes.Internal, "%s: %v", action, err)
	}
}
