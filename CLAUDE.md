# CLAUDE.md — EEBUS Bridge → Home Assistant

## Projektstruktur

```
eebus-ha-bridge/
├── eebus-bridge/          # Go-Bridge (EEBUS ↔ gRPC)
│   ├── cmd/eebus-bridge/  # main.go
│   ├── internal/
│   │   ├── certs/         # TLS-Zertifikat-Management
│   │   ├── config/        # YAML+Env-Konfiguration
│   │   ├── eebus/         # EEBUS-Service-Wrapper (eebus-go)
│   │   ├── grpc/          # gRPC-Server + Service-Implementierungen
│   │   └── usecases/      # LPC + Monitoring Use-Case-Wrapper
│   ├── proto/             # Protobuf-Schemas
│   └── Makefile
├── custom_components/eebus/  # HA-Integration (Python)
│   ├── coordinator.py     # DataUpdateCoordinator + gRPC-Streaming
│   ├── config_flow.py     # Config Flow + Reconfigure
│   ├── entity.py          # Basis-Entity mit runtime_data-Pattern
│   ├── sensor.py / number.py / switch.py / binary_sensor.py
│   ├── diagnostics.py     # Gold-Scale: Diagnose-Export
│   ├── generated/         # Auto-generierter gRPC-Python-Code (nicht editieren)
│   ├── proto_stubs.py     # Re-Exports aus generated/ mit # noqa: F401
│   └── tests/
├── .github/workflows/
│   ├── ci.yml             # Go + Python-Tests + release-draft
│   ├── hacs.yml           # HACS-Validierung (separate Pflicht-Datei)
│   └── hassfest.yml       # Hassfest-Validierung (separate Pflicht-Datei)
├── generate_proto.sh      # Protobuf-Codegen (Go + Python)
├── pyproject.toml         # pytest + coverage + ruff Konfiguration
└── hacs.json
```

## Build & Test

### Go-Bridge

```bash
cd eebus-bridge
make build          # → bin/eebus-bridge
make test           # go test -v -race ./...
make lint           # go vet ./...
make proto          # buf generate (Protobuf-Codegen)

# Integration-Test (erwartet laufenden gRPC-Server):
go test -tags=integration -v ./internal/grpc/ -run TestIntegration
```

### Python-Integration (HA)

```bash
# Abhängigkeiten (kein venv nötig für CI, lokal empfohlen):
pip install pytest pytest-asyncio pytest-cov ruff homeassistant voluptuous grpcio protobuf

# Tests:
PYTHONPATH=. pytest --cov --cov-report=term-missing -v

# Linting:
ruff check custom_components/
```

### Protobuf neu generieren

```bash
./generate_proto.sh   # Go + Python aus eebus-bridge/proto/
```

## Architektur-Entscheidungen

- **Monolithische Go-Bridge:** eebus-go, ship-go, spine-go sind direkt eingebettet — kein separater EEBUS-Daemon.
- **gRPC als IPC:** Bridge ↔ HA kommunizieren über gRPC (Port 50051). Kein MQTT, kein REST.
- **Use-Cases:** Nur EG-LPC (Lastbegrenzung) und MA-MPC (Messung) implementiert. HVAC intentionally ausgelassen — Vaillant exponiert es nicht via EEBUS.
- **PV-Logik bleibt in HA:** Die Bridge setzt nur Limits, HA-Automationen entscheiden wann und wie viel.
- **Pairing via HA Config Flow:** SKI-Eingabe im Config Flow, Bestätigung in der myVaillant-App.

## eebus-go API-Eigenheiten

- `ServiceReaderInterface` erfordert 5 Methoden: `RemoteSKIConnected`, `RemoteSKIDisconnected`, `ServiceShipIDUpdate`, `ServicePairingDetailUpdate`, `CemOnEntities` — alle müssen implementiert sein.
- Kein Heartbeat auf LPC-Ebene in eebus-go — Heartbeat wird separat über MA-MPC gehandelt.
- `RemoteService.Ski()` gibt lowercase zurück — beim SKI-Vergleich case-insensitiv arbeiten oder normalisieren.
- Mutex für Listener-Registrierung nötig (Race Condition zwischen gRPC-Stream-Subscription und Event-Dispatch).
- 50ms Sleep im Streaming-Loop nötig, damit der Server-Goroutine die Subscription registriert, bevor Events gefeuert werden.

## CI-Workflow-Design

`hacs.yml` und `hassfest.yml` sind **separate Pflicht-Dateien** (HACS/HA-Richtlinien) — nicht in `ci.yml` einbetten.

`ci.yml` enthält:
- `changes` — Path-Filter (dorny/paths-filter), Go-Jobs nur bei Änderungen in `eebus-bridge/`
- `go` — vet, test, integration-test, build
- `docker` — Build-Check, nur wenn `go` erfolgreich
- `test` — Python-Tests + ruff für 3.12 und 3.13
- `release-draft` — wartet via `int128/wait-for-workflows-action` auf HACS + Hassfest, dann release-drafter

## Wichtige Konventionen

- `generated/`-Verzeichnis nie manuell editieren; aus ruff-Prüfung ausgeschlossen.
- `proto_stubs.py` Re-Exports immer mit `# noqa: F401` markieren.
- `PARALLEL_UPDATES = 0` inline nach allen Imports definieren (nicht aus const importieren), um E402 zu vermeiden.
- Tests patchen keine HA-Interna — `EebusCoordinator` wird per `__new__` direkt instanziiert.
- Coverage-Source via `source_pkgs = ["custom_components.eebus"]` in `pyproject.toml`.
