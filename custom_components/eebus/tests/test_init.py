"""Tests for EEBUS integration setup and unload."""

from unittest.mock import AsyncMock, MagicMock, call, patch

import pytest


@pytest.mark.asyncio
async def test_setup_entry():
    """Test async_setup_entry creates coordinator and forwards platforms."""
    from custom_components.eebus import async_setup_entry

    hass = MagicMock()
    entry = MagicMock()
    entry.data = {
        "grpc_host": "127.0.0.1",
        "grpc_port": 50051,
        "device_ski": "test-ski",
    }
    entry.runtime_data = None
    entry.async_on_unload = MagicMock()
    entry.add_update_listener = MagicMock()

    with (
        patch("custom_components.eebus.EebusCoordinator") as mock_coordinator_cls,
        patch("custom_components.eebus.er.async_get") as async_get_entity_registry,
    ):
        coordinator = AsyncMock()
        coordinator.async_config_entry_first_refresh = AsyncMock()
        coordinator.async_start_streams = MagicMock()
        coordinator.async_start_grid_push = MagicMock()
        coordinator.async_start_pv_push = MagicMock()
        coordinator.async_start_battery_push = MagicMock()
        coordinator.ski = "test-ski"
        mock_coordinator_cls.return_value = coordinator

        entity_registry = MagicMock()
        entity_registry.async_get_entity_id.side_effect = [
            "number.eebus_dhw_setpoint",
            "select.eebus_dhw_operation_mode",
        ]
        async_get_entity_registry.return_value = entity_registry

        hass.config_entries.async_forward_entry_setups = AsyncMock()

        result = await async_setup_entry(hass, entry)

        assert result is True
        assert entry.runtime_data == coordinator
        coordinator.async_config_entry_first_refresh.assert_awaited_once()
        coordinator.async_start_streams.assert_called_once()
        coordinator.async_start_grid_push.assert_called_once()
        coordinator.async_start_pv_push.assert_called_once()
        coordinator.async_start_battery_push.assert_called_once()
        assert entity_registry.async_remove.call_args_list == [
            call("number.eebus_dhw_setpoint"),
            call("select.eebus_dhw_operation_mode"),
        ]


@pytest.mark.asyncio
async def test_unload_entry():
    """Test async_unload_entry shuts down coordinator."""
    from custom_components.eebus import async_unload_entry

    hass = MagicMock()
    entry = MagicMock()

    coordinator = AsyncMock()
    coordinator.async_shutdown = AsyncMock()
    entry.runtime_data = coordinator

    hass.config_entries.async_unload_platforms = AsyncMock(return_value=True)

    result = await async_unload_entry(hass, entry)

    assert result is True
    coordinator.async_shutdown.assert_awaited_once()
