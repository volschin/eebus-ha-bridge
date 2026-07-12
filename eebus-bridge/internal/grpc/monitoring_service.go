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
	measurements := make([]*pb.MeasurementEntry, 0, 12)

	if value, err := s.readPower(req.Ski); err == nil {
		appendMeasurement(&measurements, now, "power_consumption", value, "W")
	}

	if values, err := s.readPowerPerPhase(req.Ski); err == nil {
		for idx, value := range values {
			appendMeasurement(&measurements, now, fmt.Sprintf("power_l%d", idx+1), value, "W")
		}
	}

	if values, err := s.readCurrentPerPhase(req.Ski); err == nil {
		for idx, value := range values {
			appendMeasurement(&measurements, now, fmt.Sprintf("current_l%d", idx+1), value, "A")
		}
	}

	if values, err := s.readVoltagePerPhase(req.Ski); err == nil {
		for idx, value := range values {
			appendMeasurement(&measurements, now, fmt.Sprintf("voltage_l%d", idx+1), value, "V")
		}
	}

	if value, err := s.readFrequency(req.Ski); err == nil {
		appendMeasurement(&measurements, now, "frequency", value, "Hz")
	}

	if value, err := s.readEnergyConsumed(req.Ski); err == nil {
		appendMeasurement(&measurements, now, "energy_consumed", value, "kWh")
	}

	if value, err := s.readEnergyProduced(req.Ski); err == nil {
		appendMeasurement(&measurements, now, "energy_produced", value, "kWh")
	}

	if values, err := s.monitoring.GenericMeasurements(req.Ski); err == nil {
		seen := make(map[string]struct{}, len(measurements)+len(values))
		for _, measurement := range measurements {
			seen[measurement.Type] = struct{}{}
		}
		for _, value := range values {
			if _, ok := seen[value.Type]; ok {
				continue
			}
			appendMeasurement(&measurements, now, value.Type, value.Value, value.Unit)
			seen[value.Type] = struct{}{}
		}
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
			event := &pb.MeasurementEvent{Ski: evt.SKI, EventType: eventType}
			s.attachMeasurementPayload(event, evt.SKI, eventType)
			if err := stream.Send(event); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

// attachMeasurementPayload best-effort fills the event's typed payload with the
// current value so subscribers receive data directly instead of polling. On any
// read failure the event is sent without a payload and the client falls back to
// a refresh. Reuses the SKI-resolve + nil-entity fallback of the Get* readers.
func (s *MonitoringService) attachMeasurementPayload(event *pb.MeasurementEvent, ski string, eventType pb.MeasurementEventType) {
	if s.monitoring == nil {
		return
	}
	switch eventType {
	case pb.MeasurementEventType_MEASUREMENT_EVENT_POWER_UPDATED:
		if value, err := s.readPower(ski); err == nil {
			event.Data = &pb.MeasurementEvent_Power{Power: &pb.PowerMeasurement{
				Watts:     value,
				Timestamp: timestamppb.Now(),
			}}
		}
	case pb.MeasurementEventType_MEASUREMENT_EVENT_ENERGY_UPDATED:
		if value, err := s.readEnergyConsumed(ski); err == nil {
			event.Data = &pb.MeasurementEvent_Energy{Energy: &pb.EnergyMeasurement{
				KilowattHours: value,
				Timestamp:     timestamppb.Now(),
			}}
		}
	}
}

func (s *MonitoringService) resolveEntity(ski string) (spineapi.EntityRemoteInterface, error) {
	if s.monitoring != nil {
		if entity := s.monitoring.CompatibleEntity(ski); entity != nil {
			if s.registry != nil {
				s.registry.RecordMonitoringSuccess()
			}
			return entity, nil
		}
	}
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

// The following readers expose the additional MPC measurements (per-phase
// power/current/voltage, grid frequency and produced energy). They are
// best-effort: GetMeasurements only appends their results when the read
// succeeds, so a device that does not advertise a given measurement simply
// omits it rather than failing the whole call. Each falls back to a nil-entity
// read (guarded against panics) when the SKI cannot be resolved, matching the
// behaviour of readPower / readEnergyConsumed.

func (s *MonitoringService) readEnergyProduced(ski string) (float64, error) {
	entity, err := s.resolveEntity(ski)
	if err == nil {
		return s.monitoring.EnergyProduced(entity)
	}
	if status.Code(err) != codes.NotFound {
		return 0, err
	}
	value, fallbackErr := s.safeEnergyProducedNilEntity()
	if fallbackErr != nil {
		return 0, err
	}
	return value, nil
}

func (s *MonitoringService) readPowerPerPhase(ski string) ([]float64, error) {
	entity, err := s.resolveEntity(ski)
	if err == nil {
		return s.monitoring.PowerPerPhase(entity)
	}
	if status.Code(err) != codes.NotFound {
		return nil, err
	}
	values, fallbackErr := s.safePowerPerPhaseNilEntity()
	if fallbackErr != nil {
		return nil, err
	}
	return values, nil
}

func (s *MonitoringService) readCurrentPerPhase(ski string) ([]float64, error) {
	entity, err := s.resolveEntity(ski)
	if err == nil {
		return s.monitoring.CurrentPerPhase(entity)
	}
	if status.Code(err) != codes.NotFound {
		return nil, err
	}
	values, fallbackErr := s.safeCurrentPerPhaseNilEntity()
	if fallbackErr != nil {
		return nil, err
	}
	return values, nil
}

func (s *MonitoringService) readVoltagePerPhase(ski string) ([]float64, error) {
	entity, err := s.resolveEntity(ski)
	if err == nil {
		return s.monitoring.VoltagePerPhase(entity)
	}
	if status.Code(err) != codes.NotFound {
		return nil, err
	}
	values, fallbackErr := s.safeVoltagePerPhaseNilEntity()
	if fallbackErr != nil {
		return nil, err
	}
	return values, nil
}

func (s *MonitoringService) readFrequency(ski string) (float64, error) {
	entity, err := s.resolveEntity(ski)
	if err == nil {
		return s.monitoring.Frequency(entity)
	}
	if status.Code(err) != codes.NotFound {
		return 0, err
	}
	value, fallbackErr := s.safeFrequencyNilEntity()
	if fallbackErr != nil {
		return 0, err
	}
	return value, nil
}

func appendMeasurement(measurements *[]*pb.MeasurementEntry, now *timestamppb.Timestamp, measurementType string, value float64, unit string) {
	*measurements = append(*measurements, &pb.MeasurementEntry{
		Type:      measurementType,
		Value:     value,
		Unit:      unit,
		Timestamp: now,
	})
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
