"""Tests for the OHPCF (heat-pump compressor flexibility) HA wiring."""

import asyncio
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

import grpc
import pytest
from grpc.aio import AioRpcError, Metadata
from homeassistant.exceptions import HomeAssistantError, ServiceValidationError

from custom_components.eebus import proto_stubs
from custom_components.eebus.coordinator import EebusCoordinator
from custom_components.eebus.generated.eebus.v1 import ohpcf_service_pb2 as ohpcf_pb2
from custom_components.eebus.select import EebusCompressorFlexibilitySelect


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


def _select_with(flex):
    sel = EebusCompressorFlexibilitySelect.__new__(EebusCompressorFlexibilitySelect)
    sel.coordinator = SimpleNamespace(
        data={"compressor_flexibility": flex},
        async_control_compressor=AsyncMock(),
        async_request_refresh=AsyncMock(),
    )
    return sel


def test_select_current_option_distinguishes_paused_from_off():
    """Select reports on/paused/off as three distinct options, not a binary collapse."""
    assert _select_with({"state": "COMPRESSOR_STATE_RUNNING"}).current_option == "on"
    assert _select_with({"state": "COMPRESSOR_STATE_SCHEDULED"}).current_option == "on"
    assert _select_with({"state": "COMPRESSOR_STATE_PAUSED"}).current_option == "paused"
    assert _select_with({"state": "COMPRESSOR_STATE_AVAILABLE"}).current_option == "off"
    assert _select_with({"state": "COMPRESSOR_STATE_STOPPED"}).current_option == "off"
    assert _select_with(None).current_option is None


def test_select_option_on_schedules_or_resumes():
    """Selecting 'on' schedules when not paused, resumes when paused."""
    sel = _select_with({"state": "COMPRESSOR_STATE_AVAILABLE"})
    asyncio.run(sel.async_select_option("on"))
    sel.coordinator.async_control_compressor.assert_awaited_once_with(
        proto_stubs.OHPCFAction.OHPCF_ACTION_SCHEDULE
    )

    sel = _select_with({"state": "COMPRESSOR_STATE_PAUSED"})
    asyncio.run(sel.async_select_option("on"))
    sel.coordinator.async_control_compressor.assert_awaited_once_with(
        proto_stubs.OHPCFAction.OHPCF_ACTION_RESUME
    )


def test_select_option_paused_pauses():
    """Selecting 'paused' issues a pause regardless of is_pausable (device rejects if unsupported)."""
    sel = _select_with({"is_pausable": True})
    asyncio.run(sel.async_select_option("paused"))
    sel.coordinator.async_control_compressor.assert_awaited_once_with(
        proto_stubs.OHPCFAction.OHPCF_ACTION_PAUSE
    )


def test_select_option_off_aborts():
    """Selecting 'off' aborts the process."""
    sel = _select_with({"state": "COMPRESSOR_STATE_RUNNING"})
    asyncio.run(sel.async_select_option("off"))
    sel.coordinator.async_control_compressor.assert_awaited_once_with(
        proto_stubs.OHPCFAction.OHPCF_ACTION_ABORT
    )


def test_select_extra_state_attributes_exposes_constraints():
    """Process constraints (stoppable, min run/pause) surface as attributes."""
    sel = _select_with(
        {"is_stoppable": False, "minimal_run_seconds": 600, "minimal_pause_seconds": 300}
    )
    attrs = sel.extra_state_attributes
    assert attrs == {
        "is_stoppable": False,
        "minimal_run_seconds": 600,
        "minimal_pause_seconds": 300,
    }
    assert _select_with(None).extra_state_attributes is None


def test_control_compressor_wraps_rpc_error_as_validation_error():
    """A device-side control rejection surfaces as ServiceValidationError (HTTP 400)."""
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
        with pytest.raises(ServiceValidationError) as exc:
            asyncio.run(
                c.async_control_compressor(proto_stubs.OHPCFAction.OHPCF_ACTION_SCHEDULE)
            )
    # ServiceValidationError is a HomeAssistantError subclass; message carries the detail.
    assert isinstance(exc.value, HomeAssistantError)
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
