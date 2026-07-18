"""Contract tests for shared bridge runtimes and entry-scoped sessions."""

import asyncio
from datetime import UTC, datetime
from unittest.mock import AsyncMock, MagicMock, patch

import grpc
import pytest
from grpc.aio import AioRpcError, Metadata

from custom_components.eebus import _async_reload_entry, async_unload_entry, proto_stubs
from custom_components.eebus.coordinator import EebusCoordinator
from custom_components.eebus.providers import ProviderMappings
from custom_components.eebus.runtime import (
    BridgeRuntime,
    BridgeRuntimeKey,
    BridgeRuntimeRegistry,
    canonical_bridge_host,
)
from custom_components.eebus.server_info import BridgeContract
from custom_components.eebus.state import DeviceState, MeasurementsState
from google.protobuf.timestamp_pb2 import Timestamp


@pytest.fixture(autouse=True)
def _runtime_contract_is_already_negotiated(monkeypatch) -> None:
    async def ensure_contract(runtime: BridgeRuntime) -> BridgeContract:
        if runtime.contract is None:
            runtime._contract = BridgeContract(1, 0, "test", frozenset(), "LOCAL")
        assert runtime.contract is not None
        return runtime.contract

    monkeypatch.setattr(BridgeRuntime, "ensure_contract", ensure_contract)


def test_runtime_key_is_canonical_and_contains_only_credential_hashes() -> None:
    ca = "-----BEGIN PRIVATE TEST CA-----"
    token = "top-secret-token"
    key = BridgeRuntimeKey.from_connection(
        " BRIDGE.Example. ",
        50051,
        "TLS_TOKEN",
        ca,
        token,
    )

    assert key.host == "bridge.example"
    assert key.security_mode == "tls_token"
    assert len(key.ca_hash) == len(key.token_hash) == 64
    assert ca not in repr(key)
    assert token not in repr(key)
    assert canonical_bridge_host("[2001:0db8::1]") == "2001:db8::1"


async def test_same_bridge_entries_share_one_channel_until_last_release() -> None:
    registry = BridgeRuntimeRegistry()
    channel = MagicMock()
    channel.close = AsyncMock()

    with patch(
        "custom_components.eebus.grpc_client.create_grpc_channel",
        return_value=channel,
    ) as create_channel, patch(
        "custom_components.eebus.runtime.async_read_bridge_contract",
        new=AsyncMock(return_value=BridgeContract(1, 0, "test", frozenset(), "LOCAL")),
    ):
        first = await registry.acquire("Bridge.Local.", 50051, "loopback", None, None)
        second = await registry.acquire("bridge.local", 50051, "loopback", None, None)
        assert first is second
        assert await first.channel_manager.ensure_channel() is channel
        assert await second.channel_manager.ensure_channel() is channel
        create_channel.assert_called_once()

        await registry.release(first)
        channel.close.assert_not_awaited()
        assert await registry.reference_count(second) == 1

        await registry.release(second)
        channel.close.assert_awaited_once_with(None)


async def test_different_credentials_never_share_runtime() -> None:
    registry = BridgeRuntimeRegistry()
    first = await registry.acquire("bridge", 50051, "tls_token", "ca", "token-a")
    second = await registry.acquire("bridge", 50051, "tls_token", "ca", "token-b")

    assert first is not second
    assert first.key != second.key

    await registry.release(first)
    await registry.release(second)


async def test_runtime_owns_separate_device_sessions_and_shared_status() -> None:
    registry = BridgeRuntimeRegistry()
    runtime = await registry.acquire("bridge", 50051, "loopback", None, None)
    hass = MagicMock()
    first = runtime.create_device_session(
        hass,
        "AA:11",
        MagicMock(),
        AsyncMock(),
    )
    second = runtime.create_device_session(
        hass,
        "BB:22",
        MagicMock(),
        AsyncMock(),
    )

    assert first.store is not second.store
    assert first.poller is not second.poller
    assert first.writer is not second.writer
    assert runtime.device_session_count == 2
    assert runtime.mark_unavailable() is True
    assert runtime.mark_unavailable() is False
    assert runtime.mark_available() is True

    await runtime.release_device_session(first)
    await runtime.release_device_session(second)
    await registry.release(runtime)


