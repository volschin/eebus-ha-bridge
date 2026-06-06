package grpc

import (
	"context"
	"time"

	ucapi "github.com/enbility/eebus-go/usecases/api"
	spineapi "github.com/enbility/spine-go/api"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type LPCService struct {
	pb.UnimplementedLPCServiceServer
	lpc      *usecases.LPCWrapper
	bus      *eebus.EventBus
	registry *eebus.DeviceRegistry
}

func NewLPCService(lpc *usecases.LPCWrapper, bus *eebus.EventBus, registry *eebus.DeviceRegistry) *LPCService {
	return &LPCService{lpc: lpc, bus: bus, registry: registry}
}

func (s *LPCService) GetConsumptionLimit(_ context.Context, req *pb.DeviceRequest) (*pb.LoadLimit, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	entity, err := s.resolveEntity(req.Ski)
	if err != nil {
		return nil, err
	}
	limit, err := s.lpc.ConsumptionLimit(entity)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "reading consumption limit: %v", err)
	}
	return convertLoadLimit(limit), nil
}

func (s *LPCService) WriteConsumptionLimit(_ context.Context, req *pb.WriteLoadLimitRequest) (*pb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	entity, err := s.resolveEntity(req.Ski)
	if err != nil {
		return nil, err
	}
	err = s.lpc.WriteConsumptionLimit(entity, ucapi.LoadLimit{
		Value:        req.ValueWatts,
		Duration:     time.Duration(req.DurationSeconds) * time.Second,
		IsActive:     req.IsActive,
		IsChangeable: true,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "writing consumption limit: %v", err)
	}
	return &pb.Empty{}, nil
}

func (s *LPCService) GetFailsafeLimit(_ context.Context, req *pb.DeviceRequest) (*pb.FailsafeLimit, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	entity, err := s.resolveEntity(req.Ski)
	if err != nil {
		return nil, err
	}
	value, err := s.lpc.FailsafeConsumptionActivePowerLimit(entity)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "reading failsafe power: %v", err)
	}
	duration, err := s.lpc.FailsafeDurationMinimum(entity)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "reading failsafe duration: %v", err)
	}
	return &pb.FailsafeLimit{
		ValueWatts:             value,
		DurationMinimumSeconds: int64(duration / time.Second),
	}, nil
}

func (s *LPCService) WriteFailsafeLimit(_ context.Context, req *pb.WriteFailsafeLimitRequest) (*pb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	entity, err := s.resolveEntity(req.Ski)
	if err != nil {
		return nil, err
	}
	if err := s.lpc.WriteFailsafeConsumptionActivePowerLimit(entity, req.ValueWatts); err != nil {
		return nil, status.Errorf(codes.Internal, "writing failsafe power: %v", err)
	}
	if req.DurationMinimumSeconds > 0 {
		if err := s.lpc.WriteFailsafeDurationMinimum(entity, time.Duration(req.DurationMinimumSeconds)*time.Second); err != nil {
			return nil, status.Errorf(codes.Internal, "writing failsafe duration: %v", err)
		}
	}
	return &pb.Empty{}, nil
}

func (s *LPCService) StartHeartbeat(_ context.Context, req *pb.DeviceRequest) (*pb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	if err := s.lpc.StartHeartbeat(req.Ski); err != nil {
		return nil, status.Errorf(codes.Internal, "starting LPC heartbeat: %v", err)
	}
	return &pb.Empty{}, nil
}

func (s *LPCService) StopHeartbeat(_ context.Context, _ *pb.DeviceRequest) (*pb.Empty, error) {
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	if err := s.lpc.StopHeartbeat(); err != nil {
		return nil, status.Errorf(codes.Internal, "stopping LPC heartbeat: %v", err)
	}
	return &pb.Empty{}, nil
}

func (s *LPCService) GetHeartbeatStatus(_ context.Context, req *pb.DeviceRequest) (*pb.HeartbeatStatus, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	entity, err := s.resolveEntity(req.Ski)
	if err != nil {
		return &pb.HeartbeatStatus{
			Running:        s.lpc.IsHeartbeatRunning(),
			WithinDuration: false,
		}, nil
	}
	return &pb.HeartbeatStatus{
		Running:        s.lpc.IsHeartbeatRunning(),
		WithinDuration: s.lpc.IsHeartbeatWithinDuration(entity),
	}, nil
}

func (s *LPCService) GetConsumptionNominalMax(_ context.Context, req *pb.DeviceRequest) (*pb.PowerValue, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	entity, err := s.resolveEntity(req.Ski)
	if err != nil {
		return nil, err
	}
	value, err := s.lpc.ConsumptionNominalMax(entity)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "reading nominal max consumption: %v", err)
	}
	return &pb.PowerValue{Watts: value}, nil
}

func (s *LPCService) SubscribeLPCEvents(req *pb.DeviceRequest, stream pb.LPCService_SubscribeLPCEventsServer) error {
	ch := s.bus.Subscribe()
	defer s.bus.Unsubscribe(ch)

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return nil
			}
			if req.Ski != "" && evt.SKI != req.Ski {
				continue
			}
			var eventType pb.LPCEventType
			switch evt.Type {
			case "lpc.limit_updated":
				eventType = pb.LPCEventType_LPC_EVENT_LIMIT_UPDATED
			case "lpc.failsafe_power_updated", "lpc.failsafe_duration_updated":
				eventType = pb.LPCEventType_LPC_EVENT_FAILSAFE_UPDATED
			default:
				continue
			}
			if err := stream.Send(&pb.LPCEvent{
				Ski:       evt.SKI,
				EventType: eventType,
			}); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

func convertLoadLimit(l ucapi.LoadLimit) *pb.LoadLimit {
	return &pb.LoadLimit{
		ValueWatts:      l.Value,
		DurationSeconds: int64(l.Duration / time.Second),
		IsActive:        l.IsActive,
		IsChangeable:    l.IsChangeable,
	}
}

func (s *LPCService) resolveEntity(ski string) (spineapi.EntityRemoteInterface, error) {
	if s.registry == nil {
		return nil, status.Error(codes.Unavailable, "device registry not initialized")
	}
	ski = eebus.NormalizeSKI(ski)
	entity := s.registry.FirstEntity(ski)
	if entity == nil {
		// Bosch/Connect-Key sometimes stores the entity only after the LPC
		// UseCaseSupportUpdate callback. In single-device installations use the
		// only known entity as fallback instead of returning NOT_FOUND.
		entity = s.registry.FirstAvailableEntity()
	}
	if entity == nil {
		return nil, status.Errorf(codes.NotFound, "no remote entity found for ski %s", ski)
	}
	return entity, nil
}
