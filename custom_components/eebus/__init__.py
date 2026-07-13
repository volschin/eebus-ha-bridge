"""EEBUS integration for Home Assistant."""

from __future__ import annotations

from typing import TYPE_CHECKING

from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant
from homeassistant.helpers import entity_registry as er

from .const import (
    CONF_BATTERY_CHARGED_ENERGY_ENTITY,
    CONF_BATTERY_DISCHARGED_ENERGY_ENTITY,
    CONF_BATTERY_POWER_ENTITY,
    CONF_BATTERY_SOC_ENTITY,
    CONF_DEVICE_SKI,
    CONF_GRID_CONSUMPTION_ENERGY_ENTITY,
    CONF_GRID_FEED_IN_ENERGY_ENTITY,
    CONF_GRID_POWER_ENTITY,
    CONF_GRPC_HOST,
    CONF_GRPC_PORT,
    CONF_PV_PEAK_POWER_ENTITY,
    CONF_PV_POWER_ENTITY,
    CONF_PV_YIELD_ENERGY_ENTITY,
    DOMAIN,
    PLATFORMS,
)
from .coordinator import EebusCoordinator

if TYPE_CHECKING:
    EebusConfigEntry = ConfigEntry[EebusCoordinator]
else:
    EebusConfigEntry = ConfigEntry


async def async_setup_entry(hass: HomeAssistant, entry: EebusConfigEntry) -> bool:
    """Set up EEBUS from a config entry."""
    coordinator = EebusCoordinator(
        hass,
        host=entry.data[CONF_GRPC_HOST],
        port=entry.data[CONF_GRPC_PORT],
        ski=entry.data[CONF_DEVICE_SKI],
        grid_power_entity=entry.options.get(CONF_GRID_POWER_ENTITY) or None,
        grid_feed_in_energy_entity=entry.options.get(CONF_GRID_FEED_IN_ENERGY_ENTITY) or None,
        grid_consumption_energy_entity=entry.options.get(CONF_GRID_CONSUMPTION_ENERGY_ENTITY) or None,
        pv_power_entity=entry.options.get(CONF_PV_POWER_ENTITY) or None,
        pv_yield_energy_entity=entry.options.get(CONF_PV_YIELD_ENERGY_ENTITY) or None,
        pv_peak_power_entity=entry.options.get(CONF_PV_PEAK_POWER_ENTITY) or None,
        battery_power_entity=entry.options.get(CONF_BATTERY_POWER_ENTITY) or None,
        battery_charged_energy_entity=entry.options.get(CONF_BATTERY_CHARGED_ENERGY_ENTITY) or None,
        battery_discharged_energy_entity=entry.options.get(CONF_BATTERY_DISCHARGED_ENERGY_ENTITY) or None,
        battery_soc_entity=entry.options.get(CONF_BATTERY_SOC_ENTITY) or None,
    )
    await coordinator.async_config_entry_first_refresh()
    coordinator.async_start_streams()
    coordinator.async_start_grid_push()
    coordinator.async_start_pv_push()
    coordinator.async_start_battery_push()

    entry.runtime_data = coordinator

    _remove_replaced_dhw_entities(hass, coordinator.ski)

    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)

    entry.async_on_unload(entry.add_update_listener(_async_reload_entry))

    return True


def _remove_replaced_dhw_entities(hass: HomeAssistant, ski: str) -> None:
    """Remove registry entries superseded by the combined water-heater entity."""
    entity_registry = er.async_get(hass)
    for domain, unique_id in (
        ("number", f"{ski}_dhw_setpoint"),
        ("select", f"{ski}_dhw_operation_mode"),
    ):
        if entity_id := entity_registry.async_get_entity_id(domain, DOMAIN, unique_id):
            entity_registry.async_remove(entity_id)


async def async_unload_entry(hass: HomeAssistant, entry: EebusConfigEntry) -> bool:
    """Unload EEBUS config entry."""
    if unload_ok := await hass.config_entries.async_unload_platforms(entry, PLATFORMS):
        await entry.runtime_data.async_shutdown()
    return unload_ok


async def _async_reload_entry(hass: HomeAssistant, entry: EebusConfigEntry) -> None:
    """Reload on options change."""
    await hass.config_entries.async_reload(entry.entry_id)
