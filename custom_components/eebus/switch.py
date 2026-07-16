"""Switch entities for EEBUS integration."""

from __future__ import annotations

from typing import Any

from homeassistant.components.switch import SwitchEntity
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import EntityCategory
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from .coordinator import EebusCoordinator
from .entity import EebusEntity
from .models import DHWSystemFunctionState

PARALLEL_UPDATES = 0  # Coordinator-based, no per-entity polling


async def async_setup_entry(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    """Set up EEBUS switch entities."""
    coordinator: EebusCoordinator = entry.runtime_data
    entities: list[SwitchEntity] = [
        EebusLPCActiveSwitch(coordinator),
        EebusHeartbeatSwitch(coordinator),
        EebusDHWBoostSwitch(coordinator),
    ]
    async_add_entities(entities)


class EebusLPCActiveSwitch(EebusEntity, SwitchEntity):
    """Switch for activating/deactivating LPC limit."""

    _attr_translation_key = "lpc_active"

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
        is_active = limit.get("is_active")
        return None if is_active is None else bool(is_active)

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
        running = hb.get("running")
        return None if running is None else bool(running)

    async def async_turn_on(self, **kwargs: Any) -> None:
        """Start heartbeat."""
        await self.coordinator.async_start_heartbeat()
        await self.coordinator.async_request_refresh()

    async def async_turn_off(self, **kwargs: Any) -> None:
        """Stop heartbeat."""
        await self.coordinator.async_stop_heartbeat()
        await self.coordinator.async_request_refresh()


class EebusDHWBoostSwitch(EebusEntity, SwitchEntity):
    """Switch for domestic-hot-water one-time boost."""

    _attr_translation_key = "dhw_boost"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_dhw_boost"

    def _state(self) -> DHWSystemFunctionState | None:
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("dhw_system_function")

    @property
    def available(self) -> bool:
        """Available only for writable DHW boost state."""
        if not super().available:
            return False
        data = self.coordinator.data or {}
        state = self._state()
        return bool(
            data.get("dhw_sysfn_supported") is not False
            and state is not None
            and state.get("boost_writable")
        )

    @property
    def is_on(self) -> bool | None:
        """Return True while boost is active or running."""
        state = self._state()
        if state is None:
            return None
        return state.get("boost_status") in ("active", "running")

    @property
    def extra_state_attributes(self) -> dict[str, Any] | None:
        """Expose the raw boost status."""
        state = self._state()
        if state is None:
            return None
        return {"boost_status": state.get("boost_status")}

    async def async_turn_on(self, **kwargs: Any) -> None:
        """Activate DHW boost."""
        await self.coordinator.async_set_dhw_boost(True)
        await self.coordinator.async_request_refresh()

    async def async_turn_off(self, **kwargs: Any) -> None:
        """Cancel DHW boost."""
        await self.coordinator.async_set_dhw_boost(False)
        await self.coordinator.async_request_refresh()
