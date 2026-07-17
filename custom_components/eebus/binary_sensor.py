"""Binary sensor entities for EEBUS integration."""

from __future__ import annotations

from homeassistant.components.binary_sensor import (
    BinarySensorDeviceClass,
    BinarySensorEntity,
)
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import EntityCategory
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from .coordinator import EebusCoordinator
from .entity import EebusEntity

PARALLEL_UPDATES = 0  # Coordinator-based, no per-entity polling


async def async_setup_entry(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    """Set up EEBUS binary sensors."""
    coordinator: EebusCoordinator = entry.runtime_data
    async_add_entities([
        EebusConnectedSensor(coordinator),
        EebusHeartbeatOkSensor(coordinator),
    ])


class EebusConnectedSensor(EebusEntity, BinarySensorEntity):
    """Binary sensor for EEBUS connection status.

    Gold: translation_key, entity_category DIAGNOSTIC.
    """

    _attr_device_class = BinarySensorDeviceClass.CONNECTIVITY
    _attr_translation_key = "connected"
    _attr_entity_category = EntityCategory.DIAGNOSTIC

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_connected"

    @property
    def available(self) -> bool:
        """Stay available on a successful poll regardless of connected state.

        EebusEntity.available gates on the device being connected, which would
        make this exact sensor disappear as "unavailable" instead of showing
        "off" the moment it has something to report.
        """
        return self.coordinator.last_update_success

    @property
    def is_on(self) -> bool | None:
        """Return True if connected."""
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("connected")


class EebusHeartbeatOkSensor(EebusEntity, BinarySensorEntity):
    """Binary sensor for heartbeat health.

    Gold: translation_key, entity_category DIAGNOSTIC, disabled by default.
    """

    _attr_device_class = BinarySensorDeviceClass.PROBLEM
    _attr_translation_key = "heartbeat_ok"
    _attr_entity_category = EntityCategory.DIAGNOSTIC
    _attr_entity_registry_enabled_default = False  # Gold: less popular, disabled by default

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_heartbeat_ok"

    @property
    def available(self) -> bool:
        """Stay available on a successful poll regardless of connected state.

        EebusEntity.available gates on device connection, which would flip
        this sensor to "unavailable" (grey, no history) on every transient
        per-device disconnect instead of a stable on/off a dashboard can plot.
        """
        return self.coordinator.last_update_success

    @property
    def is_on(self) -> bool | None:
        """Return True if heartbeat has a problem (inverted for PROBLEM class)."""
        if self.coordinator.data is None:
            return None
        hb = self.coordinator.data.get("heartbeat_status")
        if hb is None:
            return None
        within_duration = hb.get("within_duration")
        if within_duration is None:
            return None
        # PROBLEM class: is_on=True means there's a problem
        return not within_duration
