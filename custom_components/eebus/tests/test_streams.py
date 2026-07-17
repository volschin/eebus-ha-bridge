"""Tests for background gRPC stream lifecycle management."""

import asyncio
from collections.abc import Coroutine
from typing import Any
from unittest.mock import AsyncMock, MagicMock

import grpc
import pytest
from grpc.aio import AioRpcError, Metadata

from custom_components.eebus.streams import StreamManager


def _rpc_error(code: grpc.StatusCode) -> AioRpcError:
    """Build an async gRPC error with the requested status code."""
    return AioRpcError(code, Metadata(), Metadata(), details="stream failed")


def _manager(
    *,
    sleep: AsyncMock | None = None,
) -> tuple[StreamManager, MagicMock, AsyncMock]:
    """Build a stream manager with deterministic lifecycle dependencies."""
    channel_manager = MagicMock()
    channel_manager.ensure_channel = AsyncMock(return_value=MagicMock())
    fake_sleep = sleep or AsyncMock()
    manager = StreamManager(
        MagicMock(),
        channel_manager,
        sleep=fake_sleep,
        jitter=lambda _minimum, _maximum: 0.0,
    )
    return manager, channel_manager, fake_sleep


async def test_stream_abort_backs_off_and_reacquires_channel() -> None:
    """A failed stream backs off, reacquires the channel, and retries."""
    manager, channel_manager, sleep = _manager()
    calls = 0

    async def consume(_channel: grpc.aio.Channel) -> None:
        nonlocal calls
        calls += 1
        if calls == 1:
            raise _rpc_error(grpc.StatusCode.UNAVAILABLE)
        raise asyncio.CancelledError

    with pytest.raises(asyncio.CancelledError):
        await manager._run_stream("device_events", consume)

    assert calls == 2
    assert channel_manager.ensure_channel.await_count == 2
    sleep.assert_awaited_once_with(2.0)


async def test_unimplemented_stream_stops_permanently() -> None:
    """An unsupported bridge stream returns without sleeping or retrying."""
    manager, channel_manager, sleep = _manager()
    consume = AsyncMock(side_effect=_rpc_error(grpc.StatusCode.UNIMPLEMENTED))

    await manager._run_stream("device_events", consume)

    consume.assert_awaited_once()
    channel_manager.ensure_channel.assert_awaited_once_with()
    sleep.assert_not_awaited()


async def test_unimplemented_stream_starts_compatibility_fallback_once() -> None:
    """The consolidated stream may activate legacy consumers without retrying."""
    manager, _channel_manager, sleep = _manager()
    consume = AsyncMock(side_effect=_rpc_error(grpc.StatusCode.UNIMPLEMENTED))
    fallback = MagicMock()

    await manager._run_stream("device_state", consume, fallback)

    fallback.assert_called_once_with("device_state")
    sleep.assert_not_awaited()


async def test_normal_completion_backs_off_before_retry() -> None:
    """A graceful stream end cannot cause a tight restart loop."""
    manager, _channel_manager, sleep = _manager()
    calls = 0

    async def consume(_channel: grpc.aio.Channel) -> None:
        nonlocal calls
        calls += 1
        if calls == 2:
            raise asyncio.CancelledError

    with pytest.raises(asyncio.CancelledError):
        await manager._run_stream("measurements", consume)

    assert calls == 2
    sleep.assert_awaited_once_with(2.0)


async def test_cancellation_during_backoff_propagates() -> None:
    """Cancellation from the backoff await is never swallowed or retried."""
    sleep = AsyncMock(side_effect=asyncio.CancelledError)
    manager, _channel_manager, _sleep = _manager(sleep=sleep)
    consume = AsyncMock()

    with pytest.raises(asyncio.CancelledError):
        await manager._run_stream("lpc_events", consume)

    consume.assert_awaited_once()
    sleep.assert_awaited_once_with(2.0)


