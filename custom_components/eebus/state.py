"""Authoritative immutable device state and serialized observation reducer."""

from __future__ import annotations

from collections import deque
from collections.abc import Callable
from dataclasses import dataclass, field, replace
from datetime import datetime
from enum import StrEnum
from typing import Any, cast

import grpc

from .models import (
    CapabilityState,
    CompressorFlexibilityState,
    ConsumptionLimitState,
    DHWSystemFunctionState,
    DeviceInfo,
    FailsafeState,
    HeartbeatState,
    SetpointState,
    SystemFunctionState,
)


@dataclass(frozen=True, slots=True)
class ConnectionState:
    """Connection and remote-device identity state."""

    connected: bool = False
    local_ski: str = ""
    ski_registered: bool = False
    device_info: DeviceInfo | None = None
    device_operating_state: str | None = None


@dataclass(frozen=True, slots=True)
class MeasurementsState:
    """Device telemetry and energy measurements."""

    power_l1_w: float | None = None
    power_l2_w: float | None = None
    power_l3_w: float | None = None
    current_l1_a: float | None = None
    current_l2_a: float | None = None
    current_l3_a: float | None = None
    voltage_l1_v: float | None = None
    voltage_l2_v: float | None = None
    voltage_l3_v: float | None = None
    frequency_hz: float | None = None
    energy_produced_kwh: float | None = None
    dhw_temperature_c: float | None = None
    room_temperature_c: float | None = None
    outdoor_temperature_c: float | None = None
    flow_temperature_c: float | None = None
    return_temperature_c: float | None = None
    compressor_temperature_c: float | None = None
    compressor_power_w: float | None = None
    power_watts: float | None = None
    energy_consumed_heating_kwh: float | None = None
    energy_consumed_dhw_kwh: float | None = None
    energy_consumed_kwh: float | None = None


@dataclass(frozen=True, slots=True)
class LPCState:
    """Limitation-of-power-consumption state."""

    consumption_limit: ConsumptionLimitState | None = None
    failsafe_limit: FailsafeState | None = None
    heartbeat_status: HeartbeatState | None = None


@dataclass(frozen=True, slots=True)
class DHWState:
    """Domestic-hot-water state."""

    setpoint: SetpointState | None = None
    system_function: DHWSystemFunctionState | None = None


@dataclass(frozen=True, slots=True)
class HVACState:
    """Room-heating state."""

    setpoint: SetpointState | None = None
    system_function: SystemFunctionState | None = None


@dataclass(frozen=True, slots=True)
class OHPCFState:
    """Heat-pump compressor-flexibility state."""

    compressor_flexibility: CompressorFlexibilityState | None = None


@dataclass(frozen=True, slots=True)
class CapabilitiesState:
    """Availability state for optional EEBUS capabilities."""

    monitoring: CapabilityState = CapabilityState.UNKNOWN
    heartbeat: CapabilityState = CapabilityState.UNKNOWN
    lpc: CapabilityState = CapabilityState.UNKNOWN
    failsafe: CapabilityState = CapabilityState.UNKNOWN
    ohpcf: CapabilityState = CapabilityState.UNKNOWN
    dhw: CapabilityState = CapabilityState.UNKNOWN
    dhw_system_function: CapabilityState = CapabilityState.UNKNOWN
    room_heating: CapabilityState = CapabilityState.UNKNOWN


