"""DataUpdateCoordinator for EEBUS integration."""

from __future__ import annotations

import asyncio
import logging
import math
from collections.abc import Awaitable, Callable
from contextlib import suppress
from datetime import timedelta
from functools import lru_cache
from types import ModuleType
from typing import TYPE_CHECKING, Any

import grpc
import grpc.aio

from homeassistant.const import (
    ATTR_UNIT_OF_MEASUREMENT,
    PERCENTAGE,
    STATE_UNAVAILABLE,
    STATE_UNKNOWN,
    UnitOfEnergy,
    UnitOfPower,
)
from homeassistant.core import Event, HomeAssistant
from homeassistant.exceptions import ConfigEntryAuthFailed, ServiceValidationError
from homeassistant.helpers.event import (
    EventStateChangedData,
    async_track_state_change_event,
)
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed

from .const import SECURITY_MODE_LOOPBACK
from .grpc_client import (
    RPC_TIMEOUT,
    WRITE_VALIDATION_CODES,
    GrpcChannelManager,
    is_not_found as _is_not_found,
    is_unimplemented as _is_unimplemented,
    rpc_error_text as _rpc_error_text,
)
from .streams import ConsumeFn, StreamManager

if TYPE_CHECKING:
    from . import proto_stubs

_LOGGER = logging.getLogger(__name__)

# Convert a Home Assistant grid sensor's value to the unit the MGCP provider
# expects (power in W, energy in Wh) using its unit_of_measurement attribute.
POWER_UNIT_TO_W: dict[str, float] = {
    UnitOfPower.WATT: 1.0,
    UnitOfPower.KILO_WATT: 1000.0,
    UnitOfPower.MEGA_WATT: 1_000_000.0,
}
ENERGY_UNIT_TO_WH: dict[str, float] = {
    UnitOfEnergy.WATT_HOUR: 1.0,
    UnitOfEnergy.KILO_WATT_HOUR: 1000.0,
    UnitOfEnergy.MEGA_WATT_HOUR: 1_000_000.0,
}
# State of charge is a plain percentage (0-100); no conversion needed.
SOC_UNIT_TO_PCT: dict[str, float] = {PERCENTAGE: 1.0}

# Event streams deliver push updates; polling only reconciles state the
# streams cannot carry (scoped energy, heartbeat, support flags).
POLL_INTERVAL = timedelta(minutes=5)
RE_REGISTER_NOT_FOUND_STREAK = 4


def _normalize_ski(ski: str) -> str:
    """Normalize an SKI for comparisons without changing its stored form."""
    return ski.replace(":", "").replace("-", "").replace(" ", "").casefold()

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


def _dhw_system_function_to_dict(state: Any) -> dict[str, Any]:
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


@lru_cache(maxsize=1)
def _measurement_event_map() -> tuple[dict[int, tuple[str, str, str]], frozenset[int]]:
    """Build the measurement-event dispatch tables.

    Streaming twin of FLAT_MEASUREMENT_TYPE_TO_KEY: maps an event type to
    (payload field, value attribute, coordinator key). Support events carry no
    payload and always reconcile via a poll. Cached because the proto enum is
    only importable at runtime.
    """
    from . import proto_stubs

    event_type = proto_stubs.MeasurementEventType
    value_events: dict[int, tuple[str, str, str]] = {
        event_type.MEASUREMENT_EVENT_POWER_UPDATED: ("power", "watts", "power_watts"),
        event_type.MEASUREMENT_EVENT_ENERGY_UPDATED: (
            "energy",
            "kilowatt_hours",
            "energy_consumed_kwh",
        ),
        event_type.MEASUREMENT_EVENT_DHW_TEMPERATURE_UPDATED: (
            "measurement",
            "value",
            "dhw_temperature_c",
        ),
        event_type.MEASUREMENT_EVENT_ROOM_TEMPERATURE_UPDATED: (
            "measurement",
            "value",
            "room_temperature_c",
        ),
        event_type.MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_UPDATED: (
            "measurement",
            "value",
            "outdoor_temperature_c",
        ),
        event_type.MEASUREMENT_EVENT_FLOW_TEMPERATURE_UPDATED: (
            "measurement",
            "value",
            "flow_temperature_c",
        ),
        event_type.MEASUREMENT_EVENT_RETURN_TEMPERATURE_UPDATED: (
            "measurement",
            "value",
            "return_temperature_c",
        ),
        event_type.MEASUREMENT_EVENT_DEVICE_OPERATING_STATE_UPDATED: (
            "device_diagnostics",
            "operating_state",
            "device_operating_state",
        ),
    }
    support_events = frozenset(
        {
            event_type.MEASUREMENT_EVENT_DHW_TEMPERATURE_SUPPORT_UPDATED,
            event_type.MEASUREMENT_EVENT_ROOM_TEMPERATURE_SUPPORT_UPDATED,
            event_type.MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_SUPPORT_UPDATED,
        }
    )
    return value_events, support_events


def _setpoint_to_dict(setpoint: Any) -> dict[str, Any]:
    """Convert a protobuf setpoint (value/min/max/step/writable) to coordinator data."""
    return {
        "value_celsius": setpoint.value_celsius,
        "min_celsius": setpoint.min_celsius,
        "max_celsius": setpoint.max_celsius,
        "step_celsius": setpoint.step_celsius,
        "writable": setpoint.writable,
    }


def _system_function_to_dict(system_function: Any) -> dict[str, Any]:
    """Convert a protobuf system-function state to coordinator data."""
    return {
        "operation_mode": system_function.operation_mode,
        "available_modes": list(system_function.available_modes),
        "mode_writable": system_function.mode_writable,
    }


