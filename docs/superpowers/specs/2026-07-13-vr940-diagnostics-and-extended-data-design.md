# VR940 diagnostics and extended data design

**Date:** 2026-07-13

**Status:** Proposed; hardware capture required before entity implementation

**Branch:** `feat/vr940-diagnostics-usecases`

## Goal

Extend the EEBUS bridge and Home Assistant integration with additional VR940 data,
with priority on:

- heating-circuit/system pressure;
- firmware and hardware revisions;
- device and energy-management state;
- number of compressor on/off cycles;
- operation time;
- away mode;
- further diagnostic and measurement data already exposed by the gateway.

The design must expose a value only when its meaning can be established from SPINE
metadata or an explicitly documented vendor mapping. Presence of a feature alone is
not evidence that a particular value is available.

## Evidence and current limitation

The live capture in `docs/vr940-usecase-dump.txt` proves that the VR940 exposes these
relevant server features:

| Entity | Relevant server features |
|---|---|
| `0` DeviceInformation | DeviceClassification |
| `3` HeatPumpAppliance | DeviceClassification, DeviceConfiguration, DeviceDiagnosis, ElectricalConnection, Measurement |
| `3:1` Compressor | ElectricalConnection, Measurement, SmartEnergyManagementPs |
| `4` DHWCircuit | HVAC, Measurement, Setpoint |
| `5`, `5:1`, `5:1:1` heating hierarchy | DeviceClassification; HVACRoom additionally HVAC, Measurement and Setpoint |
| `6` TemperatureSensor | Measurement |

It also advertises monitoring and configuration use cases for room heating and DHW,
plus OHPCF compressor flexibility and power monitoring.

The current discovery logger only records entity types, feature types and advertised
use cases. It does **not** record feature operations or any description/value list.
Consequently, the capture does not yet prove that the VR940 publishes pressure,
cycle counts, runtime or an away overrun. An extended data capture is the first
implementation phase and a gate for the later entities.

## Requested data and EEBUS correspondence

| Requested value | SPINE correspondence | Evidence from current dump | Decision |
|---|---|---|---|
| Heating-system pressure | `Measurement` with `measurementType=pressure`; units include Pa, bar and psi | `Measurement/server` exists on HeatPumpAppliance, but no description data was captured | Implement only if the live description identifies the value as heating/system pressure through entity, scope, label or description |
| Firmware | `DeviceClassificationManufacturerData.softwareRevision`; hardware is `hardwareRevision` | DeviceClassification exists on DeviceInformation, HeatPumpAppliance and heating entities | High-confidence implementation; populate Home Assistant device information from the primary device and retain per-entity revisions in diagnostics when they differ |
| Energy manager state | No single canonical value; candidates are DeviceDiagnosis operating/vendor state, ElectricalConnection current energy mode, OHPCF process state and the bridge connection/trust state | All except a generic CEM configuration state have corresponding features or existing lifecycle data | Expose separate, precisely named states; do not combine them into an ambiguous `energy_manager_state` |
| Number of on/off cycles | A `Measurement` of type `count` may carry a vendor label; there is no compressor-start-specific standard scope in the used SPINE model | Compressor has Measurement/server, but descriptions are missing | Hardware capture required; never substitute DeviceDiagnosis `bootCounter`, `loadCycleCount`, or OHPCF `maxCyclesPerDay` |
| Operation time | Candidate 1: Compressor ElectricalConnection `totalConsumptionTime`; candidate 2: a labelled `Measurement` of type `time`; DeviceDiagnosis `upTime`/`totalUpTime` describes device uptime | ElectricalConnection exists on HeatPumpAppliance and Compressor; DeviceDiagnosis exists on HeatPumpAppliance | Expose compressor runtime only after the connection/measurement semantics are confirmed; expose gateway uptime separately |
| Away mode | HVAC overrun type `oneDayAway`, paired with `oneDayAtHome`; this is not an HVAC operation mode | HVAC/server and room-heating configuration are advertised, but overrun descriptions and write operations are missing | If advertised and writable, map the exact one-day override; do not present it as an indefinite holiday mode |

### Important distinctions

`DeviceDiagnosisServiceData.bootCounter` counts device boots, not compressor starts.
The measurement scope `loadCycleCount` is not sufficiently specific for a compressor
without matching entity and description metadata. OHPCF `maxCyclesPerDay` is a
constraint on an offered flexible load process, not a historical cycle counter.

Likewise, these are distinct states and must remain distinct in the API and UI:

1. SHIP/SPINE connection and trust state of the bridge;
2. `DeviceDiagnosis.operatingState` and `vendorStateCode` of the heat-pump appliance;
3. `ElectricalConnection.currentEnergyMode` (`consume`, `produce`, `idle`, `auto`);
4. OHPCF offer/process state and constraints;
5. a Vaillant application setting that enables energy management for heating or
   DHW, if such a setting appears in DeviceConfiguration data.

