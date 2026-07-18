package grpc

import (
	"context"
	"sync"
	"time"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type SnapshotMeasurementSource interface {
	MeasurementPayloadSource
	SnapshotMeasurements(string) (*pb.MeasurementList, error)
}

type snapshotMeasurementStateSource interface {
	SnapshotMeasurementsWithStates(string) (*pb.MeasurementList, map[pb.MeasurementId]pb.SnapshotValueState, error)
}

// snapshotDeviceDiagnosticsSource actively refreshes device operating state at
// snapshot build time, unlike the passive cache-only AttachMeasurementPayload
// path used by the per-event stream loop.
type snapshotDeviceDiagnosticsSource interface {
	SnapshotDeviceDiagnostics(string) *pb.DeviceDiagnosticsData
}

type SnapshotHeartbeatSource interface {
	SnapshotHeartbeat(string) (*pb.HeartbeatStatus, error)
}

// DeviceSnapshotAssembler is the single bridge read model for HA. It consumes
// domain reader ports directly and never invokes another service's public RPC.
type DeviceSnapshotAssembler struct {
	registry   *eebus.DeviceRegistry
	monitoring SnapshotMeasurementSource
	lpc        LPCPayloadSource
	heartbeat  SnapshotHeartbeatSource
	dhw        DHWPayloadSource
	hvac       HVACPayloadSource
	ohpcf      OHPCFPayloadSource
	recovery   RecoveryDiagnosticsSource
	metricsMu  sync.RWMutex
	metrics    map[string]SnapshotReadMetrics
}

type SnapshotReadMetrics struct {
	Duration    time.Duration
	LastSuccess time.Time
}

func NewDeviceSnapshotAssembler(registry *eebus.DeviceRegistry, sources DeviceStatePayloadSources) *DeviceSnapshotAssembler {
	assembler := &DeviceSnapshotAssembler{
		registry: registry, lpc: sources.LPC, dhw: sources.DHW, hvac: sources.HVAC, ohpcf: sources.OHPCF,
		metrics: make(map[string]SnapshotReadMetrics),
	}
	assembler.monitoring, _ = sources.Monitoring.(SnapshotMeasurementSource)
	assembler.heartbeat, _ = sources.LPC.(SnapshotHeartbeatSource)
	return assembler
}

func (a *DeviceSnapshotAssembler) Build(ski string, revision uint64) (*pb.DeviceSnapshot, error) {
	ski, err := requireExplicitSKI(ski)
	if err != nil {
		return nil, err
	}
	if a == nil || a.registry == nil {
		return nil, status.Error(codes.Unavailable, "device snapshot service not initialized")
	}
	if !a.registry.KnownDevice(ski) {
		return nil, status.Error(codes.NotFound, "device not found for specified ski")
	}
	startedAt := time.Now()

	result := &pb.DeviceSnapshot{
		Ski:           ski,
		CapturedAt:    timestamppb.Now(),
		EventRevision: revision,
	}
	valueState := func(capability eebus.Capability, available, attempted bool) pb.SnapshotValueState {
		return a.valueState(ski, capability, available, attempted)
	}
	connected, transition, _ := a.registry.DeviceConnection(ski)
	result.Connection = &pb.DeviceStatus{Connected: connected}
	if !transition.IsZero() {
		result.Connection.LastTransition = timestamppb.New(transition)
	}
	if a.recovery != nil {
		recovery := a.recovery.Snapshot(ski, time.Now())
		result.Connection.Readiness = readinessState(recovery.State)
		result.Connection.Recovery = recoveryDiagnostics(recovery)
	}
	result.ConnectionState = pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_AVAILABLE

	if info, ok := a.registry.GetDevice(ski); ok {
		result.Classification = &pb.PairedDevice{
			Ski: ski, Brand: info.Brand, Model: info.Model, Serial: info.Serial,
			DeviceType: info.DeviceType, SupportedUseCases: append([]string(nil), info.UseCases...),
		}
		result.ClassificationState = pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_AVAILABLE
	}

	measurementStates := make(map[pb.MeasurementId]pb.SnapshotValueState)
	if a.monitoring != nil {
		var measurements *pb.MeasurementList
		var readErr error
		if source, ok := a.monitoring.(snapshotMeasurementStateSource); ok {
			measurements, measurementStates, readErr = source.SnapshotMeasurementsWithStates(ski)
		} else {
			measurements, readErr = a.monitoring.SnapshotMeasurements(ski)
		}
		if readErr == nil && measurements != nil {
			result.Measurements = measurements.Measurements
		}
		result.MeasurementsState = valueState(eebus.CapabilityMonitoring, len(result.Measurements) > 0, true)
		if source, ok := a.monitoring.(snapshotDeviceDiagnosticsSource); ok {
			result.DeviceDiagnostics = source.SnapshotDeviceDiagnostics(ski)
		} else {
			diagnostics := &pb.MeasurementEvent{Ski: ski, EventType: pb.MeasurementEventType_MEASUREMENT_EVENT_DEVICE_OPERATING_STATE_UPDATED}
			a.monitoring.AttachMeasurementPayload(diagnostics, ski, diagnostics.EventType)
			result.DeviceDiagnostics = diagnostics.GetDeviceDiagnostics()
		}
		result.DeviceDiagnosticsState = valueState(eebus.CapabilityMonitoring, result.DeviceDiagnostics != nil, true)
	} else {
		result.MeasurementsState = valueState(eebus.CapabilityMonitoring, false, false)
		result.DeviceDiagnosticsState = result.MeasurementsState
	}

	if a.lpc != nil {
		limit := &pb.LPCEvent{Ski: ski, EventType: pb.LPCEventType_LPC_EVENT_LIMIT_UPDATED}
		a.lpc.AttachLPCPayload(limit, ski, limit.EventType)
		result.ConsumptionLimit = limit.GetLimitUpdate()
		result.ConsumptionLimitState = valueState(eebus.CapabilityLPC, result.ConsumptionLimit != nil, true)
		failsafe := &pb.LPCEvent{Ski: ski, EventType: pb.LPCEventType_LPC_EVENT_FAILSAFE_UPDATED}
		a.lpc.AttachLPCPayload(failsafe, ski, failsafe.EventType)
		result.FailsafeLimit = failsafe.GetFailsafeUpdate()
		result.FailsafeLimitState = valueState(eebus.CapabilityFailsafe, result.FailsafeLimit != nil, true)
	} else {
		result.ConsumptionLimitState = valueState(eebus.CapabilityLPC, false, false)
		result.FailsafeLimitState = valueState(eebus.CapabilityFailsafe, false, false)
	}
	if a.heartbeat != nil {
		result.Heartbeat, err = a.heartbeat.SnapshotHeartbeat(ski)
		result.HeartbeatState = valueState(eebus.CapabilityHeartbeat, err == nil && result.Heartbeat != nil, true)
	} else {
		result.HeartbeatState = valueState(eebus.CapabilityHeartbeat, false, false)
	}

	if a.dhw != nil {
		setpoint := &pb.DHWEvent{Ski: ski, EventType: pb.DHWEventType_DHW_EVENT_SETPOINT_UPDATED}
		a.dhw.AttachDHWPayload(setpoint, ski)
		result.DhwSetpoint = setpoint.GetSetpoint()
		result.DhwSetpointState = valueState(eebus.CapabilityDHW, result.DhwSetpoint != nil, true)
		systemFunction := &pb.DHWSystemFunctionEvent{Ski: ski, EventType: pb.DHWSystemFunctionEventType_DHW_SYSTEM_FUNCTION_EVENT_STATE_UPDATED}
		a.dhw.AttachDHWSystemFunctionPayload(systemFunction, ski)
		result.DhwSystemFunction = systemFunction.GetState()
		result.DhwSystemFunctionState = valueState(eebus.CapabilityDHWSystemFunction, result.DhwSystemFunction != nil, true)
	} else {
		result.DhwSetpointState = valueState(eebus.CapabilityDHW, false, false)
		result.DhwSystemFunctionState = valueState(eebus.CapabilityDHWSystemFunction, false, false)
	}

	if a.hvac != nil {
		heating := &pb.RoomHeatingEvent{Ski: ski, EventType: pb.RoomHeatingEventType_ROOM_HEATING_EVENT_SETPOINT_UPDATED}
		a.hvac.AttachRoomHeatingPayload(heating, ski)
		result.RoomHeating = heating.GetState()
		result.RoomHeatingState = valueState(eebus.CapabilityRoomHeating, result.RoomHeating != nil, true)
	} else {
		result.RoomHeatingState = valueState(eebus.CapabilityRoomHeating, false, false)
	}

	if a.ohpcf != nil {
		flexibility := &pb.OHPCFEvent{Ski: ski, EventType: pb.OHPCFEventType_OHPCF_EVENT_STATE_UPDATED}
		a.ohpcf.AttachOHPCFPayload(flexibility, ski, eebus.EventTypeOHPCFConsumptionStateUpdated)
		result.CompressorFlexibility = flexibility.GetFlexibility()
		result.CompressorFlexibilityState = valueState(eebus.CapabilityOHPCF, result.CompressorFlexibility != nil, true)
	} else {
		result.CompressorFlexibilityState = valueState(eebus.CapabilityOHPCF, false, false)
	}
	// Domain reads above update capability state in the registry. Capture the
	// contract only after those reads so payload availability and capabilities
	// describe the same point-in-time result.
	result.Capabilities = capabilitiesFromRegistry(a.registry, ski)
	populateSnapshotFieldStates(result, measurementStates)
	a.recordMetrics(ski, time.Since(startedAt), time.Now().UTC())
	return result, nil
}

func (a *DeviceSnapshotAssembler) recordMetrics(ski string, duration time.Duration, success time.Time) {
	a.metricsMu.Lock()
	defer a.metricsMu.Unlock()
	if a.metrics == nil {
		a.metrics = make(map[string]SnapshotReadMetrics)
	}
	a.metrics[eebus.NormalizeSKI(ski)] = SnapshotReadMetrics{Duration: duration, LastSuccess: success}
}

func (a *DeviceSnapshotAssembler) Metrics(ski string) SnapshotReadMetrics {
	if a == nil {
		return SnapshotReadMetrics{}
	}
	a.metricsMu.RLock()
	defer a.metricsMu.RUnlock()
	return a.metrics[eebus.NormalizeSKI(ski)]
}

var snapshotMeasurementFields = map[pb.MeasurementId]pb.SnapshotFieldId{
	pb.MeasurementId_MEASUREMENT_ID_POWER_CONSUMPTION:       pb.SnapshotFieldId_SNAPSHOT_FIELD_POWER,
	pb.MeasurementId_MEASUREMENT_ID_ENERGY_CONSUMED:         pb.SnapshotFieldId_SNAPSHOT_FIELD_ENERGY_CONSUMED,
	pb.MeasurementId_MEASUREMENT_ID_POWER_L1:                pb.SnapshotFieldId_SNAPSHOT_FIELD_POWER_L1,
	pb.MeasurementId_MEASUREMENT_ID_POWER_L2:                pb.SnapshotFieldId_SNAPSHOT_FIELD_POWER_L2,
	pb.MeasurementId_MEASUREMENT_ID_POWER_L3:                pb.SnapshotFieldId_SNAPSHOT_FIELD_POWER_L3,
	pb.MeasurementId_MEASUREMENT_ID_CURRENT_L1:              pb.SnapshotFieldId_SNAPSHOT_FIELD_CURRENT_L1,
	pb.MeasurementId_MEASUREMENT_ID_CURRENT_L2:              pb.SnapshotFieldId_SNAPSHOT_FIELD_CURRENT_L2,
	pb.MeasurementId_MEASUREMENT_ID_CURRENT_L3:              pb.SnapshotFieldId_SNAPSHOT_FIELD_CURRENT_L3,
	pb.MeasurementId_MEASUREMENT_ID_VOLTAGE_L1:              pb.SnapshotFieldId_SNAPSHOT_FIELD_VOLTAGE_L1,
	pb.MeasurementId_MEASUREMENT_ID_VOLTAGE_L2:              pb.SnapshotFieldId_SNAPSHOT_FIELD_VOLTAGE_L2,
	pb.MeasurementId_MEASUREMENT_ID_VOLTAGE_L3:              pb.SnapshotFieldId_SNAPSHOT_FIELD_VOLTAGE_L3,
	pb.MeasurementId_MEASUREMENT_ID_FREQUENCY:               pb.SnapshotFieldId_SNAPSHOT_FIELD_FREQUENCY,
	pb.MeasurementId_MEASUREMENT_ID_ENERGY_PRODUCED:         pb.SnapshotFieldId_SNAPSHOT_FIELD_ENERGY_PRODUCED,
	pb.MeasurementId_MEASUREMENT_ID_DHW_TEMPERATURE:         pb.SnapshotFieldId_SNAPSHOT_FIELD_DHW_TEMPERATURE,
	pb.MeasurementId_MEASUREMENT_ID_ROOM_TEMPERATURE:        pb.SnapshotFieldId_SNAPSHOT_FIELD_ROOM_TEMPERATURE,
	pb.MeasurementId_MEASUREMENT_ID_OUTDOOR_TEMPERATURE:     pb.SnapshotFieldId_SNAPSHOT_FIELD_OUTDOOR_TEMPERATURE,
	pb.MeasurementId_MEASUREMENT_ID_FLOW_TEMPERATURE:        pb.SnapshotFieldId_SNAPSHOT_FIELD_FLOW_TEMPERATURE,
	pb.MeasurementId_MEASUREMENT_ID_RETURN_TEMPERATURE:      pb.SnapshotFieldId_SNAPSHOT_FIELD_RETURN_TEMPERATURE,
	pb.MeasurementId_MEASUREMENT_ID_COMPRESSOR_TEMPERATURE:  pb.SnapshotFieldId_SNAPSHOT_FIELD_COMPRESSOR_TEMPERATURE,
	pb.MeasurementId_MEASUREMENT_ID_COMPRESSOR_POWER:        pb.SnapshotFieldId_SNAPSHOT_FIELD_COMPRESSOR_POWER,
	pb.MeasurementId_MEASUREMENT_ID_ENERGY_CONSUMED_HEATING: pb.SnapshotFieldId_SNAPSHOT_FIELD_ENERGY_CONSUMED_HEATING,
	pb.MeasurementId_MEASUREMENT_ID_ENERGY_CONSUMED_DHW:     pb.SnapshotFieldId_SNAPSHOT_FIELD_ENERGY_CONSUMED_DHW,
}

func populateSnapshotFieldStates(snapshot *pb.DeviceSnapshot, measurementOverrides ...map[pb.MeasurementId]pb.SnapshotValueState) {
	states := map[pb.SnapshotFieldId]pb.SnapshotValueState{
		pb.SnapshotFieldId_SNAPSHOT_FIELD_CONNECTED:                    snapshot.ConnectionState,
		pb.SnapshotFieldId_SNAPSHOT_FIELD_LOCAL_SKI:                    pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_AVAILABLE,
		pb.SnapshotFieldId_SNAPSHOT_FIELD_DEVICE_INFO:                  snapshot.ClassificationState,
		pb.SnapshotFieldId_SNAPSHOT_FIELD_DEVICE_OPERATING_STATE:       snapshot.DeviceDiagnosticsState,
		pb.SnapshotFieldId_SNAPSHOT_FIELD_CONSUMPTION_LIMIT:            snapshot.ConsumptionLimitState,
		pb.SnapshotFieldId_SNAPSHOT_FIELD_FAILSAFE_LIMIT:               snapshot.FailsafeLimitState,
		pb.SnapshotFieldId_SNAPSHOT_FIELD_HEARTBEAT_STATUS:             snapshot.HeartbeatState,
		pb.SnapshotFieldId_SNAPSHOT_FIELD_DHW_SETPOINT:                 snapshot.DhwSetpointState,
		pb.SnapshotFieldId_SNAPSHOT_FIELD_DHW_SYSTEM_FUNCTION:          snapshot.DhwSystemFunctionState,
		pb.SnapshotFieldId_SNAPSHOT_FIELD_ROOM_HEATING_SETPOINT:        snapshot.RoomHeatingState,
		pb.SnapshotFieldId_SNAPSHOT_FIELD_ROOM_HEATING_SYSTEM_FUNCTION: snapshot.RoomHeatingState,
		pb.SnapshotFieldId_SNAPSHOT_FIELD_COMPRESSOR_FLEXIBILITY:       snapshot.CompressorFlexibilityState,
	}
	for _, field := range snapshotMeasurementFields {
		state := snapshot.MeasurementsState
		if state == pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_AVAILABLE {
			state = pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_UNKNOWN
		}
		states[field] = state
	}
	for _, measurement := range snapshot.Measurements {
		if measurement.Id == nil {
			continue
		}
		if field, ok := snapshotMeasurementFields[measurement.GetId()]; ok {
			states[field] = pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_AVAILABLE
		}
	}
	for _, existing := range snapshot.FieldStates {
		states[existing.Id] = existing.State
	}
	for _, overrides := range measurementOverrides {
		for measurementID, state := range overrides {
			if field, ok := snapshotMeasurementFields[measurementID]; ok {
				states[field] = state
			}
		}
	}
	if snapshot.RoomHeating != nil {
		if snapshot.RoomHeating.Setpoint == nil {
			states[pb.SnapshotFieldId_SNAPSHOT_FIELD_ROOM_HEATING_SETPOINT] = missingSnapshotFieldState(snapshot.RoomHeatingState)
		} else {
			states[pb.SnapshotFieldId_SNAPSHOT_FIELD_ROOM_HEATING_SETPOINT] = pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_AVAILABLE
		}
		if snapshot.RoomHeating.SystemFunction == nil {
			states[pb.SnapshotFieldId_SNAPSHOT_FIELD_ROOM_HEATING_SYSTEM_FUNCTION] = missingSnapshotFieldState(snapshot.RoomHeatingState)
		} else {
			states[pb.SnapshotFieldId_SNAPSHOT_FIELD_ROOM_HEATING_SYSTEM_FUNCTION] = pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_AVAILABLE
		}
		if snapshot.RoomHeating.CurrentTemperatureCelsius != nil {
			states[pb.SnapshotFieldId_SNAPSHOT_FIELD_ROOM_TEMPERATURE] = pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_AVAILABLE
		}
	}
	snapshot.FieldStates = make([]*pb.SnapshotFieldStatus, 0, int(pb.SnapshotFieldId_SNAPSHOT_FIELD_ROOM_HEATING_SYSTEM_FUNCTION))
	for id := pb.SnapshotFieldId_SNAPSHOT_FIELD_CONNECTED; id <= pb.SnapshotFieldId_SNAPSHOT_FIELD_ROOM_HEATING_SYSTEM_FUNCTION; id++ {
		snapshot.FieldStates = append(snapshot.FieldStates, &pb.SnapshotFieldStatus{Id: id, State: states[id]})
	}
}

func missingSnapshotFieldState(aggregate pb.SnapshotValueState) pb.SnapshotValueState {
	if aggregate == pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_AVAILABLE {
		return pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_UNKNOWN
	}
	return aggregate
}

func (a *DeviceSnapshotAssembler) valueState(ski string, capability eebus.Capability, available, attempted bool) pb.SnapshotValueState {
	if available {
		return pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_AVAILABLE
	}
	entries, _ := a.registry.DeviceCapabilities(ski)
	for _, entry := range entries {
		if entry.ID != capability {
			continue
		}
		switch entry.State {
		case eebus.CapabilityStateUnsupported:
			return pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_UNSUPPORTED
		case eebus.CapabilityStateTemporarilyUnavailable:
			return pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_TEMPORARILY_UNAVAILABLE
		}
	}
	if attempted {
		return pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_TEMPORARILY_UNAVAILABLE
	}
	return pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_UNKNOWN
}

func capabilitiesFromRegistry(registry *eebus.DeviceRegistry, ski string) *pb.DeviceCapabilities {
	entries, _ := registry.DeviceCapabilities(ski)
	capabilities := make([]*pb.DeviceCapability, 0, len(entries))
	for _, entry := range entries {
		capability := &pb.DeviceCapability{Id: capabilityID(entry.ID), State: capabilityState(entry.State), Reason: capabilityReason(entry.Reason)}
		if !entry.LastChanged.IsZero() {
			capability.LastChanged = timestamppb.New(entry.LastChanged)
		}
		capabilities = append(capabilities, capability)
	}
	return &pb.DeviceCapabilities{Ski: ski, Capabilities: capabilities}
}

func (s *DeviceService) GetDeviceSnapshot(_ context.Context, req *pb.DeviceRequest) (*pb.DeviceSnapshot, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if s.snapshot == nil {
		return nil, status.Error(codes.Unavailable, "device snapshot service not initialized")
	}
	revision := uint64(0)
	if s.bus != nil {
		revision = s.bus.Revision(req.Ski)
	}
	snapshot, err := s.snapshot.Build(req.Ski, revision)
	if err == nil {
		snapshot.LocalSki = s.localSKI
		populateSnapshotFieldStates(snapshot)
	}
	return snapshot, err
}
