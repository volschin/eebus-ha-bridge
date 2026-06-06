# Bosch EEBUS reference notes

This file tracks public references and the practical implications for this bridge.

## Public references

- EEBUS official site: https://www.eebus.org/
  - General architecture and interoperability scope.
- EEBUS specifications pointer (via eebus-go README): https://www.eebus.org/media-downloads/
  - Source for SHIP/SPINE and use-case specifications.
- enbility eebus-go README: https://github.com/enbility/eebus-go
  - Documents supported SHIP/SPINE base and example use cases used by this project.
- Bosch/Robotron/PPC announcement: https://www.robotron.de/en/company/current-topics/news/article/bosch-home-comfort-robotron-and-ppc-digitize-control-of-heat-pumps
  - Confirms Bosch heat pump EEBUS control path for grid/tariff use cases.
- Bosch EEBUS gateway case study: https://halready.com/case-study/smart-gateway-for-bosch-heat-pumps-with-eebus/
  - Mentions Bosch heat pump gateway implementation with EEBUS protocol integration.

## Observed Bosch behavior in this project

- Bosch setups can provide power values via MPC.
- Total energy counters can return "data not available".
- LPC and heartbeat/control channels can still be available independently.

## Implementation decision

- Expose all available MPC metrics to Home Assistant via `GetMeasurements`.
- Treat unavailable energy counters as unsupported/not available, not as internal errors.
- Keep dynamic behavior: only metrics delivered by the device are populated in HA.
