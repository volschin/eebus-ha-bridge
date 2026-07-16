"""Atomic EEBUS polling snapshot construction without Home Assistant side effects."""

from __future__ import annotations

import asyncio
import logging
from collections.abc import Awaitable
from dataclasses import dataclass
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
    CompressorFlexibilityState,
    ConsumptionLimitState,
    CoordinatorSnapshot,
    DHWSystemFunctionState,
    DeviceInfo,
    FailsafeState,
    HeartbeatState,
    SetpointState,
    SystemFunctionState,
    _dhw_system_function_to_dict,
    _extract_flat_measurements,
    _extract_scoped_energy_kwh,
    _setpoint_to_dict,
    _system_function_to_dict,
)

_LOGGER = logging.getLogger(__name__)

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
    supported: bool | None
    saw_not_found: bool = False


@dataclass(frozen=True, slots=True)
class SnapshotSupport:
    """Support flags carried into and returned from one polling cycle."""

    lpc: bool | None = None
    failsafe: bool | None = None
    heartbeat: bool | None = None
    ohpcf: bool | None = None
    dhw: bool | None = None
    dhw_system_function: bool | None = None
    room_heating: bool | None = None


@dataclass(frozen=True, slots=True)
class SnapshotResult:
    """Complete polling result and coordinator-owned state updates."""

    snapshot: CoordinatorSnapshot
    support: SnapshotSupport
    ski_registered: bool
    not_found_streak: int


@dataclass(frozen=True, slots=True)
class _RoomHeatingRead:
    """Room-heating fields returned together so they can be published atomically."""

    setpoint: SetpointState | None
    system_function: SystemFunctionState | None
    current_temperature_celsius: float | None


