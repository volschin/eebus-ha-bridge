# Go-Test-Coverage: Spezifikation und Umsetzungsstand

## Ziel und Randbedingungen

Der produktive, handgeschriebene Go-Code muss eine Statement-Coverage von
mindestens **83,0 %** erreichen. Neu hinzugefügter oder geänderter produktiver
Go-Code muss in der CI mindestens **90,0 % Patch-Coverage** erreichen.

Für die Testerweiterung gelten folgende Randbedingungen:

- Produktiver Go-Code wird nicht verändert.
- Maschinell generierter Code wird weder in die Gesamt- noch in die
  Patch-Coverage einbezogen.
- Tests bleiben unabhängig von externen Geräten und externen Netzwerkdiensten.
- Lokale Netzwerk-Fixtures verwenden dynamisch vergebene Ports.
- Tests prüfen fachliche Ergebnisse, Fehler, Validierung und Zustandswechsel;
  reine Aufrufe ohne Verhaltensprüfung sind nicht ausreichend.

## Messmethode und Scope

Die reproduzierbare Gesamtmessung erfolgt ohne Test-Cache:

```bash
coverage_dir=$(mktemp -d)
go test -count=1 -coverprofile="$coverage_dir/coverage.out" \
  ./cmd/eebus-bridge ./cmd/eebus-watch ./internal/...
go tool cover -func="$coverage_dir/coverage.out"
```

Im produktiven Scope enthalten sind:

- `cmd/eebus-bridge`
- `cmd/eebus-watch`
- `internal/...`

Ausgeschlossen sind:

- `gen/proto/**`, da dieser Code aus Protobuf-Spezifikationen generiert wird;
- `cmd/eebus-contract-testserver`, da dieses Programm eine Test-Fixture für
  sprachübergreifende Vertragstests ist;
- `*_test.go` und externe Abhängigkeiten.

Zusätzlich muss `go test -count=1 ./...` erfolgreich bleiben. Race-Erkennung
wird separat mit `go test -race ./...` ausgeführt und ist nicht Bestandteil des
Coverage-Prozentwerts.

## Ausgangslage

Die Baseline vom 19. Juli 2026 betrug **59,3 %** beziehungsweise 2.906 von
4.897 Statements. Zur Zielerreichung mussten mindestens 4.065 Statements
abgedeckt werden.

## Erreichter Stand vom 19. Juli 2026 auf `origin/main`

| Paket | Abgedeckt | Statements | Coverage | Nicht abgedeckt |
|---|---:|---:|---:|---:|
| `cmd/eebus-bridge` | 386 | 481 | 80,2 % | 95 |
| `cmd/eebus-watch` | 175 | 201 | 87,1 % | 26 |
| `internal/certs` | 40 | 43 | 93,0 % | 3 |
| `internal/config` | 210 | 214 | 98,1 % | 4 |
| `internal/eebus` | 822 | 905 | 90,8 % | 83 |
| `internal/grpc` | 1.386 | 1.631 | 85,0 % | 245 |
| `internal/usecases` | 1.153 | 1.531 | 75,3 % | 378 |
| **Produktiver Scope** | **4.172** | **5.006** | **83,340 %** | **834** |

Das Ziel von 83,0 % ist damit um 17 abgedeckte Statements überschritten. Die
seit der Baseline auf `main` zusammengeführten Produktivänderungen vergrößerten
den Scope auf 5.006 Statements; der Coverage-PR selbst ändert weiterhin keinen
Produktivcode.

## Umgesetzte Testerweiterungen

Die Erweiterungen konzentrieren sich auf fachlich relevante, zuvor schwach
abgedeckte Pfade:

- `eebus-watch`: vollständiger `--once`-Ablauf, Registrierung, Snapshot-Aufbau,
  Rendering, Validierung, Abbruch und RPC-Fehler über lokale gRPC-Fixtures;
- Bridge-Anwendung: Healthcheck mit Klartext und TLS/Token,
  Produktionskomposition, Provider-Aktivierung, Startzustände, Signale und
  kontrollierte Fehlerbehandlung;
- gRPC-Adapter: LPC-Lese-/Schreiboperationen und Heartbeat, Monitoring-Power,
  Energie und Snapshots, OHPCF-Steuerung sowie End-to-End-Streams für DHW,
  HVAC und OHPCF;
- Use-Cases: Provider-Lebenszyklen, Initialisierungs-Guards, DHW- und
  Raumheizungsereignisse, Systemfunktionen, Monitoring-Delegation und
  hydraulische Temperaturen einschließlich Einheitenumrechnung;
- EEBUS-Infrastruktur: Bridge-Service-Lifecycle, Provider-Entities,
  Pairing-/Trust-Callbacks, TrustController und vollständiger SHIP-Logging-
  Adapter;
- Konfiguration, Zertifikate und Registry: Validierungs-, Fehler- und
  Zustandsverträge.

## CI-Prüfung

Der Go-CI-Job erzeugt dasselbe produktive Coverage-Profil und prüft zwei
Schwellen:

1. Die Gesamt-Coverage darf nicht unter **83,0 %** fallen.
2. Die Coverage geänderter produktiver Go-Statements muss mindestens
   **90,0 %** betragen.

Für die Patch-Coverage ermittelt `scripts/check_go_patch_coverage.py` die
geänderten Zeilen zwischen dem Ziel-Branch und `HEAD` beziehungsweise zwischen
dem vorherigen und aktuellen Push-Commit. Coverage-Blöcke, die eine geänderte
Zeile überlappen, werden anhand der Statement-Anzahl des Go-Coverage-Profils
gezählt. Ein Block wird auch bei mehreren geänderten Zeilen nur einmal gezählt.

Folgende Änderungen bleiben bei der Patch-Coverage unberücksichtigt:

- Testdateien (`*_test.go`);
- `eebus-bridge/gen/**`;
- `eebus-bridge/cmd/eebus-contract-testserver/**`;
- Änderungen ohne ausführbare Go-Statements, beispielsweise Kommentare.

Enthält ein Patch keine geänderten produktiven Go-Statements, besteht die
Patch-Prüfung. Der Checker selbst besitzt isolierte Unit-Tests für Diff-Parsing,
Scope-Ausschlüsse, Profil-Parsing, Blocküberlappung und Prozentberechnung.

## Abnahmekriterien

- [x] Der definierte produktive Scope erreicht mindestens 83,0 %.
- [x] Generierter Code und der Contract-Testserver sind ausgeschlossen.
- [x] Produktiver Go-Code blieb unverändert.
- [x] Die Tests prüfen sinnvolle Erfolgs-, Fehler-, Validierungs- und
  Ereignispfade.
- [x] `go test -count=1` ist im vollständigen produktiven Scope erfolgreich.
- [x] Die CI verhindert eine Gesamt-Coverage unter 83,0 %.
- [x] Die CI schlägt bei weniger als 90,0 % Coverage neuen oder geänderten
  produktiven Go-Codes fehl.
