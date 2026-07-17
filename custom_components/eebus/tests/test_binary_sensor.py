"""Tests for EEBUS binary sensor entities."""

from unittest.mock import MagicMock

from custom_components.eebus.binary_sensor import (
    EebusConnectedSensor,
    EebusHeartbeatOkSensor,
)


def _sensor(*, connected: bool | None, poll_ok: bool) -> EebusConnectedSensor:
    coordinator = MagicMock()
    coordinator.data = None if connected is None else {"connected": connected}
    coordinator.ski = "test-ski"
    coordinator.last_update_success = poll_ok
    return EebusConnectedSensor(coordinator)


def _heartbeat_sensor(
    *, within_duration: bool | None, poll_ok: bool
) -> EebusHeartbeatOkSensor:
    coordinator = MagicMock()
    coordinator.data = {
        "heartbeat_status": (
            None if within_duration is None else {"within_duration": within_duration}
        )
    }
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


def test_heartbeat_available_when_device_disconnected_but_poll_succeeded() -> None:
    sensor = _heartbeat_sensor(within_duration=True, poll_ok=True)
    assert sensor.available is True
    assert sensor.is_on is False


def test_heartbeat_unavailable_when_poll_failed() -> None:
    sensor = _heartbeat_sensor(within_duration=True, poll_ok=False)
    assert sensor.available is False
