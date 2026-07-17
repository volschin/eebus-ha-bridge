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
	vapd pvSnapshotPublisher
	vabd batterySnapshotPublisher
}

func NewVisualizationService(vapd *usecases.VAPDProvider, vabd *usecases.VABDProvider) *VisualizationService {
	service := &VisualizationService{}
	if vapd != nil {
		service.vapd = vapd
	}
	if vabd != nil {
		service.vabd = vabd
	}
	return service
}

type pvSnapshotPublisher interface {
	PublishPVSnapshot(usecases.PVSnapshot) error
	PublishPeakPower(float64) error
}

type batterySnapshotPublisher interface {
	PublishBatterySnapshot(usecases.BatterySnapshot) error
}

func (s *VisualizationService) PublishPVData(_ context.Context, req *pb.PVData) (*pb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	validity, err := providerValidity(req.Sample)
	if err != nil {
		return nil, err
	}
	if validity.Invalid {
		if s.vapd == nil {
			return nil, status.Error(codes.Unavailable, "VAPD PV provider not enabled")
		}
		if err := s.vapd.PublishPVSnapshot(usecases.PVSnapshot{Validity: validity}); err != nil {
			return nil, mapUsecaseError("invalidating PV data", err, standardUsecaseErrorClasses)
		}
		return &pb.Empty{}, nil
	}
	// PV production and its yield/peak figures are non-negative by definition.
	// Reject bad input before touching the provider.
	if req.PowerW == nil {
		return nil, status.Error(codes.InvalidArgument, "PV power is required")
	}
	if err := nonNegative("PV power", *req.PowerW); err != nil {
		return nil, err
	}
	if req.YieldWh != nil {
		if err := nonNegative("PV yield energy", *req.YieldWh); err != nil {
			return nil, err
		}
	}
	if req.PeakPowerW != nil {
		return nil, status.Error(codes.InvalidArgument, "PV peak_power_w must be published via PublishPVPeakPower")
	}
	if s.vapd == nil {
		return nil, status.Error(codes.Unavailable, "VAPD PV provider not enabled")
	}
	if err := s.vapd.PublishPVSnapshot(usecases.PVSnapshot{
		PowerW:   *req.PowerW,
		YieldWh:  req.YieldWh,
		Validity: validity,
	}); err != nil {
		return nil, mapUsecaseError("publishing PV data", err, standardUsecaseErrorClasses)
	}
	return &pb.Empty{}, nil
}

func (s *VisualizationService) PublishPVPeakPower(_ context.Context, req *pb.PVPeakPowerData) (*pb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if err := nonNegative("PV peak power", req.PeakPowerW); err != nil {
		return nil, err
	}
	if s.vapd == nil {
		return nil, status.Error(codes.Unavailable, "VAPD PV provider not enabled")
	}
	if err := s.vapd.PublishPeakPower(req.PeakPowerW); err != nil {
		return nil, mapUsecaseError("publishing PV peak power", err, standardUsecaseErrorClasses)
	}
	return &pb.Empty{}, nil
}

func (s *VisualizationService) PublishBatteryData(_ context.Context, req *pb.BatteryData) (*pb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	validity, err := providerValidity(req.Sample)
	if err != nil {
		return nil, err
	}
	if validity.Invalid {
		if s.vabd == nil {
			return nil, status.Error(codes.Unavailable, "VABD battery provider not enabled")
		}
		if err := s.vabd.PublishBatterySnapshot(usecases.BatterySnapshot{Validity: validity}); err != nil {
			return nil, mapUsecaseError("invalidating battery data", err, standardUsecaseErrorClasses)
		}
		return &pb.Empty{}, nil
	}
	// Battery PowerW is signed (negative = charging), so any finite value is
	// valid; the energy totals are non-negative counters and SoC is a percentage.
	// Reject bad input before touching the provider.
	if req.PowerW == nil {
		return nil, status.Error(codes.InvalidArgument, "battery power is required")
	}
	if err := finite("battery power", *req.PowerW); err != nil {
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
	if err := s.vabd.PublishBatterySnapshot(usecases.BatterySnapshot{
		PowerW:           *req.PowerW,
		ChargedWh:        req.ChargedWh,
		DischargedWh:     req.DischargedWh,
		StateOfChargePct: req.StateOfChargePct,
		Validity:         validity,
	}); err != nil {
		return nil, mapUsecaseError("publishing battery data", err, standardUsecaseErrorClasses)
	}
	return &pb.Empty{}, nil
}
