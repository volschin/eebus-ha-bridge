"""Tests for the EEBUS room-heating climate entity."""

from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock

from homeassistant.components.climate.const import ClimateEntityFeature, HVACMode

from custom_components.eebus.climate import EebusRoomHeatingClimate


def _coordinator(data: dict) -> MagicMock:
    coordinator = MagicMock()
    coordinator.data = data
    coordinator.ski = "test-ski"
    coordinator.last_update_success = True
    return coordinator


def test_temperature_and_constraints_are_device_provided() -> None:
    entity = EebusRoomHeatingClimate(
        _coordinator(
            {
                "room_temperature_c": 22.5,
                "room_heating_setpoint": {
                    "value_celsius": 21.0,
                    "min_celsius": 5.0,
                    "max_celsius": 30.0,
                    "step_celsius": 0.5,
                    "writable": True,
                },
            }
        )
    )
    assert entity.current_temperature == 22.5
    assert entity.target_temperature == 21.0
    assert entity.min_temp == 5.0
    assert entity.max_temp == 30.0
    assert entity.target_temperature_step == 0.5
    assert entity.supported_features & ClimateEntityFeature.TARGET_TEMPERATURE


def test_hvac_modes_map_and_unknown_current_mode_fails_closed() -> None:
    coordinator = _coordinator(
        {
            "room_temperature_c": 22.5,
            "room_heating_system_function": {
                "operation_mode": "on",
                "available_modes": ["auto", "on", "off"],
                "mode_writable": True,
            },
        }
    )
    entity = EebusRoomHeatingClimate(coordinator)
    assert entity.hvac_mode == HVACMode.HEAT
    assert set(entity.hvac_modes) == {HVACMode.AUTO, HVACMode.HEAT, HVACMode.OFF}
    coordinator.data["room_heating_system_function"]["operation_mode"] = "cool"
    assert entity.available is False


async def test_writes_delegate_to_coordinator() -> None:
    coordinator = _coordinator({})
    coordinator.async_set_room_heating_temperature = AsyncMock()
    coordinator.async_set_room_heating_mode = AsyncMock()
    coordinator.async_request_refresh = AsyncMock()
    entity = EebusRoomHeatingClimate(coordinator)

    await entity.async_set_temperature(temperature=22.0)
    await entity.async_set_hvac_mode(HVACMode.AUTO)

    coordinator.async_set_room_heating_temperature.assert_awaited_once_with(22.0)
    coordinator.async_set_room_heating_mode.assert_awaited_once_with("auto")
