"""Tests for EEBUS number entities."""

from unittest.mock import MagicMock

import pytest

from custom_components.eebus.number import EebusLPCLimitNumber


def test_lpc_limit_value():
    """Test LPC limit number returns correct value."""
    coordinator = MagicMock()
    coordinator.data = {
        "consumption_limit": {"value_watts": 4200.0, "is_active": True, "is_changeable": True},
        "connected": True,
    }
    coordinator.ski = "test-ski"

    number = EebusLPCLimitNumber(coordinator)
    assert number.native_value == 4200.0
