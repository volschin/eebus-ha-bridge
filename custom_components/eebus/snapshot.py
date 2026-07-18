"""Atomic EEBUS polling snapshot construction without Home Assistant side effects."""

from __future__ import annotations

import asyncio
import logging
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, replace
from datetime import UTC
from typing import Generic, Protocol, TypeVar, cast

import grpc
import grpc.aio

from . import proto_stubs
from .grpc_client import (
    RPC_TIMEOUT,
    is_not_found as _is_not_found,
    is_unimplemented as _is_unimplemented,
    rpc_error_text as _rpc_error_text,
)
from .models import (
    CapabilityState,
    FLAT_MEASUREMENT_KEYS,
    CompressorFlexibilityState,
    ConsumptionLimitState,
    DHWSystemFunctionState,
    DeviceInfo,
    FailsafeState,
    HeartbeatState,
    RoomHeatingValues,
    SetpointState,
    _dhw_system_function_to_dict,
    _extract_flat_measurements,
    _extract_scoped_energy_kwh,
    _room_heating_from_proto,
    _setpoint_to_dict,
)
from .state import (
    CapabilityKey,
    CapabilityResult,
    ConnectionState,
    DHWState,
    DeviceState,
    DeviceStateStore,
    HVACState,
    LPCState,
    MeasurementsState,
    OHPCFState,
    StateField,
    StateObservation,
)

_LOGGER = logging.getLogger(__name__)
_LEGACY_CAPABILITY_WARNED: set[str] = set()

RE_REGISTER_NOT_FOUND_STREAK = 4

_ResponseT = TypeVar("_ResponseT")
_ResponseT_co = TypeVar("_ResponseT_co", covariant=True)


class _ReadCall(Protocol[_ResponseT_co]):
    """Typed shape of an asynchronous unary device read."""

    def __call__(self, request: proto_stubs.DeviceRequest, *, timeout: float) -> Awaitable[_ResponseT_co]: ...


@dataclass(frozen=True, slots=True)
class _ReadResult(Generic[_ResponseT]):
    """One best-effort read plus its deferred support result."""

    value: _ResponseT | None
    saw_not_found: bool = False
    status: grpc.StatusCode | None = None


@dataclass(frozen=True, slots=True)
class SnapshotResult:
    """One polling observation and poller-owned lifecycle updates."""

    observation: StateObservation
    ski_registered: bool
    not_found_streak: int


