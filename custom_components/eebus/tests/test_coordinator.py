"""Tests for the EEBUS coordinator."""

import asyncio
import inspect
from contextlib import ExitStack, contextmanager
from datetime import timedelta
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock, call, patch

import grpc
import pytest
from grpc.aio import AioRpcError, Metadata
from homeassistant.exceptions import ConfigEntryAuthFailed

from custom_components.eebus import proto_stubs
from custom_components.eebus.coordinator import EebusCoordinator, POLL_INTERVAL
from custom_components.eebus.models import CapabilityState, _extract_flat_measurements
from custom_components.eebus.snapshot import (
    SnapshotSupport,
    _async_fetch_device_info,
    _async_read_device_diagnostics,
    _next_capability_state,
    _poll_read,
)
from custom_components.eebus.generated.eebus.v1 import (
    device_service_pb2,
    hvac_service_pb2,
    lpc_service_pb2,
    monitoring_service_pb2,
)


def _device_stub_returning(*devices):
    """Build a stub whose ListPairedDevices async-returns the given devices."""
    response = device_service_pb2.ListPairedDevicesResponse(devices=list(devices))

    async def _list(_request, timeout=None):
        return response

    stub = MagicMock()
    stub.ListPairedDevices = _list
    return stub


def _poll_stubs(room_heating_read):
    """Build complete service stubs for one polling snapshot."""
    unsupported = AioRpcError(
        grpc.StatusCode.UNIMPLEMENTED,
        Metadata(),
        Metadata(),
        details="use case disabled",
    )
    device_stub = SimpleNamespace(
        GetStatus=AsyncMock(
            return_value=device_service_pb2.ServiceStatus(
                running=True, local_ski="bridge-ski"
            )
        ),
        GetDeviceStatus=AsyncMock(
            return_value=device_service_pb2.DeviceStatus(connected=True)
        ),
        ListPairedDevices=AsyncMock(
            return_value=device_service_pb2.ListPairedDevicesResponse()
        ),
        RegisterRemoteSKI=AsyncMock(return_value=proto_stubs.Empty()),
    )
    monitoring_stub = SimpleNamespace(
        GetPowerConsumption=AsyncMock(
            return_value=proto_stubs.PowerMeasurement(watts=1234.0)
        ),
        GetMeasurements=AsyncMock(
            return_value=monitoring_service_pb2.MeasurementList()
        ),
        GetEnergyConsumed=AsyncMock(
            return_value=monitoring_service_pb2.EnergyMeasurement(
                kilowatt_hours=42.0
            )
        ),
        GetDeviceDiagnostics=AsyncMock(
            return_value=proto_stubs.DeviceDiagnosticsData(
                operating_state="normalOperation"
            )
        ),
    )
    lpc_stub = SimpleNamespace(
        GetConsumptionLimit=AsyncMock(
            return_value=proto_stubs.LoadLimit(
                value_watts=4200.0, is_active=True, is_changeable=True
            )
        ),
        GetFailsafeLimit=AsyncMock(
            return_value=lpc_service_pb2.FailsafeLimit(
                value_watts=3000.0, duration_minimum_seconds=7200
            )
        ),
        GetHeartbeatStatus=AsyncMock(
            return_value=lpc_service_pb2.HeartbeatStatus(
                running=True, within_duration=True
            )
        ),
    )
    return {
        "device_service_stub": device_stub,
        "monitoring_service_stub": monitoring_stub,
        "lpc_service_stub": lpc_stub,
        "ohpcf_service_stub": SimpleNamespace(
            GetCompressorFlexibility=AsyncMock(side_effect=unsupported)
        ),
        "dhw_service_stub": SimpleNamespace(
            GetDHWSetpoint=AsyncMock(side_effect=unsupported),
            GetDHWSystemFunction=AsyncMock(side_effect=unsupported),
        ),
        "hvac_service_stub": SimpleNamespace(GetRoomHeating=room_heating_read),
    }


