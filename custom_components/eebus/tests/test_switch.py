"""Tests for EEBUS switch entities."""

from unittest.mock import MagicMock

from custom_components.eebus.models import CapabilityState, ConsumptionLimitState
from custom_components.eebus.state import (
    CapabilitiesState,
    ConnectionState,
    DeviceState,
    LPCState,
)
from custom_components.eebus.switch import EebusLPCActiveSwitch


def test_lpc_is_primary_control():
    """Classify LPC as an operational switch."""
    coordinator = MagicMock()
    coordinator.data = DeviceState(connection=ConnectionState(connected=True))
    coordinator.ski = "test-ski"

    assert EebusLPCActiveSwitch(coordinator).entity_category is None


def test_lpc_control_is_unavailable_while_value_is_stale() -> None:
    coordinator = MagicMock()
    coordinator.ski = "test-ski"
    coordinator.last_update_success = True
    coordinator.data = DeviceState(
        connection=ConnectionState(connected=True),
        lpc=LPCState(consumption_limit=ConsumptionLimitState(4200.0, True, True)),
        capabilities=CapabilitiesState(lpc=CapabilityState.TEMPORARILY_UNAVAILABLE),
        fresh_fields=frozenset(),
    )
    switch = EebusLPCActiveSwitch(coordinator)
    assert switch.is_on is True
    assert switch.available is False
