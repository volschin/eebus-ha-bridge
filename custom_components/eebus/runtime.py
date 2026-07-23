"""Shared bridge transport runtime and per-device session ownership."""

from __future__ import annotations

import asyncio
import hashlib
import ipaddress
from collections.abc import Callable, Coroutine
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Any

import grpc
import grpc.aio
from homeassistant.core import HomeAssistant

from . import proto_stubs
from .device_session import DeviceSession
from .device_streams import DeviceStreams
from .grpc_client import RPC_TIMEOUT, GrpcChannelManager
from .server_info import BridgeContract, async_read_bridge_contract
from .session_diagnostics import (
    DeviceStreamDiagnostics,
    OperationalDiagnostics,
    ProviderSampleProjection,
    RecoveryProjection,
)
from .ski import normalize_ski
from .snapshot import DevicePoller
from .state import DeviceState, DeviceStateStore


def _proto_timestamp(value: Any) -> datetime | None:
    if value is None or (value.seconds == 0 and value.nanos == 0):
        return None
    result = value.ToDatetime(tzinfo=UTC)
    return result if isinstance(result, datetime) else None


def _recovery_from_proto(
    value: proto_stubs.RecoveryDiagnostics | None,
) -> RecoveryProjection:
    if value is None:
        return RecoveryProjection("DEVICE_READINESS_UNKNOWN", 0, None, None, None, None)
    return RecoveryProjection(
        state=proto_stubs.DeviceReadinessState.Name(value.state),
        attempts=int(value.attempts),
        first_stale_at=_proto_timestamp(value.first_stale_at),
        last_attempt_at=_proto_timestamp(value.last_attempt_at),
        next_attempt_at=_proto_timestamp(value.next_attempt_at),
        last_transition_at=_proto_timestamp(value.last_transition_at),
    )


def _operational_diagnostics_from_proto(
    value: proto_stubs.DeviceOperationalDiagnostics,
) -> OperationalDiagnostics:
    events = value.events
    snapshot = value.snapshot_reads
    return OperationalDiagnostics(
        redacted_ski=value.redacted_ski,
        readiness=proto_stubs.DeviceReadinessState.Name(value.readiness),
        recovery=_recovery_from_proto(value.recovery),
        event_revision=int(events.revision),
        dropped_events=int(events.dropped_events),
        resync_count=int(events.resync_count),
        unresolved_events=int(events.unresolved_events),
        connection_age_seconds=(
            int(value.connection_age_seconds)
            if value.HasField("connection_age_seconds")
            else None
        ),
        monitoring_last_success_age_seconds=(
            int(value.monitoring_last_success_age_seconds)
            if value.HasField("monitoring_last_success_age_seconds")
            else None
        ),
        snapshot_duration_milliseconds=int(snapshot.duration_milliseconds),
        snapshot_last_success=_proto_timestamp(snapshot.last_success),
        providers=tuple(
            ProviderSampleProjection(
                provider=provider.provider,
                state=proto_stubs.ProviderSampleState.Name(provider.state),
                observed_at=_proto_timestamp(provider.observed_at),
                valid_until=_proto_timestamp(provider.valid_until),
            )
            for provider in value.providers
        ),
        features=tuple(proto_stubs.FeatureId.Name(feature) for feature in value.features),
    )


async def _await_cleanup_task(task: asyncio.Task[None]) -> None:
    """Let cleanup finish even when the caller is cancelled mid-close."""
    try:
        await asyncio.shield(task)
    except asyncio.CancelledError:
        try:
            await asyncio.shield(task)
        finally:
            raise


def canonical_bridge_host(host: str) -> str:
    """Return a stable endpoint host without changing what is logged elsewhere."""
    value = host.strip()
    if value.startswith("[") and value.endswith("]"):
        value = value[1:-1]
    try:
        return ipaddress.ip_address(value).compressed.lower()
    except ValueError:
        return value.rstrip(".").encode("idna").decode("ascii").lower()


