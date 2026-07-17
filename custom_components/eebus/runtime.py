"""Shared bridge transport runtime and per-device session ownership."""

from __future__ import annotations

import asyncio
import hashlib
import ipaddress
from collections.abc import Callable, Coroutine
from dataclasses import dataclass
from typing import Any

from homeassistant.core import HomeAssistant

from .device_session import DeviceSession
from .device_streams import DeviceStreams
from .grpc_client import GrpcChannelManager
from .ski import normalize_ski
from .snapshot import DevicePoller
from .state import DeviceState, DeviceStateStore


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
        self.store = DeviceStateStore(publish_state)
        self.poller = DevicePoller(self.ski, runtime.channel_manager.ensure_channel, self.store)
        self.writer = DeviceSession(self.ski, runtime.channel_manager.ensure_channel)
        self.streams = DeviceStreams(
            hass,
            runtime.channel_manager,
            self.ski,
            self.store,
            request_refresh,
        )
        self._close_task: asyncio.Task[None] | None = None

    async def close(self) -> None:
        if self._close_task is None:
            self._close_task = asyncio.create_task(self._async_close())
        await _await_cleanup_task(self._close_task)

    async def _async_close(self) -> None:
        await self.streams.stop()


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
        self._sessions: dict[str, RuntimeDeviceSession] = {}
        self._close_task: asyncio.Task[None] | None = None

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
