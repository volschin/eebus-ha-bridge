"""EEBUS integration for Home Assistant."""

from __future__ import annotations

import logging
from typing import TYPE_CHECKING

from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant
from homeassistant.helpers import entity_registry as er

from .const import (
    CONF_BATTERY_CHARGED_ENERGY_ENTITY,
    CONF_BATTERY_DISCHARGED_ENERGY_ENTITY,
    CONF_BATTERY_POWER_ENTITY,
    CONF_BATTERY_SOC_ENTITY,
    CONF_AUTH_TOKEN,
    CONF_DEVICE_SKI,
    CONF_GRID_CONSUMPTION_ENERGY_ENTITY,
    CONF_GRID_FEED_IN_ENERGY_ENTITY,
    CONF_GRID_POWER_ENTITY,
    CONF_GRPC_HOST,
    CONF_GRPC_PORT,
    CONF_SECURITY_MODE,
    CONF_TLS_CA_CERTIFICATE,
    CONF_PV_PEAK_POWER_ENTITY,
    CONF_PV_POWER_ENTITY,
    CONF_PV_YIELD_ENERGY_ENTITY,
    DOMAIN,
    PLATFORMS,
    SECURITY_MODE_LOOPBACK,
)
from .coordinator import EebusCoordinator
from .ski import is_valid_ski, normalize_ski

_LOGGER = logging.getLogger(__name__)

if TYPE_CHECKING:
    EebusConfigEntry = ConfigEntry[EebusCoordinator]
else:
    EebusConfigEntry = ConfigEntry


async def async_migrate_entry(
    hass: HomeAssistant, config_entry: ConfigEntry
) -> bool:
    """Migrate an EEBUS config entry to canonical SKI storage."""
    if config_entry.version >= 2:
        return True

    canonical_ski = normalize_ski(config_entry.data[CONF_DEVICE_SKI])
    if not is_valid_ski(canonical_ski):
        _LOGGER.warning(
            "Cannot migrate EEBUS config entry %s (%s): stored SKI %r is not a "
            "valid 40-character hexadecimal fingerprint",
            config_entry.entry_id,
            config_entry.title,
            config_entry.data[CONF_DEVICE_SKI],
        )
        return False

    for other_entry in hass.config_entries.async_entries(DOMAIN):
        if other_entry.entry_id == config_entry.entry_id:
            continue
        other_ski = other_entry.unique_id
        if other_ski is None or normalize_ski(other_ski) != canonical_ski:
            continue
        if other_entry.entry_id < config_entry.entry_id:
            _LOGGER.warning(
                "Cannot migrate duplicate EEBUS config entry %s (%s): canonical "
                "SKI %s conflicts with older entry %s (%s)",
                config_entry.entry_id,
                config_entry.title,
                canonical_ski,
                other_entry.entry_id,
                other_entry.title,
            )
            return False

    if (
        config_entry.data[CONF_DEVICE_SKI] == canonical_ski
        and config_entry.unique_id == canonical_ski
    ):
        hass.config_entries.async_update_entry(config_entry, version=2)
        return True

    data = {**config_entry.data, CONF_DEVICE_SKI: canonical_ski}
    hass.config_entries.async_update_entry(
        config_entry,
        data=data,
        unique_id=canonical_ski,
        version=2,
    )
    return True


async def async_setup_entry(hass: HomeAssistant, entry: EebusConfigEntry) -> bool:
    """Set up EEBUS from a config entry."""
    coordinator = EebusCoordinator(
        hass,
        host=entry.data[CONF_GRPC_HOST],
        port=entry.data[CONF_GRPC_PORT],
        ski=entry.data[CONF_DEVICE_SKI],
        security_mode=entry.data.get(CONF_SECURITY_MODE, SECURITY_MODE_LOOPBACK),
        tls_ca_certificate=entry.data.get(CONF_TLS_CA_CERTIFICATE),
        auth_token=entry.data.get(CONF_AUTH_TOKEN),
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