def _credential_hash(value: str | None) -> str:
    return hashlib.sha256((value or "").encode()).hexdigest()


@dataclass(frozen=True, slots=True)
class BridgeRuntimeKey:
    """Non-secret identity for transport sharing."""

    host: str
    port: int
    security_mode: str
    ca_hash: str
    token_hash: str

    @classmethod
    def from_connection(
        cls,
        host: str,
        port: int,
        security_mode: str,
        tls_ca_certificate: str | None,
        auth_token: str | None,
    ) -> BridgeRuntimeKey:
        return cls(
            canonical_bridge_host(host),
            int(port),
            security_mode.strip().lower(),
            _credential_hash(tls_ca_certificate),
            _credential_hash(auth_token),
        )


@dataclass(slots=True)
class BridgeStatus:
    """Bridge-wide reconnect state shared by all device coordinators."""

    unavailable: bool = False


class RuntimeDeviceSession:
    """All stateful resources for one SKI inside a bridge runtime."""

    def __init__(
        self,
        hass: HomeAssistant,
        runtime: BridgeRuntime,
        ski: str,
        publish_state: Callable[[DeviceState], None],
        request_refresh: Callable[[], Coroutine[Any, Any, None]],
    ) -> None:
        self.ski = normalize_ski(ski)
        self._runtime = runtime
        self.store = DeviceStateStore(publish_state)
        self.poller = DevicePoller(
            self.ski,
            runtime.channel_manager.ensure_channel,
            self.store,
            runtime.supports,
        )
        self.writer = DeviceSession(self.ski, runtime.channel_manager.ensure_channel)
        self.streams = DeviceStreams(
            hass,
            runtime.channel_manager,
            self.ski,
            self.store,
            request_refresh,
            runtime.supports,
        )
        self._close_task: asyncio.Task[None] | None = None

    async def close(self) -> None:
        if self._close_task is None:
            self._close_task = asyncio.create_task(self._async_close())
        await _await_cleanup_task(self._close_task)

    async def _async_close(self) -> None:
        await self.streams.stop()

    @property
    def stream_diagnostics(self) -> DeviceStreamDiagnostics:
        return self.streams.diagnostics()

    async def async_operational_diagnostics(
        self,
    ) -> OperationalDiagnostics | None:
        if not self._runtime.supports(
            int(proto_stubs.FeatureId.FEATURE_OPERATIONAL_DIAGNOSTICS)
        ):
            return None
        channel = await self._runtime.channel_manager.ensure_channel()
        try:
            response = await proto_stubs.device_service_stub(
                channel
            ).GetDeviceDiagnostics(
                proto_stubs.DeviceRequest(ski=self.ski), timeout=RPC_TIMEOUT
            )
        except grpc.aio.AioRpcError as err:
            if err.code() == grpc.StatusCode.UNIMPLEMENTED:
                return None
            raise
        return _operational_diagnostics_from_proto(response)


