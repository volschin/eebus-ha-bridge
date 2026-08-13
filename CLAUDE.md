# EEBUS bridge repository instructions

Local two-component bridge:

```text
Home Assistant custom_components/eebus (Python) -> gRPC -> eebus-bridge (Go)
-> enbility/eebus-go SHIP/SPINE -> heat-pump gateway
```

The Python side is a gRPC client and contains no EEBUS protocol logic. SHIP mDNS
requires host networking. gRPC defaults to plaintext loopback; non-loopback binds
must use `security.mode: tls_token` with TLS and per-RPC bearer authentication.

## Commands

From the repository root:

```bash
PYTHONPATH=. pytest
ruff check custom_components/
mypy custom_components/eebus
bash generate_proto.sh
```

From `eebus-bridge/`:

```bash
go vet ./...
make test                 # go test -v -race ./...
make build
make proto
```

## Cross-component contracts

- `eebus-bridge/proto/eebus/v1/*.proto` is the sole gRPC source of truth. After
  any proto change run both `make proto` in `eebus-bridge/` and
  `bash generate_proto.sh` at the root; commit both generated trees and never
  hand-edit them.
- Python code imports generated messages through `proto_stubs.py`. Keep explicit
  `__all__` exports and construct grpc stubs through its typed factory helpers.
- Keep HA's grpcio pin, `manifest.json`, `generate_proto.sh` and
  `.github/workflows/grpcio-sync.yml` aligned.
- New entities, config-flow paths and translated exceptions may require matching
  README, translations and `quality_scale.yaml` updates.

## Runtime invariants

- `EebusCoordinator` is streaming-first and falls back to five-minute polling.
  Provider callbacks signal `_ProviderPusher`; they do not push inline.
- Empty SKI resolves only when exactly one compatible device exists; ambiguity
  fails with `FAILED_PRECONDITION`.
- Pair/unpair is a synchronous command through `TrustController`; bus events are
  observations. Deleting `internal/certs` changes the bridge SKI and forces
  re-pairing.
- Vaillant gateways accept only one active SHIP connection. Before diagnosing an
  endless trust loop, rule out another energy manager and inspect `ship_log`.
- The `volschin/eebus-go` replacement carries required room-heating/DHW patches.
  Renovate is intentionally disabled for it; do not replace it with upstream or
  change its digest without checking `eebus-bridge/UPSTREAM_PATCHES.md`.
- Consumer/provider ownership and migration rules are documented in the current
  specs under `docs/`; do not reintroduce deleted RF/SPEC2/SPEC3 tracking as open
  work. `docs/refactoring-optimization-spec-v4.md` is the structural baseline.
- Releases are tag-triggered. `manifest.json` and
  `eebus-bridge-addon/config.yaml` versions must match; CI enforces this through
  `scripts/check_addon_version.py`. Do not bump either version or create a
  release tag inside a feature/fix change.

Use real generated protobuf objects in tests. Preserve fail-closed security and
ambiguity handling. Build only the current use case; do not add speculative
use-case scaffolding, options or compatibility layers.
