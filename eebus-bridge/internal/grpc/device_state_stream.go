package grpc

import (
	"time"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// SubscribeDeviceState exposes one ordered stream for all device-facing state
// domains. Legacy streams remain available during the additive migration.
func (s *DeviceService) SubscribeDeviceState(
	req *pb.DeviceRequest,
	stream pb.DeviceService_SubscribeDeviceStateServer,
) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "request is required")
	}
	ski := eebus.NormalizeSKI(req.Ski)
	if !validSKI(ski) {
		return status.Errorf(codes.InvalidArgument, "ski must be 40 hex characters, got %q", req.Ski)
	}
	if s.bus == nil {
		return status.Error(codes.Unavailable, "event bus not initialized")
	}

	ch, revision := s.bus.SubscribeWithRevision(ski)
	defer s.bus.Unsubscribe(ch)
	if err := stream.Send(newResyncEnvelope(
		ski,
		revision,
		time.Now().UTC(),
		pb.ResyncReason_RESYNC_REASON_INITIAL_STATE_REQUIRED,
		0,
	)); err != nil {
		return err
	}

	for {
		if event, pending := s.bus.TakePendingResync(ch); pending {
			if err := stream.Send(s.deviceStateEnvelope(event)); err != nil {
				return err
			}
			continue
		}
		select {
		case event, ok := <-ch:
			if !ok {
				return nil
			}
			envelope := s.deviceStateEnvelope(event)
			if err := stream.Send(envelope); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
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
	if event.Type == eebus.EventTypeResyncRequired {
		envelope.Payload = &pb.DeviceStateEvent_ResyncRequired{ResyncRequired: &pb.ResyncRequired{
			Reason:        pb.ResyncReason_RESYNC_REASON_EVENT_DROPPED,
			DroppedEvents: event.Dropped,
		}}
		return envelope
	}
	if capabilitySupportEvent(event.Type) {
		envelope.Payload = &pb.DeviceStateEvent_Capability{Capability: s.deviceCapabilities(event.SKI)}
		return envelope
	}

	switch event.Type {
	case eebus.EventTypeDeviceConnected, eebus.EventTypeDeviceDisconnected, eebus.EventTypeDeviceTrustRemoved:
		envelope.Payload = &pb.DeviceStateEvent_Device{Device: deviceEvent(event)}
	case eebus.EventTypeLPCLimitUpdated,
		eebus.EventTypeLPCFailsafePowerUpdated,
		eebus.EventTypeLPCFailsafeDurationUpdated,
		eebus.EventTypeLPCHeartbeatUpdated:
		envelope.Payload = &pb.DeviceStateEvent_Lpc{Lpc: lpcEvent(event)}
	case eebus.EventTypeDHWSetpointUpdated:
		envelope.Payload = &pb.DeviceStateEvent_Dhw{Dhw: &pb.DeviceStateDHWEvent{
			Payload: &pb.DeviceStateDHWEvent_Temperature{Temperature: &pb.DHWEvent{
				Ski: event.SKI, EventType: pb.DHWEventType_DHW_EVENT_SETPOINT_UPDATED,
			}},
		}}
	case eebus.EventTypeDHWSystemFunctionUpdated:
		envelope.Payload = &pb.DeviceStateEvent_Dhw{Dhw: &pb.DeviceStateDHWEvent{
			Payload: &pb.DeviceStateDHWEvent_SystemFunction{SystemFunction: &pb.DHWSystemFunctionEvent{
				Ski: event.SKI, EventType: pb.DHWSystemFunctionEventType_DHW_SYSTEM_FUNCTION_EVENT_STATE_UPDATED,
			}},
		}}
	case eebus.EventTypeRoomHeatingSetpointUpdated,
		eebus.EventTypeRoomHeatingSystemFunctionUpdated:
		envelope.Payload = &pb.DeviceStateEvent_Hvac{Hvac: roomHeatingEvent(event)}
	case eebus.EventTypeOHPCFConsumptionStateUpdated,
		eebus.EventTypeOHPCFConsumptionStoppableUpdated,
		eebus.EventTypeOHPCFConsumptionPausableUpdated,
		eebus.EventTypeOHPCFConsumptionStartTimeUpdated,
		eebus.EventTypeOHPCFRequestedPowerEstimateUpdated,
		eebus.EventTypeOHPCFRequestedPowerMaxUpdated,
		eebus.EventTypeOHPCFMinimalRunDurationUpdated,
		eebus.EventTypeOHPCFMinimalPauseDurationUpdated:
		envelope.Payload = &pb.DeviceStateEvent_Ohpcf{Ohpcf: ohpcfEvent(event)}
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
		eebus.EventTypeRoomTemperatureUpdated,
		eebus.EventTypeOutdoorTemperatureUpdated:
		envelope.Payload = &pb.DeviceStateEvent_Measurement{Measurement: measurementEvent(event)}
	default:
		// Provider-side events are still revision-bearing device events. HA's
		// provider manager owns their values, so the device session only needs
		// to consume the revision without triggering a state mutation.
		envelope.Payload = &pb.DeviceStateEvent_Device{Device: &pb.DeviceEvent{
			Ski: event.SKI, EventType: pb.DeviceEventType_DEVICE_EVENT_PROVIDER_UPDATED,
		}}
	}
	return envelope
}

func capabilitySupportEvent(eventType eebus.EventType) bool {
	switch eventType {
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
		return true
	default:
		return false
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
	if event.Type == eebus.EventTypeRoomHeatingSetpointUpdated {
		eventType = pb.RoomHeatingEventType_ROOM_HEATING_EVENT_SETPOINT_UPDATED
	} else if event.Type == eebus.EventTypeRoomHeatingSystemFunctionUpdated {
		eventType = pb.RoomHeatingEventType_ROOM_HEATING_EVENT_SYSTEM_FUNCTION_UPDATED
	}
	return &pb.RoomHeatingEvent{Ski: event.SKI, EventType: eventType}
}

func ohpcfEvent(event eebus.Event) *pb.OHPCFEvent {
	eventType := pb.OHPCFEventType_OHPCF_EVENT_DATA_UPDATED
	if event.Type == eebus.EventTypeOHPCFConsumptionStateUpdated {
		eventType = pb.OHPCFEventType_OHPCF_EVENT_STATE_UPDATED
	}
	return &pb.OHPCFEvent{Ski: event.SKI, EventType: eventType}
}

func measurementEvent(event eebus.Event) *pb.MeasurementEvent {
	eventType := pb.MeasurementEventType_MEASUREMENT_EVENT_UNSPECIFIED
	switch event.Type {
	case eebus.EventTypeMonitoringPowerUpdated:
		eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_POWER_UPDATED
	case eebus.EventTypeMonitoringEnergyConsumedUpdated:
		eventType = pb.MeasurementEventType_MEASUREMENT_EVENT_ENERGY_UPDATED
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
