"""Typed gRPC write-RPC session for one EEBUS device: stubs, requests, error translation."""

from __future__ import annotations

import logging
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Any

import grpc
import grpc.aio

from . import proto_stubs
from .grpc_client import RPC_TIMEOUT, WRITE_VALIDATION_CODES, is_unimplemented

_LOGGER = logging.getLogger(__name__)


@dataclass(frozen=True)
class WriteOutcome:
    """Result of one write RPC. The caller applies capability state and error handling."""

    status_code: grpc.StatusCode | None
    unimplemented: bool = False
    validation_error: str | None = None
    error: grpc.aio.AioRpcError | None = None


class DeviceSession:
    """Typed reads/writes against one device's SKI, with shared error translation."""

    def __init__(
        self,
        ski: str,
        ensure_channel: Callable[[], Awaitable[grpc.aio.Channel]],
    ) -> None:
        """Initialize with the target SKI and a channel provider."""
        self._ski = ski
        self._ensure_channel = ensure_channel

    async def _write(
        self, label: str, call: Any, request: Any, *, validation: bool = False
    ) -> WriteOutcome:
        """Execute one write RPC and classify the result; never raises for AioRpcError."""
        try:
            await call(request, timeout=RPC_TIMEOUT)
            return WriteOutcome(status_code=None)
        except grpc.aio.AioRpcError as err:
            if is_unimplemented(err):
                _LOGGER.info(
                    "%s unsupported for SKI %s: %s", label, self._ski, err.details()
                )
                return WriteOutcome(
                    status_code=err.code(), unimplemented=True, error=err
                )
            if validation and err.code() in WRITE_VALIDATION_CODES:
                return WriteOutcome(
                    status_code=err.code(),
                    validation_error=f"{label} failed: {err.details()}",
                    error=err,
                )
            return WriteOutcome(status_code=err.code(), error=err)

    async def write_lpc_limit(self, value_watts: float) -> WriteOutcome:
        """Write the LPC consumption limit."""
        channel = await self._ensure_channel()
        stub = proto_stubs.lpc_service_stub(channel)
        return await self._write(
            "LPC write",
            stub.WriteConsumptionLimit,
            proto_stubs.WriteLoadLimitRequest(
                ski=self._ski, value_watts=value_watts, is_active=True
            ),
        )

    async def write_failsafe_limit(self, value_watts: float) -> WriteOutcome:
        """Write the LPC failsafe limit."""
        channel = await self._ensure_channel()
        stub = proto_stubs.lpc_service_stub(channel)
        return await self._write(
            "Failsafe write",
            stub.WriteFailsafeLimit,
            proto_stubs.WriteFailsafeLimitRequest(
                ski=self._ski, value_watts=value_watts
            ),
        )

    async def set_lpc_active(self, active: bool) -> WriteOutcome:
        """Read the current LPC limit, then write it back with a new active flag."""
        channel = await self._ensure_channel()
        stub = proto_stubs.lpc_service_stub(channel)
        current = await stub.GetConsumptionLimit(
            proto_stubs.DeviceRequest(ski=self._ski), timeout=RPC_TIMEOUT
        )
        return await self._write(
            "LPC activation",
            stub.WriteConsumptionLimit,
            proto_stubs.WriteLoadLimitRequest(
                ski=self._ski,
                value_watts=current.value_watts,
                is_active=active,
            ),
        )

    async def control_compressor(
        self, action: proto_stubs.OHPCFAction
    ) -> WriteOutcome:
        """Schedule/pause/resume/abort the compressor's optional consumption."""
        channel = await self._ensure_channel()
        stub = proto_stubs.ohpcf_service_stub(channel)
        return await self._write(
            "OHPCF control",
            stub.ControlCompressorFlexibility,
            proto_stubs.ControlCompressorRequest(ski=self._ski, action=action),
            validation=True,
        )

    async def write_dhw_setpoint(self, value_celsius: float) -> WriteOutcome:
        """Write the domestic-hot-water target."""
        channel = await self._ensure_channel()
        stub = proto_stubs.dhw_service_stub(channel)
        return await self._write(
            "Domestic hot water setpoint",
            stub.SetDHWSetpoint,
            proto_stubs.SetDHWSetpointRequest(
                ski=self._ski, value_celsius=value_celsius
            ),
            validation=True,
        )

    async def set_dhw_boost(self, active: bool) -> WriteOutcome:
        """Activate or cancel the DHW one-time boost."""
        channel = await self._ensure_channel()
        stub = proto_stubs.dhw_service_stub(channel)
        return await self._write(
            "Domestic hot water boost",
            stub.SetDHWBoost,
            proto_stubs.SetDHWBoostRequest(ski=self._ski, active=active),
            validation=True,
        )

    async def set_dhw_operation_mode(self, mode: str) -> WriteOutcome:
        """Set the DHW operation mode by advertised mode type."""
        channel = await self._ensure_channel()
        stub = proto_stubs.dhw_service_stub(channel)
        return await self._write(
            "Domestic hot water operation mode",
            stub.SetDHWOperationMode,
            proto_stubs.SetDHWOperationModeRequest(ski=self._ski, mode=mode),
            validation=True,
        )

    async def set_room_heating_temperature(self, value_celsius: float) -> WriteOutcome:
        """Set the room-heating target temperature."""
        channel = await self._ensure_channel()
        stub = proto_stubs.hvac_service_stub(channel)
        return await self._write(
            "Room heating setpoint",
            stub.SetRoomHeatingTemperature,
            proto_stubs.SetRoomHeatingTemperatureRequest(
                ski=self._ski, value_celsius=value_celsius
            ),
            validation=True,
        )

    async def set_room_heating_mode(self, mode: str) -> WriteOutcome:
        """Set the room-heating operation mode."""
        channel = await self._ensure_channel()
        stub = proto_stubs.hvac_service_stub(channel)
        return await self._write(
            "Room heating mode",
            stub.SetRoomHeatingMode,
            proto_stubs.SetRoomHeatingModeRequest(ski=self._ski, mode=mode),
            validation=True,
        )
