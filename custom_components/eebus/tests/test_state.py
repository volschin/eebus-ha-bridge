"""Tests for the authoritative immutable device-state reducer."""

from dataclasses import FrozenInstanceError

import grpc
import pytest

from custom_components.eebus.models import CapabilityState, SetpointState
from custom_components.eebus.state import (
    CapabilitiesState,
    CapabilityKey,
    CapabilityResult,
    DHWState,
    DeviceState,
    DeviceStateStore,
    MeasurementsState,
    StateField,
    StateObservation,
    is_fresh,
    next_capability_state,
)


@pytest.mark.parametrize(
    ("current", "status", "expected"),
    [
        (CapabilityState.UNKNOWN, None, CapabilityState.AVAILABLE),
        (CapabilityState.UNKNOWN, grpc.StatusCode.UNIMPLEMENTED, CapabilityState.UNSUPPORTED),
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
        (CapabilityState.AVAILABLE, grpc.StatusCode.INTERNAL, CapabilityState.AVAILABLE),
    ],
)
def test_next_capability_state_transitions(current, status, expected) -> None:
    assert next_capability_state(current, status) == expected


def test_device_state_is_deeply_immutable() -> None:
    state = DeviceState(dhw=DHWState(setpoint=_setpoint(48.0)))
    with pytest.raises(FrozenInstanceError):
        state.dhw = DHWState()  # type: ignore[misc]
    with pytest.raises(FrozenInstanceError):
        state.dhw.setpoint.value_celsius = 50.0  # type: ignore[misc,union-attr]


def _setpoint(value: float) -> SetpointState:
    return SetpointState(value, 35.0, 70.0, 1.0, True)


def _dhw_observation(
    value: SetpointState | None,
    status: grpc.StatusCode | None,
    *,
    base_revision: int | None = None,
) -> StateObservation:
    observed = frozenset({StateField.DHW_SETPOINT}) if status is None else frozenset()
    return StateObservation(
        state=DeviceState(dhw=DHWState(setpoint=value)),
        observed_fields=observed,
        unavailable_fields=(frozenset() if status is None else frozenset({StateField.DHW_SETPOINT})),
        capability_results=(CapabilityResult(CapabilityKey.DHW, status),),
        base_revision=base_revision,
    )


def test_successful_empty_read_clears_value() -> None:
    store = DeviceStateStore()
    store.dispatch(_dhw_observation(_setpoint(48.0), None))
    store.dispatch(_dhw_observation(None, None))
    assert store.state.dhw.setpoint is None
    assert store.state.capabilities.dhw == CapabilityState.AVAILABLE
    assert is_fresh(store.state, StateField.DHW_SETPOINT)


def test_temporary_failure_retains_last_value_but_marks_it_stale() -> None:
    store = DeviceStateStore()
    value = _setpoint(48.0)
    store.dispatch(_dhw_observation(value, None))
    store.dispatch(_dhw_observation(None, grpc.StatusCode.NOT_FOUND))
    assert store.state.dhw.setpoint == value
    assert store.state.capabilities.dhw == CapabilityState.TEMPORARILY_UNAVAILABLE
    assert not is_fresh(store.state, StateField.DHW_SETPOINT)


def test_unsupported_clears_last_value() -> None:
    store = DeviceStateStore()
    store.dispatch(_dhw_observation(_setpoint(48.0), None))
    store.dispatch(_dhw_observation(None, grpc.StatusCode.UNIMPLEMENTED))
    assert store.state.dhw.setpoint is None
    assert store.state.capabilities.dhw == CapabilityState.UNSUPPORTED


def test_unsupported_is_sticky_until_explicit_support_event() -> None:
    store = DeviceStateStore()
    store.dispatch(_dhw_observation(None, grpc.StatusCode.UNIMPLEMENTED))
    store.dispatch(_dhw_observation(_setpoint(48.0), None))
    assert store.state.capabilities.dhw == CapabilityState.UNSUPPORTED
    assert store.state.dhw.setpoint is None

    poll_base = store.revision
    store.dispatch(
        StateObservation(
            capability_results=(
                CapabilityResult(
                    CapabilityKey.DHW,
                    grpc.StatusCode.UNKNOWN,
                    explicit_support=True,
                ),
            )
        )
    )
    store.dispatch(_dhw_observation(_setpoint(42.0), None, base_revision=poll_base))
    assert store.state.capabilities.dhw == CapabilityState.UNKNOWN
    assert store.state.dhw.setpoint is None
    assert not is_fresh(store.state, StateField.DHW_SETPOINT)

    store.dispatch(_dhw_observation(_setpoint(48.0), None))
    assert store.state.capabilities.dhw == CapabilityState.AVAILABLE
    assert store.state.dhw.setpoint == _setpoint(48.0)


def test_newer_stream_value_wins_over_poll_started_earlier() -> None:
    """A poll candidate cannot overwrite a stream leaf newer than its base revision."""
    store = DeviceStateStore()
    poll_base = store.revision
    store.dispatch(
        StateObservation(
            state=DeviceState(measurements=MeasurementsState(power_watts=2000.0)),
            observed_fields=frozenset({StateField.POWER_WATTS}),
        )
    )
    store.dispatch(
        StateObservation(
            state=DeviceState(measurements=MeasurementsState(power_watts=1000.0)),
            observed_fields=frozenset({StateField.POWER_WATTS}),
            base_revision=poll_base,
        )
    )
    assert store.state.measurements.power_watts == 2000.0


def test_newer_stale_observation_blocks_older_successful_poll() -> None:
    store = DeviceStateStore()
    value = _setpoint(48.0)
    store.dispatch(_dhw_observation(value, None))
    poll_base = store.revision
    store.dispatch(_dhw_observation(None, grpc.StatusCode.NOT_FOUND))
    store.dispatch(_dhw_observation(_setpoint(42.0), None, base_revision=poll_base))
    assert store.state.dhw.setpoint == value
    assert store.state.capabilities.dhw == CapabilityState.TEMPORARILY_UNAVAILABLE
    assert not is_fresh(store.state, StateField.DHW_SETPOINT)


def test_equivalent_poll_and_stream_observations_produce_same_state() -> None:
    observation = _dhw_observation(_setpoint(48.0), None)
    poll_store = DeviceStateStore()
    stream_store = DeviceStateStore()
    poll_store.dispatch(observation)
    stream_store.dispatch(observation)
    assert poll_store.state == stream_store.state


def test_fifo_queue_serializes_reentrant_observations() -> None:
    published: list[float | None] = []
    store: DeviceStateStore

    def publish(state: DeviceState) -> None:
        published.append(state.measurements.power_watts)
        if len(published) == 1:
            store.dispatch(
                StateObservation(
                    state=DeviceState(measurements=MeasurementsState(power_watts=2.0)),
                    observed_fields=frozenset({StateField.POWER_WATTS}),
                )
            )

    store = DeviceStateStore(publish)
    store.dispatch(
        StateObservation(
            state=DeviceState(measurements=MeasurementsState(power_watts=1.0)),
            observed_fields=frozenset({StateField.POWER_WATTS}),
        )
    )
    assert published == [1.0, 2.0]
    assert store.state.measurements.power_watts == 2.0


def test_capabilities_default_to_unknown() -> None:
    assert CapabilitiesState().dhw == CapabilityState.UNKNOWN
