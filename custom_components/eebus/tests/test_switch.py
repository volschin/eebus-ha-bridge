"""Tests for EEBUS switch entities."""

from unittest.mock import MagicMock

from custom_components.eebus.switch import EebusLPCActiveSwitch


def test_lpc_is_primary_control():
    """Classify LPC as an operational switch."""
    coordinator = MagicMock()
    coordinator.data = {"connected": True}
    coordinator.ski = "test-ski"

    assert EebusLPCActiveSwitch(coordinator).entity_category is None
