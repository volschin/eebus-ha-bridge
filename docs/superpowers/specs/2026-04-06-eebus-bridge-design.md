# EEBUS Bridge — Design-Dokument

**Datum:** 2026-04-06
**Ziel:** Lokale Integration einer Vaillant aroTHERM plus Wärmepumpe in Home Assistant über EEBUS, mittels eines Go-basierten Bridge-Dienstes und einer schlanken HA Custom Integration.

---

## 1. Kontext & Motivation

Vaillant-Wärmepumpen wie die aroTHERM plus kommunizieren intern über eBUS, stellen aber für externe Energiemanagement-Systeme eine EEBUS-Schnittstelle über Internet-Gateways (VR920/VR921/VR940f) bereit. EEBUS ist ein offenes, IP-basiertes Protokoll-Set (SHIP + SPINE), das Lastmanagement-Use-Cases standardisiert.[^1][^2]

Es existiert kein produktionsreifer Python-EEBUS-Stack. Die Go-Bibliothek eebus-go der enbility-Organisation ist die ausgereifteste Open-Source-Implementierung.[^3][^4]

### Ist-Zustand

- **VR940f Gateway** ist bestellt, EEBUS wird darüber aktiviert
- **mypyllant** HACS-Integration ist im Einsatz für Betriebsmodi und Sollwerte (Cloud-basiert)
- **Danfoss TLX Pro** HACS-Integration liefert PV-Daten
- Ziel ist Cloud-Unabhängigkeit — lokale EEBUS-Steuerung wird bevorzugt

### Was die VR940f tatsächlich über EEBUS exponiert

Aus realen Discovery-Daten des enbility/devices-Repositories[^5] und Praxistests in der evcc-Community[^6]:

**Verfügbar:**
- LPC (Limitation of Power Consumption) — Wärmepumpe akzeptiert Leistungslimits
- Basis-Leistungsmessung — Stromverbrauch ca. jede Minute

**Nicht verfügbar:**
- Betriebsmodi, Sollwerte, HVAC-Features
- Das Gerät meldet sich als `Generic` (nicht `HeatPumpAppliance`) und kündigt keine Use Cases öffentlich an[^5]

Die EEBUS-Spezifikation definiert HVAC-Use-Cases (CDSF, CRHSF, CDT, OHPCF etc.), Vaillant implementiert diese aber aktuell nicht. HVAC/Setpoint-Features fehlen auch im Open-Source eebus-go Stack.[^7]

### Konsequenz

Der EEBUS-Bridge fokussiert sich initial auf LPC + Monitoring. Betriebsmodi und Sollwerte laufen weiterhin über mypyllant. Die Architektur ist so aufgebaut, dass HVAC-Use-Cases ergänzt werden können, sobald Vaillant diese in zukünftiger Firmware nachrüstet.

---

## 2. Systemarchitektur

```
┌──────────────────────────────────────────────────────────┐
│                    Home Assistant                         │
│                                                          │
│  ┌─────────────┐  ┌──────────────┐  ┌────────────────┐  │
│  │ Danfoss TLX │  │ eebus custom │  │ HA Automations │  │
│  │ (PV-Daten)  │  │ integration  │  │ (PV-Logik)     │  │
│  └──────┬──────┘  └──────┬───────┘  └───────┬────────┘  │
│         │                │ gRPC              │           │
│         │                │                   │           │
│         └────sensors─────┤◄──commands────────┘           │
│                          │                               │
│  ┌───────────────┐       │                               │
│  │ mypyllant     │       │  (Betriebsmodi, Sollwerte     │
│  │ (Cloud)       │       │   bis Vaillant EEBUS-HVAC     │
│  └───────────────┘       │   nachrüstet)                 │
└──────────────────────────┼───────────────────────────────┘
                           │ gRPC (Protobuf)
                           │
              ┌────────────▼────────────────┐
              │     eebus-bridge (Go)        │
              │                              │
              │  ┌────────────────────────┐  │
              │  │  gRPC Server Layer     │  │
              │  │  - DeviceService       │  │
              │  │  - LPCService          │  │
              │  │  - MonitoringService   │  │
              │  └────────┬───────────────┘  │
              │           │                  │
              │  ┌────────▼───────────────┐  │
              │  │  Use Case Manager      │  │
              │  │  (maps gRPC ↔ eebus)   │  │
              │  └────────┬───────────────┘  │
              │           │                  │
              │  ┌────────▼───────────────┐  │
              │  │  eebus-go              │  │
              │  │  (SHIP + SPINE)        │  │
              │  └────────┬───────────────┘  │
              │           │                  │
              │  ┌────────▼───────────────┐  │
              │  │  Cert Store            │  │
              │  │  (auto-gen / override) │  │
              │  └────────────────────────┘  │
              └────────────┬────────────────┘
                           │ WebSocket/TLS (SHIP)
              ┌────────────▼────────────────┐
              │  Vaillant VR940f Gateway    │
              │  (aroTHERM plus als CS)     │
              └─────────────────────────────┘
```