async def test_device_session_projects_operational_diagnostics_immutably() -> None:
    feature = int(proto_stubs.FeatureId.FEATURE_OPERATIONAL_DIAGNOSTICS)
    runtime = BridgeRuntime(
        BridgeRuntimeKey.from_connection("bridge", 50051, "loopback", None, None),
        None,
        None,
    )
    runtime._contract = BridgeContract(1, 0, "test", frozenset({feature}), "LOCAL")
    runtime.channel_manager.ensure_channel = AsyncMock(return_value=MagicMock())
    observed_at = Timestamp()
    observed_at.FromDatetime(datetime(2026, 7, 18, 12, 0, tzinfo=UTC))
    response = proto_stubs.DeviceOperationalDiagnostics(
        redacted_ski="…AABBCC",
        readiness=proto_stubs.DeviceReadinessState.DEVICE_READINESS_RECOVERING,
        recovery=proto_stubs.RecoveryDiagnostics(
            state=proto_stubs.DeviceReadinessState.DEVICE_READINESS_RECOVERING,
            attempts=2,
        ),
        events=proto_stubs.EventTransportDiagnostics(
            revision=42,
            dropped_events=3,
            resync_count=1,
            unresolved_events=2,
        ),
        connection_age_seconds=31,
        monitoring_last_success_age_seconds=17,
        snapshot_reads=proto_stubs.SnapshotReadDiagnostics(
            duration_milliseconds=23,
            last_success=observed_at,
        ),
        providers=[
            proto_stubs.ProviderSampleDiagnostics(
                provider="grid",
                state=proto_stubs.ProviderSampleState.PROVIDER_SAMPLE_STATE_CURRENT,
                observed_at=observed_at,
            )
        ],
        features=[proto_stubs.FeatureId.FEATURE_OPERATIONAL_DIAGNOSTICS],
    )
    stub = MagicMock(GetDeviceDiagnostics=AsyncMock(return_value=response))
    session = runtime.create_device_session(MagicMock(), "AA11", MagicMock(), AsyncMock())

    with patch("custom_components.eebus.proto_stubs.device_service_stub", return_value=stub):
        diagnostics = await session.async_operational_diagnostics()

    assert diagnostics is not None
    assert diagnostics.redacted_ski == "…AABBCC"
    assert diagnostics.readiness == "DEVICE_READINESS_RECOVERING"
    assert diagnostics.recovery.attempts == 2
    assert diagnostics.event_revision == 42
    assert diagnostics.dropped_events == 3
    assert diagnostics.resync_count == 1
    assert diagnostics.connection_age_seconds == 31
    assert diagnostics.monitoring_last_success_age_seconds == 17
    assert diagnostics.snapshot_duration_milliseconds == 23
    assert diagnostics.snapshot_last_success == datetime(2026, 7, 18, 12, 0, tzinfo=UTC)
    assert diagnostics.providers[0].provider == "grid"
    assert diagnostics.features == ("FEATURE_OPERATIONAL_DIAGNOSTICS",)
    stub.GetDeviceDiagnostics.assert_awaited_once()

    await runtime.release_device_session(session)


async def test_session_diagnostics_remain_available_when_operational_rpc_fails() -> None:
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator._last_successful_read_at = None
    coordinator.last_update_success = False
    coordinator._runtime = MagicMock()
    coordinator._runtime.status.unavailable = True
    streams = MagicMock()
    coordinator._runtime_session = MagicMock(stream_diagnostics=streams)
    coordinator._runtime_session.async_operational_diagnostics = AsyncMock(
        side_effect=AioRpcError(
            grpc.StatusCode.UNAVAILABLE,
            Metadata(),
            Metadata(),
            details="bridge unavailable",
        )
    )

    diagnostics = await coordinator.async_session_diagnostics()

    assert diagnostics.bridge_unavailable is True
    assert diagnostics.last_update_success is False
    assert diagnostics.streams is streams
    assert diagnostics.operational is None


