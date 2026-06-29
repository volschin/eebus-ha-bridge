package grpc

import (
	"context"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// VisualizationService is the gRPC front-end for the VAPD (PV) and VABD (battery)
// display providers. Home Assistant pushes live PV / battery figures so a consumer
// such as the Vaillant VR940 can display the home's PV/battery state. Each provider
// is experimental and may be nil when disabled, in which case its RPC returns
// Unavailable (mirrors the grid/LPC services' not-initialized behaviour).
type VisualizationService struct {
	pb.UnimplementedVisualizationServiceServer
	vapd *usecases.VAPDProvider
	vabd *usecases.VABDProvider
}

func NewVisualizationService(vapd *usecases.VAPDProvider, vabd *usecases.VABDProvider) *VisualizationService {
	return &VisualizationService{vapd: vapd, vabd: vabd}
}

func (s *VisualizationService) PublishPVData(_ context.Context, req *pb.PVData) (*pb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	// PV production and its yield/peak figures are non-negative by definition.
	// Reject bad input before touching the provider.
	if err := nonNegative("PV power", req.PowerW); err != nil {
		return nil, err
	}
	if req.YieldWh != nil {
		if err := nonNegative("PV yield energy", *req.YieldWh); err != nil {
			return nil, err
		}
	}
	if req.PeakPowerW != nil {
		if err := nonNegative("PV peak power", *req.PeakPowerW); err != nil {
			return nil, err
		}
	}
	if s.vapd == nil {
		return nil, status.Error(codes.Unavailable, "VAPD PV provider not enabled")
	}
	if err := s.vapd.PublishPower(req.PowerW); err != nil {
		return nil, status.Errorf(codes.Internal, "publishing PV power: %v", err)
	}
	if req.YieldWh != nil {
		if err := s.vapd.PublishYield(*req.YieldWh); err != nil {
			return nil, status.Errorf(codes.Internal, "publishing PV yield energy: %v", err)
		}
	}
	if req.PeakPowerW != nil {
		if err := s.vapd.PublishPeakPower(*req.PeakPowerW); err != nil {
			return nil, status.Errorf(codes.Internal, "publishing PV peak power: %v", err)
		}
	}
	return &pb.Empty{}, nil
}

func (s *VisualizationService) PublishBatteryData(_ context.Context, req *pb.BatteryData) (*pb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	// Battery PowerW is signed (negative = charging), so any finite value is
	// valid; the energy totals are non-negative counters and SoC is a percentage.
	// Reject bad input before touching the provider.
	if err := finite("battery power", req.PowerW); err != nil {
		return nil, err
	}
	if req.ChargedWh != nil {
		if err := nonNegative("battery charged energy", *req.ChargedWh); err != nil {
			return nil, err
		}
	}
	if req.DischargedWh != nil {
		if err := nonNegative("battery discharged energy", *req.DischargedWh); err != nil {
			return nil, err
		}
	}
	if req.StateOfChargePct != nil {
		if err := percent("battery state of charge", *req.StateOfChargePct); err != nil {
			return nil, err
		}
	}
	if s.vabd == nil {
		return nil, status.Error(codes.Unavailable, "VABD battery provider not enabled")
	}
	if err := s.vabd.PublishPower(req.PowerW); err != nil {
		return nil, status.Errorf(codes.Internal, "publishing battery power: %v", err)
	}
	if req.ChargedWh != nil {
		if err := s.vabd.PublishEnergyCharged(*req.ChargedWh); err != nil {
			return nil, status.Errorf(codes.Internal, "publishing battery charged energy: %v", err)
		}
	}
	if req.DischargedWh != nil {
		if err := s.vabd.PublishEnergyDischarged(*req.DischargedWh); err != nil {
			return nil, status.Errorf(codes.Internal, "publishing battery discharged energy: %v", err)
		}
	}
	if req.StateOfChargePct != nil {
		if err := s.vabd.PublishStateOfCharge(*req.StateOfChargePct); err != nil {
			return nil, status.Errorf(codes.Internal, "publishing battery state of charge: %v", err)
		}
	}
	return &pb.Empty{}, nil
}
