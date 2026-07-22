package eebus

import "time"

// EventType identifies an event published on the internal event bus.
type EventType string

const (
	EventTypeDeviceConnected             EventType = "device.connected"
	EventTypeDeviceDisconnected          EventType = "device.disconnected"
	EventTypeDeviceTrustRemoved          EventType = "device.trust_removed"
	EventTypeDeviceClassificationUpdated EventType = "device.classification_updated"
	EventTypeDiscoveryUpdated            EventType = "discovery.updated"
	EventTypePairingUpdated              EventType = "pairing.updated"
	EventTypeResyncRequired              EventType = "device_state.resync_required"

	EventTypeMonitoringPowerUpdated                EventType = "monitoring.power_updated"
	EventTypeMonitoringPowerPerPhaseUpdated        EventType = "monitoring.power_per_phase_updated"
	EventTypeMonitoringEnergyConsumedUpdated       EventType = "monitoring.energy_consumed_updated"
	EventTypeMonitoringEnergyProducedUpdated       EventType = "monitoring.energy_produced_updated"
	EventTypeMonitoringCurrentsPerPhaseUpdated     EventType = "monitoring.currents_per_phase_updated"
	EventTypeMonitoringVoltagePerPhaseUpdated      EventType = "monitoring.voltage_per_phase_updated"
	EventTypeMonitoringFrequencyUpdated            EventType = "monitoring.frequency_updated"
	EventTypeMonitoringUseCaseSupportUpdated       EventType = "monitoring.use_case_support_updated"
	EventTypeMonitoringFlowTemperatureUpdated      EventType = "monitoring.flow_temperature_updated"
	EventTypeMonitoringReturnTemperatureUpdated    EventType = "monitoring.return_temperature_updated"
	EventTypeMonitoringDeviceOperatingStateUpdated EventType = "monitoring.device_operating_state_updated"

	EventTypeLPCLimitUpdated            EventType = "lpc.limit_updated"
	EventTypeLPCFailsafePowerUpdated    EventType = "lpc.failsafe_power_updated"
	EventTypeLPCFailsafeDurationUpdated EventType = "lpc.failsafe_duration_updated"
	EventTypeLPCUseCaseSupportUpdated   EventType = "lpc.use_case_support_updated"
	EventTypeLPCHeartbeatUpdated        EventType = "lpc.heartbeat_updated"

	EventTypeDHWTemperatureUpdated                   EventType = "dhw.temperature_updated"
	EventTypeDHWMonitoringSupportUpdated             EventType = "dhw.monitoring_support_updated"
	EventTypeDHWSetpointUpdated                      EventType = "dhw.setpoint_updated"
	EventTypeDHWUseCaseSupportUpdated                EventType = "dhw.use_case_support_updated"
	EventTypeDHWSystemFunctionUpdated                EventType = "dhwsysfn.updated"
	EventTypeDHWSystemFunctionSupportUpdated         EventType = "dhwsysfn.use_case_support_updated"
	EventTypeRoomTemperatureUpdated                  EventType = "room.temperature_updated"
	EventTypeRoomMonitoringSupportUpdated            EventType = "room.monitoring_support_updated"
	EventTypeOutdoorTemperatureUpdated               EventType = "outdoor.temperature_updated"
	EventTypeOutdoorMonitoringSupportUpdated         EventType = "outdoor.monitoring_support_updated"
	EventTypeRoomHeatingSetpointUpdated              EventType = "roomheating.setpoint_updated"
	EventTypeRoomHeatingUseCaseSupportUpdated        EventType = "roomheating.use_case_support_updated"
	EventTypeRoomHeatingSystemFunctionUpdated        EventType = "roomheatingsysfn.updated"
	EventTypeRoomHeatingSystemFunctionSupportUpdated EventType = "roomheatingsysfn.use_case_support_updated"

	EventTypeOHPCFUseCaseSupportUpdated         EventType = "ohpcf.use_case_support_updated"
	EventTypeOHPCFConsumptionStateUpdated       EventType = "ohpcf.consumption_state_updated"
	EventTypeOHPCFConsumptionStoppableUpdated   EventType = "ohpcf.consumption_stoppable_updated"
	EventTypeOHPCFConsumptionPausableUpdated    EventType = "ohpcf.consumption_pausable_updated"
	EventTypeOHPCFConsumptionStartTimeUpdated   EventType = "ohpcf.consumption_start_time_updated"
	EventTypeOHPCFRequestedPowerEstimateUpdated EventType = "ohpcf.requested_power_estimate_updated"
	EventTypeOHPCFRequestedPowerMaxUpdated      EventType = "ohpcf.requested_power_max_updated"
	EventTypeOHPCFMinimalRunDurationUpdated     EventType = "ohpcf.minimal_run_duration_updated"
	EventTypeOHPCFMinimalPauseDurationUpdated   EventType = "ohpcf.minimal_pause_duration_updated"

	EventTypeMGCPConsumerUpdated EventType = "mgcp.consumer_updated"
	EventTypeVAPDConsumerUpdated EventType = "vapd.consumer_updated"
	EventTypeVABDConsumerUpdated EventType = "vabd.consumer_updated"
)

// Event represents an internal event from eebus-go callbacks.
type Event struct {
	SKI        string
	Type       EventType
	Revision   uint64
	OccurredAt time.Time
	Dropped    uint64
}
