"""Number entities for EEBUS integration."""

from __future__ import annotations

from homeassistant.components.number import NumberDeviceClass, NumberEntity, NumberMode
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import EntityCategory, UnitOfPower, UnitOfTemperature
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
    """Set up EEBUS number entities."""
    coordinator: EebusCoordinator = entry.runtime_data
    async_add_entities([
        EebusLPCLimitNumber(coordinator),
        EebusFailsafeLimitNumber(coordinator),
        EebusDHWSetpointNumber(coordinator),
    ])


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
        limit = self.coordinator.data.get("consumption_limit")
        if limit is None:
            return None
        value = limit.get("value_watts")
        return None if value is None else float(value)

    @property
    def available(self) -> bool:
        """Disable entity when LPC is known to be unsupported."""
        if not super().available:
            return False
        if self.coordinator.data is None:
            return False
        return self.coordinator.data.get("lpc_supported") is not False

    async def async_set_native_value(self, value: float) -> None:
        """Set new LPC limit via gRPC."""
        await self.coordinator.async_write_lpc_limit(value)
        await self.coordinator.async_request_refresh()


class EebusFailsafeLimitNumber(EebusEntity, NumberEntity):
    """Number entity for setting failsafe limit.

    Gold: entity_category CONFIG, entity_disabled_by_default.
    """

    _attr_device_class = NumberDeviceClass.POWER
    _attr_native_unit_of_measurement = UnitOfPower.WATT
    _attr_mode = NumberMode.BOX
    _attr_native_min_value = 0
    _attr_native_max_value = 32000
    _attr_native_step = 100
    _attr_translation_key = "failsafe_limit"
    _attr_entity_category = EntityCategory.CONFIG
    _attr_entity_registry_enabled_default = False  # Gold: less popular entities disabled

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_failsafe_limit"

    @property
    def native_value(self) -> float | None:
        """Return current failsafe limit."""
        if self.coordinator.data is None:
            return None
        failsafe = self.coordinator.data.get("failsafe_limit")
        if failsafe is None:
            return None
        value = failsafe.get("value_watts")
        return None if value is None else float(value)

    @property
    def available(self) -> bool:
        """Disable entity when failsafe is known to be unsupported."""
        if not super().available:
            return False
        if self.coordinator.data is None:
            return False
        return self.coordinator.data.get("failsafe_supported") is not False

    async def async_set_native_value(self, value: float) -> None:
        """Set new failsafe limit via gRPC."""
        await self.coordinator.async_write_failsafe_limit(value)
        await self.coordinator.async_request_refresh()


class EebusDHWSetpointNumber(EebusEntity, NumberEntity):
    """Domestic-hot-water target temperature advertised by the heat pump."""

    _attr_device_class = NumberDeviceClass.TEMPERATURE
    _attr_native_unit_of_measurement = UnitOfTemperature.CELSIUS
    _attr_mode = NumberMode.BOX
    _attr_translation_key = "dhw_setpoint"
    _attr_entity_category = EntityCategory.CONFIG

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize the DHW setpoint number."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_dhw_setpoint"

    @property
    def native_value(self) -> float | None:
        """Return the current DHW target."""
        setpoint = (self.coordinator.data or {}).get("dhw_setpoint")
        return None if setpoint is None else float(setpoint["value_celsius"])

    @property
    def native_min_value(self) -> float:
        """Return the device-provided lower bound."""
        setpoint = (self.coordinator.data or {}).get("dhw_setpoint")
        return float(setpoint["min_celsius"]) if setpoint is not None else 0.0

    @property
    def native_max_value(self) -> float:
        """Return the device-provided upper bound."""
        setpoint = (self.coordinator.data or {}).get("dhw_setpoint")
        return float(setpoint["max_celsius"]) if setpoint is not None else 100.0

    @property
    def native_step(self) -> float:
        """Return the device-provided increment."""
        setpoint = (self.coordinator.data or {}).get("dhw_setpoint")
        return float(setpoint["step_celsius"]) if setpoint is not None else 1.0

    @property
    def available(self) -> bool:
        """Expose control only for a negotiated writable DHW setpoint."""
        if not super().available:
            return False
        data = self.coordinator.data or {}
        setpoint = data.get("dhw_setpoint")
        return bool(
            data.get("dhw_supported") is not False
            and setpoint is not None
            and setpoint.get("writable")
        )

    async def async_set_native_value(self, value: float) -> None:
        """Set the DHW target via gRPC."""
        await self.coordinator.async_write_dhw_setpoint(value)
        await self.coordinator.async_request_refresh()
