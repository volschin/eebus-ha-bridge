# VR940 extended capture: findings and Phase 2 scope

**Date:** 2026-07-13

**Status:** Phase 1 capture executed once (idle state); resolves most open questions
from the Phase 1 design doc with hardware evidence; one comparison capture (compressor
running) still outstanding before finalizing Phase 2.

**Branch:** `feat/vr940-diagnostics-usecases`

**Supersedes the open questions in** `docs/superpowers/specs/2026-07-13-vr940-diagnostics-and-extended-data-design.md`

## What this is

The extended capture tool (`docs/vr940-extended-capture.md`) was run once against the
live VR940 while the heat pump was idle (compressor not actively running, standby
draw only). This document reads the resulting artifact and answers, with evidence,
every open question the Phase 1 design doc raised. Redacted artifacts are retained at:

- `docs/vr940-extended-capture-2026-07-13-idle.json` (canonical)
- `docs/vr940-extended-capture-2026-07-13-idle.txt` (greppable summary)

Device: aroTHERM plus, device code `VWL 75/8.1 A 230V`, brand Vaillant. Captured
2026-07-13T21:19:23Z, ~4s after bridge start, compressor idle (~7–12W standby draw).

## Resolved: confirmed present, worth implementing

| Value | Evidence | Confidence |
|---|---|---|
| Device operating state | `DeviceDiagnosis` (entity `3`, feature `3:f1000`) `deviceDiagnosisStateData` → `{"operatingState": "normalOperation"}`. Read-only, no write op. | High — standardized field, single unambiguous value |
| Heating-zone label | `DeviceClassification` (entity `5:1` HeatingZone, feature `5:1:f4`) `deviceClassificationUserData` → `{"userLabel": "Zone 1"}`. Entities `5` (HeatingCircuit) and `5:1:1` (HVACRoom) return the *same* function with **empty** data — the label lives only on the `HeatingZone` entity. | High — directly labelled, single-zone system today but the field is genuinely populated |

Both are read via features the bridge already binds/subscribes to no other use case
overlaps this — no new SPINE binding is required, only new reads on already-trusted
entities (same "no separate use case, direct feature read" pattern as
`internal/eebus/hydraulictemp.go`).

## Resolved: confirmed absent on this hardware — do not implement

The Phase 1 doc listed these as candidates pending evidence. The capture now shows
they are **not advertised or not populated** on this VR940 + aroTHERM plus pairing.
Per this project's own out-of-scope rule ("guessing semantics from numeric IDs or a
single unexplained value" / CLAUDE.md YAGNI), none of these should be implemented:

| Requested value | What the capture actually shows |
|---|---|
| Firmware / hardware revision | `deviceClassificationManufacturerData` on **every** entity (`0`, `3`, `5`, `5:1`, `5:1:1`) contains only `brandName`, `vendorName`, `deviceCode`, `deviceName`, `serialNumber` — **no `softwareRevision` or `hardwareRevision` field at all**. The Phase 1 doc's "high-confidence" assumption was wrong; this VR940/aroTHERM combination does not publish firmware/hardware revision over SPINE. |
| Heating-system pressure | No `measurementType=pressure` or pressure-related `scopeType` anywhere in any `measurementDescriptionListData` (checked entities `3`, `3:1`, `4`, `5:1:1`, `6` — all Measurement/server features on the device). Not advertised at all. |
| Vendor state code / last error code | `deviceDiagnosisStateData` contains only `operatingState` — no `vendorStateCode`, no error-code field. |
| Boot count / gateway uptime / next service date | `deviceDiagnosisHeartbeatData` contains only `heartbeatCounter`, `heartbeatTimeout`, `timestamp` — no `bootCounter`, `upTime`/`totalUpTime`, or service-date field anywhere in the DeviceDiagnosis feature. |
| Compressor cycle count / runtime | `electricalConnectionCharacteristicListData` on the whole-appliance entity (`3`) returns `{}` (empty — advertised but nothing populated). The same function **isn't even advertised** on the Compressor entity (`3:1`) — its `ElectricalConnection` feature only exposes `electricalConnectionDescriptionListData` and `electricalConnectionParameterDescriptionListData`. No `totalConsumptionTime`, no count-type `Measurement`, anywhere. The Phase 1 doc's `totalConsumptionTime` candidate does not exist on this device. |
| Current energy mode | `electricalConnectionDescriptionListData` contains only `positiveEnergyDirection` (`consume`) and `powerSupplyType` (`ac`) — no `currentEnergyMode` field. |
| `eco` HVAC operation mode | `hvacOperationModeDescriptionListData` on both `DHWCircuit` (entity `4`) and `HVACRoom` (entity `5:1:1`) lists exactly three modes: `auto`, `on`, `off`. No `eco`. |
| Away mode (`oneDayAway`/`oneDayAtHome`/`party`/`hvacSystemOff`) | `HVACRoom`'s `HVAC` feature (`5:1:1:f9`) does not advertise `hvacOverrunDescriptionListData` or `hvacOverrunListData` **at all** — those functions exist only on `DHWCircuit` (`4:f9`), and there the only overrun type is `oneTimeDhw` (already shipped as DHW boost). Room heating has no overrun mechanism on this device — no away mode, no party mode. |

