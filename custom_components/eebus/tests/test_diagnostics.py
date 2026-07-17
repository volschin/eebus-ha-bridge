"""Tests for EEBUS diagnostics."""

from unittest.mock import MagicMock

import pytest

from custom_components.eebus.diagnostics import async_get_config_entry_diagnostics
from custom_components.eebus.state import ConnectionState, DeviceState, MeasurementsState


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
        connection=ConnectionState(connected=True),
        measurements=MeasurementsState(power_watts=1500.0),
    )
    entry.runtime_data = coordinator

    result = await async_get_config_entry_diagnostics(hass, entry)

    assert "config" in result
    assert result["config"]["grpc_host"] == "192.168.1.100"
    assert result["config"]["device_ski"] == "**REDACTED**"
    assert result["config"]["security_mode"] == "tls_token"
    assert result["config"]["tls_ca_certificate"] == "**REDACTED**"
    assert result["config"]["auth_token"] == "**REDACTED**"
    serialized = repr(result)
    assert "diagnostics-secret-token" not in serialized
    assert "diagnostics-secret-ca-material" not in serialized
    assert "diagnostics-secret-private-key" not in serialized
    assert "coordinator_data" in result