class BridgeRuntime:
    """Own one shared transport and all device sessions for a bridge key."""

    def __init__(
        self,
        key: BridgeRuntimeKey,
        tls_ca_certificate: str | None,
        auth_token: str | None,
    ) -> None:
        self.key = key
        self.channel_manager = GrpcChannelManager(
            key.host,
            key.port,
            key.security_mode,
            tls_ca_certificate,
            auth_token,
        )
        self.status = BridgeStatus()
        self._contract: BridgeContract | None = None
        self._contract_lock = asyncio.Lock()
        self._contract_generation = 0
        self.channel_manager.set_channel_ready_hook(self._negotiate_contract)
        self._sessions: dict[str, RuntimeDeviceSession] = {}
        self._close_task: asyncio.Task[None] | None = None

    async def ensure_contract(self) -> BridgeContract:
        """Return the contract negotiated for the current channel generation."""
        await self.channel_manager.ensure_channel()
        contract = self._contract
        if contract is None:
            raise RuntimeError("bridge contract negotiation did not complete")
        return contract

    async def _negotiate_contract(
        self, channel: grpc.aio.Channel, generation: int
    ) -> None:
        contract = await async_read_bridge_contract(channel)
        changed = False
        async with self._contract_lock:
            if generation >= self._contract_generation:
                changed = self._contract is not None and self._contract != contract
                self._contract = contract
                self._contract_generation = generation
        if changed:
            for session in tuple(self._sessions.values()):
                session.streams.contract_changed()

    @property
    def contract(self) -> BridgeContract | None:
        return self._contract

    def supports(self, feature: int) -> bool:
        contract = self._contract
        return contract is not None and contract.supports(feature)

    def create_device_session(
        self,
        hass: HomeAssistant,
        ski: str,
        publish_state: Callable[[DeviceState], None],
        request_refresh: Callable[[], Coroutine[Any, Any, None]],
    ) -> RuntimeDeviceSession:
        if self._close_task is not None:
            raise RuntimeError("bridge runtime is closing")
        canonical_ski = normalize_ski(ski)
        if canonical_ski in self._sessions:
            raise RuntimeError(f"device session already active for SKI {canonical_ski}")
        session = RuntimeDeviceSession(
            hass,
            self,
            canonical_ski,
            publish_state,
            request_refresh,
        )
        self._sessions[canonical_ski] = session
        return session

    async def release_device_session(self, session: RuntimeDeviceSession) -> None:
        if self._sessions.get(session.ski) is session:
            del self._sessions[session.ski]
        await session.close()

    @property
    def device_session_count(self) -> int:
        return len(self._sessions)

    def mark_available(self) -> bool:
        restored = self.status.unavailable
        self.status.unavailable = False
        return restored

    def mark_unavailable(self) -> bool:
        first_failure = not self.status.unavailable
        self.status.unavailable = True
        return first_failure

    async def close(self) -> None:
        if self._close_task is None:
            self._close_task = asyncio.create_task(self._async_close())
        await _await_cleanup_task(self._close_task)

    async def _async_close(self) -> None:
        sessions = tuple(self._sessions.values())
        self._sessions.clear()
        try:
            await asyncio.gather(*(session.close() for session in sessions))
        finally:
            await self.channel_manager.close()


@dataclass(slots=True)
class _RuntimeReference:
    runtime: BridgeRuntime
    count: int


class BridgeRuntimeRegistry:
    """Reference-count shared runtimes without retaining secrets in keys."""

    def __init__(self) -> None:
        self._runtimes: dict[BridgeRuntimeKey, _RuntimeReference] = {}
        self._lock = asyncio.Lock()

    async def acquire(
        self,
        host: str,
        port: int,
        security_mode: str,
        tls_ca_certificate: str | None,
        auth_token: str | None,
    ) -> BridgeRuntime:
        key = BridgeRuntimeKey.from_connection(
            host,
            port,
            security_mode,
            tls_ca_certificate,
            auth_token,
        )
        async with self._lock:
            reference = self._runtimes.get(key)
            if reference is None:
                reference = _RuntimeReference(
                    BridgeRuntime(key, tls_ca_certificate, auth_token),
                    0,
                )
                self._runtimes[key] = reference
            reference.count += 1
            return reference.runtime

    async def retain(self, runtime: BridgeRuntime) -> None:
        async with self._lock:
            reference = self._runtimes.get(runtime.key)
            if reference is None or reference.runtime is not runtime:
                raise RuntimeError("cannot retain an unregistered bridge runtime")
            reference.count += 1

    async def release(self, runtime: BridgeRuntime) -> None:
        close = False
        async with self._lock:
            reference = self._runtimes.get(runtime.key)
            if reference is None or reference.runtime is not runtime:
                return
            reference.count -= 1
            if reference.count == 0:
                del self._runtimes[runtime.key]
                close = True
        if close:
            await runtime.close()

    async def reference_count(self, runtime: BridgeRuntime) -> int:
        async with self._lock:
            reference = self._runtimes.get(runtime.key)
            if reference is None or reference.runtime is not runtime:
                return 0
            return reference.count