async def test_device_session_close_finishes_stream_stop_when_cancelled() -> None:
    registry = BridgeRuntimeRegistry()
    runtime = await registry.acquire("bridge", 50051, "loopback", None, None)
    session = runtime.create_device_session(
        MagicMock(),
        "AA11",
        MagicMock(),
        AsyncMock(),
    )
    stop_started = asyncio.Event()
    stop_finished = asyncio.Event()

    async def stop() -> None:
        stop_started.set()
        await stop_finished.wait()

    session.streams.stop = AsyncMock(side_effect=stop)

    close_task = asyncio.create_task(session.close())
    await stop_started.wait()
    close_task.cancel()
    await asyncio.sleep(0)

    assert not close_task.done()

    stop_finished.set()
    with pytest.raises(asyncio.CancelledError):
        await close_task

    session.streams.stop.assert_awaited_once_with()
    await session.close()
    await registry.release(runtime)


async def test_registry_release_finishes_last_runtime_close_when_cancelled() -> None:
    registry = BridgeRuntimeRegistry()
    runtime = await registry.acquire("bridge", 50051, "loopback", None, None)
    session = runtime.create_device_session(
        MagicMock(),
        "AA11",
        MagicMock(),
        AsyncMock(),
    )
    runtime.channel_manager.close = AsyncMock()
    stop_started = asyncio.Event()
    stop_finished = asyncio.Event()

    async def stop() -> None:
        stop_started.set()
        await stop_finished.wait()

    session.streams.stop = AsyncMock(side_effect=stop)

    release_task = asyncio.create_task(registry.release(runtime))
    await stop_started.wait()
    release_task.cancel()
    await asyncio.sleep(0)

    assert not release_task.done()

    stop_finished.set()
    with pytest.raises(asyncio.CancelledError):
        await release_task

    assert await registry.reference_count(runtime) == 0
    session.streams.stop.assert_awaited_once_with()
    runtime.channel_manager.close.assert_awaited_once_with()
    await registry.release(runtime)


def test_provider_managers_remain_entry_scoped_on_shared_runtime() -> None:
    runtime = MagicMock()
    runtime.channel_manager = MagicMock()
    sessions = [MagicMock(), MagicMock()]
    for session in sessions:
        session.store = MagicMock()
        session.poller = MagicMock()
        session.writer = MagicMock()
        session.streams = MagicMock()
    runtime.create_device_session.side_effect = sessions
    first_provider = MagicMock()
    second_provider = MagicMock()

    with (
        patch(
            "custom_components.eebus.coordinator.DataUpdateCoordinator.__init__",
            return_value=None,
        ),
        patch(
            "custom_components.eebus.coordinator.ProviderManager",
            side_effect=[first_provider, second_provider],
        ),
    ):
        first = EebusCoordinator(
            MagicMock(), "bridge", 50051, "AA11", runtime=runtime
        )
        second = EebusCoordinator(
            MagicMock(), "bridge", 50051, "BB22", runtime=runtime
        )

    assert first.runtime is second.runtime is runtime
    assert first._provider_manager is first_provider
    assert second._provider_manager is second_provider
    assert first._provider_manager is not second._provider_manager


async def test_reload_keeps_old_runtime_until_replacement_handover() -> None:
    hass = MagicMock()
    hass.data = {}
    registry = BridgeRuntimeRegistry()
    hass.data["eebus"] = {"runtime_registry": registry}
    old = await registry.acquire("old.bridge", 50051, "loopback", None, None)
    old.channel_manager.close = AsyncMock()
    old_coordinator = MagicMock(runtime=old, reconfigure_lock=asyncio.Lock())
    replacement_seen = None

    async def reconfigure(replacement, **_kwargs) -> None:
        nonlocal replacement_seen
        old.channel_manager.close.assert_not_awaited()
        replacement_seen = replacement
        old_coordinator.runtime = replacement

    old_coordinator.async_reconfigure_runtime = AsyncMock(side_effect=reconfigure)
    entry = MagicMock()
    entry.entry_id = "entry-1"
    entry.data = {
        "grpc_host": "new.bridge",
        "grpc_port": 50051,
        "security_mode": "tls_token",
        "tls_ca_certificate": "new-ca",
        "auth_token": "new-token",
    }
    entry.runtime_data = old_coordinator
    await _async_reload_entry(hass, entry)

    assert replacement_seen is not None
    assert replacement_seen is not old
    old_coordinator.async_reconfigure_runtime.assert_awaited_once()
    old.channel_manager.close.assert_awaited_once_with()
    assert await registry.reference_count(replacement_seen) == 1
    await registry.release(replacement_seen)


