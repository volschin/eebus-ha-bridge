package grpc

import (
	"time"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// SubscribeDeviceState exposes one ordered stream for all device-facing state
// domains. Legacy streams remain available during the additive migration.
func (s *DeviceService) SubscribeDeviceState(
	req *pb.DeviceRequest,
	stream pb.DeviceService_SubscribeDeviceStateServer,
) error {
	return subscribeRevisionedEvents(
		s.bus,
		req,
		stream.Context(),
		stream.Send,
		func(ski string, revision uint64, eventTime time.Time) *pb.DeviceStateEvent {
			return newResyncEnvelope(
				ski,
				revision,
				eventTime,
				pb.ResyncReason_RESYNC_REASON_INITIAL_STATE_REQUIRED,
				0,
			)
		},
		func(event eebus.Event) (*pb.DeviceStateEvent, bool) {
			classification, known := classifyDeviceStateEvent(event.Type)
			if known && classification == deviceStateEventIgnored {
				return nil, false
			}
			return s.deviceStateEnvelope(event), true
		},
	)
}

type deviceStateEventClass uint8

const (
	deviceStateEventStateDelta deviceStateEventClass = iota + 1
	deviceStateEventCapabilityDelta
	deviceStateEventProviderAcknowledgement
	deviceStateEventResync
	deviceStateEventIgnored
)

// classifyDeviceStateEvent deliberately has no catch-all classification. A
// newly added internal EventType therefore has to be assigned a stream meaning
// explicitly; unknown values are converted to resync rather than being
// mislabeled as provider acknowledgements.
func classifyDeviceStateEvent(eventType eebus.EventType) (deviceStateEventClass, bool) {
	switch eventType {
	case eebus.EventTypeDeviceConnected,
		eebus.EventTypeDeviceDisconnected,
		eebus.EventTypeDeviceTrustRemoved,
		eebus.EventTypeMonitoringPowerUpdated,
		eebus.EventTypeMonitoringPowerPerPhaseUpdated,
		eebus.EventTypeMonitoringEnergyConsumedUpdated,
		eebus.EventTypeMonitoringEnergyProducedUpdated,
		eebus.EventTypeMonitoringCurrentsPerPhaseUpdated,
		eebus.EventTypeMonitoringVoltagePerPhaseUpdated,
		eebus.EventTypeMonitoringFrequencyUpdated,
		eebus.EventTypeMonitoringFlowTemperatureUpdated,
		eebus.EventTypeMonitoringReturnTemperatureUpdated,
		eebus.EventTypeMonitoringDeviceOperatingStateUpdated,
		eebus.EventTypeLPCLimitUpdated,
		eebus.EventTypeLPCFailsafePowerUpdated,
		eebus.EventTypeLPCFailsafeDurationUpdated,
		eebus.EventTypeLPCHeartbeatUpdated,
		eebus.EventTypeDHWTemperatureUpdated,
		eebus.EventTypeDHWSetpointUpdated,
		eebus.EventTypeDHWSystemFunctionUpdated,
		eebus.EventTypeRoomTemperatureUpdated,
		eebus.EventTypeOutdoorTemperatureUpdated,
		eebus.EventTypeRoomHeatingSetpointUpdated,
		eebus.EventTypeRoomHeatingSystemFunctionUpdated,
		eebus.EventTypeOHPCFConsumptionStateUpdated,
		eebus.EventTypeOHPCFConsumptionStoppableUpdated,
		eebus.EventTypeOHPCFConsumptionPausableUpdated,
		eebus.EventTypeOHPCFConsumptionStartTimeUpdated,
		eebus.EventTypeOHPCFRequestedPowerEstimateUpdated,
		eebus.EventTypeOHPCFRequestedPowerMaxUpdated,
		eebus.EventTypeOHPCFMinimalRunDurationUpdated,
		eebus.EventTypeOHPCFMinimalPauseDurationUpdated:
		return deviceStateEventStateDelta, true
	case eebus.EventTypeMonitoringUseCaseSupportUpdated,
		eebus.EventTypeLPCUseCaseSupportUpdated,
		eebus.EventTypeDHWMonitoringSupportUpdated,
		eebus.EventTypeDHWUseCaseSupportUpdated,
		eebus.EventTypeDHWSystemFunctionSupportUpdated,
		eebus.EventTypeRoomMonitoringSupportUpdated,
		eebus.EventTypeOutdoorMonitoringSupportUpdated,
		eebus.EventTypeRoomHeatingUseCaseSupportUpdated,
		eebus.EventTypeRoomHeatingSystemFunctionSupportUpdated,
		eebus.EventTypeOHPCFUseCaseSupportUpdated:
		return deviceStateEventCapabilityDelta, true
	case eebus.EventTypeMGCPConsumerUpdated,
		eebus.EventTypeVAPDConsumerUpdated,
		eebus.EventTypeVABDConsumerUpdated,
		// Pairing details carry no HA-visible state, but the bus has already
		// assigned them a per-SKI revision. Dropping them would create a
		// revision gap that clients answer with a full snapshot poll, so they
		// are forwarded as revision-bearing no-op envelopes instead.
		eebus.EventTypePairingUpdated:
		return deviceStateEventProviderAcknowledgement, true
	case eebus.EventTypeResyncRequired:
		return deviceStateEventResync, true
	case eebus.EventTypeDiscoveryUpdated:
		// Discovery reports on the whole visible-services list; it has no
		// device SKI, never consumes a per-device revision, and never reaches
		// a device-scoped subscriber.
		return deviceStateEventIgnored, true
	default:
		return 0, false
	}
}

func newResyncEnvelope(
	ski string,
	revision uint64,
	eventTime time.Time,
	reason pb.ResyncReason,
	dropped uint64,
) *pb.DeviceStateEvent {
	return &pb.DeviceStateEvent{
		Ski:       ski,
		Revision:  revision,
		EventTime: timestamppb.New(eventTime),
		Payload: &pb.DeviceStateEvent_ResyncRequired{ResyncRequired: &pb.ResyncRequired{
			Reason:        reason,
			DroppedEvents: dropped,
		}},
	}
}

func (s *DeviceService) deviceStateEnvelope(event eebus.Event) *pb.DeviceStateEvent {
	envelope := &pb.DeviceStateEvent{
		Ski:       event.SKI,
		Revision:  event.Revision,
		EventTime: timestamppb.New(event.OccurredAt),
	}
	classification, known := classifyDeviceStateEvent(event.Type)
	if !known {
		envelope.Payload = &pb.DeviceStateEvent_ResyncRequired{ResyncRequired: &pb.ResyncRequired{
			Reason: pb.ResyncReason_RESYNC_REASON_UNCLASSIFIED_EVENT,
		}}
		return envelope
	}
	if classification == deviceStateEventResync {
		envelope.Payload = &pb.DeviceStateEvent_ResyncRequired{ResyncRequired: &pb.ResyncRequired{
			Reason:        pb.ResyncReason_RESYNC_REASON_EVENT_DROPPED,
			DroppedEvents: event.Dropped,
		}}
		return envelope
	}
	if classification == deviceStateEventCapabilityDelta {
		envelope.Payload = &pb.DeviceStateEvent_Capability{Capability: s.deviceCapabilities(event.SKI)}
		envelope.Availability = pb.EventAvailability_EVENT_AVAILABILITY_AVAILABLE
		return envelope
	}
	if classification == deviceStateEventProviderAcknowledgement {
		envelope.Payload = &pb.DeviceStateEvent_Device{Device: &pb.DeviceEvent{
			Ski: event.SKI, EventType: pb.DeviceEventType_DEVICE_EVENT_PROVIDER_UPDATED,
		}}
		envelope.Availability = pb.EventAvailability_EVENT_AVAILABILITY_AVAILABLE
		return envelope
	}

	switch event.Type {
	case eebus.EventTypeDeviceConnected, eebus.EventTypeDeviceDisconnected, eebus.EventTypeDeviceTrustRemoved:
		envelope.Payload = &pb.DeviceStateEvent_Device{Device: deviceEvent(event)}
		envelope.Availability = pb.EventAvailability_EVENT_AVAILABILITY_AVAILABLE
	case eebus.EventTypeLPCLimitUpdated,
		eebus.EventTypeLPCFailsafePowerUpdated,
		eebus.EventTypeLPCFailsafeDurationUpdated,
		eebus.EventTypeLPCHeartbeatUpdated:
		lpc := lpcEvent(event)
		if s.payloads.LPC != nil {
			s.payloads.LPC.AttachLPCPayload(lpc, event.SKI, lpc.EventType)
		}
		envelope.Payload = &pb.DeviceStateEvent_Lpc{Lpc: lpc}
		envelope.Availability = eventAvailability(lpcPayloadPresent(lpc))
	case eebus.EventTypeDHWSetpointUpdated:
		dhwEvent := &pb.DHWEvent{
			Ski: event.SKI, EventType: pb.DHWEventType_DHW_EVENT_SETPOINT_UPDATED,
		}
		if s.payloads.DHW != nil {
			s.payloads.DHW.AttachDHWPayload(dhwEvent, event.SKI)
		}
		envelope.Payload = &pb.DeviceStateEvent_Dhw{Dhw: &pb.DeviceStateDHWEvent{
			Payload: &pb.DeviceStateDHWEvent_Temperature{Temperature: dhwEvent},
		}}
		envelope.Availability = eventAvailability(dhwEvent.GetSetpoint() != nil)
	case eebus.EventTypeDHWSystemFunctionUpdated:
		dhwEvent := &pb.DHWSystemFunctionEvent{
			Ski: event.SKI, EventType: pb.DHWSystemFunctionEventType_DHW_SYSTEM_FUNCTION_EVENT_STATE_UPDATED,
		}
		if s.payloads.DHW != nil {
			s.payloads.DHW.AttachDHWSystemFunctionPayload(dhwEvent, event.SKI)
		}
		envelope.Payload = &pb.DeviceStateEvent_Dhw{Dhw: &pb.DeviceStateDHWEvent{
			Payload: &pb.DeviceStateDHWEvent_SystemFunction{SystemFunction: dhwEvent},
		}}
		envelope.Availability = eventAvailability(dhwEvent.GetState() != nil)
	case eebus.EventTypeRoomHeatingSetpointUpdated,
		eebus.EventTypeRoomTemperatureUpdated,
		eebus.EventTypeRoomHeatingSystemFunctionUpdated:
		hvac := roomHeatingEvent(event)
		targetAvailable := false
		if s.payloads.HVAC != nil {
			targetAvailable = s.payloads.HVAC.AttachRoomHeatingPayload(hvac, event.SKI)
		}
		envelope.Payload = &pb.DeviceStateEvent_Hvac{Hvac: hvac}
		envelope.Availability = eventAvailability(targetAvailable)
	case eebus.EventTypeOHPCFConsumptionStateUpdated,
		eebus.EventTypeOHPCFConsumptionStoppableUpdated,
		eebus.EventTypeOHPCFConsumptionPausableUpdated,
		eebus.EventTypeOHPCFConsumptionStartTimeUpdated,
		eebus.EventTypeOHPCFRequestedPowerEstimateUpdated,
		eebus.EventTypeOHPCFRequestedPowerMaxUpdated,
		eebus.EventTypeOHPCFMinimalRunDurationUpdated,
		eebus.EventTypeOHPCFMinimalPauseDurationUpdated:
		ohpcf := ohpcfEvent(event)
		targetAvailable := false
		if s.payloads.OHPCF != nil {
			targetAvailable = s.payloads.OHPCF.AttachOHPCFPayload(ohpcf, event.SKI, event.Type)
		}
		envelope.Payload = &pb.DeviceStateEvent_Ohpcf{Ohpcf: ohpcf}
		envelope.Availability = eventAvailability(targetAvailable)
	case eebus.EventTypeMonitoringPowerUpdated,
		eebus.EventTypeMonitoringPowerPerPhaseUpdated,
		eebus.EventTypeMonitoringEnergyConsumedUpdated,
		eebus.EventTypeMonitoringEnergyProducedUpdated,
		eebus.EventTypeMonitoringCurrentsPerPhaseUpdated,
		eebus.EventTypeMonitoringVoltagePerPhaseUpdated,
		eebus.EventTypeMonitoringFrequencyUpdated,
		eebus.EventTypeMonitoringFlowTemperatureUpdated,
		eebus.EventTypeMonitoringReturnTemperatureUpdated,
		eebus.EventTypeMonitoringDeviceOperatingStateUpdated,
		eebus.EventTypeDHWTemperatureUpdated,
		eebus.EventTypeOutdoorTemperatureUpdated:
		measurement := measurementEvent(event)
		if s.payloads.Monitoring != nil {
			s.payloads.Monitoring.AttachMeasurementPayload(measurement, event.SKI, measurement.EventType)
		}
		if isDetailMeasurementEvent(measurement.GetEventType()) {
			// Preserve the previous fallback contract: older consolidated-stream
			// clients poll on UNSPECIFIED, while new clients consume MeasurementList.
			measurement.UpdateField = measurementUpdateField(measurement.GetEventType())
			measurement.EventType = pb.MeasurementEventType_MEASUREMENT_EVENT_UNSPECIFIED
		}
		envelope.Payload = &pb.DeviceStateEvent_Measurement{Measurement: measurement}
		envelope.Availability = eventAvailability(measurementPayloadPresent(measurement))
	default:
		// Every known state delta is expected above. Keep a safe resync for a
		// converter omission; never silently relabel it as another domain.
		envelope.Payload = &pb.DeviceStateEvent_ResyncRequired{ResyncRequired: &pb.ResyncRequired{
			Reason: pb.ResyncReason_RESYNC_REASON_UNCLASSIFIED_EVENT,
		}}
	}
	return envelope
}

func eventAvailability(available bool) pb.EventAvailability {
	if available {
		return pb.EventAvailability_EVENT_AVAILABILITY_AVAILABLE
	}
	return pb.EventAvailability_EVENT_AVAILABILITY_TEMPORARILY_UNAVAILABLE
}

func lpcPayloadPresent(event *pb.LPCEvent) bool {
	return event.GetLimitUpdate() != nil || event.GetFailsafeUpdate() != nil
}

func measurementPayloadPresent(event *pb.MeasurementEvent) bool {
	return event.GetPower() != nil || event.GetEnergy() != nil || event.GetMeasurement() != nil ||
		event.GetDeviceDiagnostics() != nil || event.GetMeasurements() != nil
}

func isDetailMeasurementEvent(eventType pb.MeasurementEventType) bool {
	switch eventType {
	case pb.MeasurementEventType_MEASUREMENT_EVENT_POWER_PER_PHASE_UPDATED,
		pb.MeasurementEventType_MEASUREMENT_EVENT_ENERGY_PRODUCED_UPDATED,
		pb.MeasurementEventType_MEASUREMENT_EVENT_CURRENT_PER_PHASE_UPDATED,
		pb.MeasurementEventType_MEASUREMENT_EVENT_VOLTAGE_PER_PHASE_UPDATED,
		pb.MeasurementEventType_MEASUREMENT_EVENT_FREQUENCY_UPDATED:
		return true
	default:
		return false
	}
}

func measurementUpdateField(eventType pb.MeasurementEventType) pb.MeasurementUpdateField {
	switch eventType {
	case pb.MeasurementEventType_MEASUREMENT_EVENT_POWER_PER_PHASE_UPDATED:
		return pb.MeasurementUpdateField_MEASUREMENT_UPDATE_FIELD_POWER_PER_PHASE
	case pb.MeasurementEventType_MEASUREMENT_EVENT_ENERGY_PRODUCED_UPDATED:
		return pb.MeasurementUpdateField_MEASUREMENT_UPDATE_FIELD_ENERGY_PRODUCED
	case pb.MeasurementEventType_MEASUREMENT_EVENT_CURRENT_PER_PHASE_UPDATED:
		return pb.MeasurementUpdateField_MEASUREMENT_UPDATE_FIELD_CURRENT_PER_PHASE
	case pb.MeasurementEventType_MEASUREMENT_EVENT_VOLTAGE_PER_PHASE_UPDATED:
		return pb.MeasurementUpdateField_MEASUREMENT_UPDATE_FIELD_VOLTAGE_PER_PHASE
	case pb.MeasurementEventType_MEASUREMENT_EVENT_FREQUENCY_UPDATED:
		return pb.MeasurementUpdateField_MEASUREMENT_UPDATE_FIELD_FREQUENCY
	default:
		return pb.MeasurementUpdateField_MEASUREMENT_UPDATE_FIELD_UNSPECIFIED
	}
}

func deviceEvent(event eebus.Event) *pb.DeviceEvent {
	eventType := pb.DeviceEventType_DEVICE_EVENT_UNSPECIFIED
	switch event.Type {
	case eebus.EventTypeDeviceConnected:
		eventType = pb.DeviceEventType_DEVICE_EVENT_CONNECTED
	case eebus.EventTypeDeviceDisconnected:
		eventType = pb.DeviceEventType_DEVICE_EVENT_DISCONNECTED
	case eebus.EventTypeDeviceTrustRemoved:
		eventType = pb.DeviceEventType_DEVICE_EVENT_TRUST_REMOVED
	}
	return &pb.DeviceEvent{Ski: event.SKI, EventType: eventType}
}

func lpcEvent(event eebus.Event) *pb.LPCEvent {
	eventType := pb.LPCEventType_LPC_EVENT_UNSPECIFIED
	switch event.Type {
	case eebus.EventTypeLPCLimitUpdated:
		eventType = pb.LPCEventType_LPC_EVENT_LIMIT_UPDATED
	case eebus.EventTypeLPCFailsafePowerUpdated, eebus.EventTypeLPCFailsafeDurationUpdated:
		eventType = pb.LPCEventType_LPC_EVENT_FAILSAFE_UPDATED
	case eebus.EventTypeLPCHeartbeatUpdated:
		eventType = pb.LPCEventType_LPC_EVENT_HEARTBEAT_TIMEOUT
	}
	return &pb.LPCEvent{Ski: event.SKI, EventType: eventType}
}

func roomHeatingEvent(event eebus.Event) *pb.RoomHeatingEvent {
	eventType := pb.RoomHeatingEventType_ROOM_HEATING_EVENT_UNSPECIFIED
	switch event.Type {
	case eebus.EventTypeRoomTemperatureUpdated:
		eventType = pb.RoomHeatingEventType_ROOM_HEATING_EVENT_CURRENT_TEMPERATURE_UPDATED
	case eebus.EventTypeRoomHeatingSetpointUpdated:
		eventType = pb.RoomHeatingEventType_ROOM_HEATING_EVENT_SETPOINT_UPDATED
	case eebus.EventTypeRoomHeatingSystemFunctionUpdated:
		eventType = pb.RoomHeatingEventType_ROOM_HEATING_EVENT_SYSTEM_FUNCTION_UPDATED
	}
	return &pb.RoomHeatingEvent{Ski: event.SKI, EventType: eventType}
}

func ohpcfEvent(event eebus.Event) *pb.OHPCFEvent {
	eventType := pb.OHPCFEventType_OHPCF_EVENT_DATA_UPDATED
	updateField := pb.OHPCFUpdateField_OHPCF_UPDATE_FIELD_UNSPECIFIED
	switch event.Type {
	case eebus.EventTypeOHPCFConsumptionStateUpdated:
		eventType = pb.OHPCFEventType_OHPCF_EVENT_STATE_UPDATED
		updateField = pb.OHPCFUpdateField_OHPCF_UPDATE_FIELD_STATE
	case eebus.EventTypeOHPCFConsumptionStoppableUpdated:
		updateField = pb.OHPCFUpdateField_OHPCF_UPDATE_FIELD_STOPPABLE
	case eebus.EventTypeOHPCFConsumptionPausableUpdated:
		updateField = pb.OHPCFUpdateField_OHPCF_UPDATE_FIELD_PAUSABLE
	case eebus.EventTypeOHPCFConsumptionStartTimeUpdated:
		updateField = pb.OHPCFUpdateField_OHPCF_UPDATE_FIELD_START_TIME
	case eebus.EventTypeOHPCFRequestedPowerEstimateUpdated:
		updateField = pb.OHPCFUpdateField_OHPCF_UPDATE_FIELD_REQUESTED_POWER_ESTIMATE
	case eebus.EventTypeOHPCFRequestedPowerMaxUpdated:
		updateField = pb.OHPCFUpdateField_OHPCF_UPDATE_FIELD_REQUESTED_POWER_MAX
	case eebus.EventTypeOHPCFMinimalRunDurationUpdated:
		updateField = pb.OHPCFUpdateField_OHPCF_UPDATE_FIELD_MINIMAL_RUN_DURATION
	case eebus.EventTypeOHPCFMinimalPauseDurationUpdated:
		updateField = pb.OHPCFUpdateField_OHPCF_UPDATE_FIELD_MINIMAL_PAUSE_DURATION
	}
	return &pb.OHPCFEvent{Ski: event.SKI, EventType: eventType, UpdateField: updateField}
}

func measurementEvent(event eebus.Event) *pb.MeasurementEvent {
	eventType := pb.MeasurementEventType_MEASUREMENT_EVENT_UNSPECIFIED
	switch event.Type {
	case eebus.EventTypeMonitoringPowerUpdated:
		eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_POWER_UPDATED
	case eebus.EventTypeMonitoringEnergyConsumedUpdated:
		eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_ENERGY_UPDATED
	case eebus.EventTypeMonitoringPowerPerPhaseUpdated:
		eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_POWER_PER_PHASE_UPDATED
	case eebus.EventTypeMonitoringEnergyProducedUpdated:
		eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_ENERGY_PRODUCED_UPDATED
	case eebus.EventTypeMonitoringCurrentsPerPhaseUpdated:
		eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_CURRENT_PER_PHASE_UPDATED
	case eebus.EventTypeMonitoringVoltagePerPhaseUpdated:
		eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_VOLTAGE_PER_PHASE_UPDATED
	case eebus.EventTypeMonitoringFrequencyUpdated:
		eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_FREQUENCY_UPDATED
	case eebus.EventTypeDHWTemperatureUpdated:
		eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_DHW_TEMPERATURE_UPDATED
	case eebus.EventTypeRoomTemperatureUpdated:
		eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_ROOM_TEMPERATURE_UPDATED
	case eebus.EventTypeOutdoorTemperatureUpdated:
		eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_UPDATED
	case eebus.EventTypeMonitoringFlowTemperatureUpdated:
		eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_FLOW_TEMPERATURE_UPDATED
	case eebus.EventTypeMonitoringReturnTemperatureUpdated:
		eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_RETURN_TEMPERATURE_UPDATED
	case eebus.EventTypeMonitoringDeviceOperatingStateUpdated:
		eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_DEVICE_OPERATING_STATE_UPDATED
	}
	return &pb.MeasurementEvent{Ski: event.SKI, EventType: eventType}
}
