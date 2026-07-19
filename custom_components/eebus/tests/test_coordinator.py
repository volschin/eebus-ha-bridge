"""Tests for the coordinator facade, poller and stream integration."""

import asyncio
from datetime import timedelta
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock, call, patch

import grpc
import pytest
from grpc.aio import AioRpcError, Metadata
from homeassistant.exceptions import ConfigEntryAuthFailed, ConfigEntryNotReady

from custom_components.eebus import proto_stubs
from custom_components.eebus.coordinator import EebusCoordinator, POLL_INTERVAL
from custom_components.eebus.device_streams import DeviceStreams
from custom_components.eebus.generated.eebus.v1 import (
    device_service_pb2,
    device_service_pb2_grpc,
    dhw_service_pb2,
    hvac_service_pb2,
    lpc_service_pb2,
    monitoring_service_pb2,
    ohpcf_service_pb2,
)
from custom_components.eebus.models import (
    CapabilityState,
    CompressorFlexibilityState,
    ConsumptionLimitState,
    DeviceInfo,
    MEASUREMENT_ID_CATALOG,
    RoomHeatingValues,
    SetpointState,
    SystemFunctionState,
    _extract_flat_measurements,
    _extract_scoped_energy_kwh,
)
from custom_components.eebus.snapshot import (
    DevicePoller,
    RE_REGISTER_NOT_FOUND_STREAK,
    SnapshotResult,
    _async_fetch_device_info,
    _async_read_room_heating,
    _poll_read,
    _snapshot_observation_from_proto,
    _SNAPSHOT_FIELD_TO_STATE_FIELD,
    async_build_device_snapshot,
    async_build_snapshot,
)
from custom_components.eebus.state import (
    DeviceState,
    DeviceStateStore,
    HVACState,
    LPCState,
    MeasurementsState,
    OHPCFState,
    CapabilitiesState,
    ConnectionState,
    StateField,
    StateObservation,
)


def test_poll_interval_is_slow_reconciliation() -> None:
    assert POLL_INTERVAL == timedelta(minutes=5)


def test_stream_publish_does_not_postpone_reconciliation_poll() -> None:
    """Frequent push updates must not reset the coordinator's poll interval."""
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator.data = None
    coordinator.last_update_success = False
    coordinator.async_set_updated_data = MagicMock()
    coordinator.async_update_listeners = MagicMock()
    state = DeviceState(connection=ConnectionState(connected=True))

    coordinator._publish_state(state)

    assert coordinator.data is state
    assert coordinator.last_update_success is True
    coordinator.async_update_listeners.assert_called_once_with()
    coordinator.async_set_updated_data.assert_not_called()


async def test_ensure_channel_delegates_to_manager() -> None:
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    channel = MagicMock()
    coordinator._channel_manager = MagicMock()
    coordinator._channel_manager.ensure_channel = AsyncMock(return_value=channel)
    assert await coordinator._ensure_channel() is channel


async def test_initial_sync_uses_registration_and_stream_snapshot_without_unary_poll() -> None:
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator._runtime = MagicMock()
    coordinator._runtime.supports.side_effect = lambda feature: feature in {
        int(proto_stubs.FeatureId.FEATURE_CONSOLIDATED_DEVICE_STREAM),
        int(proto_stubs.FeatureId.FEATURE_DEVICE_SNAPSHOT),
    }
    coordinator._runtime.mark_available = MagicMock()
    coordinator._poller = MagicMock()
    coordinator._poller.ensure_registered = AsyncMock()
    coordinator._poller.poll = AsyncMock()
    coordinator._device_streams = MagicMock()
    expected = DeviceState(connection=ConnectionState(connected=True, ski_registered=True))
    coordinator._device_streams.wait_initial_snapshot = AsyncMock(return_value=expected)
    coordinator.async_set_updated_data = MagicMock()
    coordinator.async_config_entry_first_refresh = AsyncMock()

    await coordinator.async_initialize()

    coordinator._poller.ensure_registered.assert_awaited_once_with()
    coordinator._poller.poll.assert_not_awaited()
    coordinator.async_config_entry_first_refresh.assert_not_awaited()
    coordinator._device_streams.mark_registered.assert_called_once_with()
    coordinator._device_streams.start.assert_called_once_with()
    coordinator._device_streams.wait_initial_snapshot.assert_awaited_once()
    coordinator.async_set_updated_data.assert_not_called()


async def test_initial_sync_keeps_legacy_poll_then_stream() -> None:
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator._runtime = MagicMock()
    coordinator._runtime.supports.return_value = False
    coordinator._device_streams = MagicMock()
    coordinator.async_config_entry_first_refresh = AsyncMock()

    await coordinator.async_initialize()

    coordinator.async_config_entry_first_refresh.assert_awaited_once_with()
    coordinator._device_streams.start.assert_called_once_with()


async def test_initial_stream_timeout_requests_config_entry_retry() -> None:
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator._runtime = MagicMock()
    coordinator._runtime.supports.return_value = True
    coordinator._poller = MagicMock()
    coordinator._poller.ensure_registered = AsyncMock()
    coordinator._device_streams = MagicMock()
    coordinator._device_streams.wait_initial_snapshot = AsyncMock(side_effect=TimeoutError)

    with pytest.raises(ConfigEntryNotReady):
        await coordinator.async_initialize()


async def test_unauthenticated_poll_starts_reauthentication() -> None:
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator.host = "bridge.local"
    coordinator.port = 50051
    coordinator._was_unavailable = False
    coordinator._channel_manager = MagicMock()
    coordinator._channel_manager.invalidate = AsyncMock()
    error = AioRpcError(
        grpc.StatusCode.UNAUTHENTICATED,
        Metadata(),
        Metadata(),
        details="valid token required",
    )
    coordinator._poller = MagicMock()
    coordinator._poller.poll = AsyncMock(side_effect=error)
    with pytest.raises(ConfigEntryAuthFailed):
        await coordinator._async_update_data()
    coordinator._channel_manager.invalidate.assert_awaited_once_with()


async def test_poll_event_race_keeps_newer_stream_value() -> None:
    """Deterministically finish an old poll after a newer stream observation."""
    started = asyncio.Event()
    finish = asyncio.Event()
    store = DeviceStateStore()

    async def build(*_args, **_kwargs):
        started.set()
        await finish.wait()
        return SnapshotResult(
            StateObservation(
                state=DeviceState(measurements=MeasurementsState(power_watts=1000.0)),
                observed_fields=frozenset({StateField.POWER_WATTS}),
            ),
            False,
            0,
        )

    poller = DevicePoller("device-ski", AsyncMock(return_value=MagicMock()), store)
    with patch("custom_components.eebus.snapshot.async_build_snapshot", side_effect=build):
        task = asyncio.create_task(poller.poll())
        await started.wait()
        store.dispatch(
            StateObservation(
                state=DeviceState(measurements=MeasurementsState(power_watts=2000.0)),
                observed_fields=frozenset({StateField.POWER_WATTS}),
            )
        )
        finish.set()
        state = await task
    assert state.measurements.power_watts == 2000.0


async def test_poller_selects_legacy_capability_path_from_negotiated_contract() -> None:
    captured: dict[str, object] = {}

    async def build(*_args, **kwargs):
        captured.update(kwargs)
        return SnapshotResult(StateObservation(), False, 0)

    poller = DevicePoller(
        "device-ski",
        AsyncMock(return_value=MagicMock()),
        DeviceStateStore(),
        lambda feature: feature
        not in (
            proto_stubs.FeatureId.FEATURE_EXPLICIT_CAPABILITIES,
            proto_stubs.FeatureId.FEATURE_DEVICE_SNAPSHOT,
        ),
    )
    with patch("custom_components.eebus.snapshot.async_build_snapshot", side_effect=build):
        await poller.poll()

    assert captured["supports_explicit_capabilities"] is False


