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
- `eebus-bridge/` — Go daemon embedding `enbility/eebus-go` (SHIP+SPINE). Runs as separate process/container (host networking required: SHIP discovery uses `_ship._tcp` mDNS multicast which can't cross Docker bridge nets). gRPC binds `127.0.0.1` by default (`security.mode: loopback`, plaintext); a non-loopback bind is rejected at startup unless `security.mode: tls_token` is set (TLS cert + bearer token, constant-time compared on every RPC).

## The proto contract (critical)

`eebus-bridge/proto/eebus/v1/*.proto` is the single source of truth for the gRPC interface. It generates stubs for **both** sides — they must stay in sync:

- **Go** stubs → `eebus-bridge/gen/proto/` via `buf generate` (`make proto`, run from `eebus-bridge/`).
- **Python** stubs → `custom_components/eebus/generated/` via `bash generate_proto.sh` (run from repo root).

After any `.proto` change, regenerate **both**. CI's `proto-drift` job regenerates Python stubs and fails on `git diff`. Generated dirs are committed, excluded from ruff/coverage, and must not be hand-edited.

`generate_proto.sh` pins `grpcio-tools==1.78.0` and rewrites absolute `from eebus.v1 import` to relative imports so stubs load inside the `custom_components.eebus` package. Use `proto_stubs.py` re-exports, not deep imports into `generated/`.

`proto_stubs.py` re-exports need an explicit `__all__` (mypy `--strict` implies `no_implicit_reexport`) and grpc Stub classes must be constructed through its `*_service_stub(channel)` factory functions, not called directly — grpcio-tools emits untyped stub classes, so the one unavoidable `type: ignore[no-untyped-call]` per stub lives there instead of at every call site. `custom_components.eebus.generated.*` is exempted from strict mypy in `pyproject.toml` (vendored, not hand-maintained).

### grpcio version pin

HA Core pins `grpcio==1.78.0` in `package_constraints.txt`. The manifest floor (`requirements` in `manifest.json`) and `generate_proto.sh`'s `grpcio-tools` version must match HA's pin, or the embedded `GRPC_GENERATED_VERSION` exceeds HA's runtime grpcio and install fails (issue #22). `.github/workflows/grpcio-sync.yml` automates bumping these when HA changes the pin. When touching grpc deps, keep `manifest.json`, `generate_proto.sh`, and the bump workflow aligned.

## Commands

### Python dev environment
No `[project]` deps in `pyproject.toml` — it's tool config only (pytest/coverage/ruff/mypy sections). Install the same set CI installs:
```bash
pip install pytest pytest-asyncio pytest-cov ruff mypy grpc-stubs homeassistant voluptuous grpcio protobuf
```
`homeassistant` is a heavy dependency (pulls in most of HA core); expect a multi-minute install. It's needed for imports only (`DataUpdateCoordinator`, `Event`, etc.) — tests are plain unit tests against `unittest.mock.MagicMock`, not a running `hass` instance, so no `pytest-homeassistant-custom-component` fixture package is used.

### Python (run from repo root)
```bash
PYTHONPATH=. pytest                              # all tests (asyncio_mode=auto)
PYTHONPATH=. pytest custom_components/eebus/tests/test_coordinator.py::test_name  # single test
PYTHONPATH=. pytest --cov --cov-report=term-missing
ruff check custom_components/                    # lint (line-length 120, py312 target)
mypy custom_components/eebus                     # strict type check (see quality_scale.yaml: strict-typing)
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
- `coordinator.py` (`EebusCoordinator`) — central hub. Primary path is gRPC **streaming** (push); on stream failure it falls back to **polling** (`POLL_INTERVAL = 5min`) and reconnects the stream in the background. Polling only reconciles state streams can't carry (scoped energy, heartbeat, support flags). `FLAT_MEASUREMENT_TYPE_TO_KEY` maps bridge `GetMeasurements` entry types to per-phase/grid sensor keys. Grid/PV/battery provider pushes go through `_ProviderPusher` (one worker per provider): state-change callbacks only `signal()` it, it coalesces bursts into a single in-flight push — don't call `push()` directly from a callback.
- Platforms: `sensor`, `number`, `switch`, `select`, `binary_sensor`, `water_heater` — all read coordinator data; `entity.py` is the shared base. `select.eebus_compressor_flexibility` exposes OHPCF's `on`/`paused`/`off` as three distinct options — a plain switch collapses PAUSED into the same state as AVAILABLE/COMPLETED/STOPPED, losing the running-vs-stopped distinction.
- `config_flow.py` — bridge host/port + device SKI pairing. `iot_class: local_push`, `quality_scale: platinum` (see `quality_scale.yaml`).
- Tests are plain unit tests (`unittest.mock.MagicMock` for HA objects, no running `hass` instance); `conftest.py` is currently empty. Protobuf messages must be real generated types, not duck-typed namespaces — mypy/tests will reject a hand-rolled stand-in.

### Go bridge
- `cmd/eebus-bridge/main.go` — entrypoint, wires config + service + gRPC server. `cmd/eebus-watch/` is a standalone terminal live-viewer for manual testing against a running bridge (see README).
- `internal/eebus/` — `service.go` embeds eebus-go; `registry.go` (DeviceRegistry, shared across use cases), `eventbus.go` (fan-out to gRPC streams), `callbacks.go` (SPINE event handlers), `trust.go` (`TrustController` — pairing register/unregister calls the bridge synchronously and maps errors to gRPC status; only `connected`/`disconnected`/`trust_removed` stay as bus observations, not commands).
- `internal/usecases/` — one file per EEBUS use case, split by role:
  - **Consumer** (bridge reads from the device): `lpc.go` (Limitation of Power Consumption, §14a), `monitoring.go` (measurements), `classification.go` (device mfr/model from EEBUS DeviceClassification), `ohpcf.go` (Optimization of Self-Consumption by Heat Pump Compressor Flexibility — CEM client, schedule/pause/resume/abort).
  - **Provider** (bridge feeds data *to* the device, sourced from HA sensors) — all experimental/off-by-default, see `docs/eebus-vaillant-improvements.md`: `mgcp.go` (grid connection point — power/energy), `vapd.go` (aggregated PV data), `vabd.go` (aggregated battery data).
  - eebus-go validates entity compatibility **per use case**, not just registry lookup (issue #47). `CompatibleEntity`/`compatibleEntity` return an `EntityResolution` (entity + distinct-device count): empty SKI resolves only when exactly one compatible device exists, else every gRPC service reports `FAILED_PRECONDITION` instead of silently picking the first device (RF-02).
- `internal/grpc/` — service impls, one per proto service (`device_service.go`, `lpc_service.go`, `monitoring_service.go`, `ohpcf_service.go`, `grid_service.go` (MGCP), `visualization_service.go` (VAPD/VABD)) + `server.go` + `security.go` (enforces `loopback`/`tls_token` mode on every unary/stream RPC, including health/reflection).
- `internal/certs/` — auto-generated TLS certs define the bridge SKI. Deleting them changes the SKI and forces re-pairing.

## Conventions

- Releases are tag-triggered: pushing a `v*` tag builds multi-arch (amd64/arm64/armv7) Docker images to GHCR. `release-drafter` drafts notes on merge to main. **Bumping `version` in `manifest.json` and pushing the `v*` tag is a maintainer release step, done in its own commit after a PR merges — don't bump it as part of a feature/fix PR.**
- CI skips Go/proto jobs via path filters when only the other side changed. `paths-ignore` skips `**/*.md`.
- `docs/refactoring-optimization-spec.md` tracks RF-01..RF-10, priority-ordered structural work; RF-01..RF-06 and RF-08 (security, device-identity isolation, pairing off the event bus, provider-push coalescing, Python lifecycle split into `grpc_client.py`/`streams.py`/`models.py`/`snapshot.py`/`providers.py`, per-device watchdog, Go `Application` lifecycle extraction in `cmd/eebus-bridge/app.go`) are done, RF-07/RF-09/RF-10 (proto CI hardening, config/doc hardening, further Go dedup) are open.
- `docs/refactoring-optimization-spec-v2.md` (SPEC2-01..13, code-derived, supersedes RF-* as the factual basis going forward) tracks a separate priority-ordered list starting from semantic correctness. SPEC2-04 (one canonical-SKI normalizer per language, `ski.py`/`eebus.NormalizeSKI`, config-entry migration to version 2), SPEC2-01 (device-level `connected`/`last_transition` split from bridge `GetStatus`, threadsafe registry-backed per-SKI connection tracking; `device_ready` deferred to SPEC2-12), and SPEC2-02 (LPC heartbeat is bridge-lifecycle-scoped — starts/stops exactly once with the bridge, not per HA device entry; the per-device `switch.eebus_heartbeat` is removed, `StartHeartbeat`/`StopHeartbeat` RPCs marked deprecated) are done; SPEC2-03/05..13 are open.
- HVAC/DHW setpoint+mode control **shipped** (`climate.eebus_room_heating`: setpoint + `auto`/`on`/`off`; DHW boost switch + mode select) via a `replace` directive in `eebus-bridge/go.mod` pointing at a `volschin/eebus-go` fork carrying the configurationOf*/setpoint use cases upstream doesn't have — Renovate is disabled for that package (`renovate.json`) since a digest bump would silently drop the patches. Cooling, schedules, and `hvac_action` are still not offered by the VR940.
- Follow YAGNI: build only what a current use case needs. No speculative abstractions, config knobs, or use-case scaffolding for hardware/features not yet supported.
- `custom_components/eebus/quality_scale.yaml` is not decorative — it's checked against HA's [quality scale rules](https://www.home-assistant.io/docs/quality_scale/). Currently platinum (all rules `done` or `exempt`, see comments for the exempt reasoning). A PR that adds a new entity, config-flow step, or exception path should update the relevant rule/comment, not silently leave it stale.

## Delegating implementation to Codex

Implementation tasks are generally delegated to Codex; Claude acts as architect and reviewer. Standard flow:

1. **Claude** writes the requirements/design (what to build, constraints, acceptance criteria).
2. `/codex:rescue --background <task>` — Codex implements.
3. `/codex:result` — fetch the result.
4. **Claude** verifies: inspect `git diff`, run the relevant tests/linters.
5. `/codex:review --base main` — independent Codex review of the change.
6. **Claude** fixes review findings directly, or delegates them back with `/codex:rescue --resume`.

## Before opening a PR

`ci.yml` runs ruff, mypy, pytest+coverage, `go vet`+`golangci-lint`+`go test -race`+`govulncheck`, hadolint (Dockerfile), and `proto-drift` if `.proto` files changed; `hassfest.yml` and `hacs.yml` separately validate the HA integration's manifest/structure. Run the relevant subset locally first — a red CI run on someone else's PR is a slow way to find out ruff/mypy/`go vet` would have caught it in ten seconds:
- Python changes: `ruff check custom_components/`, `mypy custom_components/eebus`, `PYTHONPATH=. pytest`.
- Go changes: `go vet ./...` and `make test`, from `eebus-bridge/`.
- `.proto` changes: regenerate **both** stub sets (`make proto` in `eebus-bridge/`, `bash generate_proto.sh` from repo root) and commit the diff — `proto-drift` CI fails otherwise.
- New/changed config knob, entity, or service call: check whether `quality_scale.yaml`, README (feature list / troubleshooting), or `docs/` need a matching update.

## Hardware testing gotcha

Vaillant EEBUS gateways accept only **one** active EEBUS/SHIP connection at a time. If you're testing against real hardware and the bridge can't reach `Trusted` state (endless reconnect loop), check first whether another energy manager (e.g. the myVAILLANT sensoNET cloud client) is already holding that slot — this is a device limitation, not a bug in this code, and burns a lot of debugging time if you don't know about it. Enable `logging.ship_log: true` (see README Troubleshooting) to confirm from the abort reason before digging further.
