"""Device stream lifecycle and protobuf-to-state observation conversion."""

from __future__ import annotations

import logging
from collections.abc import Callable, Coroutine
from dataclasses import replace
from functools import lru_cache
from typing import Any

import grpc.aio
from homeassistant.core import HomeAssistant

from . import proto_stubs
from .grpc_client import GrpcChannelManager
from .models import (
    CompressorFlexibilityState,
    ConsumptionLimitState,
    FailsafeState,
    _dhw_system_function_to_dict,
    _room_heating_from_proto,
    _setpoint_to_dict,
)
from .ski import normalize_ski
from .state import (
    CapabilityKey,
    CapabilityResult,
    DHWState,
    DeviceState,
    DeviceStateStore,
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


class DeviceStreams:
    """Own all legacy device streams and reduce their events through one store."""

    def __init__(
        self,
        hass: HomeAssistant,
        channel_manager: GrpcChannelManager,
        ski: str,
        store: DeviceStateStore,
        request_refresh: Callable[[], Coroutine[Any, Any, None]],
    ) -> None:
        self._hass = hass
        self._ski = ski
        self._store = store
        self._request_refresh = request_refresh
        self._manager = StreamManager(hass, channel_manager)

    def start(self) -> None:
        """Start all compatibility streams for this device."""

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

        streams: dict[str, ConsumeFn] = {
            "device_events": device,
            "lpc_events": lpc,
            "measurements": measurements,
            "ohpcf_events": ohpcf,
            "dhw_events": dhw,
            "dhw_sysfn_events": dhw_system_function,
            "room_heating_events": room_heating,
        }
        self._manager.start(streams, f"eebus_{{name}}_{self._ski}")

    async def stop(self) -> None:
        """Stop every stream before transport shutdown."""
        await self._manager.stop()

    def _matches(self, event_ski: str) -> bool:
        return normalize_ski(event_ski) == normalize_ski(self._ski)

    def _refresh(self) -> None:
        self._hass.async_create_task(self._request_refresh())

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
            self._mark_temporarily_unavailable(
                frozenset({StateField.HEARTBEAT_STATUS}),
                CapabilityKey.HEARTBEAT,
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
        value = CompressorFlexibilityState(
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
        )
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
        fields = {
            StateField.ROOM_HEATING_SETPOINT,
            StateField.ROOM_HEATING_SYSTEM_FUNCTION,
        }
        if values.current_temperature_celsius is not None:
            fields.add(StateField.ROOM_TEMPERATURE_C)
        self._store.dispatch(
            StateObservation(
                state=state,
                observed_fields=frozenset(fields),
                capability_results=(CapabilityResult(CapabilityKey.ROOM_HEATING, None),),
            )
        )
