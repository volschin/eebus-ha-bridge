"""Background gRPC stream consumption with reconnect/backoff for the EEBUS integration."""

from __future__ import annotations

import asyncio
import logging
import random
from collections.abc import Awaitable, Callable

import grpc
import grpc.aio
from homeassistant.core import HomeAssistant

from .grpc_client import GrpcChannelManager, is_unimplemented, rpc_error_text

_LOGGER = logging.getLogger(__name__)

STREAM_BACKOFF_BASE_SECONDS = 2.0
STREAM_BACKOFF_MAX_SECONDS = 60.0
STREAM_BACKOFF_JITTER_SECONDS = 3.0

ConsumeFn = Callable[[grpc.aio.Channel], Awaitable[None]]
UnsupportedFn = Callable[[str], None]


class StreamManager:
    """Own the coordinator's long-lived event-stream task lifecycle."""

    def __init__(
        self,
        hass: HomeAssistant,
        channel_manager: GrpcChannelManager,
        sleep: Callable[[float], Awaitable[None]] = asyncio.sleep,
        jitter: Callable[[float, float], float] = random.uniform,
    ) -> None:
        """Initialize stream lifecycle dependencies."""
        self._hass = hass
        self._channel_manager = channel_manager
        self._sleep = sleep
        self._jitter = jitter
        self._tasks: list[asyncio.Task[None]] = []

    def start(
        self,
        streams: dict[str, ConsumeFn],
        task_name_prefix: str,
        on_unimplemented: UnsupportedFn | None = None,
    ) -> None:
        """Launch one background task per named stream, unless already started."""
        if self._tasks:
            return
        for name, consume in streams.items():
            self._tasks.append(
                self._hass.async_create_background_task(
                    self._run_stream(name, consume, on_unimplemented),
                    name=task_name_prefix.format(name, name=name),
                )
            )

    async def stop(self) -> None:
        """Cancel every stream task and await completion before returning."""
        for task in self._tasks:
            task.cancel()
        await asyncio.gather(*self._tasks, return_exceptions=True)
        self._tasks.clear()

    def diagnostics(self) -> dict[str, int]:
        """Return non-secret stream lifecycle counters for diagnostics."""
        return {
            "configured": len(self._tasks),
            "running": sum(1 for task in self._tasks if not task.done()),
            "done": sum(1 for task in self._tasks if task.done()),
        }

    async def _run_stream(
        self,
        name: str,
        consume: ConsumeFn,
        on_unimplemented: UnsupportedFn | None = None,
    ) -> None:
        """Consume one stream with reconnect/backoff until cancelled."""
        attempt = 0
        while True:
            try:
                channel = await self._channel_manager.ensure_channel()
                await consume(channel)
                attempt = 0
            except asyncio.CancelledError:
                raise
            except grpc.aio.AioRpcError as err:
                if is_unimplemented(err):
                    _LOGGER.info(
                        "EEBUS %s stream not supported by bridge; relying on polling",
                        name,
                    )
                    if on_unimplemented is not None:
                        on_unimplemented(name)
                    return
                attempt += 1
                _LOGGER.debug(
                    "EEBUS %s stream ended (%s); scheduling retry",
                    name,
                    rpc_error_text(err),
                )
            except Exception:  # noqa: BLE001
                attempt += 1
                _LOGGER.exception("EEBUS %s stream failed; scheduling retry", name)

            delay = min(
                STREAM_BACKOFF_BASE_SECONDS * (2 ** max(0, attempt - 1)),
                STREAM_BACKOFF_MAX_SECONDS,
            ) + self._jitter(0, STREAM_BACKOFF_JITTER_SECONDS)
            _LOGGER.debug("Retrying EEBUS %s stream in %.1fs", name, delay)
            await self._sleep(delay)
