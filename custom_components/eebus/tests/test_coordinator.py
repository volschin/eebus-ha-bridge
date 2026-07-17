"""Tests for the coordinator facade, poller and stream integration."""

import asyncio
from datetime import timedelta
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock, call, patch

import grpc
import pytest
from grpc.aio import AioRpcError, Metadata
from homeassistant.exceptions import ConfigEntryAuthFailed

from custom_components.eebus import proto_stubs
from custom_components.eebus.coordinator import EebusCoordinator, POLL_INTERVAL
from custom_components.eebus.device_streams import DeviceStreams
from custom_components.eebus.generated.eebus.v1 import (
    device_service_pb2,
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
    RoomHeatingValues,
    SetpointState,
    SystemFunctionState,
    _extract_flat_measurements,
)
from custom_components.eebus.snapshot import (
    DevicePoller,
    SnapshotResult,
    _async_fetch_device_info,
    _async_read_room_heating,
    _poll_read,
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


async def test_ensure_channel_delegates_to_manager() -> None:
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    channel = MagicMock()
    coordinator._channel_manager = MagicMock()
    coordinator._channel_manager.ensure_channel = AsyncMock(return_value=channel)
    assert await coordinator._ensure_channel() is channel


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
    assert store.state.hvac.setpoint is poll_result.value.setpoint
    assert store.state.hvac.system_function is poll_result.value.system_function
    assert store.state.measurements.room_temperature_c == 22.0
    assert StateField.ROOM_HEATING_SETPOINT in store.state.fresh_fields
    assert StateField.ROOM_HEATING_SYSTEM_FUNCTION in store.state.fresh_fields


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
    store, streams, _hass = _streams_with(
        DeviceState(capabilities=CapabilitiesState(dhw=CapabilityState.AVAILABLE))
    )
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
    store = DeviceStateStore(
        initial=DeviceState(measurements=MeasurementsState(power_watts=1.0))
    )

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
        SimpleNamespace(type="power_l1", value=230.0),
        SimpleNamespace(type="frequency", value=50.0),
        SimpleNamespace(type="unknown", value=1.0),
    ]
    assert _extract_flat_measurements(entries) == {
        "power_l1_w": 230.0,
        "frequency_hz": 50.0,
    }


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