_CAPABILITY_KEYS = {
    proto_stubs.CapabilityId.CAPABILITY_MONITORING: CapabilityKey.MONITORING,
    proto_stubs.CapabilityId.CAPABILITY_LPC: CapabilityKey.LPC,
    proto_stubs.CapabilityId.CAPABILITY_FAILSAFE: CapabilityKey.FAILSAFE,
    proto_stubs.CapabilityId.CAPABILITY_HEARTBEAT: CapabilityKey.HEARTBEAT,
    proto_stubs.CapabilityId.CAPABILITY_OHPCF: CapabilityKey.OHPCF,
    proto_stubs.CapabilityId.CAPABILITY_DHW: CapabilityKey.DHW,
    proto_stubs.CapabilityId.CAPABILITY_DHW_SYSTEM_FUNCTION: CapabilityKey.DHW_SYSTEM_FUNCTION,
    proto_stubs.CapabilityId.CAPABILITY_ROOM_HEATING: CapabilityKey.ROOM_HEATING,
}
_CAPABILITY_STATES = {
    proto_stubs.CapabilityState.CAPABILITY_STATE_UNKNOWN: CapabilityState.UNKNOWN,
    proto_stubs.CapabilityState.CAPABILITY_STATE_AVAILABLE: CapabilityState.AVAILABLE,
    proto_stubs.CapabilityState.CAPABILITY_STATE_TEMPORARILY_UNAVAILABLE: CapabilityState.TEMPORARILY_UNAVAILABLE,
    proto_stubs.CapabilityState.CAPABILITY_STATE_UNSUPPORTED: CapabilityState.UNSUPPORTED,
}
_CAPABILITY_REASONS = {
    proto_stubs.CapabilityReason.CAPABILITY_REASON_UNSPECIFIED: None,
    proto_stubs.CapabilityReason.CAPABILITY_REASON_LOCAL_DISABLED: "local_disabled",
    proto_stubs.CapabilityReason.CAPABILITY_REASON_REMOTE_NOT_ADVERTISED: "remote_not_advertised",
    proto_stubs.CapabilityReason.CAPABILITY_REASON_ENTITY_NOT_BOUND: "entity_not_bound",
    proto_stubs.CapabilityReason.CAPABILITY_REASON_READ_FAILED: "read_failed",
    proto_stubs.CapabilityReason.CAPABILITY_REASON_DEVICE_DISCONNECTED: "device_disconnected",
}
_SNAPSHOT_FIELD_TO_STATE_FIELD = {
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_CONNECTED: StateField.CONNECTED,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_LOCAL_SKI: StateField.LOCAL_SKI,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_DEVICE_INFO: StateField.DEVICE_INFO,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_DEVICE_OPERATING_STATE: StateField.DEVICE_OPERATING_STATE,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_POWER_L1: StateField.POWER_L1_W,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_POWER_L2: StateField.POWER_L2_W,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_POWER_L3: StateField.POWER_L3_W,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_CURRENT_L1: StateField.CURRENT_L1_A,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_CURRENT_L2: StateField.CURRENT_L2_A,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_CURRENT_L3: StateField.CURRENT_L3_A,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_VOLTAGE_L1: StateField.VOLTAGE_L1_V,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_VOLTAGE_L2: StateField.VOLTAGE_L2_V,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_VOLTAGE_L3: StateField.VOLTAGE_L3_V,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_FREQUENCY: StateField.FREQUENCY_HZ,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_ENERGY_PRODUCED: StateField.ENERGY_PRODUCED_KWH,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_DHW_TEMPERATURE: StateField.DHW_TEMPERATURE_C,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_ROOM_TEMPERATURE: StateField.ROOM_TEMPERATURE_C,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_OUTDOOR_TEMPERATURE: StateField.OUTDOOR_TEMPERATURE_C,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_FLOW_TEMPERATURE: StateField.FLOW_TEMPERATURE_C,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_RETURN_TEMPERATURE: StateField.RETURN_TEMPERATURE_C,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_COMPRESSOR_TEMPERATURE: StateField.COMPRESSOR_TEMPERATURE_C,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_COMPRESSOR_POWER: StateField.COMPRESSOR_POWER_W,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_POWER: StateField.POWER_WATTS,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_ENERGY_CONSUMED_HEATING: StateField.ENERGY_CONSUMED_HEATING_KWH,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_ENERGY_CONSUMED_DHW: StateField.ENERGY_CONSUMED_DHW_KWH,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_ENERGY_CONSUMED: StateField.ENERGY_CONSUMED_KWH,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_CONSUMPTION_LIMIT: StateField.CONSUMPTION_LIMIT,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_FAILSAFE_LIMIT: StateField.FAILSAFE_LIMIT,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_HEARTBEAT_STATUS: StateField.HEARTBEAT_STATUS,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_COMPRESSOR_FLEXIBILITY: StateField.COMPRESSOR_FLEXIBILITY,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_DHW_SETPOINT: StateField.DHW_SETPOINT,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_DHW_SYSTEM_FUNCTION: StateField.DHW_SYSTEM_FUNCTION,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_ROOM_HEATING_SETPOINT: StateField.ROOM_HEATING_SETPOINT,
    proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_ROOM_HEATING_SYSTEM_FUNCTION: StateField.ROOM_HEATING_SYSTEM_FUNCTION,
}


async def _async_read_capabilities(
    device_stub: proto_stubs.DeviceServiceStub,
    request: proto_stubs.DeviceRequest,
    ski: str,
) -> tuple[CapabilityResult, ...] | None:
    """Read the explicit bridge contract; ``None`` means legacy fallback only."""
    try:
        response: proto_stubs.DeviceCapabilities = await device_stub.GetDeviceCapabilities(request, timeout=RPC_TIMEOUT)
    except grpc.aio.AioRpcError as err:
        if _is_unimplemented(err):
            if ski not in _LEGACY_CAPABILITY_WARNED:
                _LEGACY_CAPABILITY_WARNED.add(ski)
                _LOGGER.warning(
                    "Bridge lacks GetDeviceCapabilities; using legacy gRPC-status capability inference for SKI %s",
                    ski,
                )
            return None
        _LOGGER.debug("Capability contract read failed for SKI %s: %s", ski, _rpc_error_text(err))
        return None

    return _capability_results_from_proto(response)


