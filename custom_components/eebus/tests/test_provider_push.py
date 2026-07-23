"""Tests for serialized and coalesced provider pushes."""

import asyncio
import logging
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock

import grpc
import pytest
from grpc.aio import AioRpcError, Metadata

from custom_components.eebus import proto_stubs
from custom_components.eebus import providers as providers_module
from custom_components.eebus.providers import (
    ProviderManager,
    ProviderMappings,
    _ProviderPusher,
)


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

    monkeypatch.setattr(providers_module, "async_track_state_change_event", _track_state_changes)

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
        _fake_hass(), "test", "test-ski", ("sensor.provider",), _push, _push
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
        providers_module,
        "async_track_state_change_event",
        lambda *_args: unsub,
    )
    push_started = asyncio.Event()
    never_release = asyncio.Event()

    async def _push():
        push_started.set()
        await never_release.wait()

    pusher = _ProviderPusher(
        _fake_hass(), "test", "test-ski", ("sensor.provider",), _push, _push
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
        async def Publish(self, _request, timeout=None):
            outcome = outcomes.pop(0)
            if outcome is not None:
                raise outcome

    manager = ProviderManager(
        _fake_hass(), "test-ski", AsyncMock(return_value=object()), ProviderMappings()
    )
    stub = _FakeStub()

    async def publish(_channel):
        await stub.Publish(object())
    caplog.set_level(logging.DEBUG, logger=providers_module.__name__)

    for _ in range(3):
        await manager._async_publish_provider("test", publish)
    await manager._async_publish_provider("test", publish)
    await manager._async_publish_provider("test", publish)

    failure_records = [
        record for record in caplog.records if record.getMessage().startswith("Failed to push test data")
    ]
    assert [record.levelno for record in failure_records] == [
        logging.WARNING,
        logging.DEBUG,
        logging.DEBUG,
        logging.WARNING,
    ]
    assert [record.getMessage() for record in caplog.records if record.levelno == logging.INFO] == [
        "test provider push recovered"
    ]


async def test_provider_manager_stop_invalidates_enabled_providers(monkeypatch):
    """Stopping sends best-effort invalidations for all enabled provider feeds."""
    requests = []

    class _GridStub:
        def __init__(self, _channel):
            pass

        async def PublishGridData(self, request, timeout=None):
            requests.append(("grid", request))
            return proto_stubs.Empty()

    class _VisualizationStub:
        def __init__(self, _channel):
            pass

        async def PublishPVData(self, request, timeout=None):
            requests.append(("pv", request))
            return proto_stubs.Empty()

        async def PublishBatteryData(self, request, timeout=None):
            requests.append(("battery", request))
            return proto_stubs.Empty()

    class _DeviceStub:
        def __init__(self, _channel):
            raise AssertionError("provider invalidation must not probe GetDeviceCapabilities")

    monkeypatch.setattr(proto_stubs, "GridServiceStub", _GridStub)
    monkeypatch.setattr(proto_stubs, "VisualizationServiceStub", _VisualizationStub)
    monkeypatch.setattr(proto_stubs, "DeviceServiceStub", _DeviceStub)
    manager = ProviderManager(
        _fake_hass(),
        "test-ski",
        AsyncMock(return_value=object()),
        ProviderMappings(
            grid_power="sensor.grid_power",
            pv_power="sensor.pv_power",
            battery_power="sensor.battery_power",
        ),
        supports_feature=lambda feature: feature
        == proto_stubs.FeatureId.FEATURE_PROVIDER_SAMPLE_INVALIDATION,
    )

    await manager.async_stop()

    assert [label for label, _request in requests] == ["grid", "pv", "battery"]
    for _label, request in requests:
        assert request.HasField("sample") is True
        assert request.sample.invalid is True


async def test_provider_manager_stop_finishes_every_cleanup_when_caller_is_cancelled():
    """Cancellation is delayed until every worker and invalidation has finished."""
    manager = ProviderManager(
        _fake_hass(),
        "test-ski",
        AsyncMock(return_value=object()),
        ProviderMappings(grid_power="sensor.grid", pv_power="sensor.pv"),
    )
    first_started = asyncio.Event()
    release_first = asyncio.Event()

    async def slow_stop():
        first_started.set()
        await release_first.wait()

    first = MagicMock(stop=AsyncMock(side_effect=slow_stop))
    second = MagicMock(stop=AsyncMock())
    manager._provider_pushers.extend((first, second))
    manager._async_invalidate_grid_data = AsyncMock()
    manager._async_invalidate_pv_data = AsyncMock()

    stop = asyncio.create_task(manager.async_stop())
    await first_started.wait()
    stop.cancel()
    await asyncio.sleep(0)

    assert stop.done() is False
    second.stop.assert_awaited_once_with()
    release_first.set()
    with pytest.raises(asyncio.CancelledError):
        await stop

    first.stop.assert_awaited_once_with()
    manager._async_invalidate_grid_data.assert_awaited_once_with()
    manager._async_invalidate_pv_data.assert_awaited_once_with()
    assert manager._provider_pushers == []


async def test_provider_manager_skips_invalidations_for_old_bridge(monkeypatch):
    requests = []
    error = AioRpcError(grpc.StatusCode.UNIMPLEMENTED, Metadata(), Metadata(), details="old bridge")

    class _DeviceStub:
        def __init__(self, _channel):
            pass

        async def GetDeviceCapabilities(self, request, timeout=None):
            raise error

    class _GridStub:
        def __init__(self, _channel):
            pass

        async def PublishGridData(self, request, timeout=None):
            requests.append(request)
            return proto_stubs.Empty()

    monkeypatch.setattr(proto_stubs, "DeviceServiceStub", _DeviceStub)
    monkeypatch.setattr(proto_stubs, "GridServiceStub", _GridStub)
    manager = ProviderManager(
        _fake_hass(),
        "test-ski",
        AsyncMock(return_value=object()),
        ProviderMappings(grid_power="sensor.grid_power"),
    )

    await manager.async_stop()

    assert requests == []


@pytest.mark.parametrize(
    ("invalidate_method", "publish_method", "latch"),
    [
        ("_async_invalidate_grid_data", "_async_publish_grid", "grid"),
        ("_async_invalidate_pv_data", "_async_publish_pv", "PV"),
        ("_async_invalidate_battery_data", "_async_publish_battery", "battery"),
    ],
)
async def test_failed_provider_invalidation_is_retried_until_committed(
    invalidate_method, publish_method, latch
):
    manager = ProviderManager(
        _fake_hass(),
        "test-ski",
        AsyncMock(return_value=object()),
        ProviderMappings(),
        supports_feature=lambda feature: feature
        == proto_stubs.FeatureId.FEATURE_PROVIDER_SAMPLE_INVALIDATION,
    )
    publish = AsyncMock(side_effect=[False, True])
    setattr(manager, publish_method, publish)
    invalidate = getattr(manager, invalidate_method)

    await invalidate()
    assert latch not in manager._provider_invalidated
    await invalidate()
    assert latch in manager._provider_invalidated
    await invalidate()

    assert publish.await_count == 2


async def test_provider_invalidation_tracks_each_renegotiated_contract():
    """Bridge upgrades and downgrades take effect without recreating the manager."""
    supported = False
    manager = ProviderManager(
        _fake_hass(),
        "test-ski",
        AsyncMock(return_value=object()),
        ProviderMappings(),
        supports_feature=lambda _feature: supported,
    )
    publish = AsyncMock(return_value=True)
    manager._async_publish_grid = publish

    await manager._async_invalidate_grid_data()
    publish.assert_not_awaited()

    supported = True
    await manager._async_invalidate_grid_data()
    publish.assert_awaited_once()
    manager._provider_invalidated.clear()

    supported = False
    await manager._async_invalidate_grid_data()
    assert publish.await_count == 1
