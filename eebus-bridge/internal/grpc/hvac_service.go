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
	CompatibleEntity(string) spineapi.EntityRemoteInterface
	State(spineapi.EntityRemoteInterface) (usecases.RoomHeatingSetpoint, error)
	Write(context.Context, spineapi.EntityRemoteInterface, float64) error
}

type roomHeatingSysFnController interface {
	CompatibleEntity(string) spineapi.EntityRemoteInterface
	State(spineapi.EntityRemoteInterface) (usecases.RoomHeatingSystemFunctionState, error)
	WriteOperationMode(context.Context, spineapi.EntityRemoteInterface, string) error
}

// HVACService exposes the room-heating Configuration use cases over gRPC.
type HVACService struct {
	pb.UnimplementedHVACServiceServer
	temp  roomHeatingTempController
	sysfn roomHeatingSysFnController
	room  temperatureReader
	bus   *eebus.EventBus
}

func NewHVACService(
	temp roomHeatingTempController,
	sysfn roomHeatingSysFnController,
	room temperatureReader,
	bus *eebus.EventBus,
) *HVACService {
	return &HVACService{temp: temp, sysfn: sysfn, room: room, bus: bus}
}

func (s *HVACService) GetRoomHeating(_ context.Context, req *pb.DeviceRequest) (*pb.RoomHeatingState, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	state := &pb.RoomHeatingState{}
	resolved := false
	if s.room != nil {
		if value, err := s.room.Temperature(req.Ski); err == nil {
			state.CurrentTemperatureCelsius = &value
			resolved = true
		}
	}
	if s.temp != nil {
		if entity := s.temp.CompatibleEntity(req.Ski); entity != nil {
			resolved = true
			if setpoint, err := s.temp.State(entity); err == nil {
				state.Setpoint = convertRoomHeatingSetpoint(setpoint)
			}
		}
	}
	if s.sysfn != nil {
		if entity := s.sysfn.CompatibleEntity(req.Ski); entity != nil {
			resolved = true
			if sysfn, err := s.sysfn.State(entity); err == nil {
				state.SystemFunction = convertRoomHeatingSystemFunction(sysfn)
			}
		}
	}
	if !resolved {
		return nil, status.Errorf(codes.NotFound, "no compatible HVACRoom found for ski %s", req.Ski)
	}
	return state, nil
}

func (s *HVACService) SetRoomHeatingTemperature(
	ctx context.Context,
	req *pb.SetRoomHeatingTemperatureRequest,
) (*pb.Empty, error) {
	if req == nil || req.Ski == "" {
		return nil, status.Error(codes.InvalidArgument, "ski is required for write operations")
	}
	if s.temp == nil {
		return nil, status.Error(codes.Unavailable, "room heating temperature use case not initialized")
	}
	entity := s.temp.CompatibleEntity(req.Ski)
	if entity == nil {
		return nil, status.Errorf(codes.NotFound, "no compatible HVACRoom found for ski %s", req.Ski)
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
	if req == nil || req.Ski == "" {
		return nil, status.Error(codes.InvalidArgument, "ski is required for write operations")
	}
	if s.sysfn == nil {
		return nil, status.Error(codes.Unavailable, "room heating system function use case not initialized")
	}
	entity := s.sysfn.CompatibleEntity(req.Ski)
	if entity == nil {
		return nil, status.Errorf(codes.NotFound, "no compatible HVACRoom found for ski %s", req.Ski)
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
	if req == nil {
		return status.Error(codes.InvalidArgument, "request is required")
	}
	if s.bus == nil {
		return status.Error(codes.Unavailable, "event bus not initialized")
	}
	ch := s.bus.Subscribe()
	defer s.bus.Unsubscribe(ch)
	reqSKI := eebus.NormalizeSKI(req.Ski)

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return nil
			}
			if reqSKI != "" && evt.SKI != reqSKI {
				continue
			}
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
				continue
			}
			event := &pb.RoomHeatingEvent{Ski: evt.SKI, EventType: eventType}
			if state, err := s.GetRoomHeating(stream.Context(), &pb.DeviceRequest{Ski: evt.SKI}); err == nil {
				event.State = state
			}
			if err := stream.Send(event); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
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
	failedPrecondition: []error{usecases.ErrRoomHeatingNotWritable, usecases.ErrRoomHeatingSysFnNotWritable, usecases.ErrRoomHeatingSysFnRejected},
	notFound:           []error{usecases.ErrRoomHeatingDataUnavailable, usecases.ErrRoomHeatingSysFnDataUnavailable},
}

func mapRoomHeatingError(action string, err error) error {
	return mapUsecaseError(action, err, roomHeatingErrorClasses)
}
