"""Tests for the grouped EEBUS domain state and pure reducer."""

from dataclasses import FrozenInstanceError

import grpc
import pytest

from custom_components.eebus.models import CapabilityState
from custom_components.eebus.state import (
    CapabilitiesState,
    DHWState,
    DomainState,
    apply_reading,
    flatten,
    next_capability_state,
)


@pytest.mark.parametrize(
    ("current", "status", "expected"),
    [
        (CapabilityState.UNKNOWN, None, CapabilityState.AVAILABLE),
        (
            CapabilityState.UNKNOWN,
            grpc.StatusCode.UNIMPLEMENTED,
            CapabilityState.UNSUPPORTED,
        ),
        (
            CapabilityState.UNKNOWN,
            grpc.StatusCode.NOT_FOUND,
            CapabilityState.TEMPORARILY_UNAVAILABLE,
        ),
        (
            CapabilityState.AVAILABLE,
            grpc.StatusCode.UNAVAILABLE,
            CapabilityState.TEMPORARILY_UNAVAILABLE,
        ),
        (
            CapabilityState.AVAILABLE,
            grpc.StatusCode.INTERNAL,
            CapabilityState.AVAILABLE,
        ),
    ],
)
def test_next_capability_state_transitions(
    current: CapabilityState,
    status: grpc.StatusCode | None,
    expected: CapabilityState,
) -> None:
    """Capability outcomes follow the shared transition table."""
    assert next_capability_state(current, status) == expected


def test_apply_reading_updates_value_and_capability_immutably() -> None:
    """One reducer call replaces both state parts without mutating its inputs."""
    group = DHWState()
    capabilities = CapabilitiesState()
    value = {
        "value_celsius": 48.0,
        "min_celsius": 35.0,
        "max_celsius": 70.0,
        "step_celsius": 1.0,
        "writable": True,
    }

    updated_group, updated_capabilities = apply_reading(
        group, "setpoint", value, capabilities, "dhw", None
    )

    assert group.setpoint is None
    assert capabilities.dhw == CapabilityState.UNKNOWN
    assert updated_group is not group
    assert updated_capabilities is not capabilities
    assert updated_group.setpoint == value
    assert updated_capabilities.dhw == CapabilityState.AVAILABLE


def test_apply_reading_without_value_leaves_group_unchanged() -> None:
    """A failed observation changes capability state without clearing a value."""
    existing = {
        "value_celsius": 48.0,
        "min_celsius": 35.0,
        "max_celsius": 70.0,
        "step_celsius": 1.0,
        "writable": True,
    }
    group = DHWState(setpoint=existing)

    updated_group, updated_capabilities = apply_reading(
        group,
        "setpoint",
        None,
        CapabilitiesState(dhw=CapabilityState.AVAILABLE),
        "dhw",
        grpc.StatusCode.NOT_FOUND,
    )

    assert updated_group is group
    assert updated_group.setpoint == existing
    assert updated_capabilities.dhw == CapabilityState.TEMPORARILY_UNAVAILABLE


def test_domain_state_is_frozen() -> None:
    """Grouped state cannot be assigned to in place."""
    domain = DomainState()

    with pytest.raises(FrozenInstanceError):
        domain.dhw = DHWState()  # type: ignore[misc]


def test_flatten_empty_domain_matches_public_initial_shape() -> None:
    """An untouched domain produces every public snapshot key and default."""
    assert flatten(DomainState()) == {
        "connected": False,
        "local_ski": "",
        "ski_registered": False,
        "power_l1_w": None,
        "power_l2_w": None,
        "power_l3_w": None,
        "current_l1_a": None,
        "current_l2_a": None,
        "current_l3_a": None,
        "voltage_l1_v": None,
        "voltage_l2_v": None,
        "voltage_l3_v": None,
        "frequency_hz": None,
        "energy_produced_kwh": None,
        "dhw_temperature_c": None,
        "room_temperature_c": None,
        "outdoor_temperature_c": None,
        "flow_temperature_c": None,
        "return_temperature_c": None,
        "compressor_temperature_c": None,
        "compressor_power_w": None,
        "power_watts": None,
        "energy_consumed_heating_kwh": None,
        "energy_consumed_dhw_kwh": None,
        "energy_consumed_kwh": None,
        "consumption_limit": None,
        "failsafe_limit": None,
        "heartbeat_status": None,
        "heartbeat_supported": CapabilityState.UNKNOWN,
        "lpc_supported": CapabilityState.UNKNOWN,
        "failsafe_supported": CapabilityState.UNKNOWN,
        "device_info": None,
        "compressor_flexibility": None,
        "dhw_setpoint": None,
        "dhw_system_function": None,
        "room_heating_setpoint": None,
        "room_heating_system_function": None,
        "device_operating_state": None,
        "ohpcf_supported": CapabilityState.UNKNOWN,
        "dhw_supported": CapabilityState.UNKNOWN,
        "dhw_sysfn_supported": CapabilityState.UNKNOWN,
        "room_heating_supported": CapabilityState.UNKNOWN,
    }


def test_equivalent_poll_and_stream_readings_flatten_identically() -> None:
    """Equivalent poll and stream observations share one reduction path."""
    value = {
        "value_celsius": 48.0,
        "min_celsius": 35.0,
        "max_celsius": 70.0,
        "step_celsius": 1.0,
        "writable": True,
    }
    poll_group, poll_capabilities = apply_reading(
        DHWState(), "setpoint", value, CapabilitiesState(), "dhw", None
    )
    stream_group, stream_capabilities = apply_reading(
        DHWState(), "setpoint", value, CapabilitiesState(), "dhw", None
    )

    poll_snapshot = flatten(
        DomainState(dhw=poll_group, capabilities=poll_capabilities)
    )
    stream_snapshot = flatten(
        DomainState(dhw=stream_group, capabilities=stream_capabilities)
    )

    assert poll_snapshot == stream_snapshot
