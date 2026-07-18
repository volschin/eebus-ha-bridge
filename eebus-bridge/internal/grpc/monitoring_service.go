package grpc

import (
	"context"
	"errors"
	"fmt"

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
	monitoring  monitoringReader
	dhw         temperatureReader
	room        temperatureReader
	outdoor     temperatureReader
	flow        temperatureReader
	returnTemp  temperatureReader
	diagnostics deviceOperatingStateReader
	bus         *eebus.EventBus
	registry    *eebus.DeviceRegistry
	debug       bool
}

type monitoringReader interface {
	CompatibleEntity(string) eebus.EntityResolution
	Power(spineapi.EntityRemoteInterface) (float64, error)
	PowerPerPhase(spineapi.EntityRemoteInterface) ([]float64, error)
	EnergyConsumed(spineapi.EntityRemoteInterface) (float64, error)
	EnergyProduced(spineapi.EntityRemoteInterface) (float64, error)
	CurrentPerPhase(spineapi.EntityRemoteInterface) ([]float64, error)
	VoltagePerPhase(spineapi.EntityRemoteInterface) ([]float64, error)
	Frequency(spineapi.EntityRemoteInterface) (float64, error)
	GenericMeasurements(string) ([]usecases.GenericMeasurement, error)
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
	monitoring monitoringReader,
	readers MonitoringReaders,
	bus *eebus.EventBus,
	registry *eebus.DeviceRegistry,
	debug ...bool,
) *MonitoringService {
	debugEnabled := false
	if len(debug) > 0 {
		debugEnabled = debug[0]
	}
	// A nil *MonitoringWrapper narrowed into the interface would defeat the
	// nil guard in attachMeasurementPayload (typed-nil, the v0.13.0 crash class).
	if wrapper, ok := monitoring.(*usecases.MonitoringWrapper); ok && wrapper == nil {
		monitoring = nil
	}
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
		debug:       debugEnabled,
	}
}

// SnapshotDeviceDiagnostics actively reads the remote operating state for
// snapshot construction (the initial gRPC-stream snapshot and GetDeviceSnapshot
// polls), unlike attachMeasurementPayload's per-event cache-only read: no
// SPINE subscription pushes DeviceDiagnosisStateData on its own, so without an
// active read here the field never populates in the snapshot/stream
// architecture (cf. the retired legacy GetDeviceDiagnostics poll, which was
// the only remaining caller of the active OperatingState path before this).
// Safe to block briefly here: unlike the event send loop, both snapshot call
// sites run per-request/per-subscribe, not in a hot per-event loop.
func (s *MonitoringService) SnapshotDeviceDiagnostics(ski string) *pb.DeviceDiagnosticsData {
	if s.diagnostics == nil {
		return nil
	}
	state, err := s.diagnostics.OperatingState(ski)
	if err != nil {
		return nil
	}
	return &pb.DeviceDiagnosticsData{OperatingState: state, Timestamp: timestamppb.Now()}
}

