# SPEC2-09 Slice 1 — reject NaN/Inf in LPC numeric writes

Source: `docs/refactoring-optimization-spec-v2.md` SPEC2-09 (P1). This slice covers
only the first Abnahme bullet: `NaN/Inf werden in allen numerischen Write- und
Publish-RPCs als INVALID_ARGUMENT abgewiesen.` The remaining Soll bullets (central
error-mapping policy adoption elsewhere, unified entity-resolution flow, uniform
stream validation, deadline propagation) are separate follow-up slices — see
"Deliberately out of scope" below for why they don't belong in this slice.

## Problem

`eebus-bridge/internal/grpc/errors.go` already has `finite`/`nonNegative`/`percent`
helpers, and `grid_service.go`/`visualization_service.go` (provider push RPCs)
already use them. But `lpc_service.go`'s four numeric write RPCs never adopted
them — they still use a raw `req.ValueWatts < 0` / `req.DurationSeconds < 0`
comparison. In Go, `NaN < 0` is `false`, so a NaN payload sails past this check,
gets forwarded into `s.lpc.WriteConsumptionLimit`/`WriteFailsafeConsumptionActivePowerLimit`,
and is serialized into a spine-go scaled number advertised to the heat pump as a
real load-limit reading — silent bad data, exactly the failure mode `finite`'s doc
comment in errors.go already describes for the provider RPCs.

Affected in `eebus-bridge/internal/grpc/lpc_service.go`:
- `WriteConsumptionLimit`: `req.ValueWatts < 0` (line 52), `req.DurationSeconds < 0` (line 55)
- `WriteFailsafeLimit`: `req.ValueWatts < 0` (line 108), `req.DurationMinimumSeconds < 0` (line 111)

(Line numbers from the current `main` tip; re-check before editing.)

## What to build

Replace each of the four raw `< 0` comparisons with a call to the existing
`nonNegative(name, v)` helper from `errors.go` (same helper `grid_service.go`
already uses), converting `int64` duration fields to `float64` for the check
(`nonNegative("duration_seconds", float64(req.DurationSeconds))`). Keep the
field names in error messages consistent with the current ones (`value_watts`,
`duration_seconds`, `duration_minimum_seconds`) so behavior for legitimately
negative input is unchanged — only NaN/Inf becomes newly rejected.

Do not touch the SKI/nil/init checks in these four methods, and don't restructure
the methods otherwise — this is a single-line-per-check swap.

## Deliberately out of scope

- **Error-policy migration for LPC/OHPCF/monitoring/device services** (Soll
  bullet 2, `mapUsecaseError`): DHW and HVAC already adopted this because
  `internal/usecases/dhw.go`/`hvac.go` define their own sentinel errors
  (`ErrDHWOutOfRange`, etc.) to classify. LPC/OHPCF/monitoring's use-case calls
  return raw errors straight from the vendored `enbility/eebus-go` library with
  no sentinel taxonomy of our own — `codes.Internal` is the honest mapping today,
  not a shortcut. Introducing sentinels there is real design work (what error
  classes does eebus-go actually surface?), not a mechanical migration — separate
  slice.
- Shared range/enum/timestamp boundary helpers, unified entity-resolution flow,
  uniform stream validation, context/deadline propagation into the use-case layer
  (remaining Soll bullets) — each is its own slice.

## Acceptance

- Table-driven test in `eebus-bridge/internal/grpc/lpc_service_test.go` proving
  `WriteConsumptionLimit` and `WriteFailsafeLimit` reject `math.NaN()`,
  `math.Inf(1)`, and `math.Inf(-1)` for both `ValueWatts` and the duration field
  with `codes.InvalidArgument`, while legitimate non-negative values still pass
  (regression check against the existing negative-value tests, if any).
- No other file changes; no Python-side changes.

## Verification

- `go vet ./...`
- `go test -race ./internal/grpc/...` (full `make test` optional but not required
  for this narrowly-scoped change)