async def test_legacy_snapshot_cancellation_stops_hanging_status_read(monkeypatch) -> None:
    started = asyncio.Event()
    cancelled = asyncio.Event()

    async def hanging_status(*_args, **_kwargs):
        started.set()
        try:
            await asyncio.Event().wait()
        finally:
            cancelled.set()

    device_stub = SimpleNamespace(
        GetStatus=hanging_status,
        GetDeviceStatus=AsyncMock(return_value=proto_stubs.DeviceStatus(connected=True)),
        ListPairedDevices=AsyncMock(return_value=device_service_pb2.ListPairedDevicesResponse()),
        GetDeviceCapabilities=AsyncMock(return_value=proto_stubs.DeviceCapabilities(ski="device-ski")),
    )
    monitoring_stub = SimpleNamespace(
        GetPowerConsumption=AsyncMock(return_value=proto_stubs.PowerMeasurement()),
        GetMeasurements=AsyncMock(return_value=proto_stubs.MeasurementList()),
        GetEnergyConsumed=AsyncMock(return_value=proto_stubs.EnergyMeasurement()),
        GetDeviceDiagnostics=AsyncMock(return_value=proto_stubs.DeviceDiagnosticsData()),
    )
    lpc_stub = SimpleNamespace(
        GetConsumptionLimit=AsyncMock(return_value=proto_stubs.LoadLimit()),
        GetFailsafeLimit=AsyncMock(return_value=proto_stubs.FailsafeLimit()),
        GetHeartbeatStatus=AsyncMock(return_value=proto_stubs.HeartbeatStatus()),
    )
    monkeypatch.setattr(proto_stubs, "device_service_stub", lambda _channel: device_stub)
    monkeypatch.setattr(proto_stubs, "monitoring_service_stub", lambda _channel: monitoring_stub)
    monkeypatch.setattr(proto_stubs, "lpc_service_stub", lambda _channel: lpc_stub)
    monkeypatch.setattr(
        proto_stubs,
        "ohpcf_service_stub",
        lambda _channel: SimpleNamespace(GetCompressorFlexibility=AsyncMock(return_value=proto_stubs.CompressorFlexibility())),
    )
    monkeypatch.setattr(
        proto_stubs,
        "dhw_service_stub",
        lambda _channel: SimpleNamespace(
            GetDHWSetpoint=AsyncMock(return_value=proto_stubs.DHWSetpoint()),
            GetDHWSystemFunction=AsyncMock(return_value=proto_stubs.DHWSystemFunctionState()),
        ),
    )
    monkeypatch.setattr(
        proto_stubs,
        "hvac_service_stub",
        lambda _channel: SimpleNamespace(GetRoomHeating=AsyncMock(return_value=proto_stubs.RoomHeatingState())),
    )

    task = asyncio.create_task(
        async_build_snapshot(
            MagicMock(),
            "device-ski",
            ski_registered=True,
            not_found_streak=0,
        )
    )
    await started.wait()
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task
    await asyncio.wait_for(cancelled.wait(), timeout=1)


async def test_legacy_snapshot_hanging_rpc_stops_at_real_grpc_deadline(monkeypatch) -> None:
    class HangingDeviceService(device_service_pb2_grpc.DeviceServiceServicer):
        async def GetStatus(self, _request, _context):
            await asyncio.Event().wait()

        async def GetDeviceStatus(self, _request, _context):
            return proto_stubs.DeviceStatus(connected=True)

        async def ListPairedDevices(self, _request, _context):
            return device_service_pb2.ListPairedDevicesResponse()

    server = grpc.aio.server()
    device_service_pb2_grpc.add_DeviceServiceServicer_to_server(
        HangingDeviceService(), server
    )
    port = server.add_insecure_port("127.0.0.1:0")
    await server.start()
    channel = grpc.aio.insecure_channel(f"127.0.0.1:{port}")
    monkeypatch.setattr("custom_components.eebus.snapshot.RPC_TIMEOUT", 0.05)
    started = asyncio.get_running_loop().time()
    try:
        with pytest.raises(grpc.aio.AioRpcError) as raised:
            await async_build_snapshot(
                channel,
                "device-ski",
                ski_registered=True,
                not_found_streak=0,
                supports_explicit_capabilities=False,
            )
        assert raised.value.code() == grpc.StatusCode.DEADLINE_EXCEEDED
        assert asyncio.get_running_loop().time() - started < 0.5
    finally:
        await channel.close()
        await server.stop(grace=None)


async def test_partial_room_heating_poll_and_stream_clear_the_same_fields() -> None:
    """Both protobuf paths interpret an equivalent partial aggregate identically."""
    payload = hvac_service_pb2.RoomHeatingState(current_temperature_celsius=22.0)
    stub = SimpleNamespace(GetRoomHeating=AsyncMock(return_value=payload))
    with patch.object(proto_stubs, "hvac_service_stub", return_value=stub):
        poll_result = await _async_read_room_heating(MagicMock(), proto_stubs.DeviceRequest(ski="device-ski"))
    assert poll_result.value == RoomHeatingValues(None, None, 22.0)

    initial = DeviceState(
        connection=ConnectionState(connected=True),
        hvac=HVACState(
            setpoint=SetpointState(21.0, 5.0, 30.0, 0.5, True),
            system_function=SystemFunctionState("on", ("on", "off"), True),
        ),
        capabilities=CapabilitiesState(room_heating=CapabilityState.AVAILABLE),
        fresh_fields=frozenset(
            {
                StateField.ROOM_HEATING_SETPOINT,
                StateField.ROOM_HEATING_SYSTEM_FUNCTION,
            }
        ),
    )
    store = DeviceStateStore(initial=initial)
    streams = DeviceStreams(MagicMock(), MagicMock(), "device-ski", store, AsyncMock())
    streams.handle_room_heating_event(
        hvac_service_pb2.RoomHeatingEvent(
            ski="device-ski",
            event_type=hvac_service_pb2.ROOM_HEATING_EVENT_CURRENT_TEMPERATURE_UPDATED,
            state=payload,
        )
    )
    assert store.state.hvac.setpoint == initial.hvac.setpoint
    assert store.state.hvac.system_function == initial.hvac.system_function
    assert store.state.measurements.room_temperature_c == 22.0
    assert StateField.ROOM_HEATING_SETPOINT in store.state.fresh_fields
    assert StateField.ROOM_HEATING_SYSTEM_FUNCTION in store.state.fresh_fields


