"""Tests for EEBUS diagnostics."""

from unittest.mock import MagicMock

import pytest

from custom_components.eebus.diagnostics import async_get_config_entry_diagnostics
from custom_components.eebus.models import CapabilityState
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
    coordinator.last_update_success_time = None
    coordinator.last_update_success = True
    coordinator.runtime.status.unavailable = False
    leaked_ski = "AABBCCDDEEFF00112233445566778899AABBCCDD"
    coordinator._device_streams.diagnostics.return_value = {
        "last_device_state_revision": 42,
        "primary": {"configured": 1, "running": 1, "done": 0},
        "legacy": {"configured": 0, "running": 0, "done": 0},
        "nested": {
            "ski_values": [leaked_ski],
            leaked_ski: "map-key",
            "payload": "-----BEGIN CERTIFICATE-----\nsecret-pem\n-----END CERTIFICATE-----",
            "access_token": "nested-access-token",
            "api_token": "nested-api-token",
        },
    }
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
    assert leaked_ski not in serialized
    assert "secret-pem" not in serialized
    assert "nested-access-token" not in serialized
    assert "nested-api-token" not in serialized
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
    assert result["streams"]["nested"]["ski_values"] == ["AABBCC…CCDD"]
    assert result["streams"]["nested"]["AABBCC…CCDD"] == "map-key"
    assert result["streams"]["nested"]["payload"] == "**REDACTED**"
    assert result["streams"]["nested"]["access_token"] == "**REDACTED**"
    assert result["streams"]["nested"]["api_token"] == "**REDACTED**"
