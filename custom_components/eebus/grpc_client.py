"""gRPC client lifecycle and error helpers for the EEBUS integration."""

from __future__ import annotations

import asyncio

import grpc
import grpc.aio

from .security import create_grpc_channel

RPC_TIMEOUT = 10

# Write-RPC status codes surfaced to the user as a validation error instead of
# a raw AioRpcError traceback (device-side rejections).
WRITE_VALIDATION_CODES = (
    grpc.StatusCode.INVALID_ARGUMENT,
    grpc.StatusCode.FAILED_PRECONDITION,
    grpc.StatusCode.NOT_FOUND,
)


def is_unimplemented(err: grpc.aio.AioRpcError) -> bool:
    """Return True when gRPC reports method/use case is not implemented."""
    return err.code() == grpc.StatusCode.UNIMPLEMENTED


def is_not_found(err: grpc.aio.AioRpcError) -> bool:
    """Return True when gRPC reports missing entity/data for requested SKI."""
    return err.code() == grpc.StatusCode.NOT_FOUND


def rpc_error_text(err: grpc.aio.AioRpcError) -> str:
    """Build compact debug output for gRPC errors."""
    return f"code={err.code().name} details={err.details()}"


class GrpcChannelManager:
    """Serialize creation and invalidation of the bridge gRPC channel."""

    def __init__(
        self,
        host: str,
        port: int,
        security_mode: str,
        tls_ca_certificate: str | None,
        auth_token: str | None,
    ) -> None:
        """Initialize the channel manager with bridge connection settings."""
        self._host = host
        self._port = port
        self._security_mode = security_mode
        self._tls_ca_certificate = tls_ca_certificate
        self._auth_token = auth_token
        self._channel: grpc.aio.Channel | None = None
        self._lock = asyncio.Lock()

    async def ensure_channel(self) -> grpc.aio.Channel:
        """Create the channel once and return it to all concurrent callers."""
        async with self._lock:
            if self._channel is None:
                self._channel = create_grpc_channel(
                    self._host,
                    self._port,
                    self._security_mode,
                    self._tls_ca_certificate,
                    self._auth_token,
                )
            return self._channel

    async def invalidate(self) -> None:
        """Close and discard the current channel, if one exists."""
        async with self._lock:
            if self._channel is not None:
                await self._channel.close(None)
                self._channel = None

    async def close(self) -> None:
        """Close the current channel during integration shutdown."""
        await self.invalidate()
