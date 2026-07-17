"""Tests for OHPCF state conversion and controls."""

import asyncio
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

import grpc
import pytest
from grpc.aio import AioRpcError, Metadata
from homeassistant.exceptions import ServiceValidationError

from custom_components.eebus import proto_stubs
from custom_components.eebus.coordinator import EebusCoordinator
from custom_components.eebus.device_session import DeviceSession
from custom_components.eebus.generated.eebus.v1 import ohpcf_service_pb2 as ohpcf_pb2
from custom_components.eebus.models import CapabilityState, CompressorFlexibilityState
from custom_components.eebus.select import EebusCompressorFlexibilitySelect
from custom_components.eebus.sensor import EebusMeasurementSensor, STATE_SENSORS
from custom_components.eebus.snapshot import _async_read_compressor_flexibility
from custom_components.eebus.state import (
    CapabilitiesState,
    ConnectionState,
    DeviceState,
    DeviceStateStore,
    OHPCFState,
    StateField,
)


def _flex(state: str = "COMPRESSOR_STATE_AVAILABLE") -> CompressorFlexibilityState:
    return CompressorFlexibilityState(True, state, None, None, True, False, 600, 300)


def _coordinator(flex: CompressorFlexibilityState | None = None) -> EebusCoordinator:
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator.ski = "test-ski"
    initial = DeviceState(
        connection=ConnectionState(connected=True),
        ohpcf=OHPCFState(compressor_flexibility=flex),
        capabilities=CapabilitiesState(ohpcf=(CapabilityState.AVAILABLE if flex else CapabilityState.UNKNOWN)),
        fresh_fields=(frozenset({StateField.COMPRESSOR_FLEXIBILITY}) if flex else frozenset()),
    )
    coordinator.data = initial
    coordinator.last_update_success = True
    coordinator._ensure_channel = AsyncMock(return_value=None)
    coordinator._state_store = DeviceStateStore(lambda value: setattr(coordinator, "data", value), initial)
    coordinator._device_session = DeviceSession(coordinator.ski, coordinator._ensure_channel)
    return coordinator


def test_read_compressor_flexibility_maps_fields() -> None:
    flex = ohpcf_pb2.CompressorFlexibility(
        available=True,
        requested_power_estimate_w=1500.0,
        is_pausable=True,
        state=ohpcf_pb2.COMPRESSOR_STATE_RUNNING,
        minimal_run_seconds=600,
    )
    stub = SimpleNamespace(GetCompressorFlexibility=AsyncMock(return_value=flex))
    with patch.object(proto_stubs, "ohpcf_service_stub", return_value=stub):
        result = asyncio.run(
            _async_read_compressor_flexibility(None, proto_stubs.DeviceRequest(ski="test-ski"), "test-ski")
        )
    assert result.status is None
    assert result.value == CompressorFlexibilityState(
        True,
        "COMPRESSOR_STATE_RUNNING",
        1500.0,
        None,
        True,
        False,
        600,
        0,
    )


def test_read_unavailable_defers_status_to_reducer() -> None:
    error = AioRpcError(grpc.StatusCode.UNAVAILABLE, Metadata(), Metadata(), details="off")
    stub = SimpleNamespace(GetCompressorFlexibility=AsyncMock(side_effect=error))
    with patch.object(proto_stubs, "ohpcf_service_stub", return_value=stub):
        result = asyncio.run(
            _async_read_compressor_flexibility(None, proto_stubs.DeviceRequest(ski="test-ski"), "test-ski")
        )
    assert result.value is None
    assert result.status == grpc.StatusCode.UNAVAILABLE


def _select_with(flex: CompressorFlexibilityState | None):
    coordinator = _coordinator(flex)
    coordinator.async_control_compressor = AsyncMock()
    coordinator.async_request_refresh = AsyncMock()
    return EebusCompressorFlexibilitySelect(coordinator)


@pytest.mark.parametrize(
    ("raw", "option"),
    [
        ("COMPRESSOR_STATE_RUNNING", "on"),
        ("COMPRESSOR_STATE_SCHEDULED", "on"),
        ("COMPRESSOR_STATE_PAUSED", "paused"),
        ("COMPRESSOR_STATE_AVAILABLE", "off"),
        ("COMPRESSOR_STATE_STOPPED", "off"),
    ],
)
def test_select_current_option(raw: str, option: str) -> None:
    assert _select_with(_flex(raw)).current_option == option


def test_select_is_unavailable_when_retained_offer_is_stale() -> None:
    coordinator = _coordinator(_flex())
    coordinator.data = DeviceState(
        connection=ConnectionState(connected=True),
        ohpcf=coordinator.data.ohpcf,
        capabilities=CapabilitiesState(ohpcf=CapabilityState.TEMPORARILY_UNAVAILABLE),
    )
    assert EebusCompressorFlexibilitySelect(coordinator).available is False


def test_select_option_on_schedules_or_resumes() -> None:
    select = _select_with(_flex())
    asyncio.run(select.async_select_option("on"))
    select.coordinator.async_control_compressor.assert_awaited_once_with(proto_stubs.OHPCFAction.OHPCF_ACTION_SCHEDULE)
    select = _select_with(_flex("COMPRESSOR_STATE_PAUSED"))
    asyncio.run(select.async_select_option("on"))
    select.coordinator.async_control_compressor.assert_awaited_once_with(proto_stubs.OHPCFAction.OHPCF_ACTION_RESUME)


def test_status_sensor_maps_raw_state() -> None:
    description = next(item for item in STATE_SENSORS if item.key == "compressor_flexibility_status")
    sensor = EebusMeasurementSensor(_coordinator(_flex("COMPRESSOR_STATE_RUNNING")), description)
    assert sensor.native_value == "running"


def test_control_rejection_is_validation_error() -> None:
    coordinator = _coordinator()
    error = AioRpcError(
        grpc.StatusCode.INTERNAL,
        Metadata(),
        Metadata(),
        details="ohpcf control: data not available",
    )
    stub = SimpleNamespace(ControlCompressorFlexibility=AsyncMock(side_effect=error))
    with patch.object(proto_stubs, "ohpcf_service_stub", return_value=stub):
        with pytest.raises(ServiceValidationError):
            asyncio.run(coordinator.async_control_compressor(proto_stubs.OHPCFAction.OHPCF_ACTION_SCHEDULE))


@pytest.mark.parametrize(
    ("error_status", "expected"),
    [
        (None, CapabilityState.AVAILABLE),
        (grpc.StatusCode.UNAVAILABLE, CapabilityState.TEMPORARILY_UNAVAILABLE),
        (grpc.StatusCode.UNIMPLEMENTED, CapabilityState.UNSUPPORTED),
    ],
)
def test_write_status_updates_authoritative_capability(error_status, expected) -> None:
    coordinator = _coordinator()
    side_effect = None
    if error_status is not None:
        side_effect = AioRpcError(error_status, Metadata(), Metadata(), details="failure")
    stub = SimpleNamespace(ControlCompressorFlexibility=AsyncMock(side_effect=side_effect))
    with patch.object(proto_stubs, "ohpcf_service_stub", return_value=stub):
        if error_status == grpc.StatusCode.UNAVAILABLE:
            with pytest.raises(ServiceValidationError):
                asyncio.run(coordinator.async_control_compressor(proto_stubs.OHPCFAction.OHPCF_ACTION_SCHEDULE))
        else:
            asyncio.run(coordinator.async_control_compressor(proto_stubs.OHPCFAction.OHPCF_ACTION_SCHEDULE))
    assert coordinator.data.capabilities.ohpcf == expected