The current dump proves no standard field corresponding to the two Vaillant
application energy-management toggles. DeviceConfiguration descriptions and vendor
state codes are the candidates to inspect.

## Additional data worth capturing

### Device classification

- software and hardware revision;
- device name/code and manufacturer label/description;
- serial number and manufacturer node identification;
- user label/description for the heating circuit, zone and room.

Primary device classification maps to Home Assistant `DeviceInfo`, including
`sw_version` and `hw_version`. Entity-specific labels should be preferred over
address-derived names when present.

### Device diagnosis

- operating state: normal, standby, failure, service needed, alarm, off, and related
  standardized states;
- vendor state code and last error code;
- current and total gateway uptime;
- power-supply condition;
- installation time, boot count and next service date;
- heartbeat counter and timeout.

Error and service fields are diagnostic data. They must not be interpreted as the
heating or compressor activity state.

### Measurements

For every Measurement server, capture all descriptions, constraints and current
values. In addition to the already known room, outside, DHW, flow, return,
compressor-temperature and compressor-power data, useful candidates include:

- pressure;
- volumetric or mass flow;
- electrical power, energy, current, voltage, frequency and power factor;
- component temperatures;
- accumulated energy;
- counts and time values whose labels identify their purpose.

There is no standardized heating-pressure, compressor-start-count or
compressor-runtime scope in the currently pinned SPINE model. A generic type alone
is therefore insufficient. Resolution requires this evidence, in descending order:

1. standardized scope plus compatible entity and unit;
2. measurement type plus compatible entity and unambiguous label/description;
3. an explicit, tested vendor mapping keyed by device code and stable metadata.

Numeric IDs and current entity addresses are discovery results, never stable mapping
keys.

### Electrical connection

Capture descriptions, parameter descriptions, characteristics and state lists from
both HeatPumpAppliance and Compressor. Potential values include:

- current energy mode;
- current and total consumption/production time;
- phase topology and measurement relations;
- permitted or characteristic power values.

`totalConsumptionTime` on the Compressor entity is a promising runtime candidate,
but hardware validation must show whether it follows physical compressor activity
and whether it survives gateway/device restarts.

### HVAC and room-heating monitoring

Capture system-function, operation-mode, overrun and relation descriptions and data
from both DHWCircuit and HVACRoom. Potential additions are:

- the advertised `eco` operation mode, if actually present in the description list;
- exact current system-function mode;
- active overrun type and status;
- `oneDayAway`, `oneDayAtHome`, `party` and `hvacSystemOff` overrides if advertised;
- setpoint selection and changeability;
- monitoring of room-heating system function, which is advertised but not currently
  represented as a dedicated state.

A persistent vacation schedule would normally require corresponding schedule/time
table data. No TimeTable server is present in the current dump, so the initial scope
is limited to an explicitly advertised one-day overrun.

### Device configuration and OHPCF

Capture DeviceConfiguration description, value and constraint lists. Standard or
vendor-labelled fields may reveal failsafe values, energy-management enablement, or
additional Vaillant settings. Unknown fields remain visible in the redacted probe
artifact but are not automatically exposed as writable Home Assistant controls.

The existing OHPCF data can additionally report process/offer state, requested and
maximum power, pausable/stoppable flags, minimum run/pause durations and maximum
allowed cycles. These are flexibility-control data, not historical appliance
diagnostics.

## Extended discovery capture

Add a temporary, opt-in read-only extended capture mode alongside the existing
`[DISCOVERY]` logger. It must not run by default on every connection.

For every remote server feature, the capture records:

- entity address and type;
- feature address, type and role;
- every advertised function and whether read, write and subscription are supported;
- cached data before active reads;
- the result of explicit reads only for functions advertising read support;
- the resulting typed description, constraint and value data;
- read errors and SPINE result codes without aborting the remaining capture.

At minimum, the capture requests these families:

- DeviceClassification manufacturer and user data;
- DeviceDiagnosis state, service and heartbeat data;
- DeviceConfiguration descriptions, values and constraints;
- Measurement descriptions, values and constraints;
- ElectricalConnection descriptions, parameter descriptions, characteristics,
  permitted values and state;
- HVAC system-function, operation-mode, overrun and relation descriptions/data;
- Setpoint descriptions, values and constraints;
- SmartEnergyManagementPs descriptions, constraints and current process data.

The output consists of:

1. canonical JSON for complete machine-readable evidence and future fixtures;
2. a stable, greppable text summary for review and diffs.

Serial numbers, SKIs, network addresses and manufacturer node identifiers are
redacted by default. A locally retained unredacted capture may be used for debugging.
The probe performs no writes. Writable HVAC overruns are validated only in a later,
separately enabled hardware test that restores the original state.

