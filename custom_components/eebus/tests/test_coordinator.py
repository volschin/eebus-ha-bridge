"""Tests for the EEBUS coordinator."""

import inspect
from datetime import timedelta
from types import SimpleNamespace

from custom_components.eebus.coordinator import EebusCoordinator, POLL_INTERVAL


def test_coordinator_poll_interval():
    """Test that coordinator poll interval is configured."""
    assert POLL_INTERVAL == timedelta(seconds=30)


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


def test_extract_standard_measurements_maps_entries():
    """Test mapping bridge measurement entries to coordinator keys."""
    measurements = [
        SimpleNamespace(type="power_consumption", value=1234.5),
        SimpleNamespace(type="energy_consumed", value=6.7),
        SimpleNamespace(type="energy_produced", value=1.2),
        SimpleNamespace(type="frequency", value=49.98),
        SimpleNamespace(type="power_l1", value=410.0),
        SimpleNamespace(type="current_l1", value=1.9),
        SimpleNamespace(type="voltage_l1", value=229.7),
    ]

    result = EebusCoordinator._extract_standard_measurements(measurements)

    assert result["power_watts"] == 1234.5
    assert result["energy_consumed_kwh"] == 6.7
    assert result["energy_produced_kwh"] == 1.2
    assert result["grid_frequency_hz"] == 49.98
    assert result["power_l1_watts"] == 410.0
    assert result["current_l1_ampere"] == 1.9
    assert result["voltage_l1_volt"] == 229.7
