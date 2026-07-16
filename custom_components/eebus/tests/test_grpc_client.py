"""Tests for shared gRPC client lifecycle management."""

import asyncio
from unittest.mock import AsyncMock, MagicMock, call, patch

import grpc
from grpc.aio import AioRpcError, Metadata

from custom_components.eebus.grpc_client import (
    GrpcChannelManager,
    is_not_found,
    is_unimplemented,
    rpc_error_text,
)


async def test_concurrent_ensure_channel_creates_one_channel():
    """Six simultaneous stream callers share one channel creation."""
    channel = MagicMock()
    manager = GrpcChannelManager("localhost", 50051, "loopback", None, None)

    with patch(
        "custom_components.eebus.grpc_client.create_grpc_channel",
        return_value=channel,
    ) as create_channel:
        await manager._lock.acquire()
        tasks = [asyncio.create_task(manager.ensure_channel()) for _ in range(6)]
        await asyncio.sleep(0)
        manager._lock.release()
        channels = await asyncio.gather(*tasks)

    create_channel.assert_called_once_with("localhost", 50051, "loopback", None, None)
    assert all(result is channel for result in channels)


async def test_invalidate_racing_ensure_returns_fresh_channel():
    """Acquisition waits for invalidation and never returns the closing channel."""
    close_started = asyncio.Event()
    finish_close = asyncio.Event()

    async def slow_close(_grace):
        close_started.set()
        await finish_close.wait()

    first = MagicMock()
    first.close = AsyncMock(side_effect=slow_close)
    second = MagicMock()
    second.close = AsyncMock()
    manager = GrpcChannelManager("localhost", 50051, "loopback", None, None)

    with patch(
        "custom_components.eebus.grpc_client.create_grpc_channel",
        side_effect=[first, second],
    ) as create_channel:
        assert await manager.ensure_channel() is first
        invalidate_task = asyncio.create_task(manager.invalidate())
        await close_started.wait()
        ensure_task = asyncio.create_task(manager.ensure_channel())
        await asyncio.sleep(0)

        assert ensure_task.done() is False
        finish_close.set()
        await invalidate_task
        assert await ensure_task is second
        await manager.close()
        await manager.close()

    assert create_channel.call_args_list == [
        call("localhost", 50051, "loopback", None, None),
        call("localhost", 50051, "loopback", None, None),
    ]
    first.close.assert_awaited_once_with(None)
    second.close.assert_awaited_once_with(None)


def test_rpc_error_helpers():
    """gRPC status helpers retain the coordinator's previous behavior."""
    unimplemented = AioRpcError(
        grpc.StatusCode.UNIMPLEMENTED,
        Metadata(),
        Metadata(),
        details="method unavailable",
    )
    not_found = AioRpcError(
        grpc.StatusCode.NOT_FOUND,
        Metadata(),
        Metadata(),
        details="device missing",
    )

    assert is_unimplemented(unimplemented) is True
    assert is_unimplemented(not_found) is False
    assert is_not_found(not_found) is True
    assert is_not_found(unimplemented) is False
    assert rpc_error_text(not_found) == "code=NOT_FOUND details=device missing"
