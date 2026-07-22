# DHW CDSF Upstream Migration — Specification

**Date:** 2026-07-21 
**Status:** Phase 4 exit criteria validated
**Scope:** Incremental replacement of the bridge-local Configuration of DHW
System Function (CDSF) implementation with `eebus-go` CDSF while preserving the
existing gRPC and Home Assistant behaviour.

## 1. Decision summary

The migration will be incremental. The bridge will not replace the complete DHW
system-function stack in one change.

1. MDSF remains the owner of DHW system-function reads and state events.
2. The existing `DHWSystemFunctionAdapter` remains the boundary that combines
   MDSF state with CDSF write capability.
3. The CDSF use case from the pinned `eebus-go` fork will first take over use-case
   negotiation and feature setup.
4. Upstream CDSF will then take over boost writes and operation-mode writes in
   separate, independently verifiable steps.
5. A small bridge wrapper will continue to translate asynchronous EEBUS result
   callbacks into context-aware synchronous gRPC results.
6. Bridge-local capability inspection and refresh helpers may remain temporarily,
   but the target is to move generic CDSF write-capability semantics upstream.
7. The fork `replace` is removed only after all required behaviour exists in an
   upstream revision used by the bridge.

There must never be an automatic per-request fallback from an upstream write to
the legacy writer. Once a write may have reached the device, retrying it through a
second implementation could execute the operation twice.

## 2. Context and current state

The released bridge version at the time of this specification is `v0.14.4`.
It pins:

```text
volschin/eebus-go@a5640012fbd6
```

through a `replace` directive while retaining imports from
`github.com/enbility/eebus-go`. The patch inventory is maintained in
`eebus-bridge/UPSTREAM_PATCHES.md`.

Relevant upstream work:

- `enbility/eebus-go#239`: HVAC and Setpoint client features.
- `enbility/eebus-go#246`: Monitoring of DHW System Function (MDSF).
- `enbility/eebus-go#247`: Configuration of DHW System Function (CDSF).
- `volschin/eebus-go#1`: additional fail-closed list-write, CDSF and CDT
  hardening currently included in the pinned fork.

The fork implementation can already perform the required CDSF writes:

- `WriteOperationMode`
- `StartOneTimeDhw`
- `StopOneTimeDhw`
- relation-safe mode resolution
- advertised `Write()` checks
- partial-list writes when advertised
- full cached-list merge otherwise
- result callbacks containing the device's `ResultData`
- fail-closed handling of ambiguous system-function and overrun identifiers

### 2.1 Current bridge ownership

```text
MDSF wrapper
  ├── compatible monitoring entity
  ├── available/current operation modes
  ├── overrun state
  └── state/support events
             │
             ▼
  DHWSystemFunctionAdapter ─────────► DHW gRPC service ─────────► Home Assistant
             ▲
             │
local CDSF implementation
  ├── compatible configuration entity
  ├── write-capability calculation
  ├── operation-mode write
  ├── boost start/stop write
  ├── result wait/error mapping
  └── post-write refresh
```

The main implementation points are:

- `eebus-bridge/internal/usecases/dhwsysfn_adapter.go`
- `eebus-bridge/internal/usecases/dhwsysfn.go`
- `eebus-bridge/internal/usecases/hvac_write_flow.go`
- `eebus-bridge/cmd/eebus-bridge/app.go`
- `eebus-bridge/internal/grpc/dhw_service.go`

`dhwsysfn.go` is approximately 500 lines and contains both generic CDSF protocol
logic and bridge-specific error/lifecycle behaviour. The adapter is not itself a
duplicate CDSF implementation and is expected to remain.

## 3. Goals

- Use the fork's existing CDSF write implementation before it is merged upstream.
- Migrate boost and operation-mode writes independently.
- Preserve the existing gRPC and Home Assistant contracts without a proto change.
- Preserve context cancellation, timeout and device-rejection reporting.
- Preserve fail-closed entity and identifier resolution.
- Keep `boost_writable` and `mode_writable` consistent with actual write
  acceptance rules.
- Retain an easy release-level rollback during hardware validation.
- Remove duplicated generic CDSF state/write logic from the bridge.
- Produce reusable evidence and tests for the corresponding upstream changes.

## 4. Non-goals

- Replacing MDSF reads with CDSF reads.
- Changing the Home Assistant water-heater or switch entity model.
- Changing the DHW gRPC messages or RPC names.
- Automatically retrying a potentially delivered write through another path.
- Maintaining a permanent bridge-specific fork of `eebus-go`.
- Moving gRPC status codes, HA policy or bridge registry behaviour into
  `eebus-go`.
