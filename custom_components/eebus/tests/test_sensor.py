"""Tests for EEBUS sensor entities."""

from unittest.mock import MagicMock

from homeassistant.const import EntityCategory

from custom_components.eebus.sensor import (
    EebusMeasurementDescription,
    EebusMeasurementSensor,
    MEASUREMENT_SENSORS,
    STATE_SENSORS,
)


def _state_sensor(coordinator, key):
    """Build the STATE_SENSORS entry with the given key."""
    description = next(item for item in STATE_SENSORS if item.key == key)
    return EebusMeasurementSensor(coordinator, description)


def test_power_sensor_value():
    """Test power sensor returns correct value from coordinator data."""
    coordinator = MagicMock()
    coordinator.data = {"power_watts": 1500.0, "connected": True}
    coordinator.ski = "test-ski-123"

    sensor = _state_sensor(coordinator, "power")
    assert sensor.native_value == 1500.0
    assert sensor.native_unit_of_measurement == "W"


def test_power_sensor_unavailable():
    """Test power sensor returns None when data missing."""
    coordinator = MagicMock()
    coordinator.data = {"power_watts": None, "connected": True}
    coordinator.ski = "test-ski-123"

    sensor = _state_sensor(coordinator, "power")
    assert sensor.native_value is None


def test_failsafe_limit_sensor_value():
    """Test failsafe limit sensor returns correct value from coordinator data."""
    coordinator = MagicMock()
    coordinator.data = {
        "failsafe_limit": {"value_watts": 4200.0, "duration_minimum_seconds": 7200},
        "failsafe_supported": True,
    }
    coordinator.ski = "test-ski-123"

    sensor = _state_sensor(coordinator, "failsafe_limit")
    assert sensor.native_value == 4200.0
    assert sensor.native_unit_of_measurement == "W"
    assert sensor.available is True


def test_failsafe_limit_sensor_unavailable_when_unsupported():
    """Test failsafe limit sensor reports unavailable when device lacks support."""
    coordinator = MagicMock()
    coordinator.data = {"failsafe_limit": None, "failsafe_supported": False}
    coordinator.ski = "test-ski-123"

    sensor = _state_sensor(coordinator, "failsafe_limit")
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

    sensor = _state_sensor(coordinator, "failsafe_duration")
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


def _device_operating_state_sensor(value):
    """Build the device operating state sensor with the supplied value."""
    coordinator = MagicMock()
    coordinator.data = {"device_operating_state": value, "connected": True}
    coordinator.ski = "test-ski-123"
    description = next(
        item for item in MEASUREMENT_SENSORS if item.key == "device_operating_state"
    )
    return EebusMeasurementSensor(coordinator, description)


def test_device_operating_state_sensor_value():
    """Device operating state is an enabled diagnostic sensor."""
    sensor = _device_operating_state_sensor("normalOperation")
    assert sensor.native_value == "normalOperation"
    assert sensor.entity_description.entity_category == EntityCategory.DIAGNOSTIC
    assert sensor.entity_description.entity_registry_enabled_default is True
    assert sensor.entity_description.device_class is None


def test_device_operating_state_sensor_unavailable():
    """Missing device operating state produces no native value."""
    assert _device_operating_state_sensor(None).native_value is None


def test_device_operating_state_sensor_passes_through_unknown_value():
    """Unknown future device states remain visible as their raw string."""
    assert (
        _device_operating_state_sensor("futureVendorState").native_value
        == "futureVendorState"
    )
