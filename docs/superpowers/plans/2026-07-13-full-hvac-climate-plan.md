# Full VR940 Room-Heating Climate Support — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a native Home Assistant `climate` entity for the VR940's room heating (current/target temperature, device-advertised auto/on/off modes), upgrade flow/return temperature to a typed push-capable path, and correct the stale "Vaillant does not expose HVAC" documentation claim.

**Architecture:** Mirror the existing DHW pattern (`internal/usecases/dhw.go` + `dhwsysfn.go`) exactly for the two new room-heating Configuration use cases — built **directly in the bridge** on the generic `usecase.UseCaseBase` primitive, **not** as new `eebus-go` fork contributions. This is a deliberate deviation from the original spec (`docs/superpowers/specs/2026-07-13-full-hvac-climate-design.md` §5), verified against the actual codebase: DHW's Configuration-side use cases (write-capable) were never contributed upstream — only the read-only Monitoring use cases (MDT/MRT/MOT) needed fork packages, and all SPINE constants room-heating needs (`ScopeTypeTypeRoomAirTemperature`, `HvacSystemFunctionTypeTypeHeating`, `ScopeTypeTypeFlowTemperature`, `ScopeTypeTypeReturnTemperature`) already exist in the currently-pinned `spine-go` model. Flow/return temperature reuses the entity already registered by the existing `MonitoringWrapper` (MPC use case binds the `HeatPumpAppliance` entity that carries the `Measurement/server` feature) via a lightweight raw-SPINE event subscriber, not a new negotiated use case.

**Tech Stack:** Go (`eebus-bridge`, `enbility/eebus-go`/`spine-go` — no fork changes), Protocol Buffers (`buf`, `grpcio-tools==1.78.0`), Python 3.13 / Home Assistant (`custom_components/eebus`).

## Implementation Status (2026-07-13)

- Tasks 1–9: implemented and locally verified. The task-local commit steps remain intentionally pending so the resumed work can be reviewed and committed as one coherent change set.
- Task 10: pending manual VR940 hardware acceptance. Flow/return sensors remain disabled by default until this gate passes.
- Task 11: pending Task 10; the experimental HVAC probe is retained until hardware acceptance is complete.
- Verification: `go vet ./...`, `go test -race ./...`, `ruff check custom_components/`, strict `mypy`, 76 Python tests, both protobuf generators, and `git diff --check` pass.

## Global Constraints

- Room-heating Configuration use cases are bridge-local Go, mirroring `dhw.go`/`dhwsysfn.go` — no `eebus-go` fork commits, no `UPSTREAM_PATCHES.md` entry, no `go.mod` pin change.
- No hardcoded entity addresses, setpoint IDs, system-function IDs, or mode IDs — resolve everything from Description/Constraints/Relation lists at runtime, exactly like the DHW code.
- Only an explicit `IsOperationModeIdChangeable == false` blocks a mode write; a missing/nil flag must not hide a write the device advertises via `Operations().Write()` (same `boolPtrNotFalse` rule as DHW).
- Full-list-copy on every write (copy the whole cached list, mutate only the addressed entry, send the complete list) — never send a partial list.
- Room heating is a first-class, always-on feature like DHW — no experimental/env-var gate.
- Climate entity is fail-closed: if the current EEBUS mode can't map to a known `HVACMode`, the entity goes `unavailable`; the separate room-temperature sensor keeps working independently.
- `ruff check custom_components/`, `mypy custom_components/eebus --strict` (via `pyproject.toml` config), `PYTHONPATH=. pytest`, `go vet ./...`, `go test -race ./...` must all stay green after every task.
- After any `.proto` change: regenerate **both** stub sets — `make proto` in `eebus-bridge/` (needs `buf`, `protoc-gen-go`, `protoc-gen-go-grpc` on `PATH`, e.g. `~/go/bin`) and `bash generate_proto.sh` from repo root (needs `grpcio-tools==1.78.0`; use an ephemeral `uv venv` + `uv pip install` if system Python is externally-managed — do **not** pass `--break-system-packages`).
- Flow/return sensors (`sensor.py`) stay `entity_registry_enabled_default=False` until hardware acceptance (Task 10) passes; only then flip to `True` in a follow-up commit.

---

### Task 1: `RoomHeatingTemperature` use case (mirrors `dhw.go`)

**Files:**
- Create: `eebus-bridge/internal/usecases/roomheatingtemp.go`
- Test: `eebus-bridge/internal/usecases/roomheatingtemp_test.go`

**Interfaces:**
- Produces: `type RoomHeatingSetpoint struct { Value, Minimum, Maximum, Step float64; Writable bool }`, `func NewRoomHeatingTemperature(localEntity spineapi.EntityLocalInterface, bus *eebus.EventBus, registry *eebus.DeviceRegistry, debug bool) *RoomHeatingTemperature`, `func (r *RoomHeatingTemperature) UseCase() eebusapi.UseCaseInterface`, `func (r *RoomHeatingTemperature) CompatibleEntity(ski string) spineapi.EntityRemoteInterface`, `func (r *RoomHeatingTemperature) State(entity spineapi.EntityRemoteInterface) (RoomHeatingSetpoint, error)`, `func (r *RoomHeatingTemperature) Write(ctx context.Context, entity spineapi.EntityRemoteInterface, value float64) error`. Sentinel errors: `ErrRoomHeatingDataUnavailable`, `ErrRoomHeatingNotWritable`, `ErrRoomHeatingOutOfRange`, `ErrRoomHeatingInvalidStep`. Bus events: `"roomheating.setpoint_updated"`, `"roomheating.use_case_support_updated"`.

- [ ] **Step 1: Write the failing tests**

```go
// eebus-bridge/internal/usecases/roomheatingtemp_test.go
package usecases

import (
	"context"
	"errors"
	"testing"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
)

func TestRoomHeatingStateUsesScopedSetpointAndDeviceConstraints(t *testing.T) {
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeSetpointDescriptionListData).Return(
		&model.SetpointDescriptionListDataType{SetpointDescriptionData: []model.SetpointDescriptionDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeRoomAirTemperature)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointListData).Return(
		&model.SetpointListDataType{SetpointData: []model.SetpointDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(21)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointConstraintsListData).Return(
		&model.SetpointConstraintsListDataType{SetpointConstraintsData: []model.SetpointConstraintsDataType{
			{
				SetpointId:       ptr(model.SetpointIdType(1)),
				SetpointRangeMin: model.NewScaledNumberType(5),
				SetpointRangeMax: model.NewScaledNumberType(30),
				SetpointStepSize: model.NewScaledNumberType(0.5),
			},
		}},
	)
	operation := mocks.NewOperationsInterface(t)
	operation.On("Write").Return(true)
	feature.On("Operations").Return(map[model.FunctionType]spineapi.OperationsInterface{
		model.FunctionTypeSetpointListData: operation,
	})

	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).Return(feature)

	state, err := (&RoomHeatingTemperature{}).State(entity)
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if state.Value != 21 || state.Minimum != 5 || state.Maximum != 30 || state.Step != 0.5 || !state.Writable {
		t.Errorf("State() = %+v", state)
	}
}

func TestRoomHeatingStateFailsClosedWithoutConstraints(t *testing.T) {
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeSetpointDescriptionListData).Return(
		&model.SetpointDescriptionListDataType{SetpointDescriptionData: []model.SetpointDescriptionDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeRoomAirTemperature)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointListData).Return(
		&model.SetpointListDataType{SetpointData: []model.SetpointDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(21)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointConstraintsListData).Return(nil)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).Return(feature)

	_, err := (&RoomHeatingTemperature{}).State(entity)
	if !errors.Is(err, ErrRoomHeatingDataUnavailable) {
		t.Fatalf("State() error = %v, want ErrRoomHeatingDataUnavailable", err)
	}
}

func TestRoomHeatingWriteRejectsOutOfRangeValue(t *testing.T) {
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeSetpointDescriptionListData).Return(
		&model.SetpointDescriptionListDataType{SetpointDescriptionData: []model.SetpointDescriptionDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeRoomAirTemperature)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointListData).Return(
		&model.SetpointListDataType{SetpointData: []model.SetpointDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(21)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeSetpointConstraintsListData).Return(
		&model.SetpointConstraintsListDataType{SetpointConstraintsData: []model.SetpointConstraintsDataType{
			{
				SetpointId:       ptr(model.SetpointIdType(1)),
				SetpointRangeMin: model.NewScaledNumberType(5),
				SetpointRangeMax: model.NewScaledNumberType(30),
				SetpointStepSize: model.NewScaledNumberType(0.5),
			},
		}},
	)
	operation := mocks.NewOperationsInterface(t)
	operation.On("Write").Return(true)
	feature.On("Operations").Return(map[model.FunctionType]spineapi.OperationsInterface{
		model.FunctionTypeSetpointListData: operation,
	})
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).Return(feature)

	err := (&RoomHeatingTemperature{}).Write(context.Background(), entity, 35)
	if !errors.Is(err, ErrRoomHeatingOutOfRange) {
		t.Fatalf("Write() error = %v, want ErrRoomHeatingOutOfRange", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd eebus-bridge && go test ./internal/usecases/ -run TestRoomHeating -v`
Expected: FAIL — `RoomHeatingTemperature` undefined.

- [ ] **Step 3: Implement `roomheatingtemp.go`**

Copy `eebus-bridge/internal/usecases/dhw.go` verbatim to `roomheatingtemp.go` and apply these substitutions (keep every other line, including the write-timeout/response-callback/full-list-copy logic, byte-for-byte identical):

