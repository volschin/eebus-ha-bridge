"""Sensor entities for EEBUS integration."""

from __future__ import annotations

from collections.abc import Callable, Mapping
from dataclasses import dataclass
from typing import cast

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
    UnitOfTemperature,
    UnitOfTime,
)
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from .coordinator import EebusCoordinator
from .entity import EebusEntity
from .models import CapabilityState, CoordinatorSnapshot

_OHPCF_STATUS_OPTIONS = [
    "available",
    "scheduled",
    "running",
    "paused",
    "completed",
    "stopped",
]
_OHPCF_STATE_PREFIX = "COMPRESSOR_STATE_"


def _nested_float(
    container_key: str, value_key: str
) -> Callable[[CoordinatorSnapshot], float | None]:
    """Build a value_fn reading ``data[container_key][value_key]`` as float."""

    def _get(data: CoordinatorSnapshot) -> float | None:
        container = cast(Mapping[str, object] | None, data.get(container_key))
        if container is None:
            return None
        value = container.get(value_key)
        return None if value is None else float(cast(float, value))

    return _get


def _key_is_present(key: str) -> Callable[[CoordinatorSnapshot], bool]:
    """Build an available_fn requiring ``data[key]`` to be non-None."""

    def _check(data: CoordinatorSnapshot) -> bool:
        return data.get(key) is not None

    return _check


def _failsafe_available(data: CoordinatorSnapshot) -> bool:
    """Failsafe sensors stay available until support is known to be absent."""
    return data.get("failsafe_supported") != CapabilityState.UNSUPPORTED


def _ohpcf_status(data: CoordinatorSnapshot) -> str | None:
    """Return the raw OHPCF process state, lower-cased and without the enum prefix.

    The compressor_flexibility select folds five of the six process states into
    on/paused/off for control purposes; this exposes the raw state for
    visibility.
    """
    flex = data.get("compressor_flexibility")
    if flex is None:
        return None
    option = str(flex.get("state", "")).removeprefix(_OHPCF_STATE_PREFIX).lower()
    return option if option in _OHPCF_STATUS_OPTIONS else None


@dataclass(frozen=True, kw_only=True)
class EebusMeasurementDescription(SensorEntityDescription):
    """Describes a coordinator-data-backed EEBUS sensor.

    Simple sensors name a flat ``data_key``; sensors reading nested containers
    supply ``value_fn`` instead. ``available_fn`` gates availability on
    coordinator data (support flags, offer presence). ``unique_id_suffix``
    defaults to ``key`` and exists only to keep historical unique IDs stable.
    """

    data_key: str = ""
    value_fn: Callable[[CoordinatorSnapshot], float | str | None] | None = None
    available_fn: Callable[[CoordinatorSnapshot], bool] | None = None
    unique_id_suffix: str | None = None


