package grpc

import (
	"context"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	spineapi "github.com/enbility/spine-go/api"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// OHPCFService exposes the bridge's OHPCF (heat-pump compressor flexibility)
// CEM-client use case over gRPC: read the compressor's optional-consumption offer
// and schedule/pause/resume/abort it.
type OHPCFService struct {
	pb.UnimplementedOHPCFServiceServer
	ohpcf    *usecases.OHPCFWrapper
	bus      *eebus.EventBus
	registry *eebus.DeviceRegistry
}

func NewOHPCFService(ohpcf *usecases.OHPCFWrapper, bus *eebus.EventBus, registry *eebus.DeviceRegistry) *OHPCFService {
	return &OHPCFService{ohpcf: ohpcf, bus: bus, registry: registry}
}

func (s *OHPCFService) GetCompressorFlexibility(_ context.Context, req *pb.DeviceRequest) (*pb.CompressorFlexibility, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if s.ohpcf == nil {
		return nil, status.Error(codes.Unavailable, "OHPCF use case not initialized")
	}
	entity, err := s.resolveEntity(req.Ski)
	if err != nil {
		return nil, err
	}
	flexibility, successfulReads := s.buildFlexibility(entity)
	if successfulReads == 0 {
		if s.registry != nil {
			s.registry.RecordCapabilityRead(req.Ski, eebus.CapabilityOHPCF, eebusapi.ErrDataNotAvailable)
		}
		return nil, status.Error(codes.Unavailable, "reading compressor flexibility: temporarily unavailable")
	}
	if s.registry != nil {
		s.registry.RecordCapabilityRead(req.Ski, eebus.CapabilityOHPCF, nil)
	}
	return flexibility, nil
}

func (s *OHPCFService) ControlCompressorFlexibility(_ context.Context, req *pb.ControlCompressorRequest) (*pb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if req.Ski == "" {
		return nil, status.Error(codes.InvalidArgument, "ski is required for control operations")
	}
	if s.ohpcf == nil {
		return nil, status.Error(codes.Unavailable, "OHPCF use case not initialized")
	}
	entity, err := s.resolveEntity(req.Ski)
	if err != nil {
		return nil, err
	}

	switch req.Action {
	case pb.OHPCFAction_OHPCF_ACTION_SCHEDULE:
		var start time.Time
		if req.StartTime != nil {
			start = req.StartTime.AsTime()
		}
		err = s.ohpcf.Schedule(entity, start)
	case pb.OHPCFAction_OHPCF_ACTION_PAUSE:
		err = s.ohpcf.Pause(entity)
	case pb.OHPCFAction_OHPCF_ACTION_RESUME:
		err = s.ohpcf.Resume(entity)
	case pb.OHPCFAction_OHPCF_ACTION_ABORT:
		err = s.ohpcf.Abort(entity)
	default:
		return nil, status.Error(codes.InvalidArgument, "action is required (schedule/pause/resume/abort)")
	}
	if err != nil {
		return nil, mapUsecaseError("controlling compressor flexibility", err, standardUsecaseErrorClasses)
	}
	return &pb.Empty{}, nil
}

func (s *OHPCFService) SubscribeOHPCFEvents(req *pb.DeviceRequest, stream pb.OHPCFService_SubscribeOHPCFEventsServer) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "request is required")
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
			var eventType pb.OHPCFEventType
			switch evt.Type {
			case eebus.EventTypeOHPCFUseCaseSupportUpdated:
				eventType = pb.OHPCFEventType_OHPCF_EVENT_SUPPORT_UPDATED
			case eebus.EventTypeOHPCFConsumptionStateUpdated:
				eventType = pb.OHPCFEventType_OHPCF_EVENT_STATE_UPDATED
			case eebus.EventTypeOHPCFConsumptionStoppableUpdated,
				eebus.EventTypeOHPCFConsumptionPausableUpdated,
				eebus.EventTypeOHPCFConsumptionStartTimeUpdated,
				eebus.EventTypeOHPCFRequestedPowerEstimateUpdated,
				eebus.EventTypeOHPCFRequestedPowerMaxUpdated,
				eebus.EventTypeOHPCFMinimalRunDurationUpdated,
				eebus.EventTypeOHPCFMinimalPauseDurationUpdated:
				eventType = pb.OHPCFEventType_OHPCF_EVENT_DATA_UPDATED
			default:
				continue
			}
			event := &pb.OHPCFEvent{Ski: evt.SKI, EventType: eventType}
			if entity, err := s.resolveEntity(evt.SKI); err == nil {
				event.Flexibility, _ = s.buildFlexibility(entity)
			}
			if err := stream.Send(event); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

