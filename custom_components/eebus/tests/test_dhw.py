"""Tests for domestic-hot-water setpoint control."""

import asyncio
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock, patch

import grpc
from grpc.aio import AioRpcError, Metadata

from custom_components.eebus import proto_stubs
from custom_components.eebus.coordinator import EebusCoordinator
from custom_components.eebus.generated.eebus.v1 import dhw_service_pb2
from custom_components.eebus.number import EebusDHWSetpointNumber
from custom_components.eebus.select import EebusDHWOperationModeSelect
from custom_components.eebus.switch import EebusDHWBoostSwitch


def _coordinator(data=None):
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator.ski = "test-ski"
    coordinator.data = data
    coordinator._dhw_supported = None
    coordinator._dhw_sysfn_supported = None
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


def test_read_dhw_system_function_maps_state():
    """The coordinator retains boost and device-advertised mode options."""
    response = dhw_service_pb2.DHWSystemFunctionState(
        boost_status=dhw_service_pb2.DHW_BOOST_STATUS_RUNNING,
        boost_writable=True,
        operation_mode="auto",
        available_modes=["auto", "on", "off"],
        mode_writable=True,
    )
    stub = SimpleNamespace(GetDHWSystemFunction=AsyncMock(return_value=response))
    coordinator = _coordinator()

    result = asyncio.run(
        coordinator._async_read_dhw_system_function(
            None, _fake_proto(stub), proto_stubs.DeviceRequest(ski=coordinator.ski)
        )
    )

    assert result == {
        "boost_status": "running",
        "boost_writable": True,
        "operation_mode": "auto",
        "available_modes": ["auto", "on", "off"],
        "mode_writable": True,
    }
    assert coordinator._dhw_sysfn_supported is True


def test_read_dhw_system_function_not_found_marks_unsupported():
    """A device without configurationOfDhwSystemFunction stays unavailable."""
    error = AioRpcError(grpc.StatusCode.NOT_FOUND, Metadata(), Metadata(), details="no sysfn")
    stub = SimpleNamespace(GetDHWSystemFunction=AsyncMock(side_effect=error))
    coordinator = _coordinator()

    result = asyncio.run(
        coordinator._async_read_dhw_system_function(
            None, _fake_proto(stub), proto_stubs.DeviceRequest(ski=coordinator.ski)
        )
    )

    assert result is None
    assert coordinator._dhw_sysfn_supported is False


def test_dhw_system_function_writes_map_validation_errors():
    """Device-side rejections become Home Assistant validation errors."""
    error = AioRpcError(
        grpc.StatusCode.FAILED_PRECONDITION, Metadata(), Metadata(), details="not writable"
    )
    stub = SimpleNamespace(SetDHWBoost=AsyncMock(side_effect=error))
    coordinator = _coordinator()
    coordinator._ensure_channel = AsyncMock(return_value=None)

    with patch.object(proto_stubs, "dhw_service_stub", return_value=stub):
        try:
            asyncio.run(coordinator.async_set_dhw_boost(True))
        except Exception as err:  # noqa: BLE001
            assert err.__class__.__name__ == "ServiceValidationError"
        else:
            raise AssertionError("expected ServiceValidationError")


def test_dhw_system_function_event_pushes_state():
    """DHW system-function stream events update coordinator data."""
    coordinator = _coordinator(data={"connected": True})
    pushed = {}
    coordinator.async_set_updated_data = pushed.update
    event = dhw_service_pb2.DHWSystemFunctionEvent(
        ski="test-ski",
        event_type=dhw_service_pb2.DHW_SYSTEM_FUNCTION_EVENT_STATE_UPDATED,
        state=dhw_service_pb2.DHWSystemFunctionState(
            boost_status=dhw_service_pb2.DHW_BOOST_STATUS_ACTIVE,
            boost_writable=True,
            operation_mode="on",
            available_modes=["auto", "on", "off"],
            mode_writable=True,
        ),
    )

    coordinator._handle_dhw_sysfn_event(event)

    assert pushed["dhw_system_function"]["boost_status"] == "active"
    assert pushed["dhw_system_function"]["operation_mode"] == "on"
    assert pushed["dhw_sysfn_supported"] is True


def test_dhw_boost_switch_and_operation_mode_select():
    """The HA entities mirror coordinator state and call the DHW sysfn RPCs."""
    coordinator = MagicMock()
    coordinator.ski = "test-ski"
    coordinator.data = {
        "connected": True,
        "dhw_sysfn_supported": True,
        "dhw_system_function": {
            "boost_status": "running",
            "boost_writable": True,
            "operation_mode": "auto",
            "available_modes": ["auto", "on", "off"],
            "mode_writable": True,
        },
    }
    coordinator.async_set_dhw_boost = AsyncMock()
    coordinator.async_set_dhw_operation_mode = AsyncMock()
    coordinator.async_request_refresh = AsyncMock()

    switch = EebusDHWBoostSwitch(coordinator)
    assert switch.available is True
    assert switch.is_on is True
    assert switch.extra_state_attributes == {"boost_status": "running"}
    asyncio.run(switch.async_turn_off())
    coordinator.async_set_dhw_boost.assert_awaited_once_with(False)

    select = EebusDHWOperationModeSelect(coordinator)
    assert select.available is True
    assert select.options == ["auto", "on", "off"]
    assert select.current_option == "auto"
    asyncio.run(select.async_select_option("off"))
    coordinator.async_set_dhw_operation_mode.assert_awaited_once_with("off")
