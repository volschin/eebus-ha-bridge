"""Tests for the EEBUS config flow."""

import json
from pathlib import Path
from unittest.mock import AsyncMock, patch

from homeassistant.data_entry_flow import FlowResultType

from custom_components.eebus.config_flow import EebusConfigFlow, _normalize_ski
from custom_components.eebus.const import CONF_DEVICE_SKI, DOMAIN


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


def test_manifest_declares_ship_zeroconf():
    """Test that the manifest registers SHIP mDNS discovery."""
    manifest = json.loads(
        (Path(__file__).parent.parent / "manifest.json").read_text()
    )
    assert manifest["zeroconf"] == ["_ship._tcp.local."]
