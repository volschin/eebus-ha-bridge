"""Typed coordinator snapshot models and protobuf conversion helpers."""

from __future__ import annotations

from dataclasses import dataclass
from enum import StrEnum
from threading import Lock
from typing import TYPE_CHECKING, cast

from . import proto_stubs

if TYPE_CHECKING:
    from collections.abc import Sequence
    from datetime import datetime

class CapabilityState(StrEnum):
    """Availability state for an EEBUS use-case capability."""

    UNKNOWN = "unknown"
    AVAILABLE = "available"
    TEMPORARILY_UNAVAILABLE = "temporarily_unavailable"
    UNSUPPORTED = "unsupported"


@dataclass(frozen=True, slots=True)
class SetpointState:
    """Temperature setpoint and its device-provided constraints."""

    value_celsius: float
    min_celsius: float
    max_celsius: float
    step_celsius: float
    writable: bool


@dataclass(frozen=True, slots=True)
class SystemFunctionState:
    """Operation mode state shared by system-function use cases."""

    operation_mode: str
    available_modes: tuple[str, ...]
    mode_writable: bool


@dataclass(frozen=True, slots=True)
class DHWSystemFunctionState:
    """Domestic-hot-water boost and operation mode state."""

    boost_status: str
    boost_writable: bool
    operation_mode: str
    available_modes: tuple[str, ...]
    mode_writable: bool


@dataclass(frozen=True, slots=True)
class ConsumptionLimitState:
    """Current limitation-of-power-consumption state."""

    value_watts: float
    is_active: bool
    is_changeable: bool


@dataclass(frozen=True, slots=True)
class FailsafeState:
    """Current failsafe limit state."""

    value_watts: float
    duration_minimum_seconds: int


@dataclass(frozen=True, slots=True)
class HeartbeatState:
    """Current heartbeat state."""

    running: bool
    within_duration: bool


@dataclass(frozen=True, slots=True)
class CompressorFlexibilityState:
    """OHPCF compressor offer and process state."""

    available: bool
    state: str
    requested_power_estimate_w: float | None
    requested_power_max_w: float | None
    is_pausable: bool
    is_stoppable: bool
    minimal_run_seconds: int
    minimal_pause_seconds: int
    start_time: datetime | None = None


@dataclass(frozen=True, slots=True)
class DeviceInfo:
    """Optional device classification fields reported by the bridge."""

    manufacturer: str | None = None
    model: str | None = None
    serial: str | None = None
    device_type: str | None = None
    sw_version: str | None = None
    hw_version: str | None = None


@dataclass(frozen=True, slots=True)
class RoomHeatingValues:
    """Fields returned together by the room-heating aggregate."""

    setpoint: SetpointState | None
    system_function: SystemFunctionState | None
    current_temperature_celsius: float | None


# Maps a GetMeasurements entry type (as emitted by the Go bridge) to the
# coordinator data key consumed by the per-phase / grid / produced-energy
# sensors. Types not present here (e.g. power_consumption, energy_consumed) are
# handled by their own dedicated reads.
LEGACY_MEASUREMENT_CATALOG: dict[str, tuple[str, str]] = {
    "power_l1": ("power_l1_w", "W"),
    "power_l2": ("power_l2_w", "W"),
    "power_l3": ("power_l3_w", "W"),
    "current_l1": ("current_l1_a", "A"),
    "current_l2": ("current_l2_a", "A"),
    "current_l3": ("current_l3_a", "A"),
    "voltage_l1": ("voltage_l1_v", "V"),
    "voltage_l2": ("voltage_l2_v", "V"),
    "voltage_l3": ("voltage_l3_v", "V"),
    "frequency": ("frequency_hz", "Hz"),
    "energy_produced": ("energy_produced_kwh", "kWh"),
    "dhw_temperature": ("dhw_temperature_c", "degC"),
    "room_temperature": ("room_temperature_c", "degC"),
    "outdoor_temperature": ("outdoor_temperature_c", "degC"),
    "flow_temperature": ("flow_temperature_c", "degC"),
    "return_temperature": ("return_temperature_c", "degC"),
    "compressor_temperature": ("compressor_temperature_c", "degC"),
    "compressor_power": ("compressor_power_w", "W"),
}
FLAT_MEASUREMENT_KEYS: tuple[str, ...] = tuple(field for field, _unit in LEGACY_MEASUREMENT_CATALOG.values())

