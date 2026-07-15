package grpc

import (
	"context"
	"errors"
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
	monitoring  *usecases.MonitoringWrapper
	dhw         temperatureReader
	room        temperatureReader
	outdoor     temperatureReader
	flow        temperatureReader
	returnTemp  temperatureReader
	diagnostics deviceOperatingStateReader
	bus         *eebus.EventBus
	registry    *eebus.DeviceRegistry
}

type temperatureReader interface {
	Temperature(string) (float64, error)
}

type deviceOperatingStateReader interface {
	OperatingState(string) (string, error)
	CachedOperatingState(string) (string, error)
}

// MonitoringReaders bundles the optional per-reading dependencies of the
// MonitoringService; leave a field nil when the reading is unsupported.
type MonitoringReaders struct {
	DHW         temperatureReader
	Room        temperatureReader
	Outdoor     temperatureReader
	Flow        temperatureReader
	Return      temperatureReader
	Diagnostics deviceOperatingStateReader
}

func NewMonitoringService(
	monitoring *usecases.MonitoringWrapper,
	readers MonitoringReaders,
	bus *eebus.EventBus,
	registry *eebus.DeviceRegistry,
) *MonitoringService {
	return &MonitoringService{
		monitoring:  monitoring,
		dhw:         readers.DHW,
		room:        readers.Room,
		outdoor:     readers.Outdoor,
		flow:        readers.Flow,
		returnTemp:  readers.Return,
		diagnostics: readers.Diagnostics,
		bus:         bus,
		registry:    registry,
	}
}

