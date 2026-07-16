"""Tests for serialized and coalesced provider pushes."""

import asyncio
import logging
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock

import grpc
from grpc.aio import AioRpcError, Metadata

from custom_components.eebus import coordinator as coordinator_module
from custom_components.eebus import proto_stubs
from custom_components.eebus.coordinator import EebusCoordinator, _ProviderPusher


def _fake_hass():
    """Build the task-creation surface used by a provider pusher."""

    def _create_background_task(coro, *, name):
        return asyncio.create_task(coro, name=name)

    return SimpleNamespace(async_create_background_task=_create_background_task)


async def test_provider_pusher_coalesces_burst_and_publishes_latest(monkeypatch):
    """A 100-change burst stays serialized and ends with the newest value."""
    callbacks = []
    unsub = MagicMock()

    def _track_state_changes(_hass, entity_ids, callback):
        assert entity_ids == ["sensor.provider"]
        callbacks.append(callback)
        return unsub

    monkeypatch.setattr(
        coordinator_module, "async_track_state_change_event", _track_state_changes
    )

    current_value = 0
    in_flight = 0
    max_in_flight = 0
    published = []
    first_started = asyncio.Event()
    release_first = asyncio.Event()
    drained = asyncio.Event()

    async def _push():
        nonlocal in_flight, max_in_flight
        in_flight += 1
        max_in_flight = max(max_in_flight, in_flight)
        assert in_flight == 1
        try:
            published.append(current_value)
            if len(published) == 1:
                first_started.set()
                await release_first.wait()
            await asyncio.sleep(0.001)
        finally:
            in_flight -= 1
            if len(published) >= 2:
                drained.set()

    pusher = _ProviderPusher(
        _fake_hass(), "test", "test-ski", ("sensor.provider",), _push
    )
    pusher.start()
    await asyncio.wait_for(first_started.wait(), timeout=1)

    callback = callbacks[0]
    for value in range(1, 101):
        current_value = value
        assert callback(None) is None

    release_first.set()
    await asyncio.wait_for(drained.wait(), timeout=1)

    assert max_in_flight == 1
    assert len(published) < 100
    assert published[-1] == 100

    await pusher.stop()
    unsub.assert_called_once_with()


async def test_provider_pusher_stop_cancels_in_flight_push(monkeypatch):
    """Stopping during a blocked RPC awaits cancellation and leaks no task."""
    unsub = MagicMock()
    monkeypatch.setattr(
        coordinator_module,
        "async_track_state_change_event",
        lambda *_args: unsub,
    )
    push_started = asyncio.Event()
    never_release = asyncio.Event()

    async def _push():
        push_started.set()
        await never_release.wait()

    pusher = _ProviderPusher(
        _fake_hass(), "test", "test-ski", ("sensor.provider",), _push
    )
    pusher.start()
    await asyncio.wait_for(push_started.wait(), timeout=1)
    pusher.signal()
    worker_task = pusher._task
    assert worker_task is not None

    await asyncio.wait_for(pusher.stop(), timeout=1)

    assert worker_task.done()
    assert worker_task.cancelled()
    assert pusher._task is None
    unsub.assert_called_once_with()


async def test_provider_push_failure_warning_is_rate_limited(monkeypatch, caplog):
    """Only a new failure streak warns, and recovery resets that streak."""
    failure = AioRpcError(
        grpc.StatusCode.INTERNAL,
        Metadata(),
        Metadata(),
        details="provider failed",
    )
    outcomes = [failure, failure, failure, None, failure]

    class _FakeStub:
        async def Publish(self, _request, timeout=None):  # noqa: N802
            outcome = outcomes.pop(0)
            if outcome is not None:
                raise outcome

    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator._ensure_channel = AsyncMock(return_value=object())
    coordinator._provider_push_failing = {}
    monkeypatch.setattr(
        proto_stubs,
        "test_provider_stub",
        lambda _channel: _FakeStub(),
        raising=False,
    )
    caplog.set_level(logging.DEBUG, logger=coordinator_module.__name__)

    for _ in range(3):
        await coordinator._async_publish_provider(
            "test", "test_provider_stub", "Publish", object()
        )
    await coordinator._async_publish_provider(
        "test", "test_provider_stub", "Publish", object()
    )
    await coordinator._async_publish_provider(
        "test", "test_provider_stub", "Publish", object()
    )

    failure_records = [
        record
        for record in caplog.records
        if record.getMessage().startswith("Failed to push test data")
    ]
    assert [record.levelno for record in failure_records] == [
        logging.WARNING,
        logging.DEBUG,
        logging.DEBUG,
        logging.WARNING,
    ]
    assert [
        record.getMessage()
        for record in caplog.records
        if record.levelno == logging.INFO
    ] == ["test provider push recovered"]
