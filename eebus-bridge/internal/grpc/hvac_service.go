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

type roomHeatingTempController interface {
	CompatibleEntity(string) eebus.EntityResolution
	State(spineapi.EntityRemoteInterface) (usecases.RoomHeatingSetpoint, error)
	Write(context.Context, spineapi.EntityRemoteInterface, float64) error
}

type roomHeatingSysFnController interface {
	CompatibleEntity(string) eebus.EntityResolution
	State(spineapi.EntityRemoteInterface) (usecases.RoomHeatingSystemFunctionState, error)
	WriteOperationMode(context.Context, spineapi.EntityRemoteInterface, string) error
}

// HVACService exposes the room-heating Configuration use cases over gRPC.
type HVACService struct {
	pb.UnimplementedHVACServiceServer
	temp     roomHeatingTempController
	sysfn    roomHeatingSysFnController
	room     temperatureReader
	bus      *eebus.EventBus
	registry *eebus.DeviceRegistry
}

func NewHVACService(
	temp roomHeatingTempController,
	sysfn roomHeatingSysFnController,
	room temperatureReader,
	bus *eebus.EventBus,
	registries ...*eebus.DeviceRegistry,
) *HVACService {
	var registry *eebus.DeviceRegistry
	if len(registries) > 0 {
		registry = registries[0]
	}
	return &HVACService{temp: temp, sysfn: sysfn, room: room, bus: bus, registry: registry}
}

func (s *HVACService) GetRoomHeating(_ context.Context, req *pb.DeviceRequest) (*pb.RoomHeatingState, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if _, err := normalizeReadSKI(req.Ski); err != nil {
		return nil, err
	}
	state := &pb.RoomHeatingState{}
	entityResolved := false
	readSucceeded := false
	var resolveErr error
	if s.room != nil {
		if value, err := s.room.Temperature(req.Ski); err == nil {
			state.CurrentTemperatureCelsius = &value
			readSucceeded = true
		}
	}
	if s.temp != nil {
		entity, err := resolveCompatibleEntity(
			req.Ski,
			"HVACRoom",
			eebus.CapabilityRoomHeating,
			s.registry,
			s.temp.CompatibleEntity,
		)
		if err == nil {
			entityResolved = true
			if setpoint, err := s.temp.State(entity); err == nil {
				state.Setpoint = convertRoomHeatingSetpoint(setpoint)
				readSucceeded = true
			}
		} else if status.Code(err) == codes.InvalidArgument || status.Code(err) == codes.FailedPrecondition {
			return nil, err
		} else if resolveErr == nil {
			resolveErr = err
		}
	}
	if s.sysfn != nil {
		entity, err := resolveCompatibleEntity(
			req.Ski,
			"HVACRoom",
			eebus.CapabilityRoomHeating,
			s.registry,
			s.sysfn.CompatibleEntity,
		)
		if err == nil {
			entityResolved = true
			if sysfn, err := s.sysfn.State(entity); err == nil {
				state.SystemFunction = convertRoomHeatingSystemFunction(sysfn)
				readSucceeded = true
			}
		} else if status.Code(err) == codes.InvalidArgument || status.Code(err) == codes.FailedPrecondition {
			return nil, err
		} else if resolveErr == nil {
			resolveErr = err
		}
	}
	if !entityResolved && !readSucceeded {
		if resolveErr != nil {
			return nil, resolveErr
		}
		if s.registry != nil {
			s.registry.RecordCapabilityMissingEntity(req.Ski, eebus.CapabilityRoomHeating)
		}
		return nil, status.Errorf(codes.NotFound, "no compatible HVACRoom found for %s ski", requestedSKIForError(req.Ski))
	}
	if !readSucceeded {
		if s.registry != nil {
			s.registry.RecordCapabilityRead(req.Ski, eebus.CapabilityRoomHeating, usecases.ErrRoomHeatingDataUnavailable)
		}
		return nil, status.Error(codes.Unavailable, "reading room heating: temporarily unavailable")
	}
	if s.registry != nil {
		s.registry.RecordCapabilityRead(req.Ski, eebus.CapabilityRoomHeating, nil)
	}
	return state, nil
}

