# Go-Test-Coverage: Zielbild und Erweiterungsspezifikation

## Ziel

Der produktive, handgeschriebene Go-Code soll eine Statement-Coverage von
mindestens 95,0 % erreichen. Produktiver Code darf dafür nicht verändert
werden; die Umsetzung erfolgt ausschließlich durch zusätzliche oder erweiterte
Tests und Test-Fixtures.

## Messmethode und Scope

Die reproduzierbare Messung erfolgt ohne Test-Cache:

```bash
coverage_dir=$(mktemp -d)
go test -count=1 -coverprofile="$coverage_dir/coverage.out" \
  ./cmd/eebus-bridge ./cmd/eebus-watch ./internal/...
go tool cover -func="$coverage_dir/coverage.out"
```

Im Ziel-Scope enthalten sind:

- `cmd/eebus-bridge`
- `cmd/eebus-watch`
- `internal/...`

Nicht im Ziel-Scope enthalten sind:

- `gen/proto/**`, weil dieser Code aus den Protobuf-Spezifikationen generiert
  wird;
- `cmd/eebus-contract-testserver`, weil dieses Programm selbst ausschließlich
  eine Fixture für sprachübergreifende Vertragstests ist;
- externe Abhängigkeiten.

Zusätzlich zur Coverage-Messung muss `go test -count=1 ./...` erfolgreich
bleiben. Race-Erkennung wird separat mit `go test -race ./...` ausgeführt und
ist kein Bestandteil des Coverage-Prozentwerts.

## Baseline vom 19. Juli 2026

Alle Tests waren bei der Baseline-Messung erfolgreich.

| Paket | Abgedeckt | Statements | Coverage | Nicht abgedeckt |
|---|---:|---:|---:|---:|
| `cmd/eebus-bridge` | 269 | 476 | 56,5 % | 207 |
| `cmd/eebus-watch` | 24 | 201 | 11,9 % | 177 |
| `internal/certs` | 34 | 43 | 79,1 % | 9 |
| `internal/config` | 139 | 214 | 65,0 % | 75 |
| `internal/eebus` | 593 | 822 | 72,1 % | 229 |
| `internal/grpc` | 1.184 | 1.610 | 73,5 % | 426 |
| `internal/usecases` | 663 | 1.531 | 43,3 % | 868 |
| **Produktiver Scope** | **2.906** | **4.897** | **59,3 %** | **1.991** |

Zum Erreichen von 95,0 % müssen im definierten Scope mindestens 4.653 von
4.897 Statements abgedeckt sein. Gegenüber der Baseline sind somit mindestens
1.747 zusätzliche Statements abzudecken; höchstens 244 Statements dürfen offen
bleiben.

Zum Vergleich beträgt `go test ./...` einschließlich der 2.438 nicht
instrumentierten generierten Statements 39,3 %. Dieser Rohwert ist kein
sinnvoller Qualitätsindikator für den gepflegten Quellcode.

## Umsetzungsstand vom 19. Juli 2026

Die erste Umsetzungsetappe ergänzt Tests für Monitoring-Guards und
-Klassifikation, Registry-Verträge, Konfigurationsvalidierung und
Zertifikatsfehler. Danach ergibt sich folgender Zwischenstand:

| Paket | Baseline | Aktuell | Änderung |
|---|---:|---:|---:|
| `cmd/eebus-bridge` | 56,5 % | 56,5 % | ±0,0 PP |
| `cmd/eebus-watch` | 11,9 % | 11,9 % | ±0,0 PP |
| `internal/certs` | 79,1 % | 93,0 % | +13,9 PP |
| `internal/config` | 65,0 % | 98,1 % | +33,1 PP |
| `internal/eebus` | 72,1 % | 84,5 % | +12,4 PP |
| `internal/grpc` | 73,5 % | 73,5 % | ±0,0 PP |
| `internal/usecases` | 43,3 % | 47,1 % | +3,8 PP |
| **Produktiver Scope** | **59,3 %** | **64,2 %** | **+4,9 PP** |

Die erste Etappe deckt 237 zusätzliche produktive Statements ab. Bis zum
95-%-Ziel fehlen noch mindestens 1.510 Statements. Die vollständige Suite
`go test -count=1 ./...` ist nach dieser Etappe erfolgreich.

## Abnahmekriterien

- Der definierte produktive Scope erreicht insgesamt mindestens 95,0 %
  Statement-Coverage.
- Kein produktives Paket liegt unter 90,0 %, damit große Pakete schwache Pakete
  nicht verdecken.
- Alle Tests laufen wiederholbar mit `-count=1` und ohne externe Geräte oder
  externe Netzwerkdienste.
- Zeitabhängige Tests verwenden kontrollierte Uhren, großzügige
  Synchronisationsgrenzen oder explizite Signale statt knapper Sleeps.
