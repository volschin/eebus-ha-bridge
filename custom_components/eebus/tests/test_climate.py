"""Tests for the typed room-heating climate selector."""

from dataclasses import replace
from unittest.mock import AsyncMock, MagicMock

from homeassistant.components.climate.const import ClimateEntityFeature, HVACMode

from custom_components.eebus.climate import EebusRoomHeatingClimate
from custom_components.eebus.models import CapabilityState, SetpointState, SystemFunctionState
from custom_components.eebus.state import (
    CapabilitiesState,
    ConnectionState,
    DeviceState,
    HVACState,
    MeasurementsState,
    StateField,
)


def _coordinator(state: DeviceState) -> MagicMock:
    coordinator = MagicMock()
    coordinator.data = state
    coordinator.ski = "test-ski"
    coordinator.last_update_success = True
    return coordinator


def _state() -> DeviceState:
    return DeviceState(
        connection=ConnectionState(connected=True),
        measurements=MeasurementsState(room_temperature_c=22.5),
        hvac=HVACState(
            setpoint=SetpointState(21.0, 5.0, 30.0, 0.5, True),
            system_function=SystemFunctionState("on", ("auto", "on", "off"), True),
        ),
        capabilities=CapabilitiesState(room_heating=CapabilityState.AVAILABLE),
        fresh_fields=frozenset(
            {
                StateField.ROOM_TEMPERATURE_C,
                StateField.ROOM_HEATING_SETPOINT,
                StateField.ROOM_HEATING_SYSTEM_FUNCTION,
            }
        ),
    )


def test_temperature_and_constraints_are_device_provided() -> None:
    entity = EebusRoomHeatingClimate(_coordinator(_state()))
    assert entity.current_temperature == 22.5
    assert entity.target_temperature == 21.0
    assert entity.min_temp == 5.0
    assert entity.max_temp == 30.0
    assert entity.target_temperature_step == 0.5
    assert entity.supported_features & ClimateEntityFeature.TARGET_TEMPERATURE


def test_hvac_modes_map_and_unknown_current_mode_fails_closed() -> None:
    coordinator = _coordinator(_state())
    entity = EebusRoomHeatingClimate(coordinator)
    assert entity.hvac_mode == HVACMode.HEAT
    assert set(entity.hvac_modes) == {HVACMode.AUTO, HVACMode.HEAT, HVACMode.OFF}
    coordinator.data = replace(
        coordinator.data,
        hvac=replace(
            coordinator.data.hvac,
            system_function=SystemFunctionState("cool", ("auto", "on", "off"), True),
        ),
    )
    assert entity.available is False


async def test_writes_delegate_to_coordinator() -> None:
    coordinator = _coordinator(_state())
    coordinator.async_set_room_heating_temperature = AsyncMock()
    coordinator.async_set_room_heating_mode = AsyncMock()
    coordinator.async_request_refresh = AsyncMock()
    entity = EebusRoomHeatingClimate(coordinator)
    await entity.async_set_temperature(temperature=22.0)
    await entity.async_set_hvac_mode(HVACMode.AUTO)
    coordinator.async_set_room_heating_temperature.assert_awaited_once_with(22.0)
    coordinator.async_set_room_heating_mode.assert_awaited_once_with("auto")