def _capability_results_from_proto(
    response: proto_stubs.DeviceCapabilities,
) -> tuple[CapabilityResult, ...]:
    """Convert one explicit capability snapshot for polling or streaming."""
    results: list[CapabilityResult] = []
    for entry in response.capabilities:
        key = _CAPABILITY_KEYS.get(entry.id)
        state = _CAPABILITY_STATES.get(entry.state)
        if key is None or state is None:
            continue
        last_changed = entry.last_changed.ToDatetime() if entry.HasField("last_changed") else None
        results.append(
            CapabilityResult(
                key,
                None,
                explicit_support=True,
                explicit_state=state,
                reason=_CAPABILITY_REASONS.get(entry.reason),
                last_changed=last_changed,
            )
        )
    return tuple(results)


async def _poll_read(
    label: str,
    call: _ReadCall[_ResponseT],
    request: proto_stubs.DeviceRequest,
    ski: str,
) -> _ReadResult[_ResponseT]:
    """Call a device-scoped read RPC once and return deferred status metadata."""
    try:
        response = await call(request, timeout=RPC_TIMEOUT)
    except grpc.aio.AioRpcError as err:
        _LOGGER.debug(
            "EEBUS %s read failed for SKI %s: %s",
            label,
            ski,
            _rpc_error_text(err),
        )
        return _ReadResult(
            None,
            _is_not_found(err),
            status=err.code(),
        )
    except Exception:  # noqa: BLE001
        _LOGGER.exception("Failed to read %s", label)
        return _ReadResult(
            None,
            status=grpc.StatusCode.UNKNOWN,
        )
    _LOGGER.debug("EEBUS %s read for SKI %s succeeded", label, ski)
    return _ReadResult(response)


async def _async_register_remote_ski(
    device_stub: proto_stubs.DeviceServiceStub,
    ski: str,
    *,
    force: bool,
    registered: bool,
) -> bool:
    """Register remote SKI with bridge, optionally forcing re-registration."""
    try:
        await device_stub.RegisterRemoteSKI(proto_stubs.RegisterSKIRequest(ski=ski), timeout=RPC_TIMEOUT)
        if force:
            _LOGGER.info("Forced re-registration of remote SKI %s with bridge", ski)
        else:
            _LOGGER.info("Registered remote SKI %s with bridge", ski)
        return True
    except grpc.aio.AioRpcError as err:
        if force:
            _LOGGER.warning(
                "Forced remote SKI re-registration failed for %s: %s",
                ski,
                _rpc_error_text(err),
            )
        else:
            # Retry in next polling cycle until the bridge accepts registration.
            _LOGGER.debug(
                "Remote SKI registration pending for %s: %s",
                ski,
                _rpc_error_text(err),
            )
        return registered


async def _async_fetch_device_info(device_stub: proto_stubs.DeviceServiceStub, ski: str) -> DeviceInfo | None:
    """Read classification metadata for the configured device, best-effort."""
    try:
        response = await device_stub.ListPairedDevices(proto_stubs.Empty(), timeout=RPC_TIMEOUT)
    except grpc.aio.AioRpcError as err:
        if not _is_unimplemented(err):
            _LOGGER.debug(
                "ListPairedDevices failed for SKI %s: %s",
                ski,
                _rpc_error_text(err),
            )
        return None
    except Exception:  # noqa: BLE001
        _LOGGER.exception("Failed to list paired devices")
        return None

    devices = list(response.devices)
    if not devices:
        return None

    match = next((device for device in devices if device.ski == ski), None)
    if match is None:
        return None

    info = DeviceInfo(
        manufacturer=match.brand or None,
        model=match.model or None,
        serial=match.serial or None,
        device_type=match.device_type or None,
    )
    return info if any((info.manufacturer, info.model, info.serial, info.device_type)) else None