# Single stable-ID mapping for every catalog value consumed by HA. Values are
# StateField wire values; keeping models independent of state.py avoids a cycle.
MEASUREMENT_ID_CATALOG: dict[int, tuple[str, str]] = {
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_POWER_CONSUMPTION): ("power_watts", "W"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_ENERGY_CONSUMED): ("energy_consumed_kwh", "kWh"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_POWER_L1): ("power_l1_w", "W"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_POWER_L2): ("power_l2_w", "W"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_POWER_L3): ("power_l3_w", "W"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_CURRENT_L1): ("current_l1_a", "A"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_CURRENT_L2): ("current_l2_a", "A"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_CURRENT_L3): ("current_l3_a", "A"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_VOLTAGE_L1): ("voltage_l1_v", "V"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_VOLTAGE_L2): ("voltage_l2_v", "V"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_VOLTAGE_L3): ("voltage_l3_v", "V"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_FREQUENCY): ("frequency_hz", "Hz"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_ENERGY_PRODUCED): ("energy_produced_kwh", "kWh"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_DHW_TEMPERATURE): ("dhw_temperature_c", "degC"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_ROOM_TEMPERATURE): ("room_temperature_c", "degC"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_OUTDOOR_TEMPERATURE): ("outdoor_temperature_c", "degC"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_FLOW_TEMPERATURE): ("flow_temperature_c", "degC"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_RETURN_TEMPERATURE): ("return_temperature_c", "degC"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_COMPRESSOR_TEMPERATURE): ("compressor_temperature_c", "degC"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_COMPRESSOR_POWER): ("compressor_power_w", "W"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_ENERGY_CONSUMED_HEATING): ("energy_consumed_heating_kwh", "kWh"),
    int(proto_stubs.MeasurementId.MEASUREMENT_ID_ENERGY_CONSUMED_DHW): ("energy_consumed_dhw_kwh", "kWh"),
}
LEGACY_SCOPED_ENERGY_CATALOG: dict[str, str] = {
    "energy_consumed_heating": "heating",
    "energy_consumed_dhw": "dhw",
}
_MEASUREMENT_DIAGNOSTICS = {
    "unknown_typed_ids": 0,
    "invalid_typed_units": 0,
    "invalid_legacy_units": 0,
    "extension_strings": 0,
}
_MEASUREMENT_DIAGNOSTICS_LOCK = Lock()


def measurement_diagnostics() -> dict[str, int]:
    """Return process-local counters for safely ignored measurement entries."""
    with _MEASUREMENT_DIAGNOSTICS_LOCK:
        return dict(_MEASUREMENT_DIAGNOSTICS)


def _count_ignored_measurement(key: str) -> None:
    with _MEASUREMENT_DIAGNOSTICS_LOCK:
        _MEASUREMENT_DIAGNOSTICS[key] += 1


def _measurement_state_field(measurement: proto_stubs.MeasurementEntry) -> str | None:
    """Prefer a stable ID and use the legacy string only when ID is absent."""
    has_field = getattr(measurement, "HasField", None)
    if callable(has_field) and has_field("id"):
        definition = MEASUREMENT_ID_CATALOG.get(int(measurement.id))
        if definition is None:
            _count_ignored_measurement("unknown_typed_ids")
            return None
        field, canonical_unit = definition
        if measurement.unit != canonical_unit:
            _count_ignored_measurement("invalid_typed_units")
            return None
        return field
    measurement_type = measurement.type.lower().strip()
    if not measurement_type:
        return None
    normalized = measurement_type.replace("-", "_").replace(" ", "_")
    legacy_definition = LEGACY_MEASUREMENT_CATALOG.get(normalized)
    if legacy_definition is None:
        _count_ignored_measurement("extension_strings")
        return None
    legacy_field, canonical_unit = legacy_definition
    if getattr(measurement, "unit", "") != canonical_unit:
        _count_ignored_measurement("invalid_legacy_units")
        return None
    return legacy_field


