"""DataUpdateCoordinator for EEBUS integration."""

from __future__ import annotations

import asyncio
import logging
from datetime import timedelta
from typing import Any

import grpc
import grpc.aio

from homeassistant.core import HomeAssistant
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed

_LOGGER = logging.getLogger(__name__)

POLL_INTERVAL = timedelta(seconds=30)
RPC_TIMEOUT = 10
RE_REGISTER_NOT_FOUND_STREAK = 4


def _is_unimplemented(err: grpc.aio.AioRpcError) -> bool:
    """Return True when gRPC reports method/use case is not implemented."""
    return err.code() == grpc.StatusCode.UNIMPLEMENTED


def _is_not_found(err: grpc.aio.AioRpcError) -> bool:
    """Return True when gRPC reports missing entity/data for requested SKI."""
    return err.code() == grpc.StatusCode.NOT_FOUND


def _rpc_error_text(err: grpc.aio.AioRpcError) -> str:
    """Build compact debug output for gRPC errors."""
    return f"code={err.code().name} details={err.details()}"


def _normalize_ski(ski: str) -> str:
    """Normalize SKI input to the compact uppercase representation used by the bridge."""
    return ski.strip().replace(" ", "").upper()


class EebusCoordinator(DataUpdateCoordinator[dict[str, Any]]):
    """Coordinator that manages gRPC connection and data updates."""

    def __init__(
        self,
        hass: HomeAssistant,
        host: str,
        port: int,
        ski: str,
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
        self.ski = _normalize_ski(ski)
        self._channel: grpc.aio.Channel | None = None
        self._stream_tasks: list[asyncio.Task] = []
        self._was_unavailable: bool = False
        self._heartbeat_supported: bool | None = None
        self._lpc_supported: bool | None = None
        self._failsafe_supported: bool | None = None
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

    async def async_shutdown(self) -> None:
        """Close gRPC channel and cancel stream tasks."""
        for task in self._stream_tasks:
            task.cancel()
        self._stream_tasks.clear()
        if self._channel is not None:
            await self._channel.close()
            self._channel = None
