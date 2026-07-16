"""DataUpdateCoordinator for EEBUS integration."""

from __future__ import annotations

import logging
from dataclasses import replace
from datetime import timedelta
from functools import lru_cache
from typing import TYPE_CHECKING, Any, cast

import grpc
import grpc.aio

from homeassistant.core import HomeAssistant
from homeassistant.exceptions import ConfigEntryAuthFailed, ServiceValidationError
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed

from .const import SECURITY_MODE_LOOPBACK
from .grpc_client import (
    RPC_TIMEOUT,
    WRITE_VALIDATION_CODES,
    GrpcChannelManager,
    is_unimplemented as _is_unimplemented,
)
from .models import (
    CoordinatorSnapshot,
    _dhw_system_function_to_dict,
    _setpoint_to_dict,
    _system_function_to_dict,
)
from .providers import ProviderManager
from .snapshot import SnapshotSupport, async_build_snapshot
from .ski import normalize_ski
from .state import DomainState, apply_reading, flatten, next_capability_state
from .streams import ConsumeFn, StreamManager

if TYPE_CHECKING:
    from . import proto_stubs

_LOGGER = logging.getLogger(__name__)

# Event streams deliver push updates; polling only reconciles state the
# streams cannot carry (scoped energy, heartbeat, support flags).
POLL_INTERVAL = timedelta(minutes=5)


@lru_cache(maxsize=1)
def _measurement_event_map() -> tuple[dict[int, tuple[str, str, str]], frozenset[int]]:
    """Build the measurement-event dispatch tables.

    Streaming twin of FLAT_MEASUREMENT_TYPE_TO_KEY: maps an event type to
    (payload field, value attribute, coordinator key). Support events carry no
    payload and always reconcile via a poll. Cached because the proto enum is
    only importable at runtime.
    """
    from . import proto_stubs

    event_type = proto_stubs.MeasurementEventType
    value_events: dict[int, tuple[str, str, str]] = {
        event_type.MEASUREMENT_EVENT_POWER_UPDATED: ("power", "watts", "power_watts"),
        event_type.MEASUREMENT_EVENT_ENERGY_UPDATED: (
            "energy",
            "kilowatt_hours",
            "energy_consumed_kwh",
        ),
        event_type.MEASUREMENT_EVENT_DHW_TEMPERATURE_UPDATED: (
            "measurement",
            "value",
            "dhw_temperature_c",
        ),
        event_type.MEASUREMENT_EVENT_ROOM_TEMPERATURE_UPDATED: (
            "measurement",
            "value",
            "room_temperature_c",
        ),
        event_type.MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_UPDATED: (
            "measurement",
            "value",
            "outdoor_temperature_c",
        ),
        event_type.MEASUREMENT_EVENT_FLOW_TEMPERATURE_UPDATED: (
            "measurement",
            "value",
            "flow_temperature_c",
        ),
        event_type.MEASUREMENT_EVENT_RETURN_TEMPERATURE_UPDATED: (
            "measurement",
            "value",
            "return_temperature_c",
        ),
        event_type.MEASUREMENT_EVENT_DEVICE_OPERATING_STATE_UPDATED: (
            "device_diagnostics",
            "operating_state",
            "device_operating_state",
        ),
    }
    support_events = frozenset(
        {
            event_type.MEASUREMENT_EVENT_DHW_TEMPERATURE_SUPPORT_UPDATED,
            event_type.MEASUREMENT_EVENT_ROOM_TEMPERATURE_SUPPORT_UPDATED,
            event_type.MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_SUPPORT_UPDATED,
        }
    )
    return value_events, support_events


