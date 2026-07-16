"""Immutable grouped EEBUS domain state and pure state transitions."""

from __future__ import annotations

from dataclasses import dataclass, field, replace
from typing import TypeVar

import grpc

from .models import (
    CapabilityState,
    CompressorFlexibilityState,
    ConsumptionLimitState,
    CoordinatorSnapshot,
    DHWSystemFunctionState,
    DeviceInfo,
    FailsafeState,
    HeartbeatState,
    SetpointState,
    SystemFunctionState,
)


@dataclass(frozen=True, slots=True)
class ConnectionState:
    """Connection and remote-device identity state."""

    connected: bool = False
    local_ski: str = ""
    ski_registered: bool = False
    device_info: DeviceInfo | None = None
    device_operating_state: str | None = None


@dataclass(frozen=True, slots=True)
class MeasurementsState:
    """Device telemetry and energy measurements."""

    power_l1_w: float | None = None
    power_l2_w: float | None = None
    power_l3_w: float | None = None
    current_l1_a: float | None = None
    current_l2_a: float | None = None
    current_l3_a: float | None = None
    voltage_l1_v: float | None = None
    voltage_l2_v: float | None = None
    voltage_l3_v: float | None = None
    frequency_hz: float | None = None
    energy_produced_kwh: float | None = None
    dhw_temperature_c: float | None = None
    room_temperature_c: float | None = None
    outdoor_temperature_c: float | None = None
    flow_temperature_c: float | None = None
    return_temperature_c: float | None = None
    compressor_temperature_c: float | None = None
    compressor_power_w: float | None = None
    power_watts: float | None = None
    energy_consumed_heating_kwh: float | None = None
    energy_consumed_dhw_kwh: float | None = None
    energy_consumed_kwh: float | None = None


@dataclass(frozen=True, slots=True)
class LPCState:
    """Limitation-of-power-consumption state."""

    consumption_limit: ConsumptionLimitState | None = None
    failsafe_limit: FailsafeState | None = None
    heartbeat_status: HeartbeatState | None = None


@dataclass(frozen=True, slots=True)
class DHWState:
    """Domestic-hot-water state."""

    setpoint: SetpointState | None = None
    system_function: DHWSystemFunctionState | None = None


@dataclass(frozen=True, slots=True)
class HVACState:
    """Room-heating state."""

    setpoint: SetpointState | None = None
    system_function: SystemFunctionState | None = None


@dataclass(frozen=True, slots=True)
class OHPCFState:
    """Heat-pump compressor-flexibility state."""

    compressor_flexibility: CompressorFlexibilityState | None = None


@dataclass(frozen=True, slots=True)
class CapabilitiesState:
    """Availability state for optional EEBUS capabilities."""

    heartbeat: CapabilityState = CapabilityState.UNKNOWN
    lpc: CapabilityState = CapabilityState.UNKNOWN
    failsafe: CapabilityState = CapabilityState.UNKNOWN
    ohpcf: CapabilityState = CapabilityState.UNKNOWN
    dhw: CapabilityState = CapabilityState.UNKNOWN
    dhw_system_function: CapabilityState = CapabilityState.UNKNOWN
    room_heating: CapabilityState = CapabilityState.UNKNOWN


@dataclass(frozen=True, slots=True)
class DomainState:
    """Complete grouped EEBUS domain state."""

    connection: ConnectionState = field(default_factory=ConnectionState)
    measurements: MeasurementsState = field(default_factory=MeasurementsState)
    lpc: LPCState = field(default_factory=LPCState)
    dhw: DHWState = field(default_factory=DHWState)
    hvac: HVACState = field(default_factory=HVACState)
    ohpcf: OHPCFState = field(default_factory=OHPCFState)
    capabilities: CapabilitiesState = field(default_factory=CapabilitiesState)


def next_capability_state(
    current: CapabilityState, status: grpc.StatusCode | None
) -> CapabilityState:
    """Return the capability state after a successful call or gRPC status."""
    if status is None:
        return CapabilityState.AVAILABLE
    if status == grpc.StatusCode.UNIMPLEMENTED:
        return CapabilityState.UNSUPPORTED
    if status in (grpc.StatusCode.NOT_FOUND, grpc.StatusCode.UNAVAILABLE):
        return CapabilityState.TEMPORARILY_UNAVAILABLE
    return current


_G = TypeVar("_G", LPCState, DHWState, HVACState, OHPCFState)


def apply_reading(
    group: _G,
    field_name: str,
    value: object | None,
    capabilities: CapabilitiesState,
    capability_field: str,
    status: grpc.StatusCode | None,
) -> tuple[_G, CapabilitiesState]:
    """Apply one observation to a grouped object and its capability."""
    new_capabilities = replace(
        capabilities,
        **{
            capability_field: next_capability_state(
                getattr(capabilities, capability_field), status
            )
        },
    )
    new_group = (
        group
        if value is None
        else replace(group, **{field_name: value})  # type: ignore[arg-type]
    )
    return new_group, new_capabilities


def flatten(domain: DomainState) -> CoordinatorSnapshot:
    """Produce the public flat coordinator snapshot from grouped domain state."""
    connection = domain.connection
    measurements = domain.measurements
    capabilities = domain.capabilities
    return CoordinatorSnapshot(
        connected=connection.connected,
        local_ski=connection.local_ski,
        ski_registered=connection.ski_registered,
        power_l1_w=measurements.power_l1_w,
        power_l2_w=measurements.power_l2_w,
        power_l3_w=measurements.power_l3_w,
        current_l1_a=measurements.current_l1_a,
        current_l2_a=measurements.current_l2_a,
        current_l3_a=measurements.current_l3_a,
        voltage_l1_v=measurements.voltage_l1_v,
        voltage_l2_v=measurements.voltage_l2_v,
        voltage_l3_v=measurements.voltage_l3_v,
        frequency_hz=measurements.frequency_hz,
        energy_produced_kwh=measurements.energy_produced_kwh,
        dhw_temperature_c=measurements.dhw_temperature_c,
        room_temperature_c=measurements.room_temperature_c,
        outdoor_temperature_c=measurements.outdoor_temperature_c,
        flow_temperature_c=measurements.flow_temperature_c,
        return_temperature_c=measurements.return_temperature_c,
        compressor_temperature_c=measurements.compressor_temperature_c,
        compressor_power_w=measurements.compressor_power_w,
        power_watts=measurements.power_watts,
        energy_consumed_heating_kwh=measurements.energy_consumed_heating_kwh,
        energy_consumed_dhw_kwh=measurements.energy_consumed_dhw_kwh,
        energy_consumed_kwh=measurements.energy_consumed_kwh,
        consumption_limit=domain.lpc.consumption_limit,
        failsafe_limit=domain.lpc.failsafe_limit,
        heartbeat_status=domain.lpc.heartbeat_status,
        heartbeat_supported=capabilities.heartbeat,
        lpc_supported=capabilities.lpc,
        failsafe_supported=capabilities.failsafe,
        device_info=connection.device_info,
        compressor_flexibility=domain.ohpcf.compressor_flexibility,
        dhw_setpoint=domain.dhw.setpoint,
        dhw_system_function=domain.dhw.system_function,
        room_heating_setpoint=domain.hvac.setpoint,
        room_heating_system_function=domain.hvac.system_function,
        device_operating_state=connection.device_operating_state,
        ohpcf_supported=capabilities.ohpcf,
        dhw_supported=capabilities.dhw,
        dhw_sysfn_supported=capabilities.dhw_system_function,
        room_heating_supported=capabilities.room_heating,
    )