async def _async_read_compressor_flexibility(
    channel: grpc.aio.Channel,
    request: proto_stubs.DeviceRequest,
    ski: str,
) -> _ReadResult[CompressorFlexibilityState]:
    """Read the OHPCF compressor flexibility offer/state, or None when off."""
    try:
        stub = proto_stubs.ohpcf_service_stub(channel)
        flex: proto_stubs.CompressorFlexibility = await stub.GetCompressorFlexibility(request, timeout=RPC_TIMEOUT)
    except grpc.aio.AioRpcError as err:
        if not _is_unimplemented(err):
            _LOGGER.debug(
                "EEBUS OHPCF read failed for SKI %s: %s",
                ski,
                _rpc_error_text(err),
            )
        return _ReadResult(None, status=err.code())

    value = CompressorFlexibilityState(
        available=flex.available,
        state=proto_stubs.CompressorPowerConsumptionState.Name(flex.state),
        requested_power_estimate_w=(
            flex.requested_power_estimate_w if flex.HasField("requested_power_estimate_w") else None
        ),
        requested_power_max_w=(flex.requested_power_max_w if flex.HasField("requested_power_max_w") else None),
        is_pausable=flex.is_pausable,
        is_stoppable=flex.is_stoppable,
        minimal_run_seconds=flex.minimal_run_seconds,
        minimal_pause_seconds=flex.minimal_pause_seconds,
        start_time=flex.start_time.ToDatetime(tzinfo=UTC) if flex.HasField("start_time") else None,
    )
    return _ReadResult(value)


async def _async_read_dhw_setpoint(
    channel: grpc.aio.Channel,
    request: proto_stubs.DeviceRequest,
    ski: str,
) -> _ReadResult[SetpointState]:
    """Read the DHW target and device-provided constraints."""
    try:
        stub = proto_stubs.dhw_service_stub(channel)
        setpoint: proto_stubs.DHWSetpoint = await stub.GetDHWSetpoint(request, timeout=RPC_TIMEOUT)
    except grpc.aio.AioRpcError as err:
        if not _is_unimplemented(err):
            _LOGGER.debug(
                "EEBUS DHW setpoint read failed for SKI %s: %s",
                ski,
                _rpc_error_text(err),
            )
        return _ReadResult(None, status=err.code())
    return _ReadResult(_setpoint_to_dict(setpoint))


async def _async_read_dhw_system_function(
    channel: grpc.aio.Channel,
    request: proto_stubs.DeviceRequest,
    ski: str,
) -> _ReadResult[DHWSystemFunctionState]:
    """Read DHW boost and operation mode state."""
    try:
        stub = proto_stubs.dhw_service_stub(channel)
        state: proto_stubs.DHWSystemFunctionState = await stub.GetDHWSystemFunction(request, timeout=RPC_TIMEOUT)
    except grpc.aio.AioRpcError as err:
        if not _is_unimplemented(err):
            _LOGGER.debug(
                "EEBUS DHW system function read failed for SKI %s: %s",
                ski,
                _rpc_error_text(err),
            )
        return _ReadResult(None, status=err.code())
    return _ReadResult(_dhw_system_function_to_dict(state))


async def _async_read_room_heating(
    channel: grpc.aio.Channel,
    request: proto_stubs.DeviceRequest,
) -> _ReadResult[RoomHeatingValues]:
    """Read all room-heating fields without publishing a partial update."""
    try:
        state: proto_stubs.RoomHeatingState = await proto_stubs.hvac_service_stub(channel).GetRoomHeating(
            request, timeout=RPC_TIMEOUT
        )
    except grpc.aio.AioRpcError as err:
        return _ReadResult(
            None,
            status=err.code(),
        )

    return _ReadResult(_room_heating_from_proto(state))


async def _async_read_device_diagnostics(
    channel: grpc.aio.Channel, request: proto_stubs.DeviceRequest, ski: str
) -> _ReadResult[str]:
    """Read the device operating state."""
    try:
        diagnostics: proto_stubs.DeviceDiagnosticsData = await proto_stubs.monitoring_service_stub(
            channel
        ).GetDeviceDiagnostics(request, timeout=RPC_TIMEOUT)
    except grpc.aio.AioRpcError as err:
        if not (_is_unimplemented(err) or err.code() in (grpc.StatusCode.NOT_FOUND, grpc.StatusCode.UNAVAILABLE)):
            _LOGGER.debug(
                "EEBUS device diagnosis read failed for SKI %s: %s",
                ski,
                _rpc_error_text(err),
            )
        return _ReadResult(None, status=err.code())
    return _ReadResult(diagnostics.operating_state or None)