async def test_failed_reload_preserves_old_runtime_reference() -> None:
    hass = MagicMock()
    hass.data = {}
    registry = BridgeRuntimeRegistry()
    hass.data["eebus"] = {"runtime_registry": registry}
    old = await registry.acquire("old.bridge", 50051, "loopback", None, None)
    old.channel_manager.close = AsyncMock()
    old_coordinator = MagicMock(runtime=old, reconfigure_lock=asyncio.Lock())
    old_coordinator.async_reconfigure_runtime = AsyncMock(
        side_effect=RuntimeError("replacement first refresh failed")
    )
    entry = MagicMock(
        entry_id="entry-1",
        data={
            "grpc_host": "new.bridge",
            "grpc_port": 50051,
            "security_mode": "loopback",
        },
    )
    entry.runtime_data = old_coordinator
    with pytest.raises(RuntimeError, match="replacement first refresh failed"):
        await _async_reload_entry(hass, entry)

    old.channel_manager.close.assert_not_awaited()
    assert entry.runtime_data is old_coordinator
    assert await registry.reference_count(old) == 1
    hass.config_entries.async_reload.assert_not_called()
    await registry.release(old)


async def test_cancelled_reload_before_commit_releases_only_replacement() -> None:
    hass = MagicMock()
    hass.data = {}
    registry = BridgeRuntimeRegistry()
    hass.data["eebus"] = {"runtime_registry": registry}
    old = await registry.acquire("old.bridge", 50051, "loopback", None, None)
    coordinator = MagicMock(runtime=old, reconfigure_lock=asyncio.Lock())
    coordinator.async_reconfigure_runtime = AsyncMock(
        side_effect=asyncio.CancelledError
    )
    entry = MagicMock(
        data={"grpc_host": "new.bridge", "grpc_port": 50051},
        options={},
        runtime_data=coordinator,
    )

    with pytest.raises(asyncio.CancelledError):
        await _async_reload_entry(hass, entry)

    assert await registry.reference_count(old) == 1
    replacement = await registry.acquire("new.bridge", 50051, "loopback", None, None)
    assert await registry.reference_count(replacement) == 1
    await registry.release(replacement)
    await registry.release(old)


async def test_cancelled_reload_after_commit_releases_only_previous() -> None:
    hass = MagicMock()
    hass.data = {}
    registry = BridgeRuntimeRegistry()
    hass.data["eebus"] = {"runtime_registry": registry}
    old = await registry.acquire("old.bridge", 50051, "loopback", None, None)
    coordinator = MagicMock(runtime=old, reconfigure_lock=asyncio.Lock())

    async def commit_then_cancel(replacement, **_kwargs) -> None:
        coordinator.runtime = replacement
        raise asyncio.CancelledError

    coordinator.async_reconfigure_runtime = AsyncMock(side_effect=commit_then_cancel)
    entry = MagicMock(
        data={"grpc_host": "new.bridge", "grpc_port": 50051},
        options={},
        runtime_data=coordinator,
    )

    with pytest.raises(asyncio.CancelledError):
        await _async_reload_entry(hass, entry)

    assert await registry.reference_count(old) == 0
    assert await registry.reference_count(coordinator.runtime) == 1
    await registry.release(coordinator.runtime)


async def test_same_runtime_options_failure_keeps_session_and_provider() -> None:
    hass = MagicMock()
    hass.data = {}
    registry = BridgeRuntimeRegistry()
    hass.data["eebus"] = {"runtime_registry": registry}
    runtime = await registry.acquire("bridge", 50051, "loopback", None, None)
    coordinator = MagicMock(runtime=runtime, reconfigure_lock=asyncio.Lock())
    coordinator.async_reconfigure_providers = AsyncMock(
        side_effect=RuntimeError("replacement provider failed")
    )
    entry = MagicMock(
        entry_id="entry-1",
        data={"grpc_host": "bridge", "grpc_port": 50051},
        options={},
        runtime_data=coordinator,
    )

    with pytest.raises(RuntimeError, match="replacement provider failed"):
        await _async_reload_entry(hass, entry)

    assert await registry.reference_count(runtime) == 1
    hass.config_entries.async_reload.assert_not_called()
    await registry.release(runtime)


