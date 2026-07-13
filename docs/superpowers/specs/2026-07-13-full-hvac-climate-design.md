# Vollständige Raumheizungs-/Climate-Integration — Spec

**Datum:** 2026-07-13  
**Status:** Umsetzungsbereit  
**Feature-Branch:** `feat/full-hvac-climate`  
**Abhängigkeit:** PR #100 (MRT/MOT und eebus-go-Integrationsstand)

## 1. Ziel

Die auf dem Vaillant VR940 tatsächlich angebotene Raumheizung wird als native
Home-Assistant-`climate`-Entity integriert. Die Entity verbindet:

- gemessene Raumtemperatur,
- Raum-Solltemperatur mit geräteseitigen Grenzen und Schrittweite,
- die vom Gerät angebotenen Betriebsarten,
- Vorlauf- und Rücklauftemperatur als hydraulische Messwerte,
- lesende und schreibende Echtzeit-Aktualisierung,
- robustes Verhalten bei fehlenden Teil-Use-Cases und Reconnects.

Diese Spec beschreibt ausschließlich die vom vorliegenden VR940-Dump und den
Live-Probes belegte Funktion. Sie setzt weder Kühlung noch Zeitprogramme,
Lüftersteuerung oder einen nicht beobachteten Heizaktivitätsstatus voraus.

## 2. Evidenzbasis

### 2.1 Discovery-Dump

Der [VR940-Use-Case-Dump](../../vr940-usecase-dump.txt) wurde am 27.06.2026
gegen ein live gepairtes Gateway aufgenommen. Er enthält zehn Entities. Für die
Raumheizung ist diese Hierarchie relevant:

```text
HeatingCircuit  5
└── HeatingZone 5:1
    └── HVACRoom 5:1:1
        ├── DeviceClassification/server
        ├── HVAC/server
        ├── Measurement/server
        └── Setpoint/server
```

Die `HVACRoom`-Entity kündigt vier verfügbare Use Cases an:

| Use Case | Szenario | Bedeutung für die Bridge |
|---|---:|---|
| `monitoringOfRoomTemperature` | 1 | aktuelle Raumtemperatur lesen |
| `configurationOfRoomHeatingTemperature` | 1 | Raum-Solltemperatur lesen/schreiben |
| `monitoringOfRoomHeatingSystemFunction` | 1 | aktuelle Heizungsbetriebsart überwachen |
| `configurationOfRoomHeatingSystemFunction` | 1 | Heizungsbetriebsart lesen/schreiben |

`visualizationOfHeatingAreaName` wird für HeatingCircuit, HeatingZone und
HVACRoom zwar aufgeführt, aber jeweils mit `available=false`. Ein Zonenname darf
daher nicht aus diesem Use Case erwartet oder erfunden werden.

### 2.2 Live-Probe des Setpoint-/HVAC-Datenmodells