async def test_lpc_only_not_found_does_not_force_reregistration(monkeypatch) -> None:
    not_found = AioRpcError(grpc.StatusCode.NOT_FOUND, Metadata(), Metadata(), details="no lpc")
    unimplemented = AioRpcError(grpc.StatusCode.UNIMPLEMENTED, Metadata(), Metadata(), details="not supported")

    device_stub = SimpleNamespace(
        GetStatus=AsyncMock(return_value=proto_stubs.ServiceStatus(running=True, local_ski="local")),
        RegisterRemoteSKI=AsyncMock(return_value=proto_stubs.Empty()),
        GetDeviceStatus=AsyncMock(return_value=proto_stubs.DeviceStatus(connected=True)),
        ListPairedDevices=AsyncMock(return_value=device_service_pb2.ListPairedDevicesResponse()),
        GetDeviceCapabilities=AsyncMock(return_value=proto_stubs.DeviceCapabilities(ski="device-ski")),
    )
    monitoring_stub = SimpleNamespace(
        GetPowerConsumption=AsyncMock(return_value=proto_stubs.PowerMeasurement(watts=1000)),
        GetMeasurements=AsyncMock(return_value=proto_stubs.MeasurementList()),
        GetEnergyConsumed=AsyncMock(return_value=proto_stubs.EnergyMeasurement(kilowatt_hours=12)),
        GetDeviceDiagnostics=AsyncMock(return_value=monitoring_service_pb2.DeviceDiagnosticsData()),
    )
    lpc_stub = SimpleNamespace(
        GetConsumptionLimit=AsyncMock(side_effect=not_found),
        GetFailsafeLimit=AsyncMock(side_effect=not_found),
        GetHeartbeatStatus=AsyncMock(side_effect=not_found),
    )
    monkeypatch.setattr(proto_stubs, "device_service_stub", lambda _channel: device_stub)
    monkeypatch.setattr(proto_stubs, "monitoring_service_stub", lambda _channel: monitoring_stub)
    monkeypatch.setattr(proto_stubs, "lpc_service_stub", lambda _channel: lpc_stub)
    monkeypatch.setattr(
        proto_stubs,
        "ohpcf_service_stub",
        lambda _channel: SimpleNamespace(GetCompressorFlexibility=AsyncMock(side_effect=unimplemented)),
    )
    monkeypatch.setattr(
        proto_stubs,
        "dhw_service_stub",
        lambda _channel: SimpleNamespace(
            GetDHWSetpoint=AsyncMock(side_effect=unimplemented),
            GetDHWSystemFunction=AsyncMock(side_effect=unimplemented),
        ),
    )
    monkeypatch.setattr(
        proto_stubs,
        "hvac_service_stub",
        lambda _channel: SimpleNamespace(GetRoomHeating=AsyncMock(side_effect=unimplemented)),
    )

    result = await async_build_snapshot(
        MagicMock(),
        "device-ski",
        ski_registered=True,
        not_found_streak=3,
        supports_explicit_capabilities=False,
    )

    assert result.not_found_streak == 0
    device_stub.RegisterRemoteSKI.assert_not_awaited()
    device_stub.GetDeviceCapabilities.assert_not_awaited()
    assert device_stub.GetStatus.await_args.kwargs["timeout"] == 10


def test_start_streams_delegates_to_device_stream_component() -> None:
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator._device_streams = MagicMock()
    coordinator.async_start_streams()
    coordinator._device_streams.start.assert_called_once_with()


def test_device_streams_starts_one_consolidated_consumer() -> None:
    streams = DeviceStreams.__new__(DeviceStreams)
    streams._ski = "test-ski"
    streams._manager = MagicMock()
    streams._legacy_manager = MagicMock()
    streams._supports_feature = lambda _feature: True
    streams._started = False
    streams.start()
    mapping, name = streams._manager.start.call_args.args
    assert list(mapping) == ["device_state"]
    assert name == "eebus_{name}_test-ski"
    assert streams._manager.start.call_args.kwargs["on_unimplemented"] is not None
    streams._legacy_manager.start.assert_not_called()

    streams._manager.start.call_args.kwargs["on_unimplemented"]("device_state")
    legacy_mapping, legacy_name = streams._legacy_manager.start.call_args.args
    assert list(legacy_mapping) == [
        "device_events",
        "lpc_events",
        "measurements",
        "ohpcf_events",
        "dhw_events",
        "dhw_sysfn_events",
        "room_heating_events",
    ]
    assert legacy_name == "eebus_{name}_test-ski"


def test_device_streams_selects_legacy_profile_without_probe_rpc() -> None:
    streams = DeviceStreams.__new__(DeviceStreams)
    streams._ski = "test-ski"
    streams._manager = MagicMock()
    streams._legacy_manager = MagicMock()
    streams._supports_feature = MagicMock(return_value=False)
    streams._started = False

    streams.start()

    streams._manager.start.assert_not_called()
    streams._legacy_manager.start.assert_called_once()
    streams._supports_feature.assert_called_once_with(
        proto_stubs.FeatureId.FEATURE_CONSOLIDATED_DEVICE_STREAM
    )


async def test_device_streams_reselects_profile_after_contract_change() -> None:
    """A channel-generation upgrade replaces legacy workers with consolidated."""
    streams = DeviceStreams.__new__(DeviceStreams)
    streams._ski = "test-ski"
    streams._manager = MagicMock(stop=AsyncMock())
    streams._legacy_manager = MagicMock(stop=AsyncMock())
    streams._supports_feature = MagicMock(return_value=False)
    streams._started = True
    streams._restart_pending = False
    streams._restart_task = None

    streams.contract_changed()
    assert streams._restart_task is not None
    await streams._restart_task
    streams._legacy_manager.start.assert_called_once()

    streams._supports_feature.return_value = True
    streams.contract_changed()
    assert streams._restart_task is not None
    await streams._restart_task
    streams._manager.start.assert_called_once()
    streams._legacy_manager.stop.assert_awaited()


def _streams_with(initial: DeviceState) -> tuple[DeviceStateStore, DeviceStreams, MagicMock]:
    store = DeviceStateStore(initial=initial)
    hass = MagicMock()
    hass.async_create_task.side_effect = lambda coroutine: coroutine.close()
    streams = DeviceStreams(hass, MagicMock(), "device-ski", store, AsyncMock())
    return store, streams, hass


def test_consolidated_stream_ignores_duplicates_and_detects_revision_gap() -> None:
    store, streams, hass = _streams_with(DeviceState())
    initial = device_service_pb2.DeviceStateEvent(
        ski="device-ski",
        revision=4,
        resync_required=device_service_pb2.ResyncRequired(
            reason=device_service_pb2.RESYNC_REASON_INITIAL_STATE_REQUIRED
        ),
    )
    streams.handle_device_state_event(initial)
    streams.handle_device_state_event(initial)
    streams.handle_device_state_event(
        device_service_pb2.DeviceStateEvent(
            ski="device-ski",
            revision=6,
            device=device_service_pb2.DeviceEvent(
                ski="device-ski",
                event_type=device_service_pb2.DEVICE_EVENT_PROVIDER_UPDATED,
            ),
        )
    )

    assert streams._last_revision == 6
    assert hass.async_create_task.call_count == 2


def test_consolidated_capability_payload_is_explicit_truth() -> None:
    store, streams, _hass = _streams_with(DeviceState(capabilities=CapabilitiesState(dhw=CapabilityState.AVAILABLE)))
    streams.handle_device_state_event(
        device_service_pb2.DeviceStateEvent(
            ski="device-ski",
            revision=1,
            capability=device_service_pb2.DeviceCapabilities(
                ski="device-ski",
                capabilities=[
                    device_service_pb2.DeviceCapability(
                        id=device_service_pb2.CAPABILITY_DHW,
                        state=device_service_pb2.CAPABILITY_STATE_UNSUPPORTED,
                        reason=device_service_pb2.CAPABILITY_REASON_REMOTE_NOT_ADVERTISED,
                    )
                ],
            ),
        )
    )

    assert store.state.explicit_capability_contract is True
    assert store.state.capabilities.dhw == CapabilityState.UNSUPPORTED


