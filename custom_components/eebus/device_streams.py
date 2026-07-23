"""Device stream lifecycle and protobuf-to-state observation conversion."""

from __future__ import annotations

import asyncio
import logging
from collections.abc import Callable, Coroutine
from dataclasses import replace
from datetime import UTC
from functools import lru_cache
from typing import Any

import grpc.aio
from homeassistant.core import HomeAssistant

from . import proto_stubs
from .grpc_client import GrpcChannelManager
from .models import (
    CapabilityState,
    CompressorFlexibilityState,
    ConsumptionLimitState,
    FailsafeState,
    HeartbeatState,
    _dhw_system_function_to_dict,
    _extract_flat_measurements,
    _room_heating_from_proto,
    _setpoint_to_dict,
)
from .session_diagnostics import DeviceStreamDiagnostics
from .ski import normalize_ski
from .snapshot import _capability_results_from_proto, _snapshot_observation_from_proto
from .state import (
    CapabilityKey,
    CapabilityResult,
    DeviceState,
    DeviceStateStore,
    DHWState,
    HVACState,
    LPCState,
    MeasurementsState,
    OHPCFState,
    StateField,
    StateObservation,
)
from .streams import ConsumeFn, StreamManager

_LOGGER = logging.getLogger(__name__)


@lru_cache(maxsize=1)
def _measurement_event_map() -> tuple[dict[int, tuple[str, str, StateField]], dict[int, StateField]]:
    """Build measurement event conversion tables once protobufs are importable."""
    event_type = proto_stubs.MeasurementEventType
    value_events: dict[int, tuple[str, str, StateField]] = {
        int(event_type.MEASUREMENT_EVENT_POWER_UPDATED): (
            "power",
            "watts",
            StateField.POWER_WATTS,
        ),
        int(event_type.MEASUREMENT_EVENT_ENERGY_UPDATED): (
            "energy",
            "kilowatt_hours",
            StateField.ENERGY_CONSUMED_KWH,
        ),
        int(event_type.MEASUREMENT_EVENT_DHW_TEMPERATURE_UPDATED): (
            "measurement",
            "value",
            StateField.DHW_TEMPERATURE_C,
        ),
        int(event_type.MEASUREMENT_EVENT_ROOM_TEMPERATURE_UPDATED): (
            "measurement",
            "value",
            StateField.ROOM_TEMPERATURE_C,
        ),
        int(event_type.MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_UPDATED): (
            "measurement",
            "value",
            StateField.OUTDOOR_TEMPERATURE_C,
        ),
        int(event_type.MEASUREMENT_EVENT_FLOW_TEMPERATURE_UPDATED): (
            "measurement",
            "value",
            StateField.FLOW_TEMPERATURE_C,
        ),
        int(event_type.MEASUREMENT_EVENT_RETURN_TEMPERATURE_UPDATED): (
            "measurement",
            "value",
            StateField.RETURN_TEMPERATURE_C,
        ),
        int(event_type.MEASUREMENT_EVENT_DEVICE_OPERATING_STATE_UPDATED): (
            "device_diagnostics",
            "operating_state",
            StateField.DEVICE_OPERATING_STATE,
        ),
    }
    support_events = {
        int(event_type.MEASUREMENT_EVENT_DHW_TEMPERATURE_SUPPORT_UPDATED): StateField.DHW_TEMPERATURE_C,
        int(event_type.MEASUREMENT_EVENT_ROOM_TEMPERATURE_SUPPORT_UPDATED): StateField.ROOM_TEMPERATURE_C,
        int(event_type.MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_SUPPORT_UPDATED): StateField.OUTDOOR_TEMPERATURE_C,
    }
    return value_events, support_events