async def test_concurrent_runtime_reloads_release_superseded_runtime() -> None:
    hass = MagicMock()
    hass.data = {}
    registry = BridgeRuntimeRegistry()
    hass.data["eebus"] = {"runtime_registry": registry}
    old = await registry.acquire("old.bridge", 50051, "loopback", None, None)
    coordinator = MagicMock(runtime=old, reconfigure_lock=asyncio.Lock())
    replacements = []
    first_started = asyncio.Event()
    finish_first = asyncio.Event()

    async def reconfigure(replacement, **_kwargs) -> None:
        replacements.append(replacement)
        if len(replacements) == 1:
            first_started.set()
            await finish_first.wait()
        coordinator.runtime = replacement

    coordinator.async_reconfigure_runtime = AsyncMock(side_effect=reconfigure)
    first_entry = MagicMock(
        data={"grpc_host": "new-a.bridge", "grpc_port": 50051},
        options={},
        runtime_data=coordinator,
    )
    second_entry = MagicMock(
        data={"grpc_host": "new-b.bridge", "grpc_port": 50051},
        options={},
        runtime_data=coordinator,
    )

    first_task = asyncio.create_task(_async_reload_entry(hass, first_entry))
    await first_started.wait()
    second_task = asyncio.create_task(_async_reload_entry(hass, second_entry))
    await asyncio.sleep(0)

    assert coordinator.async_reconfigure_runtime.await_count == 1

    finish_first.set()
    await asyncio.gather(first_task, second_task)

    assert len(replacements) == 2
    assert await registry.reference_count(old) == 0
    assert await registry.reference_count(replacements[0]) == 0
    assert await registry.reference_count(replacements[1]) == 1
    assert coordinator.runtime is replacements[1]
    await registry.release(replacements[1])


async def test_unload_waits_for_runtime_reload_handover() -> None:
    hass = MagicMock()
    hass.data = {}
    hass.config_entries.async_unload_platforms = AsyncMock(return_value=True)
    registry = BridgeRuntimeRegistry()
    hass.data["eebus"] = {"runtime_registry": registry}
    old = await registry.acquire("old.bridge", 50051, "loopback", None, None)
    coordinator = MagicMock(runtime=old, reconfigure_lock=asyncio.Lock())
    coordinator.async_shutdown = AsyncMock()
    replacements = []
    reload_started = asyncio.Event()
    finish_reload = asyncio.Event()

    async def reconfigure(replacement, **_kwargs) -> None:
        replacements.append(replacement)
        reload_started.set()
        await finish_reload.wait()
        coordinator.runtime = replacement

    coordinator.async_reconfigure_runtime = AsyncMock(side_effect=reconfigure)
    entry = MagicMock(
        data={"grpc_host": "new.bridge", "grpc_port": 50051},
        options={},
        runtime_data=coordinator,
    )

    reload_task = asyncio.create_task(_async_reload_entry(hass, entry))
    await reload_started.wait()
    unload_task = asyncio.create_task(async_unload_entry(hass, entry))
    await asyncio.sleep(0)

    hass.config_entries.async_unload_platforms.assert_not_awaited()

    finish_reload.set()
    await reload_task
    assert await unload_task is True

    assert len(replacements) == 1
    assert await registry.reference_count(old) == 0
    assert await registry.reference_count(replacements[0]) == 0
    coordinator.async_shutdown.assert_awaited_once_with()