def _poll_coordinator():
    """Build a coordinator skeleton with all snapshot-owned state initialized."""
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator.host = "bridge.local"
    coordinator.port = 50051
    coordinator.ski = "device-ski"
    coordinator.data = {"room_temperature_c": 18.0}
    coordinator._channel_manager = MagicMock()
    coordinator._channel_manager.invalidate = AsyncMock()
    coordinator._ensure_channel = AsyncMock(return_value=MagicMock())
    coordinator._was_unavailable = False
    coordinator._lpc_supported = CapabilityState.UNKNOWN
    coordinator._failsafe_supported = CapabilityState.UNKNOWN
    coordinator._heartbeat_supported = CapabilityState.UNKNOWN
    coordinator._ohpcf_supported = CapabilityState.UNKNOWN
    coordinator._dhw_supported = CapabilityState.UNKNOWN
    coordinator._dhw_sysfn_supported = CapabilityState.UNKNOWN
    coordinator._room_heating_supported = CapabilityState.UNKNOWN
    coordinator._ski_registered = True
    coordinator._not_found_streak = 0
    return coordinator


@contextmanager
def _patch_poll_stubs(stubs):
    """Patch every snapshot stub factory with the supplied fake service."""
    with ExitStack() as stack:
        for factory, stub in stubs.items():
            stack.enter_context(patch.object(proto_stubs, factory, return_value=stub))
        yield


def test_coordinator_poll_interval():
    """Test that polling is demoted to slow reconciliation (push is primary)."""
    assert POLL_INTERVAL == timedelta(minutes=5)


def test_coordinator_attributes():
    """Test that coordinator class stores expected connection param names."""
    sig = inspect.signature(EebusCoordinator.__init__)
    params = list(sig.parameters.keys())
    assert "host" in params
    assert "port" in params
    assert "ski" in params


def test_coordinator_init():
    """Test that coordinator stores connection params without calling HA internals."""
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator.host = "192.168.1.100"
    coordinator.port = 50051
    coordinator.ski = "test-ski"
    coordinator._channel_manager = MagicMock()
    coordinator._stream_manager = MagicMock()
    coordinator._was_unavailable = False

    assert coordinator.host == "192.168.1.100"
    assert coordinator.port == 50051
    assert coordinator.ski == "test-ski"
    assert coordinator._channel_manager is not None
    assert coordinator._stream_manager is not None
    assert coordinator._was_unavailable is False


async def test_unauthenticated_poll_starts_reauthentication():
    """An invalid bridge token is surfaced as a config-entry auth failure."""
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator._channel_manager = MagicMock()
    coordinator._channel_manager.invalidate = AsyncMock()
    coordinator.ski = "test-ski"
    coordinator._not_found_streak = 0
    coordinator._was_unavailable = False
    coordinator._lpc_supported = CapabilityState.UNKNOWN
    coordinator._failsafe_supported = CapabilityState.UNKNOWN
    coordinator._heartbeat_supported = CapabilityState.UNKNOWN
    coordinator._ohpcf_supported = CapabilityState.UNKNOWN
    coordinator._dhw_supported = CapabilityState.UNKNOWN
    coordinator._dhw_sysfn_supported = CapabilityState.UNKNOWN
    coordinator._room_heating_supported = CapabilityState.UNKNOWN
    coordinator._ski_registered = False
    coordinator._ensure_channel = AsyncMock(return_value=MagicMock())
    stub = MagicMock()
    stub.GetStatus = AsyncMock(
        side_effect=AioRpcError(
            grpc.StatusCode.UNAUTHENTICATED,
            Metadata(),
            Metadata(),
            details="valid bearer token required",
        )
    )

    with patch.object(proto_stubs, "device_service_stub", return_value=stub):
        with pytest.raises(ConfigEntryAuthFailed):
            await coordinator._async_update_data()

    coordinator._channel_manager.invalidate.assert_awaited_once_with()


