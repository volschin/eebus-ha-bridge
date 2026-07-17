"""Tests for EEBUS number entities."""

from unittest.mock import MagicMock

from homeassistant.const import EntityCategory

from custom_components.eebus.number import EebusLPCLimitNumber
from custom_components.eebus.models import CapabilityState, ConsumptionLimitState
from custom_components.eebus.state import (
    CapabilitiesState,
    ConnectionState,
    DeviceState,
    LPCState,
    StateField,
)


def test_lpc_limit_value():
    """Test LPC limit number returns correct value."""
    coordinator = MagicMock()
    coordinator.data = DeviceState(
        connection=ConnectionState(connected=True),
        lpc=LPCState(consumption_limit=ConsumptionLimitState(4200.0, True, True)),
        capabilities=CapabilitiesState(lpc=CapabilityState.AVAILABLE),
        fresh_fields=frozenset({StateField.CONSUMPTION_LIMIT}),
    )
    coordinator.ski = "test-ski"

    number = EebusLPCLimitNumber(coordinator)
    assert number.entity_category == EntityCategory.CONFIG
    assert number.native_value == 4200.0
