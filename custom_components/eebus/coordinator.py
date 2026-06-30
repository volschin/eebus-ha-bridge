"""DataUpdateCoordinator for EEBUS integration."""

from __future__ import annotations

import asyncio
import logging
import math
from datetime import timedelta
from typing import Any

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
from homeassistant.helpers.event import async_track_state_change_event
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed

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
RPC_TIMEOUT = 10
RE_REGISTER_NOT_FOUND_STREAK = 4
STREAM_RETRY_SECONDS = 30

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
}
FLAT_MEASUREMENT_KEYS: tuple[str, ...] = tuple(FLAT_MEASUREMENT_TYPE_TO_KEY.values())


def _is_unimplemented(err: grpc.aio.AioRpcError) -> bool:
    """Return True when gRPC reports method/use case is not implemented."""
    return err.code() == grpc.StatusCode.UNIMPLEMENTED


def _is_not_found(err: grpc.aio.AioRpcError) -> bool:
    """Return True when gRPC reports missing entity/data for requested SKI."""
    return err.code() == grpc.StatusCode.NOT_FOUND


def _rpc_error_text(err: grpc.aio.AioRpcError) -> str:
    """Build compact debug output for gRPC errors."""
    return f"code={err.code().name} details={err.details()}"


class EebusCoordinator(DataUpdateCoordinator[dict[str, Any]]):
    """Coordinator that manages gRPC connection and data updates."""

    def __init__(
        self,
        hass: HomeAssistant,
        host: str,
        port: int,
        ski: str,
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
        self._channel: grpc.aio.Channel | None = None
        self._stream_tasks: list[asyncio.Task] = []
        self._grid_unsub: Any = None
        self._pv_unsub: Any = None
        self._battery_unsub: Any = None
        self._was_unavailable: bool = False
        self._heartbeat_supported: bool | None = None
        self._lpc_supported: bool | None = None
        self._failsafe_supported: bool | None = None
        self._ohpcf_supported: bool | None = None
        self._ski_registered: bool = False
        self._not_found_streak: int = 0

    async def _ensure_channel(self) -> grpc.aio.Channel:
        """Create or return existing gRPC channel."""
        if self._channel is None:
            self._channel = grpc.aio.insecure_channel(f"{self.host}:{self.port}")
        return self._channel

    async def _async_update_data(self) -> dict[str, Any]:
        """Fetch data via gRPC polling."""
        try:
            channel = await self._ensure_channel()
            from . import proto_stubs

            device_stub = proto_stubs.DeviceServiceStub(channel)
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

            monitoring_stub = proto_stubs.MonitoringServiceStub(channel)
            request = proto_stubs.DeviceRequest(ski=self.ski)
            fallback_request = proto_stubs.DeviceRequest(ski="")
            used_fallback = False
            saw_not_found = False

            try:
                power = await monitoring_stub.GetPowerConsumption(
                    request, timeout=RPC_TIMEOUT
                )
                data["power_watts"] = power.watts
                _LOGGER.debug(
                    "EEBUS power read for SKI %s succeeded: watts=%s",
                    self.ski,
                    power.watts,
                )
            except grpc.aio.AioRpcError as err:
                if _is_not_found(err):
                    saw_not_found = True
                    try:
                        power = await monitoring_stub.GetPowerConsumption(
                            fallback_request, timeout=RPC_TIMEOUT
                        )
                        data["power_watts"] = power.watts
                        used_fallback = True
                        _LOGGER.debug(
                            "EEBUS power read for SKI %s used fallback entity: watts=%s",
                            self.ski,
                            power.watts,
                        )
                    except grpc.aio.AioRpcError as retry_err:
                        data["power_watts"] = None
                        _LOGGER.debug(
                            "EEBUS power read failed for SKI %s and fallback: %s",
                            self.ski,
                            _rpc_error_text(retry_err),
                        )
                else:
                    data["power_watts"] = None
                    _LOGGER.debug(
                        "EEBUS power read failed for SKI %s: %s",
                        self.ski,
                        _rpc_error_text(err),
                    )
            except Exception:  # noqa: BLE001
                _LOGGER.exception("Failed to read power consumption")
                data["power_watts"] = None

            try:
                measurements = await monitoring_stub.GetMeasurements(
                    request, timeout=RPC_TIMEOUT
                )
                scoped_energy = self._extract_scoped_energy_kwh(measurements.measurements)
                data["energy_consumed_heating_kwh"] = scoped_energy["heating"]
                data["energy_consumed_dhw_kwh"] = scoped_energy["dhw"]
                data.update(self._extract_flat_measurements(measurements.measurements))
                _LOGGER.debug(
                    "EEBUS scoped energy read for SKI %s: heating=%s dhw=%s entries=%s",
                    self.ski,
                    data["energy_consumed_heating_kwh"],
                    data["energy_consumed_dhw_kwh"],
                    len(measurements.measurements),
                )
            except grpc.aio.AioRpcError as err:
                if _is_not_found(err):
                    saw_not_found = True
                    try:
                        measurements = await monitoring_stub.GetMeasurements(
                            fallback_request, timeout=RPC_TIMEOUT
                        )
                        scoped_energy = self._extract_scoped_energy_kwh(
                            measurements.measurements
                        )
                        data["energy_consumed_heating_kwh"] = scoped_energy["heating"]
                        data["energy_consumed_dhw_kwh"] = scoped_energy["dhw"]
                        data.update(
                            self._extract_flat_measurements(measurements.measurements)
                        )
                        used_fallback = True
                        _LOGGER.debug(
                            "EEBUS scoped energy read for SKI %s used fallback: heating=%s dhw=%s entries=%s",
                            self.ski,
                            data["energy_consumed_heating_kwh"],
                            data["energy_consumed_dhw_kwh"],
                            len(measurements.measurements),
                        )
                    except grpc.aio.AioRpcError as retry_err:
                        data["energy_consumed_heating_kwh"] = None
                        data["energy_consumed_dhw_kwh"] = None
                        _LOGGER.debug(
                            "EEBUS scoped energy read failed for SKI %s and fallback: %s",
                            self.ski,
                            _rpc_error_text(retry_err),
                        )
                else:
                    data["energy_consumed_heating_kwh"] = None
                    data["energy_consumed_dhw_kwh"] = None
                    _LOGGER.debug(
                        "EEBUS scoped energy read failed for SKI %s: %s",
                        self.ski,
                        _rpc_error_text(err),
                    )
            except Exception:  # noqa: BLE001
                _LOGGER.exception("Failed to read scoped energy measurements")
                data["energy_consumed_heating_kwh"] = None
                data["energy_consumed_dhw_kwh"] = None

            try:
                energy = await monitoring_stub.GetEnergyConsumed(
                    request, timeout=RPC_TIMEOUT
                )
                data["energy_consumed_kwh"] = energy.kilowatt_hours
                _LOGGER.debug(
                    "EEBUS total energy read for SKI %s succeeded: kWh=%s",
                    self.ski,
                    energy.kilowatt_hours,
                )
            except grpc.aio.AioRpcError as err:
                if _is_not_found(err):
                    saw_not_found = True
                    try:
                        energy = await monitoring_stub.GetEnergyConsumed(
                            fallback_request, timeout=RPC_TIMEOUT
                        )
                        data["energy_consumed_kwh"] = energy.kilowatt_hours
                        used_fallback = True
                        _LOGGER.debug(
                            "EEBUS total energy read for SKI %s used fallback: kWh=%s",
                            self.ski,
                            energy.kilowatt_hours,
                        )
                    except grpc.aio.AioRpcError as retry_err:
                        data["energy_consumed_kwh"] = None
                        _LOGGER.debug(
                            "EEBUS total energy read failed for SKI %s and fallback: %s",
                            self.ski,
                            _rpc_error_text(retry_err),
                        )
                else:
                    data["energy_consumed_kwh"] = None
                    _LOGGER.debug(
                        "EEBUS total energy read failed for SKI %s: %s",
                        self.ski,
                        _rpc_error_text(err),
                    )
            except Exception:  # noqa: BLE001
                _LOGGER.exception("Failed to read total consumed energy")
                data["energy_consumed_kwh"] = None

            try:
                lpc_stub = proto_stubs.LPCServiceStub(channel)
                limit = await lpc_stub.GetConsumptionLimit(
                    request, timeout=RPC_TIMEOUT
                )
                data["consumption_limit"] = {
                    "value_watts": limit.value_watts,
                    "is_active": limit.is_active,
                    "is_changeable": limit.is_changeable,
                }
                self._lpc_supported = True
                _LOGGER.debug(
                    "EEBUS consumption limit read for SKI %s: value=%s active=%s changeable=%s",
                    self.ski,
                    limit.value_watts,
                    limit.is_active,
                    limit.is_changeable,
                )
            except grpc.aio.AioRpcError as err:
                if _is_not_found(err):
                    saw_not_found = True
                    try:
                        limit = await lpc_stub.GetConsumptionLimit(
                            fallback_request, timeout=RPC_TIMEOUT
                        )
                        data["consumption_limit"] = {
                            "value_watts": limit.value_watts,
                            "is_active": limit.is_active,
                            "is_changeable": limit.is_changeable,
                        }
                        self._lpc_supported = True
                        used_fallback = True
                        _LOGGER.debug(
                            "EEBUS consumption limit read for SKI %s used fallback: value=%s active=%s changeable=%s",
                            self.ski,
                            limit.value_watts,
                            limit.is_active,
                            limit.is_changeable,
                        )
                    except grpc.aio.AioRpcError as retry_err:
                        data["consumption_limit"] = None
                        _LOGGER.debug(
                            "EEBUS consumption limit read failed for SKI %s and fallback: %s",
                            self.ski,
                            _rpc_error_text(retry_err),
                        )
                        if _is_unimplemented(retry_err):
                            self._lpc_supported = False
                else:
                    data["consumption_limit"] = None
                    _LOGGER.debug(
                        "EEBUS consumption limit read failed for SKI %s: %s",
                        self.ski,
                        _rpc_error_text(err),
                    )
                    if _is_unimplemented(err):
                        self._lpc_supported = False

            try:
                lpc_stub = proto_stubs.LPCServiceStub(channel)
                failsafe = await lpc_stub.GetFailsafeLimit(
                    request, timeout=RPC_TIMEOUT
                )
                data["failsafe_limit"] = {
                    "value_watts": failsafe.value_watts,
                    "duration_minimum_seconds": failsafe.duration_minimum_seconds,
                }
                self._failsafe_supported = True
                _LOGGER.debug(
                    "EEBUS failsafe read for SKI %s: value=%s min_duration_s=%s",
                    self.ski,
                    failsafe.value_watts,
                    failsafe.duration_minimum_seconds,
                )
            except grpc.aio.AioRpcError as err:
                if _is_not_found(err):
                    saw_not_found = True
                    try:
                        failsafe = await lpc_stub.GetFailsafeLimit(
                            fallback_request, timeout=RPC_TIMEOUT
                        )
                        data["failsafe_limit"] = {
                            "value_watts": failsafe.value_watts,
                            "duration_minimum_seconds": failsafe.duration_minimum_seconds,
                        }
                        self._failsafe_supported = True
                        used_fallback = True
                        _LOGGER.debug(
                            "EEBUS failsafe read for SKI %s used fallback: value=%s min_duration_s=%s",
                            self.ski,
                            failsafe.value_watts,
                            failsafe.duration_minimum_seconds,
                        )
                    except grpc.aio.AioRpcError as retry_err:
                        data["failsafe_limit"] = None
                        _LOGGER.debug(
                            "EEBUS failsafe read failed for SKI %s and fallback: %s",
                            self.ski,
                            _rpc_error_text(retry_err),
                        )
                        if _is_unimplemented(retry_err):
                            self._failsafe_supported = False
                else:
                    data["failsafe_limit"] = None
                    _LOGGER.debug(
                        "EEBUS failsafe read failed for SKI %s: %s",
                        self.ski,
                        _rpc_error_text(err),
                    )
                    if _is_unimplemented(err):
                        self._failsafe_supported = False

            try:
                lpc_stub = proto_stubs.LPCServiceStub(channel)
                hb = await lpc_stub.GetHeartbeatStatus(
                    request, timeout=RPC_TIMEOUT
                )
                data["heartbeat_status"] = {
                    "running": hb.running,
                    "within_duration": hb.within_duration,
                }
                data["heartbeat_supported"] = True
                self._heartbeat_supported = True
                _LOGGER.debug(
                    "EEBUS heartbeat status for SKI %s: running=%s within_duration=%s",
                    self.ski,
                    hb.running,
                    hb.within_duration,
                )
            except grpc.aio.AioRpcError as err:
                if _is_not_found(err):
                    saw_not_found = True
                    try:
                        hb = await lpc_stub.GetHeartbeatStatus(
                            fallback_request, timeout=RPC_TIMEOUT
                        )
                        data["heartbeat_status"] = {
                            "running": hb.running,
                            "within_duration": hb.within_duration,
                        }
                        data["heartbeat_supported"] = True
                        self._heartbeat_supported = True
                        used_fallback = True
                        _LOGGER.debug(
                            "EEBUS heartbeat read for SKI %s used fallback: running=%s within_duration=%s",
                            self.ski,
                            hb.running,
                            hb.within_duration,
                        )
                    except grpc.aio.AioRpcError as retry_err:
                        data["heartbeat_status"] = None
                        data["heartbeat_supported"] = self._heartbeat_supported
                        _LOGGER.debug(
                            "EEBUS heartbeat read failed for SKI %s and fallback: %s",
                            self.ski,
                            _rpc_error_text(retry_err),
                        )
                        if _is_unimplemented(retry_err):
                            data["heartbeat_supported"] = False
                            self._heartbeat_supported = False
                else:
                    data["heartbeat_status"] = None
                    data["heartbeat_supported"] = self._heartbeat_supported
                    _LOGGER.debug(
                        "EEBUS heartbeat read failed for SKI %s: %s",
                        self.ski,
                        _rpc_error_text(err),
                    )
                    if _is_unimplemented(err):
                        data["heartbeat_supported"] = False
                        self._heartbeat_supported = False
            except Exception:  # noqa: BLE001
                _LOGGER.exception("Failed to read heartbeat status")
                data["heartbeat_status"] = None
                data["heartbeat_supported"] = self._heartbeat_supported

            data["lpc_supported"] = self._lpc_supported
            data["failsafe_supported"] = self._failsafe_supported
            data["read_fallback_used"] = used_fallback
            data["device_info"] = await self._async_fetch_device_info(
                device_stub, proto_stubs, allow_fallback=used_fallback
            )

            # OHPCF (heat-pump compressor flexibility): read the compressor's
            # optional-consumption offer. Unsupported/unavailable when the bridge
            # OHPCF client is off; the entities then stay unavailable.
            data["compressor_flexibility"] = await self._async_read_compressor_flexibility(
                channel, proto_stubs, request
            )
            data["ohpcf_supported"] = self._ohpcf_supported

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
                "EEBUS poll summary for SKI %s: power=%s energy_total=%s energy_heating=%s energy_dhw=%s fallback=%s",
                self.ski,
                data["power_watts"],
                data["energy_consumed_kwh"],
                data["energy_consumed_heating_kwh"],
                data["energy_consumed_dhw_kwh"],
                used_fallback,
            )

            if self._was_unavailable:
                _LOGGER.info("EEBUS bridge connection restored at %s:%s", self.host, self.port)
                self._was_unavailable = False

            return data
        except grpc.aio.AioRpcError as err:
            if self._channel is not None:
                await self._channel.close()
                self._channel = None
            self._not_found_streak = 0

            if not self._was_unavailable:
                _LOGGER.warning(
                    "EEBUS bridge unavailable at %s:%s: %s", self.host, self.port, err
                )
                self._was_unavailable = True

            raise UpdateFailed(f"gRPC error: {err}") from err

    async def _async_fetch_device_info(
        self, device_stub: Any, proto_stubs: Any, allow_fallback: bool
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
        if match is None and allow_fallback and len(devices) == 1:
            match = devices[0]
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
        self, device_stub: Any, proto_stubs: Any, force: bool
    ) -> None:
        """Register remote SKI with bridge, optionally forcing re-registration."""
        try:
            register_request_cls = getattr(proto_stubs, "RegisterSKIRequest", None)
            if register_request_cls is None:
                from .generated.eebus.v1.device_service_pb2 import (
                    RegisterSKIRequest as register_request_cls,
                )

            await device_stub.RegisterRemoteSKI(
                register_request_cls(ski=self.ski), timeout=RPC_TIMEOUT
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

    async def async_write_lpc_limit(self, value_watts: float) -> None:
        """Write LPC consumption limit via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.LPCServiceStub(channel)
        try:
            await stub.WriteConsumptionLimit(
                proto_stubs.WriteLoadLimitRequest(
                    ski=self.ski, value_watts=value_watts, is_active=True
                ),
                timeout=RPC_TIMEOUT,
            )
            self._lpc_supported = True
        except grpc.aio.AioRpcError as err:
            if _is_unimplemented(err):
                self._lpc_supported = False
                _LOGGER.info(
                    "LPC write unsupported for SKI %s: %s", self.ski, err.details()
                )
                return
            raise

    async def async_write_failsafe_limit(self, value_watts: float) -> None:
        """Write failsafe limit via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.LPCServiceStub(channel)
        try:
            await stub.WriteFailsafeLimit(
                proto_stubs.WriteFailsafeLimitRequest(
                    ski=self.ski, value_watts=value_watts
                ),
                timeout=RPC_TIMEOUT,
            )
            self._failsafe_supported = True
        except grpc.aio.AioRpcError as err:
            if _is_unimplemented(err):
                self._failsafe_supported = False
                _LOGGER.info(
                    "Failsafe write unsupported for SKI %s: %s", self.ski, err.details()
                )
                return
            raise

    async def async_set_lpc_active(self, active: bool) -> None:
        """Activate or deactivate LPC limit via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.LPCServiceStub(channel)
        current = await stub.GetConsumptionLimit(
            proto_stubs.DeviceRequest(ski=self.ski), timeout=RPC_TIMEOUT
        )
        try:
            await stub.WriteConsumptionLimit(
                proto_stubs.WriteLoadLimitRequest(
                    ski=self.ski,
                    value_watts=current.value_watts,
                    is_active=active,
                ),
                timeout=RPC_TIMEOUT,
            )
            self._lpc_supported = True
        except grpc.aio.AioRpcError as err:
            if _is_unimplemented(err):
                self._lpc_supported = False
                _LOGGER.info(
                    "LPC activation unsupported for SKI %s: %s", self.ski, err.details()
                )
                return
            raise

    async def _async_read_compressor_flexibility(
        self, channel: grpc.aio.Channel, proto_stubs: Any, request: Any
    ) -> dict[str, Any] | None:
        """Read the OHPCF compressor flexibility offer/state, or None when off."""
        from .generated.eebus.v1 import ohpcf_service_pb2 as ohpcf_pb2

        try:
            stub = proto_stubs.OHPCFServiceStub(channel)
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

    async def async_control_compressor(self, action: int) -> None:
        """Schedule/pause/resume/abort the compressor's optional consumption."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.OHPCFServiceStub(channel)
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
            raise

    async def async_start_heartbeat(self) -> None:
        """Start EEBUS heartbeat via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.LPCServiceStub(channel)
        try:
            await stub.StartHeartbeat(
                proto_stubs.DeviceRequest(ski=self.ski), timeout=RPC_TIMEOUT
            )
        except grpc.aio.AioRpcError as err:
            if _is_unimplemented(err):
                self._heartbeat_supported = False
                _LOGGER.info(
                    "Heartbeat start unsupported for SKI %s: %s", self.ski, err.details()
                )
                return
            raise

    async def async_stop_heartbeat(self) -> None:
        """Stop EEBUS heartbeat via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.LPCServiceStub(channel)
        try:
            await stub.StopHeartbeat(
                proto_stubs.DeviceRequest(ski=self.ski), timeout=RPC_TIMEOUT
            )
        except grpc.aio.AioRpcError as err:
            if _is_unimplemented(err):
                self._heartbeat_supported = False
                _LOGGER.info(
                    "Heartbeat stop unsupported for SKI %s: %s", self.ski, err.details()
                )
                return
            raise

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
        factor = unit_map.get(unit)
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

        channel = await self._ensure_channel()
        from . import proto_stubs

        stub = proto_stubs.GridServiceStub(channel)
        request = proto_stubs.GridData(power_w=power_w)
        if feed_in_wh is not None:
            request.feed_in_wh = feed_in_wh
        if consumed_wh is not None:
            request.consumed_wh = consumed_wh
        try:
            await stub.PublishGridData(request, timeout=RPC_TIMEOUT)
            _LOGGER.debug(
                "Pushed grid data: power=%.1fW feed_in=%s consumed=%s",
                power_w,
                feed_in_wh,
                consumed_wh,
            )
        except grpc.aio.AioRpcError as err:
            # UNIMPLEMENTED/UNAVAILABLE = provider disabled or bridge down; skip
            # quietly so a missing grid provider never spams or fails HA.
            if _is_unimplemented(err) or err.code() == grpc.StatusCode.UNAVAILABLE:
                _LOGGER.debug("Grid provider not ready; skipping push: %s", _rpc_error_text(err))
                return
            _LOGGER.warning("Failed to push grid data: %s", _rpc_error_text(err))

    def async_start_grid_push(self) -> None:
        """Track mapped grid sensors and push their values to the bridge."""
        if not self.grid_push_enabled:
            return
        tracked = [
            entity_id
            for entity_id in (
                self.grid_power_entity,
                self.grid_feed_in_energy_entity,
                self.grid_consumption_energy_entity,
            )
            if entity_id
        ]
        self._grid_unsub = async_track_state_change_event(
            self.hass, tracked, self._handle_grid_state_change
        )
        # Initial push so the provider has data before the first sensor change.
        self._stream_tasks.append(
            self.hass.async_create_background_task(
                self.async_push_grid_data(), name=f"eebus_grid_initial_push_{self.ski}"
            )
        )

    async def _handle_grid_state_change(self, _event: Event) -> None:
        """Push grid data whenever a mapped sensor changes state."""
        await self.async_push_grid_data()

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

        channel = await self._ensure_channel()
        from . import proto_stubs

        stub = proto_stubs.VisualizationServiceStub(channel)
        request = proto_stubs.PVData(power_w=power_w)
        if yield_wh is not None:
            request.yield_wh = yield_wh
        if peak_power_w is not None:
            request.peak_power_w = peak_power_w
        try:
            await stub.PublishPVData(request, timeout=RPC_TIMEOUT)
            _LOGGER.debug(
                "Pushed PV data: power=%.1fW yield=%s peak=%s",
                power_w,
                yield_wh,
                peak_power_w,
            )
        except grpc.aio.AioRpcError as err:
            if _is_unimplemented(err) or err.code() == grpc.StatusCode.UNAVAILABLE:
                _LOGGER.debug("PV provider not ready; skipping push: %s", _rpc_error_text(err))
                return
            _LOGGER.warning("Failed to push PV data: %s", _rpc_error_text(err))

    def async_start_pv_push(self) -> None:
        """Track mapped PV sensors and push their values to the bridge."""
        if not self.pv_push_enabled:
            return
        tracked = [
            entity_id
            for entity_id in (
                self.pv_power_entity,
                self.pv_yield_energy_entity,
                self.pv_peak_power_entity,
            )
            if entity_id
        ]
        self._pv_unsub = async_track_state_change_event(
            self.hass, tracked, self._handle_pv_state_change
        )
        self._stream_tasks.append(
            self.hass.async_create_background_task(
                self.async_push_pv_data(), name=f"eebus_pv_initial_push_{self.ski}"
            )
        )

    async def _handle_pv_state_change(self, _event: Event) -> None:
        """Push PV data whenever a mapped sensor changes state."""
        await self.async_push_pv_data()

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

        channel = await self._ensure_channel()
        from . import proto_stubs

        stub = proto_stubs.VisualizationServiceStub(channel)
        request = proto_stubs.BatteryData(power_w=power_w)
        if charged_wh is not None:
            request.charged_wh = charged_wh
        if discharged_wh is not None:
            request.discharged_wh = discharged_wh
        if soc_pct is not None:
            request.state_of_charge_pct = soc_pct
        try:
            await stub.PublishBatteryData(request, timeout=RPC_TIMEOUT)
            _LOGGER.debug(
                "Pushed battery data: power=%.1fW charged=%s discharged=%s soc=%s",
                power_w,
                charged_wh,
                discharged_wh,
                soc_pct,
            )
        except grpc.aio.AioRpcError as err:
            if _is_unimplemented(err) or err.code() == grpc.StatusCode.UNAVAILABLE:
                _LOGGER.debug("Battery provider not ready; skipping push: %s", _rpc_error_text(err))
                return
            _LOGGER.warning("Failed to push battery data: %s", _rpc_error_text(err))

    def async_start_battery_push(self) -> None:
        """Track mapped battery sensors and push their values to the bridge."""
        if not self.battery_push_enabled:
            return
        tracked = [
            entity_id
            for entity_id in (
                self.battery_power_entity,
                self.battery_charged_energy_entity,
                self.battery_discharged_energy_entity,
                self.battery_soc_entity,
            )
            if entity_id
        ]
        self._battery_unsub = async_track_state_change_event(
            self.hass, tracked, self._handle_battery_state_change
        )
        self._stream_tasks.append(
            self.hass.async_create_background_task(
                self.async_push_battery_data(), name=f"eebus_battery_initial_push_{self.ski}"
            )
        )

    async def _handle_battery_state_change(self, _event: Event) -> None:
        """Push battery data whenever a mapped sensor changes state."""
        await self.async_push_battery_data()

    def async_start_streams(self) -> None:
        """Start background tasks consuming bridge event streams."""
        if self._stream_tasks:
            return
        for name, runner in (
            ("device_events", self._run_device_event_stream),
            ("lpc_events", self._run_lpc_event_stream),
            ("measurements", self._run_measurement_stream),
        ):
            self._stream_tasks.append(
                self.hass.async_create_background_task(
                    runner(), name=f"eebus_{name}_{self.ski}"
                )
            )

    async def _run_stream(self, name: str, consume: Any) -> None:
        """Run a stream consumer with reconnect/backoff until cancelled."""
        while True:
            try:
                channel = await self._ensure_channel()
                await consume(channel)
            except asyncio.CancelledError:
                raise
            except grpc.aio.AioRpcError as err:
                if _is_unimplemented(err):
                    _LOGGER.info(
                        "EEBUS %s stream not supported by bridge; relying on polling",
                        name,
                    )
                    return
                _LOGGER.debug(
                    "EEBUS %s stream ended (%s); retrying in %ss",
                    name,
                    _rpc_error_text(err),
                    STREAM_RETRY_SECONDS,
                )
            except Exception:  # noqa: BLE001
                _LOGGER.exception("EEBUS %s stream failed; retrying", name)
            await asyncio.sleep(STREAM_RETRY_SECONDS)

    async def _run_device_event_stream(self) -> None:
        from . import proto_stubs

        async def consume(channel: grpc.aio.Channel) -> None:
            stub = proto_stubs.DeviceServiceStub(channel)
            async for event in stub.SubscribeDeviceEvents(proto_stubs.Empty()):
                self._handle_device_event(event)

        await self._run_stream("device", consume)

    async def _run_lpc_event_stream(self) -> None:
        from . import proto_stubs

        async def consume(channel: grpc.aio.Channel) -> None:
            stub = proto_stubs.LPCServiceStub(channel)
            async for event in stub.SubscribeLPCEvents(
                proto_stubs.DeviceRequest(ski=self.ski)
            ):
                self._handle_lpc_event(event)

        await self._run_stream("LPC", consume)

    async def _run_measurement_stream(self) -> None:
        from . import proto_stubs

        async def consume(channel: grpc.aio.Channel) -> None:
            stub = proto_stubs.MonitoringServiceStub(channel)
            async for event in stub.SubscribeMeasurements(
                proto_stubs.DeviceRequest(ski=self.ski)
            ):
                self._handle_measurement_event(event)

        await self._run_stream("measurement", consume)

    def _event_matches(self, event_ski: str) -> bool:
        """Return True when an event applies to the configured device."""
        if not event_ski or event_ski == self.ski:
            return True
        # Reads fell back to the first available entity; its events are ours.
        return bool(self.data and self.data.get("read_fallback_used"))

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
        from . import proto_stubs

        if not self._event_matches(event.ski):
            return
        event_type = event.event_type
        if event_type == proto_stubs.MeasurementEventType.MEASUREMENT_EVENT_POWER_UPDATED:
            if event.HasField("power"):
                self._push_data({"power_watts": event.power.watts})
            else:
                # Change signalled without a payload; reconcile via poll.
                self.hass.async_create_task(self.async_request_refresh())
        elif event_type == proto_stubs.MeasurementEventType.MEASUREMENT_EVENT_ENERGY_UPDATED:
            if event.HasField("energy"):
                self._push_data({"energy_consumed_kwh": event.energy.kilowatt_hours})
            else:
                self.hass.async_create_task(self.async_request_refresh())

    async def async_shutdown(self) -> None:
        """Close gRPC channel and cancel stream tasks."""
        if self._grid_unsub is not None:
            self._grid_unsub()
            self._grid_unsub = None
        if self._pv_unsub is not None:
            self._pv_unsub()
            self._pv_unsub = None
        if self._battery_unsub is not None:
            self._battery_unsub()
            self._battery_unsub = None
        for task in self._stream_tasks:
            task.cancel()
        self._stream_tasks.clear()
        if self._channel is not None:
            await self._channel.close()
            self._channel = None
