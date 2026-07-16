"""Typed coordinator snapshot models and protobuf conversion helpers."""

from __future__ import annotations

from enum import StrEnum
from typing import TYPE_CHECKING, TypedDict, cast

if TYPE_CHECKING:
    from collections.abc import Sequence

    from . import proto_stubs


class CapabilityState(StrEnum):
    """Availability state for an EEBUS use-case capability."""

    UNKNOWN = "unknown"
    AVAILABLE = "available"
    TEMPORARILY_UNAVAILABLE = "temporarily_unavailable"
    UNSUPPORTED = "unsupported"


class SetpointState(TypedDict):
    """Temperature setpoint and its device-provided constraints."""

    value_celsius: float
    min_celsius: float
    max_celsius: float
    step_celsius: float
    writable: bool


class SystemFunctionState(TypedDict):
    """Operation mode state shared by system-function use cases."""

    operation_mode: str
    available_modes: list[str]
    mode_writable: bool


class DHWSystemFunctionState(TypedDict):
    """Domestic-hot-water boost and operation mode state."""

    boost_status: str
    boost_writable: bool
    operation_mode: str
    available_modes: list[str]
    mode_writable: bool


class ConsumptionLimitState(TypedDict):
    """Current limitation-of-power-consumption state."""

    value_watts: float
    is_active: bool
    is_changeable: bool


class FailsafeState(TypedDict):
    """Current failsafe limit state."""

    value_watts: float
    duration_minimum_seconds: int


class HeartbeatState(TypedDict):
    """Current heartbeat state."""

    running: bool
    within_duration: bool


class CompressorFlexibilityState(TypedDict):
    """OHPCF compressor offer and process state."""

    available: bool
    state: str
    requested_power_estimate_w: float | None
    requested_power_max_w: float | None
    is_pausable: bool
    is_stoppable: bool
    minimal_run_seconds: int
    minimal_pause_seconds: int


class DeviceInfo(TypedDict, total=False):
    """Optional device classification fields reported by the bridge."""

    manufacturer: str
    model: str
    serial: str
    device_type: str


class CoordinatorSnapshot(TypedDict):
    """Complete atomically published coordinator polling snapshot."""

    connected: bool
    local_ski: str
    ski_registered: bool
    power_l1_w: float | None
    power_l2_w: float | None
    power_l3_w: float | None
    current_l1_a: float | None
    current_l2_a: float | None
    current_l3_a: float | None
    voltage_l1_v: float | None
    voltage_l2_v: float | None
    voltage_l3_v: float | None
    frequency_hz: float | None
    energy_produced_kwh: float | None
    dhw_temperature_c: float | None
    room_temperature_c: float | None
    outdoor_temperature_c: float | None
    flow_temperature_c: float | None
    return_temperature_c: float | None
    compressor_temperature_c: float | None
    compressor_power_w: float | None
    power_watts: float | None
    energy_consumed_heating_kwh: float | None
    energy_consumed_dhw_kwh: float | None
    energy_consumed_kwh: float | None
    consumption_limit: ConsumptionLimitState | None
    failsafe_limit: FailsafeState | None
    heartbeat_status: HeartbeatState | None
    heartbeat_supported: CapabilityState
    lpc_supported: CapabilityState
    failsafe_supported: CapabilityState
    device_info: DeviceInfo | None
    compressor_flexibility: CompressorFlexibilityState | None
    dhw_setpoint: SetpointState | None
    dhw_system_function: DHWSystemFunctionState | None
    room_heating_setpoint: SetpointState | None
    room_heating_system_function: SystemFunctionState | None
    device_operating_state: str | None
    ohpcf_supported: CapabilityState
    dhw_supported: CapabilityState
    dhw_sysfn_supported: CapabilityState
    room_heating_supported: CapabilityState