def test_consolidated_initial_snapshot_replaces_resync_without_refresh() -> None:
    publish = MagicMock()
    store = DeviceStateStore(
        publish=publish,
        initial=DeviceState(connection=ConnectionState(ski_registered=True)),
    )
    hass = MagicMock()
    streams = DeviceStreams(hass, MagicMock(), "device-ski", store, AsyncMock())
    available = proto_stubs.SnapshotValueState.SNAPSHOT_VALUE_STATE_AVAILABLE
    streams.handle_device_state_event(
        proto_stubs.DeviceStateEvent(
            ski="device-ski",
            revision=7,
            initial_snapshot=proto_stubs.DeviceSnapshot(
                ski="device-ski",
                local_ski="local-ski",
                connection=proto_stubs.DeviceStatus(connected=True),
                field_states=[
                    proto_stubs.SnapshotFieldStatus(
                        id=proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_CONNECTED, state=available
                    ),
                    proto_stubs.SnapshotFieldStatus(
                        id=proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_LOCAL_SKI, state=available
                    ),
                ],
                capabilities=proto_stubs.DeviceCapabilities(ski="device-ski"),
            ),
        )
    )

    assert store.state.connection.connected is True
    assert store.state.connection.local_ski == "local-ski"
    assert store.state.connection.ski_registered is True
    publish.assert_called_once()
    hass.async_create_task.assert_not_called()


async def test_wait_initial_snapshot_returns_reduced_stream_state() -> None:
    store = DeviceStateStore()
    streams = DeviceStreams(MagicMock(), MagicMock(), "device-ski", store, AsyncMock())
    streams.mark_registered()
    waiter = asyncio.create_task(streams.wait_initial_snapshot(1))
    streams.handle_device_state_event(
        proto_stubs.DeviceStateEvent(
            ski="device-ski",
            revision=1,
            initial_snapshot=proto_stubs.DeviceSnapshot(
                ski="device-ski",
                connection=proto_stubs.DeviceStatus(connected=True),
                capabilities=proto_stubs.DeviceCapabilities(ski="device-ski"),
                field_states=[
                    proto_stubs.SnapshotFieldStatus(
                        id=proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_CONNECTED,
                        state=proto_stubs.SnapshotValueState.SNAPSHOT_VALUE_STATE_AVAILABLE,
                    )
                ],
            ),
        )
    )
    state = await waiter
    assert state.connection.connected is True
    assert state.connection.ski_registered is True


async def test_event_drop_requests_exactly_one_snapshot_reconciliation() -> None:
    poll_snapshot = AsyncMock()
    hass = MagicMock()
    hass.async_create_task.side_effect = asyncio.create_task
    streams = DeviceStreams(
        hass,
        MagicMock(),
        "device-ski",
        DeviceStateStore(),
        poll_snapshot,
    )
    streams._last_revision = 1
    streams.handle_device_state_event(
        proto_stubs.DeviceStateEvent(
            ski="device-ski",
            revision=3,
            resync_required=proto_stubs.ResyncRequired(
                reason=proto_stubs.ResyncReason.RESYNC_REASON_EVENT_DROPPED,
                dropped_events=3,
            ),
        )
    )
    assert streams._refresh_task is not None
    await streams._refresh_task
    poll_snapshot.assert_awaited_once_with()


def test_scoped_legacy_energy_uses_exact_allowlist_not_substrings() -> None:
    measurements = [
        proto_stubs.MeasurementEntry(
            type="energy_consumed_heating", value=12, unit="kWh"
        ),
        proto_stubs.MeasurementEntry(
            type="experimental_heating_energy_counter", value=999, unit="kWh"
        ),
        proto_stubs.MeasurementEntry(
            type="energy_consumed_dhw", value=4, unit="bananas"
        ),
    ]
    assert _extract_scoped_energy_kwh(measurements) == {
        "heating": 12,
        "dhw": None,
    }


@pytest.mark.parametrize(
    "payload",
    [
        {
            "measurement": monitoring_service_pb2.MeasurementEvent(
                ski="device-ski",
                event_type=monitoring_service_pb2.MEASUREMENT_EVENT_POWER_UPDATED,
                power=proto_stubs.PowerMeasurement(watts=1250),
            )
        },
        {
            "lpc": lpc_service_pb2.LPCEvent(
                ski="device-ski",
                event_type=lpc_service_pb2.LPC_EVENT_LIMIT_UPDATED,
                limit_update=proto_stubs.LoadLimit(value_watts=3000, is_active=True),
            )
        },
        {
            "dhw": device_service_pb2.DeviceStateDHWEvent(
                temperature=dhw_service_pb2.DHWEvent(
                    ski="device-ski",
                    event_type=dhw_service_pb2.DHW_EVENT_SETPOINT_UPDATED,
                    setpoint=dhw_service_pb2.DHWSetpoint(value_celsius=50),
                )
            )
        },
        {
            "hvac": hvac_service_pb2.RoomHeatingEvent(
                ski="device-ski",
                event_type=hvac_service_pb2.ROOM_HEATING_EVENT_CURRENT_TEMPERATURE_UPDATED,
                state=hvac_service_pb2.RoomHeatingState(current_temperature_celsius=21),
            )
        },
        {
            "ohpcf": ohpcf_service_pb2.OHPCFEvent(
                ski="device-ski",
                event_type=ohpcf_service_pb2.OHPCF_EVENT_STATE_UPDATED,
                flexibility=ohpcf_service_pb2.CompressorFlexibility(available=True),
            )
        },
    ],
)
def test_complete_consolidated_domain_delta_publishes_once_without_refresh(payload: dict[str, object]) -> None:
    publish = MagicMock()
    store = DeviceStateStore(publish=publish)
    hass = MagicMock()
    streams = DeviceStreams(hass, MagicMock(), "device-ski", store, AsyncMock())

    streams.handle_device_state_event(
        device_service_pb2.DeviceStateEvent(
            ski="device-ski",
            revision=1,
            availability=device_service_pb2.EVENT_AVAILABILITY_AVAILABLE,
            **payload,
        )
    )

    publish.assert_called_once()
    hass.async_create_task.assert_not_called()


def test_detail_measurements_publish_atomically_without_refresh() -> None:
    publish = MagicMock()
    store = DeviceStateStore(publish=publish)
    hass = MagicMock()
    streams = DeviceStreams(hass, MagicMock(), "device-ski", store, AsyncMock())
    measurement = monitoring_service_pb2.MeasurementEvent(
        ski="device-ski",
        # New clients consume the additive list; old consolidated-stream clients
        # see UNSPECIFIED and retain their established poll fallback.
        event_type=monitoring_service_pb2.MEASUREMENT_EVENT_UNSPECIFIED,
        measurements=monitoring_service_pb2.MeasurementList(
            measurements=[
                proto_stubs.MeasurementEntry(type="power_l1", value=100, unit="W"),
                proto_stubs.MeasurementEntry(type="power_l2", value=200, unit="W"),
                proto_stubs.MeasurementEntry(type="power_l3", value=300, unit="W"),
            ]
        ),
    )

    streams.handle_device_state_event(
        device_service_pb2.DeviceStateEvent(
            ski="device-ski",
            revision=1,
            availability=device_service_pb2.EVENT_AVAILABILITY_AVAILABLE,
            measurement=measurement,
        )
    )

    assert store.state.measurements.power_l1_w == 100
    assert store.state.measurements.power_l2_w == 200
    assert store.state.measurements.power_l3_w == 300
    publish.assert_called_once()
    hass.async_create_task.assert_not_called()


def test_consolidated_device_connected_event_triggers_reconciliation_poll() -> None:
    store = DeviceStateStore()
    hass = MagicMock()
    hass.async_create_task.side_effect = lambda coroutine: coroutine.close()
    streams = DeviceStreams(hass, MagicMock(), "device-ski", store, AsyncMock())

    streams.handle_device_state_event(
        device_service_pb2.DeviceStateEvent(
            ski="device-ski",
            revision=1,
            availability=device_service_pb2.EVENT_AVAILABILITY_AVAILABLE,
            device=device_service_pb2.DeviceEvent(
                ski="device-ski",
                event_type=device_service_pb2.DEVICE_EVENT_CONNECTED,
            ),
        )
    )

    assert store.state.connection.connected is True
    hass.async_create_task.assert_called_once()


