"""Tests for domestic-hot-water state and controls."""

import asyncio
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock, patch

import grpc
import pytest
from grpc.aio import AioRpcError, Metadata
from homeassistant.components.water_heater import WaterHeaterEntityFeature
from homeassistant.const import ATTR_TEMPERATURE
from homeassistant.exceptions import ServiceValidationError

from custom_components.eebus import proto_stubs
from custom_components.eebus.coordinator import EebusCoordinator
from custom_components.eebus.device_session import DeviceSession
from custom_components.eebus.device_streams import DeviceStreams
from custom_components.eebus.generated.eebus.v1 import dhw_service_pb2
from custom_components.eebus.models import (
    CapabilityState,
    DHWSystemFunctionState,
    SetpointState,
)
from custom_components.eebus.snapshot import (
    _async_read_dhw_setpoint,
    _async_read_dhw_system_function,
)
from custom_components.eebus.state import (
    CapabilitiesState,
    ConnectionState,
    DHWState,
    DeviceState,
    DeviceStateStore,
    MeasurementsState,
    StateField,
)
from custom_components.eebus.switch import EebusDHWBoostSwitch
from custom_components.eebus.water_heater import EebusDHWWaterHeater


def _coordinator(state: DeviceState | None = None) -> EebusCoordinator:
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator.ski = "test-ski"
    coordinator.data = state
    coordinator.last_update_success = True
    coordinator._ensure_channel = AsyncMock(return_value=None)
    coordinator._state_store = DeviceStateStore(lambda value: setattr(coordinator, "data", value), state)
    coordinator._device_session = DeviceSession(coordinator.ski, coordinator._ensure_channel)
    coordinator._device_streams = DeviceStreams(
        MagicMock(),
        MagicMock(),
        coordinator.ski,
        coordinator._state_store,
        AsyncMock(),
    )
    return coordinator


def _dhw_state(*, supported: CapabilityState = CapabilityState.AVAILABLE) -> DeviceState:
    return DeviceState(
        connection=ConnectionState(connected=True),
        measurements=MeasurementsState(dhw_temperature_c=44.5),
        dhw=DHWState(
            setpoint=SetpointState(46, 35, 70, 1, True),
            system_function=DHWSystemFunctionState("inactive", True, "auto", ("auto", "on", "off"), True),
        ),
        capabilities=CapabilitiesState(dhw=supported, dhw_system_function=supported),
        fresh_fields=frozenset(
            {
                StateField.DHW_TEMPERATURE_C,
                StateField.DHW_SETPOINT,
                StateField.DHW_SYSTEM_FUNCTION,
            }
        ),
    )


def test_read_dhw_setpoint_maps_value_and_constraints() -> None:
    response = dhw_service_pb2.DHWSetpoint(
        value_celsius=46,
        min_celsius=35,
        max_celsius=70,
        step_celsius=1,
        writable=True,
    )
    stub = SimpleNamespace(GetDHWSetpoint=AsyncMock(return_value=response))
    with patch.object(proto_stubs, "dhw_service_stub", return_value=stub):
        result = asyncio.run(_async_read_dhw_setpoint(None, proto_stubs.DeviceRequest(ski="test-ski"), "test-ski"))
    assert result.value == SetpointState(46, 35, 70, 1, True)
    assert result.status is None


def test_read_dhw_setpoint_not_found_is_deferred_to_reducer() -> None:
    error = AioRpcError(grpc.StatusCode.NOT_FOUND, Metadata(), Metadata(), details="no DHW")
    stub = SimpleNamespace(GetDHWSetpoint=AsyncMock(side_effect=error))
    with patch.object(proto_stubs, "dhw_service_stub", return_value=stub):
        result = asyncio.run(_async_read_dhw_setpoint(None, proto_stubs.DeviceRequest(ski="test-ski"), "test-ski"))
    assert result.value is None
    assert result.status == grpc.StatusCode.NOT_FOUND


def test_water_heater_combines_temperatures_modes_and_writes() -> None:
    coordinator = _coordinator(_dhw_state())
    coordinator.async_write_dhw_setpoint = AsyncMock()
    coordinator.async_set_dhw_operation_mode = AsyncMock()
    coordinator.async_request_refresh = AsyncMock()
    entity = EebusDHWWaterHeater(coordinator)
    assert entity.current_temperature == 44.5
    assert entity.target_temperature == 46
    assert entity.min_temp == 35
    assert entity.max_temp == 70
    assert entity.current_operation == "auto"
    assert entity.operation_list == ["auto", "on", "off"]
    assert entity.supported_features == (
        WaterHeaterEntityFeature.TARGET_TEMPERATURE | WaterHeaterEntityFeature.OPERATION_MODE
    )
    assert entity.available is True
    asyncio.run(entity.async_set_temperature(**{ATTR_TEMPERATURE: 47}))
    asyncio.run(entity.async_set_operation_mode("off"))
    coordinator.async_write_dhw_setpoint.assert_awaited_once_with(47)
    coordinator.async_set_dhw_operation_mode.assert_awaited_once_with("off")