# Primary readings plus LPC and OHPCF diagnostics.
STATE_SENSORS: tuple[EebusMeasurementDescription, ...] = (
    EebusMeasurementDescription(
        key="power",
        data_key="power_watts",
        translation_key="power_consumption",
        device_class=SensorDeviceClass.POWER,
        native_unit_of_measurement=UnitOfPower.WATT,
        state_class=SensorStateClass.MEASUREMENT,
    ),
    EebusMeasurementDescription(
        key="energy_consumed",
        data_key="energy_consumed_kwh",
        translation_key="energy_consumed",
        device_class=SensorDeviceClass.ENERGY,
        native_unit_of_measurement=UnitOfEnergy.KILO_WATT_HOUR,
        state_class=SensorStateClass.TOTAL_INCREASING,
    ),
    EebusMeasurementDescription(
        key="energy_consumed_heating",
        data_key="energy_consumed_heating_kwh",
        translation_key="energy_consumed_heating",
        device_class=SensorDeviceClass.ENERGY,
        native_unit_of_measurement=UnitOfEnergy.KILO_WATT_HOUR,
        state_class=SensorStateClass.TOTAL_INCREASING,
    ),
    EebusMeasurementDescription(
        key="energy_consumed_dhw",
        data_key="energy_consumed_dhw_kwh",
        translation_key="energy_consumed_dhw",
        device_class=SensorDeviceClass.ENERGY,
        native_unit_of_measurement=UnitOfEnergy.KILO_WATT_HOUR,
        state_class=SensorStateClass.TOTAL_INCREASING,
    ),
    EebusMeasurementDescription(
        key="consumption_limit",
        value_fn=_nested_float("consumption_limit", "value_watts"),
        translation_key="consumption_limit",
        device_class=SensorDeviceClass.POWER,
        native_unit_of_measurement=UnitOfPower.WATT,
        state_class=SensorStateClass.MEASUREMENT,
        entity_category=EntityCategory.DIAGNOSTIC,
    ),
    # The failsafe number entity is disabled by default (Gold: less popular
    # entities disabled), so this diagnostic sensor is the only place most
    # users will ever see the value the device falls back to once its
    # heartbeat lapses.
    EebusMeasurementDescription(
        key="failsafe_limit",
        unique_id_suffix="failsafe_limit_diagnostic",
        value_fn=_nested_float("failsafe_limit", "value_watts"),
        available_fn=_failsafe_available,
        translation_key="failsafe_limit",
        device_class=SensorDeviceClass.POWER,
        native_unit_of_measurement=UnitOfPower.WATT,
        state_class=SensorStateClass.MEASUREMENT,
        entity_category=EntityCategory.DIAGNOSTIC,
    ),
    EebusMeasurementDescription(
        key="failsafe_duration",
        value_fn=_nested_float("failsafe_limit", "duration_minimum_seconds"),
        available_fn=_failsafe_available,
        translation_key="failsafe_duration",
        device_class=SensorDeviceClass.DURATION,
        native_unit_of_measurement=UnitOfTime.SECONDS,
        state_class=SensorStateClass.MEASUREMENT,
        entity_category=EntityCategory.DIAGNOSTIC,
    ),
    EebusMeasurementDescription(
        key="compressor_flexibility_status",
        value_fn=_ohpcf_status,
        available_fn=_key_is_present("compressor_flexibility"),
        translation_key="compressor_flexibility_status",
        device_class=SensorDeviceClass.ENUM,
        options=_OHPCF_STATUS_OPTIONS,
        entity_category=EntityCategory.DIAGNOSTIC,
    ),
    EebusMeasurementDescription(
        key="compressor_flexibility_power_estimate",
        value_fn=_nested_float("compressor_flexibility", "requested_power_estimate_w"),
        available_fn=_key_is_present("compressor_flexibility"),
        translation_key="compressor_flexibility_power_estimate",
        device_class=SensorDeviceClass.POWER,
        native_unit_of_measurement=UnitOfPower.WATT,
        state_class=SensorStateClass.MEASUREMENT,
        entity_category=EntityCategory.DIAGNOSTIC,
        entity_registry_enabled_default=False,
    ),
    EebusMeasurementDescription(
        key="compressor_flexibility_power_max",
        value_fn=_nested_float("compressor_flexibility", "requested_power_max_w"),
        available_fn=_key_is_present("compressor_flexibility"),
        translation_key="compressor_flexibility_power_max",
        device_class=SensorDeviceClass.POWER,
        native_unit_of_measurement=UnitOfPower.WATT,
        state_class=SensorStateClass.MEASUREMENT,
        entity_category=EntityCategory.DIAGNOSTIC,
        entity_registry_enabled_default=False,
    ),
)

