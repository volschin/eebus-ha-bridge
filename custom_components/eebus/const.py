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

PLATFORMS = [
    Platform.BINARY_SENSOR,
    Platform.NUMBER,
    Platform.SENSOR,
    Platform.SWITCH,
]

PARALLEL_UPDATES = 0  # Coordinator-based, no per-entity polling