- Package-level consts/vars: `dhwUseCaseSupportUpdate` → `roomHeatingUseCaseSupportUpdate` (value `"bridge-room-heating-temperature-support-update"`); reuse the existing `dhwWriteTimeout` constant (already defined in `dhw.go`, same package) — do not redeclare it.
- Errors: `ErrDHWDataUnavailable` → `ErrRoomHeatingDataUnavailable` ("room heating setpoint data unavailable"), `ErrDHWNotWritable` → `ErrRoomHeatingNotWritable`, `ErrDHWOutOfRange` → `ErrRoomHeatingOutOfRange`, `ErrDHWInvalidStep` → `ErrRoomHeatingInvalidStep`.
- Type: `DHWSetpoint` → `RoomHeatingSetpoint`.
- Type/constructor: `DHWTemperature` → `RoomHeatingTemperature`, `NewDHWTemperature` → `NewRoomHeatingTemperature`.
- `model.UseCaseNameTypeConfigurationOfDhwTemperature` → `model.UseCaseNameTypeConfigurationOfRoomHeatingTemperature` (verify the exact constant name first: `go doc github.com/enbility/spine-go/model.UseCaseNameType | grep -i room`).
- `[]model.UseCaseActorType{model.UseCaseActorTypeDHWCircuit}` → `[]model.UseCaseActorType{model.UseCaseActorTypeHVACRoom}` (verify exact constant: `go doc github.com/enbility/spine-go/model.UseCaseActorType | grep -i hvac`).
- `[]model.EntityTypeType{model.EntityTypeTypeDHWCircuit}` → `[]model.EntityTypeType{model.EntityTypeTypeHVACRoom}` (verify: `go doc github.com/enbility/spine-go/model.EntityTypeType | grep -i hvac`).
- `handleUseCaseEvent`: `d.registry.UpsertObservation(ski, device, entity, "dhw_temperature")` → `"room_heating_temperature"`; published event `"dhw.use_case_support_updated"` → `"roomheating.use_case_support_updated"`.
- `HandleEvent`: published event `"dhw.setpoint_updated"` → `"roomheating.setpoint_updated"`.
- Log prefixes `[DHW]` → `[ROOMHEATING]`.
- Helper functions `dhwSetpointID`/`setpointServer`/`setpointValue`/`setpointRange`/`isFinite`: **do not duplicate** `setpointServer`, `setpointValue`, `setpointRange`, `isFinite` — they already exist package-wide from `dhw.go` and are generic (take a `model.SetpointIdType`, not DHW-specific). Only add a new `roomHeatingSetpointID` function analogous to `dhwSetpointID` but filtering `model.ScopeTypeTypeRoomAirTemperature` instead of `model.ScopeTypeTypeDhwTemperature`.
- `localSetpointFeature`: rename to `localRoomHeatingSetpointFeature` to avoid a duplicate method name collision on a different receiver type (Go allows same method name on different types, so this rename is optional — keep it named `localSetpointFeature` on `*RoomHeatingTemperature`, no collision since it's a method, not a package func).

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd eebus-bridge && go test ./internal/usecases/ -run TestRoomHeating -v`
Expected: PASS (3 tests).

- [ ] **Step 5: `go vet` and full package test**

Run: `cd eebus-bridge && go vet ./... && go test -race ./internal/usecases/...`
Expected: no vet warnings, all tests pass (existing DHW tests unaffected).

- [ ] **Step 6: Commit**

```bash
git add eebus-bridge/internal/usecases/roomheatingtemp.go eebus-bridge/internal/usecases/roomheatingtemp_test.go
git commit -m "feat: add RoomHeatingTemperature use case (Configuration of Room Heating Temperature)"
```

---

### Task 2: `RoomHeatingSystemFunction` use case (mirrors `dhwsysfn.go`, minus boost)

**Files:**
- Create: `eebus-bridge/internal/usecases/roomheatingsysfn.go`
- Test: `eebus-bridge/internal/usecases/roomheatingsysfn_test.go`

**Interfaces:**
- Consumes: nothing from Task 1 (independent use case, separate `HVAC` feature vs. `Setpoint`).
- Produces: `type RoomHeatingSystemFunctionState struct { OperationMode string; AvailableModes []string; ModeWritable bool }`, `func NewRoomHeatingSystemFunction(localEntity spineapi.EntityLocalInterface, bus *eebus.EventBus, registry *eebus.DeviceRegistry, debug bool) *RoomHeatingSystemFunction`, `func (r *RoomHeatingSystemFunction) UseCase() eebusapi.UseCaseInterface`, `func (r *RoomHeatingSystemFunction) CompatibleEntity(ski string) spineapi.EntityRemoteInterface`, `func (r *RoomHeatingSystemFunction) State(entity spineapi.EntityRemoteInterface) (RoomHeatingSystemFunctionState, error)`, `func (r *RoomHeatingSystemFunction) WriteOperationMode(ctx context.Context, entity spineapi.EntityRemoteInterface, modeType string) error`. Sentinels: `ErrRoomHeatingSysFnDataUnavailable`, `ErrRoomHeatingSysFnNotWritable`, `ErrRoomHeatingSysFnInvalidMode`, `ErrRoomHeatingSysFnRejected`. Bus events: `"roomheatingsysfn.use_case_support_updated"`, `"roomheatingsysfn.updated"`.

- [ ] **Step 1: Write the failing tests**

```go
// eebus-bridge/internal/usecases/roomheatingsysfn_test.go
package usecases

import (
	"context"
	"errors"
	"testing"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
)

func newRoomHeatingSysFnFeature(t *testing.T, currentModeID model.HvacOperationModeIdType, writable bool) *mocks.FeatureRemoteInterface {
	feature := mocks.NewFeatureRemoteInterface(t)
	feature.On("DataCopy", model.FunctionTypeHvacSystemFunctionDescriptionListData).Return(
		&model.HvacSystemFunctionDescriptionListDataType{HvacSystemFunctionDescriptionData: []model.HvacSystemFunctionDescriptionDataType{
			{SystemFunctionId: ptr(model.HvacSystemFunctionIdType(0)), SystemFunctionType: ptr(model.HvacSystemFunctionTypeTypeHeating)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeHvacSystemFunctionListData).Return(
		&model.HvacSystemFunctionListDataType{HvacSystemFunctionData: []model.HvacSystemFunctionDataType{
			{SystemFunctionId: ptr(model.HvacSystemFunctionIdType(0)), CurrentOperationModeId: ptr(currentModeID)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeHvacOperationModeDescriptionListData).Return(
		&model.HvacOperationModeDescriptionListDataType{HvacOperationModeDescriptionData: []model.HvacOperationModeDescriptionDataType{
			{OperationModeId: ptr(model.HvacOperationModeIdType(0)), OperationModeType: ptr(model.HvacOperationModeTypeTypeOff)},
			{OperationModeId: ptr(model.HvacOperationModeIdType(1)), OperationModeType: ptr(model.HvacOperationModeTypeTypeOn)},
			{OperationModeId: ptr(model.HvacOperationModeIdType(2)), OperationModeType: ptr(model.HvacOperationModeTypeTypeAuto)},
		}},
	)
	feature.On("DataCopy", model.FunctionTypeHvacSystemFunctionOperationModeRelationListData).Return(
		&model.HvacSystemFunctionOperationModeRelationListDataType{HvacSystemFunctionOperationModeRelationData: []model.HvacSystemFunctionOperationModeRelationDataType{
			{SystemFunctionId: ptr(model.HvacSystemFunctionIdType(0)), OperationModeId: []model.HvacOperationModeIdType{0, 1, 2}},
		}},
	)
	operation := mocks.NewOperationsInterface(t)
	operation.On("Write").Return(writable)
	feature.On("Operations").Return(map[model.FunctionType]spineapi.OperationsInterface{
		model.FunctionTypeHvacSystemFunctionListData: operation,
	})
	return feature
}

func TestRoomHeatingSysFnStateResolvesCurrentAndAvailableModes(t *testing.T) {
	feature := newRoomHeatingSysFnFeature(t, 0, true)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeHvac, model.RoleTypeServer).Return(feature)

	state, err := (&RoomHeatingSystemFunction{}).State(entity)
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if state.OperationMode != "off" || !state.ModeWritable {
		t.Errorf("State() = %+v", state)
	}
	if len(state.AvailableModes) != 3 {
		t.Errorf("AvailableModes = %v, want 3 entries", state.AvailableModes)
	}
}

func TestRoomHeatingSysFnMissingChangeableFlagDoesNotHideWrite(t *testing.T) {
	// Same VR940 firmware quirk as DHW: a nil IsOperationModeIdChangeable must
	// not block a write the device advertises via Operations().Write()==true.
	feature := newRoomHeatingSysFnFeature(t, 1, true)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeHvac, model.RoleTypeServer).Return(feature)

	state, err := (&RoomHeatingSystemFunction{}).State(entity)
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if !state.ModeWritable {
		t.Errorf("ModeWritable = false, want true (nil flag must not hide advertised write)")
	}
}

func TestRoomHeatingSysFnWriteRejectsModeNotInRelation(t *testing.T) {
	feature := newRoomHeatingSysFnFeature(t, 0, true)
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeHvac, model.RoleTypeServer).Return(feature)

	err := (&RoomHeatingSystemFunction{}).WriteOperationMode(context.Background(), entity, "cool")
	if !errors.Is(err, ErrRoomHeatingSysFnInvalidMode) {
		t.Fatalf("WriteOperationMode() error = %v, want ErrRoomHeatingSysFnInvalidMode", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd eebus-bridge && go test ./internal/usecases/ -run TestRoomHeatingSysFn -v`
Expected: FAIL — `RoomHeatingSystemFunction` undefined.

- [ ] **Step 3: Implement `roomheatingsysfn.go`**

Copy `eebus-bridge/internal/usecases/dhwsysfn.go` to `roomheatingsysfn.go` and apply:

- Drop everything boost/overrun-related entirely: no `BoostStatus`/`BoostWritable` fields, no `WriteBoost` method, no `oneTimeDHWOverrunID`/`hvacOverrun`/`resolvedDHWSysFn.overrun*` fields, no `model.FunctionTypeHvacOverrunDescriptionListData`/`HvacOverrunListData` in `Refresh`'s function list, no `HvacOverrunDescriptionListDataType`/`HvacOverrunListDataType` cases in `HandleEvent`'s switch. Room heating has no overrun/boost concept in the spec evidence.
- Const/errors: `dhwSysFnUseCaseSupportUpdate` → `roomHeatingSysFnUseCaseSupportUpdate` (`"bridge-room-heating-system-function-support-update"`); `ErrDHWSysFnDataUnavailable` → `ErrRoomHeatingSysFnDataUnavailable`, `ErrDHWSysFnNotWritable` → `ErrRoomHeatingSysFnNotWritable`, `ErrDHWSysFnInvalidMode` → `ErrRoomHeatingSysFnInvalidMode`, `ErrDHWSysFnRejected` → `ErrRoomHeatingSysFnRejected`. Drop `ErrDHWSysFnNotWritable`'s boost-specific twin if any remain unused.
- Type: `DHWSystemFunctionState` → `RoomHeatingSystemFunctionState` (only `OperationMode`, `AvailableModes`, `ModeWritable` fields — no boost fields).
- Type/constructor: `DHWSystemFunction` → `RoomHeatingSystemFunction`, `NewDHWSystemFunction` → `NewRoomHeatingSystemFunction`.
- `model.UseCaseNameTypeConfigurationOfDhwSystemFunction` → `model.UseCaseNameTypeConfigurationOfRoomHeatingSystemFunction` (verify exact constant name via `go doc`).
- Actor/entity types: `model.UseCaseActorTypeDHWCircuit` → `model.UseCaseActorTypeHVACRoom`, `model.EntityTypeTypeDHWCircuit` → `model.EntityTypeTypeHVACRoom` (same verification as Task 1).
- Scenario list: DHW registers 3 scenarios (1 mandatory + 2 optional for boost variants). Room heating only needs **scenario 1** (`Mandatory: true`) — drop the 2 optional scenario entries entirely, since there's no boost/overrun scenario to support.
- `dhwSystemFunctionID` → `roomHeatingSystemFunctionID`, filtering `model.HvacSystemFunctionTypeTypeHeating` instead of `model.HvacSystemFunctionTypeTypeDhw`.
- `resolveDHWSystemFunction` → `resolveRoomHeatingSystemFunction`: drop the `overrunID`/`overrun` resolution steps entirely (no `oneTimeDHWOverrunID`/`hvacOverrun` calls); the returned struct only needs `system`, `systemID`, `currentModeType`, `availableModeTypes`, `modeIDForType`.
- `State`: drop `BoostStatus`/`BoostWritable` fields from the returned struct; keep the `ModeWritable` computation identical (`systemOp != nil && systemOp.Write() && boolPtrNotFalse(resolved.system.IsOperationModeIdChangeable)`).
- Registry label: `"dhw_system_function"` → `"room_heating_system_function"`.
- Events: `"dhwsysfn.use_case_support_updated"` → `"roomheatingsysfn.use_case_support_updated"`, `"dhwsysfn.updated"` → `"roomheatingsysfn.updated"`.
- `localHvacFeature`, `hvacServer`, `operationModesForSystem`, `operationModeType`, `hvacSystemFunction`, `containsSystemFunction`, `boolPtrNotFalse` are package-generic (already defined by `dhwsysfn.go`) — **do not redeclare**; reuse them directly.
- Log prefix `[DHWSYSFN]` → `[ROOMHEATINGSYSFN]`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd eebus-bridge && go test ./internal/usecases/ -run TestRoomHeatingSysFn -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Full package check**

Run: `cd eebus-bridge && go vet ./... && go test -race ./internal/usecases/...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add eebus-bridge/internal/usecases/roomheatingsysfn.go eebus-bridge/internal/usecases/roomheatingsysfn_test.go
git commit -m "feat: add RoomHeatingSystemFunction use case (Configuration of Room Heating System Function)"
```

---

### Task 3: Typed flow/return temperature reader

**Files:**
- Create: `eebus-bridge/internal/usecases/hydraulictemp.go`
- Test: `eebus-bridge/internal/usecases/hydraulictemp_test.go`

**Interfaces:**
- Consumes: `eebus.DeviceRegistry.Entities(ski) []eebus.EntityInfo` (existing, already used by `MonitoringWrapper.GenericMeasurements`), `spineapi.EntityRemoteInterface.FeatureOfTypeAndRole(model.FeatureTypeTypeMeasurement, model.RoleTypeServer)`.
- Produces: `func NewHydraulicTemperatures(bus *eebus.EventBus, registry *eebus.DeviceRegistry, debug bool) *HydraulicTemperatures`, `func (h *HydraulicTemperatures) Setup(localEntity spineapi.EntityLocalInterface)`, `func (h *HydraulicTemperatures) FlowTemperature(ski string) (float64, error)`, `func (h *HydraulicTemperatures) ReturnTemperature(ski string) (float64, error)`. Two tiny adapters implementing the existing `temperatureReader` interface from `internal/grpc/monitoring_service.go` (`Temperature(string) (float64, error)`): `type FlowTemperatureReader struct{ *HydraulicTemperatures }` with `func (r FlowTemperatureReader) Temperature(ski string) (float64, error) { return r.FlowTemperature(ski) }`, and `ReturnTemperatureReader` analogously. Bus events: `"monitoring.flow_temperature_updated"`, `"monitoring.return_temperature_updated"`.

This does **not** register a new EEBUS use case — it piggybacks on the `HeatPumpAppliance` entity already registered in `DeviceRegistry` by the existing `MonitoringWrapper` (MPC), reading its `Measurement/server` feature directly via raw SPINE calls (same low-level style as `dhw.go`, not a `usecase.UseCaseBase`). Subscribes to the local device's raw event bus (`localEntity.Device().Events().Subscribe(h)`) the same way `DHWTemperature`/`DHWSystemFunction` do, to receive `MeasurementListDataType` cache-update notifications independent of `mampc`'s own semantic event filtering.

- [ ] **Step 1: Write the failing tests**

```go
// eebus-bridge/internal/usecases/hydraulictemp_test.go
package usecases

import (
	"testing"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

func TestHydraulicTemperaturesPrefersHeatPumpApplianceOnAmbiguity(t *testing.T) {
	flowFeature := mocks.NewFeatureRemoteInterface(t)
	flowFeature.On("DataCopy", model.FunctionTypeMeasurementDescriptionListData).Return(
		&model.MeasurementDescriptionListDataType{MeasurementDescriptionData: []model.MeasurementDescriptionDataType{
			{MeasurementId: ptr(model.MeasurementIdType(1)), ScopeType: ptr(model.ScopeTypeTypeFlowTemperature), Unit: ptr(model.UnitOfMeasurementTypedegC)},
		}},
	)
	flowFeature.On("DataCopy", model.FunctionTypeMeasurementListData).Return(
		&model.MeasurementListDataType{MeasurementData: []model.MeasurementDataType{
			{MeasurementId: ptr(model.MeasurementIdType(1)), Value: model.NewScaledNumberType(52.5)},
		}},
	)

	heatPumpEntity := mocks.NewEntityRemoteInterface(t)
	heatPumpEntity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeMeasurement, model.RoleTypeServer).Return(flowFeature)

	registry := eebus.NewDeviceRegistry()
	registry.AddDevice("test-ski", eebus.DeviceInfo{SKI: "test-ski"})
	registry.UpsertObservation("test-ski", nil, heatPumpEntity, "monitoring")

	h := NewHydraulicTemperatures(nil, registry, false)
	value, err := h.FlowTemperature("test-ski")
	if err != nil {
		t.Fatalf("FlowTemperature() error = %v", err)
	}
	if value != 52.5 {
		t.Errorf("FlowTemperature() = %v, want 52.5", value)
	}
}

func TestHydraulicTemperaturesReturnUnavailableWithoutScope(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	h := NewHydraulicTemperatures(nil, registry, false)
	if _, err := h.ReturnTemperature("unknown-ski"); err == nil {
		t.Fatal("ReturnTemperature() error = nil, want an error for an unknown SKI")
	}
}
```

Note: adjust the `registry.UpsertObservation`/`mocks.NewEntityRemoteInterface` call shape to match the real `eebus.EntityInfo`/`DeviceRegistry` signatures found in `internal/eebus/registry.go` (read that file first — the exact parameter list for `UpsertObservation` is `(ski string, device spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, useCase string)`; a `nil` `spineapi.DeviceRemoteInterface` is acceptable for this test since `entity.Device()` is never called by the reader logic below, only `entity.FeatureOfTypeAndRole`).

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd eebus-bridge && go test ./internal/usecases/ -run TestHydraulicTemperatures -v`
Expected: FAIL — `NewHydraulicTemperatures` undefined.

- [ ] **Step 3: Implement `hydraulictemp.go`**

```go
package usecases

import (
	"errors"
	"log"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

var ErrHydraulicTemperatureUnavailable = errors.New("hydraulic temperature data unavailable")

// HydraulicTemperatures reads flow/return temperature from the standardized
// Measurement/server feature already exposed by the entity the generic
// MonitoringWrapper (MPC) negotiates — no dedicated EEBUS use case exists for
// this, so no new use-case registration happens here.
type HydraulicTemperatures struct {
	bus         *eebus.EventBus
	registry    *eebus.DeviceRegistry
	localEntity spineapi.EntityLocalInterface
	debug       bool
}

func NewHydraulicTemperatures(bus *eebus.EventBus, registry *eebus.DeviceRegistry, debug bool) *HydraulicTemperatures {
	return &HydraulicTemperatures{bus: bus, registry: registry, debug: debug}
}

// Setup subscribes to the local device's raw event bus so cache updates on any
// entity's Measurement/server feature are observed, independent of mampc's own
// semantic event filtering (mirrors DHWTemperature's raw-subscribe pattern).
func (h *HydraulicTemperatures) Setup(localEntity spineapi.EntityLocalInterface) {
	if localEntity == nil {
		return
	}
	h.localEntity = localEntity
	_ = localEntity.Device().Events().Subscribe(h)
}

// HandleEvent reacts to Measurement cache updates and republishes flow/return
// changes as dedicated bridge events.
func (h *HydraulicTemperatures) HandleEvent(payload spineapi.EventPayload) {
	if payload.Entity == nil || payload.EventType != spineapi.EventTypeDataChange ||
		payload.ChangeType != spineapi.ElementChangeUpdate {
		return
	}
	if _, ok := payload.Data.(*model.MeasurementListDataType); !ok {
		return
	}
	if h.registry == nil || h.bus == nil {
		return
	}
	ski := eebus.NormalizeSKI(payload.Ski)
	if value, err := h.FlowTemperature(ski); err == nil {
		_ = value
		h.bus.Publish(eebus.Event{SKI: ski, Type: "monitoring.flow_temperature_updated"})
	}
	if value, err := h.ReturnTemperature(ski); err == nil {
		_ = value
		h.bus.Publish(eebus.Event{SKI: ski, Type: "monitoring.return_temperature_updated"})
	}
}

// FlowTemperature returns the flowTemperature-scoped measurement in Celsius.
func (h *HydraulicTemperatures) FlowTemperature(ski string) (float64, error) {
	return h.scopedTemperature(ski, model.ScopeTypeTypeFlowTemperature)
}

// ReturnTemperature returns the returnTemperature-scoped measurement in Celsius.
func (h *HydraulicTemperatures) ReturnTemperature(ski string) (float64, error) {
	return h.scopedTemperature(ski, model.ScopeTypeTypeReturnTemperature)
}

func (h *HydraulicTemperatures) scopedTemperature(ski string, scope model.ScopeTypeType) (float64, error) {
	if h.registry == nil {
		return 0, ErrHydraulicTemperatureUnavailable
	}
	entities := h.registry.Entities(ski)
	var preferred spineapi.EntityRemoteInterface
	var fallback spineapi.EntityRemoteInterface
	for _, info := range entities {
		if info.Entity == nil {
			continue
		}
		feature := info.Entity.FeatureOfTypeAndRole(model.FeatureTypeTypeMeasurement, model.RoleTypeServer)
		if feature == nil {
			continue
		}
		if _, id, ok := measurementIDForScope(feature, scope); ok {
			_ = id
			if info.Type == "HeatPumpAppliance" {
				preferred = info.Entity
				break
			}
			if fallback == nil {
				fallback = info.Entity
			}
		}
	}
	entity := preferred
	if entity == nil {
		entity = fallback
	}
	if entity == nil {
		return 0, ErrHydraulicTemperatureUnavailable
	}
	feature := entity.FeatureOfTypeAndRole(model.FeatureTypeTypeMeasurement, model.RoleTypeServer)
	unit, id, ok := measurementIDForScope(feature, scope)
	if !ok {
		return 0, ErrHydraulicTemperatureUnavailable
	}
	value, ok := measurementValue(feature, id)
	if !ok {
		return 0, ErrHydraulicTemperatureUnavailable
	}
	return convertToCelsius(value, unit)
}

func measurementIDForScope(feature spineapi.FeatureRemoteInterface, scope model.ScopeTypeType) (model.UnitOfMeasurementType, model.MeasurementIdType, bool) {
	data, ok := feature.DataCopy(model.FunctionTypeMeasurementDescriptionListData).(*model.MeasurementDescriptionListDataType)
	if !ok || data == nil {
		return "", 0, false
	}
	for _, description := range data.MeasurementDescriptionData {
		if description.MeasurementId != nil && description.ScopeType != nil && *description.ScopeType == scope {
			unit := model.UnitOfMeasurementType("")
			if description.Unit != nil {
				unit = *description.Unit
			}
			return unit, *description.MeasurementId, true
		}
	}
	return "", 0, false
}

func measurementValue(feature spineapi.FeatureRemoteInterface, id model.MeasurementIdType) (float64, bool) {
	data, ok := feature.DataCopy(model.FunctionTypeMeasurementListData).(*model.MeasurementListDataType)
	if !ok || data == nil {
		return 0, false
	}
	for _, entry := range data.MeasurementData {
		if entry.MeasurementId != nil && *entry.MeasurementId == id && entry.Value != nil {
			if entry.ValueState != nil && *entry.ValueState != model.MeasurementValueStateTypeNormal {
				return 0, false
			}
			return entry.Value.GetValue(), true
		}
	}
	return 0, false
}

func convertToCelsius(value float64, unit model.UnitOfMeasurementType) (float64, error) {
	switch unit {
	case model.UnitOfMeasurementTypedegC, "":
		return value, nil
	case model.UnitOfMeasurementTypedegF:
		return (value - 32) / 1.8, nil
	case model.UnitOfMeasurementTypeK:
		return value - 273.15, nil
	default:
		return 0, ErrHydraulicTemperatureUnavailable
	}
}

// FlowTemperatureReader adapts HydraulicTemperatures to the temperatureReader
// interface used by internal/grpc.MonitoringService.
type FlowTemperatureReader struct{ *HydraulicTemperatures }

func (r FlowTemperatureReader) Temperature(ski string) (float64, error) { return r.FlowTemperature(ski) }

// ReturnTemperatureReader adapts HydraulicTemperatures to the temperatureReader
// interface used by internal/grpc.MonitoringService.
type ReturnTemperatureReader struct{ *HydraulicTemperatures }

func (r ReturnTemperatureReader) Temperature(ski string) (float64, error) {
	return r.ReturnTemperature(ski)
}

var _ = log.Printf // keep log import if debug logging is added during review
```

Before compiling: run `go doc github.com/enbility/spine-go/model.UnitOfMeasurementType | grep -iE "degc|degf|typek"` to confirm the exact Kelvin/Fahrenheit constant names (used elsewhere in the codebase already for DHW/room/outdoor Celsius normalization — check `internal/usecases/dhwmonitoring.go` or `roommonitoring.go` for the established Celsius-conversion helper and **reuse it instead of redefining `convertToCelsius`** if one already exists package-wide; grep first: `grep -n "func convert.*Celsius\|degF\|UnitOfMeasurementTypeK" eebus-bridge/internal/usecases/*.go`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd eebus-bridge && go test ./internal/usecases/ -run TestHydraulicTemperatures -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Full package check**

Run: `cd eebus-bridge && go vet ./... && go test -race ./internal/usecases/...`
Expected: clean. Remove the placeholder `var _ = log.Printf` line and the unused `log` import if no debug logging was added.

- [ ] **Step 6: Commit**

```bash
git add eebus-bridge/internal/usecases/hydraulictemp.go eebus-bridge/internal/usecases/hydraulictemp_test.go
git commit -m "feat: add typed flow/return temperature reader (HydraulicTemperatures)"
```

---

### Task 4: `hvac_service.proto` + `HVACService` gRPC handler + monitoring proto extension

**Files:**
- Create: `eebus-bridge/proto/eebus/v1/hvac_service.proto`
- Create: `eebus-bridge/internal/grpc/hvac_service.go`
- Test: `eebus-bridge/internal/grpc/hvac_service_test.go`
- Modify: `eebus-bridge/proto/eebus/v1/monitoring_service.proto`
- Modify: `eebus-bridge/internal/grpc/monitoring_service.go` (add `flow`/`return_` `temperatureReader` fields, wire the 2 new event types)
- Modify: `eebus-bridge/internal/grpc/monitoring_service_test.go`
- Modify: `custom_components/eebus/generate_proto.sh` (add `eebus/v1/hvac_service.proto` to the `protoc` file list)

**Interfaces:**
- Consumes: `usecases.RoomHeatingTemperature` (Task 1), `usecases.RoomHeatingSystemFunction` (Task 2), `usecases.RoomMonitoringWrapper.Temperature(ski) (float64, error)` (existing, PR #100), `usecases.FlowTemperatureReader`/`ReturnTemperatureReader` (Task 3).
- Produces: `pb.HVACService` gRPC service (`GetRoomHeating`, `SetRoomHeatingTemperature`, `SetRoomHeatingMode`, `SubscribeRoomHeatingEvents`), `bridgegrpc.NewHVACService(temp roomHeatingTempController, sysfn roomHeatingSysFnController, room roomTemperatureReader, bus *eebus.EventBus) *HVACService`.

- [ ] **Step 1: Write `hvac_service.proto`**

```protobuf
syntax = "proto3";

package eebus.v1;

option go_package = "github.com/volschin/eebus-bridge/gen/proto/eebus/v1;eebusv1";

import "eebus/v1/common.proto";

// HVACService exposes the room-heating Configuration use cases (Setpoint +
// HVAC System Function) validated against the Vaillant VR940's HVACRoom
// entity. Values and constraints originate from that remote entity's Setpoint
// and HVAC servers; current_temperature_celsius is sourced from the existing
// MonitoringOfRoomTemperature (MRT) path (PR #100), not re-read here.
service HVACService {
  rpc GetRoomHeating(DeviceRequest) returns (RoomHeatingState);
  rpc SetRoomHeatingTemperature(SetRoomHeatingTemperatureRequest) returns (Empty);
  rpc SetRoomHeatingMode(SetRoomHeatingModeRequest) returns (Empty);
  rpc SubscribeRoomHeatingEvents(DeviceRequest) returns (stream RoomHeatingEvent);
}

message RoomHeatingState {
  optional double current_temperature_celsius = 1;
  RoomHeatingSetpoint setpoint = 2;
  RoomHeatingSystemFunction system_function = 3;
}

message RoomHeatingSetpoint {
  double value_celsius = 1;
  double min_celsius = 2;
  double max_celsius = 3;
  double step_celsius = 4;
  bool writable = 5;
}

message RoomHeatingSystemFunction {
  string operation_mode = 1;
  repeated string available_modes = 2;
  bool mode_writable = 3;
}

message SetRoomHeatingTemperatureRequest {
  string ski = 1;
  double value_celsius = 2;
}

message SetRoomHeatingModeRequest {
  string ski = 1;
  string mode = 2;
}

enum RoomHeatingEventType {
  ROOM_HEATING_EVENT_UNSPECIFIED = 0;
  ROOM_HEATING_EVENT_SUPPORT_UPDATED = 1;
  ROOM_HEATING_EVENT_CURRENT_TEMPERATURE_UPDATED = 2;
  ROOM_HEATING_EVENT_SETPOINT_UPDATED = 3;
  ROOM_HEATING_EVENT_SYSTEM_FUNCTION_UPDATED = 4;
}

message RoomHeatingEvent {
  string ski = 1;
  RoomHeatingEventType event_type = 2;
  RoomHeatingState state = 3;
}
```

- [ ] **Step 2: Extend `monitoring_service.proto`**

Append two values to the existing `MeasurementEventType` enum (append-only, per spec §7 — do not renumber existing values):

```protobuf
  MEASUREMENT_EVENT_FLOW_TEMPERATURE_UPDATED = 9;
  MEASUREMENT_EVENT_RETURN_TEMPERATURE_UPDATED = 10;
```

- [ ] **Step 3: Regenerate both stub sets**

```bash
cd eebus-bridge && PATH="$HOME/go/bin:$PATH" buf generate
cd ..
# add "eebus/v1/hvac_service.proto" to the protoc file list in generate_proto.sh
# then, using an ephemeral venv (system Python is externally-managed):
uv venv -p 3.13 /tmp/proto-venv -q
source /tmp/proto-venv/bin/activate
uv pip install -q grpcio-tools==1.78.0
bash generate_proto.sh
deactivate
```

Verify: `git status --short eebus-bridge/gen/proto custom_components/eebus/generated` shows the new `hvac_service_pb2.py`/`hvac_service_pb2_grpc.py`/`hvac_service_pb2.pyi` and `hvac_service.pb.go`/`hvac_service_grpc.pb.go`, plus `monitoring_service.pb.go`/`monitoring_service_pb2.py` diffs for the 2 new enum values only (a `protoc (unknown)` vs. a pinned version string diff in the Go header comment is expected noise from a missing local `protoc` binary — ignore it, or install `protoc` to match).

- [ ] **Step 4: Write `internal/grpc/hvac_service_test.go`** — the failing test

```go
package grpc

import (
	"context"
	"errors"
	"testing"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeRoomHeatingTemp struct {
	entity spineapi.EntityRemoteInterface
	state  usecases.RoomHeatingSetpoint
	err    error
}

func (f *fakeRoomHeatingTemp) CompatibleEntity(string) spineapi.EntityRemoteInterface { return f.entity }
func (f *fakeRoomHeatingTemp) State(spineapi.EntityRemoteInterface) (usecases.RoomHeatingSetpoint, error) {
	return f.state, f.err
}
func (f *fakeRoomHeatingTemp) Write(context.Context, spineapi.EntityRemoteInterface, float64) error {
	return f.err
}

func TestHVACServiceGetRoomHeatingReturnsNotFoundWithoutCompatibleEntity(t *testing.T) {
	svc := NewHVACService(&fakeRoomHeatingTemp{entity: nil}, nil, nil, nil)
	_, err := svc.GetRoomHeating(context.Background(), &pb.DeviceRequest{Ski: "missing"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("GetRoomHeating() error = %v, want NotFound", err)
	}
}

func TestHVACServiceGetRoomHeatingReturnsSetpointAndSystemFunction(t *testing.T) {
	entity := mocks.NewEntityRemoteInterface(t)
	temp := &fakeRoomHeatingTemp{
		entity: entity,
		state:  usecases.RoomHeatingSetpoint{Value: 21, Minimum: 5, Maximum: 30, Step: 0.5, Writable: true},
	}
	state, err := NewHVACService(temp, nil, nil, nil).GetRoomHeating(context.Background(), &pb.DeviceRequest{Ski: "test"})
	if err != nil {
		t.Fatalf("GetRoomHeating() error = %v", err)
	}
	if state.Setpoint == nil || state.Setpoint.ValueCelsius != 21 {
		t.Errorf("GetRoomHeating() = %+v", state)
	}
}

func TestHVACServiceSetRoomHeatingTemperatureRequiresSKI(t *testing.T) {
	svc := NewHVACService(&fakeRoomHeatingTemp{}, nil, nil, nil)
	_, err := svc.SetRoomHeatingTemperature(context.Background(), &pb.SetRoomHeatingTemperatureRequest{ValueCelsius: 21})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SetRoomHeatingTemperature() error = %v, want InvalidArgument", err)
	}
}

func TestHVACServiceMapsOutOfRangeToInvalidArgument(t *testing.T) {
	entity := mocks.NewEntityRemoteInterface(t)
	temp := &fakeRoomHeatingTemp{entity: entity, err: usecases.ErrRoomHeatingOutOfRange}
	svc := NewHVACService(temp, nil, nil, nil)
	_, err := svc.SetRoomHeatingTemperature(context.Background(), &pb.SetRoomHeatingTemperatureRequest{Ski: "test", ValueCelsius: 99})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SetRoomHeatingTemperature() error = %v, want InvalidArgument", err)
	}
}

var _ = errors.New // keep errors import if mapRoomHeatingError needs errors.Is
```

- [ ] **Step 5: Run tests to verify they fail**

Run: `cd eebus-bridge && go test ./internal/grpc/ -run TestHVACService -v`
Expected: FAIL — `NewHVACService` undefined.

- [ ] **Step 6: Implement `internal/grpc/hvac_service.go`**

Mirror `internal/grpc/dhw_service.go`'s exact shape:

```go
package grpc

import (
	"context"
	"errors"

	spineapi "github.com/enbility/spine-go/api"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type roomHeatingTempController interface {
	CompatibleEntity(string) spineapi.EntityRemoteInterface
	State(spineapi.EntityRemoteInterface) (usecases.RoomHeatingSetpoint, error)
	Write(context.Context, spineapi.EntityRemoteInterface, float64) error
}

type roomHeatingSysFnController interface {
	CompatibleEntity(string) spineapi.EntityRemoteInterface
	State(spineapi.EntityRemoteInterface) (usecases.RoomHeatingSystemFunctionState, error)
	WriteOperationMode(context.Context, spineapi.EntityRemoteInterface, string) error
}

// HVACService exposes the room-heating Configuration use cases over gRPC.
type HVACService struct {
	pb.UnimplementedHVACServiceServer
	temp   roomHeatingTempController
	sysfn  roomHeatingSysFnController
	room   temperatureReader
	bus    *eebus.EventBus
}

func NewHVACService(
	temp roomHeatingTempController,
	sysfn roomHeatingSysFnController,
	room temperatureReader,
	bus *eebus.EventBus,
) *HVACService {
	return &HVACService{temp: temp, sysfn: sysfn, room: room, bus: bus}
}

func (s *HVACService) GetRoomHeating(_ context.Context, req *pb.DeviceRequest) (*pb.RoomHeatingState, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	state := &pb.RoomHeatingState{}
	if s.room != nil {
		if value, err := s.room.Temperature(req.Ski); err == nil {
			state.CurrentTemperatureCelsius = &value
		}
	}
	if s.temp != nil {
		entity := s.temp.CompatibleEntity(req.Ski)
		if entity == nil && s.sysfn == nil {
			return nil, status.Errorf(codes.NotFound, "no compatible HVACRoom found for ski %s", req.Ski)
		}
		if entity != nil {
			if setpoint, err := s.temp.State(entity); err == nil {
				state.Setpoint = convertRoomHeatingSetpoint(setpoint)
			}
		}
	}
	if s.sysfn != nil {
		entity := s.sysfn.CompatibleEntity(req.Ski)
		if entity != nil {
			if sysfn, err := s.sysfn.State(entity); err == nil {
				state.SystemFunction = convertRoomHeatingSystemFunction(sysfn)
			}
		} else if state.Setpoint == nil && state.CurrentTemperatureCelsius == nil {
			return nil, status.Errorf(codes.NotFound, "no compatible HVACRoom found for ski %s", req.Ski)
		}
	}
	return state, nil
}

func (s *HVACService) SetRoomHeatingTemperature(ctx context.Context, req *pb.SetRoomHeatingTemperatureRequest) (*pb.Empty, error) {
	if req == nil || req.Ski == "" {
		return nil, status.Error(codes.InvalidArgument, "ski is required for write operations")
	}
	if s.temp == nil {
		return nil, status.Error(codes.Unavailable, "room heating temperature use case not initialized")
	}
	entity := s.temp.CompatibleEntity(req.Ski)
	if entity == nil {
		return nil, status.Errorf(codes.NotFound, "no compatible HVACRoom found for ski %s", req.Ski)
	}
	if err := s.temp.Write(ctx, entity, req.ValueCelsius); err != nil {
		return nil, mapRoomHeatingError("writing room heating setpoint", err)
	}
	return &pb.Empty{}, nil
}

func (s *HVACService) SetRoomHeatingMode(ctx context.Context, req *pb.SetRoomHeatingModeRequest) (*pb.Empty, error) {
	if req == nil || req.Ski == "" {
		return nil, status.Error(codes.InvalidArgument, "ski is required for write operations")
	}
	if s.sysfn == nil {
		return nil, status.Error(codes.Unavailable, "room heating system function use case not initialized")
	}
	entity := s.sysfn.CompatibleEntity(req.Ski)
	if entity == nil {
		return nil, status.Errorf(codes.NotFound, "no compatible HVACRoom found for ski %s", req.Ski)
	}
	if err := s.sysfn.WriteOperationMode(ctx, entity, req.Mode); err != nil {
		return nil, mapRoomHeatingError("writing room heating mode", err)
	}
	return &pb.Empty{}, nil
}

func (s *HVACService) SubscribeRoomHeatingEvents(req *pb.DeviceRequest, stream pb.HVACService_SubscribeRoomHeatingEventsServer) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "request is required")
	}
	ch := s.bus.Subscribe()
	defer s.bus.Unsubscribe(ch)

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return nil
			}
			if req.Ski != "" && eebus.NormalizeSKI(evt.SKI) != eebus.NormalizeSKI(req.Ski) {
				continue
			}
			var eventType pb.RoomHeatingEventType
			switch evt.Type {
			case "roomheating.use_case_support_updated", "roomheatingsysfn.use_case_support_updated":
				eventType = pb.RoomHeatingEventType_ROOM_HEATING_EVENT_SUPPORT_UPDATED
			case "room.temperature_updated":
				eventType = pb.RoomHeatingEventType_ROOM_HEATING_EVENT_CURRENT_TEMPERATURE_UPDATED
			case "roomheating.setpoint_updated":
				eventType = pb.RoomHeatingEventType_ROOM_HEATING_EVENT_SETPOINT_UPDATED
			case "roomheatingsysfn.updated":
				eventType = pb.RoomHeatingEventType_ROOM_HEATING_EVENT_SYSTEM_FUNCTION_UPDATED
			default:
				continue
			}
			event := &pb.RoomHeatingEvent{Ski: evt.SKI, EventType: eventType}
			if state, err := s.GetRoomHeating(stream.Context(), &pb.DeviceRequest{Ski: evt.SKI}); err == nil {
				event.State = state
			}
			if err := stream.Send(event); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

func convertRoomHeatingSetpoint(state usecases.RoomHeatingSetpoint) *pb.RoomHeatingSetpoint {
	return &pb.RoomHeatingSetpoint{
		ValueCelsius: state.Value,
		MinCelsius:   state.Minimum,
		MaxCelsius:   state.Maximum,
		StepCelsius:  state.Step,
		Writable:     state.Writable,
	}
}

func convertRoomHeatingSystemFunction(state usecases.RoomHeatingSystemFunctionState) *pb.RoomHeatingSystemFunction {
	return &pb.RoomHeatingSystemFunction{
		OperationMode:  state.OperationMode,
		AvailableModes: state.AvailableModes,
		ModeWritable:   state.ModeWritable,
	}
}

func mapRoomHeatingError(action string, err error) error {
	switch {
	case errors.Is(err, usecases.ErrRoomHeatingOutOfRange), errors.Is(err, usecases.ErrRoomHeatingInvalidStep),
		errors.Is(err, usecases.ErrRoomHeatingSysFnInvalidMode):
		return status.Errorf(codes.InvalidArgument, "%s: %v", action, err)
	case errors.Is(err, usecases.ErrRoomHeatingNotWritable), errors.Is(err, usecases.ErrRoomHeatingSysFnNotWritable),
		errors.Is(err, usecases.ErrRoomHeatingSysFnRejected):
		return status.Errorf(codes.FailedPrecondition, "%s: %v", action, err)
	case errors.Is(err, usecases.ErrRoomHeatingDataUnavailable), errors.Is(err, usecases.ErrRoomHeatingSysFnDataUnavailable):
		return status.Errorf(codes.NotFound, "%s: %v", action, err)
	case errors.Is(err, context.Canceled):
		return status.Errorf(codes.Canceled, "%s: %v", action, err)
	case errors.Is(err, context.DeadlineExceeded):
		return status.Errorf(codes.DeadlineExceeded, "%s: %v", action, err)
	default:
		return status.Errorf(codes.Internal, "%s: %v", action, err)
	}
}
```

Adjust `GetRoomHeating`'s not-found logic once real compile errors from `go vet`/`go build` clarify edge cases (e.g. `s.temp`/`s.sysfn` both nil in a unit test) — the intent is: if neither the temp nor system-function use case can resolve a compatible entity, return `NotFound`; if one resolves but the other doesn't (partial hardware capability), return whatever succeeded.

- [ ] **Step 7: Wire flow/return into `monitoring_service.go`**

Add two new constructor parameters (`flow`, `returnTemp temperatureReader`) to `NewMonitoringService` in `internal/grpc/monitoring_service.go`, following the exact same pattern PR #100 used for `room`/`outdoor`: add fields to the `MonitoringService` struct, append to `GetMeasurements`'s reader loop (`if s.flow != nil { ... appendMeasurement(&measurements, now, "flow_temperature", value, "degC") }`, same for `return_temperature`), and add two new `case` branches in `SubscribeMeasurements`'s switch and `attachMeasurementPayload`'s switch for `MEASUREMENT_EVENT_FLOW_TEMPERATURE_UPDATED`/`MEASUREMENT_EVENT_RETURN_TEMPERATURE_UPDATED`, calling `s.attachTemperaturePayload(event, ski, s.flow, "flow_temperature")` / `s.attachTemperaturePayload(event, ski, s.returnTemp, "return_temperature")` (the existing helper from PR #100 already handles the nil-check and payload construction).

- [ ] **Step 8: Update `internal/grpc/monitoring_service_test.go`**

Add test cases mirroring the existing room/outdoor coverage: `TestMonitoringServiceGetMeasurementsIncludesFlowAndReturnTemperature`, `TestMonitoringServiceSubscribeMeasurementsAttachesFlowTemperaturePayload`, `TestMonitoringServiceSubscribeMeasurementsAttachesReturnTemperaturePayload` — copy the shape of the existing DHW/room temperature test cases in that file, substituting the new event type and reader field.

- [ ] **Step 9: Run tests to verify they pass**

Run: `cd eebus-bridge && go test ./internal/grpc/... -v`
Expected: PASS, including the new `TestHVACService*` and `TestMonitoringService*` cases.

- [ ] **Step 10: Full package check**

Run: `cd eebus-bridge && go vet ./... && go test -race ./...`
Expected: clean.

- [ ] **Step 11: Commit**

```bash
git add eebus-bridge/proto/eebus/v1/hvac_service.proto eebus-bridge/proto/eebus/v1/monitoring_service.proto \
        eebus-bridge/gen/proto custom_components/eebus/generated \
        eebus-bridge/internal/grpc/hvac_service.go eebus-bridge/internal/grpc/hvac_service_test.go \
        eebus-bridge/internal/grpc/monitoring_service.go eebus-bridge/internal/grpc/monitoring_service_test.go \
        generate_proto.sh
git commit -m "feat: add HVACService gRPC contract and typed flow/return measurement events"
```

---

### Task 5: Wire everything into `cmd/eebus-bridge/main.go`

**Files:**
- Modify: `eebus-bridge/cmd/eebus-bridge/main.go`

**Interfaces:**
- Consumes: `usecases.NewRoomHeatingTemperature` (Task 1), `usecases.NewRoomHeatingSystemFunction` (Task 2), `usecases.NewHydraulicTemperatures`/`FlowTemperatureReader`/`ReturnTemperatureReader` (Task 3), `bridgegrpc.NewHVACService` (Task 4).

- [ ] **Step 1: Add construction and `AddUseCase` calls**

After the existing `dhwSystemFunction := usecases.NewDHWSystemFunction(...)` line, add:

```go
	roomHeatingTemperature := usecases.NewRoomHeatingTemperature(localEntity, bus, registry, cfg.Logging.DebugEvents)
	roomHeatingSystemFunction := usecases.NewRoomHeatingSystemFunction(localEntity, bus, registry, cfg.Logging.DebugEvents)
	hydraulicTemperatures := usecases.NewHydraulicTemperatures(bus, registry, cfg.Logging.DebugEvents)
	hydraulicTemperatures.Setup(localEntity)
```

After the existing `if err := bridgeSvc.Service().AddUseCase(dhwSystemFunction.UseCase()); ...` block, add:

```go
	if err := bridgeSvc.Service().AddUseCase(roomHeatingTemperature.UseCase()); err != nil {
		log.Fatalf("adding room heating temperature use case: %v", err)
	}
	if err := bridgeSvc.Service().AddUseCase(roomHeatingSystemFunction.UseCase()); err != nil {
		log.Fatalf("adding room heating system function use case: %v", err)
	}
```

Update the `registeredUseCases` slice literal to append `"RoomHeatingTemperature", "RoomHeatingSystemFunction"` after `"DHWSystemFunction"`.

- [ ] **Step 2: Wire the gRPC service**

After the existing `dhwSvc := bridgegrpc.NewDHWService(dhwTemperature, dhwSystemFunction, bus)` line, add:

```go
	hvacSvc := bridgegrpc.NewHVACService(
		roomHeatingTemperature,
		roomHeatingSystemFunction,
		roomMonitoringWrapper,
		bus,
	)
```

Update `monitoringSvc := bridgegrpc.NewMonitoringService(...)` to pass the two new flow/return readers as additional arguments, after `outdoorMonitoringWrapper`:

```go
	monitoringSvc := bridgegrpc.NewMonitoringService(
		monitoringWrapper,
		dhwMonitoringWrapper,
		roomMonitoringWrapper,
		outdoorMonitoringWrapper,
		usecases.FlowTemperatureReader{HydraulicTemperatures: hydraulicTemperatures},
		usecases.ReturnTemperatureReader{HydraulicTemperatures: hydraulicTemperatures},
		bus,
		registry,
	)
```

After the existing `pb.RegisterDHWServiceServer(grpcSrv.GRPCServer(), dhwSvc)` line, add:

```go
	pb.RegisterHVACServiceServer(grpcSrv.GRPCServer(), hvacSvc)
```

- [ ] **Step 3: Build and smoke-test**

Run: `cd eebus-bridge && go build -o /tmp/eebus-bridge-check ./cmd/eebus-bridge && go vet ./...`
Expected: builds cleanly, no vet warnings. This is a wiring-only change with no dedicated unit test — coverage comes from Tasks 1–4's tests plus the existing `internal/grpc/integration_test.go` (build-tagged `integration`) if it enumerates registered services; check whether it needs a new assertion for `HVACService`.

- [ ] **Step 4: Full local Go check**

Run: `cd eebus-bridge && go vet ./... && go test -race ./...`
Expected: all packages pass.

- [ ] **Step 5: Commit**

```bash
git add eebus-bridge/cmd/eebus-bridge/main.go
git commit -m "feat: register room-heating use cases and HVACService in the bridge"
```

---

### Task 6: HA coordinator — room heating read/write/stream + flow/return event handling

**Files:**
- Modify: `custom_components/eebus/coordinator.py`
- Modify: `custom_components/eebus/tests/test_coordinator.py`

**Interfaces:**
- Consumes: `proto_stubs.hvac_service_stub(channel)`, `proto_stubs.GetRoomHeating`/`SetRoomHeatingTemperatureRequest`/`SetRoomHeatingModeRequest`/`SubscribeRoomHeatingEvents`, `proto_stubs.RoomHeatingEventType`, `proto_stubs.MeasurementEventType.MEASUREMENT_EVENT_FLOW_TEMPERATURE_UPDATED`/`..._RETURN_TEMPERATURE_UPDATED` (generated by Task 4).
- Produces: coordinator data keys `room_heating_setpoint`, `room_heating_system_function`, `room_heating_supported`, `async_set_room_heating_temperature(value_celsius: float) -> None`, `async_set_room_heating_mode(mode: str) -> None`.

- [ ] **Step 1: Write failing coordinator tests**

Add to `custom_components/eebus/tests/test_coordinator.py`, mirroring the existing `test_dhw_setpoint_*`/`test_dhw_system_function_*` cases in that file (read them first for the exact `MagicMock`/`grpc.aio.AioRpcError` fixture style used):

```python
async def test_async_write_room_heating_temperature_calls_set_rpc(hass, mock_channel_factory):
    coordinator = _make_coordinator(hass, mock_channel_factory)
    stub = coordinator._proto_stubs_module.hvac_service_stub.return_value
    await coordinator.async_set_room_heating_temperature(21.5)
    stub.SetRoomHeatingTemperature.assert_awaited_once()
    request = stub.SetRoomHeatingTemperature.call_args.args[0]
    assert request.value_celsius == 21.5


async def test_room_heating_event_pushes_setpoint(hass, mock_channel_factory):
    coordinator = _make_coordinator(hass, mock_channel_factory)
    event = MagicMock()
    event.ski = coordinator.ski
    event.event_type = coordinator._proto_stubs_module.RoomHeatingEventType.ROOM_HEATING_EVENT_SETPOINT_UPDATED
    event.HasField.side_effect = lambda name: name == "state"
    event.state.setpoint.value_celsius = 21.0
    event.state.setpoint.min_celsius = 5.0
    event.state.setpoint.max_celsius = 30.0
    event.state.setpoint.step_celsius = 0.5
    event.state.setpoint.writable = True
    coordinator._handle_room_heating_event(event)
    assert coordinator.data["room_heating_setpoint"]["value_celsius"] == 21.0


async def test_flow_temperature_event_pushes_value(hass, mock_channel_factory):
    coordinator = _make_coordinator(hass, mock_channel_factory)
    event = MagicMock()
    event.ski = coordinator.ski
    event.event_type = coordinator._proto_stubs_module.MeasurementEventType.MEASUREMENT_EVENT_FLOW_TEMPERATURE_UPDATED
    event.HasField.return_value = True
    event.measurement.value = 52.5
    coordinator._handle_measurement_event(event)
    assert coordinator.data["flow_temperature_c"] == 52.5
```

Adjust helper names (`_make_coordinator`, `mock_channel_factory`) to whatever fixture the existing `test_dhw_system_function_event_pushes_state`-style tests actually use in this file — read `test_coordinator.py` first and copy its exact existing DHW-event-test structure rather than inventing new fixtures.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/volsch/projekte/eebus && PYTHONPATH=. .venv/bin/pytest custom_components/eebus/tests/test_coordinator.py -k "room_heating or flow_temperature" -v`
Expected: FAIL — `async_set_room_heating_temperature`/`_handle_room_heating_event` not found, or `flow_temperature_c` not populated (since `MEASUREMENT_EVENT_FLOW_TEMPERATURE_UPDATED` isn't dispatched yet).

- [ ] **Step 3: Implement coordinator changes**

In `_handle_measurement_event` (around `coordinator.py:1484`), add two more `elif` branches after the existing outdoor-temperature block, mirroring the room/outdoor shape exactly:

```python
        elif event_type == (
            proto_stubs.MeasurementEventType.MEASUREMENT_EVENT_FLOW_TEMPERATURE_UPDATED
        ):
            if event.HasField("measurement"):
                self._push_data({"flow_temperature_c": event.measurement.value})
            else:
                self.hass.async_create_task(self.async_request_refresh())
        elif event_type == (
            proto_stubs.MeasurementEventType.MEASUREMENT_EVENT_RETURN_TEMPERATURE_UPDATED
        ):
            if event.HasField("measurement"):
                self._push_data({"return_temperature_c": event.measurement.value})
            else:
                self.hass.async_create_task(self.async_request_refresh())
```

Add a new `_handle_room_heating_event` method right after `_handle_dhw_sysfn_event` (around `coordinator.py:1585`), mirroring `_handle_dhw_sysfn_event`'s shape but covering all three payload variants (current temperature, setpoint, system function) from one event stream:

```python
    def _handle_room_heating_event(self, event: Any) -> None:
        from . import proto_stubs

        if not self._event_matches(event.ski):
            return
        event_type = event.event_type
        types = proto_stubs.RoomHeatingEventType
        if event_type == types.ROOM_HEATING_EVENT_SUPPORT_UPDATED:
            self.hass.async_create_task(self.async_request_refresh())
            return
        if not event.HasField("state"):
            self.hass.async_create_task(self.async_request_refresh())
            return
        state = event.state
        updates: dict[str, Any] = {"room_heating_supported": True}
        if state.HasField("setpoint"):
            updates["room_heating_setpoint"] = {
                "value_celsius": state.setpoint.value_celsius,
                "min_celsius": state.setpoint.min_celsius,
                "max_celsius": state.setpoint.max_celsius,
                "step_celsius": state.setpoint.step_celsius,
                "writable": state.setpoint.writable,
            }
        if state.HasField("system_function"):
            updates["room_heating_system_function"] = {
                "operation_mode": state.system_function.operation_mode,
                "available_modes": list(state.system_function.available_modes),
                "mode_writable": state.system_function.mode_writable,
            }
        self._push_data(updates)
```

Add `async_set_room_heating_temperature`/`async_set_room_heating_mode` methods right after `async_set_dhw_operation_mode` (around `coordinator.py:990`), mirroring `async_write_dhw_setpoint`/`async_set_dhw_operation_mode`'s exact try/except/`ServiceValidationError` shape, substituting `dhw_service_stub`/`SetDHWSetpoint`/`SetDHWOperationMode` for `hvac_service_stub`/`SetRoomHeatingTemperature`/`SetRoomHeatingMode`, and `self._dhw_supported`/`self._dhw_sysfn_supported` for a new `self._room_heating_supported` instance flag (initialize it alongside `self._dhw_supported`/`self._dhw_sysfn_supported` near `coordinator.py:170`).

Add a `_async_read_room_heating` method mirroring `_async_read_dhw_setpoint`+`_async_read_dhw_system_function` combined into one `GetRoomHeating` call (single RPC returns both setpoint and system-function per the proto contract), called from the main poll body (`coordinator.py:550` area) right after the DHW system function block:

```python
            data["room_heating"] = await self._async_read_room_heating(
                channel, proto_stubs, request
            )
            data["room_heating_supported"] = self._room_heating_supported
```

Store the setpoint/system-function separately in the returned dict (`{"setpoint": {...} | None, "system_function": {...} | None}`) or flatten to top-level `room_heating_setpoint`/`room_heating_system_function` keys — flatten to match the flat key style the rest of `coordinator.py`/`sensor.py`/`water_heater.py` already use (`dhw_setpoint`, `dhw_system_function` are top-level, not nested under a `"dhw"` key), so use `data["room_heating_setpoint"]` / `data["room_heating_system_function"]` directly instead of a `data["room_heating"]` wrapper.

Register the new stream in `async_start_streams` (`coordinator.py:1318`), appending `("room_heating_events", self._run_room_heating_event_stream)` to the tuple list, and add `_run_room_heating_event_stream` mirroring `_run_dhw_sysfn_event_stream`'s shape:

```python
    async def _run_room_heating_event_stream(self) -> None:
        from . import proto_stubs

        async def consume(channel: grpc.aio.Channel) -> None:
            stub = proto_stubs.hvac_service_stub(channel)
            async for event in stub.SubscribeRoomHeatingEvents(
                proto_stubs.DeviceRequest(ski=self.ski)
            ):
                self._handle_room_heating_event(event)

        await self._run_stream("room heating", consume)
```

- [ ] **Step 4: Add `hvac_service_stub` to `proto_stubs.py`**

Mirror the existing `dhw_service_stub` factory function exactly — read `custom_components/eebus/proto_stubs.py` for its current shape, add `HVACServiceStub`/`hvac_service_stub`, `GetRoomHeating`/`RoomHeatingState`/`RoomHeatingSetpoint`/`RoomHeatingSystemFunction`/`SetRoomHeatingTemperatureRequest`/`SetRoomHeatingModeRequest`/`RoomHeatingEventType`/`RoomHeatingEvent` to the module's `__all__` re-exports (required — `mypy --strict` implies `no_implicit_reexport`).

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /home/volsch/projekte/eebus && PYTHONPATH=. .venv/bin/pytest custom_components/eebus/tests/test_coordinator.py -k "room_heating or flow_temperature" -v`
Expected: PASS.

- [ ] **Step 6: Full local Python check**

Run: `ruff check custom_components/ && .venv/bin/mypy custom_components/eebus && PYTHONPATH=. .venv/bin/pytest -q`
Expected: clean, all tests pass.

- [ ] **Step 7: Commit**

```bash
git add custom_components/eebus/coordinator.py custom_components/eebus/proto_stubs.py custom_components/eebus/tests/test_coordinator.py
git commit -m "feat: wire room-heating RPCs and typed flow/return events into the coordinator"
```

---

### Task 7: `climate.py` platform + registration + strings/icons

**Files:**
- Create: `custom_components/eebus/climate.py`
- Test: `custom_components/eebus/tests/test_climate.py`
- Modify: `custom_components/eebus/const.py` (add `Platform.CLIMATE`)
- Modify: `custom_components/eebus/strings.json`, `translations/de.json`, `translations/en.json`, `icons.json`

**Interfaces:**
- Consumes: coordinator data keys `room_temperature_c` (existing, PR #100), `room_heating_setpoint`, `room_heating_system_function`, `room_heating_supported` (Task 6), `coordinator.async_set_room_heating_temperature`/`async_set_room_heating_mode` (Task 6).
- Produces: `climate.eebus_room_heating` entity, unique ID `${ski}_room_heating`.

- [ ] **Step 1: Write failing tests**

```python
# custom_components/eebus/tests/test_climate.py
"""Tests for the EEBUS room-heating climate entity."""

from __future__ import annotations

from unittest.mock import MagicMock

from homeassistant.components.climate import HVACMode
from homeassistant.components.climate.const import ClimateEntityFeature

from custom_components.eebus.climate import EebusRoomHeatingClimate


def _coordinator(data: dict) -> MagicMock:
    coordinator = MagicMock()
    coordinator.data = data
    coordinator.ski = "test-ski"
    return coordinator


def test_current_temperature_from_room_temperature_c() -> None:
    entity = EebusRoomHeatingClimate(_coordinator({"room_temperature_c": 22.5}))
    assert entity.current_temperature == 22.5


def test_target_temperature_and_constraints_from_setpoint() -> None:
    coordinator = _coordinator({
        "room_heating_setpoint": {
            "value_celsius": 21.0, "min_celsius": 5.0, "max_celsius": 30.0,
            "step_celsius": 0.5, "writable": True,
        },
    })
    entity = EebusRoomHeatingClimate(coordinator)
    assert entity.target_temperature == 21.0
    assert entity.min_temp == 5.0
    assert entity.max_temp == 30.0
    assert entity.target_temperature_step == 0.5
    assert entity.supported_features & ClimateEntityFeature.TARGET_TEMPERATURE


def test_hvac_mode_maps_known_eebus_modes() -> None:
    coordinator = _coordinator({
        "room_heating_system_function": {
            "operation_mode": "on", "available_modes": ["auto", "on", "off"], "mode_writable": True,
        },
    })
    entity = EebusRoomHeatingClimate(coordinator)
    assert entity.hvac_mode == HVACMode.HEAT
    assert set(entity.hvac_modes) == {HVACMode.AUTO, HVACMode.HEAT, HVACMode.OFF}


def test_unavailable_when_mode_unmappable_but_temperature_present() -> None:
    coordinator = _coordinator({
        "room_temperature_c": 22.5,
        "room_heating_system_function": {
            "operation_mode": "cool", "available_modes": ["cool"], "mode_writable": False,
        },
    })
    entity = EebusRoomHeatingClimate(coordinator)
    entity.coordinator.last_update_success = True
    assert entity.available is False


async def test_async_set_temperature_calls_coordinator() -> None:
    coordinator = _coordinator({"room_heating_setpoint": {"writable": True}})
    coordinator.async_set_room_heating_temperature = MagicMock()
    coordinator.async_request_refresh = MagicMock()
    entity = EebusRoomHeatingClimate(coordinator)
    await entity.async_set_temperature(temperature=22.0)
    coordinator.async_set_room_heating_temperature.assert_called_once_with(22.0)
```

Adjust `async_set_temperature`/`async_set_hvac_mode` test invocations once the base `EebusEntity`/`CoordinatorEntity` async method-mocking conventions used by `water_heater.py`'s own test file (`test_water_heater.py`, if one exists — check `custom_components/eebus/tests/` for the actual file name and fixture pattern for `EebusDHWWaterHeater`) are confirmed; mirror that file's exact `async def test_...` + `await entity.async_set_temperature(...)` + `coordinator.async_write_dhw_setpoint.assert_awaited_once_with(...)` (note: `assert_awaited_once_with`, not `assert_called_once_with`, if the coordinator methods are `AsyncMock`) shape precisely.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/volsch/projekte/eebus && PYTHONPATH=. .venv/bin/pytest custom_components/eebus/tests/test_climate.py -v`
Expected: FAIL — `custom_components.eebus.climate` module not found.

- [ ] **Step 3: Implement `climate.py`**

```python
"""Climate entity for EEBUS room-heating control."""

from __future__ import annotations

import logging
from typing import Any

from homeassistant.components.climate import (
    ClimateEntity,
    ClimateEntityFeature,
    HVACMode,
)
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import ATTR_TEMPERATURE, UnitOfTemperature
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from .coordinator import EebusCoordinator
from .entity import EebusEntity

PARALLEL_UPDATES = 0  # Coordinator-based, no per-entity polling

_LOGGER = logging.getLogger(__name__)

# EEBUS HvacOperationModeType <-> Home Assistant HVACMode. Explicit both ways;
# an EEBUS mode outside this map must not be invented as a HA mode.
_EEBUS_TO_HA_MODE: dict[str, HVACMode] = {
    "auto": HVACMode.AUTO,
    "on": HVACMode.HEAT,
    "off": HVACMode.OFF,
}
_HA_TO_EEBUS_MODE: dict[HVACMode, str] = {v: k for k, v in _EEBUS_TO_HA_MODE.items()}


async def async_setup_entry(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    """Set up the EEBUS room-heating climate entity."""
    coordinator: EebusCoordinator = entry.runtime_data
    async_add_entities([EebusRoomHeatingClimate(coordinator)])


class EebusRoomHeatingClimate(EebusEntity, ClimateEntity):
    """Room-heating control exposed by the EEBUS heat pump's HVACRoom entity."""

    _attr_temperature_unit = UnitOfTemperature.CELSIUS
    _attr_translation_key = "room_heating"

    def __init__(self, coordinator: EebusCoordinator) -> None:
        """Initialize the room-heating climate entity."""
        super().__init__(coordinator)
        self._attr_unique_id = f"{coordinator.ski}_room_heating"

    def _setpoint(self) -> dict[str, Any] | None:
        return (self.coordinator.data or {}).get("room_heating_setpoint")

    def _system_function(self) -> dict[str, Any] | None:
        return (self.coordinator.data or {}).get("room_heating_system_function")

    @property
    def available(self) -> bool:
        """Return whether the bridge is connected and the mode maps cleanly."""
        if not super().available:
            return False
        data = self.coordinator.data or {}
        if data.get("room_heating_supported") is False:
            return False
        system_function = self._system_function()
        if system_function is not None:
            mode = system_function.get("operation_mode")
            if mode and mode not in _EEBUS_TO_HA_MODE:
                _LOGGER.debug("EEBUS room heating mode %r has no HA mapping", mode)
                return False
        return bool(
            data.get("room_temperature_c") is not None
            or self._setpoint() is not None
        )

    @property
    def supported_features(self) -> ClimateEntityFeature:
        """Return the controls currently advertised as writable by the device."""
        features = ClimateEntityFeature(0)
        setpoint = self._setpoint()
        if setpoint is not None and setpoint.get("writable"):
            features |= ClimateEntityFeature.TARGET_TEMPERATURE
        system_function = self._system_function()
        if (
            system_function is not None
            and system_function.get("mode_writable")
            and system_function.get("available_modes")
        ):
            features |= ClimateEntityFeature.TURN_ON
            features |= ClimateEntityFeature.TURN_OFF
        return features

    @property
    def current_temperature(self) -> float | None:
        """Return the measured room temperature (shared with sensor.eebus_room_temperature)."""
        value = (self.coordinator.data or {}).get("room_temperature_c")
        return None if value is None else float(value)

    @property
    def target_temperature(self) -> float | None:
        """Return the configured room-heating target."""
        setpoint = self._setpoint()
        return None if setpoint is None else float(setpoint["value_celsius"])

    @property
    def min_temp(self) -> float:
        """Return the device-provided lower target-temperature bound."""
        setpoint = self._setpoint()
        return float(setpoint["min_celsius"]) if setpoint is not None else 5.0

    @property
    def max_temp(self) -> float:
        """Return the device-provided upper target-temperature bound."""
        setpoint = self._setpoint()
        return float(setpoint["max_celsius"]) if setpoint is not None else 30.0

    @property
    def target_temperature_step(self) -> float | None:
        """Return the device-provided target-temperature increment."""
        setpoint = self._setpoint()
        return None if setpoint is None else float(setpoint["step_celsius"])

    @property
    def hvac_modes(self) -> list[HVACMode]:
        """Return HA modes for the EEBUS modes advertised by the device."""
        system_function = self._system_function()
        if system_function is None:
            return [HVACMode.OFF]
        modes = system_function.get("available_modes") or []
        mapped = [_EEBUS_TO_HA_MODE[mode] for mode in modes if mode in _EEBUS_TO_HA_MODE]
        return mapped or [HVACMode.OFF]

    @property
    def hvac_mode(self) -> HVACMode | None:
        """Return the active HA mode mapped from the current EEBUS mode."""
        system_function = self._system_function()
        if system_function is None:
            return None
        mode = system_function.get("operation_mode")
        return _EEBUS_TO_HA_MODE.get(mode) if mode else None

    async def async_set_temperature(self, **kwargs: Any) -> None:
        """Set the room-heating target temperature."""
        await self.coordinator.async_set_room_heating_temperature(float(kwargs[ATTR_TEMPERATURE]))
        await self.coordinator.async_request_refresh()

    async def async_set_hvac_mode(self, hvac_mode: HVACMode) -> None:
        """Set the room-heating operation mode."""
        eebus_mode = _HA_TO_EEBUS_MODE.get(hvac_mode)
        if eebus_mode is None:
            raise ValueError(f"Unsupported HVAC mode: {hvac_mode}")
        await self.coordinator.async_set_room_heating_mode(eebus_mode)
        await self.coordinator.async_request_refresh()
```

- [ ] **Step 4: Register the platform**

In `custom_components/eebus/const.py`, add `Platform.CLIMATE` to the `PLATFORMS` list (alphabetically before `Platform.NUMBER`, matching the existing ordering convention: `BINARY_SENSOR, CLIMATE, NUMBER, SELECT, SENSOR, SWITCH, WATER_HEATER`).

- [ ] **Step 5: Add translations/icons**

In `strings.json`, `translations/de.json`, `translations/en.json`, add a `"climate"` sibling key next to `"water_heater"`:

```json
    "climate": {
      "room_heating": { "name": "Room Heating" }
    },
```

(German: `"Raumheizung"`.) In `icons.json`, add:

```json
    "climate": {
      "room_heating": { "default": "mdi:radiator" }
    },
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd /home/volsch/projekte/eebus && PYTHONPATH=. .venv/bin/pytest custom_components/eebus/tests/test_climate.py -v`
Expected: PASS (5 tests).

- [ ] **Step 7: Full local Python check**

Run: `ruff check custom_components/ && .venv/bin/mypy custom_components/eebus && PYTHONPATH=. .venv/bin/pytest -q`
Expected: clean.

- [ ] **Step 8: Commit**

```bash
git add custom_components/eebus/climate.py custom_components/eebus/const.py \
        custom_components/eebus/strings.json custom_components/eebus/translations/de.json \
        custom_components/eebus/translations/en.json custom_components/eebus/icons.json \
        custom_components/eebus/tests/test_climate.py
git commit -m "feat: add climate.eebus_room_heating entity"
```

---

### Task 8: `quality_scale.yaml` + README correction

**Files:**
- Modify: `custom_components/eebus/quality_scale.yaml`
- Modify: `README.md`

**Interfaces:** none (docs-only).

- [ ] **Step 1: Update `quality_scale.yaml`**

Update the `docs-supported-functions` comment (currently at line ~50-52) to add climate/room-heating alongside the existing DHW/water-heater mention:

```yaml
  docs-supported-functions:
    status: done
    comment: README lists the DHW water heater, boost, room-heating climate control, and their device-provided constraints/options.
```

- [ ] **Step 2: Correct the README HVAC claim**

Replace the stale line in `README.md`'s "Bekannte Einschraenkungen" section (currently: `**Keine HVAC-Steuerung:** Vaillant exponiert Betriebsmodi und Sollwerte nicht ueber EEBUS. Dafuer weiterhin mypyllant nutzen.`):

```markdown
- **HVAC-Steuerung:** Raumheizung wird als `climate.eebus_room_heating` exponiert (Ist-/Solltemperatur, Modi `auto`/`on`/`off`). Kuehlung, Zeitprogramme und ein belastbarer Heizaktivitaetsstatus (`hvac_action`) werden vom VR940 nicht angeboten.
```

Add a short "Supported functions" bullet for the climate entity near the existing DHW/water-heater description (find that section and add climate alongside it — read the surrounding README structure first to match its existing list format for feature entries).

- [ ] **Step 3: Verify no other stale references**

Run: `grep -rn "exponiert.*nicht ueber EEBUS\|Vaillant.*keine.*HVAC\|does not expose HVAC" README.md docs/ custom_components/`
Expected: no remaining hits besides the corrected line (and this plan document itself, which is expected).

- [ ] **Step 4: Commit**

```bash
git add custom_components/eebus/quality_scale.yaml README.md
git commit -m "docs: correct HVAC support claim now that room heating is implemented"
```

---

### Task 9: Local full verification pass

**Files:** none (verification only).

- [ ] **Step 1: Go**

Run: `cd eebus-bridge && go vet ./... && make test`
Expected: `go vet` clean, `go test -v -race ./...` all green.

- [ ] **Step 2: Python**

Run: `cd /home/volsch/projekte/eebus && ruff check custom_components/ && .venv/bin/mypy custom_components/eebus && PYTHONPATH=. .venv/bin/pytest --cov --cov-report=term-missing`
Expected: ruff/mypy clean, full suite green.

- [ ] **Step 3: Proto drift check**

Run (from repo root, using a scratch worktree or the same tree — revert after diffing):

```bash
cd eebus-bridge && PATH="$HOME/go/bin:$PATH" buf generate && cd ..
git status --short eebus-bridge/gen/proto  # expect no diff (or only a protoc-version header no-op)
uv venv -p 3.13 /tmp/proto-verify -q && source /tmp/proto-verify/bin/activate
uv pip install -q grpcio-tools==1.78.0 && bash generate_proto.sh && deactivate
git status --short custom_components/eebus/generated  # expect no diff
```

Expected: no drift beyond what was already committed in Task 4.

- [ ] **Step 4: Commit (if verification uncovered fixes)**

Only if Steps 1-3 required code changes:

```bash
git add -A
git commit -m "fix: address local verification findings for room-heating climate feature"
```

If no changes were needed, skip this step — nothing to commit.

---

### Task 10: Hardware acceptance at the VR940 (manual, gated)

**Files:** none — this is a live-hardware test, not a code change. Do not proceed to Task 11 until every item below is confirmed and documented (e.g. in the PR description or a project memory entry).

- [ ] **Step 1:** Deploy a branch-tagged bridge image (mirror the PR #100 process: `docker build -t ghcr.io/volschin/eebus-bridge:full-hvac-climate-spike eebus-bridge/`, push, update Portainer stack 93) and the branch's `custom_components/eebus` to the live HA container (tar → archive PUT → clear `__pycache__` → restart, per the established deploy runbook).
- [ ] **Step 2:** Confirm bridge log shows `RoomHeatingTemperature, RoomHeatingSystemFunction` in `Registered EEBUS use cases: ...` and no `AddUseCase` fatal errors.
- [ ] **Step 3:** Confirm `climate.eebus_room_heating` appears with correct initial state matching the device/app: current temperature, 21°C-class setpoint, range 5–30, step 0.5, current mode — no hardcoded values.
- [ ] **Step 4:** Confirm `sensor.eebus_flow_temperature`/`sensor.eebus_return_temperature` (enable them manually in the entity registry for this test, since they default disabled) report plausible live values with the correct entity/unit metadata logged.
- [ ] **Step 5:** Execute exactly one intentional room-setpoint change of one step via `climate.set_temperature`; confirm the device applies it (re-read via `GetRoomHeating` or the HA entity state), then reset it to the original value. Document the before/after values.
- [ ] **Step 6:** Execute exactly one intentional mode change via `climate.set_hvac_mode`, visible in myVAILLANT/the physical controller; confirm via re-read, then reset to the original mode. **Do not** run this automatically on every reconnect — `on`/`auto` can start heating.
- [ ] **Step 7:** Confirm a change made from myVAILLANT or the physical controller (not from HA) arrives in HA via push within a few seconds, without a poll cycle needing to elapse.
- [ ] **Step 8:** Force a SHIP reconnect (e.g. brief bridge restart) and confirm room-heating state and write capability recover without restarting the HA integration.
- [ ] **Step 9:** Once Steps 1–8 all pass, flip `entity_registry_enabled_default=False` → `True` for the `flow_temperature`/`return_temperature` entries in `custom_components/eebus/sensor.py`, and commit:

```bash
git add custom_components/eebus/sensor.py
git commit -m "feat: enable flow/return temperature sensors by default after VR940 hardware acceptance"
```

---

### Task 11: Remove the experimental HVAC probe (post hardware acceptance)

**Files:**
- Delete: `eebus-bridge/internal/eebus/hvacprobe.go`, `eebus-bridge/internal/eebus/hvacprobe_test.go`
- Modify: `eebus-bridge/cmd/eebus-bridge/main.go` (remove the `if cfg.Experimental.HvacProbe { ... }` block)
- Modify: `eebus-bridge/internal/config/config.go` (remove `HvacProbe`, `HvacProbeBind`, `HvacProbeWrite`, `HvacProbeWriteDeltaSKI`, `HvacProbeOverrunWriteSKI` fields and their env-var parsing)
- Modify: `eebus-bridge/internal/usecases/monitoring.go` (remove the `eebus.DefaultHvacProbe().ProbeOnce(ski, device)` call at line ~75)
- Modify README/docs referencing `EEBUS_EXP_HVAC_PROBE*` env vars.

**Do not start this task until Task 10 is fully signed off** — the probe is the only remaining diagnostic tool if the real use cases misbehave on first contact with hardware.

- [ ] **Step 1:** Grep for every reference before deleting: `grep -rn "HvacProbe\|EEBUS_EXP_HVAC_PROBE\|DefaultHvacProbe" eebus-bridge/ README.md docs/`.
- [ ] **Step 2:** Remove each reference found, including the `docker-compose`/README env-var documentation tables.
- [ ] **Step 3:** Run: `cd eebus-bridge && go build ./... && go vet ./... && go test -race ./...` — expect clean (no dangling references, no now-unused imports).
- [ ] **Step 4:** Commit:

```bash
git add -A
git commit -m "chore: remove experimental HVAC probe now that room heating ships as a real use case"
```

---

## Self-Review

**Spec coverage** (against `docs/superpowers/specs/2026-07-13-full-hvac-climate-design.md`):
- §3.1 current/target/min/max/step/modes → Task 7 (`climate.py`).
- §3.2 exclusions (no cooling, no `hvac_action`, no schedules/zones, DHW/outdoor stay separate) → enforced by Task 7's mode map (`_EEBUS_TO_HA_MODE` only has 3 entries) and by not adding any `hvac_action`/preset/fan property.
- §4.1–4.3 entity model, mode mapping, availability → Task 7.
- §4.4 flow/return sensors → Tasks 3, 4 (typed path + events), 10 Step 9 (enable by default).
- §5 eebus-go extensions → **deliberately not built as fork contributions**; Tasks 1–2 build them bridge-local instead, per the verified precedent and the user's explicit confirmation of this deviation.
- §6.1 use-case composition, no hardcoded IDs → Tasks 1, 2 (ID resolution via Description/Relation lists, exactly like DHW).
- §6.2 hydraulic measurements, HeatPumpAppliance preference → Task 3.
- §6.3 writes, full-list-copy, fail-closed validation → Tasks 1, 2.
- §7 gRPC contract, error mapping → Task 4.
- §8 coordinator/streams → Task 6.
- §9 test coverage (Go/Bridge/Python) → each task's own test step; Task 9 is the final full-suite gate.
- §10 hardware acceptance → Task 10.
- §11 rollout/migration, README correction, probe removal → Tasks 8, 10, 11 (reordered: fork step dropped per the architecture deviation; probe removal explicitly gated behind hardware acceptance, matching the spec's own step ordering).
- §12 Definition of Done → covered end-to-end by Tasks 1–11 collectively.

**Placeholder scan:** no TBD/TODO markers; the two spots that say "adjust once real ... clarify" (Task 4 Step 6, Task 6 Step 1, Task 7 Step 1) point at reading a *specific named existing file* for its *exact existing convention* to mirror — this is a concrete instruction (read X, copy its shape), not an open-ended placeholder.

**Type consistency:** `RoomHeatingSetpoint{Value, Minimum, Maximum, Step float64; Writable bool}` (Task 1) is consumed identically in Task 4's `convertRoomHeatingSetpoint` and Task 6's coordinator dict keys (`value_celsius`/`min_celsius`/`max_celsius`/`step_celsius`/`writable`) and Task 7's `_setpoint()` dict access — same field names throughout. `RoomHeatingSystemFunctionState{OperationMode, AvailableModes, ModeWritable}` (Task 2) flows identically through Task 4, 6, 7. `HydraulicTemperatures.FlowTemperature`/`ReturnTemperature` (Task 3) match the `temperatureReader` interface used in Task 4 Step 7 and Task 5 Step 2.
