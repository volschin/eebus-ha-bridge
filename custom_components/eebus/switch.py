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
    ]
    # OHPCF compressor-flexibility control is only offered when the bridge's OHPCF
    # client is active and a compatible heat pump was found.
    if coordinator.data and coordinator.data.get("ohpcf_supported"):
        entities.append(EebusCompressorFlexibilitySwitch(coordinator))
    async_add_entities(entities)


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


# Compressor-flexibility process states that count as "on" (consuming or about to).
_OHPCF_ON_STATES = {"COMPRESSOR_STATE_RUNNING", "COMPRESSOR_STATE_SCHEDULED"}


class EebusCompressorFlexibilitySwitch(EebusEntity, SwitchEntity):
    """Switch driving the heat-pump compressor's optional power consumption (OHPCF).

    On  = schedule (or resume) the optional consumption to soak up PV surplus.
    Off = pause the running process, or abort it when pausing is not supported.

    Gold: translation_key, entity_category CONFIG.
    """

    _attr_translation_key = "compressor_flexibility"
    _attr_entity_category = EntityCategory.CONFIG

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_compressor_flexibility"

    def _flex(self) -> dict[str, Any] | None:
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("compressor_flexibility")

    @property
    def available(self) -> bool:
        """Available only while the compressor advertises a flexibility offer."""
        return super().available and self._flex() is not None

    @property
    def is_on(self) -> bool | None:
        """Return True while the optional consumption is scheduled or running."""
        flex = self._flex()
        if flex is None:
            return None
        return flex.get("state") in _OHPCF_ON_STATES

    async def async_turn_on(self, **kwargs: Any) -> None:
        """Schedule (or resume) the optional power consumption."""
        from . import proto_stubs

        flex = self._flex() or {}
        action = (
            proto_stubs.OHPCFAction.OHPCF_ACTION_RESUME
            if flex.get("state") == "COMPRESSOR_STATE_PAUSED"
            else proto_stubs.OHPCFAction.OHPCF_ACTION_SCHEDULE
        )
        await self.coordinator.async_control_compressor(action)
        await self.coordinator.async_request_refresh()

    async def async_turn_off(self, **kwargs: Any) -> None:
        """Pause the optional power consumption, or abort it when not pausable."""
        from . import proto_stubs

        flex = self._flex() or {}
        action = (
            proto_stubs.OHPCFAction.OHPCF_ACTION_PAUSE
            if flex.get("is_pausable")
            else proto_stubs.OHPCFAction.OHPCF_ACTION_ABORT
        )
        await self.coordinator.async_control_compressor(action)
        await self.coordinator.async_request_refresh()
