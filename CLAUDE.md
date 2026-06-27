# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Two-component bridge that exposes EEBUS-capable heat pumps (primary target: Vaillant aroTHERM plus via VR920/VR921/VR940f gateways) to Home Assistant — locally, no cloud.

```
Home Assistant            eebus-bridge (Go)        Heat pump gateway
custom_components/eebus  --gRPC-->  eebus-go      --SHIP/SPINE-->  device
(Python, local_push)    50051      (SHIP+SPINE)   4712 / mDNS
```

- `custom_components/eebus/` — HA integration (Python). gRPC client only; holds no EEBUS logic.
- `eebus-bridge/` — Go daemon embedding `enbility/eebus-go` (SHIP+SPINE). Runs as separate process/container (host networking required: SHIP discovery uses `_ship._tcp` mDNS multicast which can't cross Docker bridge nets). gRPC binds `127.0.0.1` by default.

## The proto contract (critical)

`eebus-bridge/proto/eebus/v1/*.proto` is the single source of truth for the gRPC interface. It generates stubs for **both** sides — they must stay in sync:

- **Go** stubs → `eebus-bridge/gen/proto/` via `buf generate` (`make proto`, run from `eebus-bridge/`).
- **Python** stubs → `custom_components/eebus/generated/` via `bash generate_proto.sh` (run from repo root).

After any `.proto` change, regenerate **both**. CI's `proto-drift` job regenerates Python stubs and fails on `git diff`. Generated dirs are committed, excluded from ruff/coverage, and must not be hand-edited.

`generate_proto.sh` pins `grpcio-tools==1.78.0` and rewrites absolute `from eebus.v1 import` to relative imports so stubs load inside the `custom_components.eebus` package. Use `proto_stubs.py` re-exports, not deep imports into `generated/`.

### grpcio version pin

HA Core pins `grpcio==1.78.0` in `package_constraints.txt`. The manifest floor (`requirements` in `manifest.json`) and `generate_proto.sh`'s `grpcio-tools` version must match HA's pin, or the embedded `GRPC_GENERATED_VERSION` exceeds HA's runtime grpcio and install fails (issue #22). `.github/workflows/grpcio-sync.yml` automates bumping these when HA changes the pin. When touching grpc deps, keep `manifest.json`, `generate_proto.sh`, and the bump workflow aligned.

## Commands

### Python (run from repo root)
```bash
PYTHONPATH=. pytest                              # all tests (asyncio_mode=auto)
PYTHONPATH=. pytest custom_components/eebus/tests/test_coordinator.py::test_name  # single test
PYTHONPATH=. pytest --cov --cov-report=term-missing
ruff check custom_components/                    # lint (line-length 120, py312 target)
bash generate_proto.sh                           # regenerate Python stubs
```

### Go (run from `eebus-bridge/`)
```bash
make build                                       # -> bin/eebus-bridge
make test            # == go test -v -race ./...
go test -v ./internal/usecases/ -run TestName    # single test
go test -tags=integration -v ./internal/grpc/ -run TestIntegration  # integration (build-tagged)
go vet ./...
make proto                                       # buf generate -> gen/proto/
```

### Run the bridge
```bash
docker-compose up -d eebus-bridge        # ghcr.io image, host networking
# or: ./eebus-bridge --config config.yaml
```

## Architecture notes

### Python integration
- `coordinator.py` (`EebusCoordinator`) — central hub. Primary path is gRPC **streaming** (push); on stream failure it falls back to **polling** (`POLL_INTERVAL = 5min`) and reconnects the stream in the background. Polling only reconciles state streams can't carry (scoped energy, heartbeat, support flags). `FLAT_MEASUREMENT_TYPE_TO_KEY` maps bridge `GetMeasurements` entry types to per-phase/grid sensor keys.
- Platforms: `sensor`, `number`, `switch`, `binary_sensor` — all read coordinator data; `entity.py` is the shared base.
- `config_flow.py` — bridge host/port + device SKI pairing. `iot_class: local_push`, `quality_scale: gold` (see `quality_scale.yaml`).
- Tests use HA fixtures in `conftest.py`; protobuf messages must be real generated types, not duck-typed namespaces.

### Go bridge
- `cmd/eebus-bridge/main.go` — entrypoint, wires config + service + gRPC server.
- `internal/eebus/` — `service.go` embeds eebus-go; `registry.go` (DeviceRegistry, shared across use cases), `eventbus.go` (fan-out to gRPC streams), `callbacks.go` (SPINE event handlers).
- `internal/usecases/` — `lpc.go` (Limitation of Power Consumption, §14a), `monitoring.go` (measurements), `classification.go` (device mfr/model from EEBUS DeviceClassification). eebus-go validates entity compatibility **per use case**: multi-entity gateways need use-case-aware entity resolution, not just registry lookup (issue #47 — `CompatibleEntity` resolver).
- `internal/grpc/` — service impls (`device_service.go`, `lpc_service.go`, `monitoring_service.go`) + `server.go`.
- `internal/certs/` — auto-generated TLS certs define the bridge SKI. Deleting them changes the SKI and forces re-pairing.

## Conventions

- Releases are tag-triggered: pushing a `v*` tag builds multi-arch (amd64/arm64/armv7) Docker images to GHCR. `release-drafter` drafts notes on merge to main. Bump `version` in `manifest.json` for HA releases.
- CI skips Go/proto jobs via path filters when only the other side changed. `paths-ignore` skips `**/*.md`.
- HVAC control (modes/setpoints) is out of scope for now — LPC + measurement only. Note: the Vaillant VR940 *does* expose HVAC/DHW config+setpoint use cases over EEBUS (confirmed via live discovery, `docs/vr940-usecase-dump.txt`); the blocker is that `enbility/eebus-go` ships no HVAC/setpoint use cases, only the energy domain. It also advertises MGCP/VAPD/VABD/OSCF (PV/grid/battery feed-in) but only as the *consumer* role, so feeding HA PV data would need custom SPINE *provider* features — see `docs/eebus-vaillant-improvements.md`.
- Follow YAGNI: build only what a current use case needs. No speculative abstractions, config knobs, or use-case scaffolding for hardware/features not yet supported.
