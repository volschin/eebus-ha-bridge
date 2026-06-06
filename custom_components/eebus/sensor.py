"""Sensor entities for EEBUS integration."""

from __future__ import annotations

from homeassistant.components.sensor import (
    SensorDeviceClass,
    SensorEntity,
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


async def async_setup_entry(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    """Set up EEBUS sensors."""
    coordinator: EebusCoordinator = entry.runtime_data
    async_add_entities([
        EebusPowerSensor(coordinator),
        EebusEnergyConsumedSensor(coordinator),
        EebusEnergyProducedSensor(coordinator),
        EebusEnergyConsumedHeatingSensor(coordinator),
        EebusEnergyConsumedDhwSensor(coordinator),
        EebusFrequencySensor(coordinator),
        EebusPowerL1Sensor(coordinator),
        EebusPowerL2Sensor(coordinator),
        EebusPowerL3Sensor(coordinator),
        EebusCurrentL1Sensor(coordinator),
        EebusCurrentL2Sensor(coordinator),
        EebusCurrentL3Sensor(coordinator),
        EebusVoltageL1Sensor(coordinator),
        EebusVoltageL2Sensor(coordinator),
        EebusVoltageL3Sensor(coordinator),
        EebusConsumptionLimitSensor(coordinator),
        EebusNominalMaxPowerSensor(coordinator),
        EebusLimitDurationSensor(coordinator),
    ])


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


class EebusEnergyProducedSensor(EebusEntity, SensorEntity):
    """Sensor for cumulative produced energy."""

    _attr_device_class = SensorDeviceClass.ENERGY
    _attr_native_unit_of_measurement = UnitOfEnergy.KILO_WATT_HOUR
    _attr_state_class = SensorStateClass.TOTAL_INCREASING
    _attr_translation_key = "energy_produced"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_energy_produced"

    @property
    def native_value(self) -> float | None:
        """Return produced energy in kWh."""
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("energy_produced_kwh")


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


class EebusFrequencySensor(EebusEntity, SensorEntity):
    """Sensor for grid frequency."""

    _attr_native_unit_of_measurement = UnitOfFrequency.HERTZ
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_translation_key = "grid_frequency"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_grid_frequency"

    @property
    def native_value(self) -> float | None:
        """Return grid frequency in Hz."""
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("grid_frequency_hz")


class EebusPowerL1Sensor(EebusEntity, SensorEntity):
    """Sensor for phase L1 power."""

    _attr_device_class = SensorDeviceClass.POWER
    _attr_native_unit_of_measurement = UnitOfPower.WATT
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_translation_key = "power_l1"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_power_l1"

    @property
    def native_value(self) -> float | None:
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("power_l1_watts")


class EebusPowerL2Sensor(EebusEntity, SensorEntity):
    """Sensor for phase L2 power."""

    _attr_device_class = SensorDeviceClass.POWER
    _attr_native_unit_of_measurement = UnitOfPower.WATT
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_translation_key = "power_l2"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_power_l2"

    @property
    def native_value(self) -> float | None:
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("power_l2_watts")


class EebusPowerL3Sensor(EebusEntity, SensorEntity):
    """Sensor for phase L3 power."""

    _attr_device_class = SensorDeviceClass.POWER
    _attr_native_unit_of_measurement = UnitOfPower.WATT
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_translation_key = "power_l3"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_power_l3"

    @property
    def native_value(self) -> float | None:
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("power_l3_watts")


class EebusCurrentL1Sensor(EebusEntity, SensorEntity):
    """Sensor for phase L1 current."""

    _attr_native_unit_of_measurement = UnitOfElectricCurrent.AMPERE
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_translation_key = "current_l1"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_current_l1"

    @property
    def native_value(self) -> float | None:
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("current_l1_ampere")


class EebusCurrentL2Sensor(EebusEntity, SensorEntity):
    """Sensor for phase L2 current."""

    _attr_native_unit_of_measurement = UnitOfElectricCurrent.AMPERE
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_translation_key = "current_l2"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_current_l2"

    @property
    def native_value(self) -> float | None:
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("current_l2_ampere")


class EebusCurrentL3Sensor(EebusEntity, SensorEntity):
    """Sensor for phase L3 current."""

    _attr_native_unit_of_measurement = UnitOfElectricCurrent.AMPERE
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_translation_key = "current_l3"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_current_l3"

    @property
    def native_value(self) -> float | None:
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("current_l3_ampere")


class EebusVoltageL1Sensor(EebusEntity, SensorEntity):
    """Sensor for phase L1 voltage."""

    _attr_native_unit_of_measurement = UnitOfElectricPotential.VOLT
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_translation_key = "voltage_l1"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_voltage_l1"

    @property
    def native_value(self) -> float | None:
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("voltage_l1_volt")


class EebusVoltageL2Sensor(EebusEntity, SensorEntity):
    """Sensor for phase L2 voltage."""

    _attr_native_unit_of_measurement = UnitOfElectricPotential.VOLT
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_translation_key = "voltage_l2"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_voltage_l2"

    @property
    def native_value(self) -> float | None:
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("voltage_l2_volt")


class EebusVoltageL3Sensor(EebusEntity, SensorEntity):
    """Sensor for phase L3 voltage."""

    _attr_native_unit_of_measurement = UnitOfElectricPotential.VOLT
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_translation_key = "voltage_l3"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_voltage_l3"

    @property
    def native_value(self) -> float | None:
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("voltage_l3_volt")


class EebusNominalMaxPowerSensor(EebusEntity, SensorEntity):
    """Sensor for device nominal max power."""

    _attr_device_class = SensorDeviceClass.POWER
    _attr_native_unit_of_measurement = UnitOfPower.WATT
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_translation_key = "nominal_max_power"
    _attr_entity_category = EntityCategory.DIAGNOSTIC

    def __init__(self, coordinator: EebusCoordinator) -> None:
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_nominal_max_power"

    @property
    def native_value(self) -> float | None:
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.get("consumption_nominal_max_watts")


class EebusLimitDurationSensor(EebusEntity, SensorEntity):
    """Sensor for current LPC limit duration."""

    _attr_native_unit_of_measurement = "s"
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_translation_key = "limit_duration"
    _attr_entity_category = EntityCategory.DIAGNOSTIC

    def __init__(self, coordinator: EebusCoordinator) -> None:
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_limit_duration"

    @property
    def native_value(self) -> float | None:
        if self.coordinator.data is None:
            return None
        limit = self.coordinator.data.get("consumption_limit")
        if limit is None:
            return None
        return limit.get("duration_seconds")
