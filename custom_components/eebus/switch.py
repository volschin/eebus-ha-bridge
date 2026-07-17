"""Switch entities for EEBUS integration."""

from __future__ import annotations

from typing import Any

from homeassistant.components.switch import SwitchEntity
from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from .coordinator import EebusCoordinator
from .entity import EebusEntity
from .models import CapabilityState, DHWSystemFunctionState
from .state import StateField, is_fresh

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
        limit = self.coordinator.data.lpc.consumption_limit
        if limit is None:
            return None
        return limit.is_active

    @property
    def available(self) -> bool:
        """Disable the operational control while its value is stale."""
        data = self.coordinator.data
        return bool(
            super().available
            and data
            and data.capabilities.lpc == CapabilityState.AVAILABLE
            and is_fresh(data, StateField.CONSUMPTION_LIMIT)
        )

    async def async_turn_on(self, **kwargs: Any) -> None:
        """Activate LPC limit."""
        await self.coordinator.async_set_lpc_active(True)
        await self.coordinator.async_request_refresh()

    async def async_turn_off(self, **kwargs: Any) -> None:
        """Deactivate LPC limit."""
        await self.coordinator.async_set_lpc_active(False)
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
        return self.coordinator.data.dhw.system_function

    @property
    def available(self) -> bool:
        """Available only for writable DHW boost state."""
        if not super().available:
            return False
        data = self.coordinator.data
        state = self._state()
        return bool(
            data is not None
            and data.capabilities.dhw_system_function == CapabilityState.AVAILABLE
            and is_fresh(data, StateField.DHW_SYSTEM_FUNCTION)
            and state is not None
            and state.boost_writable
        )

    @property
    def is_on(self) -> bool | None:
        """Return True while boost is active or running."""
        state = self._state()
        if state is None:
            return None
        return state.boost_status in ("active", "running")

    @property
    def extra_state_attributes(self) -> dict[str, Any] | None:
        """Expose the raw boost status."""
        state = self._state()
        if state is None:
            return None
        return {"boost_status": state.boost_status}

    async def async_turn_on(self, **kwargs: Any) -> None:
        """Activate DHW boost."""
        await self.coordinator.async_set_dhw_boost(True)
        await self.coordinator.async_request_refresh()

    async def async_turn_off(self, **kwargs: Any) -> None:
        """Cancel DHW boost."""
        await self.coordinator.async_set_dhw_boost(False)
        await self.coordinator.async_request_refresh()
