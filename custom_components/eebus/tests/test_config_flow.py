"""Tests for the EEBUS config flow."""

import json
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

from homeassistant.data_entry_flow import FlowResultType

from custom_components.eebus.config_flow import (
    EebusConfigFlow,
    EebusOptionsFlow,
    _normalize_ski,
)
from custom_components.eebus.const import (
    CONF_AUTH_TOKEN,
    CONF_DEVICE_SKI,
    CONF_GRID_FEED_IN_ENERGY_ENTITY,
    CONF_GRID_POWER_ENTITY,
    CONF_GRPC_HOST,
    CONF_GRPC_PORT,
    CONF_SECURITY_MODE,
    CONF_TLS_CA_CERTIFICATE,
    DOMAIN,
    SECURITY_MODE_TLS_TOKEN,
)


def test_config_flow_domain():
    """Test that config flow has correct domain."""
    assert EebusConfigFlow.DOMAIN == DOMAIN


def test_config_flow_supports_zeroconf():
    """Test that the flow implements a zeroconf discovery step."""
    assert hasattr(EebusConfigFlow, "async_step_zeroconf")


def test_normalize_ski_strips_colons_and_case():
    """SKI normalization ignores colons, spaces, and casing."""
    assert _normalize_ski("96:81:87:DB ") == "968187db"
    assert _normalize_ski("ABCD") == "abcd"


async def test_device_step_rejects_local_ski():
    """Entering the bridge's own SKI is rejected, even with colons/casing."""
    flow = EebusConfigFlow()
    flow._local_ski = "968187db034cad41dab545c32a174ed7cc2fd8a5"
    flow._host = "localhost"
    flow._port = 50051

    typed = "96:81:87:DB:03:4C:AD:41:DA:B5:45:C3:2A:17:4E:D7:CC:2F:D8:A5"
    with patch.object(
        flow, "_async_list_discovered_skis", AsyncMock(return_value=[])
    ):
        result = await flow.async_step_device({CONF_DEVICE_SKI: typed})

    assert result["type"] == FlowResultType.FORM
    assert result["errors"][CONF_DEVICE_SKI] == "ski_is_local"


def test_config_flow_exposes_options_flow():
    """The config flow advertises the grid-sensor options flow."""
    assert hasattr(EebusConfigFlow, "async_get_options_flow")


async def test_user_step_continues_to_security_step():
    """Host/port selection is followed by explicit transport security."""
    flow = EebusConfigFlow()
    result = await flow.async_step_user(
        {CONF_GRPC_HOST: "localhost", CONF_GRPC_PORT: 50051}
    )
    assert result["type"] == FlowResultType.FORM
    assert result["step_id"] == "security"


async def test_tls_security_step_requires_ca_and_token():
    """TLS mode cannot probe or continue with incomplete credentials."""
    flow = EebusConfigFlow()
    flow._host = "bridge.example.test"
    flow._port = 50051
    result = await flow.async_step_security(
        {CONF_SECURITY_MODE: SECURITY_MODE_TLS_TOKEN}
    )
    assert result["type"] == FlowResultType.FORM
    assert result["errors"] == {
        CONF_TLS_CA_CERTIFICATE: "required",
        CONF_AUTH_TOKEN: "required",
    }


async def test_tls_security_step_probes_with_credentials():
    """The security step retains TLS credentials for subsequent RPCs."""
    flow = EebusConfigFlow()
    flow._host = "bridge.example.test"
    flow._port = 50051
    with (
        patch.object(flow, "_async_probe_bridge", AsyncMock(return_value="local-ski")) as probe,
        patch.object(flow, "_async_list_discovered_skis", AsyncMock(return_value=[])),
    ):
        result = await flow.async_step_security(
            {
                CONF_SECURITY_MODE: SECURITY_MODE_TLS_TOKEN,
                CONF_TLS_CA_CERTIFICATE: "test-ca",
                CONF_AUTH_TOKEN: "test-token",
            }
        )
    assert result["type"] == FlowResultType.FORM
    assert result["step_id"] == "device"
    probe.assert_awaited_once_with("bridge.example.test", 50051)
    assert flow._tls_ca_certificate == "test-ca"
    assert flow._auth_token == "test-token"


