"""EEBUS integration for Home Assistant."""

from __future__ import annotations

from typing import TYPE_CHECKING

from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant

from .const import CONF_DEVICE_SKI, CONF_GRPC_HOST, CONF_GRPC_PORT, PLATFORMS
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
    )
    await coordinator.async_config_entry_first_refresh()
    coordinator.async_start_streams()

    entry.runtime_data = coordinator

    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)

    entry.async_on_unload(entry.add_update_listener(_async_reload_entry))

    return True


async def async_unload_entry(hass: HomeAssistant, entry: EebusConfigEntry) -> bool:
    """Unload EEBUS config entry."""
    if unload_ok := await hass.config_entries.async_unload_platforms(entry, PLATFORMS):
        await entry.runtime_data.async_shutdown()
    return unload_ok


async def _async_reload_entry(hass: HomeAssistant, entry: EebusConfigEntry) -> None:
    """Reload on options change."""
    await hass.config_entries.async_reload(entry.entry_id)