class EebusCoordinator(DataUpdateCoordinator[CoordinatorSnapshot]):
    """Coordinator that manages gRPC connection and data updates."""

    def __init__(
        self,
        hass: HomeAssistant,
        host: str,
        port: int,
        ski: str,
        security_mode: str = SECURITY_MODE_LOOPBACK,
        tls_ca_certificate: str | None = None,
        auth_token: str | None = None,
        grid_power_entity: str | None = None,
        grid_feed_in_energy_entity: str | None = None,
        grid_consumption_energy_entity: str | None = None,
        pv_power_entity: str | None = None,
        pv_yield_energy_entity: str | None = None,
        pv_peak_power_entity: str | None = None,
        battery_power_entity: str | None = None,
        battery_charged_energy_entity: str | None = None,
        battery_discharged_energy_entity: str | None = None,
        battery_soc_entity: str | None = None,
    ) -> None:
        """Initialize the coordinator."""
        super().__init__(
            hass,
            _LOGGER,
            name="EEBUS",
            update_interval=POLL_INTERVAL,
        )
        self.host = host
        self.port = port
        self.ski = ski
        self.security_mode = security_mode
        self.tls_ca_certificate = tls_ca_certificate
        self.auth_token = auth_token
        self._channel_manager = GrpcChannelManager(
            host, port, security_mode, tls_ca_certificate, auth_token
        )
        self._stream_manager = StreamManager(self.hass, self._channel_manager)
        self._provider_manager = ProviderManager(
            self.hass,
            self.ski,
            self._ensure_channel,
            grid_power_entity=grid_power_entity,
            grid_feed_in_energy_entity=grid_feed_in_energy_entity,
            grid_consumption_energy_entity=grid_consumption_energy_entity,
            pv_power_entity=pv_power_entity,
            pv_yield_energy_entity=pv_yield_energy_entity,
            pv_peak_power_entity=pv_peak_power_entity,
            battery_power_entity=battery_power_entity,
            battery_charged_energy_entity=battery_charged_energy_entity,
            battery_discharged_energy_entity=battery_discharged_energy_entity,
            battery_soc_entity=battery_soc_entity,
        )
        self._was_unavailable: bool = False
        self._domain_state: DomainState = DomainState()
        self._ski_registered: bool = False
        self._not_found_streak: int = 0

    async def _ensure_channel(self) -> grpc.aio.Channel:
        """Create or return existing gRPC channel."""
        return await self._channel_manager.ensure_channel()

    async def _async_update_data(self) -> CoordinatorSnapshot:
        """Fetch data via gRPC polling."""
        try:
            channel = await self._ensure_channel()
            capabilities = self._domain_state.capabilities
            result = await async_build_snapshot(
                channel,
                self.ski,
                SnapshotSupport(
                    lpc=capabilities.lpc,
                    failsafe=capabilities.failsafe,
                    heartbeat=capabilities.heartbeat,
                    ohpcf=capabilities.ohpcf,
                    dhw=capabilities.dhw,
                    dhw_system_function=capabilities.dhw_system_function,
                    room_heating=capabilities.room_heating,
                ),
                ski_registered=self._ski_registered,
                not_found_streak=self._not_found_streak,
            )
            updated_capabilities = replace(
                capabilities,
                lpc=result.support.lpc,
                failsafe=result.support.failsafe,
                heartbeat=result.support.heartbeat,
                ohpcf=result.support.ohpcf,
                dhw=result.support.dhw,
                dhw_system_function=result.support.dhw_system_function,
                room_heating=result.support.room_heating,
            )
            self._domain_state = replace(
                self._domain_state, capabilities=updated_capabilities
            )
            self._ski_registered = result.ski_registered
            self._not_found_streak = result.not_found_streak

            if self._was_unavailable:
                _LOGGER.info("EEBUS bridge connection restored at %s:%s", self.host, self.port)
                self._was_unavailable = False

            return result.snapshot
        except grpc.aio.AioRpcError as err:
            await self._channel_manager.invalidate()
            self._not_found_streak = 0

            if err.code() == grpc.StatusCode.UNAUTHENTICATED:
                raise ConfigEntryAuthFailed("Bridge authentication failed") from err

            if not self._was_unavailable:
                _LOGGER.warning(
                    "EEBUS bridge unavailable at %s:%s: %s", self.host, self.port, err
                )
                self._was_unavailable = True

            raise UpdateFailed(f"gRPC error: {err}") from err
    async def _async_write_rpc(
        self,
        label: str,
        call: Any,
        request: Any,
        support_attr: str | None = None,
        validation: bool = False,
    ) -> None:
        """Run a write RPC with shared UNIMPLEMENTED / validation-error mapping.

        On success the capability becomes available; classified failures use
        the shared capability transition rule. UNIMPLEMENTED returns quietly.
        With ``validation=True``, device-side rejections
        (WRITE_VALIDATION_CODES) surface as ServiceValidationError.
        """
        try:
            await call(request, timeout=RPC_TIMEOUT)
            if support_attr is not None:
                capabilities = self._domain_state.capabilities
                updated_capabilities = replace(
                    capabilities,
                    **{
                        support_attr: next_capability_state(
                            getattr(capabilities, support_attr), None
                        )
                    },
                )
                self._domain_state = replace(
                    self._domain_state, capabilities=updated_capabilities
                )
        except grpc.aio.AioRpcError as err:
            if support_attr is not None:
                capabilities = self._domain_state.capabilities
                updated_capabilities = replace(
                    capabilities,
                    **{
                        support_attr: next_capability_state(
                            getattr(capabilities, support_attr), err.code()
                        )
                    },
                )
                self._domain_state = replace(
                    self._domain_state, capabilities=updated_capabilities
                )
            if _is_unimplemented(err):
                _LOGGER.info(
                    "%s unsupported for SKI %s: %s", label, self.ski, err.details()
                )
                return
            if validation and err.code() in WRITE_VALIDATION_CODES:
                raise ServiceValidationError(f"{label} failed: {err.details()}") from err
            raise

    async def async_write_lpc_limit(self, value_watts: float) -> None:
        """Write LPC consumption limit via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.lpc_service_stub(channel)
        await self._async_write_rpc(
            "LPC write",
            stub.WriteConsumptionLimit,
            proto_stubs.WriteLoadLimitRequest(
                ski=self.ski, value_watts=value_watts, is_active=True
            ),
            support_attr="lpc",
        )

    async def async_write_failsafe_limit(self, value_watts: float) -> None:
        """Write failsafe limit via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.lpc_service_stub(channel)
        await self._async_write_rpc(
            "Failsafe write",
            stub.WriteFailsafeLimit,
            proto_stubs.WriteFailsafeLimitRequest(ski=self.ski, value_watts=value_watts),
            support_attr="failsafe",
        )

    async def async_set_lpc_active(self, active: bool) -> None:
        """Activate or deactivate LPC limit via gRPC."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.lpc_service_stub(channel)
        current = await stub.GetConsumptionLimit(
            proto_stubs.DeviceRequest(ski=self.ski), timeout=RPC_TIMEOUT
        )
        await self._async_write_rpc(
            "LPC activation",
            stub.WriteConsumptionLimit,
            proto_stubs.WriteLoadLimitRequest(
                ski=self.ski,
                value_watts=current.value_watts,
                is_active=active,
            ),
            support_attr="lpc",
        )

    async def async_control_compressor(self, action: proto_stubs.OHPCFAction) -> None:
        """Schedule/pause/resume/abort the compressor's optional consumption."""
        channel = await self._ensure_channel()
        from . import proto_stubs
        stub = proto_stubs.ohpcf_service_stub(channel)
        try:
            await self._async_write_rpc(
                "OHPCF control",
                stub.ControlCompressorFlexibility,
                proto_stubs.ControlCompressorRequest(ski=self.ski, action=action),
                support_attr="ohpcf",
            )
        except grpc.aio.AioRpcError as err:
            # Surface device-side rejections (e.g. "data not available" when the
            # compressor advertises no writable offer — heating-side OHPCF not yet
            # commissioned) as a clean validation error (HTTP 400 + message) instead
            # of bubbling a raw AioRpcError into an aiohttp 500 traceback.
            raise ServiceValidationError(
                f"Compressor flexibility control failed: {err.details()}"
            ) from err

    async def async_write_dhw_setpoint(self, value_celsius: float) -> None:
        """Write the domestic-hot-water target via the bridge."""
        channel = await self._ensure_channel()
        from . import proto_stubs

        stub = proto_stubs.dhw_service_stub(channel)
        await self._async_write_rpc(
            "Domestic hot water setpoint",
            stub.SetDHWSetpoint,
            proto_stubs.SetDHWSetpointRequest(ski=self.ski, value_celsius=value_celsius),
            support_attr="dhw",
            validation=True,
        )

    async def async_set_dhw_boost(self, active: bool) -> None:
        """Activate or cancel DHW one-time boost."""
        channel = await self._ensure_channel()
        from . import proto_stubs

        stub = proto_stubs.dhw_service_stub(channel)
        await self._async_write_rpc(
            "Domestic hot water boost",
            stub.SetDHWBoost,
            proto_stubs.SetDHWBoostRequest(ski=self.ski, active=active),
            support_attr="dhw_system_function",
            validation=True,
        )

    async def async_set_dhw_operation_mode(self, mode: str) -> None:
        """Set the DHW operation mode by advertised mode type."""
        channel = await self._ensure_channel()
        from . import proto_stubs

        stub = proto_stubs.dhw_service_stub(channel)
        await self._async_write_rpc(
            "Domestic hot water operation mode",
            stub.SetDHWOperationMode,
            proto_stubs.SetDHWOperationModeRequest(ski=self.ski, mode=mode),
            support_attr="dhw_system_function",
            validation=True,
        )

    async def async_set_room_heating_temperature(self, value_celsius: float) -> None:
        """Set the room-heating target temperature."""
        channel = await self._ensure_channel()
        from . import proto_stubs

        await self._async_write_rpc(
            "Room heating setpoint",
            proto_stubs.hvac_service_stub(channel).SetRoomHeatingTemperature,
            proto_stubs.SetRoomHeatingTemperatureRequest(
                ski=self.ski, value_celsius=value_celsius
            ),
            support_attr="room_heating",
            validation=True,
        )

    async def async_set_room_heating_mode(self, mode: str) -> None:
        """Set the room-heating operation mode."""
        channel = await self._ensure_channel()
        from . import proto_stubs

        await self._async_write_rpc(
            "Room heating mode",
            proto_stubs.hvac_service_stub(channel).SetRoomHeatingMode,
            proto_stubs.SetRoomHeatingModeRequest(ski=self.ski, mode=mode),
            support_attr="room_heating",
            validation=True,
        )

    @property
    def grid_push_enabled(self) -> bool:
        """Return True when a grid power sensor is mapped to the MGCP provider."""
        return self._provider_manager.grid_push_enabled

    async def async_push_grid_data(self) -> None:
        """Push mapped grid sensor values to the bridge."""
        await self._provider_manager.async_push_grid_data()

    def async_start_grid_push(self) -> None:
        """Track mapped grid sensors and push their values to the bridge."""
        self._provider_manager.async_start_grid_push()

    @property
    def pv_push_enabled(self) -> bool:
        """Return True when a PV power sensor is mapped to the VAPD provider."""
        return self._provider_manager.pv_push_enabled

    async def async_push_pv_data(self) -> None:
        """Push mapped PV sensor values to the bridge."""
        await self._provider_manager.async_push_pv_data()

    def async_start_pv_push(self) -> None:
        """Track mapped PV sensors and push their values to the bridge."""
        self._provider_manager.async_start_pv_push()

    @property
    def battery_push_enabled(self) -> bool:
        """Return True when a battery power sensor is mapped to the VABD provider."""
        return self._provider_manager.battery_push_enabled

    async def async_push_battery_data(self) -> None:
        """Push mapped battery sensor values to the bridge."""
        await self._provider_manager.async_push_battery_data()

    def async_start_battery_push(self) -> None:
        """Track mapped battery sensors and push their values to the bridge."""
        self._provider_manager.async_start_battery_push()

    def async_start_streams(self) -> None:
        """Start background tasks consuming bridge event streams."""
        async def consume(channel: grpc.aio.Channel) -> None:
            from . import proto_stubs

            stub = proto_stubs.device_service_stub(channel)
            async for event in stub.SubscribeDeviceEvents(proto_stubs.Empty()):
                self._handle_device_event(event)

        async def consume_lpc(channel: grpc.aio.Channel) -> None:
            from . import proto_stubs

            stub = proto_stubs.lpc_service_stub(channel)
            async for event in stub.SubscribeLPCEvents(
                proto_stubs.DeviceRequest(ski=self.ski)
            ):
                self._handle_lpc_event(event)

        async def consume_measurements(channel: grpc.aio.Channel) -> None:
            from . import proto_stubs

            stub = proto_stubs.monitoring_service_stub(channel)
            async for event in stub.SubscribeMeasurements(
                proto_stubs.DeviceRequest(ski=self.ski)
            ):
                self._handle_measurement_event(event)

        async def consume_dhw(channel: grpc.aio.Channel) -> None:
            from . import proto_stubs

            stub = proto_stubs.dhw_service_stub(channel)
            async for event in stub.SubscribeDHWEvents(
                proto_stubs.DeviceRequest(ski=self.ski)
            ):
                self._handle_dhw_event(event)

        async def consume_dhw_sysfn(channel: grpc.aio.Channel) -> None:
            from . import proto_stubs

            stub = proto_stubs.dhw_service_stub(channel)
            async for event in stub.SubscribeDHWSystemFunctionEvents(
                proto_stubs.DeviceRequest(ski=self.ski)
            ):
                self._handle_dhw_sysfn_event(event)

        async def consume_room_heating(channel: grpc.aio.Channel) -> None:
            from . import proto_stubs

            stub = proto_stubs.hvac_service_stub(channel)
            async for event in stub.SubscribeRoomHeatingEvents(
                proto_stubs.DeviceRequest(ski=self.ski)
            ):
                self._handle_room_heating_event(event)

        streams: dict[str, ConsumeFn] = {
            "device_events": consume,
            "lpc_events": consume_lpc,
            "measurements": consume_measurements,
            "dhw_events": consume_dhw,
            "dhw_sysfn_events": consume_dhw_sysfn,
            "room_heating_events": consume_room_heating,
        }
        self._stream_manager.start(streams, f"eebus_{{name}}_{self.ski}")

    def _event_matches(self, event_ski: str) -> bool:
        """Return True when an event applies to the configured device."""
        return normalize_ski(event_ski) == normalize_ski(self.ski)

    def _push_data(self, updates: dict[str, Any]) -> None:
        """Merge stream updates into coordinator data and notify listeners."""
        if self.data is None:
            return
        merged = dict(self.data)
        merged.update(updates)
        self.async_set_updated_data(cast(CoordinatorSnapshot, merged))

    def _push_domain_fields(self, *field_names: str) -> None:
        """Publish selected flattened fields from the grouped domain state."""
        snapshot = dict(flatten(self._domain_state))
        self._push_data({name: snapshot[name] for name in field_names})

    def _handle_device_event(self, event: Any) -> None:
        from . import proto_stubs

        if not self._event_matches(event.ski):
            return
        event_type = event.event_type
        if event_type == proto_stubs.DeviceEventType.DEVICE_EVENT_TRUST_REMOVED:
            _LOGGER.warning("EEBUS device %s removed trust with bridge", event.ski)
        elif event_type not in (
            proto_stubs.DeviceEventType.DEVICE_EVENT_CONNECTED,
            proto_stubs.DeviceEventType.DEVICE_EVENT_DISCONNECTED,
        ):
            return
        # Connection state changed; reconcile everything via one poll.
        self.hass.async_create_task(self.async_request_refresh())

    def _handle_lpc_event(self, event: Any) -> None:
        from . import proto_stubs

        if not self._event_matches(event.ski):
            return
        event_type = event.event_type
        if event_type == proto_stubs.LPCEventType.LPC_EVENT_LIMIT_UPDATED:
            if not event.HasField("limit_update"):
                # Bridge signalled a change but sent no payload; reconcile via poll.
                self.hass.async_create_task(self.async_request_refresh())
                return
            limit = event.limit_update
            lpc, capabilities = apply_reading(
                self._domain_state.lpc,
                "consumption_limit",
                {
                    "value_watts": limit.value_watts,
                    "is_active": limit.is_active,
                    "is_changeable": limit.is_changeable,
                },
                self._domain_state.capabilities,
                "lpc",
                None,
            )
            self._domain_state = replace(
                self._domain_state, lpc=lpc, capabilities=capabilities
            )
            self._push_domain_fields("consumption_limit", "lpc_supported")
        elif event_type == proto_stubs.LPCEventType.LPC_EVENT_FAILSAFE_UPDATED:
            if not event.HasField("failsafe_update"):
                self.hass.async_create_task(self.async_request_refresh())
                return
            failsafe = event.failsafe_update
            lpc, capabilities = apply_reading(
                self._domain_state.lpc,
                "failsafe_limit",
                {
                    "value_watts": failsafe.value_watts,
                    "duration_minimum_seconds": failsafe.duration_minimum_seconds,
                },
                self._domain_state.capabilities,
                "failsafe",
                None,
            )
            self._domain_state = replace(
                self._domain_state, lpc=lpc, capabilities=capabilities
            )
            self._push_domain_fields("failsafe_limit", "failsafe_supported")
        elif event_type == proto_stubs.LPCEventType.LPC_EVENT_HEARTBEAT_TIMEOUT:
            self.hass.async_create_task(self.async_request_refresh())

    def _handle_measurement_event(self, event: Any) -> None:
        if not self._event_matches(event.ski):
            return
        value_events, support_events = _measurement_event_map()
        if event.event_type in support_events:
            self.hass.async_create_task(self.async_request_refresh())
            return
        spec = value_events.get(event.event_type)
        if spec is None:
            return
        field, attr, key = spec
        if not event.HasField(field):
            # Change signalled without a payload; reconcile via poll.
            self.hass.async_create_task(self.async_request_refresh())
            return
        value = getattr(getattr(event, field), attr)
        if isinstance(value, str):
            # Empty enum/state strings mean "no value" (device_operating_state).
            value = value or None
        if key == "device_operating_state":
            connection = replace(
                self._domain_state.connection, device_operating_state=value
            )
            self._domain_state = replace(self._domain_state, connection=connection)
        else:
            measurements = replace(self._domain_state.measurements, **{key: value})
            self._domain_state = replace(
                self._domain_state, measurements=measurements
            )
        self._push_domain_fields(key)

    def _handle_dhw_event(self, event: Any) -> None:
        from . import proto_stubs

        if not self._event_matches(event.ski):
            return
        if (
            event.event_type == proto_stubs.DHWEventType.DHW_EVENT_SETPOINT_UPDATED
            and event.HasField("setpoint")
        ):
            dhw, capabilities = apply_reading(
                self._domain_state.dhw,
                "setpoint",
                _setpoint_to_dict(event.setpoint),
                self._domain_state.capabilities,
                "dhw",
                None,
            )
            self._domain_state = replace(
                self._domain_state, dhw=dhw, capabilities=capabilities
            )
            self._push_domain_fields("dhw_setpoint", "dhw_supported")
        elif event.event_type == proto_stubs.DHWEventType.DHW_EVENT_SUPPORT_UPDATED:
            self.hass.async_create_task(self.async_request_refresh())

    def _handle_dhw_sysfn_event(self, event: Any) -> None:
        from . import proto_stubs

        if not self._event_matches(event.ski):
            return
        if (
            event.event_type
            == proto_stubs.DHWSystemFunctionEventType.DHW_SYSTEM_FUNCTION_EVENT_STATE_UPDATED
            and event.HasField("state")
        ):
            dhw, capabilities = apply_reading(
                self._domain_state.dhw,
                "system_function",
                _dhw_system_function_to_dict(event.state),
                self._domain_state.capabilities,
                "dhw_system_function",
                None,
            )
            self._domain_state = replace(
                self._domain_state, dhw=dhw, capabilities=capabilities
            )
            self._push_domain_fields(
                "dhw_system_function", "dhw_sysfn_supported"
            )
        elif (
            event.event_type
            == proto_stubs.DHWSystemFunctionEventType.DHW_SYSTEM_FUNCTION_EVENT_SUPPORT_UPDATED
        ):
            self.hass.async_create_task(self.async_request_refresh())

    def _handle_room_heating_event(self, event: Any) -> None:
        from . import proto_stubs

        if not self._event_matches(event.ski):
            return
        if event.event_type == proto_stubs.RoomHeatingEventType.ROOM_HEATING_EVENT_SUPPORT_UPDATED:
            self.hass.async_create_task(self.async_request_refresh())
            return
        if not event.HasField("state"):
            self.hass.async_create_task(self.async_request_refresh())
            return
        state = event.state
        field_names = ["room_heating_supported"]
        hvac, capabilities = apply_reading(
            self._domain_state.hvac,
            "setpoint",
            _setpoint_to_dict(state.setpoint) if state.HasField("setpoint") else None,
            self._domain_state.capabilities,
            "room_heating",
            None,
        )
        if state.HasField("setpoint"):
            field_names.append("room_heating_setpoint")
        hvac, capabilities = apply_reading(
            hvac,
            "system_function",
            (
                _system_function_to_dict(state.system_function)
                if state.HasField("system_function")
                else None
            ),
            capabilities,
            "room_heating",
            None,
        )
        if state.HasField("system_function"):
            field_names.append("room_heating_system_function")
        measurements = self._domain_state.measurements
        if state.HasField("current_temperature_celsius"):
            measurements = replace(
                measurements,
                room_temperature_c=state.current_temperature_celsius,
            )
            field_names.append("room_temperature_c")
        self._domain_state = replace(
            self._domain_state,
            measurements=measurements,
            hvac=hvac,
            capabilities=capabilities,
        )
        self._push_domain_fields(*field_names)

    async def async_shutdown(self) -> None:
        """Close gRPC channel and cancel stream tasks."""
        await self._provider_manager.async_stop()
        await self._stream_manager.stop()
        await self._channel_manager.close()
