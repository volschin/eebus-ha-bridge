"""Diagnostics for the EEBUS integration."""

from __future__ import annotations

from dataclasses import asdict
from typing import Any

from homeassistant.core import HomeAssistant

from . import EebusConfigEntry
from .const import SECURITY_MODE_LOOPBACK


async def async_get_config_entry_diagnostics(
    hass: HomeAssistant,
    entry: EebusConfigEntry,
) -> dict[str, Any]:
    """Return diagnostics for a config entry."""
    coordinator = entry.runtime_data

    return {
        "config": {
            "grpc_host": entry.data.get("grpc_host"),
            "grpc_port": entry.data.get("grpc_port"),
            "security_mode": entry.data.get("security_mode", SECURITY_MODE_LOOPBACK),
            "tls_ca_certificate": "**REDACTED**" if entry.data.get("tls_ca_certificate") else None,
            "auth_token": "**REDACTED**" if entry.data.get("auth_token") else None,
            "device_ski": "**REDACTED**",
        },
        "coordinator_data": asdict(coordinator.data) if coordinator.data else None,
    }
