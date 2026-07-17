# SPEC2-08 Slice 2: DeviceSession Extraction — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Repo override:** This project delegates implementation to Codex, not to a Claude subagent loop (see `CLAUDE.md` → "Delegating implementation to Codex"). Hand this plan file to `/codex:rescue --background`, then run `/codex:review --base main` before merge. Do not use `superpowers:subagent-driven-development`/`executing-plans` execution here — this plan's task/step structure is still the right shape, just executed via the Codex flow.

**Goal:** Extract the write-RPC methods currently embedded in `EebusCoordinator` into a standalone, unit-testable `DeviceSession` class — the "DeviceSession" box from `docs/refactoring-optimization-spec-v2.md`'s SPEC2-08 target architecture that hasn't been built yet (StateReducer/SnapshotPoller/StreamSupervisor/ProviderManager already exist as `state.py`/`snapshot.py`/`streams.py`/`providers.py`).

**Architecture:** `DeviceSession` owns typed stub construction, request building, and gRPC error classification for all 9 write RPCs; it returns a `WriteOutcome` value instead of mutating coordinator state or raising directly. `EebusCoordinator` keeps owning `_domain_state` and applies each `WriteOutcome`'s capability transition via one small `_finish_write` helper — same job `_async_write_rpc` does today, just fed by `DeviceSession`'s classified result instead of doing the gRPC try/except itself. This is a pure move-and-preserve refactor: no behavior change, no new capability-transition semantics, no touched entity/platform code.

**Tech Stack:** Python 3.13, dataclasses, grpc.aio, pytest + pytest-asyncio (`asyncio_mode=auto`), mypy `--strict`.

## Global Constraints