func (s *HVACService) SetRoomHeatingTemperature(
	ctx context.Context,
	req *pb.SetRoomHeatingTemperatureRequest,
) (*pb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if err := requireWriteSKI(req.Ski); err != nil {
		return nil, err
	}
	if s.temp == nil {
		return nil, status.Error(codes.Unavailable, "room heating temperature use case not initialized")
	}
	entity, err := resolveCompatibleEntity(
		req.Ski,
		"HVACRoom",
		eebus.CapabilityRoomHeating,
		s.registry,
		s.temp.CompatibleEntity,
	)
	if err != nil {
		return nil, err
	}
	if err := s.temp.Write(ctx, entity, req.ValueCelsius); err != nil {
		return nil, mapRoomHeatingError("writing room heating setpoint", err)
	}
	return &pb.Empty{}, nil
}

func (s *HVACService) SetRoomHeatingMode(
	ctx context.Context,
	req *pb.SetRoomHeatingModeRequest,
) (*pb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if err := requireWriteSKI(req.Ski); err != nil {
		return nil, err
	}
	if s.sysfn == nil {
		return nil, status.Error(codes.Unavailable, "room heating system function use case not initialized")
	}
	entity, err := resolveCompatibleEntity(
		req.Ski,
		"HVACRoom",
		eebus.CapabilityRoomHeating,
		s.registry,
		s.sysfn.CompatibleEntity,
	)
	if err != nil {
		return nil, err
	}
	if err := s.sysfn.WriteOperationMode(ctx, entity, req.Mode); err != nil {
		return nil, mapRoomHeatingError("writing room heating mode", err)
	}
	return &pb.Empty{}, nil
}

func (s *HVACService) SubscribeRoomHeatingEvents(
	req *pb.DeviceRequest,
	stream pb.HVACService_SubscribeRoomHeatingEventsServer,
) error {
	return subscribeFilteredEvents(s.bus, req, stream.Context(), stream.Send, func(evt eebus.Event) (*pb.RoomHeatingEvent, bool) {
		var eventType pb.RoomHeatingEventType
		switch evt.Type {
		case eebus.EventTypeRoomHeatingUseCaseSupportUpdated, eebus.EventTypeRoomHeatingSystemFunctionSupportUpdated:
			eventType = pb.RoomHeatingEventType_ROOM_HEATING_EVENT_SUPPORT_UPDATED
		case eebus.EventTypeRoomTemperatureUpdated:
			eventType = pb.RoomHeatingEventType_ROOM_HEATING_EVENT_CURRENT_TEMPERATURE_UPDATED
		case eebus.EventTypeRoomHeatingSetpointUpdated:
			eventType = pb.RoomHeatingEventType_ROOM_HEATING_EVENT_SETPOINT_UPDATED
		case eebus.EventTypeRoomHeatingSystemFunctionUpdated:
			eventType = pb.RoomHeatingEventType_ROOM_HEATING_EVENT_SYSTEM_FUNCTION_UPDATED
		default:
			return nil, false
		}
		event := &pb.RoomHeatingEvent{Ski: evt.SKI, EventType: eventType}
		if state, err := s.GetRoomHeating(stream.Context(), &pb.DeviceRequest{Ski: evt.SKI}); err == nil {
			event.State = state
		}
		return event, true
	})
}

func convertRoomHeatingSetpoint(state usecases.RoomHeatingSetpoint) *pb.RoomHeatingSetpoint {
	return &pb.RoomHeatingSetpoint{
		ValueCelsius: state.Value,
		MinCelsius:   state.Minimum,
		MaxCelsius:   state.Maximum,
		StepCelsius:  state.Step,
		Writable:     state.Writable,
	}
}

func convertRoomHeatingSystemFunction(state usecases.RoomHeatingSystemFunctionState) *pb.RoomHeatingSystemFunction {
	return &pb.RoomHeatingSystemFunction{
		OperationMode:  state.OperationMode,
		AvailableModes: state.AvailableModes,
		ModeWritable:   state.ModeWritable,
	}
}

var roomHeatingErrorClasses = usecaseErrorClasses{
	invalidArgument:    []error{usecases.ErrRoomHeatingOutOfRange, usecases.ErrRoomHeatingInvalidStep, usecases.ErrRoomHeatingSysFnInvalidMode},
	failedPrecondition: []error{usecases.ErrRoomHeatingNotWritable, usecases.ErrRoomHeatingRejected, usecases.ErrRoomHeatingSysFnNotWritable, usecases.ErrRoomHeatingSysFnRejected},
	unavailable:        []error{usecases.ErrRoomHeatingDataUnavailable, usecases.ErrRoomHeatingSysFnDataUnavailable},
}

func mapRoomHeatingError(action string, err error) error {
	return mapUsecaseError(action, err, roomHeatingErrorClasses)
}
