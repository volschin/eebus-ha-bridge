"""Tests for EEBUS integration setup and unload."""

from unittest.mock import AsyncMock, MagicMock, patch

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

    with patch(
        "custom_components.eebus.EebusCoordinator"
    ) as mock_coordinator_cls:
        coordinator = AsyncMock()
        coordinator.async_config_entry_first_refresh = AsyncMock()
        mock_coordinator_cls.return_value = coordinator

        hass.config_entries.async_forward_entry_setups = AsyncMock()

        result = await async_setup_entry(hass, entry)

        assert result is True
        assert entry.runtime_data == coordinator
        coordinator.async_config_entry_first_refresh.assert_awaited_once()


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
