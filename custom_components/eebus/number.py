"""Number entities for EEBUS integration."""

from __future__ import annotations

from homeassistant.components.number import NumberDeviceClass, NumberEntity, NumberMode
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import EntityCategory, UnitOfPower
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from .coordinator import EebusCoordinator
from .entity import EebusEntity
from .models import CapabilityState
from .state import StateField, is_fresh

PARALLEL_UPDATES = 0  # Coordinator-based, no per-entity polling


async def async_setup_entry(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    """Set up EEBUS number entities."""
    coordinator: EebusCoordinator = entry.runtime_data
    async_add_entities(
        [
            EebusLPCLimitNumber(coordinator),
            EebusFailsafeLimitNumber(coordinator),
        ]
    )


class EebusLPCLimitNumber(EebusEntity, NumberEntity):
    """Number entity for setting LPC consumption limit.

    Gold: device_class, translation_key, entity_category CONFIG.
    """

    _attr_device_class = NumberDeviceClass.POWER
    _attr_native_unit_of_measurement = UnitOfPower.WATT
    _attr_mode = NumberMode.BOX
    _attr_native_min_value = 0
    _attr_native_max_value = 32000
    _attr_native_step = 100
    _attr_translation_key = "lpc_limit"
    _attr_entity_category = EntityCategory.CONFIG

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_lpc_limit"

    @property
    def native_value(self) -> float | None:
        """Return current limit value."""
        if self.coordinator.data is None:
            return None
        limit = self.coordinator.data.lpc.consumption_limit
        if limit is None:
            return None
        return float(limit.value_watts)

    @property
    def available(self) -> bool:
        """Disable entity when LPC is known to be unsupported."""
        if not super().available:
            return False
        if self.coordinator.data is None:
            return False
        return self.coordinator.data.capabilities.lpc == CapabilityState.AVAILABLE and is_fresh(
            self.coordinator.data, StateField.CONSUMPTION_LIMIT
        )

    async def async_set_native_value(self, value: float) -> None:
        """Set new LPC limit via gRPC."""
        await self.coordinator.async_write_lpc_limit(value)
        await self.coordinator.async_request_refresh()


class EebusFailsafeLimitNumber(EebusEntity, NumberEntity):
    """Number entity for setting failsafe limit.

    Gold: entity_category CONFIG. Enabled by default (primary §14a control).
    """

    _attr_device_class = NumberDeviceClass.POWER
    _attr_native_unit_of_measurement = UnitOfPower.WATT
    _attr_mode = NumberMode.BOX
    _attr_native_min_value = 0
    _attr_native_max_value = 32000
    _attr_native_step = 100
    _attr_translation_key = "failsafe_limit"
    _attr_entity_category = EntityCategory.CONFIG
    # Enabled by default like the LPC limit number: the §14a failsafe fallback is a
    # primary control, not a "less popular" entity — keep it reachable without manual enable.

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_failsafe_limit"

    @property
    def native_value(self) -> float | None:
        """Return current failsafe limit."""
        if self.coordinator.data is None:
            return None
        failsafe = self.coordinator.data.lpc.failsafe_limit
        if failsafe is None:
            return None
        return float(failsafe.value_watts)

    @property
    def available(self) -> bool:
        """Disable entity when failsafe is known to be unsupported."""
        if not super().available:
            return False
        if self.coordinator.data is None:
            return False
        return self.coordinator.data.capabilities.failsafe == CapabilityState.AVAILABLE and is_fresh(
            self.coordinator.data, StateField.FAILSAFE_LIMIT
        )

    async def async_set_native_value(self, value: float) -> None:
        """Set new failsafe limit via gRPC."""
        await self.coordinator.async_write_failsafe_limit(value)
        await self.coordinator.async_request_refresh()
