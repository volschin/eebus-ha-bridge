"""Tests for domestic-hot-water setpoint control."""

import asyncio
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock

import grpc
from grpc.aio import AioRpcError, Metadata

from custom_components.eebus import proto_stubs
from custom_components.eebus.coordinator import EebusCoordinator
from custom_components.eebus.generated.eebus.v1 import dhw_service_pb2
from custom_components.eebus.number import EebusDHWSetpointNumber


def _coordinator(data=None):
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator.ski = "test-ski"
    coordinator.data = data
    coordinator._dhw_supported = None
    return coordinator


def _fake_proto(stub):
    return SimpleNamespace(dhw_service_stub=lambda _channel: stub)


def test_read_dhw_setpoint_maps_value_and_constraints():
    """The coordinator retains all device-provided number constraints."""
    response = dhw_service_pb2.DHWSetpoint(
        value_celsius=46,
        min_celsius=35,
        max_celsius=70,
        step_celsius=1,
        writable=True,
    )
    stub = SimpleNamespace(GetDHWSetpoint=AsyncMock(return_value=response))
    coordinator = _coordinator()

    result = asyncio.run(
        coordinator._async_read_dhw_setpoint(
            None, _fake_proto(stub), proto_stubs.DeviceRequest(ski=coordinator.ski)
        )
    )

    assert result == {
        "value_celsius": 46,
        "min_celsius": 35,
        "max_celsius": 70,
        "step_celsius": 1,
        "writable": True,
    }
    assert coordinator._dhw_supported is True


def test_read_dhw_setpoint_not_found_marks_unsupported():
    """A device without configurationOfDhwTemperature stays unavailable."""
    error = AioRpcError(grpc.StatusCode.NOT_FOUND, Metadata(), Metadata(), details="no DHW")
    stub = SimpleNamespace(GetDHWSetpoint=AsyncMock(side_effect=error))
    coordinator = _coordinator()

    result = asyncio.run(
        coordinator._async_read_dhw_setpoint(
            None, _fake_proto(stub), proto_stubs.DeviceRequest(ski=coordinator.ski)
        )
    )

    assert result is None
    assert coordinator._dhw_supported is False


def test_dhw_number_uses_dynamic_constraints_and_writes():
    """The HA number mirrors the heat pump range and calls the DHW RPC."""
    coordinator = MagicMock()
    coordinator.ski = "test-ski"
    coordinator.data = {
        "connected": True,
        "dhw_supported": True,
        "dhw_setpoint": {
            "value_celsius": 46,
            "min_celsius": 35,
            "max_celsius": 70,
            "step_celsius": 1,
            "writable": True,
        },
    }
    coordinator.async_write_dhw_setpoint = AsyncMock()
    coordinator.async_request_refresh = AsyncMock()
    number = EebusDHWSetpointNumber(coordinator)

    assert number.native_value == 46
    assert number.native_min_value == 35
    assert number.native_max_value == 70
    assert number.native_step == 1
    assert number.available is True

    asyncio.run(number.async_set_native_value(47))
    coordinator.async_write_dhw_setpoint.assert_awaited_once_with(47)
    coordinator.async_request_refresh.assert_awaited_once()


def test_dhw_event_pushes_setpoint():
    """DHW stream events update the coordinator without waiting for a poll."""
    coordinator = _coordinator(data={"connected": True})
    pushed = {}
    coordinator.async_set_updated_data = pushed.update
    event = dhw_service_pb2.DHWEvent(
        ski="test-ski",
        event_type=dhw_service_pb2.DHW_EVENT_SETPOINT_UPDATED,
        setpoint=dhw_service_pb2.DHWSetpoint(
            value_celsius=47,
            min_celsius=35,
            max_celsius=70,
            step_celsius=1,
            writable=True,
        ),
    )

    coordinator._handle_dhw_event(event)

    assert pushed["dhw_setpoint"]["value_celsius"] == 47
    assert pushed["dhw_supported"] is True
