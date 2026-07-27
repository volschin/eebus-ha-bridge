"""Binary sensor entities for EEBUS integration."""

from __future__ import annotations

from homeassistant.components.binary_sensor import (
    BinarySensorDeviceClass,
    BinarySensorEntity,
)
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import EntityCategory
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from .coordinator import EebusCoordinator
from .entity import EebusEntity
from .state import StateField, is_fresh

PARALLEL_UPDATES = 0  # Coordinator-based, no per-entity polling

# Standby draw sits at roughly 5-25 W (measured on a Bosch Compress 5800i), while
# a running compressor pulls far more, so anything above this separates the two
# without needing an extra EEBUS data point.
HEAT_PUMP_ACTIVE_THRESHOLD_W = 100.0


async def async_setup_entry(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    """Set up EEBUS binary sensors."""
    coordinator: EebusCoordinator = entry.runtime_data
    async_add_entities(
        [
            EebusConnectedSensor(coordinator),
            EebusHeartbeatOkSensor(coordinator),
            EebusHeatPumpActiveSensor(coordinator),
        ]
    )


class EebusConnectedSensor(EebusEntity, BinarySensorEntity):
    """Binary sensor for EEBUS connection status.

    Gold: translation_key, entity_category DIAGNOSTIC.
    """

    _attr_device_class = BinarySensorDeviceClass.CONNECTIVITY
    _attr_translation_key = "connected"
    _attr_entity_category = EntityCategory.DIAGNOSTIC

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_connected"

    @property
    def available(self) -> bool:
        """Stay available on a successful poll regardless of connected state.

        EebusEntity.available gates on the device being connected, which would
        make this exact sensor disappear as "unavailable" instead of showing
        "off" the moment it has something to report.
        """
        return self.coordinator.last_update_success

    @property
    def is_on(self) -> bool | None:
        """Return True if connected."""
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.connection.connected


class EebusHeartbeatOkSensor(EebusEntity, BinarySensorEntity):
    """Binary sensor for heartbeat health.

    Gold: translation_key, entity_category DIAGNOSTIC, disabled by default.
    """

    _attr_device_class = BinarySensorDeviceClass.PROBLEM
    _attr_translation_key = "heartbeat_ok"
    _attr_entity_category = EntityCategory.DIAGNOSTIC
    _attr_entity_registry_enabled_default = False  # Gold: less popular, disabled by default

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_heartbeat_ok"

    @property
    def available(self) -> bool:
        """Expose heartbeat health only while its observation is fresh."""
        data = self.coordinator.data
        return bool(
            self.coordinator.last_update_success
            and data
            and data.connection.connected
            and is_fresh(data, StateField.HEARTBEAT_STATUS)
        )

    @property
    def is_on(self) -> bool | None:
        """Return True if heartbeat has a problem (inverted for PROBLEM class)."""
        if self.coordinator.data is None:
            return None
        hb = self.coordinator.data.lpc.heartbeat_status
        if hb is None:
            return None
        # PROBLEM class: is_on=True means there's a problem
        return not hb.within_duration


class EebusHeatPumpActiveSensor(EebusEntity, BinarySensorEntity):
    """Binary sensor deriving compressor activity from total power draw.

    The device reports no explicit "compressor running" flag, so this thresholds
    the power consumption measurement instead: standby is a few watts, an active
    compressor is orders of magnitude above it. Purely derived — it needs no
    EEBUS data point beyond the power reading the power sensor already uses.
    """

    _attr_device_class = BinarySensorDeviceClass.RUNNING
    _attr_translation_key = "heat_pump_active"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_heat_pump_active"

    @property
    def available(self) -> bool:
        """Expose activity only while the power measurement is fresh."""
        data = self.coordinator.data
        return bool(super().available and data and is_fresh(data, StateField.POWER_WATTS))

    @property
    def is_on(self) -> bool | None:
        """Return True while power draw exceeds the standby threshold."""
        data = self.coordinator.data
        if data is None:
            return None
        power = data.measurements.power_watts
        if power is None:
            return None
        return power > HEAT_PUMP_ACTIVE_THRESHOLD_W