@lru_cache(maxsize=1)
def _availability_field_maps() -> tuple[
    dict[int, StateField],
    dict[int, frozenset[StateField]],
    dict[int, frozenset[StateField]],
]:
    """Build the consolidated-delta invalidation tables once protobufs are importable."""
    heating_event = proto_stubs.RoomHeatingEventType
    hvac_fields = {
        int(heating_event.ROOM_HEATING_EVENT_CURRENT_TEMPERATURE_UPDATED): StateField.ROOM_TEMPERATURE_C,
        int(heating_event.ROOM_HEATING_EVENT_SETPOINT_UPDATED): StateField.ROOM_HEATING_SETPOINT,
        int(heating_event.ROOM_HEATING_EVENT_SYSTEM_FUNCTION_UPDATED): StateField.ROOM_HEATING_SYSTEM_FUNCTION,
    }
    power_phases = frozenset({StateField.POWER_L1_W, StateField.POWER_L2_W, StateField.POWER_L3_W})
    current_phases = frozenset({StateField.CURRENT_L1_A, StateField.CURRENT_L2_A, StateField.CURRENT_L3_A})
    voltage_phases = frozenset({StateField.VOLTAGE_L1_V, StateField.VOLTAGE_L2_V, StateField.VOLTAGE_L3_V})
    energy_produced = frozenset({StateField.ENERGY_PRODUCED_KWH})
    frequency = frozenset({StateField.FREQUENCY_HZ})
    detail = proto_stubs.MeasurementUpdateField
    detail_fields = {
        int(detail.MEASUREMENT_UPDATE_FIELD_POWER_PER_PHASE): power_phases,
        int(detail.MEASUREMENT_UPDATE_FIELD_ENERGY_PRODUCED): energy_produced,
        int(detail.MEASUREMENT_UPDATE_FIELD_CURRENT_PER_PHASE): current_phases,
        int(detail.MEASUREMENT_UPDATE_FIELD_VOLTAGE_PER_PHASE): voltage_phases,
        int(detail.MEASUREMENT_UPDATE_FIELD_FREQUENCY): frequency,
    }
    measurement_event = proto_stubs.MeasurementEventType
    measurement_fields = {
        int(measurement_event.MEASUREMENT_EVENT_POWER_UPDATED): frozenset({StateField.POWER_WATTS}),
        int(measurement_event.MEASUREMENT_EVENT_ENERGY_UPDATED): frozenset({StateField.ENERGY_CONSUMED_KWH}),
        int(measurement_event.MEASUREMENT_EVENT_POWER_PER_PHASE_UPDATED): power_phases,
        int(measurement_event.MEASUREMENT_EVENT_ENERGY_PRODUCED_UPDATED): energy_produced,
        int(measurement_event.MEASUREMENT_EVENT_CURRENT_PER_PHASE_UPDATED): current_phases,
        int(measurement_event.MEASUREMENT_EVENT_VOLTAGE_PER_PHASE_UPDATED): voltage_phases,
        int(measurement_event.MEASUREMENT_EVENT_FREQUENCY_UPDATED): frequency,
        int(measurement_event.MEASUREMENT_EVENT_DHW_TEMPERATURE_UPDATED): frozenset({StateField.DHW_TEMPERATURE_C}),
        int(measurement_event.MEASUREMENT_EVENT_ROOM_TEMPERATURE_UPDATED): frozenset({StateField.ROOM_TEMPERATURE_C}),
        int(measurement_event.MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_UPDATED): frozenset(
            {StateField.OUTDOOR_TEMPERATURE_C}
        ),
        int(measurement_event.MEASUREMENT_EVENT_FLOW_TEMPERATURE_UPDATED): frozenset({StateField.FLOW_TEMPERATURE_C}),
        int(measurement_event.MEASUREMENT_EVENT_RETURN_TEMPERATURE_UPDATED): frozenset(
            {StateField.RETURN_TEMPERATURE_C}
        ),
        int(measurement_event.MEASUREMENT_EVENT_DEVICE_OPERATING_STATE_UPDATED): frozenset(
            {StateField.DEVICE_OPERATING_STATE}
        ),
    }
    return hvac_fields, detail_fields, measurement_fields