async def async_build_snapshot(
    channel: grpc.aio.Channel,
    ski: str,
    *,
    ski_registered: bool,
    not_found_streak: int,
    supports_explicit_capabilities: bool = True,
) -> SnapshotResult:
    """Read and assemble one complete coordinator snapshot atomically."""
    device_stub = proto_stubs.device_service_stub(channel)
    registered = ski_registered
    if not registered:
        registered = await _async_register_remote_ski(
            device_stub,
            ski,
            force=False,
            registered=registered,
        )

    monitoring_stub = proto_stubs.monitoring_service_stub(channel)
    lpc_stub = proto_stubs.lpc_service_stub(channel)
    request = proto_stubs.DeviceRequest(ski=ski)
    (
        status,
        power,
        measurements,
        energy,
        limit,
        failsafe,
        heartbeat,
        device_status,
        device_info,
        compressor,
        dhw_setpoint,
        dhw_system_function,
        room_heating,
        diagnostics,
        explicit_capabilities,
    ) = cast(
        tuple[
            proto_stubs.ServiceStatus,
            _ReadResult[proto_stubs.PowerMeasurement],
            _ReadResult[proto_stubs.MeasurementList],
            _ReadResult[proto_stubs.EnergyMeasurement],
            _ReadResult[proto_stubs.LoadLimit],
            _ReadResult[proto_stubs.FailsafeLimit],
            _ReadResult[proto_stubs.HeartbeatStatus],
            proto_stubs.DeviceStatus,
            DeviceInfo | None,
            _ReadResult[CompressorFlexibilityState],
            _ReadResult[SetpointState],
            _ReadResult[DHWSystemFunctionState],
            _ReadResult[RoomHeatingValues],
            _ReadResult[str],
            tuple[CapabilityResult, ...] | None,
        ],
        await asyncio.gather(
            cast(
                Awaitable[proto_stubs.ServiceStatus],
                device_stub.GetStatus(proto_stubs.Empty(), timeout=RPC_TIMEOUT),
            ),
            _poll_read(
                "power",
                cast(_ReadCall[proto_stubs.PowerMeasurement], monitoring_stub.GetPowerConsumption),
                request,
                ski,
            ),
            _poll_read(
                "scoped energy",
                cast(_ReadCall[proto_stubs.MeasurementList], monitoring_stub.GetMeasurements),
                request,
                ski,
            ),
            _poll_read(
                "total energy",
                cast(_ReadCall[proto_stubs.EnergyMeasurement], monitoring_stub.GetEnergyConsumed),
                request,
                ski,
            ),
            _poll_read(
                "consumption limit",
                cast(_ReadCall[proto_stubs.LoadLimit], lpc_stub.GetConsumptionLimit),
                request,
                ski,
            ),
            _poll_read(
                "failsafe",
                cast(_ReadCall[proto_stubs.FailsafeLimit], lpc_stub.GetFailsafeLimit),
                request,
                ski,
            ),
            _poll_read(
                "heartbeat",
                cast(_ReadCall[proto_stubs.HeartbeatStatus], lpc_stub.GetHeartbeatStatus),
                request,
                ski,
            ),
            cast(
                Awaitable[proto_stubs.DeviceStatus],
                device_stub.GetDeviceStatus(request, timeout=RPC_TIMEOUT),
            ),
            _async_fetch_device_info(device_stub, ski),
            _async_read_compressor_flexibility(channel, request, ski),
            _async_read_dhw_setpoint(channel, request, ski),
            _async_read_dhw_system_function(channel, request, ski),
            _async_read_room_heating(channel, request),
            _async_read_device_diagnostics(channel, request, ski),
            (
                _async_read_capabilities(device_stub, request, ski)
                if supports_explicit_capabilities
                else asyncio.sleep(0, result=None)
            ),
        ),
    )

    if ski == status.local_ski:
        _LOGGER.warning(
            "Configured remote SKI %s matches bridge local SKI; monitoring reads will stay empty",
            ski,
        )

    flat_measurements: dict[str, float | None] = {}
    scoped_energy: dict[str, float | None] = {"heating": None, "dhw": None}
    if measurements.value is not None:
        scoped_energy = _extract_scoped_energy_kwh(measurements.value.measurements)
        flat_measurements = _extract_flat_measurements(measurements.value.measurements)

    room_heating_value = room_heating.value
    room_temperature_c = flat_measurements.get("room_temperature_c")
    if room_heating_value is not None and room_heating_value.current_temperature_celsius is not None:
        room_temperature_c = room_heating_value.current_temperature_celsius

    connection = replace(
        ConnectionState(),
        connected=device_status.connected,
        local_ski=status.local_ski,
        ski_registered=registered,
        device_info=device_info,
        device_operating_state=diagnostics.value,
    )
    measurement_state = replace(MeasurementsState(), **flat_measurements)
    measurement_state = replace(
        measurement_state,
        room_temperature_c=room_temperature_c,
        power_watts=power.value.watts if power.value is not None else None,
        energy_consumed_heating_kwh=scoped_energy["heating"],
        energy_consumed_dhw_kwh=scoped_energy["dhw"],
        energy_consumed_kwh=(energy.value.kilowatt_hours if energy.value is not None else None),
    )
    consumption_limit: ConsumptionLimitState | None = None
    if limit.value is not None:
        consumption_limit = ConsumptionLimitState(
            value_watts=limit.value.value_watts,
            is_active=limit.value.is_active,
            is_changeable=limit.value.is_changeable,
        )

    failsafe_limit: FailsafeState | None = None
    if failsafe.value is not None:
        failsafe_limit = FailsafeState(
            value_watts=failsafe.value.value_watts,
            duration_minimum_seconds=failsafe.value.duration_minimum_seconds,
        )

    heartbeat_status: HeartbeatState | None = None
    if heartbeat.value is not None:
        heartbeat_status = HeartbeatState(
            running=heartbeat.value.running,
            within_duration=heartbeat.value.within_duration,
        )

    state = DeviceState(
        connection=connection,
        measurements=measurement_state,
        lpc=LPCState(
            consumption_limit=consumption_limit,
            failsafe_limit=failsafe_limit,
            heartbeat_status=heartbeat_status,
        ),
        dhw=DHWState(
            setpoint=dhw_setpoint.value,
            system_function=dhw_system_function.value,
        ),
        hvac=HVACState(
            setpoint=(room_heating_value.setpoint if room_heating_value is not None else None),
            system_function=(room_heating_value.system_function if room_heating_value is not None else None),
        ),
        ohpcf=OHPCFState(compressor_flexibility=compressor.value),
    )

    observed_fields = {
        StateField.CONNECTED,
        StateField.LOCAL_SKI,
        StateField.SKI_REGISTERED,
        StateField.DEVICE_INFO,
    }
    unavailable_fields: set[StateField] = set()
    measurement_fields = {
        *(StateField(key) for key in FLAT_MEASUREMENT_KEYS),
        StateField.ENERGY_CONSUMED_HEATING_KWH,
        StateField.ENERGY_CONSUMED_DHW_KWH,
    }
    if measurements.status is None:
        observed_fields.update(measurement_fields)
    else:
        unavailable_fields.update(measurement_fields)
    if power.status is None:
        observed_fields.add(StateField.POWER_WATTS)
    else:
        unavailable_fields.add(StateField.POWER_WATTS)
    if energy.status is None:
        observed_fields.add(StateField.ENERGY_CONSUMED_KWH)
    else:
        unavailable_fields.add(StateField.ENERGY_CONSUMED_KWH)
    if diagnostics.status is None:
        observed_fields.add(StateField.DEVICE_OPERATING_STATE)
    else:
        unavailable_fields.add(StateField.DEVICE_OPERATING_STATE)

    capability_reads = (
        (CapabilityKey.LPC, limit, StateField.CONSUMPTION_LIMIT),
        (CapabilityKey.FAILSAFE, failsafe, StateField.FAILSAFE_LIMIT),
        (CapabilityKey.HEARTBEAT, heartbeat, StateField.HEARTBEAT_STATUS),
        (CapabilityKey.OHPCF, compressor, StateField.COMPRESSOR_FLEXIBILITY),
        (CapabilityKey.DHW, dhw_setpoint, StateField.DHW_SETPOINT),
        (
            CapabilityKey.DHW_SYSTEM_FUNCTION,
            dhw_system_function,
            StateField.DHW_SYSTEM_FUNCTION,
        ),
    )
    compatibility_capability_results: list[CapabilityResult] = []
    for capability, result, field_name in capability_reads:
        compatibility_capability_results.append(CapabilityResult(capability, result.status))
        if result.status is None:
            observed_fields.add(field_name)
        else:
            unavailable_fields.add(field_name)

    compatibility_capability_results.append(CapabilityResult(CapabilityKey.ROOM_HEATING, room_heating.status))
    room_fields = {
        StateField.ROOM_HEATING_SETPOINT,
        StateField.ROOM_HEATING_SYSTEM_FUNCTION,
    }
    if room_heating.status is None:
        observed_fields.update(room_fields)
        if room_heating_value is not None and room_heating_value.current_temperature_celsius is not None:
            observed_fields.add(StateField.ROOM_TEMPERATURE_C)
    else:
        unavailable_fields.update(room_fields)

    capability_results = (
        tuple(compatibility_capability_results) if explicit_capabilities is None else explicit_capabilities
    )

    saw_not_found = any(result.saw_not_found for result in (power, measurements, energy))
    updated_not_found_streak = not_found_streak + 1 if saw_not_found else 0
    if updated_not_found_streak >= RE_REGISTER_NOT_FOUND_STREAK:
        _LOGGER.warning(
            "EEBUS reads returned NOT_FOUND for %s consecutive polls; forcing remote SKI re-registration for %s",
            updated_not_found_streak,
            ski,
        )
        registered = await _async_register_remote_ski(
            device_stub,
            ski,
            force=True,
            registered=registered,
        )
        updated_not_found_streak = 0

    _LOGGER.debug(
        "EEBUS poll summary for SKI %s: power=%s energy_total=%s energy_heating=%s energy_dhw=%s",
        ski,
        state.measurements.power_watts,
        state.measurements.energy_consumed_kwh,
        state.measurements.energy_consumed_heating_kwh,
        state.measurements.energy_consumed_dhw_kwh,
    )

    return SnapshotResult(
        StateObservation(
            state=state,
            observed_fields=frozenset(observed_fields),
            unavailable_fields=frozenset(unavailable_fields),
            capability_results=capability_results,
            explicit_capability_contract=explicit_capabilities is not None,
        ),
        registered,
        updated_not_found_streak,
    )


