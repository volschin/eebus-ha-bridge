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
	mgcp *usecases.MGCPProvider
}

func NewGridService(mgcp *usecases.MGCPProvider) *GridService {
	return &GridService{mgcp: mgcp}
}

func (s *GridService) PublishGridData(_ context.Context, req *pb.GridData) (*pb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if s.mgcp == nil {
		return nil, status.Error(codes.Unavailable, "MGCP grid provider not enabled")
	}
	if err := s.mgcp.PublishPower(req.PowerW); err != nil {
		return nil, status.Errorf(codes.Internal, "publishing grid power: %v", err)
	}
	if req.FeedInWh != nil {
		if err := s.mgcp.PublishEnergyFeedIn(*req.FeedInWh); err != nil {
			return nil, status.Errorf(codes.Internal, "publishing grid feed-in energy: %v", err)
		}
	}
	if req.ConsumedWh != nil {
		if err := s.mgcp.PublishEnergyConsumed(*req.ConsumedWh); err != nil {
			return nil, status.Errorf(codes.Internal, "publishing grid consumed energy: %v", err)
		}
	}
	return &pb.Empty{}, nil
}
