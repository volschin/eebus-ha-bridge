"""EEBUS integration for Home Assistant."""

from __future__ import annotations

import logging
from typing import TYPE_CHECKING

from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant
from homeassistant.helpers import device_registry as dr
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
from .runtime import BridgeRuntimeRegistry
from .ski import is_valid_ski, normalize_ski

_LOGGER = logging.getLogger(__name__)
_RUNTIME_REGISTRY = "runtime_registry"

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

    raw_ski = config_entry.data[CONF_DEVICE_SKI]
    if raw_ski != canonical_ski:
        _migrate_device_registry_identifier(hass, raw_ski, canonical_ski)

    data = {**config_entry.data, CONF_DEVICE_SKI: canonical_ski}
    hass.config_entries.async_update_entry(
        config_entry,
        data=data,
        unique_id=canonical_ski,
        version=2,
    )
    return True


def _migrate_device_registry_identifier(
    hass: HomeAssistant, raw_ski: str, canonical_ski: str
) -> None:
    """Rename a device registry entry's identifier from raw to canonical SKI.

    EebusEntity keys device identifiers off the stored SKI. Without this, a
    canonicalized config entry re-registers under a new identifier and HA
    creates a second device, orphaning the original's area/history/automation
    references instead of reusing it.
    """
    device_registry = dr.async_get(hass)
    device = device_registry.async_get_device(identifiers={(DOMAIN, raw_ski)})
    if device is not None:
        device_registry.async_update_device(
            device.id, new_identifiers={(DOMAIN, canonical_ski)}
        )


async def async_setup_entry(hass: HomeAssistant, entry: EebusConfigEntry) -> bool:
    """Set up EEBUS from a config entry."""
    registry = _get_runtime_registry(hass)
    runtime = await registry.acquire(
        entry.data[CONF_GRPC_HOST],
        entry.data[CONF_GRPC_PORT],
        entry.data.get(CONF_SECURITY_MODE, SECURITY_MODE_LOOPBACK),
        entry.data.get(CONF_TLS_CA_CERTIFICATE),
        entry.data.get(CONF_AUTH_TOKEN),
    )
    coordinator: EebusCoordinator | None = None
    try:
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
            runtime=runtime,
        )
        await coordinator.async_config_entry_first_refresh()
        coordinator.async_start_streams()
        coordinator.async_start_grid_push()
        coordinator.async_start_pv_push()
        coordinator.async_start_battery_push()

        entry.runtime_data = coordinator

        _remove_replaced_dhw_entities(hass, coordinator.ski)
        _remove_replaced_heartbeat_switch(hass, coordinator.ski)

        await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)

        entry.async_on_unload(entry.add_update_listener(_async_reload_entry))
    except BaseException:
        try:
            if coordinator is not None:
                await coordinator.async_shutdown()
        finally:
            await registry.release(runtime)
        raise

    return True


def _get_runtime_registry(hass: HomeAssistant) -> BridgeRuntimeRegistry:
    """Return the Home Assistant instance's shared runtime registry."""
    if not isinstance(hass.data, dict):
        hass.data = {}
    domain_data = hass.data.setdefault(DOMAIN, {})
    registry = domain_data.get(_RUNTIME_REGISTRY)
    if not isinstance(registry, BridgeRuntimeRegistry):
        registry = BridgeRuntimeRegistry()
        domain_data[_RUNTIME_REGISTRY] = registry
    return registry


def _remove_replaced_dhw_entities(hass: HomeAssistant, ski: str) -> None:
    """Remove registry entries superseded by the combined water-heater entity."""
    entity_registry = er.async_get(hass)
    for domain, unique_id in (
        ("number", f"{ski}_dhw_setpoint"),
        ("select", f"{ski}_dhw_operation_mode"),
    ):
        if entity_id := entity_registry.async_get_entity_id(domain, DOMAIN, unique_id):
            entity_registry.async_remove(entity_id)


def _remove_replaced_heartbeat_switch(hass: HomeAssistant, ski: str) -> None:
    """Remove the registry entry for the retired per-device heartbeat switch."""
    entity_registry = er.async_get(hass)
    if entity_id := entity_registry.async_get_entity_id(
        "switch", DOMAIN, f"{ski}_heartbeat"
    ):
        entity_registry.async_remove(entity_id)


async def async_unload_entry(hass: HomeAssistant, entry: EebusConfigEntry) -> bool:
    """Unload EEBUS config entry."""
    coordinator = entry.runtime_data
    async with coordinator.reconfigure_lock:
        if unload_ok := await hass.config_entries.async_unload_platforms(
            entry, PLATFORMS
        ):
            coordinator.mark_entry_unloaded()
            try:
                await coordinator.async_shutdown()
            finally:
                await _get_runtime_registry(hass).release(coordinator.runtime)
    return unload_ok


async def async_remove_config_entry_device(
    hass: HomeAssistant, config_entry: ConfigEntry, device_entry: dr.DeviceEntry
) -> bool:
    """Allow removing a stale device left behind by a SKI canonicalization.

    A device is only removable if none of its identifiers match the entry's
    current canonical SKI — i.e. it's an orphan from before the identifier
    was renamed, not the device the entry is actively using.
    """
    current_ski = config_entry.data[CONF_DEVICE_SKI]
    return not any(
        domain == DOMAIN and ski == current_ski
        for domain, ski in device_entry.identifiers
    )


async def _async_reload_entry(hass: HomeAssistant, entry: EebusConfigEntry) -> None:
    """Atomically hand an entry over while preserving exact runtime ownership."""
    registry = _get_runtime_registry(hass)
    coordinator = entry.runtime_data
    async with coordinator.reconfigure_lock:
        if coordinator.entry_unloaded is True:
            return
        current = coordinator.runtime
        replacement = await registry.acquire(
            entry.data[CONF_GRPC_HOST],
            entry.data[CONF_GRPC_PORT],
            entry.data.get(CONF_SECURITY_MODE, SECURITY_MODE_LOOPBACK),
            entry.data.get(CONF_TLS_CA_CERTIFICATE),
            entry.data.get(CONF_AUTH_TOKEN),
        )
        if replacement is current:
            # Provider-only option changes do not need to tear down the shared
            # transport or the per-SKI session at all.
            await registry.release(replacement)
            await coordinator.async_reconfigure_providers(
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
            return

        try:
            await coordinator.async_reconfigure_runtime(
                replacement,
                host=entry.data[CONF_GRPC_HOST],
                port=entry.data[CONF_GRPC_PORT],
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
        finally:
            # Reconfiguration may be cancelled on either side of its commit point.
            # The coordinator's active runtime is the authoritative ownership bit.
            if coordinator.runtime is replacement:
                await registry.release(current)
            else:
                await registry.release(replacement)