async def test_reload_queued_after_unload_does_not_recreate_runtime() -> None:
    hass = MagicMock()
    hass.data = {}
    registry = BridgeRuntimeRegistry()
    hass.data["eebus"] = {"runtime_registry": registry}
    old = await registry.acquire("old.bridge", 50051, "loopback", None, None)
    coordinator = MagicMock(
        runtime=old,
        reconfigure_lock=asyncio.Lock(),
        entry_unloaded=False,
    )
    coordinator.async_shutdown = AsyncMock()
    coordinator.async_reconfigure_runtime = AsyncMock()

    def mark_unloaded() -> None:
        coordinator.entry_unloaded = True

    coordinator.mark_entry_unloaded = MagicMock(side_effect=mark_unloaded)
    unload_started = asyncio.Event()
    finish_unload = asyncio.Event()

    async def unload_platforms(_entry, _platforms) -> bool:
        unload_started.set()
        await finish_unload.wait()
        return True

    hass.config_entries.async_unload_platforms = AsyncMock(
        side_effect=unload_platforms
    )
    entry = MagicMock(
        data={"grpc_host": "new.bridge", "grpc_port": 50051},
        options={},
        runtime_data=coordinator,
    )

    unload_task = asyncio.create_task(async_unload_entry(hass, entry))
    await unload_started.wait()
    reload_task = asyncio.create_task(_async_reload_entry(hass, entry))
    await asyncio.sleep(0)

    coordinator.async_reconfigure_runtime.assert_not_awaited()

    finish_unload.set()
    assert await unload_task is True
    await reload_task

    coordinator.mark_entry_unloaded.assert_called_once_with()
    coordinator.async_shutdown.assert_awaited_once_with()
    coordinator.async_reconfigure_runtime.assert_not_awaited()
    assert await registry.reference_count(old) == 0
    replacement = await registry.acquire("new.bridge", 50051, "loopback", None, None)
    assert await registry.reference_count(replacement) == 1
    await registry.release(replacement)


async def test_provider_reconfigure_failure_keeps_previous_manager() -> None:
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator._hass_instance = MagicMock()
    previous = MagicMock()
    previous.async_stop = AsyncMock()
    replacement = MagicMock()
    replacement.async_stop = AsyncMock()
    replacement.async_start_grid_push.side_effect = RuntimeError("start failed")
    coordinator._provider_manager = previous
    coordinator._new_provider_manager = MagicMock(return_value=replacement)

    with pytest.raises(RuntimeError, match="start failed"):
        await coordinator.async_reconfigure_providers(ProviderMappings())

    assert coordinator._provider_manager is previous
    previous.async_stop.assert_not_awaited()
    replacement.async_stop.assert_awaited_once_with(invalidate=False)


def _coordinator_for_runtime_handover() -> EebusCoordinator:
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator._hass_instance = MagicMock()
    coordinator.ski = "AA11"
    coordinator.host = "old.bridge"
    coordinator.port = 50051
    coordinator.security_mode = "loopback"
    coordinator.tls_ca_certificate = None
    coordinator.auth_token = None
    coordinator._runtime_generation = object()
    coordinator._owns_runtime = False
    coordinator._runtime = MagicMock()
    coordinator._runtime_session = MagicMock()
    coordinator._provider_manager = MagicMock()
    coordinator._provider_manager.async_stop = AsyncMock()
    coordinator._channel_manager = MagicMock()
    coordinator._state_store = MagicMock()
    coordinator._poller = MagicMock()
    coordinator._device_session = MagicMock()
    coordinator._device_streams = MagicMock()
    return coordinator


async def test_runtime_handover_commits_only_after_replacement_is_started() -> None:
    coordinator = _coordinator_for_runtime_handover()
    previous_runtime = coordinator._runtime
    previous_session = coordinator._runtime_session
    replacement_runtime = MagicMock()
    replacement_runtime.release_device_session = AsyncMock()
    replacement_runtime.channel_manager.ensure_channel = AsyncMock()
    replacement_session = MagicMock()
    replacement_state = DeviceState(
        measurements=MeasurementsState(power_watts=1000.0)
    )
    replacement_session.store.state = replacement_state
    staged_callback = None

    def create_session(_hass, _ski, publish_state, _request_refresh):
        nonlocal staged_callback
        staged_callback = publish_state
        return replacement_session

    async def poll():
        assert staged_callback is not None
        staged_callback(replacement_state)

    replacement_session.poller.poll = AsyncMock(side_effect=poll)
    replacement_session.streams.start = MagicMock()
    replacement_runtime.create_device_session.side_effect = create_session
    replacement_provider = MagicMock()
    replacement_provider.async_stop = AsyncMock()
    coordinator._new_provider_manager = MagicMock(return_value=replacement_provider)
    previous_runtime.release_device_session = AsyncMock()
    coordinator._publish_state = MagicMock()

    await coordinator.async_reconfigure_runtime(
        replacement_runtime,
        host="new.bridge",
        port=50052,
        security_mode="tls_token",
        tls_ca_certificate="ca",
        auth_token="token",
        provider_mappings=ProviderMappings(),
    )

    replacement_session.poller.poll.assert_awaited_once_with()
    replacement_session.streams.start.assert_called_once_with()
    replacement_provider.async_start_grid_push.assert_called_once_with()
    coordinator._publish_state.assert_called_once_with(replacement_state)
    assert coordinator.runtime is replacement_runtime
    assert coordinator._runtime_session is replacement_session
    assert coordinator._provider_manager is replacement_provider
    assert coordinator.host == "new.bridge"
    previous_runtime.release_device_session.assert_awaited_once_with(previous_session)