def test_consolidated_provider_acknowledgement_does_not_poll() -> None:
    store = DeviceStateStore()
    hass = MagicMock()
    streams = DeviceStreams(hass, MagicMock(), "device-ski", store, AsyncMock())

    streams.handle_device_state_event(
        device_service_pb2.DeviceStateEvent(
            ski="device-ski",
            revision=1,
            availability=device_service_pb2.EVENT_AVAILABILITY_AVAILABLE,
            device=device_service_pb2.DeviceEvent(
                ski="device-ski",
                event_type=device_service_pb2.DEVICE_EVENT_PROVIDER_UPDATED,
            ),
        )
    )

    hass.async_create_task.assert_not_called()


def test_unclassified_consolidated_measurement_falls_back_to_poll() -> None:
    store = DeviceStateStore()
    hass = MagicMock()
    hass.async_create_task.side_effect = lambda coroutine: coroutine.close()
    streams = DeviceStreams(hass, MagicMock(), "device-ski", store, AsyncMock())

    streams.handle_device_state_event(
        device_service_pb2.DeviceStateEvent(
            ski="device-ski",
            revision=1,
            availability=device_service_pb2.EVENT_AVAILABILITY_AVAILABLE,
            measurement=monitoring_service_pb2.MeasurementEvent(
                ski="device-ski",
                event_type=monitoring_service_pb2.MEASUREMENT_EVENT_UNSPECIFIED,
            ),
        )
    )

    hass.async_create_task.assert_called_once()


def test_partial_phase_list_marks_absent_phase_unavailable() -> None:
    initial = DeviceState(
        measurements=MeasurementsState(power_l1_w=1, power_l2_w=2, power_l3_w=3),
        fresh_fields=frozenset({StateField.POWER_L1_W, StateField.POWER_L2_W, StateField.POWER_L3_W}),
    )
    store = DeviceStateStore(initial=initial)
    hass = MagicMock()
    streams = DeviceStreams(hass, MagicMock(), "device-ski", store, AsyncMock())

    streams.handle_device_state_event(
        device_service_pb2.DeviceStateEvent(
            ski="device-ski",
            revision=1,
            availability=device_service_pb2.EVENT_AVAILABILITY_AVAILABLE,
            measurement=monitoring_service_pb2.MeasurementEvent(
                ski="device-ski",
                event_type=monitoring_service_pb2.MEASUREMENT_EVENT_UNSPECIFIED,
                update_field=monitoring_service_pb2.MEASUREMENT_UPDATE_FIELD_POWER_PER_PHASE,
                measurements=monitoring_service_pb2.MeasurementList(
                    measurements=[
                        proto_stubs.MeasurementEntry(type="power_l1", value=100, unit="W"),
                        proto_stubs.MeasurementEntry(type="power_l2", value=200, unit="W"),
                    ]
                ),
            ),
        )
    )

    assert store.state.measurements.power_l1_w == 100
    assert store.state.measurements.power_l2_w == 200
    assert store.state.measurements.power_l3_w == 3
    assert StateField.POWER_L2_W in store.state.fresh_fields
    assert StateField.POWER_L3_W not in store.state.fresh_fields
    hass.async_create_task.assert_not_called()


def test_payloadless_consolidated_update_is_unavailable_without_legacy_poll() -> None:
    initial = DeviceState(
        measurements=MeasurementsState(power_watts=1200),
        fresh_fields=frozenset({StateField.POWER_WATTS}),
    )
    publish = MagicMock()
    store = DeviceStateStore(publish=publish, initial=initial)
    hass = MagicMock()
    streams = DeviceStreams(hass, MagicMock(), "device-ski", store, AsyncMock())

    streams.handle_device_state_event(
        device_service_pb2.DeviceStateEvent(
            ski="device-ski",
            revision=1,
            availability=device_service_pb2.EVENT_AVAILABILITY_TEMPORARILY_UNAVAILABLE,
            measurement=monitoring_service_pb2.MeasurementEvent(
                ski="device-ski",
                event_type=monitoring_service_pb2.MEASUREMENT_EVENT_POWER_UPDATED,
            ),
        )
    )

    assert store.state.measurements.power_watts == 1200
    assert StateField.POWER_WATTS not in store.state.fresh_fields
    publish.assert_called_once()
    hass.async_create_task.assert_not_called()


def test_explicit_unavailability_rejects_stale_payload_data() -> None:
    initial = DeviceState(
        measurements=MeasurementsState(power_watts=1200),
        fresh_fields=frozenset({StateField.POWER_WATTS}),
    )
    store = DeviceStateStore(initial=initial)
    hass = MagicMock()
    streams = DeviceStreams(hass, MagicMock(), "device-ski", store, AsyncMock())

    streams.handle_device_state_event(
        device_service_pb2.DeviceStateEvent(
            ski="device-ski",
            revision=1,
            availability=device_service_pb2.EVENT_AVAILABILITY_TEMPORARILY_UNAVAILABLE,
            measurement=monitoring_service_pb2.MeasurementEvent(
                ski="device-ski",
                event_type=monitoring_service_pb2.MEASUREMENT_EVENT_POWER_UPDATED,
                power=proto_stubs.PowerMeasurement(watts=9999),
            ),
        )
    )

    assert store.state.measurements.power_watts == 1200
    assert StateField.POWER_WATTS not in store.state.fresh_fields
    hass.async_create_task.assert_not_called()


def test_missing_detail_measurement_uses_explicit_target_without_polling() -> None:
    initial = DeviceState(
        measurements=MeasurementsState(power_l1_w=100, power_l2_w=200, power_l3_w=300),
        fresh_fields=frozenset({StateField.POWER_L1_W, StateField.POWER_L2_W, StateField.POWER_L3_W}),
    )
    store = DeviceStateStore(initial=initial)
    hass = MagicMock()
    streams = DeviceStreams(hass, MagicMock(), "device-ski", store, AsyncMock())

    streams.handle_device_state_event(
        device_service_pb2.DeviceStateEvent(
            ski="device-ski",
            revision=1,
            availability=device_service_pb2.EVENT_AVAILABILITY_TEMPORARILY_UNAVAILABLE,
            measurement=monitoring_service_pb2.MeasurementEvent(
                ski="device-ski",
                event_type=monitoring_service_pb2.MEASUREMENT_EVENT_UNSPECIFIED,
                update_field=monitoring_service_pb2.MEASUREMENT_UPDATE_FIELD_POWER_PER_PHASE,
            ),
        )
    )

    assert store.state.measurements.power_l1_w == 100
    assert not {
        StateField.POWER_L1_W,
        StateField.POWER_L2_W,
        StateField.POWER_L3_W,
    }.intersection(store.state.fresh_fields)
    hass.async_create_task.assert_not_called()


def test_explicit_unsupported_is_distinct_from_temporary_unavailability() -> None:
    initial = DeviceState(
        lpc=LPCState(consumption_limit=ConsumptionLimitState(3000, True, True)),
        capabilities=CapabilitiesState(lpc=CapabilityState.AVAILABLE),
        fresh_fields=frozenset({StateField.CONSUMPTION_LIMIT}),
    )
    store = DeviceStateStore(initial=initial)
    streams = DeviceStreams(MagicMock(), MagicMock(), "device-ski", store, AsyncMock())

    streams.handle_device_state_event(
        device_service_pb2.DeviceStateEvent(
            ski="device-ski",
            revision=1,
            availability=device_service_pb2.EVENT_AVAILABILITY_UNSUPPORTED,
            lpc=lpc_service_pb2.LPCEvent(
                ski="device-ski",
                event_type=lpc_service_pb2.LPC_EVENT_LIMIT_UPDATED,
                limit_update=proto_stubs.LoadLimit(value_watts=9999),
            ),
        )
    )

    assert store.state.lpc.consumption_limit is None
    assert store.state.capabilities.lpc == CapabilityState.UNSUPPORTED
    assert StateField.CONSUMPTION_LIMIT not in store.state.fresh_fields