class DeviceStreams:
    """Own all legacy device streams and reduce their events through one store."""

    def __init__(
        self,
        hass: HomeAssistant,
        channel_manager: GrpcChannelManager,
        ski: str,
        store: DeviceStateStore,
        request_refresh: Callable[[], Coroutine[Any, Any, None]],
        supports_feature: Callable[[int], bool] | None = None,
    ) -> None:
        self._hass = hass
        self._ski = ski
        self._store = store
        self._request_refresh = request_refresh
        self._manager = StreamManager(hass, channel_manager)
        self._legacy_manager = StreamManager(hass, channel_manager)
        self._refresh_task: asyncio.Task[None] | None = None
        self._refresh_pending = False
        self._last_revision: int | None = None
        self._supports_feature = supports_feature or (lambda _feature: True)
        self._handling_consolidated = False
        self._initial_snapshot_received = asyncio.Event()
        self._ski_registered = False
        self._started = False
        self._restart_pending = False
        self._restart_task: asyncio.Task[None] | None = None

    def start(self) -> None:
        """Start the consolidated stream, falling back only for old bridges."""
        if self._started:
            return
        self._started = True
        self._start_selected_profile()

    def _start_selected_profile(self) -> None:
        """Start the stream profile from the latest negotiated contract."""

        async def device_state(channel: grpc.aio.Channel) -> None:
            self._last_revision = None
            async for event in proto_stubs.device_service_stub(channel).SubscribeDeviceState(
                proto_stubs.DeviceRequest(ski=self._ski)
            ):
                self.handle_device_state_event(event)

        async def device(channel: grpc.aio.Channel) -> None:
            async for event in proto_stubs.device_service_stub(channel).SubscribeDeviceEvents(proto_stubs.Empty()):
                self.handle_device_event(event)

        async def lpc(channel: grpc.aio.Channel) -> None:
            async for event in proto_stubs.lpc_service_stub(channel).SubscribeLPCEvents(
                proto_stubs.DeviceRequest(ski=self._ski)
            ):
                self.handle_lpc_event(event)

        async def measurements(channel: grpc.aio.Channel) -> None:
            async for event in proto_stubs.monitoring_service_stub(channel).SubscribeMeasurements(
                proto_stubs.DeviceRequest(ski=self._ski)
            ):
                self.handle_measurement_event(event)

        async def ohpcf(channel: grpc.aio.Channel) -> None:
            async for event in proto_stubs.ohpcf_service_stub(channel).SubscribeOHPCFEvents(
                proto_stubs.DeviceRequest(ski=self._ski)
            ):
                self.handle_ohpcf_event(event)

        async def dhw(channel: grpc.aio.Channel) -> None:
            async for event in proto_stubs.dhw_service_stub(channel).SubscribeDHWEvents(
                proto_stubs.DeviceRequest(ski=self._ski)
            ):
                self.handle_dhw_event(event)

        async def dhw_system_function(channel: grpc.aio.Channel) -> None:
            async for event in proto_stubs.dhw_service_stub(channel).SubscribeDHWSystemFunctionEvents(
                proto_stubs.DeviceRequest(ski=self._ski)
            ):
                self.handle_dhw_system_function_event(event)

        async def room_heating(channel: grpc.aio.Channel) -> None:
            async for event in proto_stubs.hvac_service_stub(channel).SubscribeRoomHeatingEvents(
                proto_stubs.DeviceRequest(ski=self._ski)
            ):
                self.handle_room_heating_event(event)

        legacy_streams: dict[str, ConsumeFn] = {
            "device_events": device,
            "lpc_events": lpc,
            "measurements": measurements,
            "ohpcf_events": ohpcf,
            "dhw_events": dhw,
            "dhw_sysfn_events": dhw_system_function,
            "room_heating_events": room_heating,
        }

        def start_legacy(_name: str) -> None:
            self._legacy_manager.start(legacy_streams, f"eebus_{{name}}_{self._ski}")

        if self._supports_feature(int(proto_stubs.FeatureId.FEATURE_CONSOLIDATED_DEVICE_STREAM)):
            self._manager.start(
                {"device_state": device_state},
                f"eebus_{{name}}_{self._ski}",
                on_unimplemented=start_legacy,
            )
        else:
            start_legacy("device_state")

    def contract_changed(self) -> None:
        """Re-select the stream profile after channel contract negotiation."""
        if not self._started:
            return
        self._restart_pending = True
        if self._restart_task is None or self._restart_task.done():
            self._restart_task = asyncio.create_task(
                self._restart_after_contract_change(),
                name=f"eebus_stream_profile_{self._ski}",
            )

    async def _restart_after_contract_change(self) -> None:
        """Apply the newest profile, coalescing rapid contract changes."""
        while self._restart_pending and self._started:
            self._restart_pending = False
            await self._manager.stop()
            await self._legacy_manager.stop()
            if self._started:
                self._start_selected_profile()

    async def stop(self) -> None:
        """Stop every stream before transport shutdown."""
        self._started = False
        self._restart_pending = False
        restart_task = self._restart_task
        if restart_task is not None and restart_task is not asyncio.current_task():
            restart_task.cancel()
            await asyncio.gather(restart_task, return_exceptions=True)
        self._restart_task = None
        await self._manager.stop()
        await self._legacy_manager.stop()

    async def wait_initial_snapshot(self, timeout: float) -> DeviceState:
        """Wait until the consolidated stream has reduced its initial snapshot."""
        await asyncio.wait_for(self._initial_snapshot_received.wait(), timeout=timeout)
        return self._store.state

    def mark_registered(self) -> None:
        """Remember successful explicit registration for stream snapshot reduction."""
        self._ski_registered = True

    def diagnostics(self) -> DeviceStreamDiagnostics:
        """Return redacted stream state for config-entry diagnostics."""
        running_refresh = self._refresh_task is not None and not self._refresh_task.done()
        return DeviceStreamDiagnostics(
            last_device_state_revision=self._last_revision,
            refresh_pending=self._refresh_pending,
            refresh_running=running_refresh,
            primary=self._manager.diagnostics(),
            legacy=self._legacy_manager.diagnostics(),
        )

    def _matches(self, event_ski: str) -> bool:
        return normalize_ski(event_ski) == normalize_ski(self._ski)

    def _refresh(self) -> None:
        if self._handling_consolidated:
            return
        running = getattr(self, "_refresh_task", None)
        if running is not None and not running.done():
            self._refresh_pending = True
            return
        self._refresh_pending = False
        self._refresh_task = self._hass.async_create_task(self._run_refresh())

    async def _run_refresh(self) -> None:
        """Run one refresh and exactly one trailing pass for in-flight signals."""
        try:
            while True:
                self._refresh_pending = False
                await self._request_refresh()
                if not self._refresh_pending:
                    return
        finally:
            self._refresh_task = None

    def handle_device_state_event(self, event: Any) -> None:
        """Apply one ordered envelope and request a coalesced resync on gaps."""
        if not self._matches(event.ski):
            return
        revision = int(event.revision)
        previous = self._last_revision
        if previous is not None and revision <= previous:
            return
        self._last_revision = revision
        payload = event.WhichOneof("payload")
        revision_gap = previous is not None and revision != previous + 1
        if revision_gap or payload == "resync_required":
            # One envelope can report both a revision gap and an explicit drop.
            # They describe the same recovery need and must schedule one read.
            self._refresh()
        if payload == "resync_required":
            return
        elif payload == "initial_snapshot":
            self._store.dispatch(
                _snapshot_observation_from_proto(
                    event.initial_snapshot,
                    ski_registered=(
                        self._ski_registered
                        or self._store.state.connection.ski_registered
                    ),
                )
            )
            self._initial_snapshot_received.set()
        elif payload == "capability":
            self._store.dispatch(
                StateObservation(
                    capability_results=_capability_results_from_proto(event.capability),
                    explicit_capability_contract=True,
                )
            )
        elif payload is None:
            if not revision_gap:
                self._refresh()
        else:
            if self._apply_explicit_unavailability(event, payload):
                return
            if payload == "device":
                # Connection transitions invalidate every other payload domain,
                # which only the reconciliation poll inside handle_device_event
                # can re-establish — never suppress it with the stream guard.
                self._apply_device_state_payload(event, payload)
                return
            self._handling_consolidated = True
            try:
                self._apply_device_state_payload(event, payload)
            finally:
                self._handling_consolidated = False

    def _apply_explicit_unavailability(self, event: Any, payload: str) -> bool:
        """Apply an authoritative unavailable/unsupported envelope without using stale payload data."""
        availability = int(event.availability)
        temporary = int(proto_stubs.EventAvailability.EVENT_AVAILABILITY_TEMPORARILY_UNAVAILABLE)
        unsupported = int(proto_stubs.EventAvailability.EVENT_AVAILABILITY_UNSUPPORTED)
        if availability not in (temporary, unsupported):
            return False
        fields, capability = self._availability_target(event, payload)
        if not fields:
            return False
        capability_results: tuple[CapabilityResult, ...] = ()
        if capability is not None:
            state = CapabilityState.UNSUPPORTED if availability == unsupported else CapabilityState.TEMPORARILY_UNAVAILABLE
            capability_results = (CapabilityResult(capability, None, explicit_state=state),)
        self._store.dispatch(
            StateObservation(
                unavailable_fields=fields,
                capability_results=capability_results,
            )
        )
        return True

    @staticmethod
    def _availability_target(event: Any, payload: str) -> tuple[frozenset[StateField], CapabilityKey | None]:
        """Resolve the exact state leaves invalidated by one consolidated delta."""
        if payload == "device":
            return frozenset({StateField.CONNECTED}), None
        if payload == "lpc":
            event_type = int(event.lpc.event_type)
            if event_type == int(proto_stubs.LPCEventType.LPC_EVENT_LIMIT_UPDATED):
                return frozenset({StateField.CONSUMPTION_LIMIT}), CapabilityKey.LPC
            if event_type == int(proto_stubs.LPCEventType.LPC_EVENT_FAILSAFE_UPDATED):
                return frozenset({StateField.FAILSAFE_LIMIT}), CapabilityKey.FAILSAFE
            if event_type == int(proto_stubs.LPCEventType.LPC_EVENT_HEARTBEAT_TIMEOUT):
                return frozenset({StateField.HEARTBEAT_STATUS}), CapabilityKey.HEARTBEAT
        if payload == "dhw":
            if event.dhw.WhichOneof("payload") == "system_function":
                return frozenset({StateField.DHW_SYSTEM_FUNCTION}), CapabilityKey.DHW_SYSTEM_FUNCTION
            return frozenset({StateField.DHW_SETPOINT}), CapabilityKey.DHW
        if payload == "hvac":
            hvac_fields, _, _ = _availability_field_maps()
            field = hvac_fields.get(int(event.hvac.event_type))
            return (frozenset({field}) if field is not None else frozenset()), CapabilityKey.ROOM_HEATING
        if payload == "ohpcf":
            return frozenset({StateField.COMPRESSOR_FLEXIBILITY}), CapabilityKey.OHPCF
        if payload == "measurement":
            _, detail_fields, measurement_fields = _availability_field_maps()
            update_field = int(event.measurement.update_field)
            if update_field in detail_fields:
                return detail_fields[update_field], None
            return measurement_fields.get(int(event.measurement.event_type), frozenset()), None
        return frozenset(), None

    def _apply_device_state_payload(self, event: Any, payload: str) -> None:
        """Apply one explicitly classified consolidated payload without polling."""
        if payload == "device":
            self.handle_device_event(event.device)
        elif payload == "measurement":
            if (
                event.measurement.event_type == proto_stubs.MeasurementEventType.MEASUREMENT_EVENT_UNSPECIFIED
                and not event.measurement.HasField("measurements")
            ):
                # Unclassifiable delta: the documented fallback is a poll, so
                # the stream guard must not swallow it.
                self._handling_consolidated = False
                self._refresh()
            else:
                self.handle_measurement_event(event.measurement)
        elif payload == "lpc":
            self.handle_lpc_event(event.lpc)
        elif payload == "dhw":
            dhw_payload = event.dhw.WhichOneof("payload")
            if dhw_payload == "temperature":
                self.handle_dhw_event(event.dhw.temperature)
            elif dhw_payload == "system_function":
                self.handle_dhw_system_function_event(event.dhw.system_function)
        elif payload == "hvac":
            self.handle_room_heating_event(event.hvac)
        elif payload == "ohpcf":
            self.handle_ohpcf_event(event.ohpcf)

    def _support_changed(self, capability: CapabilityKey) -> None:
        """Reset sticky unsupported state only for an explicit support event."""
        self._store.dispatch(
            StateObservation(
                capability_results=(
                    CapabilityResult(
                        capability,
                        grpc.StatusCode.UNKNOWN,
                        explicit_support=True,
                    ),
                )
            )
        )
        self._refresh()

    def _mark_temporarily_unavailable(
        self,
        fields: frozenset[StateField],
        capability: CapabilityKey | None = None,
    ) -> None:
        """Retain last values but immediately withdraw their freshness."""
        capability_results = (
            (CapabilityResult(capability, grpc.StatusCode.UNAVAILABLE),) if capability is not None else ()
        )
        self._store.dispatch(
            StateObservation(
                unavailable_fields=fields,
                capability_results=capability_results,
            )
        )
        self._refresh()

    def handle_device_event(self, event: Any) -> None:
        """Reconcile connection events through a fresh poll."""
        if not self._matches(event.ski):
            return
        if event.event_type == proto_stubs.DeviceEventType.DEVICE_EVENT_TRUST_REMOVED:
            _LOGGER.warning("EEBUS device %s removed trust with bridge", event.ski)
            connected = False
        elif event.event_type not in (
            proto_stubs.DeviceEventType.DEVICE_EVENT_CONNECTED,
            proto_stubs.DeviceEventType.DEVICE_EVENT_DISCONNECTED,
        ):
            return
        else:
            connected = event.event_type == proto_stubs.DeviceEventType.DEVICE_EVENT_CONNECTED
        self._store.dispatch(
            StateObservation(
                state=DeviceState(connection=replace(DeviceState().connection, connected=connected)),
                observed_fields=frozenset({StateField.CONNECTED}),
            )
        )
        self._refresh()

    def handle_lpc_event(self, event: Any) -> None:
        """Convert one LPC event into a typed state observation."""
        if not self._matches(event.ski):
            return
        if event.event_type == proto_stubs.LPCEventType.LPC_EVENT_LIMIT_UPDATED:
            if not event.HasField("limit_update"):
                self._mark_temporarily_unavailable(
                    frozenset({StateField.CONSUMPTION_LIMIT}),
                    CapabilityKey.LPC,
                )
                return
            value = event.limit_update
            state = DeviceState(
                lpc=LPCState(
                    consumption_limit=ConsumptionLimitState(
                        value_watts=value.value_watts,
                        is_active=value.is_active,
                        is_changeable=value.is_changeable,
                    )
                )
            )
            self._store.dispatch(
                StateObservation(
                    state=state,
                    observed_fields=frozenset({StateField.CONSUMPTION_LIMIT}),
                    capability_results=(CapabilityResult(CapabilityKey.LPC, None),),
                )
            )
        elif event.event_type == proto_stubs.LPCEventType.LPC_EVENT_FAILSAFE_UPDATED:
            if not event.HasField("failsafe_update"):
                self._mark_temporarily_unavailable(
                    frozenset({StateField.FAILSAFE_LIMIT}),
                    CapabilityKey.FAILSAFE,
                )
                return
            value = event.failsafe_update
            state = DeviceState(
                lpc=LPCState(
                    failsafe_limit=FailsafeState(
                        value_watts=value.value_watts,
                        duration_minimum_seconds=value.duration_minimum_seconds,
                    )
                )
            )
            self._store.dispatch(
                StateObservation(
                    state=state,
                    observed_fields=frozenset({StateField.FAILSAFE_LIMIT}),
                    capability_results=(CapabilityResult(CapabilityKey.FAILSAFE, None),),
                )
            )
        elif event.event_type == proto_stubs.LPCEventType.LPC_EVENT_HEARTBEAT_TIMEOUT:
            if not event.HasField("heartbeat_update"):
                self._mark_temporarily_unavailable(
                    frozenset({StateField.HEARTBEAT_STATUS}),
                    CapabilityKey.HEARTBEAT,
                )
                return
            value = event.heartbeat_update
            self._store.dispatch(
                StateObservation(
                    state=DeviceState(
                        lpc=LPCState(
                            heartbeat_status=HeartbeatState(
                                running=value.running,
                                within_duration=value.within_duration,
                            )
                        )
                    ),
                    observed_fields=frozenset({StateField.HEARTBEAT_STATUS}),
                    capability_results=(CapabilityResult(CapabilityKey.HEARTBEAT, None),),
                )
            )

    def handle_measurement_event(self, event: Any) -> None:
        """Convert a measurement event into one leaf observation."""
        if not self._matches(event.ski):
            return
        value_events, support_events = _measurement_event_map()
        support_field = support_events.get(event.event_type)
        if support_field is not None:
            self._mark_temporarily_unavailable(frozenset({support_field}))
            return
        spec = value_events.get(event.event_type)
        if event.HasField("measurements"):
            values = _extract_flat_measurements(event.measurements.measurements)
            observed = frozenset(StateField(key) for key in values)
            if not observed:
                return
            _, detail_fields, measurement_fields = _availability_field_maps()
            group = detail_fields.get(int(event.update_field)) or measurement_fields.get(
                int(event.event_type), frozenset()
            )
            state = DeviceState(measurements=replace(MeasurementsState(), **values))
            self._store.dispatch(
                StateObservation(
                    state=state,
                    observed_fields=observed,
                    unavailable_fields=group - observed,
                )
            )
            return
        if spec is None:
            return
        payload, attribute, field_name = spec
        if not event.HasField(payload):
            self._mark_temporarily_unavailable(frozenset({field_name}))
            return
        value = getattr(getattr(event, payload), attribute)
        if isinstance(value, str):
            value = value or None
        if field_name == StateField.DEVICE_OPERATING_STATE:
            state = DeviceState(connection=replace(DeviceState().connection, device_operating_state=value))
        else:
            state = DeviceState(measurements=replace(MeasurementsState(), **{field_name.value: value}))
        self._store.dispatch(StateObservation(state=state, observed_fields=frozenset({field_name})))

    def handle_ohpcf_event(self, event: Any) -> None:
        """Convert compressor-flexibility events."""
        if not self._matches(event.ski):
            return
        if event.event_type == proto_stubs.OHPCFEventType.OHPCF_EVENT_SUPPORT_UPDATED:
            self._support_changed(CapabilityKey.OHPCF)
            return
        if event.event_type not in (
            proto_stubs.OHPCFEventType.OHPCF_EVENT_STATE_UPDATED,
            proto_stubs.OHPCFEventType.OHPCF_EVENT_DATA_UPDATED,
        ):
            return
        if not event.HasField("flexibility"):
            self._mark_temporarily_unavailable(
                frozenset({StateField.COMPRESSOR_FLEXIBILITY}),
                CapabilityKey.OHPCF,
            )
            return
        flex = event.flexibility
        incoming = CompressorFlexibilityState(
            available=flex.available,
            state=proto_stubs.CompressorPowerConsumptionState.Name(flex.state),
            requested_power_estimate_w=(
                flex.requested_power_estimate_w if flex.HasField("requested_power_estimate_w") else None
            ),
            requested_power_max_w=(flex.requested_power_max_w if flex.HasField("requested_power_max_w") else None),
            is_pausable=flex.is_pausable,
            is_stoppable=flex.is_stoppable,
            minimal_run_seconds=flex.minimal_run_seconds,
            minimal_pause_seconds=flex.minimal_pause_seconds,
            start_time=flex.start_time.ToDatetime(tzinfo=UTC) if flex.HasField("start_time") else None,
        )
        value = incoming
        current = self._store.state.ohpcf.compressor_flexibility
        field = int(event.update_field)
        if current is not None:
            update_field = proto_stubs.OHPCFUpdateField
            if field == int(update_field.OHPCF_UPDATE_FIELD_STATE):
                value = replace(current, available=incoming.available, state=incoming.state)
            elif field == int(update_field.OHPCF_UPDATE_FIELD_STOPPABLE):
                value = replace(current, is_stoppable=incoming.is_stoppable)
            elif field == int(update_field.OHPCF_UPDATE_FIELD_PAUSABLE):
                value = replace(current, is_pausable=incoming.is_pausable)
            elif field == int(update_field.OHPCF_UPDATE_FIELD_START_TIME):
                value = replace(current, start_time=incoming.start_time)
            elif field == int(update_field.OHPCF_UPDATE_FIELD_REQUESTED_POWER_ESTIMATE):
                value = replace(current, requested_power_estimate_w=incoming.requested_power_estimate_w)
            elif field == int(update_field.OHPCF_UPDATE_FIELD_REQUESTED_POWER_MAX):
                value = replace(current, requested_power_max_w=incoming.requested_power_max_w)
            elif field == int(update_field.OHPCF_UPDATE_FIELD_MINIMAL_RUN_DURATION):
                value = replace(current, minimal_run_seconds=incoming.minimal_run_seconds)
            elif field == int(update_field.OHPCF_UPDATE_FIELD_MINIMAL_PAUSE_DURATION):
                value = replace(current, minimal_pause_seconds=incoming.minimal_pause_seconds)
        self._store.dispatch(
            StateObservation(
                state=DeviceState(ohpcf=OHPCFState(compressor_flexibility=value)),
                observed_fields=frozenset({StateField.COMPRESSOR_FLEXIBILITY}),
                capability_results=(CapabilityResult(CapabilityKey.OHPCF, None),),
            )
        )

    def handle_dhw_event(self, event: Any) -> None:
        """Convert DHW setpoint events."""
        if not self._matches(event.ski):
            return
        if event.event_type == proto_stubs.DHWEventType.DHW_EVENT_SETPOINT_UPDATED:
            if not event.HasField("setpoint"):
                self._mark_temporarily_unavailable(frozenset({StateField.DHW_SETPOINT}), CapabilityKey.DHW)
                return
            self._store.dispatch(
                StateObservation(
                    state=DeviceState(dhw=DHWState(setpoint=_setpoint_to_dict(event.setpoint))),
                    observed_fields=frozenset({StateField.DHW_SETPOINT}),
                    capability_results=(CapabilityResult(CapabilityKey.DHW, None),),
                )
            )
        elif event.event_type == proto_stubs.DHWEventType.DHW_EVENT_SUPPORT_UPDATED:
            self._support_changed(CapabilityKey.DHW)

    def handle_dhw_system_function_event(self, event: Any) -> None:
        """Convert DHW system-function events."""
        if not self._matches(event.ski):
            return
        if event.event_type == proto_stubs.DHWSystemFunctionEventType.DHW_SYSTEM_FUNCTION_EVENT_STATE_UPDATED:
            if not event.HasField("state"):
                self._mark_temporarily_unavailable(
                    frozenset({StateField.DHW_SYSTEM_FUNCTION}),
                    CapabilityKey.DHW_SYSTEM_FUNCTION,
                )
                return
            self._store.dispatch(
                StateObservation(
                    state=DeviceState(dhw=DHWState(system_function=_dhw_system_function_to_dict(event.state))),
                    observed_fields=frozenset({StateField.DHW_SYSTEM_FUNCTION}),
                    capability_results=(CapabilityResult(CapabilityKey.DHW_SYSTEM_FUNCTION, None),),
                )
            )
        elif event.event_type == proto_stubs.DHWSystemFunctionEventType.DHW_SYSTEM_FUNCTION_EVENT_SUPPORT_UPDATED:
            self._support_changed(CapabilityKey.DHW_SYSTEM_FUNCTION)

    def handle_room_heating_event(self, event: Any) -> None:
        """Convert room-heating state events atomically."""
        if not self._matches(event.ski):
            return
        if event.event_type == proto_stubs.RoomHeatingEventType.ROOM_HEATING_EVENT_SUPPORT_UPDATED:
            self._support_changed(CapabilityKey.ROOM_HEATING)
            return
        if not event.HasField("state"):
            self._mark_temporarily_unavailable(
                frozenset(
                    {
                        StateField.ROOM_HEATING_SETPOINT,
                        StateField.ROOM_HEATING_SYSTEM_FUNCTION,
                        StateField.ROOM_TEMPERATURE_C,
                    }
                ),
                CapabilityKey.ROOM_HEATING,
            )
            return
        values = _room_heating_from_proto(event.state)
        state = DeviceState(
            hvac=HVACState(
                setpoint=values.setpoint,
                system_function=values.system_function,
            ),
            measurements=MeasurementsState(room_temperature_c=values.current_temperature_celsius),
        )
        fields = set()
        if event.state.HasField("setpoint"):
            fields.add(StateField.ROOM_HEATING_SETPOINT)
        if event.state.HasField("system_function"):
            fields.add(StateField.ROOM_HEATING_SYSTEM_FUNCTION)
        if values.current_temperature_celsius is not None:
            fields.add(StateField.ROOM_TEMPERATURE_C)
        self._store.dispatch(
            StateObservation(
                state=state,
                observed_fields=frozenset(fields),
                capability_results=(CapabilityResult(CapabilityKey.ROOM_HEATING, None),),
            )
        )
