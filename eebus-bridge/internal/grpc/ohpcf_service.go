package grpc

import (
	"context"
	"errors"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	spineapi "github.com/enbility/spine-go/api"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ohpcfReadMask uint16

const (
	ohpcfReadAvailable ohpcfReadMask = 1 << iota
	ohpcfReadPowerEstimate
	ohpcfReadPowerMax
	ohpcfReadStoppable
	ohpcfReadPausable
	ohpcfReadState
	ohpcfReadMinimalRun
	ohpcfReadMinimalPause
	ohpcfReadStartTime
)

// ohpcfCoreReadMask contains the two values OHPCF defines even when no optional
// consumption process exists: availability=false and state=stopped. All other
// fields describe an offered process and may legitimately be absent while idle.
const ohpcfCoreReadMask = ohpcfReadAvailable | ohpcfReadState

// OHPCFService exposes the bridge's OHPCF (heat-pump compressor flexibility)
// CEM-client use case over gRPC: read the compressor's optional-consumption offer
// and schedule/pause/resume/abort it.
type OHPCFService struct {
	pb.UnimplementedOHPCFServiceServer
	ohpcf    OHPCFController
	bus      *eebus.EventBus
	registry *eebus.DeviceRegistry
}

// OHPCFController is the narrow read/write seam used by the gRPC adapter.
type OHPCFController interface {
	OptionalPowerConsumptionAvailable(spineapi.EntityRemoteInterface) (bool, error)
	CompatibleEntity(string) eebus.EntityResolution
	RequestedPowerEstimate(spineapi.EntityRemoteInterface) (float64, error)
	RequestedPowerMax(spineapi.EntityRemoteInterface) (float64, error)
	ConsumptionIsStoppable(spineapi.EntityRemoteInterface) (bool, error)
	ConsumptionIsPausable(spineapi.EntityRemoteInterface) (bool, error)
	ConsumptionState(spineapi.EntityRemoteInterface) (ucapi.CompressorPowerConsumptionStateType, error)
	ConsumptionStartTime(spineapi.EntityRemoteInterface) (time.Time, error)
	MinimalRunDuration(spineapi.EntityRemoteInterface) (time.Duration, error)
	MinimalPauseDuration(spineapi.EntityRemoteInterface) (time.Duration, error)
	Schedule(spineapi.EntityRemoteInterface, time.Time) error
	Pause(spineapi.EntityRemoteInterface) error
	Resume(spineapi.EntityRemoteInterface) error
	Abort(spineapi.EntityRemoteInterface) error
}

// OHPCFServiceOption customizes the OHPCF adapter at construction time.
type OHPCFServiceOption func(*OHPCFService)

// WithOHPCFController replaces the production wrapper for deterministic tests.
func WithOHPCFController(controller OHPCFController) OHPCFServiceOption {
	return func(service *OHPCFService) { service.ohpcf = controller }
}

func NewOHPCFService(
	ohpcf *usecases.OHPCFWrapper,
	bus *eebus.EventBus,
	registry *eebus.DeviceRegistry,
	opts ...OHPCFServiceOption,
) *OHPCFService {
	service := &OHPCFService{bus: bus, registry: registry}
	if ohpcf != nil {
		service.ohpcf = ohpcf
	}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

func (s *OHPCFService) GetCompressorFlexibility(_ context.Context, req *pb.DeviceRequest) (*pb.CompressorFlexibility, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if _, err := normalizeReadSKI(req.Ski); err != nil {
		return nil, err
	}
	if s.ohpcf == nil {
		return nil, status.Error(codes.Unavailable, "OHPCF use case not initialized")
	}
	entity, err := s.resolveEntity(req.Ski)
	if err != nil {
		return nil, err
	}
	flexibility, reads, _ := s.buildFlexibility(entity)
	if reads&ohpcfCoreReadMask != ohpcfCoreReadMask {
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
	if err := requireWriteSKI(req.Ski); err != nil {
		return nil, err
	}
	if s.ohpcf == nil {
		return nil, status.Error(codes.Unavailable, "OHPCF use case not initialized")
	}
	entity, err := s.resolveEntity(req.Ski)
	if err != nil {
		return nil, err
	}
	if err := s.validateOHPCFAction(entity, req.Action); err != nil {
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

func (s *OHPCFService) validateOHPCFAction(
	entity spineapi.EntityRemoteInterface,
	action pb.OHPCFAction,
) error {
	switch action {
	case pb.OHPCFAction_OHPCF_ACTION_SCHEDULE:
		available, err := s.ohpcf.OptionalPowerConsumptionAvailable(entity)
		if err != nil {
			return mapUsecaseError("validating compressor schedule", err, standardUsecaseErrorClasses)
		}
		if !available {
			return status.Error(codes.FailedPrecondition, "compressor schedule requires an available optional-consumption process")
		}
	case pb.OHPCFAction_OHPCF_ACTION_PAUSE:
		state, err := s.ohpcf.ConsumptionState(entity)
		if err != nil {
			return mapUsecaseError("validating compressor pause", err, standardUsecaseErrorClasses)
		}
		if state != ucapi.CompressorPowerConsumptionStateRunning {
			return status.Error(codes.FailedPrecondition, "compressor pause requires a running process")
		}
		pausable, err := s.ohpcf.ConsumptionIsPausable(entity)
		if err != nil && !isOHPCFOptionalAbsent(err) {
			return mapUsecaseError("validating compressor pause", err, standardUsecaseErrorClasses)
		}
		if !pausable {
			return status.Error(codes.FailedPrecondition, "compressor process is not pausable")
		}
	case pb.OHPCFAction_OHPCF_ACTION_RESUME:
		state, err := s.ohpcf.ConsumptionState(entity)
		if err != nil {
			return mapUsecaseError("validating compressor resume", err, standardUsecaseErrorClasses)
		}
		if state != ucapi.CompressorPowerConsumptionStatePaused {
			return status.Error(codes.FailedPrecondition, "compressor resume requires a paused process")
		}
	case pb.OHPCFAction_OHPCF_ACTION_ABORT:
		state, err := s.ohpcf.ConsumptionState(entity)
		if err != nil {
			return mapUsecaseError("validating compressor abort", err, standardUsecaseErrorClasses)
		}
		if state != ucapi.CompressorPowerConsumptionStateScheduled &&
			state != ucapi.CompressorPowerConsumptionStateRunning &&
			state != ucapi.CompressorPowerConsumptionStatePaused {
			return status.Error(codes.FailedPrecondition, "compressor abort requires an active or scheduled process")
		}
		stoppable, err := s.ohpcf.ConsumptionIsStoppable(entity)
		if err != nil && !isOHPCFOptionalAbsent(err) {
			return mapUsecaseError("validating compressor abort", err, standardUsecaseErrorClasses)
		}
		if !stoppable {
			return status.Error(codes.FailedPrecondition, "compressor process is not stoppable")
		}
	default:
		return status.Error(codes.InvalidArgument, "action is required (schedule/pause/resume/abort)")
	}
	return nil
}

func (s *OHPCFService) SubscribeOHPCFEvents(req *pb.DeviceRequest, stream pb.OHPCFService_SubscribeOHPCFEventsServer) error {
	return subscribeFilteredEvents(s.bus, req, stream.Context(), stream.Send, func(evt eebus.Event) (*pb.OHPCFEvent, bool) {
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
			return nil, false
		}
		event := &pb.OHPCFEvent{Ski: evt.SKI, EventType: eventType}
		s.attachOHPCFPayload(event, evt.SKI, evt.Type)
		return event, true
	})
}

// attachOHPCFPayload is shared by the legacy OHPCF stream and the consolidated
// DeviceState stream so both expose identical flexibility conversion. The
// payload keeps the legacy-stream contract (attached whenever any field reads
// cleanly); the boolean answers the stricter consolidated-envelope question of
// whether the core aggregate plus the event's target field were all readable.
func (s *OHPCFService) attachOHPCFPayload(event *pb.OHPCFEvent, ski string, eventType eebus.EventType) bool {
	if s.ohpcf == nil {
		return false
	}
	entity, err := s.resolveEntity(ski)
	if err != nil {
		return false
	}
	flexibility, reads, clears := s.buildFlexibility(entity)
	if reads == 0 {
		if s.registry != nil {
			s.registry.RecordCapabilityRead(ski, eebus.CapabilityOHPCF, eebusapi.ErrDataNotAvailable)
		}
		return false
	}
	event.Flexibility = flexibility
	target := ohpcfTargetRead(eventType)
	available := target != 0 && reads&ohpcfCoreReadMask == ohpcfCoreReadMask && (reads|clears)&target == target
	if s.registry != nil {
		var readErr error
		if !available {
			readErr = eebusapi.ErrDataNotAvailable
		}
		s.registry.RecordCapabilityRead(ski, eebus.CapabilityOHPCF, readErr)
	}
	return available
}

func (s *OHPCFService) AttachOHPCFPayload(event *pb.OHPCFEvent, ski string, eventType eebus.EventType) bool {
	return s.attachOHPCFPayload(event, ski, eventType)
}

func ohpcfTargetRead(eventType eebus.EventType) ohpcfReadMask {
	switch eventType {
	case eebus.EventTypeOHPCFConsumptionStateUpdated:
		return ohpcfReadAvailable | ohpcfReadState
	case eebus.EventTypeOHPCFConsumptionStoppableUpdated:
		return ohpcfReadStoppable
	case eebus.EventTypeOHPCFConsumptionPausableUpdated:
		return ohpcfReadPausable
	case eebus.EventTypeOHPCFConsumptionStartTimeUpdated:
		return ohpcfReadStartTime
	case eebus.EventTypeOHPCFRequestedPowerEstimateUpdated:
		return ohpcfReadPowerEstimate
	case eebus.EventTypeOHPCFRequestedPowerMaxUpdated:
		return ohpcfReadPowerMax
	case eebus.EventTypeOHPCFMinimalRunDurationUpdated:
		return ohpcfReadMinimalRun
	case eebus.EventTypeOHPCFMinimalPauseDurationUpdated:
		return ohpcfReadMinimalPause
	default:
		return 0
	}
}

// buildFlexibility reads the current OHPCF offer/state best-effort. Individual
// reads return ErrDataInvalid when the compressor advertises no offer yet, so each
// field is filled only when it reads cleanly; optional power fields are omitted.
func (s *OHPCFService) buildFlexibility(entity spineapi.EntityRemoteInterface) (*pb.CompressorFlexibility, ohpcfReadMask, ohpcfReadMask) {
	f := &pb.CompressorFlexibility{}
	var reads ohpcfReadMask
	var clears ohpcfReadMask
	if available, err := s.ohpcf.OptionalPowerConsumptionAvailable(entity); err == nil {
		f.Available = available
		reads |= ohpcfReadAvailable
	}
	if est, err := s.ohpcf.RequestedPowerEstimate(entity); err == nil {
		f.RequestedPowerEstimateW = &est
		reads |= ohpcfReadPowerEstimate
	} else if isOHPCFOptionalAbsent(err) {
		clears |= ohpcfReadPowerEstimate
	}
	if max, err := s.ohpcf.RequestedPowerMax(entity); err == nil {
		f.RequestedPowerMaxW = &max
		reads |= ohpcfReadPowerMax
	} else if isOHPCFOptionalAbsent(err) {
		clears |= ohpcfReadPowerMax
	}
	if stoppable, err := s.ohpcf.ConsumptionIsStoppable(entity); err == nil {
		f.IsStoppable = stoppable
		reads |= ohpcfReadStoppable
	} else if isOHPCFOptionalAbsent(err) {
		clears |= ohpcfReadStoppable
	}
	if pausable, err := s.ohpcf.ConsumptionIsPausable(entity); err == nil {
		f.IsPausable = pausable
		reads |= ohpcfReadPausable
	} else if isOHPCFOptionalAbsent(err) {
		clears |= ohpcfReadPausable
	}
	if st, err := s.ohpcf.ConsumptionState(entity); err == nil {
		f.State = convertCompressorState(st)
		reads |= ohpcfReadState
	}
	if d, err := s.ohpcf.MinimalRunDuration(entity); err == nil {
		f.MinimalRunSeconds = int64(d / time.Second)
		reads |= ohpcfReadMinimalRun
	} else if isOHPCFOptionalAbsent(err) {
		clears |= ohpcfReadMinimalRun
	}
	if d, err := s.ohpcf.MinimalPauseDuration(entity); err == nil {
		f.MinimalPauseSeconds = int64(d / time.Second)
		reads |= ohpcfReadMinimalPause
	} else if isOHPCFOptionalAbsent(err) {
		clears |= ohpcfReadMinimalPause
	}
	if start, err := s.ohpcf.ConsumptionStartTime(entity); err == nil {
		f.StartTime = timestamppb.New(start)
		reads |= ohpcfReadStartTime
	} else if isOHPCFOptionalAbsent(err) {
		clears |= ohpcfReadStartTime
	}
	return f, reads, clears
}

func isOHPCFOptionalAbsent(err error) bool {
	return errors.Is(err, eebusapi.ErrDataInvalid) || errors.Is(err, eebusapi.ErrDataNotAvailable)
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
	var resolver compatibleEntityResolver
	if s.ohpcf != nil {
		resolver = s.ohpcf.CompatibleEntity
	}
	return resolveCompatibleEntity(ski, "OHPCF entity", eebus.CapabilityOHPCF, s.registry, resolver)
}
