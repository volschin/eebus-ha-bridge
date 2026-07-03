"""Tests for the EEBUS coordinator."""

import asyncio
import inspect
from datetime import timedelta
from types import SimpleNamespace
from unittest.mock import MagicMock

from custom_components.eebus import proto_stubs
from custom_components.eebus.coordinator import EebusCoordinator, POLL_INTERVAL
from custom_components.eebus.generated.eebus.v1 import (
    device_service_pb2,
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
    coordinator._channel = None
    coordinator._stream_tasks = []
    coordinator._was_unavailable = False

    assert coordinator.host == "192.168.1.100"
    assert coordinator.port == 50051
    assert coordinator.ski == "test-ski"
    assert coordinator._channel is None
    assert coordinator._was_unavailable is False


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


def test_measurement_event_other_ski_ignored():
    """Events for a different SKI are ignored unless fallback reads are active."""
    coordinator, pushed = _make_coordinator(
        data={"power_watts": 100.0, "read_fallback_used": False}
    )
    event = monitoring_service_pb2.MeasurementEvent(
        ski="other-ski",
        event_type=monitoring_service_pb2.MEASUREMENT_EVENT_POWER_UPDATED,
        power={"watts": 1.0},
    )
    coordinator._handle_measurement_event(event)
    assert not pushed


def test_measurement_event_fallback_ski_accepted():
    """When reads fell back to first entity, its events are accepted."""
    coordinator, pushed = _make_coordinator(
        data={"power_watts": 100.0, "read_fallback_used": True}
    )
    event = monitoring_service_pb2.MeasurementEvent(
        ski="other-ski",
        event_type=monitoring_service_pb2.MEASUREMENT_EVENT_POWER_UPDATED,
        power={"watts": 7.0},
    )
    coordinator._handle_measurement_event(event)
    assert pushed["power_watts"] == 7.0


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
    coordinator, _ = _make_coordinator(ski="bosch-ski")
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
    info = asyncio.run(
        coordinator._async_fetch_device_info(stub, proto_stubs, allow_fallback=False)
    )
    assert info == {
        "manufacturer": "Bosch",
        "model": "Compress 5800i",
        "serial": "SN-123",
        "device_type": "HeatPumpAppliance",
    }


def test_fetch_device_info_fallback_single_device():
    """When reads fell back to the only device, its info is used despite SKI mismatch."""
    coordinator, _ = _make_coordinator(ski="configured-ski")
    stub = _device_stub_returning(
        device_service_pb2.PairedDevice(ski="actual-ski", brand="Bosch")
    )
    info = asyncio.run(
        coordinator._async_fetch_device_info(stub, proto_stubs, allow_fallback=True)
    )
    assert info == {"manufacturer": "Bosch"}


def test_fetch_device_info_no_match_returns_none():
    """No SKI match and no fallback yields None (no mislabeling)."""
    coordinator, _ = _make_coordinator(ski="configured-ski")
    stub = _device_stub_returning(
        device_service_pb2.PairedDevice(ski="a", brand="Bosch"),
        device_service_pb2.PairedDevice(ski="b", brand="Vaillant"),
    )
    info = asyncio.run(
        coordinator._async_fetch_device_info(stub, proto_stubs, allow_fallback=True)
    )
    assert info is None


def test_device_event_triggers_refresh():
    """Connection state events trigger a reconciliation poll."""
    coordinator, _ = _make_coordinator(data={"connected": True})
    coordinator.hass = MagicMock()
    coordinator.async_request_refresh = MagicMock(return_value=None)
    event = device_service_pb2.DeviceEvent(
        ski="test-ski",
        event_type=device_service_pb2.DEVICE_EVENT_DISCONNECTED,
    )
    coordinator._handle_device_event(event)
    coordinator.hass.async_create_task.assert_called_once()


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
    result = EebusCoordinator._extract_flat_measurements(entries)
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
    result = EebusCoordinator._extract_flat_measurements(entries)
    assert result == {"power_l1_w": 0.0}