// buildFlexibility reads the current OHPCF offer/state best-effort. Individual
// reads return ErrDataInvalid when the compressor advertises no offer yet, so each
// field is filled only when it reads cleanly; optional power fields are omitted.
func (s *OHPCFService) buildFlexibility(entity spineapi.EntityRemoteInterface) (*pb.CompressorFlexibility, int) {
	f := &pb.CompressorFlexibility{}
	successfulReads := 0
	if available, err := s.ohpcf.OptionalPowerConsumptionAvailable(entity); err == nil {
		f.Available = available
		successfulReads++
	}
	if est, err := s.ohpcf.RequestedPowerEstimate(entity); err == nil {
		f.RequestedPowerEstimateW = &est
		successfulReads++
	}
	if max, err := s.ohpcf.RequestedPowerMax(entity); err == nil {
		f.RequestedPowerMaxW = &max
		successfulReads++
	}
	if stoppable, err := s.ohpcf.ConsumptionIsStoppable(entity); err == nil {
		f.IsStoppable = stoppable
		successfulReads++
	}
	if pausable, err := s.ohpcf.ConsumptionIsPausable(entity); err == nil {
		f.IsPausable = pausable
		successfulReads++
	}
	if st, err := s.ohpcf.ConsumptionState(entity); err == nil {
		f.State = convertCompressorState(st)
		successfulReads++
	}
	if d, err := s.ohpcf.MinimalRunDuration(entity); err == nil {
		f.MinimalRunSeconds = int64(d / time.Second)
		successfulReads++
	}
	if d, err := s.ohpcf.MinimalPauseDuration(entity); err == nil {
		f.MinimalPauseSeconds = int64(d / time.Second)
		successfulReads++
	}
	return f, successfulReads
}

func convertCompressorState(st ucapi.CompressorPowerConsumptionStateType) pb.CompressorPowerConsumptionState {
	switch st {
	case ucapi.CompressorPowerConsumptionStateAvailable:
		return pb.CompressorPowerConsumptionState_COMPRESSOR_STATE_AVAILABLE
	case ucapi.CompressorPowerConsumptionStateScheduled:
		return pb.CompressorPowerConsumptionState_COMPRESSOR_STATE_SCHEDULED
	case ucapi.CompressorPowerConsumptionStateRunning:
		return pb.CompressorPowerConsumptionState_COMPRESSOR_STATE_RUNNING
	case ucapi.CompressorPowerConsumptionStatePaused:
		return pb.CompressorPowerConsumptionState_COMPRESSOR_STATE_PAUSED
	case ucapi.CompressorPowerConsumptionStateCompleted:
		return pb.CompressorPowerConsumptionState_COMPRESSOR_STATE_COMPLETED
	case ucapi.CompressorPowerConsumptionStateStopped:
		return pb.CompressorPowerConsumptionState_COMPRESSOR_STATE_STOPPED
	default:
		return pb.CompressorPowerConsumptionState_COMPRESSOR_STATE_UNSPECIFIED
	}
}

// resolveEntity prefers the OHPCF-compatible Compressor entity (issue #47); a
// VR940 registers several entities under one SKI, so the flat registry may return
// the wrong one.
func (s *OHPCFService) resolveEntity(ski string) (spineapi.EntityRemoteInterface, error) {
	if s.ohpcf != nil {
		resolution := s.ohpcf.CompatibleEntity(ski)
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
	s.registry.RecordCapabilityMissingEntity(ski, eebus.CapabilityOHPCF)
	return nil, status.Errorf(codes.NotFound, "no compatible OHPCF entity found for ski %s", ski)
}