async def test_poll_returns_room_temperature_without_mid_poll_push():
    """Room-heating temperature is part of the atomic poll snapshot."""
    room_temperature = 21.5
    room_read = AsyncMock(
        return_value=hvac_service_pb2.RoomHeatingState(
            current_temperature_celsius=room_temperature
        )
    )
    coordinator = _poll_coordinator()
    coordinator._push_data = MagicMock()

    with _patch_poll_stubs(_poll_stubs(room_read)):
        snapshot = await coordinator._async_update_data()

    assert snapshot["room_temperature_c"] == room_temperature
    assert coordinator.data == {"room_temperature_c": 18.0}
    coordinator._push_data.assert_not_called()


async def test_poll_connected_uses_device_status_independent_of_bridge_status():
    """Snapshot connectivity comes from the remote device, not bridge liveness."""
    stubs = _poll_stubs(AsyncMock(return_value=hvac_service_pb2.RoomHeatingState()))
    stubs["device_service_stub"].GetStatus.return_value = device_service_pb2.ServiceStatus(
        running=False, local_ski="bridge-ski"
    )
    stubs["device_service_stub"].GetDeviceStatus.return_value = (
        device_service_pb2.DeviceStatus(connected=True)
    )
    coordinator = _poll_coordinator()

    with _patch_poll_stubs(stubs):
        snapshot = await coordinator._async_update_data()

    assert snapshot["connected"] is True
    stubs["device_service_stub"].GetDeviceStatus.assert_awaited_once()


async def test_poll_applies_support_flags_only_after_all_reads_complete():
    """Completed reads cannot mutate coordinator support flags while a peer read is pending."""
    room_read_started = asyncio.Event()
    finish_room_read = asyncio.Event()

    async def room_read(_request, timeout=None):
        room_read_started.set()
        await finish_room_read.wait()
        return hvac_service_pb2.RoomHeatingState(current_temperature_celsius=20.0)

    coordinator = _poll_coordinator()
    with _patch_poll_stubs(_poll_stubs(room_read)):
        poll_task = asyncio.create_task(coordinator._async_update_data())
        await room_read_started.wait()
        await asyncio.sleep(0)

        assert poll_task.done() is False
        assert coordinator._lpc_supported == CapabilityState.UNKNOWN
        assert coordinator._failsafe_supported == CapabilityState.UNKNOWN
        assert coordinator._heartbeat_supported == CapabilityState.UNKNOWN
        assert coordinator._ohpcf_supported == CapabilityState.UNKNOWN
        assert coordinator._dhw_supported == CapabilityState.UNKNOWN
        assert coordinator._dhw_sysfn_supported == CapabilityState.UNKNOWN
        assert coordinator._room_heating_supported == CapabilityState.UNKNOWN

        finish_room_read.set()
        await poll_task

    assert coordinator._lpc_supported == CapabilityState.AVAILABLE
    assert coordinator._failsafe_supported == CapabilityState.AVAILABLE
    assert coordinator._heartbeat_supported == CapabilityState.AVAILABLE
    assert coordinator._ohpcf_supported == CapabilityState.UNSUPPORTED
    assert coordinator._dhw_supported == CapabilityState.UNSUPPORTED
    assert coordinator._dhw_sysfn_supported == CapabilityState.UNSUPPORTED
    assert coordinator._room_heating_supported == CapabilityState.AVAILABLE


def test_capability_state_defaults_to_unknown() -> None:
    """An unattempted capability is distinct from every failed-call state."""
    support = SnapshotSupport()

    assert support.lpc == CapabilityState.UNKNOWN
    assert support.dhw == CapabilityState.UNKNOWN
    assert support.room_heating == CapabilityState.UNKNOWN


