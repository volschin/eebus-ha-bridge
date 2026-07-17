"""Tests for typed EEBUS sensor selectors."""

from unittest.mock import MagicMock

from homeassistant.const import EntityCategory

from custom_components.eebus.models import CapabilityState, FailsafeState
from custom_components.eebus.sensor import (
    EebusMeasurementDescription,
    EebusMeasurementSensor,
    MEASUREMENT_SENSORS,
    STATE_SENSORS,
)
from custom_components.eebus.state import (
    CapabilitiesState,
    ConnectionState,
    DeviceState,
    LPCState,
    MeasurementsState,
    StateField,
)


def _coordinator(state: DeviceState) -> MagicMock:
    coordinator = MagicMock()
    coordinator.data = state
    coordinator.ski = "test-ski-123"
    coordinator.last_update_success = True
    return coordinator


def _sensor(key: str, state: DeviceState) -> EebusMeasurementSensor:
    description = next(item for item in STATE_SENSORS if item.key == key)
    return EebusMeasurementSensor(_coordinator(state), description)


def test_power_sensor_value_and_freshness() -> None:
    fresh = DeviceState(
        connection=ConnectionState(connected=True),
        measurements=MeasurementsState(power_watts=1500.0),
        fresh_fields=frozenset({StateField.POWER_WATTS}),
    )
    sensor = _sensor("power", fresh)
    assert sensor.native_value == 1500.0
    assert sensor.available is True
    assert sensor.native_unit_of_measurement == "W"
    stale = DeviceState(
        connection=ConnectionState(connected=True),
        measurements=MeasurementsState(power_watts=1500.0),
    )
    assert _sensor("power", stale).available is False


def test_failsafe_sensors_use_typed_nested_state() -> None:
    state = DeviceState(
        connection=ConnectionState(connected=True),
        lpc=LPCState(failsafe_limit=FailsafeState(4200.0, 7200)),
        capabilities=CapabilitiesState(failsafe=CapabilityState.AVAILABLE),
        fresh_fields=frozenset({StateField.FAILSAFE_LIMIT}),
    )
    assert _sensor("failsafe_limit", state).native_value == 4200.0
    assert _sensor("failsafe_duration", state).native_value == 7200
    unsupported = DeviceState(
        connection=ConnectionState(connected=True),
        capabilities=CapabilitiesState(failsafe=CapabilityState.UNSUPPORTED),
    )
    assert _sensor("failsafe_limit", unsupported).available is False


def test_measurement_description_uses_typed_state_field() -> None:
    state = DeviceState(
        connection=ConnectionState(connected=True),
        measurements=MeasurementsState(dhw_temperature_c=48.5),
        fresh_fields=frozenset({StateField.DHW_TEMPERATURE_C}),
    )
    description = EebusMeasurementDescription(
        key="dhw_temperature",
        state_field=StateField.DHW_TEMPERATURE_C,
        translation_key="dhw_temperature",
    )
    assert EebusMeasurementSensor(_coordinator(state), description).native_value == 48.5


def _operating_state_sensor(value: str | None) -> EebusMeasurementSensor:
    state = DeviceState(
        connection=ConnectionState(connected=True, device_operating_state=value),
        fresh_fields=(frozenset({StateField.DEVICE_OPERATING_STATE}) if value is not None else frozenset()),
    )
    description = next(item for item in MEASUREMENT_SENSORS if item.key == "device_operating_state")
    return EebusMeasurementSensor(_coordinator(state), description)


def test_device_operating_state_sensor_passes_through_values() -> None:
    sensor = _operating_state_sensor("normalOperation")
    assert sensor.native_value == "normalOperation"
    assert sensor.entity_description.entity_category == EntityCategory.DIAGNOSTIC
    assert sensor.entity_description.entity_registry_enabled_default is True
    assert _operating_state_sensor("futureVendorState").native_value == "futureVendorState"
    assert _operating_state_sensor(None).native_value is None