def test_temporary_dhw_state_is_not_exposed_as_current() -> None:
    coordinator = _coordinator(_dhw_state(supported=CapabilityState.TEMPORARILY_UNAVAILABLE))
    entity = EebusDHWWaterHeater(coordinator)
    assert entity.available is True  # fresh measured temperature remains useful
    assert entity.target_temperature is None
    assert entity.current_operation is None


def test_dhw_event_reduces_setpoint_without_poll() -> None:
    coordinator = _coordinator(DeviceState(connection=ConnectionState(connected=True)))
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
    assert coordinator.data.dhw.setpoint.value_celsius == 47
    assert coordinator.data.capabilities.dhw == CapabilityState.AVAILABLE


def test_payload_free_dhw_update_immediately_marks_last_value_stale() -> None:
    coordinator = _coordinator(_dhw_state())
    coordinator._device_streams._hass.async_create_task.side_effect = lambda coroutine: coroutine.close()
    event = dhw_service_pb2.DHWEvent(
        ski="test-ski",
        event_type=dhw_service_pb2.DHW_EVENT_SETPOINT_UPDATED,
    )
    coordinator._handle_dhw_event(event)
    assert coordinator.data.dhw.setpoint == SetpointState(46, 35, 70, 1, True)
    assert coordinator.data.capabilities.dhw == CapabilityState.TEMPORARILY_UNAVAILABLE
    assert StateField.DHW_SETPOINT not in coordinator.data.fresh_fields
    coordinator._device_streams._hass.async_create_task.assert_called_once()


def test_explicit_dhw_support_event_unlocks_sticky_unsupported_state() -> None:
    state = DeviceState(
        connection=ConnectionState(connected=True),
        capabilities=CapabilitiesState(dhw=CapabilityState.UNSUPPORTED),
    )
    coordinator = _coordinator(state)
    coordinator._device_streams._hass.async_create_task.side_effect = lambda coroutine: coroutine.close()
    coordinator._handle_dhw_event(
        dhw_service_pb2.DHWEvent(
            ski="test-ski",
            event_type=dhw_service_pb2.DHW_EVENT_SUPPORT_UPDATED,
        )
    )
    assert coordinator.data.capabilities.dhw == CapabilityState.UNKNOWN


def test_read_dhw_system_function_maps_state() -> None:
    response = dhw_service_pb2.DHWSystemFunctionState(
        boost_status=dhw_service_pb2.DHW_BOOST_STATUS_RUNNING,
        boost_writable=True,
        operation_mode="auto",
        available_modes=["auto", "on", "off"],
        mode_writable=True,
    )
    stub = SimpleNamespace(GetDHWSystemFunction=AsyncMock(return_value=response))
    with patch.object(proto_stubs, "dhw_service_stub", return_value=stub):
        result = asyncio.run(
            _async_read_dhw_system_function(None, proto_stubs.DeviceRequest(ski="test-ski"), "test-ski")
        )
    assert result.value == DHWSystemFunctionState("running", True, "auto", ("auto", "on", "off"), True)


def test_payload_free_system_function_update_marks_last_value_stale() -> None:
    coordinator = _coordinator(_dhw_state())
    coordinator._device_streams._hass.async_create_task.side_effect = lambda coroutine: coroutine.close()
    coordinator._handle_dhw_sysfn_event(
        dhw_service_pb2.DHWSystemFunctionEvent(
            ski="test-ski",
            event_type=dhw_service_pb2.DHW_SYSTEM_FUNCTION_EVENT_STATE_UPDATED,
        )
    )
    assert coordinator.data.dhw.system_function is not None
    assert coordinator.data.capabilities.dhw_system_function == CapabilityState.TEMPORARILY_UNAVAILABLE
    assert StateField.DHW_SYSTEM_FUNCTION not in coordinator.data.fresh_fields


def test_system_function_validation_error_is_mapped() -> None:
    error = AioRpcError(
        grpc.StatusCode.FAILED_PRECONDITION,
        Metadata(),
        Metadata(),
        details="not writable",
    )
    stub = SimpleNamespace(SetDHWBoost=AsyncMock(side_effect=error))
    coordinator = _coordinator()
    with patch.object(proto_stubs, "dhw_service_stub", return_value=stub):
        with pytest.raises(ServiceValidationError):
            asyncio.run(coordinator.async_set_dhw_boost(True))


def test_dhw_boost_switch_reads_typed_state() -> None:
    state = _dhw_state()
    state = DeviceState(
        connection=state.connection,
        measurements=state.measurements,
        dhw=DHWState(
            setpoint=state.dhw.setpoint,
            system_function=DHWSystemFunctionState("running", True, "auto", ("auto", "on", "off"), True),
        ),
        capabilities=state.capabilities,
        fresh_fields=state.fresh_fields,
    )
    coordinator = _coordinator(state)
    coordinator.async_set_dhw_boost = AsyncMock()
    coordinator.async_request_refresh = AsyncMock()
    switch = EebusDHWBoostSwitch(coordinator)
    assert switch.available is True
    assert switch.is_on is True
    assert switch.extra_state_attributes == {"boost_status": "running"}