@pytest.mark.parametrize(
    ("status", "expected"),
    [
        (None, CapabilityState.AVAILABLE),
        (grpc.StatusCode.UNIMPLEMENTED, CapabilityState.UNSUPPORTED),
        (grpc.StatusCode.NOT_FOUND, CapabilityState.TEMPORARILY_UNAVAILABLE),
        (grpc.StatusCode.UNAVAILABLE, CapabilityState.TEMPORARILY_UNAVAILABLE),
    ],
)
def test_capability_state_transitions_for_rpc_outcomes(status, expected) -> None:
    """Success and classified gRPC failures have one shared transition rule."""
    assert _next_capability_state(CapabilityState.UNKNOWN, status) == expected


def test_unrelated_capability_error_keeps_current_state() -> None:
    """Unexpected failures do not invent a new capability classification."""
    assert (
        _next_capability_state(CapabilityState.AVAILABLE, grpc.StatusCode.INTERNAL)
        == CapabilityState.AVAILABLE
    )


async def test_ensure_channel_delegates_to_channel_manager():
    """The coordinator keeps its patchable channel-acquisition seam."""
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    channel = MagicMock()
    coordinator._channel_manager = MagicMock()
    coordinator._channel_manager.ensure_channel = AsyncMock(return_value=channel)

    assert await coordinator._ensure_channel() is channel
    coordinator._channel_manager.ensure_channel.assert_awaited_once_with()


def test_start_streams_delegates_all_consumers_to_stream_manager():
    """The coordinator supplies all six event consumers to StreamManager."""
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator.ski = "test-ski"
    coordinator._stream_manager = MagicMock()

    coordinator.async_start_streams()

    coordinator._stream_manager.start.assert_called_once()
    streams, task_name_prefix = coordinator._stream_manager.start.call_args.args
    assert list(streams) == [
        "device_events",
        "lpc_events",
        "measurements",
        "dhw_events",
        "dhw_sysfn_events",
        "room_heating_events",
    ]
    assert task_name_prefix == "eebus_{name}_test-ski"


async def test_shutdown_stops_streams_before_closing_channel():
    """Shutdown fully stops stream tasks before closing the shared channel."""
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    lifecycle = MagicMock()
    coordinator._provider_manager = MagicMock()
    coordinator._provider_manager.async_stop = AsyncMock()
    coordinator._stream_manager = MagicMock()
    coordinator._stream_manager.stop = AsyncMock()
    coordinator._channel_manager = MagicMock()
    coordinator._channel_manager.close = AsyncMock()
    lifecycle.attach_mock(coordinator._provider_manager.async_stop, "stop_providers")
    lifecycle.attach_mock(coordinator._stream_manager.stop, "stop_streams")
    lifecycle.attach_mock(coordinator._channel_manager.close, "close_channel")

    await coordinator.async_shutdown()

    assert lifecycle.mock_calls == [
        call.stop_providers(),
        call.stop_streams(),
        call.close_channel(),
    ]


def _make_coordinator(ski="test-ski", data=None):
    """Build a coordinator skeleton capturing pushed data updates."""
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator.ski = ski
    coordinator.data = data
    pushed = {}
    coordinator.async_set_updated_data = pushed.update
    return coordinator, pushed


def test_measurement_power_event_pushes_data():
    """Power update event refreshes power_watts via push."""
    coordinator, pushed = _make_coordinator(data={"power_watts": 100.0})
    event = monitoring_service_pb2.MeasurementEvent(
        ski="test-ski",
        event_type=monitoring_service_pb2.MEASUREMENT_EVENT_POWER_UPDATED,
        power={"watts": 1234.5},
    )
    coordinator._handle_measurement_event(event)
    assert pushed["power_watts"] == 1234.5


def test_measurement_energy_event_pushes_data():
    """Energy update event refreshes energy_consumed_kwh via push."""
    coordinator, pushed = _make_coordinator(data={"energy_consumed_kwh": 1.0})
    event = monitoring_service_pb2.MeasurementEvent(
        ski="test-ski",
        event_type=monitoring_service_pb2.MEASUREMENT_EVENT_ENERGY_UPDATED,
        energy={"kilowatt_hours": 42.0},
    )
    coordinator._handle_measurement_event(event)
    assert pushed["energy_consumed_kwh"] == 42.0


