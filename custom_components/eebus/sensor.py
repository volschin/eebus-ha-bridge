"""Sensor entities for EEBUS integration."""

from __future__ import annotations

from dataclasses import dataclass

from homeassistant.components.sensor import (
    SensorDeviceClass,
    SensorEntity,
    SensorEntityDescription,
    SensorStateClass,
)
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import (
    EntityCategory,
    UnitOfElectricCurrent,
    UnitOfElectricPotential,
    UnitOfEnergy,
    UnitOfFrequency,
    UnitOfPower,
)
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from .coordinator import EebusCoordinator
from .entity import EebusEntity


@dataclass(frozen=True, kw_only=True)
class EebusMeasurementDescription(SensorEntityDescription):
    """Describes a coordinator-data-backed EEBUS measurement sensor."""

    data_key: str


# Per-phase power/current/voltage, grid frequency and produced energy. These are
# only meaningful when the device advertises them; the sensors report None
# (unavailable) otherwise. Disabled by default to avoid cluttering devices (e.g.
# single-phase heat pumps) that never populate them.
MEASUREMENT_SENSORS: tuple[EebusMeasurementDescription, ...] = (
    *(
        EebusMeasurementDescription(
            key=f"power_l{phase}",
            data_key=f"power_l{phase}_w",
            translation_key=f"power_l{phase}",
            device_class=SensorDeviceClass.POWER,
            native_unit_of_measurement=UnitOfPower.WATT,
            state_class=SensorStateClass.MEASUREMENT,
            entity_registry_enabled_default=False,
        )
        for phase in (1, 2, 3)
    ),
    *(
        EebusMeasurementDescription(
            key=f"current_l{phase}",
            data_key=f"current_l{phase}_a",
            translation_key=f"current_l{phase}",
            device_class=SensorDeviceClass.CURRENT,
            native_unit_of_measurement=UnitOfElectricCurrent.AMPERE,
            state_class=SensorStateClass.MEASUREMENT,
            entity_registry_enabled_default=False,
        )
        for phase in (1, 2, 3)
    ),
    *(
        EebusMeasurementDescription(
            key=f"voltage_l{phase}",
            data_key=f"voltage_l{phase}_v",
            translation_key=f"voltage_l{phase}",
            device_class=SensorDeviceClass.VOLTAGE,
            native_unit_of_measurement=UnitOfElectricPotential.VOLT,
            state_class=SensorStateClass.MEASUREMENT,
            entity_registry_enabled_default=False,
        )
        for phase in (1, 2, 3)
    ),
    EebusMeasurementDescription(
        key="frequency",
        data_key="frequency_hz",
        translation_key="frequency",
        device_class=SensorDeviceClass.FREQUENCY,
        native_unit_of_measurement=UnitOfFrequency.HERTZ,
        state_class=SensorStateClass.MEASUREMENT,
        entity_registry_enabled_default=False,
    ),
    EebusMeasurementDescription(
        key="energy_produced",
        data_key="energy_produced_kwh",
        translation_key="energy_produced",
        device_class=SensorDeviceClass.ENERGY,
        native_unit_of_measurement=UnitOfEnergy.KILO_WATT_HOUR,
        state_class=SensorStateClass.TOTAL_INCREASING,
        entity_registry_enabled_default=False,
    ),
)


async def async_setup_entry(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    """Set up EEBUS sensors."""
    coordinator: EebusCoordinator = entry.runtime_data
    entities: list[SensorEntity] = [
        EebusPowerSensor(coordinator),
        EebusEnergyConsumedSensor(coordinator),
        EebusEnergyConsumedHeatingSensor(coordinator),
        EebusEnergyConsumedDhwSensor(coordinator),
        EebusConsumptionLimitSensor(coordinator),
    ]
    entities.extend(
        EebusMeasurementSensor(coordinator, description)
        for description in MEASUREMENT_SENSORS
    )
    async_add_entities(entities)


class EebusMeasurementSensor(EebusEntity, SensorEntity):
    """Generic sensor backed by a coordinator data key."""

    entity_description: EebusMeasurementDescription

    def __init__(
        self,
        coordinator: EebusCoordinator,
        description: EebusMeasurementDescription,
    ) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self.entity_description = description
        self._attr_unique_id = f"{coordinator.ski}_{description.key}"

    @property
    def native_value(self) -> float | None:
        """Return the measurement value, or None when unavailable."""
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get(self.entity_description.data_key)


class EebusPowerSensor(EebusEntity, SensorEntity):
    """Sensor for current power consumption."""

    _attr_device_class = SensorDeviceClass.POWER
    _attr_native_unit_of_measurement = UnitOfPower.WATT
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_translation_key = "power_consumption"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_power"

    @property
    def native_value(self) -> float | None:
        """Return current power in watts."""
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("power_watts")


class EebusEnergyConsumedSensor(EebusEntity, SensorEntity):
    """Sensor for cumulative consumed energy."""

    _attr_device_class = SensorDeviceClass.ENERGY
    _attr_native_unit_of_measurement = UnitOfEnergy.KILO_WATT_HOUR
    _attr_state_class = SensorStateClass.TOTAL_INCREASING
    _attr_translation_key = "energy_consumed"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_energy_consumed"

    @property
    def native_value(self) -> float | None:
        """Return consumed energy in kWh."""
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("energy_consumed_kwh")


class EebusEnergyConsumedHeatingSensor(EebusEntity, SensorEntity):
    """Sensor for cumulative consumed energy for space heating."""

    _attr_device_class = SensorDeviceClass.ENERGY
    _attr_native_unit_of_measurement = UnitOfEnergy.KILO_WATT_HOUR
    _attr_state_class = SensorStateClass.TOTAL_INCREASING
    _attr_translation_key = "energy_consumed_heating"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_energy_consumed_heating"

    @property
    def native_value(self) -> float | None:
        """Return consumed heating energy in kWh."""
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("energy_consumed_heating_kwh")


class EebusEnergyConsumedDhwSensor(EebusEntity, SensorEntity):
    """Sensor for cumulative consumed energy for domestic hot water."""

    _attr_device_class = SensorDeviceClass.ENERGY
    _attr_native_unit_of_measurement = UnitOfEnergy.KILO_WATT_HOUR
    _attr_state_class = SensorStateClass.TOTAL_INCREASING
    _attr_translation_key = "energy_consumed_dhw"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_energy_consumed_dhw"

    @property
    def native_value(self) -> float | None:
        """Return consumed DHW energy in kWh."""
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("energy_consumed_dhw_kwh")


class EebusConsumptionLimitSensor(EebusEntity, SensorEntity):
    """Read-only sensor showing current consumption limit."""

    _attr_device_class = SensorDeviceClass.POWER
    _attr_native_unit_of_measurement = UnitOfPower.WATT
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_translation_key = "consumption_limit"
    _attr_entity_category = EntityCategory.DIAGNOSTIC

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_consumption_limit"

    @property
    def native_value(self) -> float | None:
        """Return current limit in watts."""
        if self.coordinator.data is None:
            return None
        limit = self.coordinator.data.get("consumption_limit")
        if limit is None:
            return None
        return limit.get("value_watts")
