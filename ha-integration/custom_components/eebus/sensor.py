"""Sensor entities for EEBUS integration."""

from __future__ import annotations

from homeassistant.components.sensor import (
    SensorDeviceClass,
    SensorEntity,
    SensorStateClass,
)
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import EntityCategory, UnitOfPower
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from .coordinator import EebusCoordinator
from .entity import EebusEntity


async def async_setup_entry(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    """Set up EEBUS sensors."""
    coordinator: EebusCoordinator = entry.runtime_data
    async_add_entities([
        EebusPowerSensor(coordinator),
        EebusConsumptionLimitSensor(coordinator),
    ])


class EebusPowerSensor(EebusEntity, SensorEntity):
    """Sensor for current power consumption."""

    _attr_device_class = SensorDeviceClass.POWER
    _attr_native_unit_of_measurement = UnitOfPower.WATT
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_translation_key = "power_consumption"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_power"

    @property
    def native_value(self) -> float | None:
        """Return current power in watts."""
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("power_watts")


class EebusConsumptionLimitSensor(EebusEntity, SensorEntity):
    """Read-only sensor showing current consumption limit."""

    _attr_device_class = SensorDeviceClass.POWER
    _attr_native_unit_of_measurement = UnitOfPower.WATT
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_translation_key = "consumption_limit"
    _attr_entity_category = EntityCategory.DIAGNOSTIC

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_consumption_limit"

    @property
    def native_value(self) -> float | None:
        """Return current limit in watts."""
        if self.coordinator.data is None:
            return None
        limit = self.coordinator.data.get("consumption_limit")
        if limit is None:
            return None
        return limit.get("value_watts")
