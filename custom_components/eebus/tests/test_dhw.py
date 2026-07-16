"""Tests for domestic-hot-water setpoint control."""

import asyncio
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock, patch

import grpc
from grpc.aio import AioRpcError, Metadata
from homeassistant.components.water_heater import WaterHeaterEntityFeature
from homeassistant.const import ATTR_TEMPERATURE

from custom_components.eebus import proto_stubs
from custom_components.eebus.coordinator import EebusCoordinator
from custom_components.eebus.generated.eebus.v1 import dhw_service_pb2
from custom_components.eebus.snapshot import (
    _async_read_dhw_setpoint,
    _async_read_dhw_system_function,
)
from custom_components.eebus.switch import EebusDHWBoostSwitch
from custom_components.eebus.water_heater import EebusDHWWaterHeater


def _coordinator(data=None):
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator.ski = "test-ski"
    coordinator.data = data
    coordinator._dhw_supported = None
    coordinator._dhw_sysfn_supported = None
    return coordinator


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

    with patch.object(proto_stubs, "dhw_service_stub", return_value=stub):
        result = asyncio.run(
            _async_read_dhw_setpoint(
                None,
                proto_stubs.DeviceRequest(ski=coordinator.ski),
                coordinator.ski,
                coordinator._dhw_supported,
            )
        )

    assert result.value == {
        "value_celsius": 46,
        "min_celsius": 35,
        "max_celsius": 70,
        "step_celsius": 1,
        "writable": True,
    }
    assert result.supported is True
    assert coordinator._dhw_supported is None


def test_read_dhw_setpoint_not_found_marks_unsupported():
    """A device without configurationOfDhwTemperature stays unavailable."""
    error = AioRpcError(grpc.StatusCode.NOT_FOUND, Metadata(), Metadata(), details="no DHW")
    stub = SimpleNamespace(GetDHWSetpoint=AsyncMock(side_effect=error))
    coordinator = _coordinator()

    with patch.object(proto_stubs, "dhw_service_stub", return_value=stub):
        result = asyncio.run(
            _async_read_dhw_setpoint(
                None,
                proto_stubs.DeviceRequest(ski=coordinator.ski),
                coordinator.ski,
                coordinator._dhw_supported,
            )
        )

    assert result.value is None
    assert result.supported is False
    assert coordinator._dhw_supported is None


def test_dhw_water_heater_combines_temperatures_modes_and_writes():
    """The water heater combines measured temperature, target, and operation mode."""
    coordinator = MagicMock()
    coordinator.ski = "test-ski"
    coordinator.data = {
        "connected": True,
        "dhw_temperature_c": 44.5,
        "dhw_supported": True,
        "dhw_setpoint": {
            "value_celsius": 46,
            "min_celsius": 35,
            "max_celsius": 70,
            "step_celsius": 1,
            "writable": True,
        },
        "dhw_sysfn_supported": True,
        "dhw_system_function": {
            "boost_status": "inactive",
            "boost_writable": True,
            "operation_mode": "auto",
            "available_modes": ["auto", "on", "off"],
            "mode_writable": True,
        },
    }
    coordinator.async_write_dhw_setpoint = AsyncMock()
    coordinator.async_set_dhw_operation_mode = AsyncMock()
    coordinator.async_request_refresh = AsyncMock()
    water_heater = EebusDHWWaterHeater(coordinator)

    assert water_heater.current_temperature == 44.5
    assert water_heater.target_temperature == 46
    assert water_heater.min_temp == 35
    assert water_heater.max_temp == 70
    assert water_heater.target_temperature_step == 1
    assert water_heater.current_operation == "auto"
    assert water_heater.operation_list == ["auto", "on", "off"]
    assert water_heater.supported_features == (
        WaterHeaterEntityFeature.TARGET_TEMPERATURE
        | WaterHeaterEntityFeature.OPERATION_MODE
    )
    assert water_heater.available is True

    asyncio.run(water_heater.async_set_temperature(**{ATTR_TEMPERATURE: 47}))
    coordinator.async_write_dhw_setpoint.assert_awaited_once_with(47)
    asyncio.run(water_heater.async_set_operation_mode("off"))
    coordinator.async_set_dhw_operation_mode.assert_awaited_once_with("off")
    assert coordinator.async_request_refresh.await_count == 2


def test_dhw_water_heater_is_read_only_when_controls_are_not_writable():
    """Measured DHW data remains visible without advertising write controls."""
    coordinator = MagicMock()
    coordinator.ski = "test-ski"
    coordinator.data = {"connected": True, "dhw_temperature_c": 44.5}

    water_heater = EebusDHWWaterHeater(coordinator)

    assert water_heater.available is True
    assert water_heater.current_temperature == 44.5
    assert water_heater.target_temperature is None
    assert water_heater.supported_features == WaterHeaterEntityFeature(0)


def test_dhw_water_heater_ignores_stale_unsupported_controls():
    """Support updates hide stale target and mode data retained between refreshes."""
    coordinator = MagicMock()
    coordinator.ski = "test-ski"
    coordinator.data = {
        "connected": True,
        "dhw_temperature_c": 44.5,
        "dhw_supported": False,
        "dhw_setpoint": {"value_celsius": 46, "writable": True},
        "dhw_sysfn_supported": False,
        "dhw_system_function": {
            "operation_mode": "auto",
            "available_modes": ["auto", "off"],
            "mode_writable": True,
        },
    }

    water_heater = EebusDHWWaterHeater(coordinator)

    assert water_heater.available is True
    assert water_heater.target_temperature is None
    assert water_heater.current_operation is None
    assert water_heater.operation_list is None
    assert water_heater.supported_features == WaterHeaterEntityFeature(0)


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

    with patch.object(proto_stubs, "dhw_service_stub", return_value=stub):
        result = asyncio.run(
            _async_read_dhw_system_function(
                None,
                proto_stubs.DeviceRequest(ski=coordinator.ski),
                coordinator.ski,
                coordinator._dhw_sysfn_supported,
            )
        )

    assert result.value == {
        "boost_status": "running",
        "boost_writable": True,
        "operation_mode": "auto",
        "available_modes": ["auto", "on", "off"],
        "mode_writable": True,
    }
    assert result.supported is True
    assert coordinator._dhw_sysfn_supported is None


def test_read_dhw_system_function_not_found_marks_unsupported():
    """A device without configurationOfDhwSystemFunction stays unavailable."""
    error = AioRpcError(grpc.StatusCode.NOT_FOUND, Metadata(), Metadata(), details="no sysfn")
    stub = SimpleNamespace(GetDHWSystemFunction=AsyncMock(side_effect=error))
    coordinator = _coordinator()

    with patch.object(proto_stubs, "dhw_service_stub", return_value=stub):
        result = asyncio.run(
            _async_read_dhw_system_function(
                None,
                proto_stubs.DeviceRequest(ski=coordinator.ski),
                coordinator.ski,
                coordinator._dhw_sysfn_supported,
            )
        )

    assert result.value is None
    assert result.supported is False
    assert coordinator._dhw_sysfn_supported is None


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


def test_dhw_boost_switch():
    """The HA switch mirrors coordinator boost state and calls the DHW RPC."""
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
    coordinator.async_request_refresh = AsyncMock()

    switch = EebusDHWBoostSwitch(coordinator)
    assert switch.entity_category is None
    assert switch.available is True
    assert switch.is_on is True
    assert switch.extra_state_attributes == {"boost_status": "running"}
    asyncio.run(switch.async_turn_off())
    coordinator.async_set_dhw_boost.assert_awaited_once_with(False)
