"""Home Assistant facade for one authoritative EEBUS device session."""

from __future__ import annotations

import logging
from datetime import timedelta
from typing import TYPE_CHECKING, Any

import grpc.aio
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import ConfigEntryAuthFailed, ServiceValidationError
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed

from .const import SECURITY_MODE_LOOPBACK
from .device_session import DeviceSession, WriteOutcome
from .device_streams import DeviceStreams
from .grpc_client import GrpcChannelManager
from .providers import ProviderManager
from .snapshot import DevicePoller
from .state import (
    CapabilityKey,
    CapabilityResult,
    DeviceState,
    DeviceStateStore,
    StateObservation,
)

if TYPE_CHECKING:
    from . import proto_stubs

_LOGGER = logging.getLogger(__name__)

# Push is primary; polling reconciles fields not carried by compatibility streams.
POLL_INTERVAL = timedelta(minutes=5)


class EebusCoordinator(DataUpdateCoordinator[DeviceState]):
    """Expose session lifecycle and immutable state to Home Assistant."""

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
        super().__init__(hass, _LOGGER, name="EEBUS", update_interval=POLL_INTERVAL)
        self.host = host
        self.port = port
        self.ski = ski
        self.security_mode = security_mode
        self.tls_ca_certificate = tls_ca_certificate
        self.auth_token = auth_token
        self._channel_manager = GrpcChannelManager(host, port, security_mode, tls_ca_certificate, auth_token)
        self._state_store = DeviceStateStore(self._publish_state)
        self._poller = DevicePoller(ski, self._ensure_channel, self._state_store)
        self._device_session = DeviceSession(ski, self._ensure_channel)
        self._device_streams = DeviceStreams(
            hass,
            self._channel_manager,
            ski,
            self._state_store,
            self.async_request_refresh,
        )
        self._provider_manager = ProviderManager(
            hass,
            ski,
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
        self._was_unavailable = False

    def _publish_state(self, state: DeviceState) -> None:
        """Publish one already-reduced immutable state atomically."""
        self.async_set_updated_data(state)

    async def _ensure_channel(self) -> grpc.aio.Channel:
        return await self._channel_manager.ensure_channel()

    async def _async_update_data(self) -> DeviceState:
        """Ask the poller to reconcile state without overwriting newer events."""
        try:
            state = await self._poller.poll()
            if self._was_unavailable:
                _LOGGER.info("EEBUS bridge connection restored at %s:%s", self.host, self.port)
                self._was_unavailable = False
            return state
        except grpc.aio.AioRpcError as err:
            await self._channel_manager.invalidate()
            self._poller.reset_after_transport_error()
            if err.code() == grpc.StatusCode.UNAUTHENTICATED:
                raise ConfigEntryAuthFailed("Bridge authentication failed") from err
            if not self._was_unavailable:
                _LOGGER.warning("EEBUS bridge unavailable at %s:%s: %s", self.host, self.port, err)
                self._was_unavailable = True
            raise UpdateFailed(f"gRPC error: {err}") from err

    def _finish_write(self, outcome: WriteOutcome, capability: CapabilityKey) -> None:
        """Reduce write capability status and surface classified failures."""
        self._state_store.dispatch(
            StateObservation(capability_results=(CapabilityResult(capability, outcome.status_code),))
        )
        if outcome.validation_error is not None:
            raise ServiceValidationError(outcome.validation_error) from outcome.error
        if outcome.unimplemented:
            return
        if outcome.error is not None:
            raise outcome.error

    async def async_write_lpc_limit(self, value_watts: float) -> None:
        outcome = await self._device_session.write_lpc_limit(value_watts)
        self._finish_write(outcome, CapabilityKey.LPC)

    async def async_write_failsafe_limit(self, value_watts: float) -> None:
        outcome = await self._device_session.write_failsafe_limit(value_watts)
        self._finish_write(outcome, CapabilityKey.FAILSAFE)

    async def async_set_lpc_active(self, active: bool) -> None:
        outcome = await self._device_session.set_lpc_active(active)
        self._finish_write(outcome, CapabilityKey.LPC)

    async def async_control_compressor(self, action: proto_stubs.OHPCFAction) -> None:
        outcome = await self._device_session.control_compressor(action)
        try:
            self._finish_write(outcome, CapabilityKey.OHPCF)
        except grpc.aio.AioRpcError as err:
            raise ServiceValidationError(f"Compressor flexibility control failed: {err.details()}") from err

    async def async_write_dhw_setpoint(self, value_celsius: float) -> None:
        outcome = await self._device_session.write_dhw_setpoint(value_celsius)
        self._finish_write(outcome, CapabilityKey.DHW)

    async def async_set_dhw_boost(self, active: bool) -> None:
        outcome = await self._device_session.set_dhw_boost(active)
        self._finish_write(outcome, CapabilityKey.DHW_SYSTEM_FUNCTION)

    async def async_set_dhw_operation_mode(self, mode: str) -> None:
        outcome = await self._device_session.set_dhw_operation_mode(mode)
        self._finish_write(outcome, CapabilityKey.DHW_SYSTEM_FUNCTION)

    async def async_set_room_heating_temperature(self, value_celsius: float) -> None:
        outcome = await self._device_session.set_room_heating_temperature(value_celsius)
        self._finish_write(outcome, CapabilityKey.ROOM_HEATING)

    async def async_set_room_heating_mode(self, mode: str) -> None:
        outcome = await self._device_session.set_room_heating_mode(mode)
        self._finish_write(outcome, CapabilityKey.ROOM_HEATING)

    @property
    def grid_push_enabled(self) -> bool:
        return self._provider_manager.grid_push_enabled

    async def async_push_grid_data(self) -> None:
        await self._provider_manager.async_push_grid_data()

    def async_start_grid_push(self) -> None:
        self._provider_manager.async_start_grid_push()

    @property
    def pv_push_enabled(self) -> bool:
        return self._provider_manager.pv_push_enabled

    async def async_push_pv_data(self) -> None:
        await self._provider_manager.async_push_pv_data()

    def async_start_pv_push(self) -> None:
        self._provider_manager.async_start_pv_push()

    @property
    def battery_push_enabled(self) -> bool:
        return self._provider_manager.battery_push_enabled

    async def async_push_battery_data(self) -> None:
        await self._provider_manager.async_push_battery_data()

    def async_start_battery_push(self) -> None:
        self._provider_manager.async_start_battery_push()

    def async_start_streams(self) -> None:
        self._device_streams.start()

    # Compatibility seams kept for focused event-conversion tests. Conversion
    # itself belongs to DeviceStreams, never to the coordinator facade.
    def _handle_device_event(self, event: Any) -> None:
        self._device_streams.handle_device_event(event)

    def _handle_lpc_event(self, event: Any) -> None:
        self._device_streams.handle_lpc_event(event)

    def _handle_measurement_event(self, event: Any) -> None:
        self._device_streams.handle_measurement_event(event)

    def _handle_ohpcf_event(self, event: Any) -> None:
        self._device_streams.handle_ohpcf_event(event)

    def _handle_dhw_event(self, event: Any) -> None:
        self._device_streams.handle_dhw_event(event)

    def _handle_dhw_sysfn_event(self, event: Any) -> None:
        self._device_streams.handle_dhw_system_function_event(event)

    def _handle_room_heating_event(self, event: Any) -> None:
        self._device_streams.handle_room_heating_event(event)

    async def async_shutdown(self) -> None:
        """Stop entry-scoped resources in dependency order."""
        await self._provider_manager.async_stop()
        await self._device_streams.stop()
        await self._channel_manager.close()