# Per-phase power/current/voltage, grid frequency and produced energy. These are
# only meaningful when the device advertises them; the sensors report None
# (unavailable) otherwise. Disabled by default to avoid cluttering devices (e.g.
# single-phase heat pumps) that never populate them.
MEASUREMENT_SENSORS: tuple[EebusMeasurementDescription, ...] = (
    EebusMeasurementDescription(
        key="device_operating_state",
        data_key="device_operating_state",
        translation_key="device_operating_state",
        entity_category=EntityCategory.DIAGNOSTIC,
        entity_registry_enabled_default=True,
    ),
    EebusMeasurementDescription(
        key="dhw_temperature",
        data_key="dhw_temperature_c",
        translation_key="dhw_temperature",
        device_class=SensorDeviceClass.TEMPERATURE,
        native_unit_of_measurement=UnitOfTemperature.CELSIUS,
        state_class=SensorStateClass.MEASUREMENT,
    ),
    EebusMeasurementDescription(
        key="room_temperature",
        data_key="room_temperature_c",
        translation_key="room_temperature",
        device_class=SensorDeviceClass.TEMPERATURE,
        native_unit_of_measurement=UnitOfTemperature.CELSIUS,
        state_class=SensorStateClass.MEASUREMENT,
    ),
    EebusMeasurementDescription(
        key="outdoor_temperature",
        data_key="outdoor_temperature_c",
        translation_key="outdoor_temperature",
        device_class=SensorDeviceClass.TEMPERATURE,
        native_unit_of_measurement=UnitOfTemperature.CELSIUS,
        state_class=SensorStateClass.MEASUREMENT,
    ),
    EebusMeasurementDescription(
        key="flow_temperature",
        data_key="flow_temperature_c",
        translation_key="flow_temperature",
        device_class=SensorDeviceClass.TEMPERATURE,
        native_unit_of_measurement=UnitOfTemperature.CELSIUS,
        state_class=SensorStateClass.MEASUREMENT,
        entity_registry_enabled_default=False,
    ),
    EebusMeasurementDescription(
        key="return_temperature",
        data_key="return_temperature_c",
        translation_key="return_temperature",
        device_class=SensorDeviceClass.TEMPERATURE,
        native_unit_of_measurement=UnitOfTemperature.CELSIUS,
        state_class=SensorStateClass.MEASUREMENT,
        entity_registry_enabled_default=False,
    ),
    EebusMeasurementDescription(
        key="compressor_temperature",
        data_key="compressor_temperature_c",
        translation_key="compressor_temperature",
        device_class=SensorDeviceClass.TEMPERATURE,
        native_unit_of_measurement=UnitOfTemperature.CELSIUS,
        state_class=SensorStateClass.MEASUREMENT,
        entity_registry_enabled_default=False,
    ),
    EebusMeasurementDescription(
        key="compressor_power",
        data_key="compressor_power_w",
        translation_key="compressor_power",
        device_class=SensorDeviceClass.POWER,
        native_unit_of_measurement=UnitOfPower.WATT,
        state_class=SensorStateClass.MEASUREMENT,
        entity_registry_enabled_default=False,
    ),
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
    async_add_entities(
        EebusMeasurementSensor(coordinator, description)
        for description in (*STATE_SENSORS, *MEASUREMENT_SENSORS)
    )


class EebusMeasurementSensor(EebusEntity, SensorEntity):
    """Generic sensor backed by a coordinator data key or value function."""

    entity_description: EebusMeasurementDescription

    def __init__(
        self,
        coordinator: EebusCoordinator,
        description: EebusMeasurementDescription,
    ) -> None:
        """Initialize."""
        super().__init__(coordinator)
        self.entity_description = description
        suffix = description.unique_id_suffix or description.key
        self._attr_unique_id = f"{coordinator.ski}_{suffix}"

    @property
    def available(self) -> bool:
        """Apply the description's availability gate on top of the base check."""
        if not super().available:
            return False
        available_fn = self.entity_description.available_fn
        if available_fn is None:
            return True
        data = self.coordinator.data
        return data is not None and available_fn(data)

    @property
    def native_value(self) -> float | str | None:
        """Return the sensor value, or None when unavailable."""
        data = self.coordinator.data
        if data is None:
            return None
        description = self.entity_description
        if description.value_fn is not None:
            return description.value_fn(data)
        return cast(float | str | None, data.get(description.data_key))
