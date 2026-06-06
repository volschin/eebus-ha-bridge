package grpc

import (
	"context"
	"fmt"
	"log"
	"strings"

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
	monitoring       *usecases.MonitoringWrapper
	bus              *eebus.EventBus
	registry         *eebus.DeviceRegistry
	debugProtocol    bool
}

func NewMonitoringService(monitoring *usecases.MonitoringWrapper, bus *eebus.EventBus, registry *eebus.DeviceRegistry, debugProtocol bool) *MonitoringService {
	return &MonitoringService{monitoring: monitoring, bus: bus, registry: registry, debugProtocol: debugProtocol}
}

func (s *MonitoringService) GetPowerConsumption(_ context.Context, req *pb.DeviceRequest) (*pb.PowerMeasurement, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if req.Ski == "" {
		return nil, status.Error(codes.InvalidArgument, "device SKI is required (empty SKI provided)")
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
	if s.debugProtocol {
		log.Printf("[PROTOCOL] Bosch power measurement (SKI=%s): %.2f W", req.Ski, value)
	}
	return &pb.PowerMeasurement{
		Watts:     value,
		Timestamp: timestamppb.Now(),
	}, nil
}

func (s *MonitoringService) GetEnergyConsumed(_ context.Context, req *pb.DeviceRequest) (*pb.EnergyMeasurement, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if req.Ski == "" {
		return nil, status.Error(codes.InvalidArgument, "device SKI is required (empty SKI provided)")
	}
	if s.monitoring == nil {
		return nil, status.Error(codes.Unavailable, "monitoring use case not initialized")
	}
	value, err := s.readEnergyConsumed(req.Ski)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, err
		}
		log.Printf("[DEBUG] Monitoring.GetEnergyConsumed read failed: requested_ski=%s err=%v", req.Ski, err)
		if status.Code(err) != codes.Unknown {
			return nil, err
		}
		return nil, status.Errorf(codes.Internal, "reading energy: %v", err)
	}
	log.Printf("[DEBUG] Monitoring.GetEnergyConsumed success: requested_ski=%s kWh=%f", req.Ski, value)
	if s.debugProtocol {
		log.Printf("[PROTOCOL] Bosch energy consumed (SKI=%s): %.2f kWh", req.Ski, value)
	}
	return &pb.EnergyMeasurement{
		KilowattHours: value,
		Timestamp:     timestamppb.Now(),
	}, nil
}

func (s *MonitoringService) GetMeasurements(_ context.Context, req *pb.DeviceRequest) (*pb.MeasurementList, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if req.Ski == "" {
		return nil, status.Error(codes.InvalidArgument, "device SKI is required (empty SKI provided)")
	}
	if s.monitoring == nil {
		return nil, status.Error(codes.Unavailable, "monitoring use case not initialized")
	}
	now := timestamppb.Now()
	measurements := make([]*pb.MeasurementEntry, 0, 12)

	if value, err := s.readPower(req.Ski); err == nil {
		appendMeasurement(&measurements, now, "power_consumption", value, "W")
	}

	if values, err := s.readPowerPerPhase(req.Ski); err == nil {
		for idx, value := range values {
			appendMeasurement(&measurements, now, fmt.Sprintf("power_l%d", idx+1), value, "W")
		}
	} else {
		log.Printf("[DEBUG] Monitoring.GetMeasurements readPowerPerPhase failed: requested_ski=%s err=%v", req.Ski, err)
	}

	if values, err := s.readCurrentPerPhase(req.Ski); err == nil {
		for idx, value := range values {
			appendMeasurement(&measurements, now, fmt.Sprintf("current_l%d", idx+1), value, "A")
		}
	} else {
		log.Printf("[DEBUG] Monitoring.GetMeasurements readCurrentPerPhase failed: requested_ski=%s err=%v", req.Ski, err)
	}

	if values, err := s.readVoltagePerPhase(req.Ski); err == nil {
		for idx, value := range values {
			appendMeasurement(&measurements, now, fmt.Sprintf("voltage_l%d", idx+1), value, "V")
		}
	} else {
		log.Printf("[DEBUG] Monitoring.GetMeasurements readVoltagePerPhase failed: requested_ski=%s err=%v", req.Ski, err)
	}

	if value, err := s.readFrequency(req.Ski); err == nil {
		appendMeasurement(&measurements, now, "frequency", value, "Hz")
	} else {
		log.Printf("[DEBUG] Monitoring.GetMeasurements readFrequency failed: requested_ski=%s err=%v", req.Ski, err)
	}

	if value, err := s.readEnergyConsumed(req.Ski); err == nil {
		appendMeasurement(&measurements, now, "energy_consumed", value, "kWh")
	}

	if value, err := s.readEnergyProduced(req.Ski); err == nil {
		appendMeasurement(&measurements, now, "energy_produced", value, "kWh")
	}

	if len(measurements) == 0 {
		log.Printf("[DEBUG] Monitoring.GetMeasurements produced no entries: requested_ski=%s", req.Ski)
		return nil, status.Error(codes.NotFound, "no monitoring measurements available for device")
	}

	log.Printf("[DEBUG] Monitoring.GetMeasurements success: requested_ski=%s entries=%d", req.Ski, len(measurements))

	if s.debugProtocol {
		log.Printf("[PROTOCOL] Bosch device measurements (SKI=%s):", req.Ski)
		for _, m := range measurements {
			log.Printf("  [PROTOCOL]   %s = %.2f %s", m.Type, m.Value, m.Unit)
		}
	}

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
	ski = eebus.NormalizeSKI(ski)
	entity := s.registry.FirstEntity(ski)
	if entity == nil {
		log.Printf("[DEBUG] Monitoring.resolveEntity no entity for requested SKI: requested_ski=%s", ski)
		return nil, status.Errorf(codes.NotFound, "no remote entity found for ski %s", ski)
	}
	return entity, nil
}

