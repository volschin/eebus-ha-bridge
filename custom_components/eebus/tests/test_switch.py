"""Tests for EEBUS switch entities."""

from unittest.mock import MagicMock

from homeassistant.const import EntityCategory

from custom_components.eebus.switch import EebusHeartbeatSwitch, EebusLPCActiveSwitch


def test_lpc_is_primary_control_while_heartbeat_is_configuration():
    """Classify operational and technical switches separately."""
    coordinator = MagicMock()
    coordinator.data = {"connected": True}
    coordinator.ski = "test-ski"

    assert EebusLPCActiveSwitch(coordinator).entity_category is None
    assert EebusHeartbeatSwitch(coordinator).entity_category == EntityCategory.CONFIG