class StateField(StrEnum):
    """Typed leaf identifiers used for patches, freshness and race protection."""

    CONNECTED = "connected"
    LOCAL_SKI = "local_ski"
    SKI_REGISTERED = "ski_registered"
    DEVICE_INFO = "device_info"
    DEVICE_OPERATING_STATE = "device_operating_state"
    POWER_L1_W = "power_l1_w"
    POWER_L2_W = "power_l2_w"
    POWER_L3_W = "power_l3_w"
    CURRENT_L1_A = "current_l1_a"
    CURRENT_L2_A = "current_l2_a"
    CURRENT_L3_A = "current_l3_a"
    VOLTAGE_L1_V = "voltage_l1_v"
    VOLTAGE_L2_V = "voltage_l2_v"
    VOLTAGE_L3_V = "voltage_l3_v"
    FREQUENCY_HZ = "frequency_hz"
    ENERGY_PRODUCED_KWH = "energy_produced_kwh"
    DHW_TEMPERATURE_C = "dhw_temperature_c"
    ROOM_TEMPERATURE_C = "room_temperature_c"
    OUTDOOR_TEMPERATURE_C = "outdoor_temperature_c"
    FLOW_TEMPERATURE_C = "flow_temperature_c"
    RETURN_TEMPERATURE_C = "return_temperature_c"
    COMPRESSOR_TEMPERATURE_C = "compressor_temperature_c"
    COMPRESSOR_POWER_W = "compressor_power_w"
    POWER_WATTS = "power_watts"
    ENERGY_CONSUMED_HEATING_KWH = "energy_consumed_heating_kwh"
    ENERGY_CONSUMED_DHW_KWH = "energy_consumed_dhw_kwh"
    ENERGY_CONSUMED_KWH = "energy_consumed_kwh"
    CONSUMPTION_LIMIT = "consumption_limit"
    FAILSAFE_LIMIT = "failsafe_limit"
    HEARTBEAT_STATUS = "heartbeat_status"
    COMPRESSOR_FLEXIBILITY = "compressor_flexibility"
    DHW_SETPOINT = "dhw_setpoint"
    DHW_SYSTEM_FUNCTION = "dhw_system_function"
    ROOM_HEATING_SETPOINT = "room_heating_setpoint"
    ROOM_HEATING_SYSTEM_FUNCTION = "room_heating_system_function"


class CapabilityKey(StrEnum):
    """Typed identifiers for capability reducer inputs."""

    MONITORING = "monitoring"
    LPC = "lpc"
    FAILSAFE = "failsafe"
    HEARTBEAT = "heartbeat"
    OHPCF = "ohpcf"
    DHW = "dhw"
    DHW_SYSTEM_FUNCTION = "dhw_system_function"
    ROOM_HEATING = "room_heating"


@dataclass(frozen=True, slots=True)
class DeviceState:
    """Complete, atomically published state for one target device."""

    connection: ConnectionState = field(default_factory=ConnectionState)
    measurements: MeasurementsState = field(default_factory=MeasurementsState)
    lpc: LPCState = field(default_factory=LPCState)
    dhw: DHWState = field(default_factory=DHWState)
    hvac: HVACState = field(default_factory=HVACState)
    ohpcf: OHPCFState = field(default_factory=OHPCFState)
    capabilities: CapabilitiesState = field(default_factory=CapabilitiesState)
    capability_metadata: tuple[CapabilityMetadata, ...] = ()
    explicit_capability_contract: bool = False
    fresh_fields: frozenset[StateField] = frozenset()


@dataclass(frozen=True, slots=True)
class CapabilityResult:
    """One capability attempt; a ``None`` status denotes a successful read."""

    capability: CapabilityKey
    status: grpc.StatusCode | None
    explicit_support: bool = False
    explicit_state: CapabilityState | None = None
    reason: str | None = None
    last_changed: datetime | None = None


@dataclass(frozen=True, slots=True)
class CapabilityMetadata:
    """Reason and bridge transition time for one explicit capability."""

    capability: CapabilityKey
    reason: str | None
    last_changed: datetime | None


@dataclass(frozen=True, slots=True)
class StateObservation:
    """Typed state candidate plus the exact leaves observed by one source."""

    state: DeviceState = field(default_factory=DeviceState)
    observed_fields: frozenset[StateField] = frozenset()
    unavailable_fields: frozenset[StateField] = frozenset()
    capability_results: tuple[CapabilityResult, ...] = ()
    explicit_capability_contract: bool | None = None
    base_revision: int | None = None


_CONNECTION_FIELDS = {
    StateField.CONNECTED,
    StateField.LOCAL_SKI,
    StateField.SKI_REGISTERED,
    StateField.DEVICE_INFO,
    StateField.DEVICE_OPERATING_STATE,
}
_MEASUREMENT_FIELDS = (
    frozenset(StateField)
    - _CONNECTION_FIELDS
    - {
        StateField.CONSUMPTION_LIMIT,
        StateField.FAILSAFE_LIMIT,
        StateField.HEARTBEAT_STATUS,
        StateField.COMPRESSOR_FLEXIBILITY,
        StateField.DHW_SETPOINT,
        StateField.DHW_SYSTEM_FUNCTION,
        StateField.ROOM_HEATING_SETPOINT,
        StateField.ROOM_HEATING_SYSTEM_FUNCTION,
    }
)
_CAPABILITY_FIELDS: dict[CapabilityKey, tuple[StateField, ...]] = {
    # Monitoring is a diagnostic aggregate. Individual telemetry leaves carry
    # their own freshness because optional temperature readers can succeed even
    # when the main MPC use case is unsupported.
    CapabilityKey.MONITORING: (),
    CapabilityKey.LPC: (StateField.CONSUMPTION_LIMIT,),
    CapabilityKey.FAILSAFE: (StateField.FAILSAFE_LIMIT,),
    CapabilityKey.HEARTBEAT: (StateField.HEARTBEAT_STATUS,),
    CapabilityKey.OHPCF: (StateField.COMPRESSOR_FLEXIBILITY,),
    CapabilityKey.DHW: (StateField.DHW_SETPOINT,),
    CapabilityKey.DHW_SYSTEM_FUNCTION: (StateField.DHW_SYSTEM_FUNCTION,),
    CapabilityKey.ROOM_HEATING: (
        StateField.ROOM_HEATING_SETPOINT,
        StateField.ROOM_HEATING_SYSTEM_FUNCTION,
    ),
}
_FIELD_CAPABILITY = {
    field_name: capability
    for capability, field_names in _CAPABILITY_FIELDS.items()
    for field_name in field_names
}