func (s *MonitoringService) GetDeviceDiagnostics(_ context.Context, req *pb.DeviceRequest) (*pb.DeviceDiagnosticsData, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if s.diagnostics == nil {
		return nil, status.Error(codes.Unavailable, "device diagnosis reader not initialized")
	}
	state, err := s.diagnostics.OperatingState(req.Ski)
	if err != nil {
		if errors.Is(err, usecases.ErrDeviceOperatingStateUnavailable) {
			return nil, status.Errorf(codes.NotFound, "reading device operating state: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "reading device operating state: %v", err)
	}
	return &pb.DeviceDiagnosticsData{
		OperatingState: state,
		Timestamp:      timestamppb.Now(),
	}, nil
}

func (s *MonitoringService) GetPowerConsumption(_ context.Context, req *pb.DeviceRequest) (*pb.PowerMeasurement, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if s.monitoring == nil {
		return nil, status.Error(codes.Unavailable, "monitoring use case not initialized")
	}
	value, err := readMetric("power", s.resolveForRead(req.Ski), s.monitoring.Power)
	if err != nil {
		log.Printf("[DEBUG] Monitoring.GetPowerConsumption read failed: requested_ski=%s err=%v", req.Ski, err)
		if status.Code(err) != codes.Unknown {
			return nil, err
		}
		return nil, status.Errorf(codes.Internal, "reading power: %v", err)
	}
	log.Printf("[DEBUG] Monitoring.GetPowerConsumption success: requested_ski=%s watts=%g", req.Ski, value)
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
	value, err := readMetric("energy-consumed", s.resolveForRead(req.Ski), s.monitoring.EnergyConsumed)
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
	resolved := s.resolveForRead(req.Ski)

	if value, err := readMetric("power", resolved, s.monitoring.Power); err == nil {
		appendMeasurement(&measurements, now, "power_consumption", value, "W")
	}

	if values, err := readMetric("power-per-phase", resolved, s.monitoring.PowerPerPhase); err == nil {
		for idx, value := range values {
			appendMeasurement(&measurements, now, fmt.Sprintf("power_l%d", idx+1), value, "W")
		}
	}

	if values, err := readMetric("current-per-phase", resolved, s.monitoring.CurrentPerPhase); err == nil {
		for idx, value := range values {
			appendMeasurement(&measurements, now, fmt.Sprintf("current_l%d", idx+1), value, "A")
		}
	}

	if values, err := readMetric("voltage-per-phase", resolved, s.monitoring.VoltagePerPhase); err == nil {
		for idx, value := range values {
			appendMeasurement(&measurements, now, fmt.Sprintf("voltage_l%d", idx+1), value, "V")
		}
	}

	if value, err := readMetric("frequency", resolved, s.monitoring.Frequency); err == nil {
		appendMeasurement(&measurements, now, "frequency", value, "Hz")
	}

	if value, err := readMetric("energy-consumed", resolved, s.monitoring.EnergyConsumed); err == nil {
		appendMeasurement(&measurements, now, "energy_consumed", value, "kWh")
	}

	if value, err := readMetric("energy-produced", resolved, s.monitoring.EnergyProduced); err == nil {
		appendMeasurement(&measurements, now, "energy_produced", value, "kWh")
	}

	if s.dhw != nil {
		if value, err := s.dhw.Temperature(req.Ski); err == nil {
			appendMeasurement(&measurements, now, "dhw_temperature", value, "degC")
		}
	}
	if s.room != nil {
		if value, err := s.room.Temperature(req.Ski); err == nil {
			appendMeasurement(&measurements, now, "room_temperature", value, "degC")
		}
	}
	if s.outdoor != nil {
		if value, err := s.outdoor.Temperature(req.Ski); err == nil {
			appendMeasurement(&measurements, now, "outdoor_temperature", value, "degC")
		}
	}
	if s.flow != nil {
		if value, err := s.flow.Temperature(req.Ski); err == nil {
			appendMeasurement(&measurements, now, "flow_temperature", value, "degC")
		}
	}
	if s.returnTemp != nil {
		if value, err := s.returnTemp.Temperature(req.Ski); err == nil {
			appendMeasurement(&measurements, now, "return_temperature", value, "degC")
		}
	}

	if s.monitoring != nil {
		values, err := s.monitoring.GenericMeasurements(req.Ski)
		if err != nil {
			values = nil
		}
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
			case "dhw.temperature_updated":
				eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_DHW_TEMPERATURE_UPDATED
			case "dhw.monitoring_support_updated":
				eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_DHW_TEMPERATURE_SUPPORT_UPDATED
			case "room.temperature_updated":
				eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_ROOM_TEMPERATURE_UPDATED
			case "room.monitoring_support_updated":
				eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_ROOM_TEMPERATURE_SUPPORT_UPDATED
			case "outdoor.temperature_updated":
				eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_UPDATED
			case "outdoor.monitoring_support_updated":
				eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_SUPPORT_UPDATED
			case "monitoring.flow_temperature_updated":
				eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_FLOW_TEMPERATURE_UPDATED
			case "monitoring.return_temperature_updated":
				eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_RETURN_TEMPERATURE_UPDATED
			case "monitoring.device_operating_state_updated":
				eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_DEVICE_OPERATING_STATE_UPDATED
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
	switch eventType {
	case pb.MeasurementEventType_MEASUREMENT_EVENT_POWER_UPDATED:
		if value, err := readMetric("power", s.resolveForRead(ski), s.monitoring.Power); err == nil {
			event.Data = &pb.MeasurementEvent_Power{Power: &pb.PowerMeasurement{
				Watts:     value,
				Timestamp: timestamppb.Now(),
			}}
		}
	case pb.MeasurementEventType_MEASUREMENT_EVENT_ENERGY_UPDATED:
		if value, err := readMetric("energy-consumed", s.resolveForRead(ski), s.monitoring.EnergyConsumed); err == nil {
			event.Data = &pb.MeasurementEvent_Energy{Energy: &pb.EnergyMeasurement{
				KilowattHours: value,
				Timestamp:     timestamppb.Now(),
			}}
		}
	case pb.MeasurementEventType_MEASUREMENT_EVENT_DHW_TEMPERATURE_UPDATED:
		s.attachTemperaturePayload(event, ski, s.dhw, "dhw_temperature")
	case pb.MeasurementEventType_MEASUREMENT_EVENT_ROOM_TEMPERATURE_UPDATED:
		s.attachTemperaturePayload(event, ski, s.room, "room_temperature")
	case pb.MeasurementEventType_MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_UPDATED:
		s.attachTemperaturePayload(event, ski, s.outdoor, "outdoor_temperature")
	case pb.MeasurementEventType_MEASUREMENT_EVENT_FLOW_TEMPERATURE_UPDATED:
		s.attachTemperaturePayload(event, ski, s.flow, "flow_temperature")
	case pb.MeasurementEventType_MEASUREMENT_EVENT_RETURN_TEMPERATURE_UPDATED:
		s.attachTemperaturePayload(event, ski, s.returnTemp, "return_temperature")
	case pb.MeasurementEventType_MEASUREMENT_EVENT_DEVICE_OPERATING_STATE_UPDATED:
		if s.diagnostics == nil {
			return
		}
		// The event fired because the SPINE cache was just updated, so a cache
		// read is fresh; an active read here would block the send loop.
		if state, err := s.diagnostics.CachedOperatingState(ski); err == nil {
			event.Data = &pb.MeasurementEvent_DeviceDiagnostics{DeviceDiagnostics: &pb.DeviceDiagnosticsData{
				OperatingState: state,
				Timestamp:      timestamppb.Now(),
			}}
		}
	}
}

func (s *MonitoringService) attachTemperaturePayload(
	event *pb.MeasurementEvent,
	ski string,
	reader temperatureReader,
	measurementType string,
) {
	if reader == nil {
		return
	}
	if value, err := reader.Temperature(ski); err == nil {
		event.Data = &pb.MeasurementEvent_Measurement{Measurement: &pb.MeasurementEntry{
			Type:      measurementType,
			Value:     value,
			Unit:      "degC",
			Timestamp: timestamppb.Now(),
		}}
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

// resolvedEntity carries the outcome of resolveEntity so multi-metric callers
// like GetMeasurements resolve once and share the result across all reads.
type resolvedEntity struct {
	ski    string
	entity spineapi.EntityRemoteInterface
	err    error
}

func (s *MonitoringService) resolveForRead(ski string) resolvedEntity {
	entity, err := s.resolveEntity(ski)
	return resolvedEntity{ski: ski, entity: entity, err: err}
}

// readMetric reads one measurement through the monitoring use case. Reads are
// best-effort: GetMeasurements only appends a result when the read succeeds,
// so a device that does not advertise a given measurement simply omits it.
// When the SKI did not resolve, the read is retried once with a nil entity
// (guarded against panics inside eebus-go), matching the behaviour of the
// former per-metric readers.
func readMetric[T any](label string, r resolvedEntity, read func(spineapi.EntityRemoteInterface) (T, error)) (T, error) {
	var zero T
	if r.err == nil {
		return read(r.entity)
	}
	if status.Code(r.err) != codes.NotFound {
		log.Printf("[DEBUG] Monitoring.readMetric %s resolveEntity failed without fallback: requested_ski=%s err=%v", label, r.ski, r.err)
		return zero, r.err
	}
	log.Printf("[DEBUG] Monitoring.readMetric %s attempting nil-entity fallback: requested_ski=%s", label, r.ski)
	value, fallbackErr := safeRead(label, func() (T, error) { return read(nil) })
	if fallbackErr != nil {
		log.Printf("[DEBUG] Monitoring.readMetric %s nil-entity fallback failed: requested_ski=%s err=%v", label, r.ski, fallbackErr)
		return zero, r.err
	}
	log.Printf("[DEBUG] Monitoring.readMetric %s nil-entity fallback succeeded: requested_ski=%s", label, r.ski)
	return value, nil
}

// safeRead guards a nil-entity read against panics inside eebus-go.
func safeRead[T any](label string, read func() (T, error)) (value T, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic during nil-entity %s read: %v", label, recovered)
		}
	}()
	return read()
}

func appendMeasurement(measurements *[]*pb.MeasurementEntry, now *timestamppb.Timestamp, measurementType string, value float64, unit string) {
	*measurements = append(*measurements, &pb.MeasurementEntry{
		Type:      measurementType,
		Value:     value,
		Unit:      unit,
		Timestamp: now,
	})
}