async def _poll_read(
    label: str,
    call: _ReadCall[_ResponseT],
    request: proto_stubs.DeviceRequest,
    ski: str,
    *,
    track_support: bool = False,
    current_support: bool | None = None,
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
        supported = False if track_support and _is_unimplemented(err) else current_support
        return _ReadResult(None, supported, _is_not_found(err))
    except Exception:  # noqa: BLE001
        _LOGGER.exception("Failed to read %s", label)
        return _ReadResult(None, current_support)
    _LOGGER.debug("EEBUS %s read for SKI %s succeeded", label, ski)
    return _ReadResult(response, True if track_support else current_support)


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


async def _async_fetch_device_info(
    device_stub: proto_stubs.DeviceServiceStub, ski: str
) -> DeviceInfo | None:
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

    info: DeviceInfo = {}
    if match.brand:
        info["manufacturer"] = match.brand
    if match.model:
        info["model"] = match.model
    if match.serial:
        info["serial"] = match.serial
    if match.device_type:
        info["device_type"] = match.device_type
    return info or None


async def _async_read_compressor_flexibility(
    channel: grpc.aio.Channel,
    request: proto_stubs.DeviceRequest,
    ski: str,
    current_support: bool | None,
) -> _ReadResult[CompressorFlexibilityState]:
    """Read the OHPCF compressor flexibility offer/state, or None when off."""
    try:
        stub = proto_stubs.ohpcf_service_stub(channel)
        flex: proto_stubs.CompressorFlexibility = await stub.GetCompressorFlexibility(
            request, timeout=RPC_TIMEOUT
        )
    except grpc.aio.AioRpcError as err:
        supported = current_support
        if _is_unimplemented(err) or err.code() == grpc.StatusCode.UNAVAILABLE:
            supported = False
        else:
            _LOGGER.debug(
                "EEBUS OHPCF read failed for SKI %s: %s",
                ski,
                _rpc_error_text(err),
            )
        return _ReadResult(None, supported)

    value: CompressorFlexibilityState = {
        "available": flex.available,
        "state": proto_stubs.CompressorPowerConsumptionState.Name(flex.state),
        "requested_power_estimate_w": (
            flex.requested_power_estimate_w if flex.HasField("requested_power_estimate_w") else None
        ),
        "requested_power_max_w": flex.requested_power_max_w if flex.HasField("requested_power_max_w") else None,
        "is_pausable": flex.is_pausable,
        "is_stoppable": flex.is_stoppable,
        "minimal_run_seconds": flex.minimal_run_seconds,
        "minimal_pause_seconds": flex.minimal_pause_seconds,
    }
    return _ReadResult(value, True)


async def _async_read_dhw_setpoint(
    channel: grpc.aio.Channel,
    request: proto_stubs.DeviceRequest,
    ski: str,
    current_support: bool | None,
) -> _ReadResult[SetpointState]:
    """Read the DHW target and device-provided constraints."""
    try:
        stub = proto_stubs.dhw_service_stub(channel)
        setpoint: proto_stubs.DHWSetpoint = await stub.GetDHWSetpoint(request, timeout=RPC_TIMEOUT)
    except grpc.aio.AioRpcError as err:
        supported = current_support
        if _is_unimplemented(err) or err.code() in (
            grpc.StatusCode.NOT_FOUND,
            grpc.StatusCode.UNAVAILABLE,
        ):
            supported = False
        else:
            _LOGGER.debug(
                "EEBUS DHW setpoint read failed for SKI %s: %s",
                ski,
                _rpc_error_text(err),
            )
        return _ReadResult(None, supported)
    return _ReadResult(_setpoint_to_dict(setpoint), True)


async def _async_read_dhw_system_function(
    channel: grpc.aio.Channel,
    request: proto_stubs.DeviceRequest,
    ski: str,
    current_support: bool | None,
) -> _ReadResult[DHWSystemFunctionState]:
    """Read DHW boost and operation mode state."""
    try:
        stub = proto_stubs.dhw_service_stub(channel)
        state: proto_stubs.DHWSystemFunctionState = await stub.GetDHWSystemFunction(
            request, timeout=RPC_TIMEOUT
        )
    except grpc.aio.AioRpcError as err:
        supported = current_support
        if _is_unimplemented(err) or err.code() in (
            grpc.StatusCode.NOT_FOUND,
            grpc.StatusCode.UNAVAILABLE,
        ):
            supported = False
        else:
            _LOGGER.debug(
                "EEBUS DHW system function read failed for SKI %s: %s",
                ski,
                _rpc_error_text(err),
            )
        return _ReadResult(None, supported)
    return _ReadResult(_dhw_system_function_to_dict(state), True)


async def _async_read_room_heating(
    channel: grpc.aio.Channel,
    request: proto_stubs.DeviceRequest,
    current_support: bool | None,
) -> _ReadResult[_RoomHeatingRead]:
    """Read all room-heating fields without publishing a partial update."""
    try:
        state: proto_stubs.RoomHeatingState = await proto_stubs.hvac_service_stub(channel).GetRoomHeating(
            request, timeout=RPC_TIMEOUT
        )
    except grpc.aio.AioRpcError as err:
        supported = current_support
        if _is_unimplemented(err) or err.code() in (
            grpc.StatusCode.NOT_FOUND,
            grpc.StatusCode.UNAVAILABLE,
        ):
            supported = False
        return _ReadResult(None, supported)

    setpoint = _setpoint_to_dict(state.setpoint) if state.HasField("setpoint") else None
    system_function = (
        _system_function_to_dict(state.system_function) if state.HasField("system_function") else None
    )
    current_temperature = (
        state.current_temperature_celsius if state.HasField("current_temperature_celsius") else None
    )
    return _ReadResult(_RoomHeatingRead(setpoint, system_function, current_temperature), True)


async def _async_read_device_diagnostics(
    channel: grpc.aio.Channel, request: proto_stubs.DeviceRequest, ski: str
) -> str | None:
    """Read the device operating state."""
    try:
        diagnostics: proto_stubs.DeviceDiagnosticsData = await proto_stubs.monitoring_service_stub(
            channel
        ).GetDeviceDiagnostics(request, timeout=RPC_TIMEOUT)
    except grpc.aio.AioRpcError as err:
        if not (
            _is_unimplemented(err)
            or err.code() in (grpc.StatusCode.NOT_FOUND, grpc.StatusCode.UNAVAILABLE)
        ):
            _LOGGER.debug(
                "EEBUS device diagnosis read failed for SKI %s: %s",
                ski,
                _rpc_error_text(err),
            )
        return None
    return diagnostics.operating_state or None


async def async_build_snapshot(
    channel: grpc.aio.Channel,
    ski: str,
    support: SnapshotSupport,
    *,
    ski_registered: bool,
    not_found_streak: int,
) -> SnapshotResult:
    """Read and assemble one complete coordinator snapshot atomically."""
    device_stub = proto_stubs.device_service_stub(channel)
    status: proto_stubs.ServiceStatus = await device_stub.GetStatus(proto_stubs.Empty())

    registered = ski_registered
    if not registered:
        registered = await _async_register_remote_ski(
            device_stub,
            ski,
            force=False,
            registered=registered,
        )

    if ski == status.local_ski:
        _LOGGER.warning(
            "Configured remote SKI %s matches bridge local SKI; monitoring reads will stay empty",
            ski,
        )

    monitoring_stub = proto_stubs.monitoring_service_stub(channel)
    lpc_stub = proto_stubs.lpc_service_stub(channel)
    request = proto_stubs.DeviceRequest(ski=ski)

    power, measurements, energy, limit, failsafe, heartbeat = await asyncio.gather(
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
            track_support=True,
            current_support=support.lpc,
        ),
        _poll_read(
            "failsafe",
            cast(_ReadCall[proto_stubs.FailsafeLimit], lpc_stub.GetFailsafeLimit),
            request,
            ski,
            track_support=True,
            current_support=support.failsafe,
        ),
        _poll_read(
            "heartbeat",
            cast(_ReadCall[proto_stubs.HeartbeatStatus], lpc_stub.GetHeartbeatStatus),
            request,
            ski,
            track_support=True,
            current_support=support.heartbeat,
        ),
    )

    device_info, compressor, dhw_setpoint, dhw_system_function, room_heating, diagnostics = await asyncio.gather(
        _async_fetch_device_info(device_stub, ski),
        _async_read_compressor_flexibility(channel, request, ski, support.ohpcf),
        _async_read_dhw_setpoint(channel, request, ski, support.dhw),
        _async_read_dhw_system_function(channel, request, ski, support.dhw_system_function),
        _async_read_room_heating(channel, request, support.room_heating),
        _async_read_device_diagnostics(channel, request, ski),
    )

    updated_support = SnapshotSupport(
        lpc=limit.supported,
        failsafe=failsafe.supported,
        heartbeat=heartbeat.supported,
        ohpcf=compressor.supported,
        dhw=dhw_setpoint.supported,
        dhw_system_function=dhw_system_function.supported,
        room_heating=room_heating.supported,
    )

    flat_measurements: dict[str, float | None] = {}
    scoped_energy: dict[str, float | None] = {"heating": None, "dhw": None}
    if measurements.value is not None:
        scoped_energy = _extract_scoped_energy_kwh(measurements.value.measurements)
        flat_measurements = _extract_flat_measurements(measurements.value.measurements)

    consumption_limit: ConsumptionLimitState | None = None
    if limit.value is not None:
        consumption_limit = {
            "value_watts": limit.value.value_watts,
            "is_active": limit.value.is_active,
            "is_changeable": limit.value.is_changeable,
        }

    failsafe_limit: FailsafeState | None = None
    if failsafe.value is not None:
        failsafe_limit = {
            "value_watts": failsafe.value.value_watts,
            "duration_minimum_seconds": failsafe.value.duration_minimum_seconds,
        }

    heartbeat_status: HeartbeatState | None = None
    if heartbeat.value is not None:
        heartbeat_status = {
            "running": heartbeat.value.running,
            "within_duration": heartbeat.value.within_duration,
        }

    room_heating_value = room_heating.value
    room_temperature_c = flat_measurements.get("room_temperature_c")
    if room_heating_value is not None and room_heating_value.current_temperature_celsius is not None:
        room_temperature_c = room_heating_value.current_temperature_celsius

    snapshot = CoordinatorSnapshot(
        connected=status.running,
        local_ski=status.local_ski,
        ski_registered=registered,
        power_l1_w=flat_measurements.get("power_l1_w"),
        power_l2_w=flat_measurements.get("power_l2_w"),
        power_l3_w=flat_measurements.get("power_l3_w"),
        current_l1_a=flat_measurements.get("current_l1_a"),
        current_l2_a=flat_measurements.get("current_l2_a"),
        current_l3_a=flat_measurements.get("current_l3_a"),
        voltage_l1_v=flat_measurements.get("voltage_l1_v"),
        voltage_l2_v=flat_measurements.get("voltage_l2_v"),
        voltage_l3_v=flat_measurements.get("voltage_l3_v"),
        frequency_hz=flat_measurements.get("frequency_hz"),
        energy_produced_kwh=flat_measurements.get("energy_produced_kwh"),
        dhw_temperature_c=flat_measurements.get("dhw_temperature_c"),
        room_temperature_c=room_temperature_c,
        outdoor_temperature_c=flat_measurements.get("outdoor_temperature_c"),
        flow_temperature_c=flat_measurements.get("flow_temperature_c"),
        return_temperature_c=flat_measurements.get("return_temperature_c"),
        compressor_temperature_c=flat_measurements.get("compressor_temperature_c"),
        compressor_power_w=flat_measurements.get("compressor_power_w"),
        power_watts=power.value.watts if power.value is not None else None,
        energy_consumed_heating_kwh=scoped_energy["heating"],
        energy_consumed_dhw_kwh=scoped_energy["dhw"],
        energy_consumed_kwh=energy.value.kilowatt_hours if energy.value is not None else None,
        consumption_limit=consumption_limit,
        failsafe_limit=failsafe_limit,
        heartbeat_status=heartbeat_status,
        heartbeat_supported=updated_support.heartbeat,
        lpc_supported=updated_support.lpc,
        failsafe_supported=updated_support.failsafe,
        device_info=device_info,
        compressor_flexibility=compressor.value,
        dhw_setpoint=dhw_setpoint.value,
        dhw_system_function=dhw_system_function.value,
        room_heating_setpoint=room_heating_value.setpoint if room_heating_value is not None else None,
        room_heating_system_function=(
            room_heating_value.system_function if room_heating_value is not None else None
        ),
        device_operating_state=diagnostics,
        ohpcf_supported=updated_support.ohpcf,
        dhw_supported=updated_support.dhw,
        dhw_sysfn_supported=updated_support.dhw_system_function,
        room_heating_supported=updated_support.room_heating,
    )

    saw_not_found = any(
        result.saw_not_found for result in (power, measurements, energy, limit, failsafe, heartbeat)
    )
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
        snapshot["power_watts"],
        snapshot["energy_consumed_kwh"],
        snapshot["energy_consumed_heating_kwh"],
        snapshot["energy_consumed_dhw_kwh"],
    )

    return SnapshotResult(snapshot, updated_support, registered, updated_not_found_streak)