func (s *MonitoringService) readPower(ski string) (float64, error) {
	entity, err := s.resolveEntity(ski)
	if err != nil {
		return 0, err
	}
	return s.monitoring.Power(entity)
}

func (s *MonitoringService) readEnergyConsumed(ski string) (float64, error) {
	entity, err := s.resolveEntity(ski)
	if err != nil {
		return 0, err
	}
	value, readErr := s.monitoring.EnergyConsumed(entity)
	if readErr != nil && isDataUnavailableErr(readErr) {
		return 0, status.Error(codes.NotFound, "consumed energy not available for device")
	}
	return value, readErr
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

func (s *MonitoringService) readEnergyProduced(ski string) (float64, error) {
	entity, err := s.resolveEntity(ski)
	if err != nil {
		return 0, err
	}
	value, readErr := s.monitoring.EnergyProduced(entity)
	if readErr != nil && isDataUnavailableErr(readErr) {
		return 0, status.Error(codes.NotFound, "produced energy not available for device")
	}
	return value, readErr
}

func (s *MonitoringService) readPowerPerPhase(ski string) ([]float64, error) {
	entity, err := s.resolveEntity(ski)
	if err != nil {
		return nil, err
	}
	values, readErr := s.monitoring.PowerPerPhase(entity)
	if readErr != nil {
		log.Printf("[DEBUG] Monitoring.readPowerPerPhase entity read failed: requested_ski=%s err=%v", ski, readErr)
		return nil, readErr
	}
	if len(values) == 0 {
		log.Printf("[DEBUG] Monitoring.readPowerPerPhase entity returned empty: requested_ski=%s", ski)
	}
	return values, nil
}

func (s *MonitoringService) readCurrentPerPhase(ski string) ([]float64, error) {
	entity, err := s.resolveEntity(ski)
	if err != nil {
		return nil, err
	}
	values, readErr := s.monitoring.CurrentPerPhase(entity)
	if readErr != nil {
		log.Printf("[DEBUG] Monitoring.readCurrentPerPhase entity read failed: requested_ski=%s err=%v", ski, readErr)
		return nil, readErr
	}
	if len(values) == 0 {
		log.Printf("[DEBUG] Monitoring.readCurrentPerPhase entity returned empty: requested_ski=%s", ski)
	}
	return values, nil
}

func (s *MonitoringService) readVoltagePerPhase(ski string) ([]float64, error) {
	entity, err := s.resolveEntity(ski)
	if err != nil {
		return nil, err
	}
	values, readErr := s.monitoring.VoltagePerPhase(entity)
	if readErr != nil {
		log.Printf("[DEBUG] Monitoring.readVoltagePerPhase entity read failed: requested_ski=%s err=%v", ski, readErr)
		return nil, readErr
	}
	if len(values) == 0 {
		log.Printf("[DEBUG] Monitoring.readVoltagePerPhase entity returned empty: requested_ski=%s", ski)
	}
	return values, nil
}

func (s *MonitoringService) readFrequency(ski string) (float64, error) {
	entity, err := s.resolveEntity(ski)
	if err != nil {
		return 0, err
	}
	return s.monitoring.Frequency(entity)
}

func (s *MonitoringService) safeEnergyProducedNilEntity() (value float64, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic during nil-entity produced energy read: %v", recovered)
		}
	}()
	return s.monitoring.EnergyProduced(nil)
}

func (s *MonitoringService) safePowerPerPhaseNilEntity() (values []float64, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic during nil-entity power-per-phase read: %v", recovered)
		}
	}()
	return s.monitoring.PowerPerPhase(nil)
}

func (s *MonitoringService) safeCurrentPerPhaseNilEntity() (values []float64, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic during nil-entity current-per-phase read: %v", recovered)
		}
	}()
	return s.monitoring.CurrentPerPhase(nil)
}

func (s *MonitoringService) safeVoltagePerPhaseNilEntity() (values []float64, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic during nil-entity voltage-per-phase read: %v", recovered)
		}
	}()
	return s.monitoring.VoltagePerPhase(nil)
}

func (s *MonitoringService) safeFrequencyNilEntity() (value float64, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic during nil-entity frequency read: %v", recovered)
		}
	}()
	return s.monitoring.Frequency(nil)
}

func appendMeasurement(measurements *[]*pb.MeasurementEntry, now *timestamppb.Timestamp, measurementType string, value float64, unit string) {
	*measurements = append(*measurements, &pb.MeasurementEntry{
		Type:      measurementType,
		Value:     value,
		Unit:      unit,
		Timestamp: now,
	})
}

func isDataUnavailableErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "data not available")
}