[PR #91](https://github.com/volschin/eebus-ha-bridge/pull/91) hat die
angekündigten Server-Features auf einem VR940 live gelesen, gebunden und
beschrieben. Für `HVACRoom 5:1:1` wurden folgende Daten beobachtet:

| Datenpunkt | Live-Ergebnis |
|---|---|
| Setpoint-Scope | `roomAirTemperature` |
| Setpoint-Typ | `valueAbsolute` |
| Einheit | `degC` |
| aktueller Sollwert | 21 °C |
| Minimum / Maximum | 5 °C / 30 °C |
| Schrittweite | 0,5 K |
| Setpoint-Operation | `setpointListData: rw` |
| System Function | `heating` |
| aktueller Modus beim Capture | `off` |
| angebotene Modi | `auto`, `on`, `off` |
| Modus änderbar | `isOperationModeIdChangeable=true` |
| System-Function-Operation | `hvacSystemFunctionListData: rw` |

Die Relationstabellen ordnen der Heating-System-Function alle drei Modi zu.
Setpoint-ID 1 gilt für `auto` und `on`; für `off` ist kein aktiver Setpoint
referenziert. IDs und Entity-Adressen sind Gerätewerte und dürfen in der
Produktimplementierung nicht hardcodiert werden.

### 2.3 Live-Probe des Kontrollpfads

Der Probe hat die vollständige technische Kette nachgewiesen:

1. Der VR940 akzeptierte Bindings für `Setpoint` und `HVAC` auf `DHWCircuit`
   und `HVACRoom` mit `errorNumber=0`.
2. Echo-Writes des vollständigen `SetpointListData` wurden auf beiden Entities
   akzeptiert.
3. Ein wertverändernder DHW-Test wurde angewendet und per Re-Read bestätigt
   (46 → 47 → 46). Damit ist belegt, dass der VR940 Setpoint-Writes nicht nur
   quittiert, sondern anwendet.

Für den Raum-Sollwert wurde nur der unveränderte Echo-Write live ausgeführt. Der
Schreibpfad ist damit transportseitig bestätigt; ein verändernder Raumwert und
ein Moduswechsel bleiben bewusste Hardware-Abnahmeschritte.

### 2.4 Vorlauf-/Rücklauf-Evidenz und offene Messwertdetails

Der Discovery-Dump belegt ein `Measurement/server` auf der
`HeatPumpAppliance`-Entity 3. Die Bridge kennt bereits die standardisierten
Scopes `flowTemperature` und `returnTemperature`, klassifiziert sie als
`flow_temperature`/`return_temperature` und besitzt dafür HA-Sensoren,
Coordinator-Felder und Übersetzungen.

Der vorliegende Discovery-Dump enthält jedoch keine
`MeasurementDescriptionListData` und damit keinen Live-Beleg, auf welcher
Entity, mit welchen Measurement-IDs und in welcher Temperatureinheit der VR940
Vorlauf und Rücklauf liefert. Die PR-Implementierung muss diese Metadaten zur
Laufzeit auswerten und der Hardwaretest muss beide Scopes bestätigen. IDs,
Positionen in Listen und Celsius als Remote-Einheit dürfen nicht vorausgesetzt
werden.

### 2.5 Bind-Resultat-Besonderheit

Bind-Ergebnisse werden als NodeManagement-Resultate an das lokale
NodeManagement-Feature adressiert. `spine-go` registriert seine Antwortlogik
derzeit am anfragenden Client-Feature. Dadurch kann `HasBindingToRemote` trotz
geräteseitig akzeptierter Bindung `false` bleiben.

Konsequenzen für die Produktimplementierung:

- auf Entity-Connect genau einen Bind-Versuch je Remote-Feature senden,
- Schreibfähigkeit nicht von `HasBindingToRemote` abhängig machen,
- direkte Write-Resultate weiterhin am lokalen Setpoint-/HVAC-Client erwarten,
- den bekannten Result-Routing-Mangel separat in `spine-go` lösen; keinen
  VR940-spezifischen Protokoll-Fork in die Climate-Logik einbauen.

## 3. Fachlicher Umfang

### 3.1 Enthalten

- eine HA-`climate`-Entity für die erste kompatible `HVACRoom`-Entity des
  konfigurierten Geräte-SKI,
- `current_temperature` aus `monitoringOfRoomTemperature` (MRT),
- `target_temperature`, `min_temp`, `max_temp` und
  `target_temperature_step` aus `configurationOfRoomHeatingTemperature`,
- `hvac_mode` und `hvac_modes` aus den Monitoring-/Configuration-Use-Cases der
  Room Heating System Function,
- Zieltemperatur- und Modus-Writes mit Resultatprüfung und anschließendem Read,
- Vorlauf- und Rücklauftemperatursensoren aus Measurement-Descriptions mit
  Celsius-Normalisierung und Push-/Polling-Abgleich,
- Polling plus Push-Events,
- fail-closed Verhalten bei unvollständigen Beschreibungen, Constraints,
  Relationen oder Schreiboperationen.

### 3.2 Nicht enthalten

- Kühlung oder `heat_cool`: im Dump existiert nur System Function `heating`.
- `hvac_action`: Modus `on` oder `auto` beweist nicht, dass der Verdichter
  gerade heizt. Der Dump liefert keinen belastbaren `heating`/`idle`-Status.
- Zeitprogramme, Kalender, Absenkprofile oder Presets.
- Lüftermodus, Luftfeuchte, Swing oder Aux Heat.
- frei erfundene Zonenbezeichnungen; der Namens-Use-Case ist nicht verfügbar.
- Mehrzonen-UI. Der konkrete VR940 kündigt genau eine `HVACRoom`-Entity an.
- DHW-Steuerung; diese bleibt in `water_heater.eebus_domestic_hot_water`.
- Außentemperatur als Climate-Eigenschaft; sie bleibt ein separater Sensor.
- Vorlauf/Rücklauf als Climate-Eigenschaften: Home Assistant definiert dafür
  keine standardisierten Climate-Properties; beide bleiben separate Sensoren.

## 4. Home-Assistant-Modell

### 4.1 Entity

Neue Plattform: `Platform.CLIMATE` mit einer Entity
`EebusRoomHeatingClimate`.

| Eigenschaft | Quelle |
|---|---|
| Unique ID | `${ski}_room_heating` |
| Translation Key | `room_heating` |
| Temperatur-Einheit | Celsius |
| `current_temperature` | MRT `room_temperature_c` |
| `target_temperature` | Room-Heating-Setpoint |
| Min/Max/Step | vom VR940 angekündigte Constraints |
| `hvac_mode` | gemappter aktueller EEBUS-Modus |
| `hvac_modes` | Schnittmenge der angekündigten und bekannten Modi |

Die bereits existierende `sensor.eebus_room_temperature` bleibt erhalten. Sie
ist ein numerischer Messwert mit `state_class=measurement`, besitzt bereits
eine stabile Unique ID und eignet sich besser für Statistik/Automationen. Die
Climate-Entity verwendet denselben Coordinator-Wert als
`current_temperature`; es ist keine Registry-Migration nötig.

### 4.2 Betriebsarten-Mapping

| EEBUS `HvacOperationModeType` | Home Assistant `HVACMode` |
|---|---|
| `auto` | `AUTO` |
| `on` | `HEAT` |
| `off` | `OFF` |

Das Mapping wird in beide Richtungen explizit implementiert. Unbekannte
EEBUS-Modi werden nicht als erfundene HA-Modi angeboten. Ist der aktuelle Modus
nicht abbildbar, bleibt die Climate-Entity unavailable; die separate
Raumtemperatur bleibt weiterhin nutzbar. Der rohe unbekannte Modus wird nur im
Debug-Log dokumentiert.

`ClimateEntityFeature.TARGET_TEMPERATURE` wird nur gesetzt, wenn das Remote-
Setpoint-Feature `setpointListData` als schreibbar ankündigt und vollständige
Constraints vorhanden sind. Ein Setpoint darf auch im Modus `off` geändert
werden: die Relation besagt, dass er dort nicht aktiv wirkt, nicht dass sein
gespeicherter Wert nicht konfiguriert werden darf.

### 4.3 Verfügbarkeit

Die Climate-Entity ist verfügbar, wenn:

1. die Bridge verbunden ist,
2. eine kompatible `HVACRoom` ausgehandelt wurde,
3. der aktuelle EEBUS-Modus auf einen HA-Modus abbildbar ist und
4. mindestens Raum-Ist- oder Solltemperatur vorliegt.

Teilweise fehlende Schreibrechte machen die Entity read-only, nicht
unavailable. Einzelne RPC-Fehler dürfen andere Coordinator-Daten nicht löschen.

### 4.4 Vorlauf- und Rücklauftemperatur

Die vorhandenen Sensoren werden in diesem PR produktiv abgeschlossen:

| Sensor | Coordinator-Key | EEBUS-Filter |
|---|---|---|
| `sensor.eebus_flow_temperature` | `flow_temperature_c` | `measurementType=temperature`, `scopeType=flowTemperature` |
| `sensor.eebus_return_temperature` | `return_temperature_c` | `measurementType=temperature`, `scopeType=returnTemperature` |

Beide verwenden `SensorDeviceClass.TEMPERATURE`, Celsius und
`SensorStateClass.MEASUREMENT`. Nach erfolgreicher Hardware-Abnahme werden sie
standardmäßig aktiviert; fehlt der jeweilige Scope auf einem Gerät, bleibt nur
dieser Sensor unavailable und beeinflusst die Climate-Entity nicht.

Remote-Werte in Fahrenheit oder Kelvin werden vor Übergabe an Home Assistant
nach Celsius konvertiert. Ein unbekanntes oder fehlendes Unit-Feld führt zum
Auslassen des Messwerts, nicht zu einer fälschlichen Celsius-Beschriftung.

## 5. eebus-go-Erweiterungen

Die fehlenden generischen Use Cases werden entsprechend der
[Fork-/Upstream-Strategie](./2026-07-13-eebus-go-upstream-strategy-design.md)
im `eebus-go`-Fork entwickelt, nicht als zweite rohe SPINE-Implementierung in
der Bridge.

Vorgesehene isolierte Beiträge:

| Paket/Beitrag | Lokaler Actor | Remote Actor | Feature |
|---|---|---|---|
| Configuration of Room Heating Temperature (CRHT) | Configuration Appliance | HVAC Room | Setpoint client |
| Monitoring of Room Heating System Function (MRHSF) | Monitoring Appliance | HVAC Room | HVAC client |
| Configuration of Room Heating System Function (CRHSF) | Configuration Appliance | HVAC Room | HVAC client |

Jeder Beitrag:

- implementiert ausschließlich Szenario 1 / Version 1.0.0,
- akzeptiert nur `EntityType=HVACRoom` und Actor `HVACRoom`,
- löst IDs über Description-/Relation-Listen auf,
- konvertiert Temperatur zwischen `degC`, `degF` und `K`,
- subscribed für Monitoring und bindet für Configuration,
- liefert protokollnahe Events, keine Home-Assistant-Begriffe,
- enthält generierte Interfaces/Mocks und Unit Tests nach eebus-go-Konvention.

CRHT muss mindestens folgende öffentliche API anbieten:

- State/Temperature inklusive Minimum, Maximum, Step und Write-Capability,
- WriteTemperature mit vollständiger Datenlisten-Kopie,
- Fehler für fehlende Daten, nicht schreibbar, außerhalb des Bereichs,
  ungültige Schrittweite und Geräteablehnung.

MRHSF/CRHSF müssen System Function `heating`, den aktuellen Modus und die über
Relationen erlaubten Modi auflösen. CRHSF ergänzt WriteOperationMode. Nur ein
explizites `isOperationModeIdChangeable=false` blockiert einen Write; ein
fehlendes optionales Flag darf eine als `rw` angekündigte Operation nicht
verbergen. Dieses Verhalten entspricht dem am VR940 bewährten DHW-Pfad.

Die Contribution-Commits werden in `volschin/eebus-go:bridge-integration`
aufgenommen, vollständig getestet und anschließend als konkrete Pseudoversion
in der Bridge gepinnt. `UPSTREAM_PATCHES.md` erhält pro Use Case eine eigene
Zeile und Removal Condition.

## 6. Bridge-Adapter

### 6.1 Use-Case-Komposition

Die Bridge registriert vor `Service.Start()`:

- MRT aus PR #100,
- CRHT,
- MRHSF,
- CRHSF.

Sie verwendet keine hardcodierten Werte `5:1:1`, Setpoint-ID 1,
System-Function-ID 0 oder Mode-IDs 0/1/2. Die kompatible Entity und alle IDs
kommen aus Use-Case-Aushandlung und Remote-Metadaten.

Ein Bridge-Wrapper übersetzt die drei Raumheizungszustände in interne Events:

| Ereignis | Bridge Event |
|---|---|
| MRT-Wert | `room.temperature_updated` (bereits vorhanden) |
| CRHT Support/Metadaten | `room_heating.setpoint_support_updated` |
| CRHT Setpoint | `room_heating.setpoint_updated` |
| MRHSF/CRHSF Support | `room_heating.system_function_support_updated` |
| Modus/System Function | `room_heating.system_function_updated` |

Remote-Beobachtungen werden unter unterscheidbaren Use-Case-Namen im
`DeviceRegistry` gespeichert. Reconnects müssen immer neu ausgehandelte
Remote-Entity-Referenzen verwenden.

### 6.2 Hydraulische Temperaturmessungen

Vorlauf und Rücklauf benötigen keinen erfundenen Named Use Case. Ein kleiner
Bridge-Adapter liest die vorhandenen `Measurement/server`-Features über die
standardisierten Measurement-Descriptions:

1. alle Remote-Entities des angeforderten SKI mit `Measurement/server` prüfen,
2. nur normale Temperaturwerte mit exakt passendem Scope akzeptieren,
3. bei mehreren Kandidaten `HeatPumpAppliance` bevorzugen,
4. verbleibt die Auswahl mehrdeutig, den Wert auslassen und im Debug-Log die
   Kandidaten mit Entity-Adresse dokumentieren,
5. Unit aus der Description lesen und nach Celsius konvertieren,
6. auf relevante `MeasurementListData`-Änderungen
   `monitoring.flow_temperature_updated` beziehungsweise
   `monitoring.return_temperature_updated` publizieren.

Die bestehende generische Measurement-Ausgabe bleibt für weitere Diagnosewerte
erhalten. Für Vorlauf/Rücklauf ist der neue typisierte Pfad Primärquelle; die
anschließende Typ-Deduplizierung verhindert doppelte Einträge.

### 6.3 Writes

Alle Writes gelten nur für einen nichtleeren, normalisierten SKI und die dazu
ausgehandelte `HVACRoom`-Entity. Es gibt keinen First-Device-Fallback für
Schreiboperationen.

Vor Temperatur-Writes werden geprüft:

- Scope `roomAirTemperature`,
- Typ `valueAbsolute`,
- unterstützte Temperatureinheit,
- vollständige und valide Min-/Max-/Step-Constraints,
- Wert endlich, im Bereich und auf dem Step-Raster,
- `setpointListData` tatsächlich `write=true`.

Vor Modus-Writes werden geprüft:

- genau eine System Function `heating`,
- angeforderter Modus in der Relation dieser System Function,
- Modus-ID über Description-Liste aufgelöst,
- `hvacSystemFunctionListData` `write=true`,
- Changeability nicht explizit `false`.

Wie beim bewährten DHW-Code wird jeweils die vollständige gecachte Liste
kopiert, nur der adressierte Eintrag geändert, das direkte SPINE-Resultat
abgewartet und danach der Wert neu gelesen. Timeouts und verlorene Resultate
werden nicht als Erfolg gemeldet.

## 7. gRPC-Vertrag

Neue Datei `eebus/v1/hvac_service.proto`:

```protobuf
service HVACService {
  rpc GetRoomHeating(DeviceRequest) returns (RoomHeatingState);
  rpc SetRoomHeatingTemperature(SetRoomHeatingTemperatureRequest) returns (Empty);
  rpc SetRoomHeatingMode(SetRoomHeatingModeRequest) returns (Empty);
  rpc SubscribeRoomHeatingEvents(DeviceRequest) returns (stream RoomHeatingEvent);
}
```

Vorgesehene Datenstruktur:

```protobuf
message RoomHeatingState {
  optional double current_temperature_celsius = 1;
  RoomHeatingSetpoint setpoint = 2;
  RoomHeatingSystemFunction system_function = 3;
  string entity_address = 4;
}

message RoomHeatingSetpoint {
  double value_celsius = 1;
  double min_celsius = 2;
  double max_celsius = 3;
  double step_celsius = 4;
  bool writable = 5;
}

message RoomHeatingSystemFunction {
  string operation_mode = 1;
  repeated string available_modes = 2;
  bool mode_writable = 3;
}
```

Nested Messages besitzen Presence. Ein fehlender Teilzustand wird ausgelassen,
nicht mit Nullwerten erfunden. `entity_address` ist Diagnose-/Future-Proofing;
Requests adressieren in dieser ersten, VR940-basierten Version weiterhin per
SKI genau die erste kompatible HVACRoom.

Eventtypen:

- `ROOM_HEATING_EVENT_SUPPORT_UPDATED`,
- `ROOM_HEATING_EVENT_CURRENT_TEMPERATURE_UPDATED`,
- `ROOM_HEATING_EVENT_SETPOINT_UPDATED`,
- `ROOM_HEATING_EVENT_SYSTEM_FUNCTION_UPDATED`.

Events tragen best-effort den zusammengesetzten aktuellen State. Ohne Payload
löst der Client einen Reconciliation-Poll aus.

Der bestehende `MonitoringService` wird zusätzlich append-only erweitert:

- `MEASUREMENT_EVENT_FLOW_TEMPERATURE_UPDATED`,
- `MEASUREMENT_EVENT_RETURN_TEMPERATURE_UPDATED`.

`GetMeasurements` liefert die Typen `flow_temperature` und
`return_temperature`, jeweils garantiert mit `unit=degC`. Stream-Events tragen
ein `MeasurementEntry`; bei einem Read-Fehler bleibt der Payload leer und der
Coordinator reconciled per Poll.

Fehlerabbildung:

| Ursache | gRPC-Code |
|---|---|
| Request/SKI fehlt, Range/Step/Mode ungültig | `INVALID_ARGUMENT` |
| Feature nicht schreibbar oder Gerät lehnt ab | `FAILED_PRECONDITION` |
| kompatible Entity/Metadaten fehlen | `NOT_FOUND` |
| Context abgebrochen / Deadline | `CANCELLED` / `DEADLINE_EXCEEDED` |
| unerwarteter Transport-/Cachefehler | `INTERNAL` |

## 8. Coordinator und Streams

Der Coordinator erhält:

- best-effort `GetRoomHeating` im Poll-Zyklus,
- einen `SubscribeRoomHeatingEvents`-Task mit bestehender Retry-Logik,
- `async_set_room_heating_temperature(value)` und
  `async_set_room_heating_mode(mode)`,
- Support-Status getrennt von den zuletzt bekannten Werten,
- direkte Push-Aktualisierung bei vollständigem Payload,
- Poll-Refresh bei Support-Änderung oder Event ohne Payload.

Der bestehende Monitoring-Stream bleibt Quelle für generische Sensoren. Die
Climate-Entity liest `current_temperature` aus demselben Coordinator-Feld
`room_temperature_c`; doppelte EEBUS-Reads oder abweichende Zustände werden
vermieden.

Für Flow-/Return-Events schreibt der Coordinator direkt
`flow_temperature_c`/`return_temperature_c`. Beide Werte werden zu Beginn jedes
Polls wie andere optionale Messwerte auf `None` gesetzt und nur durch gültige
Einträge ersetzt; ein alter Wert darf nach Geräte-/Scope-Verlust nicht endlos
stehen bleiben.

## 9. Tests

### 9.1 eebus-go

- Actor-/Entity-/Szenario-Kompatibilität jedes neuen Use Cases,
- Entity-Connect, Subscribe/Bind und initiale Reads,
- Filterung auf `roomAirTemperature` und System Function `heating`,
- Celsius/Fahrenheit/Kelvin-Konvertierung,
- ID-Auflösung mit Decoy-Einträgen und variablen IDs,
- vollständige/fehlende/ungültige Constraints,
- bekannte und unbekannte Operation Modes sowie Relation-Filterung,
- Full-list-copy-Write ohne Veränderung fremder Einträge,
- Write-Accept, Ablehnung, Timeout und Context-Abbruch,
- explizites `changeable=false` gegenüber fehlendem Flag,
- Daten- und Support-Events.

### 9.2 Bridge/Go

- zusammengesetzter Zustand bei vollständigen und partiellen Use Cases,
- SKI-Normalisierung und kein Schreib-Fallback,
- interne Eventübersetzung und Stream-Payloads,
- alle gRPC-Fehlercodes,
- Reconnect mit neuer Entity-Referenz,
- Flow-/Return-Filterung über Scope statt Listenposition,
- Celsius-Konvertierung aus `degC`, `degF` und `K`,
- Präferenz `HeatPumpAppliance`, mehrdeutige Kandidaten und ungültiger
  `ValueState`/Unit-Fall,
- Deduplizierung gegenüber dem generischen Measurement-Scan,
- Flow-/Return-Push-Events mit und ohne Payload,
- vollständiges `go test ./...` und Race-Test der betroffenen Packages.

### 9.3 Home Assistant/Python

- Climate-Eigenschaften für den VR940-Capture 21 °C / 5–30 / Step 0,5,
- Mapping `auto/on/off` ↔ `AUTO/HEAT/OFF`,
- unbekannter Modus macht Climate unavailable, ohne den Sensor zu verlieren,
- dynamische Supported Features bei read-only/writable,
- Temperatur- und Modus-Services senden korrekte RPCs,
- Push-Event und payload-loser Refresh,
- Stream-Reconnect und Poll-Fallback,
- unveränderte Unique ID des Raumtemperatur-Sensors,
- Vorlauf-/Rücklauf-Sensorwerte aus Poll und Push einschließlich `0.0`,
- nur der betroffene hydraulische Sensor wird bei fehlenden Daten unavailable,
- Ruff, mypy strict, vollständiges pytest.

## 10. Hardware-Abnahme am VR940

Die Release-Freigabe benötigt einen bewusst gestarteten Test gegen denselben
Gerätestand beziehungsweise dokumentiert neue Firmware:

1. CRHT/MRHSF/CRHSF werden in NodeManagement als Szenario 1 angekündigt.
2. Setpoint- und HVAC-Bindings auf Entity `5:1:1` werden mit
   `errorNumber=0` bestätigt.
3. Initialzustand entspricht Gerät/App: aktuelle Temperatur, 21-°C-Sollwert,
   Range 5–30, Step 0,5 und aktueller Modus.
4. Measurement-Descriptions und -Werte für `flowTemperature` und
   `returnTemperature` werden mit Entity-Adresse, Unit und plausiblen
   Live-Werten dokumentiert; beide HA-Sensoren stimmen mit Gerät/Diagnose ab.
5. Ein explizit freigegebener Raum-Sollwertwechsel um genau einen Schritt wird
   akzeptiert, per Re-Read bestätigt und anschließend manuell/automatisch auf
   den Ausgangswert zurückgesetzt.
6. Ein bewusst gewählter Betriebsartwechsel wird in myVAILLANT/Gerät sichtbar,
   per Re-Read bestätigt und zurückgesetzt. Dieser Test darf nicht automatisch
   in einem reconnectenden Probe laufen, da `on`/`auto` Heizen auslösen kann.
7. Änderungen aus myVAILLANT oder dem Regler sowie Flow-/Return-Änderungen
   erscheinen per Push in HA.
8. Nach SHIP-Reconnect werden Zustand und Schreibfähigkeit ohne Neustart der
   HA-Integration wiederhergestellt.

## 11. Rollout und Migration

1. Drei generische Use Cases in isolierten eebus-go-Contribution-Branches
   implementieren und testen.
2. Commits in `bridge-integration` aufnehmen und Bridge-Pin/Inventar ändern.
3. Bridge-Adapter und `HVACService` ergänzen; beide Stub-Sätze regenerieren.
4. Typisierten Flow-/Return-Reader und Monitoring-Events ergänzen.
5. Coordinator, hydraulische Sensoren und Climate-Plattform ergänzen.
6. Lokale Gesamtprüfungen und CI.
7. Hardware-Abnahme hinter einem expliziten Testablauf.
8. README korrigieren: Die derzeitige Aussage, Vaillant exponiere HVAC nicht
   über EEBUS, ist durch Dump und Probe widerlegt.
9. Nach Hardwarefreigabe den experimentellen HVAC-Probe entfernen; Discovery-
   Diagnose kann bestehen bleiben.

Es gibt keine Entity-Registry-Löschung: Der Raumtemperatursensor und die neue
Climate-Entity haben unterschiedliche stabile Unique IDs und unterschiedliche
fachliche Rollen.

## 12. Definition of Done

- `climate.eebus_room_heating` zeigt den VR940-Zustand korrekt und ohne
  hardcodierte Geräte-IDs.
- Aktuelle Temperatur, Zieltemperatur, Range, Step und Modi stammen aus den
  standardisierten Use Cases beziehungsweise deren Metadaten.
- Vorlauf und Rücklauf werden scope-basiert, einheitenrichtig und per Push als
  eigenständige Temperatursensoren geliefert.
- Setpoint- und Modus-Writes sind fail-closed, quittungsgeprüft und per Re-Read
  abgeglichen.
- Keine Kühl-/Action-/Zonenfunktion wird ohne Gerätebeleg behauptet.
- Bestehender Raumtemperatursensor und DHW-Water-Heater bleiben kompatibel.
- Reconnect, Polling und Push sind getestet.
- Alle lokalen Prüfungen und CI sind grün.
- Setpoint und Modus wurden auf realer Hardware bewusst geändert,
  zurückgesetzt und dokumentiert.