async def test_backoff_resets_after_successful_reconnect() -> None:
    """A successful stream completion resets its escalating failure counter."""
    manager, _channel_manager, sleep = _manager()
    outcomes: list[BaseException | None] = [
        _rpc_error(grpc.StatusCode.UNAVAILABLE),
        RuntimeError("consumer failed"),
        None,
        _rpc_error(grpc.StatusCode.UNKNOWN),
        asyncio.CancelledError(),
    ]

    async def consume(_channel: grpc.aio.Channel) -> None:
        outcome = outcomes.pop(0)
        if outcome is not None:
            raise outcome

    with pytest.raises(asyncio.CancelledError):
        await manager._run_stream("dhw_events", consume)

    assert [call.args[0] for call in sleep.await_args_list] == [2.0, 4.0, 2.0, 2.0]


async def test_backoff_is_bounded_and_adds_jitter() -> None:
    """Repeated failures cap the exponential component and retain jitter."""
    channel_manager = MagicMock()
    channel_manager.ensure_channel = AsyncMock(return_value=MagicMock())
    sleep = AsyncMock()
    jitter = MagicMock(return_value=2.5)
    manager = StreamManager(
        MagicMock(),
        channel_manager,
        sleep=sleep,
        jitter=jitter,
    )
    outcomes: list[BaseException] = [
        *[_rpc_error(grpc.StatusCode.UNAVAILABLE) for _ in range(5)],
        asyncio.CancelledError(),
    ]

    async def consume(_channel: grpc.aio.Channel) -> None:
        raise outcomes.pop(0)

    with pytest.raises(asyncio.CancelledError):
        await manager._run_stream("room_heating_events", consume)

    assert [call.args[0] for call in sleep.await_args_list] == [
        4.5,
        6.5,
        10.5,
        18.5,
        34.5,
    ]
    assert jitter.call_count == 5
    jitter.assert_called_with(0, 3.0)


class _TaskCreatingHass:
    """Minimal Home Assistant task-creation seam for lifecycle tests."""

    def __init__(self) -> None:
        self.task_names: list[str] = []

    def async_create_background_task(
        self,
        coro: Coroutine[Any, Any, None],
        *,
        name: str,
    ) -> asyncio.Task[None]:
        """Create and record a real asyncio task."""
        self.task_names.append(name)
        return asyncio.create_task(coro, name=name)


async def test_start_is_idempotent_and_preserves_task_names() -> None:
    """Starting twice creates one task with the established observable name."""
    hass = _TaskCreatingHass()
    channel_manager = MagicMock()
    channel_manager.ensure_channel = AsyncMock(return_value=MagicMock())
    manager = StreamManager(hass, channel_manager)  # type: ignore[arg-type]
    started = asyncio.Event()

    async def consume(_channel: grpc.aio.Channel) -> None:
        started.set()
        await asyncio.Event().wait()

    streams = {"room_heating_events": consume}
    manager.start(streams, "eebus_{name}_test-ski")
    manager.start(streams, "eebus_{name}_test-ski")
    await started.wait()

    assert hass.task_names == ["eebus_room_heating_events_test-ski"]
    await manager.stop()


async def test_stop_awaits_cancelled_tasks() -> None:
    """Stop waits until a cancelled consumer has completely unwound."""
    hass = _TaskCreatingHass()
    channel_manager = MagicMock()
    channel_manager.ensure_channel = AsyncMock(return_value=MagicMock())
    manager = StreamManager(hass, channel_manager)  # type: ignore[arg-type]
    started = asyncio.Event()
    unwound = False

    async def consume(_channel: grpc.aio.Channel) -> None:
        nonlocal unwound
        started.set()
        try:
            await asyncio.Event().wait()
        except asyncio.CancelledError:
            await asyncio.sleep(0)
            unwound = True
            raise

    manager.start({"device_events": consume}, "eebus_{name}_test-ski")
    await started.wait()

    await manager.stop()

    assert unwound is True
    assert manager._tasks == []