func (s *MonitoringService) GetDeviceDiagnostics(_ context.Context, req *pb.DeviceRequest) (*pb.DeviceDiagnosticsData, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if _, err := normalizeReadSKI(req.Ski); err != nil {
		return nil, err
	}
	if s.diagnostics == nil {
		return nil, status.Error(codes.Unavailable, "device diagnosis reader not initialized")
	}
	state, err := s.diagnostics.OperatingState(req.Ski)
	if err != nil {
		return nil, mapUsecaseError("reading device operating state", err, usecaseErrorClasses{
			unavailable: []error{usecases.ErrDeviceOperatingStateUnavailable},
		})
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
	if _, err := normalizeReadSKI(req.Ski); err != nil {
		return nil, err
	}
	if s.monitoring == nil {
		return nil, status.Error(codes.Unavailable, "monitoring use case not initialized")
	}
	value, err := readMetric("power", s.resolveForRead(req.Ski), s.monitoring.Power)
	s.recordMonitoringRead(req.Ski, err)
	if err != nil {
		debugLogf(
			s.debug,
			"[DEBUG] Monitoring.GetPowerConsumption read failed: requested_ski=%s err_class=%s",
			redactedSKIForLog(req.Ski),
			redactedErrorForLog(err),
		)
		if status.Code(err) != codes.Unknown {
			return nil, err
		}
		return nil, mapUsecaseError("reading power", err, standardUsecaseErrorClasses)
	}
	debugLogf(s.debug, "[DEBUG] Monitoring.GetPowerConsumption success: requested_ski=%s watts=%g", redactedSKIForLog(req.Ski), value)
	return &pb.PowerMeasurement{
		Watts:     value,
		Timestamp: timestamppb.Now(),
	}, nil
}

func (s *MonitoringService) GetEnergyConsumed(_ context.Context, req *pb.DeviceRequest) (*pb.EnergyMeasurement, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if _, err := normalizeReadSKI(req.Ski); err != nil {
		return nil, err
	}
	if s.monitoring == nil {
		return nil, status.Error(codes.Unavailable, "monitoring use case not initialized")
	}
	value, err := readMetric("energy-consumed", s.resolveForRead(req.Ski), s.monitoring.EnergyConsumed)
	s.recordMonitoringRead(req.Ski, err)
	if err != nil {
		debugLogf(
			s.debug,
			"[DEBUG] Monitoring.GetEnergyConsumed read failed: requested_ski=%s err_class=%s",
			redactedSKIForLog(req.Ski),
			redactedErrorForLog(err),
		)
		if status.Code(err) != codes.Unknown {
			return nil, err
		}
		return nil, mapUsecaseError("reading energy", err, standardUsecaseErrorClasses)
	}
	debugLogf(s.debug, "[DEBUG] Monitoring.GetEnergyConsumed success: requested_ski=%s kWh=%f", redactedSKIForLog(req.Ski), value)
	return &pb.EnergyMeasurement{
		KilowattHours: value,
		Timestamp:     timestamppb.Now(),
	}, nil
}

func (s *MonitoringService) GetMeasurements(_ context.Context, req *pb.DeviceRequest) (*pb.MeasurementList, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if _, err := normalizeReadSKI(req.Ski); err != nil {
		return nil, err
	}
	return s.snapshotMeasurements(req.Ski)
}

// SnapshotMeasurements reads the measurement aggregate without a public RPC hop.
func (s *MonitoringService) SnapshotMeasurements(ski string) (*pb.MeasurementList, error) {
	if _, err := normalizeReadSKI(ski); err != nil {
		return nil, err
	}
	measurements, _, err := s.snapshotMeasurementsWithStates(ski)
	return measurements, err
}

// SnapshotMeasurementsWithStates also returns explicit status for every known leaf.
func (s *MonitoringService) SnapshotMeasurementsWithStates(ski string) (*pb.MeasurementList, map[pb.MeasurementId]pb.SnapshotValueState, error) {
	if _, err := normalizeReadSKI(ski); err != nil {
		return nil, nil, err
	}
	return s.snapshotMeasurementsWithStates(ski)
}

func (s *MonitoringService) snapshotMeasurements(ski string) (*pb.MeasurementList, error) {
	measurements, _, err := s.snapshotMeasurementsWithStates(ski)
	return measurements, err
}

func (s *MonitoringService) snapshotMeasurementsWithStates(ski string) (*pb.MeasurementList, map[pb.MeasurementId]pb.SnapshotValueState, error) {
	states := make(map[pb.MeasurementId]pb.SnapshotValueState, len(measurementCatalog))
	if s.monitoring == nil {
		return nil, states, status.Error(codes.Unavailable, "monitoring use case not initialized")
	}
	now := timestamppb.Now()
	measurements := make([]*pb.MeasurementEntry, 0, 12)
	resolved := s.resolveForRead(ski)
	if status.Code(resolved.err) == codes.FailedPrecondition {
		return nil, states, resolved.err
	}
	readState := func(err error) pb.SnapshotValueState {
		if err == nil {
			return pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_AVAILABLE
		}
		if status.Code(err) == codes.Unimplemented {
			return pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_UNSUPPORTED
		}
		return pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_TEMPORARILY_UNAVAILABLE
	}
	set := func(ids []pb.MeasurementId, err error) {
		state := readState(err)
		for _, id := range ids {
			states[id] = state
		}
	}

	if value, err := readMetric("power", resolved, s.monitoring.Power); err == nil {
		appendMeasurement(&measurements, now, "power_consumption", value, "W")
		set([]pb.MeasurementId{pb.MeasurementId_MEASUREMENT_ID_POWER_CONSUMPTION}, nil)
	} else {
		set([]pb.MeasurementId{pb.MeasurementId_MEASUREMENT_ID_POWER_CONSUMPTION}, err)
	}

	if values, err := readMetric("power-per-phase", resolved, s.monitoring.PowerPerPhase); err == nil {
		ids := []pb.MeasurementId{pb.MeasurementId_MEASUREMENT_ID_POWER_L1, pb.MeasurementId_MEASUREMENT_ID_POWER_L2, pb.MeasurementId_MEASUREMENT_ID_POWER_L3}
		set(ids, status.Error(codes.Unimplemented, "phase absent"))
		for idx, value := range values {
			appendMeasurement(&measurements, now, fmt.Sprintf("power_l%d", idx+1), value, "W")
			if idx < len(ids) {
				states[ids[idx]] = pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_AVAILABLE
			}
		}
	} else {
		set([]pb.MeasurementId{pb.MeasurementId_MEASUREMENT_ID_POWER_L1, pb.MeasurementId_MEASUREMENT_ID_POWER_L2, pb.MeasurementId_MEASUREMENT_ID_POWER_L3}, err)
	}

	if values, err := readMetric("current-per-phase", resolved, s.monitoring.CurrentPerPhase); err == nil {
		ids := []pb.MeasurementId{pb.MeasurementId_MEASUREMENT_ID_CURRENT_L1, pb.MeasurementId_MEASUREMENT_ID_CURRENT_L2, pb.MeasurementId_MEASUREMENT_ID_CURRENT_L3}
		set(ids, status.Error(codes.Unimplemented, "phase absent"))
		for idx, value := range values {
			appendMeasurement(&measurements, now, fmt.Sprintf("current_l%d", idx+1), value, "A")
			if idx < len(ids) {
				states[ids[idx]] = pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_AVAILABLE
			}
		}
	} else {
		set([]pb.MeasurementId{pb.MeasurementId_MEASUREMENT_ID_CURRENT_L1, pb.MeasurementId_MEASUREMENT_ID_CURRENT_L2, pb.MeasurementId_MEASUREMENT_ID_CURRENT_L3}, err)
	}

	if values, err := readMetric("voltage-per-phase", resolved, s.monitoring.VoltagePerPhase); err == nil {
		ids := []pb.MeasurementId{pb.MeasurementId_MEASUREMENT_ID_VOLTAGE_L1, pb.MeasurementId_MEASUREMENT_ID_VOLTAGE_L2, pb.MeasurementId_MEASUREMENT_ID_VOLTAGE_L3}
		set(ids, status.Error(codes.Unimplemented, "phase absent"))
		for idx, value := range values {
			appendMeasurement(&measurements, now, fmt.Sprintf("voltage_l%d", idx+1), value, "V")
			if idx < len(ids) {
				states[ids[idx]] = pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_AVAILABLE
			}
		}
	} else {
		set([]pb.MeasurementId{pb.MeasurementId_MEASUREMENT_ID_VOLTAGE_L1, pb.MeasurementId_MEASUREMENT_ID_VOLTAGE_L2, pb.MeasurementId_MEASUREMENT_ID_VOLTAGE_L3}, err)
	}

	if value, err := readMetric("frequency", resolved, s.monitoring.Frequency); err == nil {
		appendMeasurement(&measurements, now, "frequency", value, "Hz")
		set([]pb.MeasurementId{pb.MeasurementId_MEASUREMENT_ID_FREQUENCY}, nil)
	} else {
		set([]pb.MeasurementId{pb.MeasurementId_MEASUREMENT_ID_FREQUENCY}, err)
	}

	if value, err := readMetric("energy-consumed", resolved, s.monitoring.EnergyConsumed); err == nil {
		appendMeasurement(&measurements, now, "energy_consumed", value, "kWh")
		set([]pb.MeasurementId{pb.MeasurementId_MEASUREMENT_ID_ENERGY_CONSUMED}, nil)
	} else {
		set([]pb.MeasurementId{pb.MeasurementId_MEASUREMENT_ID_ENERGY_CONSUMED}, err)
	}

	if value, err := readMetric("energy-produced", resolved, s.monitoring.EnergyProduced); err == nil {
		appendMeasurement(&measurements, now, "energy_produced", value, "kWh")
		set([]pb.MeasurementId{pb.MeasurementId_MEASUREMENT_ID_ENERGY_PRODUCED}, nil)
	} else {
		set([]pb.MeasurementId{pb.MeasurementId_MEASUREMENT_ID_ENERGY_PRODUCED}, err)
	}

	readTemperature := func(reader temperatureReader, measurementType string, id pb.MeasurementId) {
		if reader == nil {
			set([]pb.MeasurementId{id}, status.Error(codes.Unimplemented, "temperature reader absent"))
			return
		}
		value, err := reader.Temperature(resolved.ski)
		set([]pb.MeasurementId{id}, err)
		if err == nil {
			appendMeasurement(&measurements, now, measurementType, value, "degC")
		}
	}
	readTemperature(s.dhw, "dhw_temperature", pb.MeasurementId_MEASUREMENT_ID_DHW_TEMPERATURE)
	readTemperature(s.room, "room_temperature", pb.MeasurementId_MEASUREMENT_ID_ROOM_TEMPERATURE)
	readTemperature(s.outdoor, "outdoor_temperature", pb.MeasurementId_MEASUREMENT_ID_OUTDOOR_TEMPERATURE)
	readTemperature(s.flow, "flow_temperature", pb.MeasurementId_MEASUREMENT_ID_FLOW_TEMPERATURE)
	readTemperature(s.returnTemp, "return_temperature", pb.MeasurementId_MEASUREMENT_ID_RETURN_TEMPERATURE)

	if s.monitoring != nil {
		values, err := s.monitoring.GenericMeasurements(resolved.ski)
		genericIDs := []pb.MeasurementId{
			pb.MeasurementId_MEASUREMENT_ID_COMPRESSOR_TEMPERATURE,
			pb.MeasurementId_MEASUREMENT_ID_COMPRESSOR_POWER,
			pb.MeasurementId_MEASUREMENT_ID_ENERGY_CONSUMED_HEATING,
			pb.MeasurementId_MEASUREMENT_ID_ENERGY_CONSUMED_DHW,
		}
		if err != nil {
			values = nil
			set(genericIDs, err)
		} else {
			set(genericIDs, status.Error(codes.Unimplemented, "generic measurement absent"))
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
			entry := measurements[len(measurements)-1]
			if entry.Id != nil {
				states[entry.GetId()] = pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_AVAILABLE
			}
			seen[value.Type] = struct{}{}
		}
	}

	if len(measurements) == 0 {
		if status.Code(resolved.err) == codes.NotFound {
			s.recordMonitoringRead(ski, resolved.err)
			return nil, states, resolved.err
		}
		s.recordMonitoringRead(ski, errors.New("all monitoring reads failed"))
		debugLogf(s.debug, "[DEBUG] Monitoring.GetMeasurements produced no entries: requested_ski=%s", redactedSKIForLog(ski))
		return nil, states, status.Error(codes.Unavailable, "monitoring measurements temporarily unavailable")
	}
	s.recordMonitoringRead(ski, nil)

	debugLogf(s.debug, "[DEBUG] Monitoring.GetMeasurements success: requested_ski=%s entries=%d", redactedSKIForLog(ski), len(measurements))
	return &pb.MeasurementList{Measurements: measurements}, states, nil
}

func (s *MonitoringService) recordMonitoringRead(ski string, err error) {
	if s.registry == nil {
		return
	}
	if status.Code(err) == codes.NotFound {
		s.registry.RecordCapabilityMissingEntity(ski, eebus.CapabilityMonitoring)
		return
	}
	s.registry.RecordCapabilityRead(ski, eebus.CapabilityMonitoring, err)
}

func (s *MonitoringService) SubscribeMeasurements(req *pb.DeviceRequest, stream pb.MonitoringService_SubscribeMeasurementsServer) error {
	return subscribeFilteredEvents(s.bus, req, stream.Context(), stream.Send, func(evt eebus.Event) (*pb.MeasurementEvent, bool) {
		var eventType pb.MeasurementEventType
		switch evt.Type {
		case eebus.EventTypeMonitoringPowerUpdated:
			eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_POWER_UPDATED
		case eebus.EventTypeMonitoringEnergyConsumedUpdated:
			eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_ENERGY_UPDATED
		case eebus.EventTypeDHWTemperatureUpdated:
			eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_DHW_TEMPERATURE_UPDATED
		case eebus.EventTypeDHWMonitoringSupportUpdated:
			eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_DHW_TEMPERATURE_SUPPORT_UPDATED
		case eebus.EventTypeRoomTemperatureUpdated:
			eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_ROOM_TEMPERATURE_UPDATED
		case eebus.EventTypeRoomMonitoringSupportUpdated:
			eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_ROOM_TEMPERATURE_SUPPORT_UPDATED
		case eebus.EventTypeOutdoorTemperatureUpdated:
			eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_UPDATED
		case eebus.EventTypeOutdoorMonitoringSupportUpdated:
			eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_SUPPORT_UPDATED
		case eebus.EventTypeMonitoringFlowTemperatureUpdated:
			eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_FLOW_TEMPERATURE_UPDATED
		case eebus.EventTypeMonitoringReturnTemperatureUpdated:
			eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_RETURN_TEMPERATURE_UPDATED
		case eebus.EventTypeMonitoringDeviceOperatingStateUpdated:
			eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_DEVICE_OPERATING_STATE_UPDATED
		default:
			return nil, false
		}
		event := &pb.MeasurementEvent{Ski: evt.SKI, EventType: eventType}
		s.attachMeasurementPayload(event, evt.SKI, eventType)
		return event, true
	})
}

// attachMeasurementPayload best-effort fills the event's typed payload with the
// current value so subscribers receive data directly instead of polling. On any
// read failure the event is sent without a value; the consolidated envelope
// marks that delta temporarily unavailable. Reuses the SKI-resolve + nil-entity
// fallback of the Get* readers.
func (s *MonitoringService) attachMeasurementPayload(event *pb.MeasurementEvent, ski string, eventType pb.MeasurementEventType) {
	if s.monitoring == nil && eventType != pb.MeasurementEventType_MEASUREMENT_EVENT_DHW_TEMPERATURE_UPDATED &&
		eventType != pb.MeasurementEventType_MEASUREMENT_EVENT_ROOM_TEMPERATURE_UPDATED &&
		eventType != pb.MeasurementEventType_MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_UPDATED &&
		eventType != pb.MeasurementEventType_MEASUREMENT_EVENT_FLOW_TEMPERATURE_UPDATED &&
		eventType != pb.MeasurementEventType_MEASUREMENT_EVENT_RETURN_TEMPERATURE_UPDATED &&
		eventType != pb.MeasurementEventType_MEASUREMENT_EVENT_DEVICE_OPERATING_STATE_UPDATED {
		return
	}
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
	case pb.MeasurementEventType_MEASUREMENT_EVENT_POWER_PER_PHASE_UPDATED:
		s.attachMetricList(event, ski, "power-per-phase", s.monitoring.PowerPerPhase, "power_l", "W")
	case pb.MeasurementEventType_MEASUREMENT_EVENT_CURRENT_PER_PHASE_UPDATED:
		s.attachMetricList(event, ski, "current-per-phase", s.monitoring.CurrentPerPhase, "current_l", "A")
	case pb.MeasurementEventType_MEASUREMENT_EVENT_VOLTAGE_PER_PHASE_UPDATED:
		s.attachMetricList(event, ski, "voltage-per-phase", s.monitoring.VoltagePerPhase, "voltage_l", "V")
	case pb.MeasurementEventType_MEASUREMENT_EVENT_FREQUENCY_UPDATED:
		if value, err := readMetric("frequency", s.resolveForRead(ski), s.monitoring.Frequency); err == nil {
			event.Data = &pb.MeasurementEvent_Measurements{Measurements: measurementList(
				"frequency", value, "Hz",
			)}
		}
	case pb.MeasurementEventType_MEASUREMENT_EVENT_ENERGY_PRODUCED_UPDATED:
		if value, err := readMetric("energy-produced", s.resolveForRead(ski), s.monitoring.EnergyProduced); err == nil {
			event.Data = &pb.MeasurementEvent_Measurements{Measurements: measurementList(
				"energy_produced", value, "kWh",
			)}
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

func (s *MonitoringService) AttachMeasurementPayload(event *pb.MeasurementEvent, ski string, eventType pb.MeasurementEventType) {
	s.attachMeasurementPayload(event, ski, eventType)
}

func (s *MonitoringService) attachMetricList(
	event *pb.MeasurementEvent,
	ski string,
	label string,
	read func(spineapi.EntityRemoteInterface) ([]float64, error),
	typePrefix string,
	unit string,
) {
	values, err := readMetric(label, s.resolveForRead(ski), read)
	if err != nil || len(values) == 0 {
		return
	}
	now := timestamppb.Now()
	measurements := make([]*pb.MeasurementEntry, 0, len(values))
	for index, value := range values {
		measurements = append(measurements, newMeasurementEntry(
			fmt.Sprintf("%s%d", typePrefix, index+1), value, unit, now,
		))
	}
	event.Data = &pb.MeasurementEvent_Measurements{Measurements: &pb.MeasurementList{Measurements: measurements}}
}

func measurementList(measurementType string, value float64, unit string) *pb.MeasurementList {
	return &pb.MeasurementList{Measurements: []*pb.MeasurementEntry{
		newMeasurementEntry(measurementType, value, unit, timestamppb.Now()),
	}}
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
		event.Data = &pb.MeasurementEvent_Measurement{Measurement: newMeasurementEntry(
			measurementType, value, "degC", timestamppb.Now(),
		)}
	}
}

func (s *MonitoringService) resolveEntity(ski string) (spineapi.EntityRemoteInterface, error) {
	var resolver compatibleEntityResolver
	if s.monitoring != nil {
		resolver = s.monitoring.CompatibleEntity
	}
	resolution, err := resolveCompatibleEntityResolution(
		ski,
		"monitoring entity",
		eebus.CapabilityMonitoring,
		s.registry,
		resolver,
	)
	if err != nil {
		debugLogf(
			s.debug,
			"[DEBUG] Monitoring.resolveEntity failed: requested_ski=%s err_class=%s",
			redactedSKIForLog(ski),
			redactedErrorForLog(err),
		)
		return nil, err
	}
	if s.registry != nil {
		// ski can be empty, or normalize to empty (whitespace/display separators
		// only), on the single-device convenience path (EntityResolution resolves
		// it via CompatibleEntity); fall back to the resolved entity's own device
		// SKI so the per-device watchdog state lands on the real device instead
		// of an empty-string key.
		recordSKI := eebus.NormalizeSKI(ski)
		if recordSKI == "" && resolution.Entity.Device() != nil {
			recordSKI = resolution.Entity.Device().Ski()
		}
		s.registry.RecordMonitoringSuccess(recordSKI)
	}
	return resolution.Entity, nil
}

func ambiguousDeviceSelection(deviceCount int) error {
	return status.Errorf(
		codes.FailedPrecondition,
		"ambiguous device selection: %d compatible devices found for empty ski, specify ski",
		deviceCount,
	)
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
	resolvedSKI := eebus.NormalizeSKI(ski)
	if resolvedSKI == "" && entity != nil && entity.Device() != nil {
		resolvedSKI = eebus.NormalizeSKI(entity.Device().Ski())
	}
	return resolvedEntity{ski: resolvedSKI, entity: entity, err: err}
}

// readMetric reads one measurement through the monitoring use case. Reads are
// best-effort: GetMeasurements only appends a result when the read succeeds,
// so a device that does not advertise a given measurement simply omits it.
// Resolution failures are returned unchanged so an unknown explicit SKI can
// never fall through to eebus-go's implicit first-entity behavior.
func readMetric[T any](label string, r resolvedEntity, read func(spineapi.EntityRemoteInterface) (T, error)) (T, error) {
	var zero T
	if r.err == nil {
		return read(r.entity)
	}
	return zero, r.err
}

func appendMeasurement(measurements *[]*pb.MeasurementEntry, now *timestamppb.Timestamp, measurementType string, value float64, unit string) {
	*measurements = append(*measurements, newMeasurementEntry(measurementType, value, unit, now))
}
