"""Diagnostics for the EEBUS integration."""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import asdict, is_dataclass
from datetime import UTC, datetime
from enum import Enum
from typing import Any

from homeassistant.core import HomeAssistant

from . import EebusConfigEntry
from .const import SECURITY_MODE_LOOPBACK
from .models import measurement_diagnostics

_REDACTED = "**REDACTED**"
_SECRET_KEYS = {
    "auth_token",
    "tls_ca_certificate",
    "tls_private_key",
    "token",
    "certificate",
    "private_key",
}
_SECRET_KEY_MARKERS = ("token", "password", "secret", "privatekey", "certificate", "cert", "pem")


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
            "tls_ca_certificate": _REDACTED if entry.data.get("tls_ca_certificate") else None,
            "auth_token": _REDACTED if entry.data.get("auth_token") else None,
            "device_ski": _redact_ski(entry.data.get("device_ski")),
        },
        "coordinator_data": _sanitize(coordinator.data) if coordinator.data else None,
        "capabilities": _capabilities(coordinator.data),
        "capability_metadata": _capability_metadata(coordinator.data),
        "fresh_fields": _fresh_fields(coordinator.data),
        "recovery": _recovery_diagnostics(coordinator),
        "streams": _stream_diagnostics(coordinator),
        "measurements": measurement_diagnostics(),
    }


def _capabilities(data: Any) -> dict[str, Any] | None:
    capabilities = getattr(data, "capabilities", None)
    return _sanitize(capabilities) if capabilities is not None else None


def _capability_metadata(data: Any) -> list[dict[str, Any]]:
    metadata = getattr(data, "capability_metadata", ()) or ()
    return [_sanitize(entry) for entry in metadata]


def _fresh_fields(data: Any) -> list[str]:
    fields: frozenset[Any] = getattr(data, "fresh_fields", frozenset()) or frozenset()
    return sorted(str(getattr(field, "value", field)) for field in fields)


def _recovery_diagnostics(coordinator: Any) -> dict[str, Any]:
    runtime = getattr(coordinator, "runtime", None)
    status = getattr(runtime, "status", None)
    return {
        "bridge_unavailable": bool(getattr(status, "unavailable", False)),
        "last_successful_read_age_seconds": _last_successful_read_age_seconds(
            coordinator
        ),
        "last_update_success": getattr(coordinator, "last_update_success", None),
    }


def _last_successful_read_age_seconds(coordinator: Any) -> float | None:
    timestamp = getattr(coordinator, "last_update_success_time", None)
    if not isinstance(timestamp, datetime):
        return None
    if timestamp.tzinfo is None:
        timestamp = timestamp.replace(tzinfo=UTC)
    return max(0.0, (datetime.now(UTC) - timestamp.astimezone(UTC)).total_seconds())


def _stream_diagnostics(coordinator: Any) -> dict[str, Any] | None:
    streams = getattr(coordinator, "_device_streams", None)
    diagnostics = getattr(streams, "diagnostics", None)
    if not callable(diagnostics):
        return None
    sanitized = _sanitize(diagnostics())
    return sanitized if isinstance(sanitized, dict) else None


def _sanitize(value: Any) -> Any:
    if is_dataclass(value) and not isinstance(value, type):
        return _sanitize(asdict(value))
    if isinstance(value, Mapping):
        sanitized: dict[str, Any] = {}
        for key, item in value.items():
            key_text = str(key)
            sanitized_key = _sanitize_mapping_key(key_text)
            if _is_sensitive_key(key_text):
                sanitized[sanitized_key] = _REDACTED if item else None
            else:
                sanitized[sanitized_key] = _sanitize(item)
        return sanitized
    if isinstance(value, (list, tuple, set, frozenset)):
        return [_sanitize(item) for item in value]
    if isinstance(value, Enum):
        return value.value
    if isinstance(value, datetime):
        return value.isoformat()
    if isinstance(value, str):
        return _sanitize_string(value)
    return value


def _sanitize_mapping_key(value: str) -> str:
    if _is_full_ski(value):
        return _redact_ski(value) or _REDACTED
    if _contains_pem(value):
        return _REDACTED
    return value


def _sanitize_string(value: str) -> str:
    if _contains_pem(value):
        return _REDACTED
    if _is_full_ski(value):
        return _redact_ski(value) or _REDACTED
    return value


def _is_sensitive_key(value: str) -> bool:
    normalized = "".join(c for c in value.casefold() if c.isalnum())
    if value.casefold() in _SECRET_KEYS:
        return True
    return any(marker in normalized for marker in _SECRET_KEY_MARKERS)


def _contains_pem(value: str) -> bool:
    return "-----BEGIN " in value and "-----END " in value


def _is_full_ski(value: str) -> bool:
    compact = (
        value.replace(":", "")
        .replace(" ", "")
        .replace("-", "")
        .replace("_", "")
    )
    return len(compact) == 40 and all(c in "0123456789abcdefABCDEF" for c in compact)


def _redact_ski(value: object) -> str | None:
    if not isinstance(value, str) or not value:
        return None
    compact = (
        value.replace(":", "")
        .replace(" ", "")
        .replace("-", "")
        .replace("_", "")
    )
    if len(compact) < 10:
        return _REDACTED
    return f"{compact[:6]}…{compact[-4:]}"