class _ProviderPusher:
    """Serialize and coalesce state-triggered pushes for one provider."""

    def __init__(
        self,
        hass: HomeAssistant,
        label: str,
        ski: str,
        tracked: tuple[str | None, ...],
        push: Callable[[], Awaitable[None]],
    ) -> None:
        self._hass = hass
        self._label = label
        self._ski = ski
        self._entity_ids = [entity_id for entity_id in tracked if entity_id]
        self._push = push
        self._dirty = asyncio.Event()
        self._task: asyncio.Task[None] | None = None
        self._unsub: Callable[[], None] | None = None

    def start(self) -> None:
        """Subscribe to state changes and schedule the initial push."""
        if self._task is not None:
            return

        def _on_change(_event: Event[EventStateChangedData]) -> None:
            self.signal()

        self._unsub = async_track_state_change_event(
            self._hass, self._entity_ids, _on_change
        )
        self.signal()
        self._task = self._hass.async_create_background_task(
            self._run(), name=f"eebus_{self._label}_provider_push_{self._ski}"
        )

    def signal(self) -> None:
        """Mark provider data dirty, coalescing repeated signals."""
        self._dirty.set()

    async def stop(self) -> None:
        """Unsubscribe, then cancel and await the worker task."""
        unsub = self._unsub
        self._unsub = None
        task = self._task
        if task is None:
            if unsub is not None:
                unsub()
            return
        try:
            if unsub is not None:
                unsub()
        finally:
            task.cancel()
            with suppress(asyncio.CancelledError):
                await task
            self._task = None

    async def _run(self) -> None:
        """Push the freshest state once per coalesced dirty signal."""
        while True:
            await self._dirty.wait()
            self._dirty.clear()
            try:
                await self._push()
            except asyncio.CancelledError:
                raise
            except Exception:  # noqa: BLE001
                _LOGGER.exception("Unexpected failure pushing %s provider data", self._label)