def next_capability_state(
    current: CapabilityState,
    status: grpc.StatusCode | None,
    *,
    explicit_support: bool = False,
    explicit_state: CapabilityState | None = None,
) -> CapabilityState:
    """Return the capability state after a successful call or gRPC status."""
    if explicit_state is not None:
        return explicit_state
    if current == CapabilityState.UNSUPPORTED and not explicit_support:
        return CapabilityState.UNSUPPORTED
    if explicit_support and status == grpc.StatusCode.UNKNOWN:
        return CapabilityState.UNKNOWN
    if status is None:
        return CapabilityState.AVAILABLE
    if status == grpc.StatusCode.UNIMPLEMENTED:
        return CapabilityState.UNSUPPORTED
    if status in (grpc.StatusCode.NOT_FOUND, grpc.StatusCode.UNAVAILABLE):
        return CapabilityState.TEMPORARILY_UNAVAILABLE
    return current


def _field_value(state: DeviceState, field_name: StateField) -> object:
    if field_name in _CONNECTION_FIELDS:
        return getattr(state.connection, field_name.value)
    if field_name in _MEASUREMENT_FIELDS:
        return getattr(state.measurements, field_name.value)
    if field_name == StateField.CONSUMPTION_LIMIT:
        return state.lpc.consumption_limit
    if field_name == StateField.FAILSAFE_LIMIT:
        return state.lpc.failsafe_limit
    if field_name == StateField.HEARTBEAT_STATUS:
        return state.lpc.heartbeat_status
    if field_name == StateField.COMPRESSOR_FLEXIBILITY:
        return state.ohpcf.compressor_flexibility
    if field_name == StateField.DHW_SETPOINT:
        return state.dhw.setpoint
    if field_name == StateField.DHW_SYSTEM_FUNCTION:
        return state.dhw.system_function
    if field_name == StateField.ROOM_HEATING_SETPOINT:
        return state.hvac.setpoint
    return state.hvac.system_function


def _replace_field(state: DeviceState, field_name: StateField, value: object) -> DeviceState:
    if field_name in _CONNECTION_FIELDS:
        return replace(
            state,
            connection=replace(state.connection, **{field_name.value: cast(Any, value)}),
        )
    if field_name in _MEASUREMENT_FIELDS:
        return replace(
            state,
            measurements=replace(state.measurements, **{field_name.value: cast(Any, value)}),
        )
    if field_name == StateField.CONSUMPTION_LIMIT:
        return replace(state, lpc=replace(state.lpc, consumption_limit=value))  # type: ignore[arg-type]
    if field_name == StateField.FAILSAFE_LIMIT:
        return replace(state, lpc=replace(state.lpc, failsafe_limit=value))  # type: ignore[arg-type]
    if field_name == StateField.HEARTBEAT_STATUS:
        return replace(state, lpc=replace(state.lpc, heartbeat_status=value))  # type: ignore[arg-type]
    if field_name == StateField.COMPRESSOR_FLEXIBILITY:
        return replace(state, ohpcf=replace(state.ohpcf, compressor_flexibility=value))  # type: ignore[arg-type]
    if field_name == StateField.DHW_SETPOINT:
        return replace(state, dhw=replace(state.dhw, setpoint=value))  # type: ignore[arg-type]
    if field_name == StateField.DHW_SYSTEM_FUNCTION:
        return replace(state, dhw=replace(state.dhw, system_function=value))  # type: ignore[arg-type]
    if field_name == StateField.ROOM_HEATING_SETPOINT:
        return replace(state, hvac=replace(state.hvac, setpoint=value))  # type: ignore[arg-type]
    return replace(state, hvac=replace(state.hvac, system_function=value))  # type: ignore[arg-type]