- Migrating the room-heating system-function implementation in the same change.

## 5. Behavioural invariants

Every migration phase must preserve the following invariants.

### 5.1 Read ownership

- Operation mode, available modes and boost status come from MDSF.
- MDSF remains the source of state-update events published to the bridge event
  bus.
- A CDSF event must not overwrite MDSF state or create a second state authority.

### 5.2 Entity resolution

- The gRPC service resolves the monitoring entity through MDSF.
- The adapter maps the monitoring entity's normalized device SKI to a separately
  negotiated CDSF configuration entity.
- Missing, disconnected or ambiguous CDSF entities fail closed.
- A monitoring entity must never be passed directly to CDSF merely because both
  roles happen to use the same entity on one test device.

### 5.3 Write completion

A write is successful only after all of the following:

1. local validation succeeds;
2. CDSF accepts and sends the command;
3. a matching EEBUS `ResultData` is received;
4. `ResultData.ErrorNumber` is absent or zero.

The bridge must continue to support:

- caller context cancellation;
- a bounded device-result timeout;
- callbacks invoked synchronously before the CDSF method returns;
- callbacks invoked asynchronously after the method returns;
- a missing message counter or callback registration failure;
- non-zero device error numbers and descriptions.

The result channel must be buffered so a synchronous callback cannot deadlock the
write call.

### 5.4 No automatic write fallback

Legacy and upstream implementations may coexist as selectable strategies during
the migration, but only one strategy may be called for an individual operation.

If the upstream method returns an error after attempting to send, the bridge must
return that error. It must not issue the same command through the legacy writer.
Rollback is performed by changing the selected strategy in a later build or
release.

### 5.5 Post-write state convergence

After an accepted write, the affected list must be requested again unless the
library can guarantee an equivalent fresh notification. A successful transport
result alone does not prove that the local cache and HA state have converged.

