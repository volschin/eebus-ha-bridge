"""Tests for EEBUS binary sensor entities."""

from unittest.mock import MagicMock

import pytest

from custom_components.eebus.binary_sensor import (
    HEAT_PUMP_ACTIVE_THRESHOLD_W,
    EebusConnectedSensor,
    EebusHeartbeatOkSensor,
    EebusHeatPumpActiveSensor,
)
from custom_components.eebus.models import HeartbeatState
from custom_components.eebus.state import (
    ConnectionState,
    DeviceState,
    LPCState,
    MeasurementsState,
    StateField,
)


def _sensor(*, connected: bool | None, poll_ok: bool) -> EebusConnectedSensor:
    coordinator = MagicMock()
    coordinator.data = None if connected is None else DeviceState(connection=ConnectionState(connected))
    coordinator.ski = "test-ski"
    coordinator.last_update_success = poll_ok
    return EebusConnectedSensor(coordinator)


def _heartbeat_sensor(*, within_duration: bool | None, poll_ok: bool) -> EebusHeartbeatOkSensor:
    coordinator = MagicMock()
    coordinator.data = DeviceState(
        connection=ConnectionState(connected=True),
        lpc=LPCState(
            heartbeat_status=(
                None if within_duration is None else HeartbeatState(running=True, within_duration=within_duration)
            )
        ),
        fresh_fields=(frozenset({StateField.HEARTBEAT_STATUS}) if within_duration is not None else frozenset()),
    )
    coordinator.ski = "test-ski"
    coordinator.last_update_success = poll_ok
    return EebusHeartbeatOkSensor(coordinator)


def test_available_when_device_disconnected_but_poll_succeeded() -> None:
    sensor = _sensor(connected=False, poll_ok=True)
    assert sensor.available is True
    assert sensor.is_on is False


def test_unavailable_when_poll_failed() -> None:
    sensor = _sensor(connected=True, poll_ok=False)
    assert sensor.available is False


def test_is_on_true_when_connected() -> None:
    assert _sensor(connected=True, poll_ok=True).is_on is True


def test_is_on_none_when_no_data_yet() -> None:
    assert _sensor(connected=None, poll_ok=True).is_on is None


def test_heartbeat_available_with_fresh_connected_state() -> None:
    sensor = _heartbeat_sensor(within_duration=True, poll_ok=True)
    assert sensor.available is True
    assert sensor.is_on is False


def test_heartbeat_unavailable_when_poll_failed() -> None:
    sensor = _heartbeat_sensor(within_duration=True, poll_ok=False)
    assert sensor.available is False


def _heat_pump_sensor(
    *,
    power: float | None,
    poll_ok: bool = True,
    connected: bool = True,
    has_data: bool = True,
) -> EebusHeatPumpActiveSensor:
    coordinator = MagicMock()
    coordinator.data = (
        DeviceState(
            connection=ConnectionState(connected=connected),
            measurements=MeasurementsState(power_watts=power),
            fresh_fields=(frozenset({StateField.POWER_WATTS}) if power is not None else frozenset()),
        )
        if has_data
        else None
    )
    coordinator.ski = "test-ski"
    coordinator.last_update_success = poll_ok
    return EebusHeatPumpActiveSensor(coordinator)


@pytest.mark.parametrize(
    ("power", "expected"),
    [
        (0.0, False),
        (5.0, False),  # Bosch Compress 5800i standby floor
        (25.0, False),  # standby ceiling
        (HEAT_PUMP_ACTIVE_THRESHOLD_W, False),  # threshold itself is not active
        (100.1, True),
        (1500.0, True),
    ],
)
def test_heat_pump_active_thresholds_power(power: float, expected: bool) -> None:
    sensor = _heat_pump_sensor(power=power)
    assert sensor.available is True
    assert sensor.is_on is expected


def test_heat_pump_active_unknown_without_power_reading() -> None:
    sensor = _heat_pump_sensor(power=None)
    assert sensor.available is False
    assert sensor.is_on is None


def test_heat_pump_active_unknown_before_first_update() -> None:
    sensor = _heat_pump_sensor(power=None, has_data=False)
    assert sensor.available is False
    assert sensor.is_on is None


def test_heat_pump_active_unavailable_when_disconnected() -> None:
    assert _heat_pump_sensor(power=1500.0, connected=False).available is False


def test_heat_pump_active_unavailable_when_poll_failed() -> None:
    assert _heat_pump_sensor(power=1500.0, poll_ok=False).available is False
