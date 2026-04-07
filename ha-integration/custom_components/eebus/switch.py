"""Switch entities for EEBUS integration."""

from __future__ import annotations

from typing import Any

from homeassistant.components.switch import SwitchEntity
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import EntityCategory
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from .const import DOMAIN, PARALLEL_UPDATES
from .coordinator import EebusCoordinator
from .entity import EebusEntity


async def async_setup_entry(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    """Set up EEBUS switch entities."""
    coordinator: EebusCoordinator = entry.runtime_data
    async_add_entities([
        EebusLPCActiveSwitch(coordinator),
        EebusHeartbeatSwitch(coordinator),
    ])


class EebusLPCActiveSwitch(EebusEntity, SwitchEntity):
    """Switch for activating/deactivating LPC limit.

    Gold: translation_key, entity_category CONFIG.
    """

    _attr_translation_key = "lpc_active"
    _attr_entity_category = EntityCategory.CONFIG

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_lpc_active"

    @property
    def is_on(self) -> bool | None:
        """Return True if LPC limit is active."""
        if self.coordinator.data is None:
            return None
        limit = self.coordinator.data.get("consumption_limit")
        if limit is None:
            return None
        return limit.get("is_active")

    async def async_turn_on(self, **kwargs: Any) -> None:
        """Activate LPC limit."""
        await self.coordinator.async_set_lpc_active(True)
        await self.coordinator.async_request_refresh()

    async def async_turn_off(self, **kwargs: Any) -> None:
        """Deactivate LPC limit."""
        await self.coordinator.async_set_lpc_active(False)
        await self.coordinator.async_request_refresh()


class EebusHeartbeatSwitch(EebusEntity, SwitchEntity):
    """Switch for starting/stopping EEBUS heartbeat.

    Gold: translation_key, entity_category CONFIG, disabled by default.
    """

    _attr_translation_key = "heartbeat"
    _attr_entity_category = EntityCategory.CONFIG
    _attr_entity_registry_enabled_default = False  # Gold: less popular, disabled by default

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_heartbeat"

    @property
    def is_on(self) -> bool | None:
        """Return True if heartbeat is running."""
        if self.coordinator.data is None:
            return None
        hb = self.coordinator.data.get("heartbeat_status")
        if hb is None:
            return None
        return hb.get("running")

    async def async_turn_on(self, **kwargs: Any) -> None:
        """Start heartbeat."""
        await self.coordinator.async_start_heartbeat()
        await self.coordinator.async_request_refresh()

    async def async_turn_off(self, **kwargs: Any) -> None:
        """Stop heartbeat."""
        await self.coordinator.async_stop_heartbeat()
        await self.coordinator.async_request_refresh()
