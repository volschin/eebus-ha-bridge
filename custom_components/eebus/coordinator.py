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


def _is_unimplemented(err: grpc.aio.AioRpcError) -> bool:
    return err.code() == grpc.StatusCode.UNIMPLEMENTED


def _rpc_error_text(err: grpc.aio.AioRpcError) -> str:
    return f"code={err.code().name} details={err.details()}"


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
        self.ski = ski
        self._channel: grpc.aio.Channel | None = None
        self._stream_tasks: list[asyncio.Task] = []
        self._was_unavailable: bool = False
        self._lpc_supported: bool | None = None
        self._failsafe_supported: bool | None = None

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

            data: dict[str, Any] = {
                "connected": status.running,
                "local_ski": status.local_ski,
                "lpc_supported": self._lpc_supported,
                "failsafe_supported": self._failsafe_supported,
            }

            request = proto_stubs.DeviceRequest(ski=self.ski)
            monitoring_stub = proto_stubs.MonitoringServiceStub(channel)
            lpc_stub = proto_stubs.LPCServiceStub(channel)

            try:
                power = await monitoring_stub.GetPowerConsumption(request, timeout=RPC_TIMEOUT)
                data["power_watts"] = power.watts
            except grpc.aio.AioRpcError:
                data["power_watts"] = None

            try:
                energy = await monitoring_stub.GetEnergyConsumed(request, timeout=RPC_TIMEOUT)
                data["energy_consumed_kwh"] = energy.kilowatt_hours
            except grpc.aio.AioRpcError:
                data["energy_consumed_kwh"] = None

            try:
                mlist = await monitoring_stub.GetMeasurements(request, timeout=RPC_TIMEOUT)
                scoped = self._extract_scoped_energy_kwh(mlist.measurements)
                data["energy_consumed_heating_kwh"] = scoped["heating"]
                data["energy_consumed_dhw_kwh"] = scoped["dhw"]
            except grpc.aio.AioRpcError:
                data["energy_consumed_heating_kwh"] = None
                data["energy_consumed_dhw_kwh"] = None

            try:
                limit = await lpc_stub.GetConsumptionLimit(request, timeout=RPC_TIMEOUT)
                data["consumption_limit"] = {
                    "value_watts": limit.value_watts,
                    "is_active": limit.is_active,
                    "is_changeable": limit.is_changeable,
                }
                self._lpc_supported = True
            except grpc.aio.AioRpcError as err:
                data["consumption_limit"] = None
                if _is_unimplemented(err):
                    self._lpc_supported = False
                    _LOGGER.debug("LPC not supported by device %s: %s", self.ski, _rpc_error_text(err))

            try:
                failsafe = await lpc_stub.GetFailsafeLimit(request, timeout=RPC_TIMEOUT)
                data["failsafe_limit"] = {"value_watts": failsafe.value_watts}
                self._failsafe_supported = True
            except grpc.aio.AioRpcError as err:
                data["failsafe_limit"] = None
                if _is_unimplemented(err):
                    self._failsafe_supported = False
                    _LOGGER.debug("Failsafe not supported by device %s: %s", self.ski, _rpc_error_text(err))

            try:
                hb = await lpc_stub.GetHeartbeatStatus(request, timeout=RPC_TIMEOUT)
                data["heartbeat_status"] = {
                    "running": hb.running,
                    "within_duration": hb.within_duration,
                }
            except grpc.aio.AioRpcError:
                data["heartbeat_status"] = None

            if self._was_unavailable:
                _LOGGER.info("EEBUS bridge connection restored at %s:%s", self.host, self.port)
                self._was_unavailable = False

            return data
        except grpc.aio.AioRpcError as err:
            if self._channel is not None:
                await self._channel.close()
                self._channel = None

            if not self._was_unavailable:
                _LOGGER.warning(
                    "EEBUS bridge unavailable at %s:%s: %s", self.host, self.port, err
                )
                self._was_unavailable = True

            raise UpdateFailed(f"gRPC error: {err}") from err

    @staticmethod
    def _extract_scoped_energy_kwh(measurements: list[Any]) -> dict[str, float | None]:
        """Extract scoped energy counters for heating and domestic hot water from MeasurementEntry list."""
        result: dict[str, float | None] = {"heating": None, "dhw": None}
        for m in measurements:
            mtype = str(getattr(m, "type", "")).lower().strip().replace("-", "_").replace(" ", "_")
            if not mtype:
                continue
            value = getattr(m, "value", None)
            if value is None:
                continue
            if "energy" in mtype and ("domestic_hot_water" in mtype or "hot_water" in mtype or "dhw" in mtype):
                result["dhw"] = value
            elif "energy" in mtype and ("heating" in mtype or "space_heating" in mtype):
                result["heating"] = value
        return result

    async def async_write_lpc_limit(self, value_watts: float) -> None:
        """Write LPC consumption limit via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.LPCServiceStub(channel)
        await stub.WriteConsumptionLimit(
            proto_stubs.WriteLoadLimitRequest(
                ski=self.ski, value_watts=value_watts, is_active=True
            )
        )

    async def async_write_failsafe_limit(self, value_watts: float) -> None:
        """Write failsafe limit via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.LPCServiceStub(channel)
        await stub.WriteFailsafeLimit(
            proto_stubs.WriteFailsafeLimitRequest(
                ski=self.ski, value_watts=value_watts
            )
        )

    async def async_set_lpc_active(self, active: bool) -> None:
        """Activate or deactivate LPC limit via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.LPCServiceStub(channel)
        current = await stub.GetConsumptionLimit(
            proto_stubs.DeviceRequest(ski=self.ski)
        )
        await stub.WriteConsumptionLimit(
            proto_stubs.WriteLoadLimitRequest(
                ski=self.ski,
                value_watts=current.value_watts,
                is_active=active,
            )
        )

    async def async_start_heartbeat(self) -> None:
        """Start EEBUS heartbeat via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.LPCServiceStub(channel)
        await stub.StartHeartbeat(proto_stubs.DeviceRequest(ski=self.ski))

    async def async_stop_heartbeat(self) -> None:
        """Stop EEBUS heartbeat via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.LPCServiceStub(channel)
        await stub.StopHeartbeat(proto_stubs.DeviceRequest(ski=self.ski))

    async def async_shutdown(self) -> None:
        """Close gRPC channel and cancel stream tasks."""
        for task in self._stream_tasks:
            task.cancel()
        self._stream_tasks.clear()
        if self._channel is not None:
            await self._channel.close()
            self._channel = None
