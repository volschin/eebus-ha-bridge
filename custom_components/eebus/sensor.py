"""Sensor entities for EEBUS integration."""

from __future__ import annotations

from collections.abc import Callable
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
from .models import CapabilityState
from .state import DeviceState, StateField, is_fresh

_OHPCF_STATUS_OPTIONS = [
    "available",
    "scheduled",
    "running",
    "paused",
    "completed",
    "stopped",
]
_OHPCF_STATE_PREFIX = "COMPRESSOR_STATE_"


def _failsafe_value(data: DeviceState) -> float | None:
    value = data.lpc.failsafe_limit
    return float(value.value_watts) if value is not None else None


def _failsafe_duration(data: DeviceState) -> float | None:
    value = data.lpc.failsafe_limit
    return float(value.duration_minimum_seconds) if value is not None else None


def _consumption_limit(data: DeviceState) -> float | None:
    value = data.lpc.consumption_limit
    return float(value.value_watts) if value is not None else None


def _measurement_value(data: DeviceState, field_name: StateField) -> float | None:
    """Select one typed measurement leaf without a flat mirror structure."""
    value = getattr(data.measurements, field_name.value)
    return None if value is None else float(cast(float, value))


def _failsafe_available(data: DeviceState) -> bool:
    """Failsafe sensors stay available until support is known to be absent."""
    return data.capabilities.failsafe == CapabilityState.AVAILABLE and is_fresh(data, StateField.FAILSAFE_LIMIT)


def _ohpcf_status(data: DeviceState) -> str | None:
    """Return the raw OHPCF process state, lower-cased and without the enum prefix.

    The compressor_flexibility select folds five of the six process states into
    on/paused/off for control purposes; this exposes the raw state for
    visibility.
    """
    flex = data.ohpcf.compressor_flexibility
    if flex is None:
        return None
    option = flex.state.removeprefix(_OHPCF_STATE_PREFIX).lower()
    return option if option in _OHPCF_STATUS_OPTIONS else None


@dataclass(frozen=True, kw_only=True)
class EebusMeasurementDescription(SensorEntityDescription):
    """Describes a coordinator-data-backed EEBUS sensor.

    Simple sensors identify a typed state leaf; nested domain values supply
    ``value_fn`` instead. ``available_fn`` gates availability on capability
    state or offer presence. ``unique_id_suffix``
    defaults to ``key`` and exists only to keep historical unique IDs stable.
    """

    state_field: StateField | None = None
    value_fn: Callable[[DeviceState], float | str | None] | None = None
    available_fn: Callable[[DeviceState], bool] | None = None
    unique_id_suffix: str | None = None


