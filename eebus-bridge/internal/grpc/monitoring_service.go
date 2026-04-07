package grpc

import (
	"context"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type MonitoringService struct {
	pb.UnimplementedMonitoringServiceServer
	monitoring *usecases.MonitoringWrapper
	bus        *eebus.EventBus
}

func NewMonitoringService(monitoring *usecases.MonitoringWrapper, bus *eebus.EventBus) *MonitoringService {
	return &MonitoringService{monitoring: monitoring, bus: bus}
}

func (s *MonitoringService) GetPowerConsumption(_ context.Context, _ *pb.DeviceRequest) (*pb.PowerMeasurement, error) {
	if s.monitoring == nil {
		return nil, status.Error(codes.Unavailable, "monitoring use case not initialized")
	}
	return nil, status.Error(codes.Unimplemented, "entity lookup not yet wired")
}

func (s *MonitoringService) GetEnergyConsumed(_ context.Context, _ *pb.DeviceRequest) (*pb.EnergyMeasurement, error) {
	if s.monitoring == nil {
		return nil, status.Error(codes.Unavailable, "monitoring use case not initialized")
	}
	return nil, status.Error(codes.Unimplemented, "entity lookup not yet wired")
}

func (s *MonitoringService) GetMeasurements(_ context.Context, _ *pb.DeviceRequest) (*pb.MeasurementList, error) {
	if s.monitoring == nil {
		return nil, status.Error(codes.Unavailable, "monitoring use case not initialized")
	}
	return nil, status.Error(codes.Unimplemented, "entity lookup not yet wired")
}

func (s *MonitoringService) SubscribeMeasurements(req *pb.DeviceRequest, stream pb.MonitoringService_SubscribeMeasurementsServer) error {
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
			var eventType pb.MeasurementEventType
			switch evt.Type {
			case "monitoring.power_updated":
				eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_POWER_UPDATED
			case "monitoring.energy_consumed_updated":
				eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_ENERGY_UPDATED
			default:
				continue
			}
			if err := stream.Send(&pb.MeasurementEvent{
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
