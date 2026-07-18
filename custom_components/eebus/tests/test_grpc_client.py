"""Tests for shared gRPC client lifecycle management."""

import asyncio
from unittest.mock import AsyncMock, MagicMock, call, patch

import grpc
import pytest
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


async def test_cancelled_waiter_does_not_cancel_shared_contract_negotiation():
    """One abandoned device cannot break the generation used by its peers."""
    channel = MagicMock()
    channel.close = AsyncMock()
    negotiation_started = asyncio.Event()
    release_negotiation = asyncio.Event()

    async def negotiate(_channel, _generation):
        negotiation_started.set()
        await release_negotiation.wait()

    manager = GrpcChannelManager("localhost", 50051, "loopback", None, None)
    manager.set_channel_ready_hook(negotiate)
    with patch(
        "custom_components.eebus.grpc_client.create_grpc_channel",
        return_value=channel,
    ):
        abandoned = asyncio.create_task(manager.ensure_channel())
        peer = asyncio.create_task(manager.ensure_channel())
        await negotiation_started.wait()

        abandoned.cancel()
        with pytest.raises(asyncio.CancelledError):
            await abandoned
        assert manager._ready_task is not None
        assert manager._ready_task.cancelled() is False
        assert peer.done() is False

        release_negotiation.set()
        assert await peer is channel
        await manager.close()


async def test_invalidation_during_negotiation_moves_all_peers_to_fresh_channel():
    """Owner invalidation is not misclassified as cancellation of peer streams."""
    first = MagicMock()
    first.close = AsyncMock()
    second = MagicMock()
    second.close = AsyncMock()
    first_negotiation_started = asyncio.Event()
    second_negotiation_finished = asyncio.Event()

    async def negotiate(_channel, generation):
        if generation == 1:
            first_negotiation_started.set()
            await asyncio.Event().wait()
        second_negotiation_finished.set()

    manager = GrpcChannelManager("localhost", 50051, "loopback", None, None)
    manager.set_channel_ready_hook(negotiate)
    with patch(
        "custom_components.eebus.grpc_client.create_grpc_channel",
        side_effect=[first, second],
    ):
        peers = [asyncio.create_task(manager.ensure_channel()) for _ in range(2)]
        await first_negotiation_started.wait()

        await manager.invalidate()
        channels = await asyncio.gather(*peers)

        assert channels == [second, second]
        assert second_negotiation_finished.is_set()
        first.close.assert_awaited_once_with(None)
        await manager.close()


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
