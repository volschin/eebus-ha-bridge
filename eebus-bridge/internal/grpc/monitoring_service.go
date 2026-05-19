package grpc

import (
	"context"
	"fmt"
	"log"

	spineapi "github.com/enbility/spine-go/api"
	"google.golang.org/protobuf/types/known/timestamppb"

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
	registry   *eebus.DeviceRegistry
}

func NewMonitoringService(monitoring *usecases.MonitoringWrapper, bus *eebus.EventBus, registry *eebus.DeviceRegistry) *MonitoringService {
	return &MonitoringService{monitoring: monitoring, bus: bus, registry: registry}
}

func (s *MonitoringService) GetPowerConsumption(_ context.Context, req *pb.DeviceRequest) (*pb.PowerMeasurement, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if s.monitoring == nil {
		return nil, status.Error(codes.Unavailable, "monitoring use case not initialized")
	}
	value, err := s.readPower(req.Ski)
	if err != nil {
		log.Printf("[DEBUG] Monitoring.GetPowerConsumption read failed: requested_ski=%s err=%v", req.Ski, err)
		if status.Code(err) != codes.Unknown {
			return nil, err
		}
		return nil, status.Errorf(codes.Internal, "reading power: %v", err)
	}
	log.Printf("[DEBUG] Monitoring.GetPowerConsumption success: requested_ski=%s watts=%f", req.Ski, value)
	return &pb.PowerMeasurement{
		Watts:     value,
		Timestamp: timestamppb.Now(),
	}, nil
}

func (s *MonitoringService) GetEnergyConsumed(_ context.Context, req *pb.DeviceRequest) (*pb.EnergyMeasurement, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if s.monitoring == nil {
		return nil, status.Error(codes.Unavailable, "monitoring use case not initialized")
	}
	value, err := s.readEnergyConsumed(req.Ski)
	if err != nil {
		log.Printf("[DEBUG] Monitoring.GetEnergyConsumed read failed: requested_ski=%s err=%v", req.Ski, err)
		if status.Code(err) != codes.Unknown {
			return nil, err
		}
		return nil, status.Errorf(codes.Internal, "reading energy: %v", err)
	}
	log.Printf("[DEBUG] Monitoring.GetEnergyConsumed success: requested_ski=%s kWh=%f", req.Ski, value)
	return &pb.EnergyMeasurement{
		KilowattHours: value,
		Timestamp:     timestamppb.Now(),
	}, nil
}

func (s *MonitoringService) GetMeasurements(_ context.Context, req *pb.DeviceRequest) (*pb.MeasurementList, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if s.monitoring == nil {
		return nil, status.Error(codes.Unavailable, "monitoring use case not initialized")
	}
	now := timestamppb.Now()
	measurements := make([]*pb.MeasurementEntry, 0, 2)

	if value, err := s.readPower(req.Ski); err == nil {
		measurements = append(measurements, &pb.MeasurementEntry{
			Type:      "power_consumption",
			Value:     value,
			Unit:      "W",
			Timestamp: now,
		})
	}

	if value, err := s.readEnergyConsumed(req.Ski); err == nil {
		measurements = append(measurements, &pb.MeasurementEntry{
			Type:      "energy_consumed",
			Value:     value,
			Unit:      "kWh",
			Timestamp: now,
		})
	}

	if len(measurements) == 0 {
		log.Printf("[DEBUG] Monitoring.GetMeasurements produced no entries: requested_ski=%s", req.Ski)
		return nil, status.Error(codes.NotFound, "no monitoring measurements available for device")
	}

	log.Printf("[DEBUG] Monitoring.GetMeasurements success: requested_ski=%s entries=%d", req.Ski, len(measurements))
	return &pb.MeasurementList{Measurements: measurements}, nil
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

func (s *MonitoringService) resolveEntity(ski string) (spineapi.EntityRemoteInterface, error) {
	if s.registry == nil {
		return nil, status.Error(codes.Unavailable, "device registry not initialized")
	}
	entity := s.registry.FirstEntity(ski)
	if entity == nil {
		log.Printf("[DEBUG] Monitoring.resolveEntity no entity for requested SKI: requested_ski=%s", ski)
	}
	if entity == nil && ski == "" {
		entity = s.registry.FirstAvailableEntity()
		if entity != nil {
			log.Printf("[DEBUG] Monitoring.resolveEntity selected fallback entity for empty SKI request")
		}
	}
	if entity == nil {
		log.Printf("[DEBUG] Monitoring.resolveEntity returning not found: requested_ski=%s", ski)
		return nil, status.Errorf(codes.NotFound, "no remote entity found for ski %s", ski)
	}
	return entity, nil
}

func (s *MonitoringService) readPower(ski string) (float64, error) {
	entity, err := s.resolveEntity(ski)
	if err == nil {
		return s.monitoring.Power(entity)
	}
	if status.Code(err) != codes.NotFound {
		log.Printf("[DEBUG] Monitoring.readPower resolveEntity failed without fallback: requested_ski=%s err=%v", ski, err)
		return 0, err
	}
	log.Printf("[DEBUG] Monitoring.readPower attempting nil-entity fallback: requested_ski=%s", ski)
	value, fallbackErr := s.safePowerNilEntity()
	if fallbackErr != nil {
		log.Printf("[DEBUG] Monitoring.readPower nil-entity fallback failed: requested_ski=%s err=%v", ski, fallbackErr)
		return 0, err
	}
	log.Printf("[DEBUG] Monitoring.readPower nil-entity fallback succeeded: requested_ski=%s watts=%f", ski, value)
	return value, nil
}

func (s *MonitoringService) readEnergyConsumed(ski string) (float64, error) {
	entity, err := s.resolveEntity(ski)
	if err == nil {
		return s.monitoring.EnergyConsumed(entity)
	}
	if status.Code(err) != codes.NotFound {
		log.Printf("[DEBUG] Monitoring.readEnergyConsumed resolveEntity failed without fallback: requested_ski=%s err=%v", ski, err)
		return 0, err
	}
	log.Printf("[DEBUG] Monitoring.readEnergyConsumed attempting nil-entity fallback: requested_ski=%s", ski)
	value, fallbackErr := s.safeEnergyConsumedNilEntity()
	if fallbackErr != nil {
		log.Printf("[DEBUG] Monitoring.readEnergyConsumed nil-entity fallback failed: requested_ski=%s err=%v", ski, fallbackErr)
		return 0, err
	}
	log.Printf("[DEBUG] Monitoring.readEnergyConsumed nil-entity fallback succeeded: requested_ski=%s kWh=%f", ski, value)
	return value, nil
}

func (s *MonitoringService) safePowerNilEntity() (value float64, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic during nil-entity power read: %v", recovered)
		}
	}()
	return s.monitoring.Power(nil)
}

func (s *MonitoringService) safeEnergyConsumedNilEntity() (value float64, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic during nil-entity energy read: %v", recovered)
		}
	}()
	return s.monitoring.EnergyConsumed(nil)
}
