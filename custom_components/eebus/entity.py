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
        info = coordinator.data.connection.device_info if coordinator.data else None
        self._attr_device_info = DeviceInfo(
            identifiers={(DOMAIN, coordinator.ski)},
            name=f"EEBUS {coordinator.ski[:8]}",
            manufacturer=info.manufacturer if info else None,
            model=info.model if info else None,
            serial_number=info.serial if info else None,
        )

    @property
    def available(self) -> bool:
        """Unavailable whenever the coordinator poll failed or the remote device is disconnected."""
        if not super().available:
            return False
        return bool(self.coordinator.data and self.coordinator.data.connection.connected)