# Primary readings plus LPC and OHPCF diagnostics.
STATE_SENSORS: tuple[EebusMeasurementDescription, ...] = (
    EebusMeasurementDescription(
        key="power",
        state_field=StateField.POWER_WATTS,
        translation_key="power_consumption",
        device_class=SensorDeviceClass.POWER,
        native_unit_of_measurement=UnitOfPower.WATT,
        state_class=SensorStateClass.MEASUREMENT,
    ),
    EebusMeasurementDescription(
        key="energy_consumed",
        state_field=StateField.ENERGY_CONSUMED_KWH,
        translation_key="energy_consumed",
        device_class=SensorDeviceClass.ENERGY,
        native_unit_of_measurement=UnitOfEnergy.KILO_WATT_HOUR,
        state_class=SensorStateClass.TOTAL_INCREASING,
    ),
    EebusMeasurementDescription(
        key="energy_consumed_heating",
        state_field=StateField.ENERGY_CONSUMED_HEATING_KWH,
        translation_key="energy_consumed_heating",
        device_class=SensorDeviceClass.ENERGY,
        native_unit_of_measurement=UnitOfEnergy.KILO_WATT_HOUR,
        state_class=SensorStateClass.TOTAL_INCREASING,
    ),
    EebusMeasurementDescription(
        key="energy_consumed_dhw",
        state_field=StateField.ENERGY_CONSUMED_DHW_KWH,
        translation_key="energy_consumed_dhw",
        device_class=SensorDeviceClass.ENERGY,
        native_unit_of_measurement=UnitOfEnergy.KILO_WATT_HOUR,
        state_class=SensorStateClass.TOTAL_INCREASING,
    ),
    EebusMeasurementDescription(
        key="consumption_limit",
        value_fn=_consumption_limit,
        available_fn=lambda data: (
            data.capabilities.lpc == CapabilityState.AVAILABLE and is_fresh(data, StateField.CONSUMPTION_LIMIT)
        ),
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
        value_fn=_failsafe_value,
        available_fn=_failsafe_available,
        translation_key="failsafe_limit",
        device_class=SensorDeviceClass.POWER,
        native_unit_of_measurement=UnitOfPower.WATT,
        state_class=SensorStateClass.MEASUREMENT,
        entity_category=EntityCategory.DIAGNOSTIC,
    ),
    EebusMeasurementDescription(
        key="failsafe_duration",
        value_fn=_failsafe_duration,
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
        available_fn=lambda data: is_fresh(data, StateField.COMPRESSOR_FLEXIBILITY),
        translation_key="compressor_flexibility_status",
        device_class=SensorDeviceClass.ENUM,
        options=_OHPCF_STATUS_OPTIONS,
        entity_category=EntityCategory.DIAGNOSTIC,
    ),
    EebusMeasurementDescription(
        key="compressor_flexibility_power_estimate",
        value_fn=lambda data: (
            data.ohpcf.compressor_flexibility.requested_power_estimate_w
            if data.ohpcf.compressor_flexibility is not None
            else None
        ),
        available_fn=lambda data: is_fresh(data, StateField.COMPRESSOR_FLEXIBILITY),
        translation_key="compressor_flexibility_power_estimate",
        device_class=SensorDeviceClass.POWER,
        native_unit_of_measurement=UnitOfPower.WATT,
        state_class=SensorStateClass.MEASUREMENT,
        entity_category=EntityCategory.DIAGNOSTIC,
        entity_registry_enabled_default=False,
    ),
    EebusMeasurementDescription(
        key="compressor_flexibility_power_max",
        value_fn=lambda data: (
            data.ohpcf.compressor_flexibility.requested_power_max_w
            if data.ohpcf.compressor_flexibility is not None
            else None
        ),
        available_fn=lambda data: is_fresh(data, StateField.COMPRESSOR_FLEXIBILITY),
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
        value_fn=lambda data: data.connection.device_operating_state,
        available_fn=lambda data: is_fresh(data, StateField.DEVICE_OPERATING_STATE),
        translation_key="device_operating_state",
        entity_category=EntityCategory.DIAGNOSTIC,
        entity_registry_enabled_default=True,
    ),
    EebusMeasurementDescription(
        key="dhw_temperature",
        state_field=StateField.DHW_TEMPERATURE_C,
        translation_key="dhw_temperature",
        device_class=SensorDeviceClass.TEMPERATURE,
        native_unit_of_measurement=UnitOfTemperature.CELSIUS,
        state_class=SensorStateClass.MEASUREMENT,
    ),
    EebusMeasurementDescription(
        key="room_temperature",
        state_field=StateField.ROOM_TEMPERATURE_C,
        translation_key="room_temperature",
        device_class=SensorDeviceClass.TEMPERATURE,
        native_unit_of_measurement=UnitOfTemperature.CELSIUS,
        state_class=SensorStateClass.MEASUREMENT,
    ),
    EebusMeasurementDescription(
        key="outdoor_temperature",
        state_field=StateField.OUTDOOR_TEMPERATURE_C,
        translation_key="outdoor_temperature",
        device_class=SensorDeviceClass.TEMPERATURE,
        native_unit_of_measurement=UnitOfTemperature.CELSIUS,
        state_class=SensorStateClass.MEASUREMENT,
    ),
    EebusMeasurementDescription(
        key="flow_temperature",
        state_field=StateField.FLOW_TEMPERATURE_C,
        translation_key="flow_temperature",
        device_class=SensorDeviceClass.TEMPERATURE,
        native_unit_of_measurement=UnitOfTemperature.CELSIUS,
        state_class=SensorStateClass.MEASUREMENT,
        entity_registry_enabled_default=False,
    ),
    EebusMeasurementDescription(
        key="return_temperature",
        state_field=StateField.RETURN_TEMPERATURE_C,
        translation_key="return_temperature",
        device_class=SensorDeviceClass.TEMPERATURE,
        native_unit_of_measurement=UnitOfTemperature.CELSIUS,
        state_class=SensorStateClass.MEASUREMENT,
        entity_registry_enabled_default=False,
    ),
    EebusMeasurementDescription(
        key="compressor_temperature",
        state_field=StateField.COMPRESSOR_TEMPERATURE_C,
        translation_key="compressor_temperature",
        device_class=SensorDeviceClass.TEMPERATURE,
        native_unit_of_measurement=UnitOfTemperature.CELSIUS,
        state_class=SensorStateClass.MEASUREMENT,
        entity_registry_enabled_default=False,
    ),
    EebusMeasurementDescription(
        key="compressor_power",
        state_field=StateField.COMPRESSOR_POWER_W,
        translation_key="compressor_power",
        device_class=SensorDeviceClass.POWER,
        native_unit_of_measurement=UnitOfPower.WATT,
        state_class=SensorStateClass.MEASUREMENT,
        entity_registry_enabled_default=False,
    ),
    *(
        EebusMeasurementDescription(
            key=f"power_l{phase}",
            state_field=StateField(f"power_l{phase}_w"),
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
            state_field=StateField(f"current_l{phase}_a"),
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
            state_field=StateField(f"voltage_l{phase}_v"),
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
        state_field=StateField.FREQUENCY_HZ,
        translation_key="frequency",
        device_class=SensorDeviceClass.FREQUENCY,
        native_unit_of_measurement=UnitOfFrequency.HERTZ,
        state_class=SensorStateClass.MEASUREMENT,
        entity_registry_enabled_default=False,
    ),
    EebusMeasurementDescription(
        key="energy_produced",
        state_field=StateField.ENERGY_PRODUCED_KWH,
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
        EebusMeasurementSensor(coordinator, description) for description in (*STATE_SENSORS, *MEASUREMENT_SENSORS)
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
        data = self.coordinator.data
        if data is None:
            return False
        available_fn = self.entity_description.available_fn
        state_field = self.entity_description.state_field
        if state_field is not None and not is_fresh(data, state_field):
            return False
        return available_fn(data) if available_fn is not None else True

    @property
    def native_value(self) -> float | str | None:
        """Return the sensor value, or None when unavailable."""
        data = self.coordinator.data
        if data is None:
            return None
        description = self.entity_description
        if description.value_fn is not None:
            return description.value_fn(data)
        if description.state_field is None:
            return None
        return _measurement_value(data, description.state_field)