def _snapshot_observation_from_proto(
    snapshot: proto_stubs.DeviceSnapshot,
    *,
    ski_registered: bool,
) -> StateObservation:
    """Convert the aggregate bridge read into the same reducer observation as legacy polling."""
    measurements = _extract_flat_measurements(snapshot.measurements)
    room_heating = _room_heating_from_proto(snapshot.room_heating) if snapshot.HasField("room_heating") else None
    if room_heating is not None and room_heating.current_temperature_celsius is not None:
        measurements["room_temperature_c"] = room_heating.current_temperature_celsius

    device_info: DeviceInfo | None = None
    if snapshot.HasField("classification"):
        classification = snapshot.classification
        candidate = DeviceInfo(
            manufacturer=classification.brand or None,
            model=classification.model or None,
            serial=classification.serial or None,
            device_type=classification.device_type or None,
        )
        if any((candidate.manufacturer, candidate.model, candidate.serial, candidate.device_type)):
            device_info = candidate

    consumption_limit = None
    if snapshot.HasField("consumption_limit"):
        value = snapshot.consumption_limit
        consumption_limit = ConsumptionLimitState(value.value_watts, value.is_active, value.is_changeable)
    failsafe_limit = None
    if snapshot.HasField("failsafe_limit"):
        value = snapshot.failsafe_limit
        failsafe_limit = FailsafeState(value.value_watts, value.duration_minimum_seconds)
    heartbeat = None
    if snapshot.HasField("heartbeat"):
        heartbeat = HeartbeatState(snapshot.heartbeat.running, snapshot.heartbeat.within_duration)
    compressor = None
    if snapshot.HasField("compressor_flexibility"):
        value = snapshot.compressor_flexibility
        compressor = CompressorFlexibilityState(
            available=value.available,
            state=proto_stubs.CompressorPowerConsumptionState.Name(value.state),
            requested_power_estimate_w=(value.requested_power_estimate_w if value.HasField("requested_power_estimate_w") else None),
            requested_power_max_w=(value.requested_power_max_w if value.HasField("requested_power_max_w") else None),
            is_pausable=value.is_pausable,
            is_stoppable=value.is_stoppable,
            minimal_run_seconds=value.minimal_run_seconds,
            minimal_pause_seconds=value.minimal_pause_seconds,
            start_time=value.start_time.ToDatetime(tzinfo=UTC) if value.HasField("start_time") else None,
        )

    state = DeviceState(
        connection=ConnectionState(
            connected=snapshot.connection.connected if snapshot.HasField("connection") else False,
            local_ski=snapshot.local_ski,
            ski_registered=ski_registered,
            device_info=device_info,
            device_operating_state=(
                snapshot.device_diagnostics.operating_state or None
                if snapshot.HasField("device_diagnostics")
                else None
            ),
        ),
        measurements=replace(MeasurementsState(), **measurements),
        lpc=LPCState(consumption_limit=consumption_limit, failsafe_limit=failsafe_limit, heartbeat_status=heartbeat),
        dhw=DHWState(
            setpoint=_setpoint_to_dict(snapshot.dhw_setpoint) if snapshot.HasField("dhw_setpoint") else None,
            system_function=(
                _dhw_system_function_to_dict(snapshot.dhw_system_function)
                if snapshot.HasField("dhw_system_function")
                else None
            ),
        ),
        hvac=HVACState(
            setpoint=room_heating.setpoint if room_heating is not None else None,
            system_function=room_heating.system_function if room_heating is not None else None,
        ),
        ohpcf=OHPCFState(compressor_flexibility=compressor),
    )

    available = int(proto_stubs.SnapshotValueState.SNAPSHOT_VALUE_STATE_AVAILABLE)
    observed_fields = {StateField.SKI_REGISTERED}
    unavailable_fields: set[StateField] = set()
    for field_status in snapshot.field_states:
        field = _SNAPSHOT_FIELD_TO_STATE_FIELD.get(field_status.id)
        if field is None:
            continue
        if int(field_status.state) == available:
            observed_fields.add(field)
        else:
            unavailable_fields.add(field)

    return StateObservation(
        state=state,
        observed_fields=frozenset(observed_fields),
        unavailable_fields=frozenset(unavailable_fields),
        capability_results=_capability_results_from_proto(snapshot.capabilities),
        explicit_capability_contract=True,
    )


