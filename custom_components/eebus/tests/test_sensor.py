"""Tests for EEBUS sensor entities."""

from unittest.mock import MagicMock

from custom_components.eebus.sensor import (
    EebusFailsafeDurationSensor,
    EebusFailsafeLimitSensor,
    EebusMeasurementDescription,
    EebusMeasurementSensor,
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


def test_failsafe_limit_sensor_value():
    """Test failsafe limit sensor returns correct value from coordinator data."""
    coordinator = MagicMock()
    coordinator.data = {
        "failsafe_limit": {"value_watts": 4200.0, "duration_minimum_seconds": 7200},
        "failsafe_supported": True,
    }
    coordinator.ski = "test-ski-123"

    sensor = EebusFailsafeLimitSensor(coordinator)
    assert sensor.native_value == 4200.0
    assert sensor.native_unit_of_measurement == "W"
    assert sensor.available is True


def test_failsafe_limit_sensor_unavailable_when_unsupported():
    """Test failsafe limit sensor reports unavailable when device lacks support."""
    coordinator = MagicMock()
    coordinator.data = {"failsafe_limit": None, "failsafe_supported": False}
    coordinator.ski = "test-ski-123"

    sensor = EebusFailsafeLimitSensor(coordinator)
    assert sensor.native_value is None
    assert sensor.available is False


def test_failsafe_duration_sensor_value():
    """Test failsafe duration sensor returns correct value from coordinator data."""
    coordinator = MagicMock()
    coordinator.data = {
        "failsafe_limit": {"value_watts": 4200.0, "duration_minimum_seconds": 7200},
        "failsafe_supported": True,
    }
    coordinator.ski = "test-ski-123"

    sensor = EebusFailsafeDurationSensor(coordinator)
    assert sensor.native_value == 7200
    assert sensor.native_unit_of_measurement == "s"


def test_measurement_sensor_vaillant_temperature_value():
    """Generic measurement sensor exposes mapped Vaillant temperatures."""
    coordinator = MagicMock()
    coordinator.data = {"dhw_temperature_c": 48.5, "connected": True}
    coordinator.ski = "test-ski-123"
    description = EebusMeasurementDescription(
        key="dhw_temperature",
        data_key="dhw_temperature_c",
        translation_key="dhw_temperature",
    )

    sensor = EebusMeasurementSensor(coordinator, description)
    assert sensor.native_value == 48.5
