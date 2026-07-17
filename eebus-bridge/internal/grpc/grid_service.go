package grpc

import (
	"context"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GridService is the gRPC front-end for the MGCP grid-connection-point provider.
// Home Assistant pushes the live grid situation (power + optional energy totals)
// so a consumer such as the Vaillant VR940 can read it for PV-surplus
// optimisation. The provider is experimental and may be nil when disabled, in
// which case calls return Unavailable (mirrors the LPC service's not-initialized
// behaviour).
type GridService struct {
	pb.UnimplementedGridServiceServer
	mgcp gridSnapshotPublisher
}

func NewGridService(mgcp *usecases.MGCPProvider) *GridService {
	service := &GridService{}
	if mgcp != nil {
		service.mgcp = mgcp
	}
	return service
}

type gridSnapshotPublisher interface {
	PublishGridSnapshot(usecases.GridSnapshot) error
}

func (s *GridService) PublishGridData(_ context.Context, req *pb.GridData) (*pb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	validity, err := providerValidity(req.Sample)
	if err != nil {
		return nil, err
	}
	if validity.Invalid {
		if s.mgcp == nil {
			return nil, status.Error(codes.Unavailable, "MGCP grid provider not enabled")
		}
		if err := s.mgcp.PublishGridSnapshot(usecases.GridSnapshot{Validity: validity}); err != nil {
			return nil, mapUsecaseError("invalidating grid data", err, standardUsecaseErrorClasses)
		}
		return &pb.Empty{}, nil
	}
	// PowerW is the signed grid surplus signal (negative = export), so any finite
	// value is valid; the energy totals are cumulative counters and cannot be
	// negative. Reject bad input before touching the provider.
	powerW := 0.0
	if req.PowerW == nil && req.Sample != nil {
		return nil, status.Error(codes.InvalidArgument, "grid power is required")
	}
	if req.PowerW != nil {
		powerW = *req.PowerW
	}
	if err := finite("grid power", powerW); err != nil {
		return nil, err
	}
	if req.FeedInWh != nil {
		if err := nonNegative("grid feed-in energy", *req.FeedInWh); err != nil {
			return nil, err
		}
	}
	if req.ConsumedWh != nil {
		if err := nonNegative("grid consumed energy", *req.ConsumedWh); err != nil {
			return nil, err
		}
	}
	if s.mgcp == nil {
		return nil, status.Error(codes.Unavailable, "MGCP grid provider not enabled")
	}
	if err := s.mgcp.PublishGridSnapshot(usecases.GridSnapshot{
		PowerW:     powerW,
		FeedInWh:   req.FeedInWh,
		ConsumedWh: req.ConsumedWh,
		Validity:   validity,
	}); err != nil {
		return nil, mapUsecaseError("publishing grid data", err, standardUsecaseErrorClasses)
	}
	return &pb.Empty{}, nil
}