def reduce_observation(
    current: DeviceState,
    observation: StateObservation,
    field_revisions: dict[StateField, int],
    revision: int,
) -> DeviceState:
    """Apply one observation, preserving leaves changed after a poll began."""
    updated = current
    fresh = set(current.fresh_fields)
    explicit_contract = (
        current.explicit_capability_contract
        if observation.explicit_capability_contract is None
        else observation.explicit_capability_contract
    )
    explicit_states = {
        result.capability: result.explicit_state
        for result in observation.capability_results
        if result.explicit_state is not None
    }

    def is_newer(field_name: StateField) -> bool:
        base = observation.base_revision
        return base is not None and field_revisions.get(field_name, 0) > base

    for field_name in observation.observed_fields:
        if is_newer(field_name):
            continue
        field_capability = _FIELD_CAPABILITY.get(field_name)
        if explicit_contract and field_capability is not None:
            contract_state = explicit_states.get(
                field_capability,
                getattr(current.capabilities, field_capability.value),
            )
            if contract_state != CapabilityState.AVAILABLE:
                fresh.discard(field_name)
                continue
        updated = _replace_field(updated, field_name, _field_value(observation.state, field_name))
        fresh.add(field_name)
        field_revisions[field_name] = revision

    for field_name in observation.unavailable_fields:
        if not is_newer(field_name):
            fresh.discard(field_name)
            field_revisions[field_name] = revision

    capabilities = updated.capabilities
    metadata = {entry.capability: entry for entry in updated.capability_metadata}
    for result in observation.capability_results:
        if explicit_contract and result.explicit_state is None:
            continue
        value_fields = _CAPABILITY_FIELDS[result.capability]
        if result.explicit_state is None and any(is_newer(field_name) for field_name in value_fields):
            continue
        previous = getattr(capabilities, result.capability.value)
        capability = next_capability_state(
            previous,
            result.status,
            explicit_support=result.explicit_support,
            explicit_state=result.explicit_state,
        )
        capabilities = replace(capabilities, **{result.capability.value: capability})
        if result.explicit_state is not None:
            metadata[result.capability] = CapabilityMetadata(
                capability=result.capability,
                reason=result.reason,
                last_changed=result.last_changed,
            )
        if capability == CapabilityState.UNSUPPORTED:
            for field_name in value_fields:
                updated = _replace_field(updated, field_name, None)
                fresh.discard(field_name)
                field_revisions[field_name] = revision
        elif capability == CapabilityState.TEMPORARILY_UNAVAILABLE:
            for field_name in value_fields:
                fresh.discard(field_name)
                field_revisions[field_name] = revision
        elif result.explicit_support and result.explicit_state is None:
            for field_name in value_fields:
                fresh.discard(field_name)
                field_revisions[field_name] = revision

    return replace(
        updated,
        capabilities=capabilities,
        capability_metadata=tuple(metadata[key] for key in sorted(metadata, key=str)),
        explicit_capability_contract=explicit_contract,
        fresh_fields=frozenset(fresh),
    )


class DeviceStateStore:
    """Own the sole current-state reference and serialize all observations."""

    def __init__(
        self,
        publish: Callable[[DeviceState], None] | None = None,
        initial: DeviceState | None = None,
    ) -> None:
        self._state = initial or DeviceState()
        self._publish = publish
        self._revision = 0
        self._field_revisions: dict[StateField, int] = {}
        self._queue: deque[StateObservation] = deque()
        self._reducing = False

    @property
    def state(self) -> DeviceState:
        """Return the current immutable state."""
        return self._state

    @property
    def revision(self) -> int:
        """Return the current local observation revision."""
        return self._revision

    def dispatch(self, observation: StateObservation) -> DeviceState:
        """Enqueue and synchronously drain an observation in FIFO order."""
        self._queue.append(observation)
        if self._reducing:
            return self._state
        self._reducing = True
        try:
            while self._queue:
                queued = self._queue.popleft()
                self._revision += 1
                self._state = reduce_observation(
                    self._state,
                    queued,
                    self._field_revisions,
                    self._revision,
                )
                if self._publish is not None:
                    self._publish(self._state)
        finally:
            self._reducing = False
        return self._state


def is_fresh(state: DeviceState, field_name: StateField) -> bool:
    """Return whether the leaf contains a fresh successful observation."""
    return field_name in state.fresh_fields
