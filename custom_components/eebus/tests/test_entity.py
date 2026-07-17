"""Tests for the EEBUS base entity."""

from unittest.mock import MagicMock

from custom_components.eebus.entity import EebusEntity
from custom_components.eebus.state import ConnectionState, DeviceState


def _entity(*, connected: bool, poll_ok: bool) -> EebusEntity:
    coordinator = MagicMock()
    coordinator.data = DeviceState(connection=ConnectionState(connected=connected))
    coordinator.ski = "test-ski"
    coordinator.last_update_success = poll_ok
    return EebusEntity(coordinator)


def test_available_when_connected_and_poll_succeeded() -> None:
    assert _entity(connected=True, poll_ok=True).available is True


def test_unavailable_when_device_disconnected() -> None:
    assert _entity(connected=False, poll_ok=True).available is False


def test_unavailable_when_poll_failed_even_if_device_was_connected() -> None:
    assert _entity(connected=True, poll_ok=False).available is False