def test_device_operating_state_event_pushes_data():
    """DeviceDiagnosis stream events update the diagnostic sensor directly."""
    coordinator, pushed = _make_coordinator(
        data={"device_operating_state": "standby"}
    )
    event = monitoring_service_pb2.MeasurementEvent(
        ski="test-ski",
        event_type=(
            monitoring_service_pb2.MEASUREMENT_EVENT_DEVICE_OPERATING_STATE_UPDATED
        ),
        device_diagnostics=proto_stubs.DeviceDiagnosticsData(
            operating_state="futureVendorState"
        ),
    )

    coordinator._handle_measurement_event(event)

    assert pushed["device_operating_state"] == "futureVendorState"


async def test_read_device_diagnostics_returns_operating_state():
    """Polling returns the bridge operating-state string unchanged."""

    class MonitoringStub:
        async def GetDeviceDiagnostics(self, _request, timeout=None):
            return proto_stubs.DeviceDiagnosticsData(operating_state="normalOperation")

    with patch.object(proto_stubs, "monitoring_service_stub", return_value=MonitoringStub()):
        result = await _async_read_device_diagnostics(
            MagicMock(), proto_stubs.DeviceRequest(ski="test-ski"), "test-ski"
        )

    assert result == "normalOperation"


async def test_read_device_diagnostics_unavailable_returns_none():
    """Missing diagnosis data remains unavailable without failing the poll."""

    class MonitoringStub:
        async def GetDeviceDiagnostics(self, _request, timeout=None):
            raise AioRpcError(
                grpc.StatusCode.NOT_FOUND,
                Metadata(),
                Metadata(),
                details="device operating state unavailable",
            )

    with patch.object(proto_stubs, "monitoring_service_stub", return_value=MonitoringStub()):
        result = await _async_read_device_diagnostics(
            MagicMock(), proto_stubs.DeviceRequest(ski="test-ski"), "test-ski"
        )

    assert result is None


def test_measurement_event_other_ski_ignored():
    """Events for a different SKI are always ignored."""
    coordinator, pushed = _make_coordinator(data={"power_watts": 100.0})
    event = monitoring_service_pb2.MeasurementEvent(
        ski="other-ski",
        event_type=monitoring_service_pb2.MEASUREMENT_EVENT_POWER_UPDATED,
        power={"watts": 1.0},
    )
    coordinator._handle_measurement_event(event)
    assert not pushed


def test_measurement_event_matches_canonicalized_ski():
    """Canonical bridge events match a differently formatted configured SKI."""
    coordinator, pushed = _make_coordinator(
        ski="ab:cd-ef", data={"power_watts": 100.0}
    )
    event = monitoring_service_pb2.MeasurementEvent(
        ski="ABCDEF",
        event_type=monitoring_service_pb2.MEASUREMENT_EVENT_POWER_UPDATED,
        power={"watts": 1234.5},
    )

    coordinator._handle_measurement_event(event)

    assert pushed["power_watts"] == 1234.5


def test_measurement_event_empty_ski_ignored():
    """An empty event SKI is never treated as a wildcard."""
    coordinator, pushed = _make_coordinator(data={"power_watts": 100.0})
    event = monitoring_service_pb2.MeasurementEvent(
        ski="",
        event_type=monitoring_service_pb2.MEASUREMENT_EVENT_POWER_UPDATED,
        power={"watts": 7.0},
    )
    coordinator._handle_measurement_event(event)
    assert not pushed


def test_measurement_event_before_first_poll_ignored():
    """Events arriving before the first successful poll are dropped."""
    coordinator, pushed = _make_coordinator(data=None)
    event = monitoring_service_pb2.MeasurementEvent(
        ski="test-ski",
        event_type=monitoring_service_pb2.MEASUREMENT_EVENT_POWER_UPDATED,
        power={"watts": 7.0},
    )
    coordinator._handle_measurement_event(event)
    assert not pushed


