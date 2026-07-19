"""Select entities for EEBUS integration."""

from __future__ import annotations

from typing import Any

from homeassistant.components.select import SelectEntity
from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import ServiceValidationError
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from .coordinator import EebusCoordinator
from .entity import EebusEntity
from .models import CompressorFlexibilityState
from .models import CapabilityState
from .state import StateField, is_fresh

PARALLEL_UPDATES = 0  # Coordinator-based, no per-entity polling

# Compressor-flexibility process states that count as "on" (consuming or about to).
_OHPCF_ON_STATES = {"COMPRESSOR_STATE_RUNNING", "COMPRESSOR_STATE_SCHEDULED"}
_OHPCF_PAUSED_STATE = "COMPRESSOR_STATE_PAUSED"

OPTION_ON = "on"
OPTION_PAUSED = "paused"
OPTION_OFF = "off"


async def async_setup_entry(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    """Set up EEBUS select entities."""
    coordinator: EebusCoordinator = entry.runtime_data
    entities: list[SelectEntity] = [EebusCompressorFlexibilitySelect(coordinator)]
    async_add_entities(entities)


class EebusCompressorFlexibilitySelect(EebusEntity, SelectEntity):
    """Select driving the heat-pump compressor's optional power consumption (OHPCF).

    A plain on/off switch collapses PAUSED into "off" alongside AVAILABLE,
    COMPLETED and STOPPED, losing the distinction between a paused and a
    stopped process. This select exposes it as a third option instead.

    on     = schedule (or resume) the optional consumption to soak up PV surplus.
    paused = pause the running process.
    off    = abort the process (or the implicit state when no offer is running).

    This is a primary operational control, so it has no entity category.
    """

    _attr_translation_key = "compressor_flexibility"
    _attr_options = [OPTION_ON, OPTION_PAUSED, OPTION_OFF]

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_compressor_flexibility"

    def _flex(self) -> CompressorFlexibilityState | None:
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.ohpcf.compressor_flexibility

    @property
    def available(self) -> bool:
        """Available while the OHPCF capability and its base state are readable."""
        data = self.coordinator.data
        return bool(
            super().available
            and data
            and data.capabilities.ohpcf == CapabilityState.AVAILABLE
            and is_fresh(data, StateField.COMPRESSOR_FLEXIBILITY)
            and self._flex() is not None
        )

    @property
    def current_option(self) -> str | None:
        """Return on/paused/off depending on the process state."""
        flex = self._flex()
        if flex is None:
            return None
        state = flex.state
        if state in _OHPCF_ON_STATES:
            return OPTION_ON
        if state == _OHPCF_PAUSED_STATE:
            return OPTION_PAUSED
        return OPTION_OFF

    @property
    def extra_state_attributes(self) -> dict[str, Any] | None:
        """Expose the process constraints the CEM must honour once it acts."""
        flex = self._flex()
        if flex is None:
            return None
        return {
            "is_stoppable": flex.is_stoppable,
            "minimal_run_seconds": flex.minimal_run_seconds,
            "minimal_pause_seconds": flex.minimal_pause_seconds,
        }

    async def async_select_option(self, option: str) -> None:
        """Schedule/resume, pause, or abort the optional power consumption."""
        from . import proto_stubs

        flex = self._flex()
        if option == OPTION_ON:
            if flex is not None and flex.state == _OHPCF_PAUSED_STATE:
                action = proto_stubs.OHPCFAction.OHPCF_ACTION_RESUME
            elif flex is not None and flex.available:
                action = proto_stubs.OHPCFAction.OHPCF_ACTION_SCHEDULE
            else:
                raise ServiceValidationError(
                    "No optional compressor-consumption process is available to schedule"
                )
        elif option == OPTION_PAUSED:
            if (
                flex is None
                or flex.state != "COMPRESSOR_STATE_RUNNING"
                or not flex.is_pausable
            ):
                raise ServiceValidationError(
                    "Only a running, pausable compressor process can be paused"
                )
            action = proto_stubs.OHPCFAction.OHPCF_ACTION_PAUSE
        elif option == OPTION_OFF:
            if (
                flex is None
                or flex.state not in _OHPCF_ON_STATES | {_OHPCF_PAUSED_STATE}
                or not flex.is_stoppable
            ):
                raise ServiceValidationError(
                    "Only an active or scheduled stoppable compressor process can be aborted"
                )
            action = proto_stubs.OHPCFAction.OHPCF_ACTION_ABORT
        else:
            raise ServiceValidationError(f"Unsupported compressor option: {option}")
        await self.coordinator.async_control_compressor(action)
        await self.coordinator.async_request_refresh()