async def async_build_device_snapshot(
    channel: grpc.aio.Channel,
    ski: str,
    *,
    ski_registered: bool,
) -> SnapshotResult:
    """Register if necessary, then read the device in one aggregate RPC."""
    stub = proto_stubs.device_service_stub(channel)
    registered = ski_registered
    if not registered:
        registered = await _async_register_remote_ski(stub, ski, force=False, registered=False)
    snapshot: proto_stubs.DeviceSnapshot = await stub.GetDeviceSnapshot(
        proto_stubs.DeviceRequest(ski=ski), timeout=RPC_TIMEOUT
    )
    return SnapshotResult(
        _snapshot_observation_from_proto(snapshot, ski_registered=registered),
        registered,
        0,
    )


class DevicePoller:
    """Poll one device and submit the result to its authoritative store."""

    def __init__(
        self,
        ski: str,
        ensure_channel: Callable[[], Awaitable[grpc.aio.Channel]],
        store: DeviceStateStore,
        supports_feature: Callable[[int], bool] | None = None,
    ) -> None:
        self._ski = ski
        self._ensure_channel = ensure_channel
        self._store = store
        self._supports_feature = supports_feature or (lambda _feature: False)
        self._ski_registered = False
        self._not_found_streak = 0

    async def ensure_registered(self) -> None:
        """Register the remote SKI without performing a device snapshot read."""
        if self._ski_registered:
            return
        channel = await self._ensure_channel()
        stub = proto_stubs.device_service_stub(channel)
        self._ski_registered = await _async_register_remote_ski(
            stub,
            self._ski,
            force=False,
            registered=False,
        )

    async def poll(self) -> DeviceState:
        """Run one atomic poll without overwriting newer stream observations."""
        base_revision = self._store.revision
        channel = await self._ensure_channel()
        if self._supports_feature(int(proto_stubs.FeatureId.FEATURE_DEVICE_SNAPSHOT)):
            result = await async_build_device_snapshot(
                channel,
                self._ski,
                ski_registered=self._ski_registered,
            )
        else:
            result = await async_build_snapshot(
                channel,
                self._ski,
                ski_registered=self._ski_registered,
                not_found_streak=self._not_found_streak,
                supports_explicit_capabilities=self._supports_feature(
                    int(proto_stubs.FeatureId.FEATURE_EXPLICIT_CAPABILITIES)
                ),
            )
        self._ski_registered = result.ski_registered
        self._not_found_streak = result.not_found_streak
        observation = replace(result.observation, base_revision=base_revision)
        return self._store.dispatch(observation)

    def reset_after_transport_error(self) -> None:
        """Reset transport-dependent poll recovery counters."""
        self._not_found_streak = 0
