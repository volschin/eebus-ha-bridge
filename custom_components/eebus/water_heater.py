"""Water heater entity for EEBUS domestic-hot-water control."""

from __future__ import annotations

from typing import Any

from homeassistant.components.water_heater import WaterHeaterEntity, WaterHeaterEntityFeature
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import ATTR_TEMPERATURE, UnitOfTemperature
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
    """Set up the EEBUS domestic-hot-water entity."""
    coordinator: EebusCoordinator = entry.runtime_data
    async_add_entities([EebusDHWWaterHeater(coordinator)])


class EebusDHWWaterHeater(EebusEntity, WaterHeaterEntity):
    """Domestic-hot-water tank exposed by the EEBUS heat pump."""

    _attr_temperature_unit = UnitOfTemperature.CELSIUS
    _attr_translation_key = "domestic_hot_water"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize the domestic-hot-water entity."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_domestic_hot_water"

    def _setpoint(self) -> dict[str, Any] | None:
        data = self.coordinator.data or {}
        if data.get("dhw_supported") is False:
            return None
        return data.get("dhw_setpoint")

    def _system_function(self) -> dict[str, Any] | None:
        data = self.coordinator.data or {}
        if data.get("dhw_sysfn_supported") is False:
            return None
        return data.get("dhw_system_function")

    @property
    def available(self) -> bool:
        """Return whether the bridge is connected and DHW data is available."""
        if not super().available:
            return False
        data = self.coordinator.data or {}
        return bool(
            data.get("dhw_temperature_c") is not None
            or self._setpoint() is not None
            or self._system_function() is not None
        )

    @property
    def supported_features(self) -> WaterHeaterEntityFeature:
        """Return the controls currently advertised as writable by the device."""
        features = WaterHeaterEntityFeature(0)
        setpoint = self._setpoint()
        if setpoint is not None and setpoint.get("writable"):
            features |= WaterHeaterEntityFeature.TARGET_TEMPERATURE
        system_function = self._system_function()
        if (
            system_function is not None
            and system_function.get("mode_writable")
            and system_function.get("available_modes")
        ):
            features |= WaterHeaterEntityFeature.OPERATION_MODE
        return features

    @property
    def current_temperature(self) -> float | None:
        """Return the measured domestic-hot-water temperature."""
        value = (self.coordinator.data or {}).get("dhw_temperature_c")
        return None if value is None else float(value)

    @property
    def target_temperature(self) -> float | None:
        """Return the configured domestic-hot-water target."""
        setpoint = self._setpoint()
        return None if setpoint is None else float(setpoint["value_celsius"])

    @property
    def min_temp(self) -> float:
        """Return the device-provided lower target-temperature bound."""
        setpoint = self._setpoint()
        return float(setpoint["min_celsius"]) if setpoint is not None else 0.0

    @property
    def max_temp(self) -> float:
        """Return the device-provided upper target-temperature bound."""
        setpoint = self._setpoint()
        return float(setpoint["max_celsius"]) if setpoint is not None else 100.0

    @property
    def target_temperature_step(self) -> float | None:
        """Return the device-provided target-temperature increment."""
        setpoint = self._setpoint()
        return None if setpoint is None else float(setpoint["step_celsius"])

    @property
    def operation_list(self) -> list[str] | None:
        """Return operation modes advertised by the device."""
        system_function = self._system_function()
        if system_function is None:
            return None
        modes = system_function.get("available_modes")
        return list(modes) if isinstance(modes, list) else None

    @property
    def current_operation(self) -> str | None:
        """Return the active domestic-hot-water operation mode."""
        system_function = self._system_function()
        if system_function is None:
            return None
        mode = system_function.get("operation_mode")
        return mode if isinstance(mode, str) and mode else None

    async def async_set_temperature(self, **kwargs: Any) -> None:
        """Set the domestic-hot-water target temperature."""
        await self.coordinator.async_write_dhw_setpoint(float(kwargs[ATTR_TEMPERATURE]))
        await self.coordinator.async_request_refresh()

    async def async_set_operation_mode(self, operation_mode: str) -> None:
        """Set the domestic-hot-water operation mode."""
        await self.coordinator.async_set_dhw_operation_mode(operation_mode)
        await self.coordinator.async_request_refresh()
