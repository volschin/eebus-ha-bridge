# Device operating state — implementation plan

**Date:** 2026-07-13
**Branch:** `feat/device-operating-state` (based on `main` at merge of PR #101)
**Design basis:** `docs/superpowers/specs/2026-07-13-vr940-extended-capture-findings.md`,
Phase 2 item 1 ("Device operating state" — confirmed present, high confidence).

## What we're building

A new read-only diagnostic: the VR940's `DeviceDiagnosis.operatingState` (SPINE
`DeviceDiagnosisStateDataType.OperatingState`), read from the `HeatPumpAppliance`
entity, exposed as a new Home Assistant diagnostic sensor
(`sensor.eebus_operating_state` or similar, entity category `diagnostic`).

Confirmed live on the VR940 (idle capture, 2026-07-13):
`{"operatingState": "normalOperation"}`. No `vendorStateCode` or error-code field
was present — do not add fields for those; they simply weren't advertised.

Known enum values in the vendored `spine-go` model
(`model.DeviceDiagnosisOperatingStateType`, `internal/eebus` vendor dir under
`github.com/enbility/spine-go/model/devicediagnosis.go`): `normalOperation`,
`standby`, `failure`, `serviceNeeded`, `overrideDetected`, `inAlarm`,
`notReachable`, `finished`, `temporarilyNotReady`, `off`. Only `normalOperation`
has actually been observed live — treat the field as a pass-through string on
both the wire and in Home Assistant (not a hardcoded Python `StrEnum` limited to
observed values), since the device may report any of these at runtime.

## Why this needs a from-scratch read path (no existing use case covers it)

Unlike temperature/setpoint reads, **no existing eebus-go use case negotiates the
`DeviceDiagnosis` feature type** between the bridge's local CEM entity and the
remote device. The DHW/room-heating/monitoring reads all work by piggybacking on
a feature that some *other* already-added eebus-go use case (Monitoring,
DHWTemperature, etc.) already binds/subscribes as part of its own setup — see
`internal/usecases/hydraulictemp.go`, which reads flow/return temperature purely
from the local cache via `entity.FeatureOfTypeAndRole(model.FeatureTypeTypeMeasurement, model.RoleTypeServer)`
with **no active request of its own**, because the Monitoring use case already
keeps that feature's cache warm.

`DeviceDiagnosis` has no such free ride. The one proven pattern in this codebase
for reading a feature type nothing else negotiates is
`internal/eebus/extendedcapture.go` (on branch `feat/vr940-diagnostics-usecases`,
not yet merged — read it directly via
`git show origin/feat/vr940-diagnostics-usecases:eebus-bridge/internal/eebus/extendedcapture.go`
for reference, but do **not** depend on or import that file/branch):

1. **`Setup(localEntity)` — called once, before `bridgeSvc.Service().Start()`**:
   register a local **client** feature for the type you want to read:
   `localEntity.GetOrAddFeature(model.FeatureTypeTypeDeviceDiagnosis, model.RoleTypeClient)`.
   This must happen before `Start()` — client features are announced at startup
   and cannot be added to an already-running EEBUS service (see
   `docs/vr940-extended-capture.md` for why). In `main.go`, add this call in the
   same block as the other `*.Setup(localEntity)` calls (currently:
   `lpcWrapper.Setup`, `monitoringWrapper.Setup`, `dhwMonitoringWrapper.Setup`,
   `roomMonitoringWrapper.Setup`, `outdoorMonitoringWrapper.Setup` — see
   `cmd/eebus-bridge/main.go` lines ~85-99).

2. **On-demand read**: get the local client feature via
   `local.FeatureOfTypeAndRole(model.FeatureTypeTypeDeviceDiagnosis, model.RoleTypeClient)`,
   find the remote server feature on the `HeatPumpAppliance` entity via the
   registry (same lookup pattern as `hydraulictemp.go`'s `scopedTemperature`:
   iterate `registry.Entities(ski)`, match `info.Entity.FeatureOfTypeAndRole(model.FeatureTypeTypeDeviceDiagnosis, model.RoleTypeServer)`,
   prefer `info.Type == string(model.EntityTypeTypeHeatPumpAppliance)`), then:
   ```go
   msgCounter, err := localFeature.RequestRemoteData(model.FunctionTypeDeviceDiagnosisStateData, nil, nil, remoteFeature)
   ```
   Register a response callback via `localFeature.AddResponseCallback(*msgCounter, func(message spineapi.ResponseMessage) { ... })`
   and wait on a channel with a short timeout (a few seconds — mirror the style
   of `dhwWriteTimeout`/similar constants already in `internal/usecases/dhw.go`,
   but this is a read, so keep the timeout short, e.g. 5s). On timeout, fall back
   to `remoteFeature.DataCopy(model.FunctionTypeDeviceDiagnosisStateData)` in case
   a reply updated the cache before the callback attached (same
   "cache fallback conclusive only when nothing was cached before" nuance as
   `extendedcapture.go`'s `collectAndWrite` — for this feature we don't need that
   full nuance since we're not comparing against a `CachedBefore` snapshot, just
   attempt the direct response first, then the cache as fallback).
   Return `(string, error)`: the string value of `*data.OperatingState`, or a
   sentinel `ErrDeviceOperatingStateUnavailable` if neither path yields data.