As of Phase 4, `eebus-go` CDSF performs this refresh internally
(`registerResultCallback` re-requests the affected list before invoking the
bridge's result callback), so the bridge no longer runs its own post-write
request. The bridge's role is limited to waiting for the write result via
`awaitDHWWrite`.

## 6. Target architecture

```text
eebus-go MDSF                         eebus-go CDSF
  reads and state events                negotiation, writes and refresh
          │                                      │
          ▼                                      ▼
DHWSystemFunctionMonitoring       CDSFConfigurationFacade
          │                         ├── entity resolver
          │                         ├── capability inspector (upstream-delegated)
          │                         ├── boost strategy
          │                         └── mode strategy
          └──────────────────────┬───────────────────┘
                                 ▼
                    DHWSystemFunctionAdapter
                                 │
                                 ▼
                        existing gRPC contract
```

### 6.1 Narrow upstream client interface

The bridge wrapper depends on a narrow mockable interface rather than on
the concrete `*cdsf.CDSF` type:

```go
type caCDSFClient interface {
    eebusapi.UseCaseInterface
    RemoteEntitiesScenarios() []eebusapi.RemoteEntityScenarios
    WriteCapabilities(spineapi.EntityRemoteInterface) (ucapi.DHWSystemFunctionWriteCapabilities, error)
    OperationModes(spineapi.EntityRemoteInterface) ([]ucapi.HvacOperationModeType, error)
    WriteOperationMode(
        spineapi.EntityRemoteInterface,
        ucapi.HvacOperationModeType,
        func(model.ResultDataType, model.MsgCounterType),
    ) (*model.MsgCounterType, error)
    StartOneTimeDhw(
        spineapi.EntityRemoteInterface,
        func(model.ResultDataType, model.MsgCounterType),
    ) (*model.MsgCounterType, error)
    StopOneTimeDhw(
        spineapi.EntityRemoteInterface,
        func(model.ResultDataType, model.MsgCounterType),
    ) (*model.MsgCounterType, error)
}
```

The facade can therefore query negotiated CDSF scenarios without inspecting
bridge registry strings.

### 6.2 Strategy boundary

Boost and operation-mode writes must be independently replaceable:

```go
type dhwBoostWriter interface {
    WriteBoost(context.Context, spineapi.EntityRemoteInterface, bool) error
}

type dhwOperationModeWriter interface {
    WriteOperationMode(context.Context, spineapi.EntityRemoteInterface, string) error
}
```

`CDSFConfigurationFacade` implements the adapter's current configuration
contract and delegates to the selected strategies. This keeps the existing
`DHWSystemFunctionAdapter` and gRPC layer stable while allowing commit-by-commit
migration.

## 7. Write-capability contract

The gRPC state currently exposes only two booleans. They must be conservative:
the UI must not advertise a control that the write path will predictably reject.

### 7.1 Operation mode

`mode_writable` is true only if:

1. CDSF scenario 1 is negotiated for the configuration entity;
2. `HvacSystemFunctionListData` advertises `Write()`;
3. exactly one DHW system function is resolved;
4. `IsOperationModeIdChangeable` is not explicitly `false`;
5. at least one operation mode is related to that DHW system function.

A requested mode is valid only when its type resolves to exactly one mode ID in
the relation of the selected DHW system function. A globally present but
unrelated mode is invalid.

### 7.2 Boost

`boost_writable` is true only if:

1. CDSF scenarios 2 and 3 are negotiated for the configuration entity;
2. `HvacOverrunListData` advertises `Write()`;
3. exactly one one-time-DHW overrun affects the selected DHW system function;
4. `IsOverrunStatusChangeable` is not explicitly `false`.

Requiring both start and stop scenarios is deliberate. The single public boolean
cannot express direction-dependent writeability, and the bridge must not expose a
boost that HA can start but cannot subsequently stop.

### 7.3 Temporary local capability inspector

The pinned fork enforces these rules when executing a write but does not expose a
complete public capability query suitable for `boost_writable` and
`mode_writable`.

During the migration, the bridge may retain a read-only capability inspector
extracted from the existing resolver. It must:

- read only the cached remote feature and negotiated scenarios;
- never send an EEBUS command;
- use the same `Write() && changeable != false` rules as `eebus-go`;
- fail closed on missing or ambiguous data;
- be covered by contract tests shared with the upstream-writer facade.

Target upstream follow-up: expose generic CDSF write-capability methods or a
structured capability result from `eebus-go`, then remove the local inspector.

## 8. Error mapping

The existing public error contract remains unchanged.

| Condition | Bridge error | gRPC code |
|---|---|---|
| Requested mode is not in MDSF/CDSF related modes | `ErrDHWSysFnInvalidMode` | `InvalidArgument` |
| Scenario absent, write operation absent, or explicit non-changeable flag | `ErrDHWSysFnNotWritable` | `FailedPrecondition` |
| Device returns non-zero result | `ErrDHWSysFnRejected` | `FailedPrecondition` |
| Entity, metadata, relation, ID, counter or required cache data missing/ambiguous | `ErrDHWSysFnDataUnavailable` | `Unavailable` |
| Caller cancels or reaches its deadline | `context.Canceled` / `context.DeadlineExceeded` | existing context mapping |
| Internal device-result timeout | wrapped timeout error | existing write-error mapping |

Because upstream CDSF may return `api.ErrNotSupported` for both an invalid mode
and non-writeability, the bridge must prevalidate the requested mode against the
related available modes before calling `WriteOperationMode`. After that check,
`api.ErrNotSupported` maps to `ErrDHWSysFnNotWritable`.

Errors must be mapped with `errors.Is`-compatible wrapping. Error strings are not
part of the decision logic.

## 9. Incremental migration plan

### Phase 0 — Characterize and create seams

No protocol ownership changes.

Work:

1. Add contract tests for the current local implementation and adapter.
2. Separate the local cached-data resolver/capability inspector from the legacy
   write transport.
3. Introduce independent boost and operation-mode writer interfaces.
4. Introduce a context-aware `awaitDHWWrite` helper that accepts the upstream
   result-callback shape.
5. Keep the existing local CDSF use case registered and select both legacy
   writers.

Exit criteria:

- No gRPC, event or hardware behaviour changes.
- Existing local unit and integration tests remain green.
- New tests prove synchronous callback, asynchronous callback, rejection,
  cancellation, timeout and send-time failure behaviour.
- Writer selection can be changed independently without modifying gRPC code.

Rollback: revert the structural commit; no dependency or protocol change exists.

### Phase 1 — Upstream CDSF owns negotiation, legacy owns writes

Work:

1. Instantiate `ca/cdsf.NewCDSF` from the pinned fork.
2. Register it as `DHWSystemFunctionConfiguration` instead of the local CDSF use
   case.
3. Do not register both local and upstream CDSF use cases simultaneously.
4. Retain extracted legacy raw write strategies for boost and operation mode.
5. Resolve the configuration entity through upstream CDSF
   `RemoteEntitiesScenarios()`.
6. Compare upstream and legacy capability decisions in tests and optional debug
   diagnostics, but never issue shadow writes.

The extracted legacy writer may use the HVAC client feature installed by upstream
CDSF. It is a temporary rollback strategy, not a second registered use case.

Exit criteria:

- The same VR940 CDSF configuration entity is resolved before and after the
  change.
- Pairing, bridge restart and device reconnect repopulate required CDSF cache
  data.
- Current legacy writes still pass unchanged through the new facade.
- No duplicate use-case registration, feature, subscription or user-visible
  event is produced.

Rollback: restore the local CDSF use-case registration and facade composition in
a new build. Do not switch implementations inside an active request.

### Phase 2 — Upstream CDSF owns boost start and stop

Boost start and stop migrate as one atomic feature. They must not be split across
different releases.

Work:

1. Select an upstream boost writer that calls `StartOneTimeDhw` or
   `StopOneTimeDhw` according to the requested boolean.
2. Wait for the matching result using the caller context and bounded timeout.
3. Map non-zero results to `ErrDHWSysFnRejected`.
4. Refresh `HvacOverrunListData` after an accepted result.
5. Keep operation-mode writes on the extracted legacy strategy.

Exit criteria:

- At least 10 consecutive boost start/stop cycles succeed on the target hardware.
- HA state converges after every transition without waiting for the periodic
  poll.
- Cancellation and simulated rejection tests pass.
- Three reconnect/restart cycles preserve boost capability and writeability.
- Missing scenario 2 or 3 makes `boost_writable` false.

Rollback: select the legacy boost strategy in the next build. Do not retry a
failed upstream request through the legacy strategy.

### Phase 3 — Upstream CDSF owns operation-mode writes

Work:

1. Prevalidate the requested mode against the relation-safe available modes.
2. Call upstream `WriteOperationMode` with the resolved CDSF entity.
3. Await and map the device result.
4. Refresh `HvacSystemFunctionListData` after acceptance.
5. Remove the legacy operation-mode transport after the exit criteria are met.

Exit criteria:

- Every operation mode advertised by the VR940 is selected and read back at
  least once.
- At least 10 total mode transitions succeed.
- A globally present but unrelated mode is rejected as invalid.
- Ambiguous related mode IDs fail closed.
- Non-writeable and device-rejected writes retain their existing gRPC codes.
- Three reconnect/restart cycles preserve mode capability and writeability.

Rollback: restore the legacy mode strategy in the next build. No automatic
request-level fallback is permitted.

### Phase 4 — Remove local CDSF semantics (done)

Work:

1. Add or adopt an upstream CDSF write-capability API. (`WriteCapabilities`,
   `OperationModes` on `volschin/eebus-go` — done.)
2. Add or adopt upstream post-write refresh behaviour. (`registerResultCallback`
   re-requests the affected list before the result callback fires — done.)
3. Replace the temporary local capability inspector. (`upstreamDHWSystemFunctionCapabilityInspector`
   now only maps the upstream contract — done.)
4. Remove local CDSF ID, relation, list-merge and changeability resolution.
   (`dhwsysfn_cache.go` deleted — done.)
5. Remove legacy transport code and tests that only cover deleted internals.
   (`legacyDHWSystemFunctionWriter`, `NewLegacyDHWSystemFunctionConfiguration`,
   `DHWSystemFunction`/`dhwsysfn_test.go` removed — done.)
6. Retain bridge tests for the public facade, adapter and error mapping. (done.)

Exit criteria:

- The bridge contains no second implementation of CDSF ID/relation resolution or
  list-write semantics. — met.
- `dhwsysfn.go` is deleted or reduced to bridge-domain state/errors only. — met
  (state struct + error vars only).
- `DHWSystemFunctionAdapter` still maps MDSF and CDSF entities explicitly. — met
  (unchanged in this phase).
- All capability and write decisions are covered through the upstream client
  interface. — met.

### Phase 5 — Return to upstream dependency

Work:

1. Confirm #239, #246, #247 and the required hardening follow-ups are present in
   the selected upstream revision.
2. Rebuild `bridge-integration` on the new upstream base and remove merged
   patches from `UPSTREAM_PATCHES.md`.
3. Repeat unit, integration and hardware tests against the exact upstream commit.
4. Remove the `replace` directive when the patch queue is empty.
5. Publish a patch release and verify the multi-architecture image.

Exit criteria:

- `go list -m` resolves `github.com/enbility/eebus-go` directly to upstream.
- `UPSTREAM_PATCHES.md` contains no obsolete CDSF patch.
- No bridge release depends on a moving branch or unreviewed PR head.

## 10. Test specification

### 10.1 Unit tests

This list covered Phases 1-3, while the bridge still owned scenario gating,
changeability-flag resolution and post-write refresh. As of Phase 4 those
concerns moved into `eebus-go` CDSF's own contract tests
(`usecases/ca/cdsf/public_test.go`) and are no longer bridge-tested directly;
scenario-absent, changeability-flag and refresh-count cases were removed from
the bridge test suite accordingly (see `dhwsysfn_configuration_test.go`,
`dhwsysfn_upstream_boost_test.go`, `dhwsysfn_upstream_mode_test.go`).

The facade and strategies must cover:

- MDSF entity and CDSF entity are different objects for the same SKI;
- missing CDSF negotiation;
- nil monitoring entity, device or configuration entity;
- ambiguous device/entity resolution;
- `WriteCapabilities`/`OperationModes` error propagation;
- boost requires both start and stop capability;
- requested mode is related, unrelated and ambiguously related;
- partial-write and full-list-write behaviour in `eebus-go` contract tests;
- callback before method return;
- callback after method return;
- non-zero device result with and without description;
- send-time error;
- nil message counter;
- callback registration error;
- caller cancellation and deadline;
- internal result timeout.

### 10.2 Bridge integration tests

- Existing DHW gRPC response shape is unchanged.
- `mode_writable` and `boost_writable` reach HA unchanged through protobuf and
  Python model conversion.
- Existing gRPC error-code tests continue to pass.
- DHW system-function streams remain MDSF-owned and contain current state.
- Capability registry support remains derived from MDSF for monitoring and from
  CDSF only for configuration/writeability.
- No duplicate support/state events are emitted.

### 10.3 Hardware validation

For every phase that changes ownership:

1. Record bridge version, eebus-go commit, device model and firmware.
2. Capture initial negotiation after fresh bridge start.
3. Test after device disconnect/reconnect.
4. Test after bridge restart without re-pairing.
5. Confirm current state before the write.
6. Perform the write and record send/result/update timestamps.
7. Confirm the new state through MDSF and HA.
8. Retain sanitized traces for upstream evidence.

No write test may intentionally send both legacy and upstream commands for
comparison.

## 11. Verification commands

Each bridge phase must pass at minimum:

```bash
cd eebus-bridge
go vet ./...
go test -race ./...
go test -tags=integration -v ./internal/grpc/ -run TestIntegration
```

The repository CI coverage and generated-file checks remain mandatory. Changes
to the pinned fork additionally require its full test suite and the patch
inventory update.

## 12. Rollout and release policy

- Each ownership change is a separate bridge PR.
- Phase 2 and Phase 3 must not be combined into one PR.
- Each PR states the selected boost and mode strategies explicitly.
- A patch release is produced after hardware validation of each user-visible
  ownership phase, unless the phase remains on an unreleased test branch.
- Debug logging may identify the selected transport (`legacy` or `upstream`) but
  must not expose full SKIs or sensitive device data.
- Rollback is release-level strategy selection, never automatic retry.
- The old strategy is deleted only after the corresponding phase exit criteria
  are met.

## 13. Upstream follow-up candidates

The following generic improvements should be proposed upstream rather than kept
as permanent bridge logic:

1. A structured CDSF write-capability API covering mode, start and stop.
2. Explicit scenario-aware writeability semantics.
3. Post-success refresh of the affected HVAC list, or a public refresh method.
4. Stable error distinction between unsupported requested value, read-only
   capability and unavailable/ambiguous data.
5. Tests proving full-list merge failure cannot degrade into a partial-looking
   full write.

Bridge-specific context handling and gRPC error mapping remain in the bridge.

## 14. Completion definition

The migration is complete when:

- MDSF exclusively owns DHW system-function reads and state events;
- upstream CDSF owns negotiation and all DHW system-function writes;
- the bridge retains only entity composition, context/result adaptation, error
  translation and product-facing state mapping;
- no generic CDSF list merge, ID selection or relation resolution remains in the
  bridge;
- hardware validation demonstrates equivalent or better behaviour;
- the required implementation is available from the upstream dependency used by
  the bridge; and
- the temporary fork `replace` has been removed.
