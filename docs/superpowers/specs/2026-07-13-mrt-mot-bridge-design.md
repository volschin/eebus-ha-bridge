# MRT/MOT-Integration in der Bridge — Design

**Datum:** 2026-07-13  
**Status:** Erster umsetzungsreifer Entwurf  
**Abhängigkeit:** `volschin/eebus-go@1f909a8b465f` mit
[enbility/eebus-go#232](https://github.com/enbility/eebus-go/pull/232) und
[enbility/eebus-go#233](https://github.com/enbility/eebus-go/pull/233)

## 1. Ziel

Die Bridge soll Raum- und Außentemperatur über die standardisierten EEBUS Use
Cases **Monitoring of Room Temperature (MRT)** und **Monitoring of Outdoor
Temperature (MOT)** lesen und als bestehende Home-Assistant-Sensoren
`room_temperature` und `outdoor_temperature` bereitstellen.

MRT liest `roomAirTemperature` von einer `HVACRoom`-Entity. MOT liest
`outsideAirTemperature` von einer `TemperatureSensor`-Entity. Beide Use Cases
laufen in der Rolle `MonitoringAppliance`, abonnieren das jeweilige
`Measurement`-Server-Feature und liefern auf Wunsch Celsius, Fahrenheit oder
Kelvin. Die Bridge normalisiert ihre öffentliche Schnittstelle weiterhin auf
Grad Celsius.

## 2. Ausgangslage

Die Home-Assistant-Seite ist bereits vorbereitet:

- `MonitoringService.GetMeasurements` transportiert typisierte
  `MeasurementEntry`-Werte.
- Der Coordinator ordnet `room_temperature` und `outdoor_temperature` den
  Datenfeldern `room_temperature_c` und `outdoor_temperature_c` zu.
- Die Sensorplattform besitzt bereits standardmäßig aktivierte
  Temperatursensoren mit stabilen Unique IDs.

Der derzeitige Go-Pfad gewinnt beide Werte nur über
`MonitoringWrapper.GenericMeasurements`. Dieser scannt alle im Registry-Cache
bekannten Entities mit einem `Measurement/server`-Feature. Das ist als
Diagnose- und Kompatibilitäts-Fallback nützlich, bildet aber weder die
Use-Case-Aushandlung noch deren Entity-/Actor-Kompatibilität ab und erzeugt
keine spezifischen Push-Events für MRT oder MOT.

## 3. Entscheidung

MRT und MOT erhalten jeweils einen kleinen, expliziten Bridge-Wrapper nach dem
Muster von `DHWMonitoringWrapper`:

```text
eebus-go MRT/MOT
      │ UseCase callback + Temperature(entity, degC)
      ▼
Room-/OutdoorTemperatureMonitoringWrapper
      │ EventBus
      ▼
MonitoringService
      │ bestehendes MeasurementEntry-Schema
      ▼
HA Coordinator → bestehende Temperatursensoren
```

Separate Wrapper sind gegenüber einem generischen Wrapper vorzuziehen: MRT und
MOT haben unterschiedliche konkrete eebus-go-Typen und Event-Konstanten. Eine
Abstraktion würde an dieser kleinen Grenze Typadapter benötigen, ohne Lifecycle-
oder Testlogik einzusparen. Gemeinsame private Hilfsfunktionen für SKI-Auflösung
können erst bei tatsächlicher Duplikation extrahiert werden.

Der generische Measurement-Scan bleibt bestehen. `GetMeasurements` fügt zuerst
die dedizierten Werte für DHW, Raum und Außen hinzu und dedupliziert danach die
generischen Ergebnisse anhand des Measurement-Typs. Damit gilt:

1. standardisierte Use Cases sind die Primärquelle,
2. bestehende Gerätekompatibilität bleibt erhalten,
3. jedes Measurement erscheint höchstens einmal,
4. das gRPC- und HA-Datenmodell bleibt rückwärtskompatibel.

## 4. Bridge-Komponenten

### 4.1 Use-Case-Wrapper

Neue Dateien in `eebus-bridge/internal/usecases/`:

- `roommonitoring.go` / `roommonitoring_test.go`
- `outdoormonitoring.go` / `outdoormonitoring_test.go`

Jeder Wrapper übernimmt:

- `Setup(localEntity)` und Zugriff auf den konkreten MRT-/MOT-Use-Case,
- Registrierung von Beobachtungen im `DeviceRegistry`,
- Auflösung der kompatiblen Remote-Entity nach normalisierter SKI,
- `Temperature(ski)` in `degC`,
- Übersetzung der eebus-go-Callbacks auf Bridge-Events.

Vorgesehene Events:

| Use Case | eebus-go Event | Bridge Event |
|---|---|---|
| MRT | `DataUpdateTemperature` | `room.temperature_updated` |
| MRT | `UseCaseSupportUpdate` | `room.monitoring_support_updated` |
| MOT | `DataUpdateTemperature` | `outdoor.temperature_updated` |
| MOT | `UseCaseSupportUpdate` | `outdoor.monitoring_support_updated` |

Registry-Bezeichner sind `room_temperature_monitoring` und
`outdoor_temperature_monitoring`. Leere oder abweichend formatierte SKIs werden
wie bei MDT über Remote-Device-Fallback und `NormalizeSKI` behandelt.

### 4.2 Lifecycle

`cmd/eebus-bridge/main.go` erzeugt und initialisiert beide Wrapper vor
`Service.Start()`, registriert ihre Use Cases per `AddUseCase` und nimmt `MRT`
und `MOT` in die Startdiagnose auf. Die drei Temperatur-Use-Cases MDT, MRT und
MOT teilen sich das lokale `Measurement/client`-Feature über eebus-go
`GetOrAddFeature`; es wird kein weiteres lokales Entity benötigt.

Ein fehlender kompatibler Remote-Actor ist normal und darf den Bridge-Start
nicht fehlschlagen. Erst ein Fehler beim lokalen Hinzufügen des Use Cases ist
fatal, entsprechend den bestehenden Use Cases.

### 4.3 gRPC

`MonitoringService` erhält zwei schmale Reader-Interfaces neben dem vorhandenen
DHW-Reader. In `GetMeasurements` werden folgende Einträge ergänzt:

| Reader | Type | Unit |
|---|---|---|
| MRT | `room_temperature` | `degC` |
| MOT | `outdoor_temperature` | `degC` |

Das Enum `MeasurementEventType` wird ausschließlich am Ende erweitert, damit
bestehende Nummern stabil bleiben:

- `MEASUREMENT_EVENT_ROOM_TEMPERATURE_UPDATED = 5`
- `MEASUREMENT_EVENT_ROOM_TEMPERATURE_SUPPORT_UPDATED = 6`
- `MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_UPDATED = 7`
- `MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_SUPPORT_UPDATED = 8`

Bei einem `*_UPDATED`-Event liest der Stream den aktuellen Wert und setzt das
vorhandene `measurement`-Oneof. Support-Events tragen wie bei MDT keine
Messdaten; der HA-Client löst zur Zustandsabstimmung einen Refresh aus.

Die Proto-Änderung erfordert eine Regeneration der Go- und Python-Stubs, aber
keine neue RPC-Methode und keinen neuen Message-Typ.

### 4.4 Home Assistant

Es werden keine neuen Entities und keine Migration benötigt. Der Coordinator
erweitert lediglich `_handle_measurement_event` um die neuen MRT-/MOT-Enums:

- Werte aus Update-Events werden direkt in `room_temperature_c` bzw.
  `outdoor_temperature_c` geschrieben.
- Support-Events fordern eine Coordinator-Aktualisierung an.
- Der periodische `GetMeasurements`-Abruf bleibt Reconciliation und Fallback.

## 5. Fehler- und Fallback-Verhalten

- `ErrDataNotAvailable`, `ErrDataInvalid` und `ErrNoCompatibleEntity` führen
  dazu, dass der betreffende Eintrag fehlt; andere Messwerte bleiben erhalten.
- Ein fehlerhafter oder nicht unterstützter MRT-/MOT-Use-Case macht weder
  `GetMeasurements` noch andere Sensoren unavailable.
- Die generische Quelle darf einen fehlenden dedizierten Wert ersetzen.
- Liefert die dedizierte Quelle einen Wert, gewinnt sie bei der Deduplizierung.
- Nach Reconnect werden kompatible Entities aus den neu ausgehandelten
  `RemoteEntitiesScenarios` verwendet; Registry-Entity-Referenzen bleiben nur
  Fallback für den generischen Scan.

## 6. Tests und Abnahmekriterien

### Go

- Wrapper-Eventübersetzung für Data- und Support-Updates,
- SKI-Normalisierung und Auswahl der richtigen Remote-Entity,
- Fehler vor `Setup` und bei fehlenden Daten,
- `GetMeasurements` mit MRT, MOT, nur einer Quelle und generischem Fallback,
- Deduplizierung bei gleichzeitig dediziertem und generischem Wert,
- Stream-Events mit Wert und Support-Refresh,
- vollständiges `go test ./...`.

### Python

- Mapping der neuen Event-Enums auf beide Coordinator-Felder,
- Support-Events lösen Refresh aus,
- Polling bleibt kompatibel mit älteren Bridge-Versionen,
- bestehende Sensor-Unique-IDs und Verfügbarkeit bleiben stabil,
- `pytest`, Ruff und mypy.

### Hardware

Auf dem VR940/VR940f müssen nach dem Pairing sichtbar sein:

1. erfolgreiche MRT-Aushandlung mit der `HVACRoom`-Entity,
2. erfolgreiche MOT-Aushandlung mit der `TemperatureSensor`-Entity, sofern vom
   Gerätestand angeboten,
3. initialer Read und folgende Push-Aktualisierung,
4. korrekte Wiederaufnahme nach SHIP-Reconnect,
5. Celsiuswerte ohne Duplikate in `GetMeasurements` und Home Assistant.

## 7. Umsetzungsreihenfolge

1. Dependency-Pin und Patch-Inventar aktualisieren.
2. MRT- und MOT-Wrapper samt Unit Tests ergänzen.
3. Lifecycle-Registrierung in `main.go` vornehmen.
4. `MonitoringService` und Proto-Eventtypen erweitern, Stubs regenerieren.
5. Coordinator-Stream-Mapping und Python-Tests ergänzen.
6. Gesamtprüfungen ausführen und anschließend am realen Gateway verifizieren.

## 8. Offener Hardwarepunkt

Die Codepfade sind unabhängig davon, ob ein konkretes Gateway beide Use Cases
anbietet. Vor einer Release-Freigabe ist noch zu bestätigen, welche Vaillant-
Firmware MOT tatsächlich als Use Case samt `TemperatureSensor`-Entity
annonciert. Fehlt MOT, bleibt der vorhandene generische Measurement-Fallback
bewusst erhalten; daraus folgt keine Änderung am öffentlichen HA-Modell.