3. **Push path**: also subscribe to `localEntity.Device().Events()` in `Setup`
   (`_ = localEntity.Device().Events().Subscribe(w)`, same as
   `hydraulictemp.go`), implement `HandleEvent(payload spineapi.EventPayload)`
   filtering `payload.EventType == spineapi.EventTypeDataChange`,
   `payload.ChangeType == spineapi.ElementChangeUpdate`, and
   `payload.Data.(*model.DeviceDiagnosisStateDataType)` — on match, call the
   read method again (idempotent, cheap) and publish
   `bus.Publish(eebus.Event{SKI: ski, Type: "monitoring.device_operating_state_updated"})`.
   This fires whenever anything (including our own on-demand read) updates the
   cache, and gives the streaming gRPC path a push signal without needing a
   dedicated subscribe/bind.

## Files to add/change

### Go (`eebus-bridge/`)

- **New** `internal/usecases/devicediagnosis.go` — `DeviceOperatingState` struct
  (fields: `bus *eebus.EventBus`, `registry *eebus.DeviceRegistry`,
  `localEntity spineapi.EntityLocalInterface`, `debug bool`), constructor
  `NewDeviceOperatingState(bus, registry, debug)`, `Setup(localEntity)`,
  `HandleEvent(payload)`, `OperatingState(ski string) (string, error)`. Mirror
  the file layout/comment style of `internal/usecases/hydraulictemp.go` exactly
  (package doc comment explaining "reads via already-negotiated feature" would
  be wrong here — instead explain it registers its own client feature and
  performs its own active reads, since no existing use case does).
- **New** `internal/usecases/devicediagnosis_test.go` — unit tests using the
  existing spine-go mocks. Cover: successful read via response callback,
  read via cache fallback (data appears in cache before callback fires),
  timeout with no data (`ErrDeviceOperatingStateUnavailable`), unknown operating
  state string passed through unchanged, `HandleEvent` ignoring unrelated data
  types/change types. **Known gotcha** (see project memory): testify spine-go
  mocks need `.On("String").Return(...)` stubbed or an argument-diff failure
  hangs the test — check `roomheatingtemp_test.go` or `hydraulictemp_test.go`
  for the exact mock setup pattern already in use and copy it.
- `cmd/eebus-bridge/main.go` — construct
  `deviceOperatingState := usecases.NewDeviceOperatingState(bus, registry, cfg.Logging.DebugEvents)`
  and call `deviceOperatingState.Setup(localEntity)` alongside the other
  `.Setup(localEntity)` calls (before `Start()`). This does **not** need
  `AddUseCase` — it's a plain feature registration, not a formal use case, same
  as `hydraulicTemperatures` isn't added via `AddUseCase` either. Do not add it
  to the `registeredUseCases` log-line slice for the same reason
  `HydraulicTemperatures` isn't in it.
- `proto/eebus/v1/monitoring_service.proto`:
  ```proto
  message DeviceDiagnosticsData {
    string operating_state = 1;
    google.protobuf.Timestamp timestamp = 2;
  }
  ```
  Add `rpc GetDeviceDiagnostics(DeviceRequest) returns (DeviceDiagnosticsData);`
  to `MonitoringService`. Extend `MeasurementEvent`'s `oneof data` with
  `DeviceDiagnosticsData device_diagnostics = 6;` and add
  `MEASUREMENT_EVENT_DEVICE_OPERATING_STATE_UPDATED = 11;` to
  `MeasurementEventType` (next free enum value after `MEASUREMENT_EVENT_RETURN_TEMPERATURE_UPDATED = 10`).
- `internal/grpc/monitoring_service.go` — add the `GetDeviceDiagnostics` RPC
  handler (same shape as `GetPowerConsumption`/`GetMeasurements`: resolve SKI,
  call the use case, map `ErrDeviceOperatingStateUnavailable` to the same gRPC
  status code convention already used for "data not available" elsewhere in
  this file). Wire the new event type into the existing
  `SubscribeMeasurements` event-forwarding switch (translate the bus event into
  a `MeasurementEvent` with the new event type + `DeviceDiagnosticsData`
  payload), following the existing pattern for
  `MEASUREMENT_EVENT_FLOW_TEMPERATURE_UPDATED`/etc in that same file.