def test_ohpcf_partial_delta_preserves_unrelated_aggregate_fields() -> None:
    current = CompressorFlexibilityState(
        True,
        "COMPRESSOR_STATE_RUNNING",
        1000,
        2000,
        True,
        True,
        600,
        300,
    )
    store = DeviceStateStore(
        initial=DeviceState(
            ohpcf=OHPCFState(compressor_flexibility=current),
            fresh_fields=frozenset({StateField.COMPRESSOR_FLEXIBILITY}),
        )
    )
    streams = DeviceStreams(MagicMock(), MagicMock(), "device-ski", store, AsyncMock())

    streams.handle_device_state_event(
        device_service_pb2.DeviceStateEvent(
            ski="device-ski",
            revision=1,
            availability=device_service_pb2.EVENT_AVAILABILITY_AVAILABLE,
            ohpcf=ohpcf_service_pb2.OHPCFEvent(
                ski="device-ski",
                event_type=ohpcf_service_pb2.OHPCF_EVENT_DATA_UPDATED,
                update_field=ohpcf_service_pb2.OHPCF_UPDATE_FIELD_STOPPABLE,
                flexibility=ohpcf_service_pb2.CompressorFlexibility(is_stoppable=False),
            ),
        )
    )

    assert store.state.ohpcf.compressor_flexibility == CompressorFlexibilityState(
        True,
        "COMPRESSOR_STATE_RUNNING",
        1000,
        2000,
        True,
        False,
        600,
        300,
    )

    streams.handle_device_state_event(
        device_service_pb2.DeviceStateEvent(
            ski="device-ski",
            revision=2,
            availability=device_service_pb2.EVENT_AVAILABILITY_AVAILABLE,
            ohpcf=ohpcf_service_pb2.OHPCFEvent(
                ski="device-ski",
                event_type=ohpcf_service_pb2.OHPCF_EVENT_STATE_UPDATED,
                update_field=ohpcf_service_pb2.OHPCF_UPDATE_FIELD_STATE,
                flexibility=ohpcf_service_pb2.CompressorFlexibility(
                    available=False,
                    state=ohpcf_service_pb2.COMPRESSOR_STATE_COMPLETED,
                ),
            ),
        )
    )
    merged = store.state.ohpcf.compressor_flexibility
    assert merged is not None
    assert merged.available is False
    assert merged.state == "COMPRESSOR_STATE_COMPLETED"
    assert merged.requested_power_estimate_w == 1000
    assert merged.minimal_run_seconds == 600

    streams.handle_device_state_event(
        device_service_pb2.DeviceStateEvent(
            ski="device-ski",
            revision=3,
            availability=device_service_pb2.EVENT_AVAILABILITY_AVAILABLE,
            ohpcf=ohpcf_service_pb2.OHPCFEvent(
                ski="device-ski",
                event_type=ohpcf_service_pb2.OHPCF_EVENT_DATA_UPDATED,
                update_field=ohpcf_service_pb2.OHPCF_UPDATE_FIELD_REQUESTED_POWER_ESTIMATE,
                flexibility=ohpcf_service_pb2.CompressorFlexibility(),
            ),
        )
    )
    cleared = store.state.ohpcf.compressor_flexibility
    assert cleared is not None
    assert cleared.requested_power_estimate_w is None
    assert StateField.COMPRESSOR_FLEXIBILITY in store.state.fresh_fields


async def test_refresh_requests_are_coalesced_until_completion() -> None:
    started = asyncio.Event()
    release = asyncio.Event()
    calls = 0

    async def refresh() -> None:
        nonlocal calls
        calls += 1
        started.set()
        await release.wait()

    hass = MagicMock()
    hass.async_create_task.side_effect = asyncio.create_task
    streams = DeviceStreams(hass, MagicMock(), "device-ski", DeviceStateStore(), refresh)

    streams._refresh()
    streams._refresh()
    await started.wait()
    assert calls == 1
    release.set()
    assert streams._refresh_task is not None
    await streams._refresh_task


async def test_refresh_signal_during_poll_runs_one_trailing_refresh() -> None:
    first_started = asyncio.Event()
    release_first = asyncio.Event()
    second_started = asyncio.Event()
    calls = 0

    async def refresh() -> None:
        nonlocal calls
        calls += 1
        if calls == 1:
            first_started.set()
            await release_first.wait()
        else:
            second_started.set()

    hass = MagicMock()
    hass.async_create_task.side_effect = asyncio.create_task
    streams = DeviceStreams(hass, MagicMock(), "device-ski", DeviceStateStore(), refresh)

    streams._refresh()
    await first_started.wait()
    streams._refresh()
    streams._refresh()
    task = streams._refresh_task
    assert task is not None
    release_first.set()
    await second_started.wait()
    await task

    assert calls == 2


async def test_resync_reconciles_state_with_fresh_poll() -> None:
    store = DeviceStateStore(initial=DeviceState(measurements=MeasurementsState(power_watts=1.0)))

    async def refresh() -> None:
        store.dispatch(
            StateObservation(
                state=DeviceState(measurements=MeasurementsState(power_watts=2.0)),
                observed_fields=frozenset({StateField.POWER_WATTS}),
            )
        )

    hass = MagicMock()
    hass.async_create_task.side_effect = asyncio.create_task
    streams = DeviceStreams(hass, MagicMock(), "device-ski", store, refresh)
    streams.handle_device_state_event(
        device_service_pb2.DeviceStateEvent(
            ski="device-ski",
            revision=8,
            resync_required=device_service_pb2.ResyncRequired(
                reason=device_service_pb2.RESYNC_REASON_EVENT_DROPPED,
                dropped_events=3,
            ),
        )
    )
    assert streams._refresh_task is not None
    await streams._refresh_task

    assert store.state.measurements.power_watts == 2.0
    assert StateField.POWER_WATTS in store.state.fresh_fields


def test_disconnect_event_immediately_disables_device_and_retains_values() -> None:
    initial = DeviceState(
        connection=ConnectionState(connected=True),
        measurements=MeasurementsState(power_watts=1200.0),
        fresh_fields=frozenset({StateField.POWER_WATTS}),
    )
    store, streams, hass = _streams_with(initial)
    streams.handle_device_event(
        device_service_pb2.DeviceEvent(
            ski="device-ski",
            event_type=device_service_pb2.DEVICE_EVENT_DISCONNECTED,
        )
    )
    assert store.state.connection.connected is False
    assert store.state.measurements.power_watts == 1200.0
    assert StateField.POWER_WATTS in store.state.fresh_fields
    hass.async_create_task.assert_called_once()


def test_payload_free_lpc_event_marks_limit_stale_before_refresh() -> None:
    initial = DeviceState(
        connection=ConnectionState(connected=True),
        lpc=LPCState(consumption_limit=ConsumptionLimitState(4200.0, True, True)),
        capabilities=CapabilitiesState(lpc=CapabilityState.AVAILABLE),
        fresh_fields=frozenset({StateField.CONSUMPTION_LIMIT}),
    )
    store, streams, _ = _streams_with(initial)
    streams.handle_lpc_event(
        lpc_service_pb2.LPCEvent(
            ski="device-ski",
            event_type=lpc_service_pb2.LPC_EVENT_LIMIT_UPDATED,
        )
    )
    assert store.state.lpc.consumption_limit == initial.lpc.consumption_limit
    assert store.state.capabilities.lpc == CapabilityState.TEMPORARILY_UNAVAILABLE
    assert StateField.CONSUMPTION_LIMIT not in store.state.fresh_fields


