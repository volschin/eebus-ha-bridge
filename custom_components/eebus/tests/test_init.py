"""Tests for EEBUS integration setup and unload."""

import asyncio
import logging
from unittest.mock import AsyncMock, MagicMock, call, patch

import pytest

from custom_components.eebus.const import CONF_DEVICE_SKI


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
            "switch.eebus_heartbeat",
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
            call("switch.eebus_heartbeat"),
        ]


def test_remove_replaced_heartbeat_switch():
    """Test the retired per-device heartbeat switch is removed from the registry."""
    from custom_components.eebus import _remove_replaced_heartbeat_switch

    hass = MagicMock()
    entity_registry = MagicMock()
    entity_registry.async_get_entity_id.return_value = "switch.eebus_heartbeat"

    with patch(
        "custom_components.eebus.er.async_get", return_value=entity_registry
    ):
        _remove_replaced_heartbeat_switch(hass, "test-ski")

    entity_registry.async_get_entity_id.assert_called_once_with(
        "switch", "eebus", "test-ski_heartbeat"
    )
    entity_registry.async_remove.assert_called_once_with("switch.eebus_heartbeat")


async def test_remove_config_entry_device_allows_stale_identifier():
    """A device whose identifier no longer matches the canonical SKI is removable."""
    from custom_components.eebus import async_remove_config_entry_device

    canonical = "682F708CEBA5DF9ADCB9E6787EA911D9FC3AC490"
    hass = MagicMock()
    entry = MagicMock(data={CONF_DEVICE_SKI: canonical})
    device = MagicMock(identifiers={("eebus", canonical.lower())})

    assert await async_remove_config_entry_device(hass, entry, device) is True


async def test_remove_config_entry_device_blocks_active_identifier():
    """The device the entry is actively using cannot be removed from the UI."""
    from custom_components.eebus import async_remove_config_entry_device

    canonical = "682F708CEBA5DF9ADCB9E6787EA911D9FC3AC490"
    hass = MagicMock()
    entry = MagicMock(data={CONF_DEVICE_SKI: canonical})
    device = MagicMock(identifiers={("eebus", canonical)})

    assert await async_remove_config_entry_device(hass, entry, device) is False


@pytest.mark.asyncio
async def test_unload_entry():
    """Test async_unload_entry shuts down coordinator."""
    from custom_components.eebus import async_unload_entry

    hass = MagicMock()
    entry = MagicMock()

    coordinator = AsyncMock()
    coordinator.async_shutdown = AsyncMock()
    coordinator.mark_entry_unloaded = MagicMock()
    coordinator.reconfigure_lock = asyncio.Lock()
    entry.runtime_data = coordinator

    hass.config_entries.async_unload_platforms = AsyncMock(return_value=True)

    result = await async_unload_entry(hass, entry)

    assert result is True
    coordinator.async_shutdown.assert_awaited_once()


async def test_migrate_entry_rejects_malformed_legacy_ski():
    """A pre-existing entry with a non-hex/wrong-length SKI fails migration."""
    from custom_components.eebus import async_migrate_entry

    hass = MagicMock()
    entry = MagicMock(
        version=1,
        data={CONF_DEVICE_SKI: "not-an-ski"},
        unique_id="not-an-ski",
        entry_id="01",
        title="Broken entry",
    )
    hass.config_entries.async_entries.return_value = [entry]

    assert not await async_migrate_entry(hass, entry)
    hass.config_entries.async_update_entry.assert_not_called()


async def test_migrate_entry_only_bumps_version_when_ski_is_canonical():
    """A canonical v1 entry needs only a version update."""
    from custom_components.eebus import async_migrate_entry

    canonical = "682F708CEBA5DF9ADCB9E6787EA911D9FC3AC490"
    hass = MagicMock()
    entry = MagicMock(
        version=1,
        data={CONF_DEVICE_SKI: canonical},
        unique_id=canonical,
        entry_id="01",
        title="Older entry",
    )
    hass.config_entries.async_entries.return_value = [entry]

    assert await async_migrate_entry(hass, entry)
    hass.config_entries.async_update_entry.assert_called_once_with(entry, version=2)


async def test_migrate_entry_canonicalizes_data_and_unique_id():
    """A formatted mixed-case v1 SKI migrates to canonical storage."""
    from custom_components.eebus import async_migrate_entry

    raw = "68:2f:70:8C:EB:A5:DF:9A:DC:B9:E6:78:7E:A9:11:D9:FC:3A:C4:90"
    canonical = "682F708CEBA5DF9ADCB9E6787EA911D9FC3AC490"
    hass = MagicMock()
    entry = MagicMock(
        version=1,
        data={CONF_DEVICE_SKI: raw, "grpc_host": "bridge"},
        unique_id=raw,
        entry_id="01",
        title="Heat pump",
    )
    hass.config_entries.async_entries.return_value = [entry]

    with patch("custom_components.eebus.dr.async_get") as async_get_device_registry:
        device_registry = MagicMock()
        async_get_device_registry.return_value = device_registry

        assert await async_migrate_entry(hass, entry)

        device_registry.async_get_device.assert_called_once_with(
            identifiers={("eebus", raw)}
        )
        device_registry.async_update_device.assert_called_once_with(
            device_registry.async_get_device.return_value.id,
            new_identifiers={("eebus", canonical)},
        )
    hass.config_entries.async_update_entry.assert_called_once_with(
        entry,
        data={CONF_DEVICE_SKI: canonical, "grpc_host": "bridge"},
        unique_id=canonical,
        version=2,
    )


async def test_migrate_entry_skips_device_rename_when_device_not_found():
    """No matching device registry entry means nothing to rename."""
    from custom_components.eebus import async_migrate_entry

    raw = "68:2f:70:8C:EB:A5:DF:9A:DC:B9:E6:78:7E:A9:11:D9:FC:3A:C4:90"
    hass = MagicMock()
    entry = MagicMock(
        version=1,
        data={CONF_DEVICE_SKI: raw},
        unique_id=raw,
        entry_id="01",
        title="Heat pump",
    )
    hass.config_entries.async_entries.return_value = [entry]

    with patch("custom_components.eebus.dr.async_get") as async_get_device_registry:
        device_registry = MagicMock()
        device_registry.async_get_device.return_value = None
        async_get_device_registry.return_value = device_registry

        assert await async_migrate_entry(hass, entry)

        device_registry.async_update_device.assert_not_called()


async def test_migrate_entry_rejects_newer_duplicate(caplog: pytest.LogCaptureFixture):
    """The newer of two entries for one canonical SKI fails with a warning."""
    from custom_components.eebus import async_migrate_entry

    canonical = "682F708CEBA5DF9ADCB9E6787EA911D9FC3AC490"
    older = MagicMock(
        version=2,
        data={CONF_DEVICE_SKI: canonical},
        unique_id=canonical,
        entry_id="01",
        title="Older entry",
    )
    newer = MagicMock(
        version=1,
        data={CONF_DEVICE_SKI: canonical.lower()},
        unique_id=canonical.lower(),
        entry_id="02",
        title="Newer entry",
    )
    hass = MagicMock()
    hass.config_entries.async_entries.return_value = [older, newer]

    with caplog.at_level(logging.WARNING, logger="custom_components.eebus"):
        assert not await async_migrate_entry(hass, newer)

    hass.config_entries.async_update_entry.assert_not_called()
    assert "02 (Newer entry)" in caplog.text
    assert "01 (Older entry)" in caplog.text
