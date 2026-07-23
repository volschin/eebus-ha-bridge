"""Home Assistant sensor-backed EEBUS provider publishers."""

from __future__ import annotations

import asyncio
import logging
import math
from collections.abc import Awaitable, Callable
from contextlib import suppress
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from typing import Protocol

import grpc
import grpc.aio
from homeassistant.const import (
    ATTR_UNIT_OF_MEASUREMENT,
    PERCENTAGE,
    STATE_UNAVAILABLE,
    STATE_UNKNOWN,
    UnitOfEnergy,
    UnitOfPower,
)
from homeassistant.core import Event, HomeAssistant
from homeassistant.helpers.event import (
    EventStateChangedData,
    async_track_state_change_event,
)

from . import proto_stubs
from .grpc_client import (
    RPC_TIMEOUT,
)
from .grpc_client import (
    is_unimplemented as _is_unimplemented,
)
from .grpc_client import (
    rpc_error_text as _rpc_error_text,
)

_LOGGER = logging.getLogger(__name__)

# Convert a Home Assistant sensor's value to the unit the providers expect
# (power in W, energy in Wh) using its unit_of_measurement attribute.
POWER_UNIT_TO_W: dict[str, float] = {
    UnitOfPower.WATT: 1.0,
    UnitOfPower.KILO_WATT: 1000.0,
    UnitOfPower.MEGA_WATT: 1_000_000.0,
}
ENERGY_UNIT_TO_WH: dict[str, float] = {
    UnitOfEnergy.WATT_HOUR: 1.0,
    UnitOfEnergy.KILO_WATT_HOUR: 1000.0,
    UnitOfEnergy.MEGA_WATT_HOUR: 1_000_000.0,
}
# State of charge is a plain percentage (0-100); no conversion needed.
SOC_UNIT_TO_PCT: dict[str, float] = {PERCENTAGE: 1.0}
PROVIDER_SAMPLE_TTL = timedelta(minutes=2)

ChannelGetter = Callable[[], Awaitable[grpc.aio.Channel]]


@dataclass(frozen=True, slots=True)
class ProviderMappings:
    """Immutable sensor mapping input shared by setup and reconfiguration."""

    grid_power: str | None = None
    grid_feed_in_energy: str | None = None
    grid_consumption_energy: str | None = None
    pv_power: str | None = None
    pv_yield_energy: str | None = None
    pv_peak_power: str | None = None
    battery_power: str | None = None
    battery_charged_energy: str | None = None
    battery_discharged_energy: str | None = None
    battery_soc: str | None = None


class ProviderPublisher(Protocol):
    """Typed lifecycle port implemented by every provider worker."""

    def start(self) -> None: ...

    def signal(self) -> None: ...

    async def invalidate(self) -> None: ...

    async def stop(self) -> None: ...


class _ProviderPusher:
    """Serialize and coalesce state-triggered pushes for one provider."""

    def __init__(
        self,
        hass: HomeAssistant,
        label: str,
        ski: str,
        tracked: tuple[str | None, ...],
        push: Callable[[], Awaitable[None]],
        invalidate: Callable[[], Awaitable[None]],
    ) -> None:
        self._hass = hass
        self._label = label
        self._ski = ski
        self._entity_ids = [entity_id for entity_id in tracked if entity_id]
        self._push = push
        self._invalidate = invalidate
        self._dirty = asyncio.Event()
        self._task: asyncio.Task[None] | None = None
        self._unsub: Callable[[], None] | None = None

    def start(self) -> None:
        """Subscribe to state changes and schedule the initial push."""
        if self._task is not None:
            return

        def _on_change(_event: Event[EventStateChangedData]) -> None:
            self.signal()

        self._unsub = async_track_state_change_event(self._hass, self._entity_ids, _on_change)
        self.signal()
        self._task = self._hass.async_create_background_task(
            self._run(), name=f"eebus_{self._label}_provider_push_{self._ski}"
        )

    def signal(self) -> None:
        """Mark provider data dirty, coalescing repeated signals."""
        self._dirty.set()

    async def stop(self) -> None:
        """Unsubscribe, then cancel and await the worker task."""
        unsub = self._unsub
        self._unsub = None
        task = self._task
        if task is None:
            if unsub is not None:
                unsub()
            return
        try:
            if unsub is not None:
                unsub()
        finally:
            task.cancel()
            with suppress(asyncio.CancelledError):
                await task
            self._task = None

    async def invalidate(self) -> None:
        """Publish this provider's coalesced invalidation."""
        await self._invalidate()

    async def _run(self) -> None:
        """Push the freshest state once per coalesced dirty signal."""
        while True:
            await self._dirty.wait()
            self._dirty.clear()
            try:
                await self._push()
            except asyncio.CancelledError:
                raise
            except Exception:
                _LOGGER.exception("Unexpected failure pushing %s provider data", self._label)


