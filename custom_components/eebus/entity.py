"""Base entity for EEBUS integration."""

from __future__ import annotations

from typing import Any

from homeassistant.helpers import device_registry as dr
from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.update_coordinator import CoordinatorEntity

from .const import DOMAIN
from .coordinator import EebusCoordinator


class EebusEntity(CoordinatorEntity[EebusCoordinator]):
    """Base class for EEBUS entities."""

    _attr_has_entity_name = True

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize the entity."""
        super().__init__(coordinator)
        self._attr_device_info = self._current_device_info()

    def _current_device_info(self) -> DeviceInfo:
        info = self.coordinator.data.connection.device_info if self.coordinator.data else None
        return DeviceInfo(
            identifiers={(DOMAIN, self.coordinator.ski)},
            name=f"EEBUS {self.coordinator.ski[:8]}",
            manufacturer=info.manufacturer if info else None,
            model=info.model if info else None,
            serial_number=info.serial if info else None,
            sw_version=info.sw_version if info else None,
            hw_version=info.hw_version if info else None,
        )

    def _sync_device_info(self) -> None:
        """Persist classification fields that arrived after entity creation."""
        current = self._current_device_info()
        if current == self._attr_device_info:
            return
        self._attr_device_info = current
        if self.hass is None:
            return
        registry = dr.async_get(self.hass)
        device = registry.async_get_device(identifiers={(DOMAIN, self.coordinator.ski)})
        if device is None:
            return
        info = self.coordinator.data.connection.device_info if self.coordinator.data else None
        if info is None:
            return
        updates: dict[str, Any] = {
            key: value
            for key, value in {
                "manufacturer": info.manufacturer,
                "model": info.model,
                "serial_number": info.serial,
                "sw_version": info.sw_version,
                "hw_version": info.hw_version,
            }.items()
            if value is not None
        }
        if updates:
            registry.async_update_device(device.id, **updates)

    def _handle_coordinator_update(self) -> None:
        self._sync_device_info()
        super()._handle_coordinator_update()

    @property
    def available(self) -> bool:
        """Unavailable whenever the coordinator poll failed or the remote device is disconnected."""
        if not super().available:
            return False
        return bool(self.coordinator.data and self.coordinator.data.connection.connected)