## Bridge API design

Do not add a generic untyped key/value API to the permanent gRPC surface. After a
hardware capture confirms the mappings, add typed fields grouped by meaning:

- `DeviceInfo`: software revision and hardware revision;
- `Diagnostics`: operating state, vendor state, last error, gateway uptime, boot
  count, service date and power-supply condition;
- `HeatingMeasurements`: system pressure and any confirmed flow/runtime/count data;
- `ElectricalState`: current energy mode and confirmed consumption times;
- `RoomHeating`: current operation mode and supported one-day overrides;
- existing OHPCF response: retain flexibility process state and constraints.

All optional values need presence semantics. The bridge omits values that were not
published or whose meaning could not be resolved; zero must remain a valid measured
value. Push updates should be used when the remote function supports subscription,
with polling only as a fallback.

## Home Assistant mapping

| Confirmed bridge value | Home Assistant representation |
|---|---|
| Software/hardware revision | `DeviceInfo.sw_version` / `DeviceInfo.hw_version` |
| Heating-system pressure | Sensor, pressure device class, native unit from SPINE (prefer bar display without changing source precision) |
| Gateway uptime / confirmed compressor runtime | Separate diagnostic duration sensors |
| Boot count / confirmed compressor starts | Separate diagnostic count sensors |
| Device operating state / current energy mode | Diagnostic enum sensors with unavailable state when absent |
| Last error / service needed | Diagnostic sensor and, where useful, problem binary sensor |
| Next service | Diagnostic timestamp sensor |
| One-day away/home | Climate preset or dedicated control whose displayed name explicitly says “today”; only when the overrun is advertised and writable |

Availability follows the underlying feature/data availability, not merely the SHIP
connection. Removed or no-longer-advertised fields must be cleaned from the entity
registry using the integration's established migration pattern.

## Implementation phases

### Phase 1 — capture and fixture

1. Implement the opt-in, read-only extended data capture with unit tests for stable
   ordering, supported-operation filtering and redaction.
2. Run it against the paired VR940 after a normal heating cycle and retain the
   redacted JSON/text artifacts under `docs/`.
3. Repeat the capture while the compressor is running and stopped, and before/after
   changing the Vaillant energy-management and one-day-away settings. Diff the
   captures to identify state semantics.

No permanent entity work starts until this phase establishes the relevant IDs,
descriptions, operations and values.

### Phase 2 — standardized diagnostics

1. Extend classification storage, protobuf data and Home Assistant DeviceInfo with
   software/hardware revisions.
2. Add DeviceDiagnosis read/subscription support and typed diagnostics.
3. Add generic pressure and other unambiguous measurement classification.
4. Add ElectricalConnection state reads, preserving the distinction between gateway
   uptime and compressor consumption time.

### Phase 3 — HVAC overrides and monitoring

1. Implement monitoring of the room-heating system function.
2. Resolve HVAC overruns by type and affected system-function relation, never by ID.
3. If `oneDayAway`/`oneDayAtHome` are present and writable, implement activate/cancel
   with acknowledged writes, read-back confirmation and rollback in hardware tests.

### Phase 4 — vendor-confirmed values

Only after capture evidence, add compressor starts, compressor runtime or Vaillant
energy-management configuration fields that require label-based/vendor mappings.
Mappings need fixtures, device-code guards and documented behavior when firmware
changes or the field disappears.

## Hardware acceptance criteria

- Firmware and hardware revision match the values shown by the gateway/application.
- Pressure tracks an independent system-pressure reading and uses a compatible unit.
- Gateway uptime increases across normal compressor stops; it is never labelled as
  compressor runtime.
- A runtime candidate changes only under the documented operating condition and its
  reset/persistence behavior is recorded.
- A cycle-count candidate increments exactly once across a controlled compressor
  off/on cycle and is not a boot or flexibility limit counter.
- Each exposed energy-management state is correlated with one controlled setting or
  process-state change and keeps a precise name.
- One-day away activation is acknowledged, becomes observable through read-back,
  and cancellation restores the prior HVAC state.
- Restarting the bridge rediscovers all mappings without relying on entity, feature,
  measurement, connection or overrun IDs from a previous session.

## Out of scope

- Guessing semantics from numeric IDs or a single unexplained value change;
- exposing all DeviceConfiguration fields as arbitrary writable controls;
- calling SHIP connectivity, gateway uptime or OHPCF limits “energy manager state”,
  “compressor runtime” or “compressor cycles”;
- implementing a persistent holiday calendar without an advertised schedule feature;
- falling back to Vaillant's internal eBUS in this feature. Such a fallback can be a
  separate integration decision if the values are confirmed absent from EEBUS.