def test_heartbeat_stream_payload_updates_without_refresh() -> None:
    store, streams, hass = _streams_with(DeviceState())
    streams.handle_lpc_event(
        lpc_service_pb2.LPCEvent(
            ski="device-ski",
            event_type=lpc_service_pb2.LPC_EVENT_HEARTBEAT_TIMEOUT,
            heartbeat_update=lpc_service_pb2.HeartbeatStatus(running=True, within_duration=False),
        )
    )
    heartbeat = store.state.lpc.heartbeat_status
    assert heartbeat is not None
    assert heartbeat.running is True
    assert heartbeat.within_duration is False
    assert StateField.HEARTBEAT_STATUS in store.state.fresh_fields
    hass.async_create_task.assert_not_called()


def test_payload_free_measurement_event_marks_leaf_stale_before_refresh() -> None:
    initial = DeviceState(
        connection=ConnectionState(connected=True),
        measurements=MeasurementsState(power_watts=1200.0),
        fresh_fields=frozenset({StateField.POWER_WATTS}),
    )
    store, streams, _ = _streams_with(initial)
    streams.handle_measurement_event(
        monitoring_service_pb2.MeasurementEvent(
            ski="device-ski",
            event_type=monitoring_service_pb2.MEASUREMENT_EVENT_POWER_UPDATED,
        )
    )
    assert store.state.measurements.power_watts == 1200.0
    assert StateField.POWER_WATTS not in store.state.fresh_fields


def test_payload_free_ohpcf_event_marks_offer_stale_before_refresh() -> None:
    offer = CompressorFlexibilityState(True, "COMPRESSOR_STATE_RUNNING", 1000.0, None, True, False, 60, 60)
    initial = DeviceState(
        connection=ConnectionState(connected=True),
        ohpcf=OHPCFState(compressor_flexibility=offer),
        capabilities=CapabilitiesState(ohpcf=CapabilityState.AVAILABLE),
        fresh_fields=frozenset({StateField.COMPRESSOR_FLEXIBILITY}),
    )
    store, streams, _ = _streams_with(initial)
    streams.handle_ohpcf_event(
        ohpcf_service_pb2.OHPCFEvent(
            ski="device-ski",
            event_type=ohpcf_service_pb2.OHPCF_EVENT_STATE_UPDATED,
        )
    )
    assert store.state.ohpcf.compressor_flexibility == offer
    assert store.state.capabilities.ohpcf == CapabilityState.TEMPORARILY_UNAVAILABLE
    assert StateField.COMPRESSOR_FLEXIBILITY not in store.state.fresh_fields


def test_payload_free_room_event_marks_aggregate_stale_before_refresh() -> None:
    initial = DeviceState(
        connection=ConnectionState(connected=True),
        hvac=HVACState(setpoint=SetpointState(21.0, 5.0, 30.0, 0.5, True)),
        capabilities=CapabilitiesState(room_heating=CapabilityState.AVAILABLE),
        fresh_fields=frozenset({StateField.ROOM_HEATING_SETPOINT}),
    )
    store, streams, _ = _streams_with(initial)
    streams.handle_room_heating_event(
        hvac_service_pb2.RoomHeatingEvent(
            ski="device-ski",
            event_type=hvac_service_pb2.ROOM_HEATING_EVENT_SETPOINT_UPDATED,
        )
    )
    assert store.state.hvac.setpoint == initial.hvac.setpoint
    assert store.state.capabilities.room_heating == CapabilityState.TEMPORARILY_UNAVAILABLE
    assert StateField.ROOM_HEATING_SETPOINT not in store.state.fresh_fields


async def test_shutdown_releases_entry_resources_but_keeps_shared_transport() -> None:
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    lifecycle = MagicMock()
    coordinator._provider_manager = MagicMock()
    coordinator._provider_manager.async_stop = AsyncMock()
    coordinator._runtime = MagicMock()
    coordinator._runtime.release_device_session = AsyncMock()
    coordinator._runtime.close = AsyncMock()
    coordinator._runtime_session = MagicMock()
    coordinator._owns_runtime = False
    lifecycle.attach_mock(coordinator._provider_manager.async_stop, "providers")
    lifecycle.attach_mock(coordinator._runtime.release_device_session, "session")
    await coordinator.async_shutdown()
    assert lifecycle.mock_calls == [
        call.providers(),
        call.session(coordinator._runtime_session),
    ]
    coordinator._runtime.close.assert_not_awaited()


async def test_poll_read_not_found_is_attempted_once() -> None:
    rpc = AsyncMock(side_effect=AioRpcError(grpc.StatusCode.NOT_FOUND, Metadata(), Metadata(), details="not found"))
    request = proto_stubs.DeviceRequest(ski="ski-a")
    result = await _poll_read("power", rpc, request, "ski-a")
    assert result.saw_not_found is True
    assert result.status == grpc.StatusCode.NOT_FOUND
    rpc.assert_awaited_once_with(request, timeout=10)


async def test_fetch_device_info_uses_matching_ski() -> None:
    stub = SimpleNamespace(
        ListPairedDevices=AsyncMock(
            return_value=device_service_pb2.ListPairedDevicesResponse(
                devices=[
                    device_service_pb2.PairedDevice(ski="other", brand="Other"),
                    device_service_pb2.PairedDevice(ski="target", brand="Bosch", model="Compress", serial="SN"),
                ]
            )
        )
    )
    assert await _async_fetch_device_info(stub, "target") == DeviceInfo(
        manufacturer="Bosch", model="Compress", serial="SN"
    )


def test_extract_flat_measurements_maps_known_types() -> None:
    entries = [
        SimpleNamespace(type="power_l1", value=230.0, unit="W"),
        SimpleNamespace(type="frequency", value=50.0, unit="Hz"),
        SimpleNamespace(type="unknown", value=1.0, unit="bananas"),
    ]
    assert _extract_flat_measurements(entries) == {
        "power_l1_w": 230.0,
        "frequency_hz": 50.0,
    }


def test_legacy_measurements_reject_known_type_with_wrong_unit() -> None:
    entries = [proto_stubs.MeasurementEntry(type="power_l1", value=1, unit="kW")]
    assert _extract_flat_measurements(entries) == {}


def test_typed_measurements_prefer_id_keep_zero_and_ignore_unknown_id() -> None:
    entries = [
        proto_stubs.MeasurementEntry(
            id=proto_stubs.MeasurementId.MEASUREMENT_ID_POWER_L1,
            type="wrong_legacy_name",
            value=0,
            unit="W",
        ),
        proto_stubs.MeasurementEntry(id=999, type="frequency", value=50, unit="Hz"),
        proto_stubs.MeasurementEntry(
            id=proto_stubs.MeasurementId.MEASUREMENT_ID_FREQUENCY,
            type="frequency",
            value=50,
            unit="W",
        ),
    ]
    assert _extract_flat_measurements(entries) == {"power_l1_w": 0.0}


def test_python_measurement_catalog_covers_every_stable_id_once() -> None:
    stable_ids = {
        value
        for name, value in proto_stubs.MeasurementId.items()
        if name != "MEASUREMENT_ID_UNSPECIFIED"
    }
    assert set(MEASUREMENT_ID_CATALOG) == stable_ids
    assert len(MEASUREMENT_ID_CATALOG) == 22
    assert all(field and unit for field, unit in MEASUREMENT_ID_CATALOG.values())


