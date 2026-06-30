"""Tests for the OHPCF (heat-pump compressor flexibility) HA wiring."""

import asyncio
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

import grpc
import pytest
from grpc.aio import AioRpcError, Metadata
from homeassistant.exceptions import HomeAssistantError

from custom_components.eebus import proto_stubs
from custom_components.eebus.coordinator import EebusCoordinator
from custom_components.eebus.generated.eebus.v1 import ohpcf_service_pb2 as ohpcf_pb2
from custom_components.eebus.switch import EebusCompressorFlexibilitySwitch


def _coordinator(ski="test-ski"):
    c = EebusCoordinator.__new__(EebusCoordinator)
    c.ski = ski
    c._ohpcf_supported = None
    return c


def _fake_proto(stub):
    return SimpleNamespace(OHPCFServiceStub=lambda _channel: stub)


def test_read_compressor_flexibility_maps_fields():
    """A populated offer is mapped to the coordinator data dict."""
    c = _coordinator()
    flex = ohpcf_pb2.CompressorFlexibility(
        available=True,
        requested_power_estimate_w=1500.0,
        is_pausable=True,
        is_stoppable=False,
        state=ohpcf_pb2.CompressorPowerConsumptionState.COMPRESSOR_STATE_RUNNING,
        minimal_run_seconds=600,
    )
    stub = SimpleNamespace(GetCompressorFlexibility=AsyncMock(return_value=flex))

    out = asyncio.run(
        c._async_read_compressor_flexibility(None, _fake_proto(stub), proto_stubs.DeviceRequest(ski=c.ski))
    )

    assert c._ohpcf_supported is True
    assert out["available"] is True
    assert out["state"] == "COMPRESSOR_STATE_RUNNING"
    assert out["requested_power_estimate_w"] == 1500.0
    assert out["requested_power_max_w"] is None  # optional, unset
    assert out["is_pausable"] is True
    assert out["minimal_run_seconds"] == 600


def test_read_compressor_flexibility_unavailable_marks_unsupported():
    """UNAVAILABLE (bridge OHPCF client off) yields None and supported=False."""
    c = _coordinator()
    err = AioRpcError(grpc.StatusCode.UNAVAILABLE, Metadata(), Metadata(), details="off")
    stub = SimpleNamespace(GetCompressorFlexibility=AsyncMock(side_effect=err))

    out = asyncio.run(
        c._async_read_compressor_flexibility(None, _fake_proto(stub), proto_stubs.DeviceRequest(ski=c.ski))
    )

    assert out is None
    assert c._ohpcf_supported is False


def _switch_with(flex):
    sw = EebusCompressorFlexibilitySwitch.__new__(EebusCompressorFlexibilitySwitch)
    sw.coordinator = SimpleNamespace(
        data={"compressor_flexibility": flex},
        async_control_compressor=AsyncMock(),
        async_request_refresh=AsyncMock(),
    )
    return sw


def test_switch_is_on_for_running_state():
    """Switch reports on while scheduled or running."""
    assert _switch_with({"state": "COMPRESSOR_STATE_RUNNING"}).is_on is True
    assert _switch_with({"state": "COMPRESSOR_STATE_SCHEDULED"}).is_on is True
    assert _switch_with({"state": "COMPRESSOR_STATE_AVAILABLE"}).is_on is False
    assert _switch_with(None).is_on is None


def test_switch_turn_on_schedules():
    """Turn-on schedules when not paused, resumes when paused."""
    sw = _switch_with({"state": "COMPRESSOR_STATE_AVAILABLE"})
    asyncio.run(sw.async_turn_on())
    sw.coordinator.async_control_compressor.assert_awaited_once_with(
        proto_stubs.OHPCFAction.OHPCF_ACTION_SCHEDULE
    )

    sw = _switch_with({"state": "COMPRESSOR_STATE_PAUSED"})
    asyncio.run(sw.async_turn_on())
    sw.coordinator.async_control_compressor.assert_awaited_once_with(
        proto_stubs.OHPCFAction.OHPCF_ACTION_RESUME
    )


def test_switch_turn_off_pauses_or_aborts():
    """Turn-off pauses when pausable, otherwise aborts."""
    sw = _switch_with({"is_pausable": True})
    asyncio.run(sw.async_turn_off())
    sw.coordinator.async_control_compressor.assert_awaited_once_with(
        proto_stubs.OHPCFAction.OHPCF_ACTION_PAUSE
    )

    sw = _switch_with({"is_pausable": False})
    asyncio.run(sw.async_turn_off())
    sw.coordinator.async_control_compressor.assert_awaited_once_with(
        proto_stubs.OHPCFAction.OHPCF_ACTION_ABORT
    )


def test_control_compressor_wraps_rpc_error_as_ha_error():
    """A device-side control rejection surfaces as HomeAssistantError, not a raw 500."""
    c = _coordinator()
    c._ensure_channel = AsyncMock(return_value=None)
    err = AioRpcError(
        grpc.StatusCode.INTERNAL,
        Metadata(),
        Metadata(),
        details="ohpcf control: data not available",
    )
    stub = SimpleNamespace(ControlCompressorFlexibility=AsyncMock(side_effect=err))
    with patch.object(proto_stubs, "OHPCFServiceStub", lambda _channel: stub):
        with pytest.raises(HomeAssistantError) as exc:
            asyncio.run(
                c.async_control_compressor(proto_stubs.OHPCFAction.OHPCF_ACTION_SCHEDULE)
            )
    assert "data not available" in str(exc.value)


def test_control_compressor_unimplemented_is_swallowed():
    """UNIMPLEMENTED marks OHPCF unsupported and returns without raising."""
    c = _coordinator()
    c._ensure_channel = AsyncMock(return_value=None)
    err = AioRpcError(
        grpc.StatusCode.UNIMPLEMENTED, Metadata(), Metadata(), details="no ohpcf"
    )
    stub = SimpleNamespace(ControlCompressorFlexibility=AsyncMock(side_effect=err))
    with patch.object(proto_stubs, "OHPCFServiceStub", lambda _channel: stub):
        asyncio.run(
            c.async_control_compressor(proto_stubs.OHPCFAction.OHPCF_ACTION_PAUSE)
        )
    assert c._ohpcf_supported is False