def test_lpc_limit_event_pushes_data():
    """Limit update event refreshes consumption_limit via push."""
    coordinator, pushed = _make_coordinator(data={"consumption_limit": None})
    event = lpc_service_pb2.LPCEvent(
        ski="test-ski",
        event_type=lpc_service_pb2.LPC_EVENT_LIMIT_UPDATED,
        limit_update={"value_watts": 4200.0, "is_active": True, "is_changeable": True},
    )
    coordinator._handle_lpc_event(event)
    assert pushed["consumption_limit"] == {
        "value_watts": 4200.0,
        "is_active": True,
        "is_changeable": True,
    }


def test_lpc_failsafe_event_pushes_data():
    """Failsafe update event refreshes failsafe_limit via push."""
    coordinator, pushed = _make_coordinator(data={"failsafe_limit": None})
    event = lpc_service_pb2.LPCEvent(
        ski="test-ski",
        event_type=lpc_service_pb2.LPC_EVENT_FAILSAFE_UPDATED,
        failsafe_update={"value_watts": 3000.0, "duration_minimum_seconds": 7200},
    )
    coordinator._handle_lpc_event(event)
    assert pushed["failsafe_limit"] == {
        "value_watts": 3000.0,
        "duration_minimum_seconds": 7200,
    }


def test_lpc_limit_event_without_payload_refreshes():
    """LIMIT_UPDATED without a payload must reconcile via poll, never zero the limit."""
    coordinator, pushed = _make_coordinator(
        data={"consumption_limit": {"value_watts": 4200.0, "is_active": True}}
    )
    coordinator.hass = MagicMock()
    coordinator.async_request_refresh = MagicMock(return_value=None)
    event = lpc_service_pb2.LPCEvent(
        ski="test-ski",
        event_type=lpc_service_pb2.LPC_EVENT_LIMIT_UPDATED,
    )
    coordinator._handle_lpc_event(event)
    assert not pushed  # no zeroing push
    coordinator.hass.async_create_task.assert_called_once()


def test_measurement_power_event_without_payload_refreshes():
    """POWER_UPDATED without a payload must reconcile via poll instead of dropping."""
    coordinator, pushed = _make_coordinator(data={"power_watts": 100.0})
    coordinator.hass = MagicMock()
    coordinator.async_request_refresh = MagicMock(return_value=None)
    event = monitoring_service_pb2.MeasurementEvent(
        ski="test-ski",
        event_type=monitoring_service_pb2.MEASUREMENT_EVENT_POWER_UPDATED,
    )
    coordinator._handle_measurement_event(event)
    assert not pushed
    coordinator.hass.async_create_task.assert_called_once()


def test_fetch_device_info_uses_matching_ski():
    """Real brand/model/serial for the configured SKI is surfaced (issue #28)."""
    stub = _device_stub_returning(
        device_service_pb2.PairedDevice(ski="other", brand="Vaillant", model="VR940f"),
        device_service_pb2.PairedDevice(
            ski="bosch-ski",
            brand="Bosch",
            model="Compress 5800i",
            serial="SN-123",
            device_type="HeatPumpAppliance",
        ),
    )
    info = asyncio.run(_async_fetch_device_info(stub, "bosch-ski"))
    assert info == {
        "manufacturer": "Bosch",
        "model": "Compress 5800i",
        "serial": "SN-123",
        "device_type": "HeatPumpAppliance",
    }


def test_fetch_device_info_single_mismatched_device_returns_none():
    """A sole mismatched device is not used as metadata fallback."""
    stub = _device_stub_returning(
        device_service_pb2.PairedDevice(ski="actual-ski", brand="Bosch")
    )
    info = asyncio.run(_async_fetch_device_info(stub, "configured-ski"))
    assert info is None


