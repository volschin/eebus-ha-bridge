"""Climate entity for EEBUS room-heating control."""

from __future__ import annotations

import logging
from typing import Any

from homeassistant.components.climate import ClimateEntity
from homeassistant.components.climate.const import ClimateEntityFeature, HVACMode
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import ATTR_TEMPERATURE, UnitOfTemperature
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from .coordinator import EebusCoordinator
from .entity import EebusEntity
from .models import SetpointState, SystemFunctionState

PARALLEL_UPDATES = 0
_LOGGER = logging.getLogger(__name__)

_EEBUS_TO_HA_MODE: dict[str, HVACMode] = {
    "auto": HVACMode.AUTO,
    "on": HVACMode.HEAT,
    "off": HVACMode.OFF,
}
_HA_TO_EEBUS_MODE = {value: key for key, value in _EEBUS_TO_HA_MODE.items()}


async def async_setup_entry(
    hass: HomeAssistant, entry: ConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    """Set up room heating."""
    coordinator: EebusCoordinator = entry.runtime_data
    async_add_entities([EebusRoomHeatingClimate(coordinator)])


class EebusRoomHeatingClimate(EebusEntity, ClimateEntity):
    """Room-heating control exposed by the HVACRoom entity."""

    _attr_temperature_unit = UnitOfTemperature.CELSIUS
    _attr_translation_key = "room_heating"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_room_heating"

    def _setpoint(self) -> SetpointState | None:
        return (self.coordinator.data or {}).get("room_heating_setpoint")

    def _system_function(self) -> SystemFunctionState | None:
        return (self.coordinator.data or {}).get("room_heating_system_function")

    @property
    def available(self) -> bool:
        if not super().available:
            return False
        data = self.coordinator.data or {}
        if data.get("room_heating_supported") is False:
            return False
        system = self._system_function()
        if system is not None:
            mode = system.get("operation_mode")
            if mode and mode not in _EEBUS_TO_HA_MODE:
                _LOGGER.debug("EEBUS room heating mode %r has no HA mapping", mode)
                return False
        return data.get("room_temperature_c") is not None or self._setpoint() is not None

    @property
    def supported_features(self) -> ClimateEntityFeature:
        features = ClimateEntityFeature(0)
        setpoint = self._setpoint()
        if setpoint is not None and setpoint.get("writable"):
            features |= ClimateEntityFeature.TARGET_TEMPERATURE
        system = self._system_function()
        if system is not None and system.get("mode_writable") and system.get("available_modes"):
            features |= ClimateEntityFeature.TURN_ON | ClimateEntityFeature.TURN_OFF
        return features

    @property
    def current_temperature(self) -> float | None:
        value = (self.coordinator.data or {}).get("room_temperature_c")
        return None if value is None else float(value)

    @property
    def target_temperature(self) -> float | None:
        setpoint = self._setpoint()
        return None if setpoint is None else float(setpoint["value_celsius"])

    @property
    def min_temp(self) -> float:
        setpoint = self._setpoint()
        return float(setpoint["min_celsius"]) if setpoint is not None else 5.0

    @property
    def max_temp(self) -> float:
        setpoint = self._setpoint()
        return float(setpoint["max_celsius"]) if setpoint is not None else 30.0

    @property
    def target_temperature_step(self) -> float | None:
        setpoint = self._setpoint()
        return None if setpoint is None else float(setpoint["step_celsius"])

    @property
    def hvac_modes(self) -> list[HVACMode]:
        system = self._system_function()
        if system is None:
            return [HVACMode.OFF]
        return [_EEBUS_TO_HA_MODE[mode] for mode in system.get("available_modes", []) if mode in _EEBUS_TO_HA_MODE] or [HVACMode.OFF]

    @property
    def hvac_mode(self) -> HVACMode | None:
        system = self._system_function()
        if system is None:
            return None
        mode = system.get("operation_mode")
        return _EEBUS_TO_HA_MODE.get(mode) if isinstance(mode, str) else None

    async def async_set_temperature(self, **kwargs: Any) -> None:
        await self.coordinator.async_set_room_heating_temperature(float(kwargs[ATTR_TEMPERATURE]))
        await self.coordinator.async_request_refresh()

    async def async_set_hvac_mode(self, hvac_mode: HVACMode) -> None:
        mode = _HA_TO_EEBUS_MODE.get(hvac_mode)
        if mode is None:
            raise ValueError(f"Unsupported HVAC mode: {hvac_mode}")
        await self.coordinator.async_set_room_heating_mode(mode)
        await self.coordinator.async_request_refresh()
