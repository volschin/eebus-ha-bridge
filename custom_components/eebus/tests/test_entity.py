"""Tests for the EEBUS base entity."""

from unittest.mock import MagicMock, patch

from custom_components.eebus.entity import EebusEntity
from custom_components.eebus.models import DeviceInfo
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


def test_device_info_maps_all_bridge_classification_fields() -> None:
    entity = _entity(connected=True, poll_ok=True)
    entity.coordinator.data = DeviceState(
        connection=ConnectionState(
            connected=True,
            device_info=DeviceInfo(
                manufacturer="Vaillant",
                model="VR940",
                serial="SN-1",
                sw_version="4.2.1",
                hw_version="R3",
            ),
        )
    )
    entity._sync_device_info()

    assert entity.device_info is not None
    assert entity.device_info["manufacturer"] == "Vaillant"
    assert entity.device_info["model"] == "VR940"
    assert entity.device_info["serial_number"] == "SN-1"
    assert entity.device_info["sw_version"] == "4.2.1"
    assert entity.device_info["hw_version"] == "R3"


def test_late_device_info_updates_home_assistant_registry() -> None:
    entity = _entity(connected=True, poll_ok=True)
    entity.hass = MagicMock()
    device_registry = MagicMock()
    device_registry.async_get_device.return_value = MagicMock(id="device-id")
    entity.coordinator.data = DeviceState(
        connection=ConnectionState(
            connected=True,
            device_info=DeviceInfo(manufacturer="Vaillant", model="VR940", sw_version="4.2.1"),
        )
    )

    with patch("custom_components.eebus.entity.dr.async_get", return_value=device_registry):
        entity._sync_device_info()

    device_registry.async_update_device.assert_called_once_with(
        "device-id", manufacturer="Vaillant", model="VR940", sw_version="4.2.1"
    )