def test_device_snapshot_converts_partial_status_and_zero_values() -> None:
    available = proto_stubs.SnapshotValueState.SNAPSHOT_VALUE_STATE_AVAILABLE
    temporary = proto_stubs.SnapshotValueState.SNAPSHOT_VALUE_STATE_TEMPORARILY_UNAVAILABLE
    snapshot = proto_stubs.DeviceSnapshot(
        ski="DEVICE",
        local_ski="LOCAL",
        connection=proto_stubs.DeviceStatus(connected=True),
        measurements=[
            proto_stubs.MeasurementEntry(
                id=proto_stubs.MeasurementId.MEASUREMENT_ID_POWER_CONSUMPTION,
                type="power_consumption",
                value=0,
                unit="W",
            )
        ],
        field_states=[
            proto_stubs.SnapshotFieldStatus(
                id=proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_CONNECTED, state=available
            ),
            proto_stubs.SnapshotFieldStatus(
                id=proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_LOCAL_SKI, state=available
            ),
            proto_stubs.SnapshotFieldStatus(id=proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_POWER, state=available),
            proto_stubs.SnapshotFieldStatus(
                id=proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_ENERGY_CONSUMED, state=temporary
            ),
        ],
        capabilities=proto_stubs.DeviceCapabilities(
            ski="DEVICE",
            capabilities=[
                device_service_pb2.DeviceCapability(
                    id=proto_stubs.CapabilityId.CAPABILITY_MONITORING,
                    state=proto_stubs.CapabilityState.CAPABILITY_STATE_AVAILABLE,
                )
            ],
        ),
    )
    observation = _snapshot_observation_from_proto(snapshot, ski_registered=True)
    assert observation.state.connection.connected is True
    assert observation.state.connection.local_ski == "LOCAL"
    assert observation.state.measurements.power_watts == 0
    assert StateField.POWER_WATTS in observation.observed_fields
    assert StateField.ENERGY_CONSUMED_KWH in observation.unavailable_fields
    reduced = DeviceStateStore().dispatch(observation)
    assert reduced.measurements.power_watts == 0
    assert StateField.POWER_WATTS in reduced.fresh_fields


def test_snapshot_field_catalog_covers_every_bridge_owned_state_leaf() -> None:
    assert set(_SNAPSHOT_FIELD_TO_STATE_FIELD.values()) == set(StateField) - {StateField.SKI_REGISTERED}
    assert len(_SNAPSHOT_FIELD_TO_STATE_FIELD) == 34


async def test_aggregate_snapshot_path_uses_register_plus_one_read() -> None:
    snapshot = proto_stubs.DeviceSnapshot(
        ski="DEVICE",
        local_ski="LOCAL",
        connection=proto_stubs.DeviceStatus(connected=False),
        field_states=[
            proto_stubs.SnapshotFieldStatus(
                id=proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_CONNECTED,
                state=proto_stubs.SnapshotValueState.SNAPSHOT_VALUE_STATE_AVAILABLE,
            )
        ],
        capabilities=proto_stubs.DeviceCapabilities(ski="DEVICE"),
    )
    stub = SimpleNamespace(
        RegisterRemoteSKI=AsyncMock(return_value=proto_stubs.Empty()),
        GetDeviceSnapshot=AsyncMock(return_value=snapshot),
    )
    with patch("custom_components.eebus.snapshot.proto_stubs.device_service_stub", return_value=stub):
        result = await async_build_device_snapshot(
            MagicMock(), "DEVICE", ski_registered=False, not_found_streak=0
        )
    assert result.ski_registered is True
    stub.RegisterRemoteSKI.assert_awaited_once()
    stub.GetDeviceSnapshot.assert_awaited_once()


async def test_aggregate_snapshot_not_found_forces_reregistration_after_streak() -> None:
    not_found = AioRpcError(
        grpc.StatusCode.NOT_FOUND, Metadata(), Metadata(), details="device missing"
    )
    stub = SimpleNamespace(
        RegisterRemoteSKI=AsyncMock(return_value=proto_stubs.Empty()),
        GetDeviceSnapshot=AsyncMock(side_effect=not_found),
    )
    with (
        patch("custom_components.eebus.snapshot.proto_stubs.device_service_stub", return_value=stub),
        pytest.raises(grpc.aio.AioRpcError) as raised,
    ):
        await async_build_device_snapshot(
            MagicMock(),
            "DEVICE",
            ski_registered=True,
            not_found_streak=RE_REGISTER_NOT_FOUND_STREAK - 1,
        )
    assert raised.value.code() == grpc.StatusCode.NOT_FOUND
    stub.RegisterRemoteSKI.assert_awaited_once()
    assert stub.GetDeviceSnapshot.await_count == 2


async def test_aggregate_snapshot_retries_after_forced_reregistration() -> None:
    not_found = AioRpcError(
        grpc.StatusCode.NOT_FOUND, Metadata(), Metadata(), details="device missing"
    )
    snapshot = proto_stubs.DeviceSnapshot(
        ski="DEVICE",
        capabilities=proto_stubs.DeviceCapabilities(ski="DEVICE"),
    )
    stub = SimpleNamespace(
        RegisterRemoteSKI=AsyncMock(return_value=proto_stubs.Empty()),
        GetDeviceSnapshot=AsyncMock(side_effect=[not_found, snapshot]),
    )
    with patch(
        "custom_components.eebus.snapshot.proto_stubs.device_service_stub",
        return_value=stub,
    ):
        result = await async_build_device_snapshot(
            MagicMock(),
            "DEVICE",
            ski_registered=True,
            not_found_streak=RE_REGISTER_NOT_FOUND_STREAK - 1,
        )
    assert result.ski_registered is True
    assert result.not_found_streak == 0
    stub.RegisterRemoteSKI.assert_awaited_once()
    assert stub.GetDeviceSnapshot.await_count == 2


async def test_aggregate_snapshot_poller_preserves_not_found_streak_and_resets_registration() -> None:
    not_found = AioRpcError(
        grpc.StatusCode.NOT_FOUND, Metadata(), Metadata(), details="device missing"
    )
    stub = SimpleNamespace(
        RegisterRemoteSKI=AsyncMock(return_value=proto_stubs.Empty()),
        GetDeviceSnapshot=AsyncMock(side_effect=not_found),
    )
    poller = DevicePoller(
        "DEVICE",
        AsyncMock(return_value=MagicMock()),
        DeviceStateStore(),
        supports_feature=lambda feature: feature
        == int(proto_stubs.FeatureId.FEATURE_DEVICE_SNAPSHOT),
    )
    poller._ski_registered = True
    with patch("custom_components.eebus.snapshot.proto_stubs.device_service_stub", return_value=stub):
        for expected_streak in range(1, RE_REGISTER_NOT_FOUND_STREAK):
            with pytest.raises(grpc.aio.AioRpcError):
                await poller.poll()
            poller.reset_after_transport_error(grpc.StatusCode.NOT_FOUND)
            assert poller._not_found_streak == expected_streak
            assert poller._ski_registered is False

        with pytest.raises(grpc.aio.AioRpcError):
            await poller.poll()
        poller.reset_after_transport_error(grpc.StatusCode.NOT_FOUND)

    assert poller._not_found_streak == 0
    assert poller._ski_registered is False
    assert stub.RegisterRemoteSKI.await_count == RE_REGISTER_NOT_FOUND_STREAK


async def test_coordinator_reuses_one_lifelong_write_session() -> None:
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator._device_session = MagicMock()
    coordinator._device_session.write_lpc_limit = AsyncMock(
        return_value=SimpleNamespace(
            status_code=None,
            validation_error=None,
            unimplemented=False,
            error=None,
        )
    )
    coordinator._state_store = DeviceStateStore()
    await coordinator.async_write_lpc_limit(1000.0)
    await coordinator.async_write_lpc_limit(2000.0)
    assert coordinator._device_session.write_lpc_limit.await_count == 2