- `internal/grpc/monitoring_service_test.go` — add coverage for the new RPC
  handler (success + not-available cases) and the new event-forwarding branch.
- Regenerate Go proto stubs: `make proto` from `eebus-bridge/` (needs `buf`;
  see project memory "Proto regen local tooling" — `buf` lives in `~/go/bin`).

### Python (`custom_components/eebus/`)

- Regenerate Python stubs: `bash generate_proto.sh` from repo root (needs
  `grpcio-tools==1.78.0`; project memory notes PEP 668 blocks plain `pip`, use
  `uv --with grpcio-tools==1.78.0`).
- `proto_stubs.py` — add re-exports for `DeviceDiagnosticsData`,
  `GetDeviceDiagnostics` (via the existing `monitoring_service_stub` factory,
  no new factory needed), and the new `MeasurementEventType` enum member. Must
  have explicit `__all__` entries (mypy `--strict` `no_implicit_reexport`).
- `coordinator.py` — add a poll path (new `_async_read_device_diagnostics`
  method, called from `_async_update_data`, mirroring the shape of
  `_async_read_room_heating` etc. — but see the one non-blocking review nit on
  that method from the PR #101 review: don't call `_push_data`/
  `async_set_updated_data` from inside `_async_update_data` like it does;
  just return the value into the assembled `data` dict, matching every other
  sibling `_async_read_*` helper). Store the value as `data["device_operating_state"]`.
  Also add the new event type to the existing measurement-event-stream handler
  (`_run_monitoring_event_stream` or wherever `SubscribeMeasurements` events are
  dispatched) so a push update sets `self.data["device_operating_state"]` and
  calls `self.async_set_updated_data`.
- `sensor.py` — add one new `SensorEntityDescription`: key
  `device_operating_state`, `translation_key="device_operating_state"`,
  `entity_category=EntityCategory.DIAGNOSTIC`, no `device_class` (free-text
  enum, not a fixed HA device class), `entity_registry_enabled_default=True`
  (this is a plain diagnostic read, not experimental/provider-side, so no
  reason to default it off — contrast with the flow/return temperature sensors
  which stayed disabled pending a still-open hardware question; this one's
  evidence is settled). Value comes from `coordinator.data.get("device_operating_state")`.
- `translations/en.json` and `translations/de.json` — add the new sensor's
  name (e.g. "Device operating state" / "Betriebszustand"). If exposing the
  known enum values as translated state strings, add them too (English: Normal
  operation / Standby / Failure / Service needed / Override detected / In
  alarm / Not reachable / Finished / Temporarily not ready / Off) — but the
  entity itself must not error or go `unavailable` on an unrecognized value;
  fall back to displaying the raw string.
- `icons.json` — add an icon for the new sensor key.
- `custom_components/eebus/tests/test_sensor.py` — add coverage for the new
  sensor entity (value present, value absent/unavailable, unknown enum string
  passthrough).

### Docs

- `README.md` — add the new sensor to the feature/sensor list.
- `custom_components/eebus/quality_scale.yaml` — this adds a new entity; check
  whether any rule's status/comment needs updating per CLAUDE.md's rule ("A PR
  that adds a new entity... should update the relevant rule/comment, not
  silently leave it stale").

## Explicitly out of scope for this PR

Per the findings doc, do not add: firmware/hardware revision, pressure, vendor
state code/last error code, boot count/uptime/service date, compressor cycle
count/runtime, current energy mode, `eco` HVAC mode, or away/party mode for room
heating — all confirmed absent on this hardware. Do not add the heating-zone
label (`"Zone 1"`) in this PR either; that's a separate Phase 2 item with a
different entity/feature (`DeviceClassification` on the `HeatingZone` entity,
not `DeviceDiagnosis` on `HeatPumpAppliance`) and should be its own PR to keep
this one small and reviewable.

## Verification before calling this done

- `go build ./...`, `go vet ./...`, `go test -race ./...` from `eebus-bridge/`.
- `ruff check custom_components/`, `mypy custom_components/eebus`,
  `PYTHONPATH=. pytest` from repo root.
- Regenerate both proto stub sets and confirm no drift (`git diff --check`,
  matching `proto-drift` CI job behavior).
- This is a read-only, always-on (no experimental gate) addition — no hardware
  write test is needed, but before merge it should be smoke-tested against the
  live VR940 (build a throwaway-tagged image, deploy to stack 93 same as prior
  spikes, confirm `sensor.eebus_device_operating_state` (or whatever the final
  entity id is) shows `normalOperation` live) before being called done — this
  mirrors how PR #101's DHW/room-heating reads were hardware-validated before
  merge, even though there's no write path to test here.
