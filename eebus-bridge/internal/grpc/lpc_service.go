package grpc

import (
	"context"
	"time"

	ucapi "github.com/enbility/eebus-go/usecases/api"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type LPCService struct {
	pb.UnimplementedLPCServiceServer
	lpc *usecases.LPCWrapper
	bus *eebus.EventBus
}

func NewLPCService(lpc *usecases.LPCWrapper, bus *eebus.EventBus) *LPCService {
	return &LPCService{lpc: lpc, bus: bus}
}

func (s *LPCService) GetConsumptionLimit(_ context.Context, _ *pb.DeviceRequest) (*pb.LoadLimit, error) {
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	return nil, status.Error(codes.Unimplemented, "entity lookup not yet wired")
}

func (s *LPCService) WriteConsumptionLimit(_ context.Context, _ *pb.WriteLoadLimitRequest) (*pb.Empty, error) {
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	return nil, status.Error(codes.Unimplemented, "entity lookup not yet wired")
}

func (s *LPCService) GetFailsafeLimit(_ context.Context, _ *pb.DeviceRequest) (*pb.FailsafeLimit, error) {
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	return nil, status.Error(codes.Unimplemented, "entity lookup not yet wired")
}

func (s *LPCService) WriteFailsafeLimit(_ context.Context, _ *pb.WriteFailsafeLimitRequest) (*pb.Empty, error) {
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	return nil, status.Error(codes.Unimplemented, "entity lookup not yet wired")
}

func (s *LPCService) StartHeartbeat(_ context.Context, _ *pb.DeviceRequest) (*pb.Empty, error) {
	return nil, status.Error(codes.Unimplemented, "heartbeat not supported by underlying use case")
}

func (s *LPCService) StopHeartbeat(_ context.Context, _ *pb.DeviceRequest) (*pb.Empty, error) {
	return nil, status.Error(codes.Unimplemented, "heartbeat not supported by underlying use case")
}

func (s *LPCService) GetHeartbeatStatus(_ context.Context, _ *pb.DeviceRequest) (*pb.HeartbeatStatus, error) {
	return nil, status.Error(codes.Unimplemented, "heartbeat not supported by underlying use case")
}

func (s *LPCService) GetConsumptionNominalMax(_ context.Context, _ *pb.DeviceRequest) (*pb.PowerValue, error) {
	if s.lpc == nil {
		return nil, status.Error(codes.Unavailable, "LPC use case not initialized")
	}
	return nil, status.Error(codes.Unimplemented, "entity lookup not yet wired")
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

// convertLoadLimit is a helper for future use when entity lookup is wired.
func convertLoadLimit(l ucapi.LoadLimit) *pb.LoadLimit {
	return &pb.LoadLimit{
		ValueWatts:      l.Value,
		DurationSeconds: int64(l.Duration / time.Second),
		IsActive:        l.IsActive,
		IsChangeable:    l.IsChangeable,
	}
}