class ProviderManager:
    """Manage sensor-backed provider publishing and worker lifecycles."""

    def __init__(
        self,
        hass: HomeAssistant,
        ski: str,
        channel_getter: ChannelGetter,
        mappings: ProviderMappings,
        *,
        supports_feature: Callable[[int], bool] | None = None,
    ) -> None:
        """Initialize provider configuration and lifecycle state."""
        self._hass = hass
        self._ski = ski
        self._channel_getter = channel_getter
        self._mappings = mappings
        self._grid_power_entity = mappings.grid_power
        self._grid_feed_in_energy_entity = mappings.grid_feed_in_energy
        self._grid_consumption_energy_entity = mappings.grid_consumption_energy
        self._pv_power_entity = mappings.pv_power
        self._pv_yield_energy_entity = mappings.pv_yield_energy
        self._pv_peak_power_entity = mappings.pv_peak_power
        self._battery_power_entity = mappings.battery_power
        self._battery_charged_energy_entity = mappings.battery_charged_energy
        self._battery_discharged_energy_entity = mappings.battery_discharged_energy
        self._battery_soc_entity = mappings.battery_soc
        self._provider_pushers: list[ProviderPublisher] = []
        self._provider_push_failing: dict[str, bool] = {}
        self._provider_invalidated: set[str] = set()
        self._supports_feature = supports_feature or (lambda _feature: False)
        self._stop_task: asyncio.Task[None] | None = None

    @property
    def grid_push_enabled(self) -> bool:
        """Return True when a grid power sensor is mapped to the MGCP provider."""
        return bool(self._grid_power_entity)

    @property
    def pv_push_enabled(self) -> bool:
        """Return True when a PV power sensor is mapped to the VAPD provider."""
        return bool(self._pv_power_entity)

    @property
    def battery_push_enabled(self) -> bool:
        """Return True when a battery power sensor is mapped to the VABD provider."""
        return bool(self._battery_power_entity)

    def _read_sensor_value(
        self,
        entity_id: str | None,
        unit_map: dict[str, float],
        kind: str,
        *,
        minimum: float | None = None,
        maximum: float | None = None,
    ) -> float | None:
        """Read an HA sensor and normalize it via unit_map (W / Wh / %).

        Returns None when the entity is unset, missing, unavailable,
        non-numeric, non-finite (NaN/Inf), or outside ``[minimum, maximum]`` so
        the caller can omit it from the push instead of advertising a bogus
        reading to downstream equipment. ``kind`` is a short descriptor (e.g.
        "grid power", "PV yield") used only for debug logging.
        """
        if not entity_id:
            return None
        state = self._hass.states.get(entity_id)
        if state is None or state.state in (
            STATE_UNKNOWN,
            STATE_UNAVAILABLE,
            "",
            None,
        ):
            return None
        try:
            value = float(state.state)
        except (TypeError, ValueError):
            _LOGGER.debug(
                "%s sensor %s has non-numeric state %r",
                kind,
                entity_id,
                state.state,
            )
            return None
        unit = state.attributes.get(ATTR_UNIT_OF_MEASUREMENT)
        factor = unit_map.get(unit) if isinstance(unit, str) else None
        if factor is None:
            _LOGGER.debug(
                "%s sensor %s has unknown unit %r; assuming base unit",
                kind,
                entity_id,
                unit,
            )
            factor = 1.0
        result = value * factor
        if not math.isfinite(result):
            _LOGGER.debug("%s sensor %s produced non-finite value %r", kind, entity_id, result)
            return None
        if (minimum is not None and result < minimum) or (maximum is not None and result > maximum):
            _LOGGER.debug(
                "%s sensor %s value %r out of range [%s, %s]; omitting",
                kind,
                entity_id,
                result,
                minimum,
                maximum,
            )
            return None
        return result

    async def _async_publish_provider(
        self,
        label: str,
        publish: Callable[[grpc.aio.Channel], Awaitable[object]],
    ) -> bool:
        """Publish a provider reading to the bridge, quiet when the provider is off.

        UNIMPLEMENTED/UNAVAILABLE mean the provider is disabled or the bridge is
        down; skip quietly so a missing provider never spams or fails HA.
        """
        channel = await self._channel_getter()
        try:
            await publish(channel)
            if self._provider_push_failing.pop(label, False):
                _LOGGER.info("%s provider push recovered", label)
            _LOGGER.debug("Pushed %s provider data", label)
            return True
        except grpc.aio.AioRpcError as err:
            if _is_unimplemented(err) or err.code() == grpc.StatusCode.UNAVAILABLE:
                _LOGGER.debug(
                    "%s provider not ready; skipping push: %s",
                    label,
                    _rpc_error_text(err),
                )
                return False
            if self._provider_push_failing.get(label, False):
                _LOGGER.debug("Failed to push %s data: %s", label, _rpc_error_text(err))
            else:
                _LOGGER.warning("Failed to push %s data: %s", label, _rpc_error_text(err))
                self._provider_push_failing[label] = True
            return False

    def _sample_meta(self, *, invalid: bool = False) -> proto_stubs.ProviderSampleMeta:
        """Build provider validity metadata for one complete sample."""
        observed_at = datetime.now(UTC)
        return proto_stubs.ProviderSampleMeta(
            observed_at=observed_at,
            valid_until=observed_at + PROVIDER_SAMPLE_TTL,
            invalid=invalid,
        )

    async def _async_provider_invalidation_supported(self) -> bool:
        """Return whether the bridge understands sample.invalid provider pushes."""
        return self._supports_feature(
            int(proto_stubs.FeatureId.FEATURE_PROVIDER_SAMPLE_INVALIDATION)
        )

    async def _async_publish_grid(
        self, label: str, request: proto_stubs.GridData
    ) -> bool:
        async def publish(channel: grpc.aio.Channel) -> None:
            await proto_stubs.grid_service_stub(channel).PublishGridData(
                request, timeout=RPC_TIMEOUT
            )

        return await self._async_publish_provider(label, publish)

    async def _async_publish_pv(
        self, label: str, request: proto_stubs.PVData
    ) -> bool:
        async def publish(channel: grpc.aio.Channel) -> None:
            await proto_stubs.visualization_service_stub(channel).PublishPVData(
                request, timeout=RPC_TIMEOUT
            )

        return await self._async_publish_provider(label, publish)

    async def _async_publish_pv_peak(
        self, label: str, request: proto_stubs.PVPeakPowerData
    ) -> bool:
        async def publish(channel: grpc.aio.Channel) -> None:
            await proto_stubs.visualization_service_stub(
                channel
            ).PublishPVPeakPower(request, timeout=RPC_TIMEOUT)

        return await self._async_publish_provider(label, publish)

    async def _async_publish_battery(
        self, label: str, request: proto_stubs.BatteryData
    ) -> bool:
        async def publish(channel: grpc.aio.Channel) -> None:
            await proto_stubs.visualization_service_stub(channel).PublishBatteryData(
                request, timeout=RPC_TIMEOUT
            )

        return await self._async_publish_provider(label, publish)

    def _start_provider_push(
        self,
        label: str,
        tracked: tuple[str | None, ...],
        push: Callable[[], Awaitable[None]],
        invalidate: Callable[[], Awaitable[None]],
    ) -> None:
        """Start one lifecycle-owning provider push worker."""
        if self._stop_task is not None:
            raise RuntimeError("provider manager is stopping")
        pusher = _ProviderPusher(
            self._hass,
            label,
            self._ski,
            tracked,
            push,
            invalidate,
        )
        self._provider_pushers.append(pusher)
        pusher.start()

    async def async_push_grid_data(self) -> None:
        """Push the mapped grid sensors to the bridge MGCP provider.

        Grid power is the surplus signal (negative = export); the energy totals
        are optional. No-op when no grid power sensor is mapped or its value is
        currently unavailable.
        """
        if not self._grid_power_entity:
            return
        power_w = self._read_sensor_value(self._grid_power_entity, POWER_UNIT_TO_W, "grid power")
        if power_w is None:
            await self._async_invalidate_grid_data()
            return
        feed_in_wh = self._read_sensor_value(
            self._grid_feed_in_energy_entity,
            ENERGY_UNIT_TO_WH,
            "grid feed-in",
            minimum=0,
        )
        consumed_wh = self._read_sensor_value(
            self._grid_consumption_energy_entity,
            ENERGY_UNIT_TO_WH,
            "grid consumption",
            minimum=0,
        )

        request = proto_stubs.GridData(power_w=power_w, sample=self._sample_meta())
        if feed_in_wh is not None:
            request.feed_in_wh = feed_in_wh
        if consumed_wh is not None:
            request.consumed_wh = consumed_wh
        if await self._async_publish_grid("grid", request):
            self._provider_invalidated.discard("grid")

    async def async_push_pv_data(self) -> None:
        """Push the mapped PV sensors to the bridge VAPD (display) provider.

        PV power is required; yield energy and nominal peak power are optional.
        No-op when no PV power sensor is mapped or its value is unavailable.
        """
        if not self._pv_power_entity:
            return
        power_w = self._read_sensor_value(self._pv_power_entity, POWER_UNIT_TO_W, "PV power", minimum=0)
        if power_w is None:
            await self._async_invalidate_pv_data()
            return
        yield_wh = self._read_sensor_value(
            self._pv_yield_energy_entity,
            ENERGY_UNIT_TO_WH,
            "PV yield",
            minimum=0,
        )
        peak_power_w = self._read_sensor_value(
            self._pv_peak_power_entity,
            POWER_UNIT_TO_W,
            "PV peak power",
            minimum=0,
        )

        request = proto_stubs.PVData(power_w=power_w, sample=self._sample_meta())
        if yield_wh is not None:
            request.yield_wh = yield_wh
        if await self._async_publish_pv("PV", request):
            self._provider_invalidated.discard("PV")
        if peak_power_w is not None:
            await self._async_publish_pv_peak(
                "PV peak",
                proto_stubs.PVPeakPowerData(peak_power_w=peak_power_w),
            )

    async def async_push_battery_data(self) -> None:
        """Push the mapped battery sensors to the bridge VABD (display) provider.

        Battery power is required; charged/discharged energy and state of charge
        are optional. No-op when no battery power sensor is mapped or its value is
        unavailable.
        """
        if not self._battery_power_entity:
            return
        power_w = self._read_sensor_value(self._battery_power_entity, POWER_UNIT_TO_W, "battery power")
        if power_w is None:
            await self._async_invalidate_battery_data()
            return
        charged_wh = self._read_sensor_value(
            self._battery_charged_energy_entity,
            ENERGY_UNIT_TO_WH,
            "battery charged",
            minimum=0,
        )
        discharged_wh = self._read_sensor_value(
            self._battery_discharged_energy_entity,
            ENERGY_UNIT_TO_WH,
            "battery discharged",
            minimum=0,
        )
        soc_pct = self._read_sensor_value(
            self._battery_soc_entity,
            SOC_UNIT_TO_PCT,
            "battery SoC",
            minimum=0,
            maximum=100,
        )

        request = proto_stubs.BatteryData(power_w=power_w, sample=self._sample_meta())
        if charged_wh is not None:
            request.charged_wh = charged_wh
        if discharged_wh is not None:
            request.discharged_wh = discharged_wh
        if soc_pct is not None:
            request.state_of_charge_pct = soc_pct
        if await self._async_publish_battery("battery", request):
            self._provider_invalidated.discard("battery")

    async def _async_invalidate_grid_data(self) -> None:
        if not await self._async_provider_invalidation_supported():
            return
        if "grid" in self._provider_invalidated:
            return
        if await self._async_publish_grid(
            "grid",
            proto_stubs.GridData(sample=self._sample_meta(invalid=True)),
        ):
            self._provider_invalidated.add("grid")

    async def _async_invalidate_pv_data(self) -> None:
        if not await self._async_provider_invalidation_supported():
            return
        if "PV" in self._provider_invalidated:
            return
        if await self._async_publish_pv(
            "PV",
            proto_stubs.PVData(sample=self._sample_meta(invalid=True)),
        ):
            self._provider_invalidated.add("PV")

    async def _async_invalidate_battery_data(self) -> None:
        if not await self._async_provider_invalidation_supported():
            return
        if "battery" in self._provider_invalidated:
            return
        if await self._async_publish_battery(
            "battery",
            proto_stubs.BatteryData(sample=self._sample_meta(invalid=True)),
        ):
            self._provider_invalidated.add("battery")

    def async_start_grid_push(self) -> None:
        """Track mapped grid sensors and push their values to the bridge."""
        if not self.grid_push_enabled:
            return
        self._start_provider_push(
            "grid",
            (
                self._grid_power_entity,
                self._grid_feed_in_energy_entity,
                self._grid_consumption_energy_entity,
            ),
            self.async_push_grid_data,
            self._async_invalidate_grid_data,
        )

    def async_start_pv_push(self) -> None:
        """Track mapped PV sensors and push their values to the bridge."""
        if not self.pv_push_enabled:
            return
        self._start_provider_push(
            "pv",
            (
                self._pv_power_entity,
                self._pv_yield_energy_entity,
                self._pv_peak_power_entity,
            ),
            self.async_push_pv_data,
            self._async_invalidate_pv_data,
        )

    def async_start_battery_push(self) -> None:
        """Track mapped battery sensors and push their values to the bridge."""
        if not self.battery_push_enabled:
            return
        self._start_provider_push(
            "battery",
            (
                self._battery_power_entity,
                self._battery_charged_energy_entity,
                self._battery_discharged_energy_entity,
                self._battery_soc_entity,
            ),
            self.async_push_battery_data,
            self._async_invalidate_battery_data,
        )

    async def async_stop(self, *, invalidate: bool = True) -> None:
        """Stop all workers and finish cleanup even if the caller is cancelled."""
        if self._stop_task is None:
            self._stop_task = asyncio.create_task(self._async_stop(invalidate=invalidate))
        try:
            await asyncio.shield(self._stop_task)
        except asyncio.CancelledError:
            # Integration teardown may cancel its caller. The owned workers and
            # invalidation RPCs still have to finish before cancellation escapes.
            try:
                await asyncio.shield(self._stop_task)
            finally:
                raise

    async def _async_stop(self, *, invalidate: bool) -> None:
        """Run the provider cleanup exactly once."""
        pushers = tuple(self._provider_pushers)
        self._provider_pushers.clear()
        stop_results = await asyncio.gather(
            *(pusher.stop() for pusher in pushers), return_exceptions=True
        )
        for result in stop_results:
            if isinstance(result, BaseException):
                _LOGGER.warning("Failed to stop an EEBUS provider worker: %s", result)
        if not invalidate:
            return
        invalidations = [
            invalidate_provider()
            for enabled, invalidate_provider in (
                (self.grid_push_enabled, self._async_invalidate_grid_data),
                (self.pv_push_enabled, self._async_invalidate_pv_data),
                (self.battery_push_enabled, self._async_invalidate_battery_data),
            )
            if enabled
        ]
        await asyncio.gather(*invalidations, return_exceptions=True)
