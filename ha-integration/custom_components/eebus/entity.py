"""Base entity for EEBUS integration."""

from __future__ import annotations

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
        self._attr_device_info = DeviceInfo(
            identifiers={(DOMAIN, coordinator.ski)},
            name=f"EEBUS {coordinator.ski[:8]}",
            manufacturer="Vaillant",
            model="VR940f",
        )
