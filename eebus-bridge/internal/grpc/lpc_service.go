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
	if req.Ski == "" {
		return nil, status.Error(codes.InvalidArgument, "ski is required for write operations")
	}
	if err := nonNegative("value_watts", req.ValueWatts); err != nil {
		return nil, err
	}
	if err := nonNegative("duration_seconds", float64(req.DurationSeconds)); err != nil {
		return nil, err
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
	if req.Ski == "" {
		return nil, status.Error(codes.InvalidArgument, "ski is required for write operations")
	}
	if err := nonNegative("value_watts", req.ValueWatts); err != nil {
		return nil, err
	}
	if err := nonNegative("duration_minimum_seconds", float64(req.DurationMinimumSeconds)); err != nil {
		return nil, err
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

// StartHeartbeat starts the bridge-lifecycle-scoped heartbeat. The request's
// SKI is ignored.
//
// Deprecated: This RPC will be removed in a future breaking API version.
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

// StopHeartbeat stops the bridge-lifecycle-scoped heartbeat. The request's SKI
// is ignored.
//
// Deprecated: This RPC will be removed in a future breaking API version.
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
	// Heartbeat state is a property of the local entity, so report Running even
	// when the remote SKI cannot be resolved yet.
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

func (s *LPCService) SubscribeLPCEvents(req *pb.DeviceRequest, stream pb.LPCService_SubscribeLPCEventsServer) error {
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
			var eventType pb.LPCEventType
			switch evt.Type {
			case eebus.EventTypeLPCLimitUpdated:
				eventType = pb.LPCEventType_LPC_EVENT_LIMIT_UPDATED
			case eebus.EventTypeLPCFailsafePowerUpdated, eebus.EventTypeLPCFailsafeDurationUpdated:
				eventType = pb.LPCEventType_LPC_EVENT_FAILSAFE_UPDATED
			case eebus.EventTypeLPCHeartbeatUpdated:
				eventType = pb.LPCEventType_LPC_EVENT_HEARTBEAT_TIMEOUT
			default:
				continue
			}
			event := &pb.LPCEvent{Ski: evt.SKI, EventType: eventType}
			s.attachLPCPayload(event, evt.SKI, eventType)
			if err := stream.Send(event); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

// attachLPCPayload best-effort fills the event's typed payload with the current
// limit/failsafe values so subscribers receive data directly instead of having
// to poll. If the use case is not ready or the entity/value cannot be read, the
// event is sent without a payload and the client falls back to a refresh.
func (s *LPCService) attachLPCPayload(event *pb.LPCEvent, ski string, eventType pb.LPCEventType) {
	if s.lpc == nil {
		return
	}
	entity, err := s.resolveEntity(ski)
	if err != nil {
		return
	}
	switch eventType {
	case pb.LPCEventType_LPC_EVENT_LIMIT_UPDATED:
		if limit, err := s.lpc.ConsumptionLimit(entity); err == nil {
			event.Data = &pb.LPCEvent_LimitUpdate{LimitUpdate: convertLoadLimit(limit)}
		}
	case pb.LPCEventType_LPC_EVENT_FAILSAFE_UPDATED:
		value, verr := s.lpc.FailsafeConsumptionActivePowerLimit(entity)
		duration, derr := s.lpc.FailsafeDurationMinimum(entity)
		if verr == nil && derr == nil {
			event.Data = &pb.LPCEvent_FailsafeUpdate{FailsafeUpdate: &pb.FailsafeLimit{
				ValueWatts:             value,
				DurationMinimumSeconds: int64(duration / time.Second),
			}}
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
	// Prefer an entity that actually advertises the LPC use case. A device such as a
	// Vaillant VR940f gateway registers several entities under one SKI; the flat
	// registry returns whichever was observed first (often the monitoring meter),
	// which eebus-go rejects on write with ErrNoCompatibleEntity (issue #47).
	if s.lpc != nil {
		resolution := s.lpc.CompatibleEntity(ski)
		if resolution.Ambiguous() {
			return nil, ambiguousDeviceSelection(resolution.DeviceCount)
		}
		if resolution.Entity != nil {
			return resolution.Entity, nil
		}
	}
	if s.registry == nil {
		return nil, status.Error(codes.Unavailable, "device registry not initialized")
	}
	entity := s.registry.FirstEntity(ski)
	if entity == nil && ski == "" {
		resolution := s.registry.FirstAvailableEntity()
		if resolution.Ambiguous() {
			return nil, ambiguousDeviceSelection(resolution.DeviceCount)
		}
		entity = resolution.Entity
	}
	if entity == nil {
		return nil, status.Errorf(codes.NotFound, "no remote entity found for ski %s", ski)
	}
	return entity, nil
}