def test_fetch_device_info_no_match_returns_none():
    """No SKI match yields None (no cross-device mislabeling)."""
    stub = _device_stub_returning(
        device_service_pb2.PairedDevice(ski="a", brand="Bosch"),
        device_service_pb2.PairedDevice(ski="b", brand="Vaillant"),
    )
    info = asyncio.run(_async_fetch_device_info(stub, "configured-ski"))
    assert info is None


async def test_two_coordinators_isolate_device_info_and_events():
    """Each config entry surfaces only metadata and events for its own SKI."""
    coordinator_a, pushed_a = _make_coordinator(
        ski="ski-a", data={"power_watts": 100.0}
    )
    coordinator_b, pushed_b = _make_coordinator(
        ski="ski-b", data={"power_watts": 200.0}
    )
    stub = _device_stub_returning(
        device_service_pb2.PairedDevice(ski="ski-b", brand="Brand B"),
        device_service_pb2.PairedDevice(ski="ski-a", brand="Brand A"),
    )

    info_a, info_b = await asyncio.gather(
        _async_fetch_device_info(stub, coordinator_a.ski),
        _async_fetch_device_info(stub, coordinator_b.ski),
    )
    assert info_a == {"manufacturer": "Brand A"}
    assert info_b == {"manufacturer": "Brand B"}

    event_b = monitoring_service_pb2.MeasurementEvent(
        ski="ski-b",
        event_type=monitoring_service_pb2.MEASUREMENT_EVENT_POWER_UPDATED,
        power={"watts": 250.0},
    )
    coordinator_a._handle_measurement_event(event_b)
    coordinator_b._handle_measurement_event(event_b)

    assert not pushed_a
    assert pushed_b["power_watts"] == 250.0


async def test_poll_read_not_found_does_not_retry_with_empty_ski():
    """A failed device-scoped read is attempted exactly once."""
    call = AsyncMock(
        side_effect=AioRpcError(
            grpc.StatusCode.NOT_FOUND,
            Metadata(),
            Metadata(),
            details="not found",
        )
    )
    request = proto_stubs.DeviceRequest(ski="ski-a")
    result = await _poll_read("power", call, request, "ski-a")

    assert result.value is None
    assert result.saw_not_found is True
    call.assert_awaited_once_with(request, timeout=10)


async def test_device_disconnect_event_refreshes_disconnected_state():
    """A disconnect event reconciles to the registry-backed disconnected state."""
    stubs = _poll_stubs(AsyncMock(return_value=hvac_service_pb2.RoomHeatingState()))
    stubs["device_service_stub"].GetDeviceStatus.return_value = (
        device_service_pb2.DeviceStatus(connected=False)
    )
    coordinator = _poll_coordinator()
    coordinator.data = {"connected": True}
    tasks: list[asyncio.Task] = []

    async def request_refresh():
        coordinator.data = await coordinator._async_update_data()

    def create_task(coro):
        task = asyncio.create_task(coro)
        tasks.append(task)
        return task

    coordinator.async_request_refresh = request_refresh
    coordinator.hass = SimpleNamespace(async_create_task=create_task)
    event = device_service_pb2.DeviceEvent(
        ski="device-ski",
        event_type=device_service_pb2.DEVICE_EVENT_DISCONNECTED,
    )

    with _patch_poll_stubs(stubs):
        coordinator._handle_device_event(event)
        await tasks[0]

    assert coordinator.data["connected"] is False


def _entry(measurement_type, value):
    """Build a duck-typed GetMeasurements entry (type/value attributes)."""
    return SimpleNamespace(type=measurement_type, value=value)


