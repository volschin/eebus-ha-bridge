"""Tests for the EEBUS config flow."""

import pytest

from custom_components.eebus.config_flow import EebusConfigFlow
from custom_components.eebus.const import DOMAIN


def test_config_flow_domain():
    """Test that config flow has correct domain."""
    assert EebusConfigFlow.DOMAIN == DOMAIN
