"""Tests for the EEBUS config flow."""

import json
from pathlib import Path

from custom_components.eebus.config_flow import EebusConfigFlow
from custom_components.eebus.const import DOMAIN


def test_config_flow_domain():
    """Test that config flow has correct domain."""
    assert EebusConfigFlow.DOMAIN == DOMAIN


def test_config_flow_supports_zeroconf():
    """Test that the flow implements a zeroconf discovery step."""
    assert hasattr(EebusConfigFlow, "async_step_zeroconf")


def test_manifest_declares_ship_zeroconf():
    """Test that the manifest registers SHIP mDNS discovery."""
    manifest = json.loads(
        (Path(__file__).parent.parent / "manifest.json").read_text()
    )
    assert manifest["zeroconf"] == ["_ship._tcp.local."]