- Netzwerknahe Tests verwenden lokale Listener mit dynamisch vergebenem Port.
- Erfolgs-, Fehler-, Abbruch- und Validierungspfade werden fachlich geprüft; es
  werden keine inhaltslosen Aufrufe nur zur Erhöhung des Prozentwerts ergänzt.
- `go test -count=1 ./...` bleibt erfolgreich.
- Der produktive Go-Code bleibt unverändert.

## Priorisierte Testerweiterungen

### 1. `internal/usecases`

Dieses Paket besitzt mit 868 offenen Statements die größte Lücke.

- Die Provider MGCP, VAPD und VABD werden über ihren gesamten Lebenszyklus
  getestet: Konstruktion, `AddFeatures`, Deskriptoren, nicht initialisierter
  Zustand, Publish-Erfolg, Publish-Fehler, Invalidierung, Ablauf, `Close`,
  Diagnose und Support-Events.
- Der Monitoring-Wrapper erhält Tabellenfälle für Setup, alle Reads vor der
  Initialisierung, Entity-Auswahl, Multi-Device-Fallback, Measurement-Filter
  und die vollständige Klassifikationsmatrix für Temperatur, Energie und
  Leistung.
- DHW-, Room-Heating-, LPC- und OHPCF-Wrapper erhalten Lifecycle-Tests für
  Setup, Use-Case-Registrierung, Support-Updates, mehrdeutige Entities,
  Refresh-Fehler sowie erfolgreiche und abgelehnte Schreibvorgänge.
- Die Geräteklassifikation wird für fehlende Daten, Device-Type-only,
  Manufacturer-Details, alternative Entities und partielle Ergebnisse geprüft.

### 2. `internal/grpc`

- Fehlende Streamtests für DHW, DHW-Systemfunktion, Room Heating und OHPCF
  prüfen SKI-Filter, Initialzustand, Kontextabbruch, Sendefehler und Events ohne
  Payload.
- Noch offene Get- und Snapshot-Endpunkte werden für Erfolg, nicht verfügbare
  Daten und unbekannte SKIs geprüft.
- Payload-Konverter werden mit vollständigen, partiellen und ungültigen Daten
  getestet.
- Tabellengetriebene Vertragstests prüfen `nil`-Requests, ungültige SKIs, nicht
  initialisierte Controller und die erwarteten gRPC-Statuscodes.
- Readiness-, Recovery- und Server-Info-Randfälle werden ergänzt.

### 3. `internal/eebus`

- Die Registry erhält direkte Tests für Trust-Status, Health-Projektionen,
  Observation-Upserts, Entfernen einzelner Observations, bekannte Geräte,
  Entity-Adressen und Feature-Normalisierung.
- Der Bridge-Service wird für Konstruktion, Entity-Zugriffe,
  Setup/Start/Shutdown und Remote-SKI-Registrierung getestet.
- Callback-Pfade für Pairing und Auto-Trust sowie alle Logging-Adapter werden
  ausgeführt und inhaltlich geprüft.
- Recovery-Tests ergänzen Backoff-Grenzen, Zustandswechsel und fehlgeschlagene
  Wiederherstellung.

### 4. `internal/config` und `internal/certs`

- Alle Environment-Overrides werden tabellengetrieben geprüft.
- TLS-/Token-Kombinationen, fehlende Dateien, leere Tokens,
  Provider-Zeitfenster und Bind-Adressen werden validiert.
- Fehlerhafte, fehlende und nicht lesbare Konfigurations- und Zertifikatsdateien
  werden geprüft.
- Zertifikatstests decken ungültiges PEM, unpassende Schlüssel, fehlende SANs
  und vorhandene DNS-/IP-SANs ab.

### 5. Produktive CLI-Pakete

- `eebus-watch` verwendet einen lokalen In-Process-gRPC-Server für
  `collectSnapshot`, den `--once`-Ablauf, Registrierung sowie Debug- und
  Fehlerpfade. `renderSnapshot` erhält Golden- beziehungsweise strukturelle
  Ausgabetests für leere und vollständige Snapshots.
- Der Bridge-Healthcheck wird gegen SERVING, NOT_SERVING, Verbindungsfehler und
  TLS-Konfigurationen getestet.
- Das Application-Setup wird für aktivierte Provider, Konfigurationsfehler,
  Signalabbruch, Fehlerklassifikation und Lifecycle-Stages ergänzt.
- Reine, nicht sinnvoll isolierbare `main()`-Statements dürfen innerhalb des
  verbleibenden 5-%-Budgets offenbleiben.

## Umsetzungsreihenfolge

1. Gut isolierbare Guard-, Klassifikations- und Registry-Tests ergänzen.
2. Provider- und Use-Case-Lebenszyklen mit vorhandenen SPINE-Fixtures testen.
3. Fehlende gRPC-Adapter- und Streamverträge ergänzen.
4. CLI- und Healthcheck-Integrationstests mit lokalen Servern ergänzen.
5. Coverage erneut messen, verbleibende Blöcke nach fachlichem Risiko
   priorisieren und die Baseline-Tabelle fortschreiben.