class EebusCoordinator(DataUpdateCoordinator[dict[str, Any]]):
    """Coordinator that manages gRPC connection and data updates."""

    def __init__(
        self,
        hass: HomeAssistant,
        host: str,
        port: int,
        ski: str,
        security_mode: str = SECURITY_MODE_LOOPBACK,
        tls_ca_certificate: str | None = None,
        auth_token: str | None = None,
        grid_power_entity: str | None = None,
        grid_feed_in_energy_entity: str | None = None,
        grid_consumption_energy_entity: str | None = None,
        pv_power_entity: str | None = None,
        pv_yield_energy_entity: str | None = None,
        pv_peak_power_entity: str | None = None,
        battery_power_entity: str | None = None,
        battery_charged_energy_entity: str | None = None,
        battery_discharged_energy_entity: str | None = None,
        battery_soc_entity: str | None = None,
    ) -> None:
        """Initialize the coordinator."""
        super().__init__(
            hass,
            _LOGGER,
            name="EEBUS",
            update_interval=POLL_INTERVAL,
        )
        self.host = host
        self.port = port
        self.ski = ski
        self.security_mode = security_mode
        self.tls_ca_certificate = tls_ca_certificate
        self.auth_token = auth_token
        # Optional grid sensors feeding the bridge MGCP provider (PV-surplus).
        self.grid_power_entity = grid_power_entity
        self.grid_feed_in_energy_entity = grid_feed_in_energy_entity
        self.grid_consumption_energy_entity = grid_consumption_energy_entity
        # Optional PV sensors feeding the bridge VAPD (display) provider.
        self.pv_power_entity = pv_power_entity
        self.pv_yield_energy_entity = pv_yield_energy_entity
        self.pv_peak_power_entity = pv_peak_power_entity
        # Optional battery sensors feeding the bridge VABD (display) provider.
        self.battery_power_entity = battery_power_entity
        self.battery_charged_energy_entity = battery_charged_energy_entity
        self.battery_discharged_energy_entity = battery_discharged_energy_entity
        self.battery_soc_entity = battery_soc_entity
        self._channel_manager = GrpcChannelManager(
            host, port, security_mode, tls_ca_certificate, auth_token
        )
        self._stream_manager = StreamManager(self.hass, self._channel_manager)
        self._provider_pushers: list[_ProviderPusher] = []
        self._provider_push_failing: dict[str, bool] = {}
        self._was_unavailable: bool = False
        self._heartbeat_supported: bool | None = None
        self._lpc_supported: bool | None = None
        self._failsafe_supported: bool | None = None
        self._ohpcf_supported: bool | None = None
        self._dhw_supported: bool | None = None
        self._dhw_sysfn_supported: bool | None = None
        self._room_heating_supported: bool | None = None
        self._ski_registered: bool = False
        self._not_found_streak: int = 0

    async def _ensure_channel(self) -> grpc.aio.Channel:
        """Create or return existing gRPC channel."""
        return await self._channel_manager.ensure_channel()

    async def _async_update_data(self) -> dict[str, Any]:
        """Fetch data via gRPC polling."""
        try:
            channel = await self._ensure_channel()
            from . import proto_stubs

            device_stub = proto_stubs.device_service_stub(channel)
            status = await device_stub.GetStatus(proto_stubs.Empty())

            if not self._ski_registered:
                await self._async_register_remote_ski(device_stub, proto_stubs, force=False)

            data: dict[str, Any] = {
                "connected": status.running,
                "local_ski": status.local_ski,
                "ski_registered": self._ski_registered,
            }
            # Per-phase / grid / produced-energy measurements default to None and
            # are populated from GetMeasurements when the device advertises them.
            for _key in FLAT_MEASUREMENT_KEYS:
                data[_key] = None
            if self.ski == status.local_ski:
                _LOGGER.warning(
                    "Configured remote SKI %s matches bridge local SKI; monitoring reads will stay empty",
                    self.ski,
                )

            monitoring_stub = proto_stubs.monitoring_service_stub(channel)
            lpc_stub = proto_stubs.lpc_service_stub(channel)
            request = proto_stubs.DeviceRequest(ski=self.ski)
            flags = {"saw_not_found": False}

            # The reads are independent; run them concurrently so poll latency
            # is the slowest single read instead of the sum of all round-trips.
            power, measurements, energy, limit, failsafe, hb = await asyncio.gather(
                self._poll_read(
                    "power", monitoring_stub.GetPowerConsumption, request, flags
                ),
                self._poll_read(
                    "scoped energy", monitoring_stub.GetMeasurements, request, flags
                ),
                self._poll_read(
                    "total energy", monitoring_stub.GetEnergyConsumed, request, flags
                ),
                self._poll_read(
                    "consumption limit",
                    lpc_stub.GetConsumptionLimit,
                    request,
                    flags,
                    unsupported_attr="_lpc_supported",
                ),
                self._poll_read(
                    "failsafe",
                    lpc_stub.GetFailsafeLimit,
                    request,
                    flags,
                    unsupported_attr="_failsafe_supported",
                ),
                self._poll_read(
                    "heartbeat",
                    lpc_stub.GetHeartbeatStatus,
                    request,
                    flags,
                    unsupported_attr="_heartbeat_supported",
                ),
            )

            data["power_watts"] = power.watts if power is not None else None

            if measurements is not None:
                scoped_energy = self._extract_scoped_energy_kwh(measurements.measurements)
                data["energy_consumed_heating_kwh"] = scoped_energy["heating"]
                data["energy_consumed_dhw_kwh"] = scoped_energy["dhw"]
                data.update(self._extract_flat_measurements(measurements.measurements))
            else:
                data["energy_consumed_heating_kwh"] = None
                data["energy_consumed_dhw_kwh"] = None

            data["energy_consumed_kwh"] = (
                energy.kilowatt_hours if energy is not None else None
            )

            if limit is not None:
                self._lpc_supported = True
                data["consumption_limit"] = {
                    "value_watts": limit.value_watts,
                    "is_active": limit.is_active,
                    "is_changeable": limit.is_changeable,
                }
            else:
                data["consumption_limit"] = None

            if failsafe is not None:
                self._failsafe_supported = True
                data["failsafe_limit"] = {
                    "value_watts": failsafe.value_watts,
                    "duration_minimum_seconds": failsafe.duration_minimum_seconds,
                }
            else:
                data["failsafe_limit"] = None

            if hb is not None:
                self._heartbeat_supported = True
                data["heartbeat_status"] = {
                    "running": hb.running,
                    "within_duration": hb.within_duration,
                }
            else:
                data["heartbeat_status"] = None

            saw_not_found = flags["saw_not_found"]
            data["heartbeat_supported"] = self._heartbeat_supported
            data["lpc_supported"] = self._lpc_supported
            data["failsafe_supported"] = self._failsafe_supported

            # Remaining reads are independent too. OHPCF/DHW/room-heating stay
            # unavailable when the bridge side is off; device diagnostics is
            # best-effort.
            (
                data["device_info"],
                data["compressor_flexibility"],
                data["dhw_setpoint"],
                data["dhw_system_function"],
                room_heating,
                data["device_operating_state"],
            ) = await asyncio.gather(
                self._async_fetch_device_info(device_stub, proto_stubs),
                self._async_read_compressor_flexibility(channel, proto_stubs, request),
                self._async_read_dhw_setpoint(channel, proto_stubs, request),
                self._async_read_dhw_system_function(channel, proto_stubs, request),
                self._async_read_room_heating(channel, proto_stubs, request),
                self._async_read_device_diagnostics(channel, proto_stubs, request),
            )
            data["ohpcf_supported"] = self._ohpcf_supported
            data["dhw_supported"] = self._dhw_supported
            data["dhw_sysfn_supported"] = self._dhw_sysfn_supported
            (
                data["room_heating_setpoint"],
                data["room_heating_system_function"],
            ) = room_heating
            data["room_heating_supported"] = self._room_heating_supported

            if saw_not_found:
                self._not_found_streak += 1
            else:
                self._not_found_streak = 0

            if self._not_found_streak >= RE_REGISTER_NOT_FOUND_STREAK:
                _LOGGER.warning(
                    "EEBUS reads returned NOT_FOUND for %s consecutive polls; forcing remote SKI re-registration for %s",
                    self._not_found_streak,
                    self.ski,
                )
                await self._async_register_remote_ski(device_stub, proto_stubs, force=True)
                self._not_found_streak = 0

            _LOGGER.debug(
                "EEBUS poll summary for SKI %s: power=%s energy_total=%s energy_heating=%s energy_dhw=%s",
                self.ski,
                data["power_watts"],
                data["energy_consumed_kwh"],
                data["energy_consumed_heating_kwh"],
                data["energy_consumed_dhw_kwh"],
            )

            if self._was_unavailable:
                _LOGGER.info("EEBUS bridge connection restored at %s:%s", self.host, self.port)
                self._was_unavailable = False

            return data
        except grpc.aio.AioRpcError as err:
            await self._channel_manager.invalidate()
            self._not_found_streak = 0

            if err.code() == grpc.StatusCode.UNAUTHENTICATED:
                raise ConfigEntryAuthFailed("Bridge authentication failed") from err

            if not self._was_unavailable:
                _LOGGER.warning(
                    "EEBUS bridge unavailable at %s:%s: %s", self.host, self.port, err
                )
                self._was_unavailable = True

            raise UpdateFailed(f"gRPC error: {err}") from err

    async def _poll_read(
        self,
        label: str,
        call: Any,
        request: Any,
        flags: dict[str, bool],
        unsupported_attr: str | None = None,
    ) -> Any:
        """Call a device-scoped read RPC once.

        Returns the response, or None on failure. Records NOT_FOUND in
        ``flags``; when ``unsupported_attr`` is given, clears that support flag
        on UNIMPLEMENTED.
        """
        try:
            response = await call(request, timeout=RPC_TIMEOUT)
        except grpc.aio.AioRpcError as err:
            if _is_not_found(err):
                flags["saw_not_found"] = True
            _LOGGER.debug(
                "EEBUS %s read failed for SKI %s: %s",
                label,
                self.ski,
                _rpc_error_text(err),
            )
            if unsupported_attr is not None and _is_unimplemented(err):
                setattr(self, unsupported_attr, False)
            return None
        except Exception:  # noqa: BLE001
            _LOGGER.exception("Failed to read %s", label)
            return None
        _LOGGER.debug("EEBUS %s read for SKI %s succeeded", label, self.ski)
        return response

    async def _async_fetch_device_info(
        self, device_stub: Any, proto_stubs: Any
    ) -> dict[str, str] | None:
        """Read manufacturer/model metadata for the configured device.

        Returns the brand, model, serial and EEBUS device type reported by the
        bridge so Home Assistant can label the device with real values instead of
        a hardcoded manufacturer. Best-effort: returns None on any error.
        """
        try:
            response = await device_stub.ListPairedDevices(
                proto_stubs.Empty(), timeout=RPC_TIMEOUT
            )
        except grpc.aio.AioRpcError as err:
            if not _is_unimplemented(err):
                _LOGGER.debug(
                    "ListPairedDevices failed for SKI %s: %s",
                    self.ski,
                    _rpc_error_text(err),
                )
            return None
        except Exception:  # noqa: BLE001
            _LOGGER.exception("Failed to list paired devices")
            return None

        devices = list(response.devices)
        if not devices:
            return None

        match = next((d for d in devices if d.ski == self.ski), None)
        if match is None:
            return None

        info: dict[str, str] = {}
        if match.brand:
            info["manufacturer"] = match.brand
        if match.model:
            info["model"] = match.model
        if match.serial:
            info["serial"] = match.serial
        if match.device_type:
            info["device_type"] = match.device_type
        return info or None

    async def _async_register_remote_ski(
        self, device_stub: Any, proto_stubs_module: ModuleType, force: bool
    ) -> None:
        """Register remote SKI with bridge, optionally forcing re-registration."""
        try:
            await device_stub.RegisterRemoteSKI(
                proto_stubs_module.RegisterSKIRequest(ski=self.ski), timeout=RPC_TIMEOUT
            )
            self._ski_registered = True
            if force:
                _LOGGER.info("Forced re-registration of remote SKI %s with bridge", self.ski)
            else:
                _LOGGER.info("Registered remote SKI %s with bridge", self.ski)
        except grpc.aio.AioRpcError as err:
            if force:
                _LOGGER.warning(
                    "Forced remote SKI re-registration failed for %s: %s",
                    self.ski,
                    _rpc_error_text(err),
                )
            else:
                # Retry in next polling cycle until the bridge accepts registration.
                _LOGGER.debug(
                    "Remote SKI registration pending for %s: %s",
                    self.ski,
                    _rpc_error_text(err),
                )

    @staticmethod
    def _extract_scoped_energy_kwh(measurements: list[Any]) -> dict[str, float | None]:
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
            if (
                "energy" in normalized
                and ("domestic_hot_water" in normalized or "hot_water" in normalized or "dhw" in normalized)
            ):
                result["dhw"] = value
                continue

            if "energy" in normalized and ("heating" in normalized or "space_heating" in normalized):
                result["heating"] = value

        return result

    @staticmethod
    def _extract_flat_measurements(measurements: list[Any]) -> dict[str, float | None]:
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
            result[key] = value
        return result

    async def _async_write_rpc(
        self,
        label: str,
        call: Any,
        request: Any,
        support_attr: str | None = None,
        validation: bool = False,
    ) -> None:
        """Run a write RPC with shared UNIMPLEMENTED / validation-error mapping.

        On success the support flag is set; on UNIMPLEMENTED it is cleared and
        the call returns quietly. With ``validation=True``, device-side
        rejections (WRITE_VALIDATION_CODES) surface as ServiceValidationError.
        """
        try:
            await call(request, timeout=RPC_TIMEOUT)
            if support_attr is not None:
                setattr(self, support_attr, True)
        except grpc.aio.AioRpcError as err:
            if _is_unimplemented(err):
                if support_attr is not None:
                    setattr(self, support_attr, False)
                _LOGGER.info(
                    "%s unsupported for SKI %s: %s", label, self.ski, err.details()
                )
                return
            if validation and err.code() in WRITE_VALIDATION_CODES:
                raise ServiceValidationError(f"{label} failed: {err.details()}") from err
            raise

    async def async_write_lpc_limit(self, value_watts: float) -> None:
        """Write LPC consumption limit via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.lpc_service_stub(channel)
        await self._async_write_rpc(
            "LPC write",
            stub.WriteConsumptionLimit,
            proto_stubs.WriteLoadLimitRequest(
                ski=self.ski, value_watts=value_watts, is_active=True
            ),
            support_attr="_lpc_supported",
        )

    async def async_write_failsafe_limit(self, value_watts: float) -> None:
        """Write failsafe limit via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.lpc_service_stub(channel)
        await self._async_write_rpc(
            "Failsafe write",
            stub.WriteFailsafeLimit,
            proto_stubs.WriteFailsafeLimitRequest(ski=self.ski, value_watts=value_watts),
            support_attr="_failsafe_supported",
        )

    async def async_set_lpc_active(self, active: bool) -> None:
        """Activate or deactivate LPC limit via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.lpc_service_stub(channel)
        current = await stub.GetConsumptionLimit(
            proto_stubs.DeviceRequest(ski=self.ski), timeout=RPC_TIMEOUT
        )
        await self._async_write_rpc(
            "LPC activation",
            stub.WriteConsumptionLimit,
            proto_stubs.WriteLoadLimitRequest(
                ski=self.ski,
                value_watts=current.value_watts,
                is_active=active,
            ),
            support_attr="_lpc_supported",
        )

    async def _async_read_compressor_flexibility(
        self, channel: grpc.aio.Channel, proto_stubs: Any, request: Any
    ) -> dict[str, Any] | None:
        """Read the OHPCF compressor flexibility offer/state, or None when off."""
        from .generated.eebus.v1 import ohpcf_service_pb2 as ohpcf_pb2

        try:
            stub = proto_stubs.ohpcf_service_stub(channel)
            flex = await stub.GetCompressorFlexibility(request, timeout=RPC_TIMEOUT)
        except grpc.aio.AioRpcError as err:
            if _is_unimplemented(err) or err.code() == grpc.StatusCode.UNAVAILABLE:
                self._ohpcf_supported = False
            else:
                _LOGGER.debug(
                    "EEBUS OHPCF read failed for SKI %s: %s",
                    self.ski,
                    _rpc_error_text(err),
                )
            return None

        self._ohpcf_supported = True
        return {
            "available": flex.available,
            "state": ohpcf_pb2.CompressorPowerConsumptionState.Name(flex.state),
            "requested_power_estimate_w": (
                flex.requested_power_estimate_w
                if flex.HasField("requested_power_estimate_w")
                else None
            ),
            "requested_power_max_w": (
                flex.requested_power_max_w
                if flex.HasField("requested_power_max_w")
                else None
            ),
            "is_pausable": flex.is_pausable,
            "is_stoppable": flex.is_stoppable,
            "minimal_run_seconds": flex.minimal_run_seconds,
            "minimal_pause_seconds": flex.minimal_pause_seconds,
        }

    async def async_control_compressor(self, action: proto_stubs.OHPCFAction) -> None:
        """Schedule/pause/resume/abort the compressor's optional consumption."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.ohpcf_service_stub(channel)
        try:
            await stub.ControlCompressorFlexibility(
                proto_stubs.ControlCompressorRequest(ski=self.ski, action=action),
                timeout=RPC_TIMEOUT,
            )
        except grpc.aio.AioRpcError as err:
            if _is_unimplemented(err):
                self._ohpcf_supported = False
                _LOGGER.info(
                    "OHPCF control unsupported for SKI %s: %s", self.ski, err.details()
                )
                return
            # Surface device-side rejections (e.g. "data not available" when the
            # compressor advertises no writable offer — heating-side OHPCF not yet
            # commissioned) as a clean validation error (HTTP 400 + message) instead
            # of bubbling a raw AioRpcError into an aiohttp 500 traceback.
            raise ServiceValidationError(
                f"Compressor flexibility control failed: {err.details()}"
            ) from err

    async def _async_read_dhw_setpoint(
        self, channel: grpc.aio.Channel, proto_stubs: Any, request: Any
    ) -> dict[str, Any] | None:
        """Read the DHW target and device-provided constraints."""
        try:
            stub = proto_stubs.dhw_service_stub(channel)
            setpoint = await stub.GetDHWSetpoint(request, timeout=RPC_TIMEOUT)
        except grpc.aio.AioRpcError as err:
            if _is_unimplemented(err) or err.code() in (
                grpc.StatusCode.NOT_FOUND,
                grpc.StatusCode.UNAVAILABLE,
            ):
                self._dhw_supported = False
            else:
                _LOGGER.debug(
                    "EEBUS DHW setpoint read failed for SKI %s: %s",
                    self.ski,
                    _rpc_error_text(err),
                )
            return None

        self._dhw_supported = True
        return _setpoint_to_dict(setpoint)

    async def async_write_dhw_setpoint(self, value_celsius: float) -> None:
        """Write the domestic-hot-water target via the bridge."""
        channel = await self._ensure_channel()
        from . import proto_stubs

        stub = proto_stubs.dhw_service_stub(channel)
        await self._async_write_rpc(
            "Domestic hot water setpoint",
            stub.SetDHWSetpoint,
            proto_stubs.SetDHWSetpointRequest(ski=self.ski, value_celsius=value_celsius),
            support_attr="_dhw_supported",
            validation=True,
        )

    async def _async_read_dhw_system_function(
        self, channel: grpc.aio.Channel, proto_stubs: Any, request: Any
    ) -> dict[str, Any] | None:
        """Read DHW boost and operation mode state."""
        try:
            stub = proto_stubs.dhw_service_stub(channel)
            state = await stub.GetDHWSystemFunction(request, timeout=RPC_TIMEOUT)
        except grpc.aio.AioRpcError as err:
            if _is_unimplemented(err) or err.code() in (
                grpc.StatusCode.NOT_FOUND,
                grpc.StatusCode.UNAVAILABLE,
            ):
                self._dhw_sysfn_supported = False
            else:
                _LOGGER.debug(
                    "EEBUS DHW system function read failed for SKI %s: %s",
                    self.ski,
                    _rpc_error_text(err),
                )
            return None

        self._dhw_sysfn_supported = True
        return _dhw_system_function_to_dict(state)

    async def async_set_dhw_boost(self, active: bool) -> None:
        """Activate or cancel DHW one-time boost."""
        channel = await self._ensure_channel()
        from . import proto_stubs

        stub = proto_stubs.dhw_service_stub(channel)
        await self._async_write_rpc(
            "Domestic hot water boost",
            stub.SetDHWBoost,
            proto_stubs.SetDHWBoostRequest(ski=self.ski, active=active),
            support_attr="_dhw_sysfn_supported",
            validation=True,
        )

    async def async_set_dhw_operation_mode(self, mode: str) -> None:
        """Set the DHW operation mode by advertised mode type."""
        channel = await self._ensure_channel()
        from . import proto_stubs

        stub = proto_stubs.dhw_service_stub(channel)
        await self._async_write_rpc(
            "Domestic hot water operation mode",
            stub.SetDHWOperationMode,
            proto_stubs.SetDHWOperationModeRequest(ski=self.ski, mode=mode),
            support_attr="_dhw_sysfn_supported",
            validation=True,
        )

    async def _async_read_room_heating(
        self, channel: grpc.aio.Channel, proto_stubs: Any, request: Any
    ) -> tuple[dict[str, Any] | None, dict[str, Any] | None]:
        """Read room-heating setpoint and system-function state."""
        try:
            state = await proto_stubs.hvac_service_stub(channel).GetRoomHeating(
                request, timeout=RPC_TIMEOUT
            )
        except grpc.aio.AioRpcError as err:
            if _is_unimplemented(err) or err.code() in (
                grpc.StatusCode.NOT_FOUND,
                grpc.StatusCode.UNAVAILABLE,
            ):
                self._room_heating_supported = False
            return None, None
        self._room_heating_supported = True
        setpoint = None
        if state.HasField("setpoint"):
            setpoint = _setpoint_to_dict(state.setpoint)
        system_function = None
        if state.HasField("system_function"):
            system_function = _system_function_to_dict(state.system_function)
        if state.HasField("current_temperature_celsius"):
            self._push_data({"room_temperature_c": state.current_temperature_celsius})
        return setpoint, system_function

    async def _async_read_device_diagnostics(
        self, channel: grpc.aio.Channel, proto_stubs: Any, request: Any
    ) -> str | None:
        """Read the device operating state without mutating coordinator data."""
        try:
            diagnostics = await proto_stubs.monitoring_service_stub(
                channel
            ).GetDeviceDiagnostics(request, timeout=RPC_TIMEOUT)
        except grpc.aio.AioRpcError as err:
            if not (
                _is_unimplemented(err)
                or err.code()
                in (grpc.StatusCode.NOT_FOUND, grpc.StatusCode.UNAVAILABLE)
            ):
                _LOGGER.debug(
                    "EEBUS device diagnosis read failed for SKI %s: %s",
                    self.ski,
                    _rpc_error_text(err),
                )
            return None
        return diagnostics.operating_state or None

    async def async_set_room_heating_temperature(self, value_celsius: float) -> None:
        """Set the room-heating target temperature."""
        channel = await self._ensure_channel()
        from . import proto_stubs

        await self._async_write_rpc(
            "Room heating setpoint",
            proto_stubs.hvac_service_stub(channel).SetRoomHeatingTemperature,
            proto_stubs.SetRoomHeatingTemperatureRequest(
                ski=self.ski, value_celsius=value_celsius
            ),
            support_attr="_room_heating_supported",
            validation=True,
        )

    async def async_set_room_heating_mode(self, mode: str) -> None:
        """Set the room-heating operation mode."""
        channel = await self._ensure_channel()
        from . import proto_stubs

        await self._async_write_rpc(
            "Room heating mode",
            proto_stubs.hvac_service_stub(channel).SetRoomHeatingMode,
            proto_stubs.SetRoomHeatingModeRequest(ski=self.ski, mode=mode),
            support_attr="_room_heating_supported",
            validation=True,
        )

    async def async_start_heartbeat(self) -> None:
        """Start EEBUS heartbeat via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.lpc_service_stub(channel)
        await self._async_write_rpc(
            "Heartbeat start",
            stub.StartHeartbeat,
            proto_stubs.DeviceRequest(ski=self.ski),
            support_attr="_heartbeat_supported",
        )

    async def async_stop_heartbeat(self) -> None:
        """Stop EEBUS heartbeat via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.lpc_service_stub(channel)
        await self._async_write_rpc(
            "Heartbeat stop",
            stub.StopHeartbeat,
            proto_stubs.DeviceRequest(ski=self.ski),
            support_attr="_heartbeat_supported",
        )

    @property
    def grid_push_enabled(self) -> bool:
        """Return True when a grid power sensor is mapped to the MGCP provider."""
        return bool(self.grid_power_entity)

    def _read_sensor_value(
        self,
        entity_id: str | None,
        unit_map: dict[str, float],
        kind: str,
        *,
        minimum: float | None = None,
        maximum: float | None = None,
    ) -> float | None:
        """Read an HA sensor and normalize it via unit_map (W / Wh / %).

        Returns None when the entity is unset, missing, unavailable,
        non-numeric, non-finite (NaN/Inf), or outside ``[minimum, maximum]`` so
        the caller can omit it from the push instead of advertising a bogus
        reading to downstream equipment. ``kind`` is a short descriptor (e.g.
        "grid power", "PV yield") used only for debug logging.
        """
        if not entity_id:
            return None
        state = self.hass.states.get(entity_id)
        if state is None or state.state in (STATE_UNKNOWN, STATE_UNAVAILABLE, "", None):
            return None
        try:
            value = float(state.state)
        except (TypeError, ValueError):
            _LOGGER.debug("%s sensor %s has non-numeric state %r", kind, entity_id, state.state)
            return None
        unit = state.attributes.get(ATTR_UNIT_OF_MEASUREMENT)
        factor = unit_map.get(unit) if isinstance(unit, str) else None
        if factor is None:
            _LOGGER.debug(
                "%s sensor %s has unknown unit %r; assuming base unit",
                kind,
                entity_id,
                unit,
            )
            factor = 1.0
        result = value * factor
        if not math.isfinite(result):
            _LOGGER.debug("%s sensor %s produced non-finite value %r", kind, entity_id, result)
            return None
        if (minimum is not None and result < minimum) or (
            maximum is not None and result > maximum
        ):
            _LOGGER.debug(
                "%s sensor %s value %r out of range [%s, %s]; omitting",
                kind,
                entity_id,
                result,
                minimum,
                maximum,
            )
            return None
        return result

    async def _async_publish_provider(
        self, label: str, stub_factory: str, publish_method: str, request: Any
    ) -> None:
        """Publish a provider reading to the bridge, quiet when the provider is off.

        UNIMPLEMENTED/UNAVAILABLE mean the provider is disabled or the bridge is
        down; skip quietly so a missing provider never spams or fails HA.
        """
        channel = await self._ensure_channel()
        from . import proto_stubs

        stub = getattr(proto_stubs, stub_factory)(channel)
        failure_state: dict[str, bool] | None = getattr(
            self, "_provider_push_failing", None
        )
        if failure_state is None:
            failure_state = self._provider_push_failing = {}
        try:
            await getattr(stub, publish_method)(request, timeout=RPC_TIMEOUT)
            if failure_state.pop(label, False):
                _LOGGER.info("%s provider push recovered", label)
            _LOGGER.debug("Pushed %s data: %s", label, request)
        except grpc.aio.AioRpcError as err:
            if _is_unimplemented(err) or err.code() == grpc.StatusCode.UNAVAILABLE:
                _LOGGER.debug(
                    "%s provider not ready; skipping push: %s", label, _rpc_error_text(err)
                )
                return
            if failure_state.get(label, False):
                _LOGGER.debug("Failed to push %s data: %s", label, _rpc_error_text(err))
            else:
                _LOGGER.warning("Failed to push %s data: %s", label, _rpc_error_text(err))
                failure_state[label] = True

    def _start_provider_push(
        self,
        label: str,
        tracked: tuple[str | None, ...],
        push: Callable[[], Awaitable[None]],
    ) -> None:
        """Start one lifecycle-owning provider push worker."""
        pusher = _ProviderPusher(
            self.hass,
            label,
            self.ski,
            tracked,
            push,
        )
        self._provider_pushers.append(pusher)
        pusher.start()

    async def async_push_grid_data(self) -> None:
        """Push the mapped grid sensors to the bridge MGCP provider.

        Grid power is the surplus signal (negative = export); the energy totals
        are optional. No-op when no grid power sensor is mapped or its value is
        currently unavailable.
        """
        if not self.grid_power_entity:
            return
        power_w = self._read_sensor_value(self.grid_power_entity, POWER_UNIT_TO_W, "grid power")
        if power_w is None:
            return
        feed_in_wh = self._read_sensor_value(
            self.grid_feed_in_energy_entity, ENERGY_UNIT_TO_WH, "grid feed-in", minimum=0
        )
        consumed_wh = self._read_sensor_value(
            self.grid_consumption_energy_entity, ENERGY_UNIT_TO_WH, "grid consumption", minimum=0
        )

        from . import proto_stubs

        request = proto_stubs.GridData(power_w=power_w)
        if feed_in_wh is not None:
            request.feed_in_wh = feed_in_wh
        if consumed_wh is not None:
            request.consumed_wh = consumed_wh
        await self._async_publish_provider(
            "grid", "grid_service_stub", "PublishGridData", request
        )

    def async_start_grid_push(self) -> None:
        """Track mapped grid sensors and push their values to the bridge."""
        if not self.grid_push_enabled:
            return
        self._start_provider_push(
            "grid",
            (
                self.grid_power_entity,
                self.grid_feed_in_energy_entity,
                self.grid_consumption_energy_entity,
            ),
            self.async_push_grid_data,
        )

    @property
    def pv_push_enabled(self) -> bool:
        """Return True when a PV power sensor is mapped to the VAPD provider."""
        return bool(self.pv_power_entity)

    async def async_push_pv_data(self) -> None:
        """Push the mapped PV sensors to the bridge VAPD (display) provider.

        PV power is required; yield energy and nominal peak power are optional.
        No-op when no PV power sensor is mapped or its value is unavailable.
        """
        if not self.pv_power_entity:
            return
        power_w = self._read_sensor_value(
            self.pv_power_entity, POWER_UNIT_TO_W, "PV power", minimum=0
        )
        if power_w is None:
            return
        yield_wh = self._read_sensor_value(
            self.pv_yield_energy_entity, ENERGY_UNIT_TO_WH, "PV yield", minimum=0
        )
        peak_power_w = self._read_sensor_value(
            self.pv_peak_power_entity, POWER_UNIT_TO_W, "PV peak power", minimum=0
        )

        from . import proto_stubs

        request = proto_stubs.PVData(power_w=power_w)
        if yield_wh is not None:
            request.yield_wh = yield_wh
        if peak_power_w is not None:
            request.peak_power_w = peak_power_w
        await self._async_publish_provider(
            "PV", "visualization_service_stub", "PublishPVData", request
        )

    def async_start_pv_push(self) -> None:
        """Track mapped PV sensors and push their values to the bridge."""
        if not self.pv_push_enabled:
            return
        self._start_provider_push(
            "pv",
            (
                self.pv_power_entity,
                self.pv_yield_energy_entity,
                self.pv_peak_power_entity,
            ),
            self.async_push_pv_data,
        )

    @property
    def battery_push_enabled(self) -> bool:
        """Return True when a battery power sensor is mapped to the VABD provider."""
        return bool(self.battery_power_entity)

    async def async_push_battery_data(self) -> None:
        """Push the mapped battery sensors to the bridge VABD (display) provider.

        Battery power is required; charged/discharged energy and state of charge
        are optional. No-op when no battery power sensor is mapped or its value is
        unavailable.
        """
        if not self.battery_power_entity:
            return
        power_w = self._read_sensor_value(self.battery_power_entity, POWER_UNIT_TO_W, "battery power")
        if power_w is None:
            return
        charged_wh = self._read_sensor_value(
            self.battery_charged_energy_entity, ENERGY_UNIT_TO_WH, "battery charged", minimum=0
        )
        discharged_wh = self._read_sensor_value(
            self.battery_discharged_energy_entity, ENERGY_UNIT_TO_WH, "battery discharged", minimum=0
        )
        soc_pct = self._read_sensor_value(
            self.battery_soc_entity, SOC_UNIT_TO_PCT, "battery SoC", minimum=0, maximum=100
        )

        from . import proto_stubs

        request = proto_stubs.BatteryData(power_w=power_w)
        if charged_wh is not None:
            request.charged_wh = charged_wh
        if discharged_wh is not None:
            request.discharged_wh = discharged_wh
        if soc_pct is not None:
            request.state_of_charge_pct = soc_pct
        await self._async_publish_provider(
            "battery", "visualization_service_stub", "PublishBatteryData", request
        )

    def async_start_battery_push(self) -> None:
        """Track mapped battery sensors and push their values to the bridge."""
        if not self.battery_push_enabled:
            return
        self._start_provider_push(
            "battery",
            (
                self.battery_power_entity,
                self.battery_charged_energy_entity,
                self.battery_discharged_energy_entity,
                self.battery_soc_entity,
            ),
            self.async_push_battery_data,
        )

    def async_start_streams(self) -> None:
        """Start background tasks consuming bridge event streams."""
        async def consume(channel: grpc.aio.Channel) -> None:
            from . import proto_stubs

            stub = proto_stubs.device_service_stub(channel)
            async for event in stub.SubscribeDeviceEvents(proto_stubs.Empty()):
                self._handle_device_event(event)

        async def consume_lpc(channel: grpc.aio.Channel) -> None:
            from . import proto_stubs

            stub = proto_stubs.lpc_service_stub(channel)
            async for event in stub.SubscribeLPCEvents(
                proto_stubs.DeviceRequest(ski=self.ski)
            ):
                self._handle_lpc_event(event)

        async def consume_measurements(channel: grpc.aio.Channel) -> None:
            from . import proto_stubs

            stub = proto_stubs.monitoring_service_stub(channel)
            async for event in stub.SubscribeMeasurements(
                proto_stubs.DeviceRequest(ski=self.ski)
            ):
                self._handle_measurement_event(event)

        async def consume_dhw(channel: grpc.aio.Channel) -> None:
            from . import proto_stubs

            stub = proto_stubs.dhw_service_stub(channel)
            async for event in stub.SubscribeDHWEvents(
                proto_stubs.DeviceRequest(ski=self.ski)
            ):
                self._handle_dhw_event(event)

        async def consume_dhw_sysfn(channel: grpc.aio.Channel) -> None:
            from . import proto_stubs

            stub = proto_stubs.dhw_service_stub(channel)
            async for event in stub.SubscribeDHWSystemFunctionEvents(
                proto_stubs.DeviceRequest(ski=self.ski)
            ):
                self._handle_dhw_sysfn_event(event)

        async def consume_room_heating(channel: grpc.aio.Channel) -> None:
            from . import proto_stubs

            stub = proto_stubs.hvac_service_stub(channel)
            async for event in stub.SubscribeRoomHeatingEvents(
                proto_stubs.DeviceRequest(ski=self.ski)
            ):
                self._handle_room_heating_event(event)

        streams: dict[str, ConsumeFn] = {
            "device_events": consume,
            "lpc_events": consume_lpc,
            "measurements": consume_measurements,
            "dhw_events": consume_dhw,
            "dhw_sysfn_events": consume_dhw_sysfn,
            "room_heating_events": consume_room_heating,
        }
        self._stream_manager.start(streams, f"eebus_{{name}}_{self.ski}")

    def _event_matches(self, event_ski: str) -> bool:
        """Return True when an event applies to the configured device."""
        return _normalize_ski(event_ski) == _normalize_ski(self.ski)

    def _push_data(self, updates: dict[str, Any]) -> None:
        """Merge stream updates into coordinator data and notify listeners."""
        if self.data is None:
            return
        self.async_set_updated_data({**self.data, **updates})

    def _handle_device_event(self, event: Any) -> None:
        from . import proto_stubs

        if not self._event_matches(event.ski):
            return
        event_type = event.event_type
        if event_type == proto_stubs.DeviceEventType.DEVICE_EVENT_TRUST_REMOVED:
            _LOGGER.warning("EEBUS device %s removed trust with bridge", event.ski)
        elif event_type not in (
            proto_stubs.DeviceEventType.DEVICE_EVENT_CONNECTED,
            proto_stubs.DeviceEventType.DEVICE_EVENT_DISCONNECTED,
        ):
            return
        # Connection state changed; reconcile everything via one poll.
        self.hass.async_create_task(self.async_request_refresh())

    def _handle_lpc_event(self, event: Any) -> None:
        from . import proto_stubs

        if not self._event_matches(event.ski):
            return
        event_type = event.event_type
        if event_type == proto_stubs.LPCEventType.LPC_EVENT_LIMIT_UPDATED:
            if not event.HasField("limit_update"):
                # Bridge signalled a change but sent no payload; reconcile via poll.
                self.hass.async_create_task(self.async_request_refresh())
                return
            limit = event.limit_update
            self._push_data(
                {
                    "consumption_limit": {
                        "value_watts": limit.value_watts,
                        "is_active": limit.is_active,
                        "is_changeable": limit.is_changeable,
                    }
                }
            )
        elif event_type == proto_stubs.LPCEventType.LPC_EVENT_FAILSAFE_UPDATED:
            if not event.HasField("failsafe_update"):
                self.hass.async_create_task(self.async_request_refresh())
                return
            failsafe = event.failsafe_update
            self._push_data(
                {
                    "failsafe_limit": {
                        "value_watts": failsafe.value_watts,
                        "duration_minimum_seconds": failsafe.duration_minimum_seconds,
                    }
                }
            )
        elif event_type == proto_stubs.LPCEventType.LPC_EVENT_HEARTBEAT_TIMEOUT:
            self.hass.async_create_task(self.async_request_refresh())

    def _handle_measurement_event(self, event: Any) -> None:
        if not self._event_matches(event.ski):
            return
        value_events, support_events = _measurement_event_map()
        if event.event_type in support_events:
            self.hass.async_create_task(self.async_request_refresh())
            return
        spec = value_events.get(event.event_type)
        if spec is None:
            return
        field, attr, key = spec
        if not event.HasField(field):
            # Change signalled without a payload; reconcile via poll.
            self.hass.async_create_task(self.async_request_refresh())
            return
        value = getattr(getattr(event, field), attr)
        if isinstance(value, str):
            # Empty enum/state strings mean "no value" (device_operating_state).
            value = value or None
        self._push_data({key: value})

    def _handle_dhw_event(self, event: Any) -> None:
        from . import proto_stubs

        if not self._event_matches(event.ski):
            return
        if (
            event.event_type == proto_stubs.DHWEventType.DHW_EVENT_SETPOINT_UPDATED
            and event.HasField("setpoint")
        ):
            setpoint = event.setpoint
            self._dhw_supported = True
            self._push_data(
                {
                    "dhw_setpoint": _setpoint_to_dict(setpoint),
                    "dhw_supported": True,
                }
            )
        elif event.event_type == proto_stubs.DHWEventType.DHW_EVENT_SUPPORT_UPDATED:
            self.hass.async_create_task(self.async_request_refresh())

    def _handle_dhw_sysfn_event(self, event: Any) -> None:
        from . import proto_stubs

        if not self._event_matches(event.ski):
            return
        if (
            event.event_type
            == proto_stubs.DHWSystemFunctionEventType.DHW_SYSTEM_FUNCTION_EVENT_STATE_UPDATED
            and event.HasField("state")
        ):
            self._dhw_sysfn_supported = True
            self._push_data(
                {
                    "dhw_system_function": _dhw_system_function_to_dict(event.state),
                    "dhw_sysfn_supported": True,
                }
            )
        elif (
            event.event_type
            == proto_stubs.DHWSystemFunctionEventType.DHW_SYSTEM_FUNCTION_EVENT_SUPPORT_UPDATED
        ):
            self.hass.async_create_task(self.async_request_refresh())

    def _handle_room_heating_event(self, event: Any) -> None:
        from . import proto_stubs

        if not self._event_matches(event.ski):
            return
        if event.event_type == proto_stubs.RoomHeatingEventType.ROOM_HEATING_EVENT_SUPPORT_UPDATED:
            self.hass.async_create_task(self.async_request_refresh())
            return
        if not event.HasField("state"):
            self.hass.async_create_task(self.async_request_refresh())
            return
        state = event.state
        updates: dict[str, Any] = {"room_heating_supported": True}
        self._room_heating_supported = True
        if state.HasField("current_temperature_celsius"):
            updates["room_temperature_c"] = state.current_temperature_celsius
        if state.HasField("setpoint"):
            updates["room_heating_setpoint"] = _setpoint_to_dict(state.setpoint)
        if state.HasField("system_function"):
            updates["room_heating_system_function"] = _system_function_to_dict(
                state.system_function
            )
        self._push_data(updates)

    async def async_shutdown(self) -> None:
        """Close gRPC channel and cancel stream tasks."""
        for pusher in self._provider_pushers:
            await pusher.stop()
        self._provider_pushers.clear()
        await self._stream_manager.stop()
        await self._channel_manager.close()