async def test_runtime_handover_staging_failure_does_not_publish_state() -> None:
    coordinator = _coordinator_for_runtime_handover()
    previous_runtime = coordinator._runtime
    previous_session = coordinator._runtime_session
    previous_provider = coordinator._provider_manager
    replacement_runtime = MagicMock()
    replacement_runtime.release_device_session = AsyncMock()
    replacement_session = MagicMock()
    staged_state = DeviceState(measurements=MeasurementsState(power_watts=1000.0))
    staged_callback = None

    def create_session(_hass, _ski, publish_state, _request_refresh):
        nonlocal staged_callback
        staged_callback = publish_state
        return replacement_session

    async def poll():
        assert staged_callback is not None
        staged_callback(staged_state)

    replacement_session.poller.poll = AsyncMock(side_effect=poll)
    replacement_session.streams.start.side_effect = RuntimeError("stream start failed")
    replacement_runtime.create_device_session.side_effect = create_session
    replacement_provider = MagicMock()
    replacement_provider.async_stop = AsyncMock()
    coordinator._new_provider_manager = MagicMock(return_value=replacement_provider)
    coordinator._publish_state = MagicMock()

    with pytest.raises(RuntimeError, match="stream start failed"):
        await coordinator.async_reconfigure_runtime(
            replacement_runtime,
            host="new.bridge",
            port=50052,
            security_mode="tls_token",
            tls_ca_certificate="ca",
            auth_token="token",
            provider_mappings=ProviderMappings(),
        )

    assert coordinator.runtime is previous_runtime
    assert coordinator._runtime_session is previous_session
    assert coordinator._provider_manager is previous_provider
    coordinator._publish_state.assert_not_called()
    previous_provider.async_stop.assert_not_awaited()
    replacement_provider.async_stop.assert_awaited_once_with(invalidate=False)
    replacement_runtime.release_device_session.assert_awaited_once_with(
        replacement_session
    )


async def test_runtime_handover_ignores_late_previous_session_state() -> None:
    coordinator = _coordinator_for_runtime_handover()
    replacement_runtime = MagicMock()
    replacement_runtime.release_device_session = AsyncMock()
    replacement_runtime.channel_manager.ensure_channel = AsyncMock()
    replacement_session = MagicMock()
    replacement_state = DeviceState(
        measurements=MeasurementsState(power_watts=1000.0)
    )
    replacement_session.store.state = replacement_state
    replacement_session.poller.poll = AsyncMock()
    replacement_runtime.create_device_session.return_value = replacement_session
    replacement_provider = MagicMock()
    replacement_provider.async_stop = AsyncMock()
    coordinator._new_provider_manager = MagicMock(return_value=replacement_provider)
    coordinator._runtime.release_device_session = AsyncMock()
    coordinator._publish_state = MagicMock()

    await coordinator.async_reconfigure_runtime(
        replacement_runtime,
        host="new.bridge",
        port=50052,
        security_mode="loopback",
        tls_ca_certificate=None,
        auth_token=None,
        provider_mappings=ProviderMappings(),
    )
    coordinator._publish_state.reset_mock()

    coordinator._publish_session_state(
        0,
        DeviceState(measurements=MeasurementsState(power_watts=2000.0)),
    )

    coordinator._publish_state.assert_not_called()