## Unresolved: needs one more capture before deciding

| Value | What we know so far | What's missing |
|---|---|---|
| Per-phase electrical measurements (current, voltage, frequency, energy consumed/produced, per-phase power) | Both `HeatPumpAppliance` (`3`) and `Compressor` (`3:1`) advertise full `measurementDescriptionListData` for 16 measurement points each (3-phase current/voltage/apparent power, real power total, frequency, energy consumed/produced) — **but** the corresponding `measurementListData` snapshot at idle only returned a value for `measurementId 0` (`acPowerTotal`); all 15 other IDs had no entry at all. No code change is needed to surface these if the device starts reporting them — `internal/usecases/monitoring.go`'s `classifyGenericMeasurement` already builds a generic `raw_<entity>_<type>_<scope>` fallback for any populated, non-temperature/non-power-compressor measurement, so this is a data-availability question, not an implementation gap. | A capture while the compressor is actively running (Phase 1 sequence item 1 vs 2, never executed) to see whether current/voltage/frequency populate under load. If they stay empty even under load, treat as confirmed-absent like the items above. |
| Reserved setpoint slots | Both `DHWCircuit` (`setpointId 1`, active) and `HVACRoom` (`setpointId 1`, active) each also declare a `setpointId 2` with the **identical range/step** as the active setpoint, but **no current value** and **no entry in `hvacSystemFunctionSetpointRelationListData`** for any operation mode. `setpointId 0` is declared on both but carries no scope/unit/range at all in either circuit — likely a SPINE convention placeholder, not a real slot. | Unknown what activates `setpointId 2`. Candidate trigger: toggling the Vaillant myVAILLANT app's energy-management setting, per the original Phase 1 comparison sequence step 3 (not yet run). Do not guess a "reduced/eco setpoint" meaning without a state change that populates it — this is exactly the kind of numeric-ID guess the project's out-of-scope rule forbids. |
| SmartEnergyManagementPs schedule depth | `smartEnergyManagementPsData` on the Compressor (`3:1:f19`) returns a full offered-process structure (`powerSequence`, `operatingConstraintsDuration`, `operatingConstraintsInterrupt`, `powerTimeSlot`, `scheduleConstraints` with earliest/latest start/end times) at idle. `internal/usecases/ohpcf.go` already surfaces the derived values that matter (`RequestedPowerEstimate`/`Max`, `ConsumptionIsStoppable`/`IsPausable`, `MinimalRunDuration`/`PauseDuration`, `Schedule`/`Pause`/`Resume`/`Abort`) via the eebus-go `cem/ohpcf` client — this already covers the useful surface. No gap identified; not a Phase 2 candidate. |

## Recommended Phase 2 scope

Given the evidence above, the minimal, defensible Phase 2 scope is:

1. **Device operating state** — new read (no bind needed, `DeviceDiagnosis/server` is
   already discoverable on the `HeatPumpAppliance` entity the bridge already talks
   to), exposed as a diagnostic `sensor` (enum: `normalOperation` and whatever other
   values the device reports — treat unknown values as pass-through strings, not a
   fixed enum, since only one value has ever been observed).
2. **Heating-zone label** — read `deviceClassificationUserData.userLabel` from the
   `HeatingZone` entity once, expose as a device-info-level attribute (not a
   sensor) on the room-heating climate entity, e.g. as part of its device name or
   an attribute — following the "no separate use case" pattern of
   `hydraulictemp.go`.

Both of the "confirmed absent" tables above should be treated as closed, not
"pending future firmware" — nothing in this project's scope should imply support for
values this specific device does not publish.

Before starting on the "unresolved" per-phase electrical or setpoint-2 questions,
run the compressor-running capture and the energy-management-toggle capture from the
original Phase 1 comparison sequence, save them as
`docs/vr940-extended-capture-2026-07-13-<state>.json/.txt`, and diff against the idle
baseline captured here.

## Out of scope (reaffirmed)

Everything in the "confirmed absent" table, plus anything not directly evidenced by
a capture diff — matches the existing out-of-scope section in
`docs/superpowers/specs/2026-07-13-vr940-diagnostics-and-extended-data-design.md`.