def _dhw_system_function_to_dict(state: proto_stubs.DHWSystemFunctionState) -> DHWSystemFunctionState:
    """Convert a DHW system-function protobuf state into coordinator data."""
    from . import proto_stubs

    status = proto_stubs.DHWBoostStatus.Name(state.boost_status)
    prefix = "DHW_BOOST_STATUS_"
    if status.startswith(prefix):
        status = status[len(prefix) :]
    return DHWSystemFunctionState(
        boost_status=status.lower(),
        boost_writable=state.boost_writable,
        operation_mode=state.operation_mode,
        available_modes=tuple(state.available_modes),
        mode_writable=state.mode_writable,
    )


def _setpoint_to_dict(
    setpoint: proto_stubs.DHWSetpoint | proto_stubs.RoomHeatingSetpoint,
) -> SetpointState:
    """Convert a protobuf setpoint (value/min/max/step/writable) to coordinator data."""
    return SetpointState(
        value_celsius=setpoint.value_celsius,
        min_celsius=setpoint.min_celsius,
        max_celsius=setpoint.max_celsius,
        step_celsius=setpoint.step_celsius,
        writable=setpoint.writable,
    )


def _system_function_to_dict(
    system_function: proto_stubs.RoomHeatingSystemFunction,
) -> SystemFunctionState:
    """Convert a protobuf system-function state to coordinator data."""
    return SystemFunctionState(
        operation_mode=system_function.operation_mode,
        available_modes=tuple(system_function.available_modes),
        mode_writable=system_function.mode_writable,
    )


def _room_heating_from_proto(state: proto_stubs.RoomHeatingState) -> RoomHeatingValues:
    """Convert the aggregate identically for polling and streaming paths."""
    return RoomHeatingValues(
        setpoint=_setpoint_to_dict(state.setpoint) if state.HasField("setpoint") else None,
        system_function=(
            _system_function_to_dict(state.system_function) if state.HasField("system_function") else None
        ),
        current_temperature_celsius=(
            state.current_temperature_celsius if state.HasField("current_temperature_celsius") else None
        ),
    )


def _extract_scoped_energy_kwh(
    measurements: Sequence[proto_stubs.MeasurementEntry],
) -> dict[str, float | None]:
    """Extract Vaillant/EEBUS scoped counters for heating and domestic hot water."""
    result: dict[str, float | None] = {"heating": None, "dhw": None}
    for measurement in measurements:
        has_field = getattr(measurement, "HasField", None)
        if callable(has_field) and has_field("id"):
            definition = MEASUREMENT_ID_CATALOG.get(int(measurement.id))
            if definition is None or measurement.unit != definition[1]:
                continue
            field = definition[0]
            if field == "energy_consumed_heating_kwh":
                result["heating"] = measurement.value
            elif field == "energy_consumed_dhw_kwh":
                result["dhw"] = measurement.value
            continue
        measurement_type = str(getattr(measurement, "type", "")).lower().strip()
        if not measurement_type:
            continue
        normalized = measurement_type.replace("-", "_").replace(" ", "_")
        if getattr(measurement, "unit", "") != "kWh":
            _count_ignored_measurement("invalid_legacy_units")
            continue
        value = getattr(measurement, "value", None)
        if value is None:
            continue

        scope = LEGACY_SCOPED_ENERGY_CATALOG.get(normalized)
        if scope is not None:
            result[scope] = cast(float, value)

    return result


def _extract_flat_measurements(
    measurements: Sequence[proto_stubs.MeasurementEntry],
) -> dict[str, float | None]:
    """Map per-phase / grid / produced-energy entries to coordinator keys."""
    result: dict[str, float | None] = {}
    for measurement in measurements:
        key = _measurement_state_field(measurement)
        if key is None:
            continue
        value = getattr(measurement, "value", None)
        if value is None:
            continue
        result[key] = cast(float, value)
    return result
