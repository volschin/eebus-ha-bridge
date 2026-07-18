"""Home Assistant facade for one authoritative EEBUS device session."""

from __future__ import annotations

import asyncio
import logging
from collections.abc import Awaitable, Callable
from datetime import timedelta
from typing import Any

import grpc.aio
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import ConfigEntryAuthFailed, ConfigEntryNotReady, ServiceValidationError
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed

from .const import SECURITY_MODE_LOOPBACK
from .device_session import WriteOutcome
from . import proto_stubs
from .grpc_client import RPC_TIMEOUT
from .providers import ProviderManager
from .runtime import BridgeRuntime, BridgeRuntimeKey
from .state import (
    CapabilityKey,
    CapabilityResult,
    DeviceState,
    StateObservation,
)

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
        runtime: BridgeRuntime | None = None,
    ) -> None:
        super().__init__(hass, _LOGGER, name="EEBUS", update_interval=POLL_INTERVAL)
        self.host = host
        self.port = port
        self.ski = ski
        self.security_mode = security_mode
        self.tls_ca_certificate = tls_ca_certificate
        self.auth_token = auth_token
        self._hass_instance = hass
        self._reconfigure_lock = asyncio.Lock()
        self._entry_unloaded = False
        self._runtime_generation: object = object()
        self._owns_runtime = runtime is None
        self._runtime = runtime or BridgeRuntime(
            BridgeRuntimeKey.from_connection(
                host,
                port,
                security_mode,
                tls_ca_certificate,
                auth_token,
            ),
            tls_ca_certificate,
            auth_token,
        )
        initial_generation = self._runtime_generation
        self._runtime_session = self._runtime.create_device_session(
            hass,
            ski,
            lambda state: self._publish_session_state(initial_generation, state),
            self.async_request_refresh,
        )
        # Compatibility aliases for entity code and focused reducer/write tests.
        self._channel_manager = self._runtime.channel_manager
        self._state_store = self._runtime_session.store
        self._poller = self._runtime_session.poller
        self._device_session = self._runtime_session.writer
        self._device_streams = self._runtime_session.streams
        self._provider_manager = self._new_provider_manager(
            hass,
            grid_power_entity,
            grid_feed_in_energy_entity,
            grid_consumption_energy_entity,
            pv_power_entity,
            pv_yield_energy_entity,
            pv_peak_power_entity,
            battery_power_entity,
            battery_charged_energy_entity,
            battery_discharged_energy_entity,
            battery_soc_entity,
        )

    def _new_provider_manager(
        self,
        hass: HomeAssistant,
        grid_power_entity: str | None,
        grid_feed_in_energy_entity: str | None,
        grid_consumption_energy_entity: str | None,
        pv_power_entity: str | None,
        pv_yield_energy_entity: str | None,
        pv_peak_power_entity: str | None,
        battery_power_entity: str | None,
        battery_charged_energy_entity: str | None,
        battery_discharged_energy_entity: str | None,
        battery_soc_entity: str | None,
        ensure_channel: Callable[[], Awaitable[grpc.aio.Channel]] | None = None,
        supports_feature: Callable[[int], bool] | None = None,
    ) -> ProviderManager:
        return ProviderManager(
            hass,
            self.ski,
            ensure_channel or self._ensure_channel,
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
            supports_feature=supports_feature or self._runtime.supports,
        )

    async def async_reconfigure_providers(
        self,
        *,
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
        """Atomically replace entry-scoped provider mappings in-place."""
        replacement = self._new_provider_manager(
            self._hass_instance,
            grid_power_entity,
            grid_feed_in_energy_entity,
            grid_consumption_energy_entity,
            pv_power_entity,
            pv_yield_energy_entity,
            pv_peak_power_entity,
            battery_power_entity,
            battery_charged_energy_entity,
            battery_discharged_energy_entity,
            battery_soc_entity,
        )
        try:
            replacement.async_start_grid_push()
            replacement.async_start_pv_push()
            replacement.async_start_battery_push()
        except BaseException:
            await replacement.async_stop(invalidate=False)
            raise
        previous = self._provider_manager
        self._provider_manager = replacement
        try:
            await previous.async_stop(invalidate=False)
        except Exception:  # noqa: BLE001
            _LOGGER.exception("Failed to stop previous EEBUS provider manager")

    async def async_reconfigure_runtime(
        self,
        runtime: BridgeRuntime,
        *,
        host: str,
        port: int,
        security_mode: str,
        tls_ca_certificate: str | None,
        auth_token: str | None,
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
        """Stage and atomically adopt a fully operational replacement runtime."""
        replacement_generation = object()
        replacement_session = runtime.create_device_session(
            self._hass_instance,
            self.ski,
            lambda state: self._publish_session_state(replacement_generation, state),
            self.async_request_refresh,
        )
        replacement_provider: ProviderManager | None = None
        try:
            replacement_provider = self._new_provider_manager(
                self._hass_instance,
                grid_power_entity,
                grid_feed_in_energy_entity,
                grid_consumption_energy_entity,
                pv_power_entity,
                pv_yield_energy_entity,
                pv_peak_power_entity,
                battery_power_entity,
                battery_charged_energy_entity,
                battery_discharged_energy_entity,
                battery_soc_entity,
                runtime.channel_manager.ensure_channel,
                runtime.supports,
            )
            consolidated = runtime.supports(
                int(proto_stubs.FeatureId.FEATURE_CONSOLIDATED_DEVICE_STREAM)
            ) is True
            snapshots = runtime.supports(
                int(proto_stubs.FeatureId.FEATURE_DEVICE_SNAPSHOT)
            ) is True
            if consolidated and snapshots:
                await replacement_session.poller.ensure_registered()
                replacement_session.streams.mark_registered()
                replacement_session.streams.start()
                await replacement_session.streams.wait_initial_snapshot(RPC_TIMEOUT)
            else:
                await replacement_session.poller.poll()
                replacement_session.streams.start()
            replacement_provider.async_start_grid_push()
            replacement_provider.async_start_pv_push()
            replacement_provider.async_start_battery_push()
        except BaseException:
            try:
                if replacement_provider is not None:
                    await replacement_provider.async_stop(invalidate=False)
            finally:
                await runtime.release_device_session(replacement_session)
            raise

        previous_runtime = self._runtime
        previous_session = self._runtime_session
        previous_provider = self._provider_manager
        assert replacement_provider is not None
        self._runtime_generation = replacement_generation
        self._runtime = runtime
        self._runtime_session = replacement_session
        self._channel_manager = runtime.channel_manager
        self._state_store = replacement_session.store
        self._poller = replacement_session.poller
        self._device_session = replacement_session.writer
        self._device_streams = replacement_session.streams
        self._provider_manager = replacement_provider
        self._owns_runtime = False
        self.host = host
        self.port = port
        self.security_mode = security_mode
        self.tls_ca_certificate = tls_ca_certificate
        self.auth_token = auth_token
        runtime.mark_available()
        self._publish_state(replacement_session.store.state)

        # The swap above is the commit point. Cleanup failures must never make
        # the caller release the now-active replacement runtime.
        cancellation: asyncio.CancelledError | None = None
        try:
            await previous_provider.async_stop(invalidate=False)
        except asyncio.CancelledError as err:
            cancellation = err
        except Exception:  # noqa: BLE001
            _LOGGER.exception("Failed to stop previous EEBUS provider manager")
        try:
            await previous_runtime.release_device_session(previous_session)
        except asyncio.CancelledError as err:
            cancellation = cancellation or err
        except Exception:  # noqa: BLE001
            _LOGGER.exception("Failed to stop previous EEBUS device session")
        if cancellation is not None:
            raise cancellation

    def _publish_state(self, state: DeviceState) -> None:
        """Publish one already-reduced immutable state atomically."""
        self.async_set_updated_data(state)

    def _publish_session_state(self, generation: object, state: DeviceState) -> None:
        """Ignore staged or superseded session observations outside commit."""
        if generation is self._runtime_generation:
            self._publish_state(state)

    async def _ensure_channel(self) -> grpc.aio.Channel:
        return await self._channel_manager.ensure_channel()

    async def _async_update_data(self) -> DeviceState:
        """Ask the poller to reconcile state without overwriting newer events."""
        try:
            state = await self._poller.poll()
            if self._runtime.mark_available():
                _LOGGER.info("EEBUS bridge connection restored at %s:%s", self.host, self.port)
            return state
        except grpc.aio.AioRpcError as err:
            await self._channel_manager.invalidate()
            self._poller.reset_after_transport_error()
            if err.code() == grpc.StatusCode.UNAUTHENTICATED:
                raise ConfigEntryAuthFailed("Bridge authentication failed") from err
            if self._runtime.mark_unavailable():
                _LOGGER.warning("EEBUS bridge unavailable at %s:%s: %s", self.host, self.port, err)
            raise UpdateFailed(f"gRPC error: {err}") from err

    async def async_initialize(self) -> None:
        """Perform one contract-appropriate initial synchronization."""
        consolidated = self._runtime.supports(
            int(proto_stubs.FeatureId.FEATURE_CONSOLIDATED_DEVICE_STREAM)
        ) is True
        snapshots = self._runtime.supports(
            int(proto_stubs.FeatureId.FEATURE_DEVICE_SNAPSHOT)
        ) is True
        if consolidated and snapshots:
            try:
                await self._poller.ensure_registered()
                self._device_streams.mark_registered()
                self._device_streams.start()
                await self._device_streams.wait_initial_snapshot(RPC_TIMEOUT)
            except grpc.aio.AioRpcError as err:
                if err.code() == grpc.StatusCode.UNAUTHENTICATED:
                    raise ConfigEntryAuthFailed("Bridge authentication failed") from err
                raise ConfigEntryNotReady(f"Initial EEBUS synchronization failed: {err.code().name}") from err
            except TimeoutError as err:
                raise ConfigEntryNotReady("Timed out waiting for the EEBUS initial snapshot") from err
            self._runtime.mark_available()
            return
        await self.async_config_entry_first_refresh()
        self._device_streams.start()

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
        await self._runtime.release_device_session(self._runtime_session)
        if self._owns_runtime:
            await self._runtime.close()

    @property
    def runtime(self) -> BridgeRuntime:
        return self._runtime

    @property
    def reconfigure_lock(self) -> asyncio.Lock:
        if not hasattr(self, "_reconfigure_lock"):
            self._reconfigure_lock = asyncio.Lock()
        return self._reconfigure_lock

    @property
    def entry_unloaded(self) -> bool:
        return bool(getattr(self, "_entry_unloaded", False))

    def mark_entry_unloaded(self) -> None:
        self._entry_unloaded = True
