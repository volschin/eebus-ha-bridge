"""Tests for EEBUS diagnostics."""

from unittest.mock import AsyncMock, MagicMock

import pytest

from custom_components.eebus.diagnostics import async_get_config_entry_diagnostics
from custom_components.eebus.models import CapabilityState
from custom_components.eebus.session_diagnostics import (
    DeviceStreamDiagnostics,
    SessionDiagnostics,
    StreamWorkerDiagnostics,
)
from custom_components.eebus.state import (
    CapabilityKey,
    CapabilityMetadata,
    CapabilitiesState,
    ConnectionState,
    DeviceState,
    MeasurementsState,
    StateField,
)


@pytest.mark.asyncio
async def test_diagnostics_output():
    """Test diagnostics returns expected structure."""
    hass = MagicMock()
    entry = MagicMock()
    entry.data = {
        "grpc_host": "192.168.1.100",
        "grpc_port": 50051,
        "device_ski": "abcdef1234567890",
        "security_mode": "tls_token",
        "tls_ca_certificate": "diagnostics-secret-ca-material",
        "auth_token": "diagnostics-secret-token",
        "tls_private_key": "diagnostics-secret-private-key",
    }
    coordinator = MagicMock()
    coordinator.data = DeviceState(
        connection=ConnectionState(
            connected=True,
            local_ski="11223344556677889900AABBCCDDEEFF00112233",
        ),
        measurements=MeasurementsState(power_watts=1500.0),
        capabilities=CapabilitiesState(lpc=CapabilityState.AVAILABLE),
        capability_metadata=(
            CapabilityMetadata(
                capability=CapabilityKey.LPC,
                reason="supported",
                last_changed=None,
            ),
        ),
        fresh_fields=frozenset({StateField.POWER_WATTS}),
    )
    coordinator.async_session_diagnostics = AsyncMock(
        return_value=SessionDiagnostics(
            bridge_unavailable=False,
            last_successful_read_age_seconds=None,
            last_update_success=True,
            streams=DeviceStreamDiagnostics(
                last_device_state_revision=42,
                refresh_pending=False,
                refresh_running=False,
                primary=StreamWorkerDiagnostics(1, 1, 0, 3),
                legacy=StreamWorkerDiagnostics(0, 0, 0, 0),
            ),
            operational=None,
        )
    )
    entry.runtime_data = coordinator

    result = await async_get_config_entry_diagnostics(hass, entry)

    assert "config" in result
    assert result["config"]["grpc_host"] == "192.168.1.100"
    assert result["config"]["device_ski"] == "abcdef…7890"
    assert result["config"]["security_mode"] == "tls_token"
    assert result["config"]["tls_ca_certificate"] == "**REDACTED**"
    assert result["config"]["auth_token"] == "**REDACTED**"
    serialized = repr(result)
    assert "diagnostics-secret-token" not in serialized
    assert "diagnostics-secret-ca-material" not in serialized
    assert "diagnostics-secret-private-key" not in serialized
    assert "11223344556677889900AABBCCDDEEFF00112233" not in serialized
    assert result["coordinator_data"]["connection"]["local_ski"] == "112233…2233"
    assert "coordinator_data" in result
    assert result["capabilities"]["lpc"] == "available"
    assert result["capability_metadata"][0]["capability"] == "lpc"
    assert result["fresh_fields"] == ["power_watts"]
    assert result["recovery"] == {
        "bridge_unavailable": False,
        "last_successful_read_age_seconds": None,
        "last_update_success": True,
    }
    assert result["streams"]["last_device_state_revision"] == 42
    assert result["streams"]["primary"]["reconnects"] == 3
    assert result["bridge_operational"] is None
