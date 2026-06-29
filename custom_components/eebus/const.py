"""Constants for the EEBUS integration."""

from homeassistant.const import Platform

DOMAIN = "eebus"
DEFAULT_GRPC_PORT = 50051
CONF_GRPC_HOST = "grpc_host"
CONF_GRPC_PORT = "grpc_port"
CONF_DEVICE_SKI = "device_ski"

# Options: map Home Assistant grid sensors to the bridge's MGCP provider so a
# heat pump can read the live grid / PV-surplus situation (§1.3.1). Grid power
# is the surplus signal (negative = export); the energy totals are optional.
CONF_GRID_POWER_ENTITY = "grid_power_entity"
CONF_GRID_FEED_IN_ENERGY_ENTITY = "grid_feed_in_energy_entity"
CONF_GRID_CONSUMPTION_ENERGY_ENTITY = "grid_consumption_energy_entity"

# Options: map Home Assistant PV sensors to the bridge's VAPD provider so a
# device (e.g. Vaillant VR940) can display the home's PV data (§1.3.3). PV power
# is required; yield energy and nominal peak power are optional.
CONF_PV_POWER_ENTITY = "pv_power_entity"
CONF_PV_YIELD_ENERGY_ENTITY = "pv_yield_energy_entity"
CONF_PV_PEAK_POWER_ENTITY = "pv_peak_power_entity"

# Options: map Home Assistant battery sensors to the bridge's VABD provider so a
# device can display the home's battery state (§1.3.3). Battery power is required;
# charged/discharged energy and state of charge are optional.
CONF_BATTERY_POWER_ENTITY = "battery_power_entity"
CONF_BATTERY_CHARGED_ENERGY_ENTITY = "battery_charged_energy_entity"
CONF_BATTERY_DISCHARGED_ENERGY_ENTITY = "battery_discharged_energy_entity"
CONF_BATTERY_SOC_ENTITY = "battery_soc_entity"

PLATFORMS = [
    Platform.BINARY_SENSOR,
    Platform.NUMBER,
    Platform.SENSOR,
    Platform.SWITCH,
]

PARALLEL_UPDATES = 0  # Coordinator-based, no per-entity polling
