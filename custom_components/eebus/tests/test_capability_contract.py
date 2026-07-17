"""Tests for the explicit bridge capability contract and legacy boundary."""

from types import SimpleNamespace
from unittest.mock import AsyncMock

import grpc
from grpc.aio import AioRpcError, Metadata

from custom_components.eebus import proto_stubs
from custom_components.eebus.models import CapabilityState
from custom_components.eebus.snapshot import _async_read_capabilities
from custom_components.eebus.state import CapabilityKey


async def test_capability_rpc_is_converted_to_typed_truth() -> None:
    response = proto_stubs.DeviceCapabilities(
        ski="A" * 40,
        capabilities=[
            {
                "id": proto_stubs.CapabilityId.CAPABILITY_DHW,
                "state": proto_stubs.CapabilityState.CAPABILITY_STATE_UNSUPPORTED,
                "reason": proto_stubs.CapabilityReason.CAPABILITY_REASON_REMOTE_NOT_ADVERTISED,
            },
            {
                "id": proto_stubs.CapabilityId.CAPABILITY_LPC,
                "state": proto_stubs.CapabilityState.CAPABILITY_STATE_TEMPORARILY_UNAVAILABLE,
                "reason": proto_stubs.CapabilityReason.CAPABILITY_REASON_READ_FAILED,
            },
        ],
    )
    stub = SimpleNamespace(GetDeviceCapabilities=AsyncMock(return_value=response))

    result = await _async_read_capabilities(stub, proto_stubs.DeviceRequest(ski="A" * 40), "A" * 40)  # type: ignore[arg-type]

    assert result is not None
    assert [(entry.capability, entry.explicit_state, entry.reason) for entry in result] == [
        (CapabilityKey.DHW, CapabilityState.UNSUPPORTED, "remote_not_advertised"),
        (CapabilityKey.LPC, CapabilityState.TEMPORARILY_UNAVAILABLE, "read_failed"),
    ]


async def test_only_unimplemented_enables_legacy_inference() -> None:
    error = AioRpcError(grpc.StatusCode.UNIMPLEMENTED, Metadata(), Metadata(), details="old bridge")
    stub = SimpleNamespace(GetDeviceCapabilities=AsyncMock(side_effect=error))

    result = await _async_read_capabilities(stub, proto_stubs.DeviceRequest(ski="A" * 40), "A" * 40)  # type: ignore[arg-type]

    assert result is None


async def test_transient_capability_rpc_failure_does_not_enable_inference() -> None:
    error = AioRpcError(grpc.StatusCode.UNAVAILABLE, Metadata(), Metadata(), details="temporary")
    stub = SimpleNamespace(GetDeviceCapabilities=AsyncMock(side_effect=error))

    result = await _async_read_capabilities(stub, proto_stubs.DeviceRequest(ski="A" * 40), "A" * 40)  # type: ignore[arg-type]

    assert result == ()