async def test_reconfigure_security_preserves_blank_existing_token():
    """Reconfigure verifies the secure channel and retains an omitted token."""
    flow = EebusConfigFlow()
    flow._host = "bridge.example.test"
    flow._port = 50052
    entry = MagicMock()
    entry.data = {
        CONF_SECURITY_MODE: SECURITY_MODE_TLS_TOKEN,
        CONF_TLS_CA_CERTIFICATE: "old-ca",
        CONF_AUTH_TOKEN: "existing-token",
    }
    update_result = {"type": FlowResultType.ABORT}
    with (
        patch.object(flow, "_get_reconfigure_entry", return_value=entry),
        patch.object(flow, "_async_probe_bridge", AsyncMock(return_value="local-ski")),
        patch.object(
            flow, "async_update_reload_and_abort", return_value=update_result
        ) as update,
    ):
        result = await flow.async_step_reconfigure_security(
            {
                CONF_SECURITY_MODE: SECURITY_MODE_TLS_TOKEN,
                CONF_TLS_CA_CERTIFICATE: "new-ca",
                CONF_AUTH_TOKEN: "",
            }
        )

    assert result is update_result
    assert flow._auth_token == "existing-token"
    assert update.call_args.kwargs["data_updates"] == {
        CONF_GRPC_HOST: "bridge.example.test",
        CONF_GRPC_PORT: 50052,
        CONF_SECURITY_MODE: SECURITY_MODE_TLS_TOKEN,
        CONF_TLS_CA_CERTIFICATE: "new-ca",
        CONF_AUTH_TOKEN: "existing-token",
    }


async def test_reauth_replaces_and_verifies_credentials():
    """Reauthentication replaces both credentials only after a successful probe."""
    flow = EebusConfigFlow()
    flow._host = "bridge.example.test"
    flow._port = 50051
    flow._security_mode = SECURITY_MODE_TLS_TOKEN
    flow._tls_ca_certificate = "old-ca"
    entry = MagicMock()
    update_result = {"type": FlowResultType.ABORT}
    with (
        patch.object(flow, "_get_reauth_entry", return_value=entry),
        patch.object(flow, "_async_probe_bridge", AsyncMock(return_value="local-ski")),
        patch.object(
            flow, "async_update_reload_and_abort", return_value=update_result
        ) as update,
    ):
        result = await flow.async_step_reauth_confirm(
            {
                CONF_TLS_CA_CERTIFICATE: "replacement-ca",
                CONF_AUTH_TOKEN: "replacement-token",
            }
        )

    assert result is update_result
    assert update.call_args.kwargs["data_updates"] == {
        CONF_TLS_CA_CERTIFICATE: "replacement-ca",
        CONF_AUTH_TOKEN: "replacement-token",
    }
    assert update.call_args.kwargs["reason"] == "reauth_successful"


async def test_options_flow_strips_empty_selections():
    """Submitting the options form drops cleared fields so they are removed."""
    flow = EebusOptionsFlow()
    result = await flow.async_step_init(
        {
            CONF_GRID_POWER_ENTITY: "sensor.grid_power",
            CONF_GRID_FEED_IN_ENERGY_ENTITY: "",
        }
    )
    assert result["type"] == FlowResultType.CREATE_ENTRY
    assert result["data"] == {CONF_GRID_POWER_ENTITY: "sensor.grid_power"}


def test_manifest_declares_ship_zeroconf():
    """Test that the manifest registers SHIP mDNS discovery."""
    manifest = json.loads(
        (Path(__file__).parent.parent / "manifest.json").read_text()
    )
    assert manifest["zeroconf"] == ["_ship._tcp.local."]