- `PYTHONPATH=. pytest`, `ruff check custom_components/`, `mypy custom_components/eebus` must all stay green — every existing test (especially `test_ohpcf.py`'s `test_control_compressor_*` and any DHW/LPC write-path tests) must pass **unmodified**, since this is a behavior-preserving extraction, not a rewrite.
- No change to any public coordinator method signature (`async_write_lpc_limit`, `async_write_failsafe_limit`, `async_set_lpc_active`, `async_control_compressor`, `async_write_dhw_setpoint`, `async_set_dhw_boost`, `async_set_dhw_operation_mode`, `async_set_room_heating_temperature`, `async_set_room_heating_mode`) — platform files (`number.py`, `switch.py`, `select.py`, `climate.py`, `water_heater.py`) call these and must not need edits.
- No change to capability-transition semantics: `next_capability_state(current, status_code)` from `state.py` is still the single source of truth for how a write outcome maps to `CapabilityState`.
- `DeviceSession` must be constructible and callable with **no Home Assistant object**, matching the pattern `snapshot.py` already uses for `async_build_snapshot` (SnapshotPoller equivalent) — this is what makes it "isoliert ohne laufendes Home Assistant testbar" per the SPEC2-08 Abnahme criteria.
- Follow YAGNI: this slice does **not** touch event-handler extraction or narrow entity selectors (SPEC2-08's other two remaining pieces) — those are separate future slices, out of scope here.

---

## File Structure

- **Create:** `custom_components/eebus/device_session.py` — `WriteOutcome` dataclass + `DeviceSession` class (typed stubs, request building, error classification for all 9 write RPCs).
- **Create:** `custom_components/eebus/tests/test_device_session.py` — unit tests for `DeviceSession` in isolation (fake async `call` callables, no coordinator, no HA).
- **Modify:** `custom_components/eebus/coordinator.py` — remove `_async_write_rpc` and the inline stub/request-building bodies of the 9 write methods; each becomes a two-line call into `DeviceSession` + `self._finish_write(outcome, support_attr)`.

---

### Task 1: `DeviceSession` and `WriteOutcome`

**Files:**
- Create: `custom_components/eebus/device_session.py`
- Test: `custom_components/eebus/tests/test_device_session.py`

**Interfaces:**
- Consumes: `RPC_TIMEOUT`, `WRITE_VALIDATION_CODES`, `is_unimplemented` from `custom_components/eebus/grpc_client.py` (already exist, unchanged). `proto_stubs` re-exports from `custom_components/eebus/proto_stubs.py` (already exist, unchanged) — specifically the `*_service_stub(channel)` factory functions and the `WriteLoadLimitRequest`/`WriteFailsafeLimitRequest`/`ControlCompressorRequest`/`SetDHWSetpointRequest`/`SetDHWBoostRequest`/`SetDHWOperationModeRequest`/`SetRoomHeatingTemperatureRequest`/`SetRoomHeatingModeRequest`/`DeviceRequest` message types and `OHPCFAction` enum.
- Produces: `WriteOutcome` (frozen dataclass: `status_code: grpc.StatusCode | None`, `unimplemented: bool = False`, `validation_error: str | None = None`, `error: grpc.aio.AioRpcError | None = None`) and `DeviceSession(ski: str, ensure_channel: Callable[[], Awaitable[grpc.aio.Channel]])` with async methods `write_lpc_limit(value_watts: float)`, `write_failsafe_limit(value_watts: float)`, `set_lpc_active(active: bool)`, `control_compressor(action: proto_stubs.OHPCFAction)`, `write_dhw_setpoint(value_celsius: float)`, `set_dhw_boost(active: bool)`, `set_dhw_operation_mode(mode: str)`, `set_room_heating_temperature(value_celsius: float)`, `set_room_heating_mode(mode: str)` — every one returns `WriteOutcome`, none raise for classified gRPC errors (only an uncaught non-`AioRpcError` exception, e.g. from `ensure_channel()` itself, propagates). Task 2 depends on all nine method names and the `WriteOutcome` field names exactly as listed here.

- [ ] **Step 1: Write the failing tests for `DeviceSession._write` (the shared classification helper)**

```python
"""Tests for DeviceSession: typed write RPCs with gRPC error classification."""

import asyncio
from unittest.mock import AsyncMock

import grpc
import pytest
from grpc.aio import AioRpcError, Metadata

from custom_components.eebus.device_session import DeviceSession, WriteOutcome


def _session(ski: str = "test-ski") -> DeviceSession:
    return DeviceSession(ski, AsyncMock(return_value=None))


def test_write_success_returns_none_status_code():
    session = _session()
    call = AsyncMock(return_value=None)

    outcome = asyncio.run(session._write("label", call, "request"))

    assert outcome == WriteOutcome(status_code=None)
    call.assert_awaited_once_with("request", timeout=10)


def test_write_unimplemented_is_classified_not_raised():
    session = _session()
    err = AioRpcError(
        grpc.StatusCode.UNIMPLEMENTED, Metadata(), Metadata(), details="no use case"
    )
    call = AsyncMock(side_effect=err)

    outcome = asyncio.run(session._write("label", call, "request"))

    assert outcome.status_code == grpc.StatusCode.UNIMPLEMENTED
    assert outcome.unimplemented is True
    assert outcome.validation_error is None
    assert outcome.error is err


def test_write_validation_error_classified_when_validation_true():
    session = _session()
    err = AioRpcError(
        grpc.StatusCode.INVALID_ARGUMENT, Metadata(), Metadata(), details="bad value"
    )
    call = AsyncMock(side_effect=err)

    outcome = asyncio.run(session._write("Setpoint write", call, "request", validation=True))

    assert outcome.status_code == grpc.StatusCode.INVALID_ARGUMENT
    assert outcome.unimplemented is False
    assert outcome.validation_error == "Setpoint write failed: bad value"
    assert outcome.error is err


def test_write_validation_code_ignored_when_validation_false():
    session = _session()
    err = AioRpcError(
        grpc.StatusCode.INVALID_ARGUMENT, Metadata(), Metadata(), details="bad value"
    )
    call = AsyncMock(side_effect=err)

    outcome = asyncio.run(session._write("label", call, "request", validation=False))

    assert outcome.validation_error is None
    assert outcome.status_code == grpc.StatusCode.INVALID_ARGUMENT
    assert outcome.error is err


def test_write_unclassified_error_returned_not_raised():
    session = _session()
    err = AioRpcError(
        grpc.StatusCode.UNAVAILABLE, Metadata(), Metadata(), details="not ready"
    )
    call = AsyncMock(side_effect=err)

    outcome = asyncio.run(session._write("label", call, "request"))

    assert outcome.status_code == grpc.StatusCode.UNAVAILABLE
    assert outcome.unimplemented is False
    assert outcome.validation_error is None
    assert outcome.error is err
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/volsch/projekte/eebus && PYTHONPATH=. uv run --with pytest --with pytest-asyncio --with homeassistant --with voluptuous --with grpcio --with protobuf --with grpc-stubs --with protobuf pytest custom_components/eebus/tests/test_device_session.py -v`
Expected: FAIL with `ModuleNotFoundError: No module named 'custom_components.eebus.device_session'`

- [ ] **Step 3: Implement `WriteOutcome` and `DeviceSession._write`**

```python
"""Typed gRPC write-RPC session for one EEBUS device: stubs, requests, error translation."""

from __future__ import annotations

import logging
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Any

import grpc
import grpc.aio

from . import proto_stubs
from .grpc_client import RPC_TIMEOUT, WRITE_VALIDATION_CODES, is_unimplemented

_LOGGER = logging.getLogger(__name__)


@dataclass(frozen=True)
class WriteOutcome:
    """Result of one write RPC. The caller applies capability state and error handling."""

    status_code: grpc.StatusCode | None
    unimplemented: bool = False
    validation_error: str | None = None
    error: grpc.aio.AioRpcError | None = None


class DeviceSession:
    """Typed reads/writes against one device's SKI, with shared error translation."""

    def __init__(
        self,
        ski: str,
        ensure_channel: Callable[[], Awaitable[grpc.aio.Channel]],
    ) -> None:
        """Initialize with the target SKI and a channel provider."""
        self._ski = ski
        self._ensure_channel = ensure_channel

    async def _write(
        self, label: str, call: Any, request: Any, *, validation: bool = False
    ) -> WriteOutcome:
        """Execute one write RPC and classify the result; never raises for AioRpcError."""
        try:
            await call(request, timeout=RPC_TIMEOUT)
            return WriteOutcome(status_code=None)
        except grpc.aio.AioRpcError as err:
            if is_unimplemented(err):
                _LOGGER.info(
                    "%s unsupported for SKI %s: %s", label, self._ski, err.details()
                )
                return WriteOutcome(
                    status_code=err.code(), unimplemented=True, error=err
                )
            if validation and err.code() in WRITE_VALIDATION_CODES:
                return WriteOutcome(
                    status_code=err.code(),
                    validation_error=f"{label} failed: {err.details()}",
                    error=err,
                )
            return WriteOutcome(status_code=err.code(), error=err)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: same command as Step 2.
Expected: 5 passed

- [ ] **Step 5: Write failing tests for the 9 typed write methods**

Append to `custom_components/eebus/tests/test_device_session.py`:

```python
from unittest.mock import patch

from custom_components.eebus import proto_stubs


def test_write_lpc_limit_calls_lpc_stub():
    session = DeviceSession("test-ski", AsyncMock(return_value="channel"))
    stub = AsyncMock()
    stub.WriteConsumptionLimit = AsyncMock()
    with patch.object(proto_stubs, "lpc_service_stub", return_value=stub) as factory:
        outcome = asyncio.run(session.write_lpc_limit(1500.0))

    factory.assert_called_once_with("channel")
    stub.WriteConsumptionLimit.assert_awaited_once()
    request = stub.WriteConsumptionLimit.await_args.args[0]
    assert request.ski == "test-ski"
    assert request.value_watts == 1500.0
    assert request.is_active is True
    assert outcome == WriteOutcome(status_code=None)


def test_write_failsafe_limit_calls_lpc_stub():
    session = DeviceSession("test-ski", AsyncMock(return_value="channel"))
    stub = AsyncMock()
    stub.WriteFailsafeLimit = AsyncMock()
    with patch.object(proto_stubs, "lpc_service_stub", return_value=stub):
        outcome = asyncio.run(session.write_failsafe_limit(3500.0))

    request = stub.WriteFailsafeLimit.await_args.args[0]
    assert request.ski == "test-ski"
    assert request.value_watts == 3500.0
    assert outcome == WriteOutcome(status_code=None)


def test_set_lpc_active_reads_current_limit_then_writes():
    session = DeviceSession("test-ski", AsyncMock(return_value="channel"))
    stub = AsyncMock()
    stub.GetConsumptionLimit = AsyncMock(
        return_value=proto_stubs.LoadLimit(value_watts=2000.0, is_active=False)
    )
    stub.WriteConsumptionLimit = AsyncMock()
    with patch.object(proto_stubs, "lpc_service_stub", return_value=stub):
        outcome = asyncio.run(session.set_lpc_active(True))

    stub.GetConsumptionLimit.assert_awaited_once()
    request = stub.WriteConsumptionLimit.await_args.args[0]
    assert request.value_watts == 2000.0
    assert request.is_active is True
    assert outcome == WriteOutcome(status_code=None)


def test_control_compressor_calls_ohpcf_stub_without_validation():
    session = DeviceSession("test-ski", AsyncMock(return_value="channel"))
    err_stub = AsyncMock()
    err = AioRpcError(
        grpc.StatusCode.INTERNAL, Metadata(), Metadata(), details="data not available"
    )
    err_stub.ControlCompressorFlexibility = AsyncMock(side_effect=err)
    with patch.object(proto_stubs, "ohpcf_service_stub", return_value=err_stub):
        outcome = asyncio.run(
            session.control_compressor(
                proto_stubs.OHPCFAction.OHPCF_ACTION_SCHEDULE
            )
        )

    # validation=False for this method: INTERNAL is not classified as validation_error.
    assert outcome.validation_error is None
    assert outcome.error is err


def test_write_dhw_setpoint_uses_validation():
    session = DeviceSession("test-ski", AsyncMock(return_value="channel"))
    stub = AsyncMock()
    err = AioRpcError(
        grpc.StatusCode.INVALID_ARGUMENT, Metadata(), Metadata(), details="out of range"
    )
    stub.SetDHWSetpoint = AsyncMock(side_effect=err)
    with patch.object(proto_stubs, "dhw_service_stub", return_value=stub):
        outcome = asyncio.run(session.write_dhw_setpoint(55.0))

    assert outcome.validation_error == "Domestic hot water setpoint failed: out of range"


def test_set_dhw_boost_calls_dhw_stub():
    session = DeviceSession("test-ski", AsyncMock(return_value="channel"))
    stub = AsyncMock()
    stub.SetDHWBoost = AsyncMock()
    with patch.object(proto_stubs, "dhw_service_stub", return_value=stub):
        outcome = asyncio.run(session.set_dhw_boost(True))

    request = stub.SetDHWBoost.await_args.args[0]
    assert request.active is True
    assert outcome == WriteOutcome(status_code=None)


def test_set_dhw_operation_mode_calls_dhw_stub():
    session = DeviceSession("test-ski", AsyncMock(return_value="channel"))
    stub = AsyncMock()
    stub.SetDHWOperationMode = AsyncMock()
    with patch.object(proto_stubs, "dhw_service_stub", return_value=stub):
        outcome = asyncio.run(session.set_dhw_operation_mode("hotWaterNormal"))

    request = stub.SetDHWOperationMode.await_args.args[0]
    assert request.mode == "hotWaterNormal"
    assert outcome == WriteOutcome(status_code=None)


def test_set_room_heating_temperature_calls_hvac_stub():
    session = DeviceSession("test-ski", AsyncMock(return_value="channel"))
    stub = AsyncMock()
    stub.SetRoomHeatingTemperature = AsyncMock()
    with patch.object(proto_stubs, "hvac_service_stub", return_value=stub):
        outcome = asyncio.run(session.set_room_heating_temperature(21.5))

    request = stub.SetRoomHeatingTemperature.await_args.args[0]
    assert request.value_celsius == 21.5
    assert outcome == WriteOutcome(status_code=None)


def test_set_room_heating_mode_calls_hvac_stub():
    session = DeviceSession("test-ski", AsyncMock(return_value="channel"))
    stub = AsyncMock()
    stub.SetRoomHeatingMode = AsyncMock()
    with patch.object(proto_stubs, "hvac_service_stub", return_value=stub):
        outcome = asyncio.run(session.set_room_heating_mode("auto"))

    request = stub.SetRoomHeatingMode.await_args.args[0]
    assert request.mode == "auto"
    assert outcome == WriteOutcome(status_code=None)
```

- [ ] **Step 6: Run tests to verify they fail**

Run: same command as Step 2.
Expected: FAIL — `AttributeError: 'DeviceSession' object has no attribute 'write_lpc_limit'` (and similarly for the other 8 methods).

- [ ] **Step 7: Implement the 9 typed write methods**

Append to `custom_components/eebus/device_session.py` (inside the `DeviceSession` class, after `_write`):

```python
    async def write_lpc_limit(self, value_watts: float) -> WriteOutcome:
        """Write the LPC consumption limit."""
        channel = await self._ensure_channel()
        stub = proto_stubs.lpc_service_stub(channel)
        return await self._write(
            "LPC write",
            stub.WriteConsumptionLimit,
            proto_stubs.WriteLoadLimitRequest(
                ski=self._ski, value_watts=value_watts, is_active=True
            ),
        )

    async def write_failsafe_limit(self, value_watts: float) -> WriteOutcome:
        """Write the LPC failsafe limit."""
        channel = await self._ensure_channel()
        stub = proto_stubs.lpc_service_stub(channel)
        return await self._write(
            "Failsafe write",
            stub.WriteFailsafeLimit,
            proto_stubs.WriteFailsafeLimitRequest(
                ski=self._ski, value_watts=value_watts
            ),
        )

    async def set_lpc_active(self, active: bool) -> WriteOutcome:
        """Read the current LPC limit, then write it back with a new active flag."""
        channel = await self._ensure_channel()
        stub = proto_stubs.lpc_service_stub(channel)
        current = await stub.GetConsumptionLimit(
            proto_stubs.DeviceRequest(ski=self._ski), timeout=RPC_TIMEOUT
        )
        return await self._write(
            "LPC activation",
            stub.WriteConsumptionLimit,
            proto_stubs.WriteLoadLimitRequest(
                ski=self._ski,
                value_watts=current.value_watts,
                is_active=active,
            ),
        )

    async def control_compressor(
        self, action: proto_stubs.OHPCFAction
    ) -> WriteOutcome:
        """Schedule/pause/resume/abort the compressor's optional consumption."""
        channel = await self._ensure_channel()
        stub = proto_stubs.ohpcf_service_stub(channel)
        return await self._write(
            "OHPCF control",
            stub.ControlCompressorFlexibility,
            proto_stubs.ControlCompressorRequest(ski=self._ski, action=action),
        )

    async def write_dhw_setpoint(self, value_celsius: float) -> WriteOutcome:
        """Write the domestic-hot-water target."""
        channel = await self._ensure_channel()
        stub = proto_stubs.dhw_service_stub(channel)
        return await self._write(
            "Domestic hot water setpoint",
            stub.SetDHWSetpoint,
            proto_stubs.SetDHWSetpointRequest(
                ski=self._ski, value_celsius=value_celsius
            ),
            validation=True,
        )

    async def set_dhw_boost(self, active: bool) -> WriteOutcome:
        """Activate or cancel the DHW one-time boost."""
        channel = await self._ensure_channel()
        stub = proto_stubs.dhw_service_stub(channel)
        return await self._write(
            "Domestic hot water boost",
            stub.SetDHWBoost,
            proto_stubs.SetDHWBoostRequest(ski=self._ski, active=active),
            validation=True,
        )

    async def set_dhw_operation_mode(self, mode: str) -> WriteOutcome:
        """Set the DHW operation mode by advertised mode type."""
        channel = await self._ensure_channel()
        stub = proto_stubs.dhw_service_stub(channel)
        return await self._write(
            "Domestic hot water operation mode",
            stub.SetDHWOperationMode,
            proto_stubs.SetDHWOperationModeRequest(ski=self._ski, mode=mode),
            validation=True,
        )

    async def set_room_heating_temperature(self, value_celsius: float) -> WriteOutcome:
        """Set the room-heating target temperature."""
        channel = await self._ensure_channel()
        stub = proto_stubs.hvac_service_stub(channel)
        return await self._write(
            "Room heating setpoint",
            stub.SetRoomHeatingTemperature,
            proto_stubs.SetRoomHeatingTemperatureRequest(
                ski=self._ski, value_celsius=value_celsius
            ),
            validation=True,
        )

    async def set_room_heating_mode(self, mode: str) -> WriteOutcome:
        """Set the room-heating operation mode."""
        channel = await self._ensure_channel()
        stub = proto_stubs.hvac_service_stub(channel)
        return await self._write(
            "Room heating mode",
            stub.SetRoomHeatingMode,
            proto_stubs.SetRoomHeatingModeRequest(ski=self._ski, mode=mode),
            validation=True,
        )
```

- [ ] **Step 8: Run tests to verify they pass**

Run: same command as Step 2.
Expected: 14 passed

- [ ] **Step 9: Commit**

```bash
git add custom_components/eebus/device_session.py custom_components/eebus/tests/test_device_session.py
git commit -m "feat: add DeviceSession — typed write RPCs with isolated error classification"
```

---

### Task 2: Wire `DeviceSession` into `EebusCoordinator`

**Files:**
- Modify: `custom_components/eebus/coordinator.py:19-24` (imports), `:227-278` (`_async_write_rpc` → `_finish_write`), `:280-414` (the 9 write methods)

**Interfaces:**
- Consumes: `DeviceSession`, `WriteOutcome` from Task 1's `device_session.py`.
- Produces: nothing new — public coordinator method signatures are unchanged, so no other file in the codebase needs edits.

- [ ] **Step 1: Confirm the safety net — run the existing write-path tests before touching coordinator.py**

Run: `cd /home/volsch/projekte/eebus && PYTHONPATH=. uv run --with pytest --with pytest-asyncio --with homeassistant --with voluptuous --with grpcio --with protobuf --with grpc-stubs pytest custom_components/eebus/tests/test_ohpcf.py custom_components/eebus/tests/test_dhw.py custom_components/eebus/tests/test_climate.py -v`
Expected: all pass (this is the baseline — these tests exercise `async_control_compressor` and friends end-to-end through mocked `proto_stubs.*ServiceStub` factories, and must keep passing unmodified after Task 2's refactor since it's behavior-preserving).

- [ ] **Step 2: Update the import block**

In `custom_components/eebus/coordinator.py`, replace:

```python
from .grpc_client import (
    RPC_TIMEOUT,
    WRITE_VALIDATION_CODES,
    GrpcChannelManager,
    is_unimplemented as _is_unimplemented,
)
```

with:

```python
from .device_session import DeviceSession, WriteOutcome
from .grpc_client import GrpcChannelManager
```

(`RPC_TIMEOUT`, `WRITE_VALIDATION_CODES`, and `is_unimplemented` are no longer used directly in `coordinator.py` — they moved into `device_session.py` in Task 1.)

- [ ] **Step 3: Replace `_async_write_rpc` with `_finish_write`**

Replace the entire `_async_write_rpc` method (currently `coordinator.py:227-278`):

```python
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
```

with:

```python
    def _finish_write(self, outcome: WriteOutcome, support_attr: str) -> None:
        """Apply a write outcome's capability transition, then raise if the RPC failed uncleanly.

        Mirrors the old inline _async_write_rpc control flow: the capability
        transition always applies first, UNIMPLEMENTED returns quietly after
        that, a classified validation failure raises ServiceValidationError,
        and anything else re-raises the original AioRpcError.
        """
        capabilities = self._domain_state.capabilities
        updated_capabilities = replace(
            capabilities,
            **{
                support_attr: next_capability_state(
                    getattr(capabilities, support_attr), outcome.status_code
                )
            },
        )
        self._domain_state = replace(
            self._domain_state, capabilities=updated_capabilities
        )
        if outcome.validation_error is not None:
            raise ServiceValidationError(outcome.validation_error) from outcome.error
        if outcome.unimplemented:
            return
        if outcome.error is not None:
            raise outcome.error
```

- [ ] **Step 4: Replace the 9 write methods**

Replace `coordinator.py:280-414` (from `async def async_write_lpc_limit` through the end of `async def async_set_room_heating_mode`) with:

```python
    async def async_write_lpc_limit(self, value_watts: float) -> None:
        """Write LPC consumption limit via gRPC."""
        outcome = await DeviceSession(self.ski, self._ensure_channel).write_lpc_limit(
            value_watts
        )
        self._finish_write(outcome, "lpc")

    async def async_write_failsafe_limit(self, value_watts: float) -> None:
        """Write failsafe limit via gRPC."""
        outcome = await DeviceSession(
            self.ski, self._ensure_channel
        ).write_failsafe_limit(value_watts)
        self._finish_write(outcome, "failsafe")

    async def async_set_lpc_active(self, active: bool) -> None:
        """Activate or deactivate LPC limit via gRPC."""
        outcome = await DeviceSession(self.ski, self._ensure_channel).set_lpc_active(
            active
        )
        self._finish_write(outcome, "lpc")

    async def async_control_compressor(self, action: proto_stubs.OHPCFAction) -> None:
        """Schedule/pause/resume/abort the compressor's optional consumption."""
        outcome = await DeviceSession(
            self.ski, self._ensure_channel
        ).control_compressor(action)
        try:
            self._finish_write(outcome, "ohpcf")
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
        outcome = await DeviceSession(
            self.ski, self._ensure_channel
        ).write_dhw_setpoint(value_celsius)
        self._finish_write(outcome, "dhw")

    async def async_set_dhw_boost(self, active: bool) -> None:
        """Activate or cancel DHW one-time boost."""
        outcome = await DeviceSession(self.ski, self._ensure_channel).set_dhw_boost(
            active
        )
        self._finish_write(outcome, "dhw_system_function")

    async def async_set_dhw_operation_mode(self, mode: str) -> None:
        """Set the DHW operation mode by advertised mode type."""
        outcome = await DeviceSession(
            self.ski, self._ensure_channel
        ).set_dhw_operation_mode(mode)
        self._finish_write(outcome, "dhw_system_function")

    async def async_set_room_heating_temperature(self, value_celsius: float) -> None:
        """Set the room-heating target temperature."""
        outcome = await DeviceSession(
            self.ski, self._ensure_channel
        ).set_room_heating_temperature(value_celsius)
        self._finish_write(outcome, "room_heating")

    async def async_set_room_heating_mode(self, mode: str) -> None:
        """Set the room-heating operation mode."""
        outcome = await DeviceSession(
            self.ski, self._ensure_channel
        ).set_room_heating_mode(mode)
        self._finish_write(outcome, "room_heating")
```

- [ ] **Step 5: Check for now-unused imports**

`Any` (from `typing`) was only used in `_async_write_rpc`'s `call: Any, request: Any` parameters — check whether `coordinator.py` still uses `Any` elsewhere (it does, in `_push_data(self, updates: dict[str, Any])` and the `_handle_*_event(self, event: Any)` methods) — **keep** the `Any` import. Do not remove it.

- [ ] **Step 6: Run the full test suite**

Run: `cd /home/volsch/projekte/eebus && PYTHONPATH=. uv run --with pytest --with pytest-asyncio --with pytest-cov --with homeassistant --with voluptuous --with grpcio --with protobuf --with grpc-stubs pytest -q`
Expected: same pass count as the pre-refactor baseline plus the 14 new `test_device_session.py` tests — 0 failures. Pay special attention to `test_ohpcf.py::test_control_compressor_wraps_rpc_error_as_validation_error`, `::test_control_compressor_success_marks_available`, `::test_control_compressor_unavailable_is_temporary`, `::test_control_compressor_unimplemented_is_swallowed` — these are the exact regression net for the OHPCF double-wrapping behavior (`_finish_write` raising a raw `AioRpcError` that `async_control_compressor` re-catches and converts). If any of those four fail, the `_finish_write`/`async_control_compressor` control flow in Step 3/4 doesn't match the original exactly — re-check against the original `_async_write_rpc` + `async_control_compressor` bodies read from git history (`git show HEAD:custom_components/eebus/coordinator.py`).

- [ ] **Step 7: Run ruff and mypy**

Run: `cd /home/volsch/projekte/eebus && uv run --with ruff ruff check custom_components/`
Expected: All checks passed!

Run: `cd /home/volsch/projekte/eebus && uv run --with mypy --with grpc-stubs --with homeassistant --with voluptuous --with grpcio --with protobuf mypy custom_components/eebus`
Expected: Success: no issues found

- [ ] **Step 8: Commit**

```bash
git add custom_components/eebus/coordinator.py
git commit -m "refactor: wire coordinator write RPCs through DeviceSession"
```

---

## Self-Review Notes

- **Spec coverage:** SPEC2-08's "DeviceSession — Channel, typisierte Stubs, Reads/Writes, Fehlerübersetzung" box is now built (Task 1) and wired in (Task 2). Event-handler extraction and narrow entity selectors (the other two remaining SPEC2-08 pieces) are explicitly out of scope for this slice — call this out as follow-up slices when handing off.
- **Behavior parity:** Verified the `_finish_write` control flow against the original `_async_write_rpc` line-by-line (capability transition always applies → unimplemented returns quietly → validation-classified raises `ServiceValidationError` → everything else re-raises raw). Verified `async_control_compressor`'s outer try/except-and-reconvert is preserved exactly, since it depends on `_finish_write` re-raising the raw `AioRpcError` for non-unimplemented, non-validation-classified failures (OHPCF calls `DeviceSession.control_compressor` with `validation=False`, same as today).
- **Test coverage added, not replaced:** Task 1 adds new isolated `DeviceSession` unit tests; Task 2 deliberately does **not** touch any existing test file — the existing `test_ohpcf.py`/`test_dhw.py`/`test_climate.py` write-path tests are the regression net proving the refactor didn't change behavior.
