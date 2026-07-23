# VR940 SPINE function inventory

Companion to `vr940-usecase-dump.txt`. That file lists what the gateway *advertises*
(`nodeManagementUseCaseData`); this one lists what it actually *exposes* — every
feature function reported by detailed discovery, with the operations the device
permits on it.

The distinction matters because a device can implement a function without
advertising the corresponding use case. A function carrying `write` can be written
through a direct SPINE request even when `device.UseCases()` never mentions it —
that is how the DHW and room-heating setpoint paths were built before eebus-go had
the matching use cases.

Source: extended capture taken 2026-07-13 on the live stack (Vaillant VR940f
gateway, SKI `682F708CEBA5DF9ADCB9E6787EA911D9FC3AC490`, aroTHERM plus
VWL 75/8.1 A 230V). Entity/feature layout re-confirmed 2026-07-23.

## Entities

| Address | Type |
|---|---|
| `0` | DeviceInformation |
| `3` | HeatPumpAppliance |
| `3:1` | Compressor |
| `4` | DHWCircuit |
| `5` | HeatingCircuit |
| `5:1` | HeatingZone |
| `5:1:1` | HVACRoom |
| `6` | TemperatureSensor |

Entities `1` and `2` (CEM, `Generic/client`) appear in the use-case dump as the
consumer side for MGCP / VAPD / VABD and carry no server functions.

## Writable functions — the complete write surface

Every function the VR940 reports as writable, and the bridge feature that uses it.
There are no others: the gateway's write surface is fully exercised.

| Entity | Feature | Function | Used by |
|---|---|---|---|
| `3` HeatPumpAppliance | `3:f24` DeviceConfiguration | `deviceConfigurationKeyValueListData` | LPC failsafe limit / duration |
| `3` HeatPumpAppliance | `3:f10` LoadControl | `loadControlLimitListData` | LPC consumption limit (§14a) |
| `3:1` Compressor | `3:1:f19` SmartEnergyManagementPs | `smartEnergyManagementPsData` | OHPCF compressor flexibility |
| `4` DHWCircuit | `4:f9` HVAC | `hvacOverrunListData` | DHW boost switch |
| `4` DHWCircuit | `4:f9` HVAC | `hvacSystemFunctionListData` | DHW operation-mode select |
| `4` DHWCircuit | `4:f18` Setpoint | `setpointListData` | DHW target temperature |
| `5:1:1` HVACRoom | `5:1:1:f9` HVAC | `hvacSystemFunctionListData` | Room-heating mode (`auto`/`on`/`off`) |
| `5:1:1` HVACRoom | `5:1:1:f18` Setpoint | `setpointListData` | Room-heating setpoint |

## Readable functions not consumed by the bridge

Gaps, in rough order of usefulness. Tracked as OPEN-D6 in
`open-items-consolidation-spec.md`.

| Entity | Feature | Function | What it would give us |
|---|---|---|---|
| `5`, `5:1`, `5:1:1` | `f4` DeviceClassification | `deviceClassificationUserData` | User-assigned heating-circuit / zone / room names (the advertised `visualizationOfHeatingAreaName` use case), instead of generic entity names |
| `3` | `3:f7` ElectricalConnection | `electricalConnectionCharacteristicListData` | Nominal power and connection limits of the heat pump |
| `3`, `3:1`, `4`, `5:1:1`, `6` | `f11` Measurement | `measurementConstraintsListData` | Per-measurement min / max / resolution, usable as HA entity attributes |
| `4`, `5:1:1` | `f9` HVAC | `hvacSystemFunctionSetpointRelationListData`, `hvacSystemFunctionOperationModeRelationListData` | Explicit mode↔setpoint relations; currently only resolved implicitly inside the fork's use cases |

## Not present at all

Confirmed absent from the inventory, so not implementable regardless of approach:

- Cooling (no cooling system function or setpoint).
- Schedules / time programs (no `timeSeries`, no `incentiveTable`).
- `hvac_action` (no running-state function on the HVAC features).

## Regenerating this inventory

The `[DISCOVERY]` dump in `internal/eebus/discovery.go` prints entities, features,
their functions and the permitted operations (`r`, `rp`, `w`, `wp`) once per SKI.
It is gated behind debug events, so on the deployed bridge:

1. Set `logging.debug_events: true` (or `EEBUS_DEBUG_EVENTS=true`) and restart.
2. Wait for the device to reach `Trusted` and the use cases to report support.
3. `grep '\[DISCOVERY\]'` in the container log.

Diffing two dumps across firmware versions shows exactly which functions a gateway
update added or removed.