# Maps a GetMeasurements entry type (as emitted by the Go bridge) to the
# coordinator data key consumed by the per-phase / grid / produced-energy
# sensors. Types not present here (e.g. power_consumption, energy_consumed) are
# handled by their own dedicated reads.
FLAT_MEASUREMENT_TYPE_TO_KEY: dict[str, str] = {
    "power_l1": "power_l1_w",
    "power_l2": "power_l2_w",
    "power_l3": "power_l3_w",
    "current_l1": "current_l1_a",
    "current_l2": "current_l2_a",
    "current_l3": "current_l3_a",
    "voltage_l1": "voltage_l1_v",
    "voltage_l2": "voltage_l2_v",
    "voltage_l3": "voltage_l3_v",
    "frequency": "frequency_hz",
    "energy_produced": "energy_produced_kwh",
    "dhw_temperature": "dhw_temperature_c",
    "room_temperature": "room_temperature_c",
    "outdoor_temperature": "outdoor_temperature_c",
    "flow_temperature": "flow_temperature_c",
    "return_temperature": "return_temperature_c",
    "compressor_temperature": "compressor_temperature_c",
    "compressor_power": "compressor_power_w",
}
FLAT_MEASUREMENT_KEYS: tuple[str, ...] = tuple(FLAT_MEASUREMENT_TYPE_TO_KEY.values())


def _dhw_system_function_to_dict(state: proto_stubs.DHWSystemFunctionState) -> DHWSystemFunctionState:
    """Convert a DHW system-function protobuf state into coordinator data."""
    from . import proto_stubs

    status = proto_stubs.DHWBoostStatus.Name(state.boost_status)
    prefix = "DHW_BOOST_STATUS_"
    if status.startswith(prefix):
        status = status[len(prefix) :]
    return {
        "boost_status": status.lower(),
        "boost_writable": state.boost_writable,
        "operation_mode": state.operation_mode,
        "available_modes": list(state.available_modes),
        "mode_writable": state.mode_writable,
    }


def _setpoint_to_dict(
    setpoint: proto_stubs.DHWSetpoint | proto_stubs.RoomHeatingSetpoint,
) -> SetpointState:
    """Convert a protobuf setpoint (value/min/max/step/writable) to coordinator data."""
    return {
        "value_celsius": setpoint.value_celsius,
        "min_celsius": setpoint.min_celsius,
        "max_celsius": setpoint.max_celsius,
        "step_celsius": setpoint.step_celsius,
        "writable": setpoint.writable,
    }


def _system_function_to_dict(
    system_function: proto_stubs.RoomHeatingSystemFunction,
) -> SystemFunctionState:
    """Convert a protobuf system-function state to coordinator data."""
    return {
        "operation_mode": system_function.operation_mode,
        "available_modes": list(system_function.available_modes),
        "mode_writable": system_function.mode_writable,
    }


def _extract_scoped_energy_kwh(
    measurements: Sequence[proto_stubs.MeasurementEntry],
) -> dict[str, float | None]:
    """Extract Vaillant/EEBUS scoped counters for heating and domestic hot water."""
    result: dict[str, float | None] = {"heating": None, "dhw": None}
    for measurement in measurements:
        measurement_type = str(getattr(measurement, "type", "")).lower().strip()
        if not measurement_type:
            continue
        normalized = measurement_type.replace("-", "_").replace(" ", "_")
        value = getattr(measurement, "value", None)
        if value is None:
            continue

        # Vaillant uses separate thermal storage contexts for heating and DHW.
        if "energy" in normalized and (
            "domestic_hot_water" in normalized or "hot_water" in normalized or "dhw" in normalized
        ):
            result["dhw"] = cast(float, value)
            continue

        if "energy" in normalized and ("heating" in normalized or "space_heating" in normalized):
            result["heating"] = cast(float, value)

    return result


def _extract_flat_measurements(
    measurements: Sequence[proto_stubs.MeasurementEntry],
) -> dict[str, float | None]:
    """Map per-phase / grid / produced-energy entries to coordinator keys."""
    result: dict[str, float | None] = {}
    for measurement in measurements:
        measurement_type = str(getattr(measurement, "type", "")).lower().strip()
        if not measurement_type:
            continue
        normalized = measurement_type.replace("-", "_").replace(" ", "_")
        key = FLAT_MEASUREMENT_TYPE_TO_KEY.get(normalized)
        if key is None:
            continue
        value = getattr(measurement, "value", None)
        if value is None:
            continue
        result[key] = cast(float, value)
    return result
