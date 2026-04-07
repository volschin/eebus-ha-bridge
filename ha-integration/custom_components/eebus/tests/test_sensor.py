"""Tests for EEBUS sensor entities."""

from unittest.mock import MagicMock

import pytest

from custom_components.eebus.sensor import (
    EebusPowerSensor,
)


def test_power_sensor_value():
    """Test power sensor returns correct value from coordinator data."""
    coordinator = MagicMock()
    coordinator.data = {"power_watts": 1500.0, "connected": True}
    coordinator.ski = "test-ski-123"

    sensor = EebusPowerSensor(coordinator)
    assert sensor.native_value == 1500.0
    assert sensor.native_unit_of_measurement == "W"


def test_power_sensor_unavailable():
    """Test power sensor returns None when data missing."""
    coordinator = MagicMock()
    coordinator.data = {"power_watts": None, "connected": True}
    coordinator.ski = "test-ski-123"

    sensor = EebusPowerSensor(coordinator)
    assert sensor.native_value is None