### Drei Komponenten

1. **eebus-bridge** — Go-Dienst, ein Container/Binary. Bettet eebus-go ein, exponiert gRPC-Services. Agiert als HEMS (Energy Guard + Monitoring Appliance Rollen).

2. **eebus HA Custom Integration** — Python-Paket. Config Flow für Discovery/Pairing, Entity-Mapping (sensor, number, switch, binary_sensor), kommuniziert ausschließlich über gRPC mit dem Bridge.

3. **HA Automations** — PV-geführte Steuerung bleibt komplett in HA. Nutzt PV-Sensoren (Danfoss) + Wärmepumpen-Entities (aus eebus- und mypyllant-Integration) um Eigenverbrauch zu optimieren.

### EEBUS-Rollen

- Vaillant-Wärmepumpe: **Controllable System (CS)** — empfängt Limits, liefert Messwerte
- eebus-bridge: **Energy Guard (EG)** für LPC, **Monitoring Appliance (MA)** für Messwerte

### Deployment

- Primär: Docker-Container auf demselben Host wie Home Assistant
- Zukunft: Kubernetes-kompatibel (12-Factor Config, Health-Checks, kein lokaler State außer Volume)

---

## 3. gRPC API Design

Ein einzelner gRPC-Server auf einem Port (Abweichung von eebus-grpc's dynamischen Per-Use-Case-Ports[^8] — einfacher zu konfigurieren, firewall-/K8s-freundlich).

### Protobuf-Struktur

```
proto/
  eebus/v1/
    common.proto              # Gemeinsame Typen
    device_service.proto      # Discovery, Pairing, Lifecycle
    lpc_service.proto         # Leistungsbegrenzung (§14a)
    monitoring_service.proto  # Messwerte
    # hvac_service.proto      # Zukunft: wenn Vaillant HVAC-UCs nachrüstet
```

### DeviceService — Discovery, Pairing, Lifecycle

```protobuf
syntax = "proto3";
package eebus.v1;

service DeviceService {
  rpc GetStatus(Empty) returns (ServiceStatus);
  rpc ListDiscoveredDevices(Empty) returns (ListDevicesResponse);
  rpc RegisterRemoteSKI(RegisterSKIRequest) returns (Empty);
  rpc UnregisterRemoteSKI(SKIRequest) returns (Empty);
  rpc GetPairingStatus(SKIRequest) returns (PairingStatus);
  rpc ListPairedDevices(Empty) returns (ListPairedDevicesResponse);
  rpc SubscribeDeviceEvents(Empty) returns (stream DeviceEvent);
}
```

`ListPairedDevicesResponse` liefert pro Gerät: SKI, Hersteller, Modell, Liste der unterstützten Use Cases (aus NodeManagementUseCaseData). Ermöglicht der HA-Integration, dynamisch Entities für verfügbare Use Cases anzulegen.

Kein FSM-basierter SetConfig/StartService-Flow wie bei eebus-grpc[^8]. Der Bridge startet mit Config-File/Env-Vars und ist sofort bereit.

### LPCService — Leistungsbegrenzung (§14a)

```protobuf
service LPCService {
  rpc GetConsumptionLimit(DeviceRequest) returns (LoadLimit);
  rpc WriteConsumptionLimit(WriteLoadLimitRequest) returns (Empty);
  rpc GetFailsafeLimit(DeviceRequest) returns (FailsafeLimit);
  rpc WriteFailsafeLimit(WriteFailsafeLimitRequest) returns (Empty);
  rpc StartHeartbeat(DeviceRequest) returns (Empty);
  rpc StopHeartbeat(DeviceRequest) returns (Empty);
  rpc GetHeartbeatStatus(DeviceRequest) returns (HeartbeatStatus);
  rpc GetConsumptionNominalMax(DeviceRequest) returns (PowerValue);
  rpc SubscribeLPCEvents(DeviceRequest) returns (stream LPCEvent);
}
```

### MonitoringService — Messwerte

```protobuf
service MonitoringService {
  rpc GetPowerConsumption(DeviceRequest) returns (PowerMeasurement);
  rpc GetMeasurements(DeviceRequest) returns (MeasurementList);
  rpc SubscribeMeasurements(DeviceRequest) returns (stream MeasurementEvent);
}
```

### Gemeinsame Typen (common.proto)

```protobuf
message DeviceRequest {
  string ski = 1;
  repeated uint32 entity_address = 2;
}

message LoadLimit {
  double value_watts = 1;
  int64 duration_seconds = 2;
  bool is_active = 3;
  bool is_changeable = 4;
}

message PowerMeasurement {
  double watts = 1;
  google.protobuf.Timestamp timestamp = 2;
}

message MeasurementEntry {
  string type = 1;
  double value = 2;
  string unit = 3;
  google.protobuf.Timestamp timestamp = 4;
}
```

### API Design-Entscheidungen

| Entscheidung | Gewählt | Begründung |
|---|---|---|
| Ein vs. mehrere gRPC-Ports | Ein Port | Einfacher als eebus-grpc; firewall-/K8s-freundlich |
| Lifecycle-API (SetConfig/Start/Stop) | Nein | Bridge startet mit Config, kein FSM nötig |
| Use-Case-Discovery | Ja, via ListPairedDevices | Dynamische Entity-Erstellung, auch für zukünftige UCs |
| Streaming vs. Polling | Server-Streaming für Events | eebus-go ist event-driven; Streaming bildet das natürlich ab |

---

## 4. Go-Dienst — Interne Architektur

### Verzeichnisstruktur

```
eebus-bridge/
├── cmd/
│   └── eebus-bridge/
│       └── main.go                # Entrypoint, Config laden, Services starten
├── internal/
│   ├── config/
│   │   └── config.go              # YAML/Env-Config, Zertifikatspfade
│   ├── eebus/
│   │   ├── service.go             # eebus-go Service-Wrapper, Lifecycle
│   │   ├── callbacks.go           # ServiceReaderInterface Impl (Pairing, Events)
│   │   ├── certificates.go        # Auto-Gen + Override-Logik
│   │   └── usecases/
│   │       ├── registry.go        # Use-Case-Registry (erweiterbar)
│   │       ├── lpc.go             # EG-LPC Use Case Wrapper
│   │       └── monitoring.go      # MA-MPC Use Case Wrapper
│   └── grpc/
│       ├── server.go              # gRPC Server Setup, Reflection, Health
│       ├── device_service.go      # DeviceService Implementation
│       ├── lpc_service.go         # LPCService Implementation
│       └── monitoring_service.go  # MonitoringService Implementation
├── proto/
│   └── eebus/v1/
│       ├── common.proto
│       ├── device_service.proto
│       ├── lpc_service.proto
│       └── monitoring_service.proto
├── Dockerfile
├── docker-compose.yml
├── go.mod
└── go.sum
```

### Kernfluss

```
                    eebus-go Callbacks
                         │
          ┌──────────────▼──────────────┐
          │       callbacks.go          │
          │  ServiceReaderInterface     │
          │  - RemoteSKIConnected()     │
          │  - AllowWaitingForTrust()   │
          │  - Use Case Event Handler   │
          └──────┬──────────┬───────────┘
                 │          │
    ┌────────────▼┐   ┌────▼──────────┐
    │ usecases/   │   │ Event Bus     │
    │ registry    │   │ (Go channels) │
    │             │   │               │
    │ lpc.go      │   │ fan-out to    │
    │ monitoring  │   │ gRPC streams  │
    └──────┬──────┘   └──────┬────────┘
           │                 │
    ┌──────▼─────────────────▼──────┐
    │        gRPC Service Layer     │
    │                               │
    │  Unary RPCs → usecase methods │
    │  Stream RPCs ← event bus      │
    └───────────────────────────────┘
```

### Use-Case-Registry

Neue Use Cases werden registriert, ohne bestehenden Code zu ändern:

```go
type UseCase interface {
    Name() string
    Setup(localEntity spine.EntityLocalInterface) error
    HandleEvent(ski string, event api.EventType)
}

type Registry struct {
    useCases map[string]UseCase
}
```

### Event Bus

eebus-go liefert Events über Callbacks. Ein interner Event Bus (Go Channels) verteilt diese per Fan-Out an alle aktiven gRPC-Streams. Jeder `Subscribe*`-RPC öffnet einen Channel am Bus.

### Konfiguration

YAML-File + Env-Overrides (12-Factor-kompatibel):

```yaml
grpc:
  port: 50051

eebus:
  port: 4712
  vendor: "HomeAssistant"
  brand: "eebus-bridge"
  model: "eebus-bridge"
  serial: "ha-001"

certificates:
  auto_generate: true
  cert_file: ""
  key_file: ""
  storage_path: "/data/certs"
```

### Zertifikats-Logik

1. Wenn `cert_file` + `key_file` gesetzt → laden (K8s Secrets / Mount)
2. Sonst wenn Zertifikat in `storage_path` existiert → laden (Persistenz über Restarts)
3. Sonst → generieren via `ship-go/cert`, in `storage_path` speichern

### Abhängigkeiten

| Dependency | Zweck |
|---|---|
| `enbility/eebus-go` | EEBUS Use Cases (EG-LPC, MA-MPC) |
| `enbility/ship-go` | SHIP Transport, Zertifikate, mDNS |
| `enbility/spine-go` | SPINE Data Model, Features |
| `google.golang.org/grpc` | gRPC Server |
| `google.golang.org/protobuf` | Protobuf Runtime |
| `gopkg.in/yaml.v3` | Config Parsing |

---

## 5. HA Custom Integration

### Verzeichnisstruktur

```
custom_components/
└── eebus/
    ├── __init__.py              # Integration Setup, gRPC-Verbindung
    ├── manifest.json            # Integration Metadata
    ├── config_flow.py           # Discovery + Pairing UI
    ├── coordinator.py           # DataUpdateCoordinator, gRPC-Stream-Listener
    ├── const.py                 # Konstanten
    ├── entity.py                # Basis-Entity-Klasse
    ├── sensor.py                # Leistung, Energie
    ├── number.py                # LPC Limit, Failsafe
    ├── switch.py                # LPC aktiv, Heartbeat
    ├── binary_sensor.py         # Verbindungsstatus, Heartbeat-Warnung
    ├── strings.json             # UI-Texte
    └── translations/
        ├── de.json
        └── en.json
```

### Config Flow

1. **Bridge-Verbindung** — Host + Port eingeben, `GetStatus()` als Verbindungstest
2. **Gerät auswählen** — `ListDiscoveredDevices()` zeigt EEBUS-Geräte im Netz
3. **Pairing** — `RegisterRemoteSKI()`, Polling auf `GetPairingStatus()` bis Trust hergestellt, User bestätigt in myVaillant-App
4. **Fertig** — Verfügbare Use Cases anzeigen, Entities anlegen

### Coordinator

```python
class EebusCoordinator(DataUpdateCoordinator):
    """Hält gRPC-Verbindung und verteilt Daten an Entities."""
    # Zwei Datenquellen parallel:
    # 1. gRPC Streaming (SubscribeMeasurements, SubscribeLPCEvents)
    #    → async Tasks die Events empfangen und async_set_updated_data() aufrufen
    # 2. Polling-Fallback (GetPowerConsumption, GetConsumptionLimit)
    #    → falls Stream abbricht, Fallback auf _async_update_data() Intervall
```

### Entity-Mapping

| Entity | Platform | Datenquelle | Beschreibung |
|---|---|---|---|
| `sensor.eebus_power_consumption` | sensor | MonitoringService | Aktuelle elektr. Leistung (W) |
| `sensor.eebus_energy_consumed` | sensor | MonitoringService | Zählerstand (kWh), `state_class: total_increasing` |
| `sensor.eebus_consumption_limit` | sensor | LPCService | Aktuell gesetztes Limit (W), readonly |
| `number.eebus_lpc_limit` | number | LPCService | Limit setzen (W), min=0, max=nominal_max |
| `number.eebus_failsafe_limit` | number | LPCService | Failsafe-Grenze (W) |
| `switch.eebus_lpc_active` | switch | LPCService | Limit aktivieren/deaktivieren |
| `switch.eebus_heartbeat` | switch | LPCService | Heartbeat an/aus |
| `binary_sensor.eebus_connected` | binary_sensor | DeviceService | EEBUS-Verbindungsstatus |
| `binary_sensor.eebus_heartbeat_ok` | binary_sensor | LPCService | Heartbeat innerhalb Toleranz |

### Erweiterbarkeit

Die Integration fragt `ListPairedDevices()` ab und legt Entities nur für verfügbare Use Cases an:

```python
async def async_setup_entry(hass, entry):
    device = await client.get_paired_device(entry.data["ski"])
    platforms = ["binary_sensor", "sensor"]
    if "lpc" in device.supported_use_cases:
        platforms.extend(["number", "switch"])
    # Zukunft:
    # if "hvac" in device.supported_use_cases:
    #     platforms.append("climate")
    await hass.config_entries.async_forward_entry_setups(entry, platforms)
```

### manifest.json

```json
{
  "domain": "eebus",
  "name": "EEBUS",
  "version": "0.1.0",
  "requirements": ["grpcio>=1.60.0", "grpcio-tools>=1.60.0", "protobuf>=4.25.0"],
  "dependencies": [],
  "iot_class": "local_push"
}
```

---

## 6. Fehlerbehandlung & Resilienz

### Fehlerkategorien

| Fehlerfall | Erkennung | Reaktion |
|---|---|---|
| Bridge nicht erreichbar | gRPC `UNAVAILABLE` | HA: Entities `unavailable`, Reconnect mit Exponential Backoff (1s→60s max) |
| EEBUS-Gerät offline | DeviceEvent `DISCONNECTED` | HA: `binary_sensor.connected` = off, Sensoren behalten letzten Wert |
| Heartbeat-Timeout | LPCEvent `HEARTBEAT_TIMEOUT` | HA: `binary_sensor.heartbeat_ok` = off, Persistent Notification. WP fällt auf Failsafe zurück |
| Pairing verloren | DeviceEvent `TRUST_REMOVED` | HA: Entities `unavailable`, Config-Flow-Repair-Hinweis |
| Stream-Abbruch | gRPC Stream Error | HA: Fallback auf Polling (30s), Stream-Reconnect im Hintergrund |
| Zertifikat ungültig | Bridge Startup-Fehler | Bridge startet nicht, Log-Meldung. Bei Auto-Gen: neues Zertifikat → neuer SKI → Re-Pairing nötig |

### Heartbeat — Kritischer Pfad

Der EEBUS-Heartbeat ist sicherheitsrelevant (§14a). Der Heartbeat läuft im Bridge, nicht in HA. Bei Bridge-Ausfall erkennt die WP den Timeout (max 2 min) und fällt auf den Failsafe-Wert zurück. Bei HA-Neustart läuft der Heartbeat weiter.

### Persistenz

Der Bridge speichert in `/data/`:
- `certs/` — TLS-Zertifikat + Key
- Pairing-State wird von eebus-go intern verwaltet (gleicher `storage_path`)

---

## 7. Testing-Strategie

### Go-Dienst

**Unit Tests** pro Package:

| Package | Was wird getestet | Mock-Strategie |
|---|---|---|
| `internal/config` | YAML-Parsing, Env-Overrides, Defaults | Keine Mocks |
| `internal/eebus/certificates` | Auto-Gen, Laden, Fallback | Temp-Dir |
| `internal/eebus/usecases` | Event-Routing, Daten-Mapping | eebus-go Interfaces mocken |
| `internal/eebus/callbacks` | Pairing-Flow, Event-Dispatch | Mock-Service, Mock-EventBus |
| `internal/grpc` | Request→UseCase→Response | Mock-UseCases, in-process gRPC |

**Integration Tests:** Echter gRPC-Server mit gemockten Use Cases. Prüft Protobuf-Serialisierung, Stream-Verhalten, Error Codes.

### HA Integration

**Unit Tests** mit `pytest` + `pytest-homeassistant-custom-component`. gRPC wird gemockt.

### CI Pipeline

```
Lint → Unit Tests → Integration Tests → Docker Build
(go vet, golangci, ruff)  (go test, pytest)  (gRPC E2E)
```

Kein Hardware-Test in CI. Echte Geräte-Tests manuell mit VR940f.

---

## 8. Offene Punkte & Zukunft

- **HVAC-Use-Cases:** Sobald Vaillant CDSF, CRHSF, CDT o.ä. über EEBUS exponiert, kann ein `HVACService` im Bridge und ein `climate`-Entity in der HA-Integration ergänzt werden. Die Use-Case-Registry und das dynamische Entity-Mapping sind dafür vorbereitet.
- **Weitere Geräte:** Die Architektur ist nicht Vaillant-spezifisch. Weitere EEBUS-Geräte (Wallbox, Batterie) können über dieselbe Bridge angebunden werden.
- **eebus-go HVAC-Features:** PR #122 im eebus-go-Repo (OHPCF-Support) wurde geschlossen, weil HVAC/Setpoint-SPINE-Features fehlen.[^7] Fortschritte dort ermöglichen Erweiterungen hier.

---

## Quellen

[^1]: [EEBUS — Wikipedia](https://en.wikipedia.org/wiki/EEBUS) — Übersicht zum EEBUS-Protokoll-Set (SHIP + SPINE).

[^2]: [Support for EEBUS / SHIP / SPINE protocols — HA Feature Requests](https://community.home-assistant.io/t/support-for-eebus-ship-spine-protocols/352381) — Community-Diskussion zu fehlendem Python-Stack und Architekturansätzen.

[^3]: [enbility — GitHub](https://github.com/enbility) — Organisation hinter eebus-go, ship-go, spine-go. Open-Source EEBUS-Implementierung in Go.

[^4]: [enbility — Open Source EEBUS libraries released](https://enbility.net/blog/20221205-introduction/) — Einführungsbeitrag zu eebus-go mit Use-Case-Übersicht.

[^5]: [enbility/devices — Vaillant Arotherm Discovery-Daten](https://github.com/enbility/devices) — Reale EEBUS-Discovery-Daten. Vaillant-Geräte melden sich als `Generic`, kündigen keine Use Cases öffentlich an, exponieren keine HVAC-Features.

[^6]: [Vaillant: Dimmen gemäß §14a EnWG via EEBUS LPC — evcc Discussion #19662](https://github.com/evcc-io/evcc/discussions/19662) — Praxistest: LPC funktioniert mit Vaillant aroTHERM plus, Leistungsmessung verfügbar.

[^7]: [eebus-go PR #122 — OHPCF Support (geschlossen)](https://github.com/enbility/eebus-go/pull/122) — Versuch, HVAC-Optimierungs-Use-Case zu implementieren. Geschlossen weil HVAC/Setpoint-SPINE-Features im Stack fehlen.

[^8]: [enbility/eebus-grpc — GitHub](https://github.com/enbility/eebus-grpc) — Referenzprojekt für gRPC-Bridge über eebus-go. v0.1.0, experimentell. Nutzt dynamische Per-Use-Case-Ports und FSM-Lifecycle. API-Struktur und Protobuf-Schema als Inspiration genutzt.

[^9]: [Vaillant Wärmepumpe via Internetmodul für EEBus — gridX Support](https://support.gridx.de/hc/de/articles/29036980739602) — Inbetriebnahmeanleitung für EEBUS-Kopplung mit VR920/VR921/VR940f.

[^10]: [evcc Issue #23930 — EEBUS LPC für §14a](https://github.com/evcc-io/evcc/issues/23930) — Bestätigt: MPC ist Pflicht für SteuVE unter §14a, Vaillant liefert Leistungsdaten, aber formale Use-Case-Ankündigung fehlt.