def test_extract_flat_measurements_maps_types():
    """Per-phase / grid / produced-energy entries map to coordinator keys."""
    entries = [
        _entry("power_l1", 230.0),
        _entry("current_l2", 4.5),
        _entry("voltage_l3", 231.2),
        _entry("frequency", 50.0),
        _entry("energy_produced", 12.3),
        _entry("dhw_temperature", 48.5),
        _entry("outdoor_temperature", 7.0),
        _entry("compressor_power", 900.0),
        # Unrelated / scoped types are ignored by the flat extractor.
        _entry("energy_consumed", 99.0),
    ]
    result = _extract_flat_measurements(entries)
    assert result == {
        "power_l1_w": 230.0,
        "current_l2_a": 4.5,
        "voltage_l3_v": 231.2,
        "frequency_hz": 50.0,
        "energy_produced_kwh": 12.3,
        "dhw_temperature_c": 48.5,
        "outdoor_temperature_c": 7.0,
        "compressor_power_w": 900.0,
    }


def test_extract_flat_measurements_ignores_blank_and_missing():
    """Entries without a type are skipped; a value of 0.0 is kept."""
    entries = [
        _entry("", 1.0),
        _entry("voltage_l1", None),
        _entry("power_l1", 0.0),
    ]
    result = _extract_flat_measurements(entries)
    assert result == {"power_l1_w": 0.0}


def test_dhw_temperature_measurement_event_pushes_value():
    """MDT stream events update the DHW actual-temperature sensor directly."""
    coordinator, _ = _make_coordinator(data={"connected": True})
    pushed = {}
    coordinator.async_set_updated_data = pushed.update
    event = monitoring_service_pb2.MeasurementEvent(
        ski="test-ski",
        event_type=(
            monitoring_service_pb2.MEASUREMENT_EVENT_DHW_TEMPERATURE_UPDATED
        ),
        measurement=proto_stubs.MeasurementEntry(
            type="dhw_temperature", value=49.5, unit="degC"
        ),
    )

    coordinator._handle_measurement_event(event)

    assert pushed["dhw_temperature_c"] == 49.5


def test_room_temperature_measurement_event_pushes_value():
    """MRT stream events update the room-temperature sensor directly."""
    coordinator, _ = _make_coordinator(data={"connected": True})
    pushed = {}
    coordinator.async_set_updated_data = pushed.update
    event = monitoring_service_pb2.MeasurementEvent(
        ski="test-ski",
        event_type=monitoring_service_pb2.MEASUREMENT_EVENT_ROOM_TEMPERATURE_UPDATED,
        measurement=proto_stubs.MeasurementEntry(
            type="room_temperature", value=21.25, unit="degC"
        ),
    )

    coordinator._handle_measurement_event(event)

    assert pushed["room_temperature_c"] == 21.25


def test_outdoor_temperature_measurement_event_pushes_value():
    """MOT stream events update the outdoor-temperature sensor directly."""
    coordinator, _ = _make_coordinator(data={"connected": True})
    pushed = {}
    coordinator.async_set_updated_data = pushed.update
    event = monitoring_service_pb2.MeasurementEvent(
        ski="test-ski",
        event_type=(
            monitoring_service_pb2.MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_UPDATED
        ),
        measurement=proto_stubs.MeasurementEntry(
            type="outdoor_temperature", value=7.5, unit="degC"
        ),
    )

    coordinator._handle_measurement_event(event)

    assert pushed["outdoor_temperature_c"] == 7.5


def test_temperature_support_event_requests_refresh():
    """MRT and MOT support changes reconcile state through polling."""
    for event_type in (
        monitoring_service_pb2.MEASUREMENT_EVENT_ROOM_TEMPERATURE_SUPPORT_UPDATED,
        monitoring_service_pb2.MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_SUPPORT_UPDATED,
    ):
        coordinator, _ = _make_coordinator(data={"connected": True})
        coordinator.hass = MagicMock()
        coordinator.async_request_refresh = MagicMock(return_value=None)
        event = monitoring_service_pb2.MeasurementEvent(
            ski="test-ski", event_type=event_type
        )

        coordinator._handle_measurement_event(event)

        coordinator.hass.async_create_task.assert_called_once()
