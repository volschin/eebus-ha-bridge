# Refactoring- und Optimierungsspezifikation v2

- Status: Entwurf
- Basis: Softwarestand `8e775caf003b` vom 2026-07-16
- Geltungsbereich: `eebus-bridge/`, `custom_components/eebus/`, Protobuf-Vertrag, Build- und Testautomatisierung

## 1. Zweck und Herleitung

Diese Spezifikation wurde ausschließlich aus dem aktuellen Quellcode, den aktuellen Protobuf-Dateien, der Laufzeitkonfiguration und den aktuellen Tests abgeleitet. Frühere Refactoring-Spezifikationen sind keine fachliche Quelle dieses Dokuments.

Die Reihenfolge richtet sich nach der Bedeutung für das System:

1. fachlich korrekter und sicherer Zustand;
2. belastbare Identität und Schnittstellenverträge;
3. zeitnahe, verlusttolerante Synchronisation;
4. verständliche Verantwortlichkeiten und Änderbarkeit;
5. erst danach lokale Deduplizierung und Performance-Feinschliff.

Die Bezeichnung „v2“ versioniert dieses Dokument. Sie erzwingt keine Umbenennung des bestehenden Protobuf-Pakets `eebus.v1`.

## 2. Bedeutung des Systems

Das System ist ein lokaler, zweiteiliger Protokolladapter:

```text
Home Assistant
  ├─ zeigt Messwerte und Diagnosen
  ├─ sendet Bedienbefehle
  └─ liefert optionale Netz-/PV-/Batteriedaten
          │ gRPC
          ▼
eebus-bridge
  ├─ verwaltet lokale EEBUS-Identität und Vertrauen
  ├─ übersetzt gRPC in EEBUS/SHIP/SPINE
  └─ hält gerätebezogene Laufzeitbindungen
          │ EEBUS
          ▼
Wärmepumpe/Gateway
```

Hieraus folgen fünf fachliche Invarianten:

- Die Bridge ist Übersetzer und Verbindungsmanager, nicht dauerhafte Quelle erfundener Gerätezustände.
- Ein Home-Assistant-Config-Entry repräsentiert genau ein entferntes Gerät, identifiziert durch eine kanonische SKI.
- Transporterreichbarkeit der Bridge und Verbindung des entfernten Geräts sind verschiedene Zustände.
- Steuerbefehle dürfen nur auf die zum Gerät und Use Case passende Remote-Entity wirken.
- Energieoptimierung darf keine unbegrenzt alten Netz-, PV- oder Batteriewerte als aktuell ausgeben.

## 3. Ist-Architektur

### 3.1 Go-Bridge

- `cmd/eebus-bridge/app.go` konstruiert Zertifikat, Bridge, Registry, Use Cases, Provider, gRPC-Services, Watchdog und Shutdown-Lifecycle.
- `internal/eebus` kapselt eebus-go, Callbacks, Discovery, Vertrauen, Registry und einen nicht blockierenden In-Process-Eventbus.
- `internal/usecases` enthält lesende/steuernde Consumer-Use-Cases sowie die experimentellen MGCP-, VAPD- und VABD-Provider.
- `internal/grpc` bildet Use Cases und Registry auf acht Protobuf-Serviceverträge ab.
- Die Protobuf-Dateien unter `eebus-bridge/proto/eebus/v1` sind die sprachübergreifende Schnittstelle.

### 3.2 Home-Assistant-Integration

- `EebusCoordinator` hält Verbindung, Support-Flags, Polling, Writes, Stream-Handler und Provider-Fassade zusammen.
- `snapshot.py` führt Best-Effort-Reads parallel aus und publiziert einen vollständigen Snapshot.
- Sechs gRPC-Streams liefern Device-, LPC-, Monitoring-, DHW- und Raumheizungsereignisse. Der vorhandene OHPCF-Stream wird nicht konsumiert.
- Ein Fünf-Minuten-Poll gleicht nicht gestreamte oder verlorene Zustände ab.
- `ProviderManager` liest Home-Assistant-Sensoren, normalisiert Einheiten und pusht Grid/PV/Battery-Daten zustandsgetrieben.
- HA-Entities lesen den Coordinator-Snapshot und senden Writes über Coordinator-Methoden.

### 3.3 Qualitätsbaseline

- 149 Go-Tests und 114 Python-Testfunktionen sind vorhanden.
- `go test ./...` ist auf dem analysierten Stand grün.
- Go-Coverage liegt paketweise unter anderem bei 46,9 % für `internal/grpc`, 34,4 % für `internal/usecases` und 64,2 % für `internal/eebus`. Coverage ist ein Hinweis auf Risikozonen, kein alleiniges Qualitätsziel.
- `ruff check custom_components/` ist grün.
- Python-Tests und mypy konnten in der Analyseumgebung wegen fehlender installierter Werkzeuge nicht ausgeführt werden; dies ist kein festgestellter Fehler der Codebasis.
- CI führt Python-Lint, Typprüfung, Tests, Dependency-Audit sowie Go-Vet, Lint, Race-Tests, Integrationstest und Vulnerability-Scan aus.

## 4. Priorisierte Anforderungen

Prioritäten:

- **P0:** fachliche Korrektheit oder Betriebssicherheit;
- **P1:** hohe Zuverlässigkeit und Beherrschbarkeit weiterer Änderungen;
- **P2:** Wartbarkeit und Effizienz ohne akute Fehlsemantik;
- **P3:** optionaler Feinschliff nach Messung.

| ID | Priorität | Bedeutung | Kernziel |
|---|---:|---|---|
| SPEC2-01 | P0 | fachliche Zustandskorrektheit | Bridge- und Remote-Verbindung getrennt abbilden |
| SPEC2-02 | P0 | geräteübergreifende Betriebssicherheit | Heartbeat eindeutig dem Bridge-Lifecycle zuordnen |
| SPEC2-03 | P0 | sichere Energieoptimierung | veraltete Providerdaten aktiv ablaufen lassen |
| SPEC2-04 | P0 | Geräteisolation und Datenzuordnung | eine kanonische SKI in beiden Sprachen |
| SPEC2-05 | P1 | sprachübergreifende Kompatibilität | Proto-Generierung und Breaking Changes absichern |
| SPEC2-06 | P1 | dynamische Gerätefähigkeit | Capability-State-Machine statt uneindeutiger Booleans |
| SPEC2-07 | P1 | zeitnahe Konsistenz | vollständige Streams, Gap-Erkennung und Reconcile |
| SPEC2-08 | P1 | Änderbarkeit der HA-Seite | Session, Poller, Streams, Reducer und Provider trennen |
| SPEC2-09 | P1 | stabile API-Semantik | Validierung, Auflösung und Statuscodes vereinheitlichen |
| SPEC2-10 | P2 | Wartbarkeit der Go-Seite | nur belegte Wiederholungen deduplizieren |
| SPEC2-11 | P1 | vorhersehbarer Betrieb | Konfiguration strikt validieren |
| SPEC2-12 | P1 | Diagnose und Recovery | Fehlerursache, Freshness und Degradation sichtbar machen |
| SPEC2-13 | P1 | Regressionsschutz | risikobasierte Contract-, State- und Concurrency-Tests |

### SPEC2-01 – Bridge- und Geräteverbindung trennen (P0)

**Ist-Befund**

`DeviceService.GetStatus` liefert `running=true`, solange der gRPC-Service antwortet. Dieser Wert wird als `CoordinatorSnapshot.connected` gespeichert und vom HA-Connectivity-Sensor als EEBUS-Geräteverbindung angezeigt. Ein Remote-Disconnect löst zwar einen Refresh aus, der Refresh liefert jedoch erneut `running=true`.

**Soll**

- Der Vertrag unterscheidet mindestens:
  - `bridge_reachable`: gRPC/Bridge ist erreichbar;
  - `device_connected`: die konfigurierte Remote-SKI ist über SHIP verbunden;
  - `device_ready`: mindestens ein erforderlicher, frischer SPINE/Use-Case-Bindingzustand ist nutzbar.
- Die Go-Registry stellt den Verbindungszustand einer konkreten SKI threadsicher bereit.
- Die Device-API liefert einen gerätebezogenen Status; `GetStatus` bleibt Bridge-Status.
- HA verwendet `device_connected` für den Connectivity-Sensor und die Geräteverfügbarkeit. Bridge-Erreichbarkeit wird separat diagnostiziert.
- Disconnect und Trust-Removal setzen den gerätebezogenen Zustand sofort zurück und verwerfen nicht mehr gültige Nutzdaten.

**Abnahme**

- Ein Remote-Disconnect ändert den HA-Connectivity-Sensor ohne Warten auf das Fünf-Minuten-Polling auf `off`.
- Eine weiterhin erreichbare Bridge darf den Remote-Zustand nicht wieder auf `connected` setzen.
- Tests decken `bridge up/device down`, `bridge up/device up`, Trust-Removal und Reconnect ab.

**Bekannte Lücke**

`device_ready` ist noch nicht implementiert: `DeviceStatus` liefert aktuell nur `connected`/`last_transition`, kein aggregiertes Freshness-Signal über SPINE/Use-Case-Bindings. Umsetzung gehört inhaltlich zu SPEC2-12 (Diagnose/Degradation) und ist dort nachzuziehen, statt sie als Scope-Erweiterung in dieses P0-Ticket zu ziehen.

### SPEC2-02 – Heartbeat-Eigentümerschaft korrigieren (P0)

**Ist-Befund**

Die Bridge startet beim Aufbau der Anwendung genau einen lokalen LPC-Heartbeat und stoppt ihn beim Prozess-Shutdown. Gleichzeitig exponiert jeder gerätebezogene HA-Entry einen Heartbeat-Schalter. `StopHeartbeat(DeviceRequest)` ignoriert die SKI und stoppt den globalen Heartbeat. Bei mehreren Entries kann ein Gerät den Heartbeat aller Geräte beeinflussen.

**Sollentscheidung**

- Der LPC-Heartbeat ist Bridge-Lifecycle und bleibt automatisch aktiv, solange die Bridge läuft.
- Der gerätebezogene HA-Heartbeat-Schalter entfällt. HA zeigt nur einen read-only Diagnosezustand.
- Start/Stop-RPCs werden als veraltet markiert und in einer späteren brechenden API-Version entfernt. Solange sie existieren, darf ihre Semantik nicht fälschlich gerätebezogen erscheinen.
- Falls künftig manuelle Kontrolle notwendig wird, erfolgt sie als explizite Bridge-Administration mit Autorisierung und nicht als Geräte-Entity.

**Abnahme**

- Kein HA-Geräte-Entry kann den Heartbeat eines anderen Entries stoppen.
- Start und Shutdown der Bridge starten beziehungsweise stoppen den Heartbeat genau einmal.
- Migration entfernt nur die obsolete Entity und erhält alle übrigen Entity-IDs.

### SPEC2-03 – Freshness und Invalidierung für Providerdaten (P0)

**Ist-Befund**

Grid-, PV- und Battery-Pushes enthalten keinen Beobachtungszeitpunkt und keine Gültigkeitsdauer. Ist ein erforderlicher HA-Sensor nicht mehr verfügbar, sendet die Integration gar nichts; die Bridge kann dadurch den letzten Wert unbegrenzt weiter anbieten. Unbekannte Einheiten werden als Basiseinheit interpretiert. Teilfelder eines Publish-RPCs werden nacheinander mutiert.

**Soll**

- Jede Provider-Publikation trägt `observed_at` und eine explizite Gültigkeitsdauer oder ein semantisch gleichwertiges Ablaufdatum.
- Ein Publish ist ein vollständiger Ersatz-Snapshot. Fehlende optionale Werte bedeuten „unbekannt“, nicht „alten Wert behalten“.
- Die HA-Seite sendet bei fehlender, ungültiger oder zu alter Pflichtmessung eine Invalidierung. Stilles Nichtstun ist nicht zulässig.
- Die Bridge bietet abgelaufene oder invalidierte Daten gegenüber EEBUS nicht mehr als aktuelle Messung an.
- Unbekannte oder fehlende Einheiten werden abgelehnt; es gibt keine implizite Annahme von W, Wh oder Prozent.
- Alle Werte werden vor der ersten Mutation validiert. Publikationen pro Provider werden serialisiert. Ein Fehler darf nicht als erfolgreicher vollständiger Snapshot bestätigt werden.
- Ein unveränderter, weiterhin frischer HA-Zustand wird rechtzeitig vor Ablauf erneut publiziert. Die Freshness-Regel ist zentral definiert und mit Fake-Clock testbar.

**Abnahme**

- Nach Ablauf oder Sensor-Unavailability ist der Wert spätestens innerhalb der definierten Gültigkeitsdauer nicht mehr über den EEBUS-Provider lesbar.
- Tests decken unbekannte Einheiten, NaN/Inf, Grenzwerte, Ablauf, Invalidierung, unveränderte Republishes und konkurrierende Publikationen ab.
- Grid-Power kann weiterhin negativ sein; PV-Power, Energiezähler und SoC behalten ihre heutigen Wertebereiche.

### SPEC2-04 – Eine kanonische Geräteidentität (P0)

**Ist-Befund**

SKIs werden in Python-Config-Flow, Python-Coordinator, Go-gRPC-Validierung und Go-Registry separat normalisiert. Die Regeln unterscheiden sich bei Bindestrichen und Whitespace. HA-Unique-ID, Registry-Identifier und Device-Info-Abgleich verwenden teilweise die rohe Eingabe. Dadurch kann dieselbe physische SKI unterschiedlich gespeichert oder nicht wiedergefunden werden.

**Soll**

- Die kanonische Form ist exakt 40 hexadezimale Zeichen in einer festgelegten Schreibweise ohne Separatoren.
- Jede Eingabe wird an der Systemgrenze normalisiert und validiert; intern wird nur die kanonische Form gespeichert.
- Python und Go besitzen je genau eine produktive Normalisierungsfunktion mit denselben tabellengetriebenen Testvektoren.
- Config-Entry-Unique-ID, HA-Device-Identifier, gRPC-Requests, Registry-Key und Device-Info-Abgleich verwenden die kanonische SKI.
- Eine Config-Entry-Migration kanonisiert bestehende Entries und behandelt mögliche Dubletten deterministisch, ohne still Daten zu verlieren.
- Logs und Diagnosen zeigen standardmäßig nur eine gekürzte SKI.

**Abnahme**

- Groß-/Kleinschreibung, Doppelpunkte, Bindestriche und übliches Whitespace führen zum selben Identifier.
- Nicht hexadezimale oder nicht 40-stellige Werte werden vor Erstellung eines Entries abgelehnt.
- Ein formatierter bestehender Entry findet die von der Go-Registry kanonisch gelieferte Device-Info.

### SPEC2-05 – Protobuf-Vertrag reproduzierbar absichern (P1)

**Ist-Befund**

Eine Proto-Änderung erzeugt Go- und Python-Code über zwei Wege. CI regeneriert und prüft derzeit nur die Python-Stubs. Go-Generatoren sind lokal vorausgesetzt, Ausgabeverzeichnisse werden vor der Generierung nicht bereinigt, und es gibt keine Buf-Lint- oder Breaking-Change-Prüfung.

**Soll**

- Ein einziger dokumentierter Einstieg generiert beide Sprachen mit gepinnten Toolversionen.
- Die Generierung beginnt aus sauberen temporären Ausgabeverzeichnissen und ersetzt danach deterministisch die committed Stubs.
- CI prüft bei relevanten Änderungen:
  - `buf lint`;
  - Breaking Changes gegen den Zielbranch;
  - Drift der Go-Stubs;
  - Drift der Python-Stubs;
  - Vollständigkeit der Re-Exports in `proto_stubs.py`.
- Additive Felder/RPCs bleiben in `eebus.v1`. Brechende Semantik erhält erst dann `eebus.v2`, wenn eine kompatible Migration nicht möglich ist.
- Timestamp-, Duration- und Enum-Felder werden an der gRPC-Grenze validiert.

**Abnahme**

- Das Löschen oder manuelle Verändern eines generierten Go- oder Python-Files lässt CI fehlschlagen.
- Entfernen/Umnummerieren eines bestehenden Felds wird als Breaking Change erkannt.
- Zwei aufeinanderfolgende Generierungen erzeugen keinen Diff.

### SPEC2-06 – Fähigkeiten als Zustandsmodell statt Bool-Sammlung (P1)

**Ist-Befund**

Der Coordinator hält sieben optionale Support-Booleans. Je nach Read-Pfad bedeuten `NOT_FOUND` und `UNAVAILABLE` teils „unsupported“, teils „vorherigen Wert behalten“. Der OHPCF-Select wird nur beim initialen Plattform-Setup angelegt, wenn OHPCF im ersten Snapshot bereits unterstützt ist; später entdeckte Unterstützung kann die Entity nicht nachträglich erzeugen.

**Soll**

- Für jeden Use Case existiert ein expliziter Zustand:
  - `unknown`;
  - `available`;
  - `temporarily_unavailable`;
  - `unsupported`.
- Statuscodes besitzen systemweit dieselbe Bedeutung:
  - `UNIMPLEMENTED` = Vertrag/Feature nicht implementiert;
  - `UNAVAILABLE` = Komponente oder Transport aktuell nicht verfügbar;
  - `NOT_FOUND` = für diese SKI aktuell keine passende Entity/Datenbindung;
  - `FAILED_PRECONDITION` = bekannte, aber momentan nicht erfüllte Vorbedingung;
  - `INVALID_ARGUMENT` = Clientfehler.
- Entities werden deterministisch beim Plattform-Setup angelegt. Capability und Daten steuern nur Verfügbarkeit und Features, nicht die Existenz.
- Reconnect und Support-Events dürfen Fähigkeiten von `temporarily_unavailable` wieder auf `available` setzen.

**Abnahme**

- OHPCF, DHW oder HVAC können nach einem verspäteten Binding ohne Reload verfügbar werden.
- Ein temporäres `NOT_FOUND` entfernt keine Entity und wird nicht dauerhaft als `unsupported` gespeichert.
- Übergänge sind als reine Zustandsübergangstests abgedeckt.

### SPEC2-07 – Push-Pfad vollständig und verlusttolerant machen (P1)

**Ist-Befund**

Die Bridge implementiert `SubscribeOHPCFEvents`, die HA-Seite startet diesen Stream jedoch nicht. OHPCF-Änderungen von außen können daher bis zum nächsten Fünf-Minuten-Poll unsichtbar bleiben. Der Go-Eventbus verwirft bei vollem Subscriber-Puffer Ereignisse ohne Zähler pro Stream oder Gap-Signal; der spätere Poll repariert dies nur verzögert.

**Soll**

- HA konsumiert den OHPCF-Stream und reduziert dessen Payload in denselben Domänenzustand wie Polling.
- Jeder Stream-Familie wird pro SKI eine monotone Revision zugeordnet. Erkennt HA eine Lücke, fordert es genau einen koaleszierten Vollabgleich an.
- Support-Events ohne vollständigen Payload lösen ebenfalls einen koaleszierten Abgleich aus.
- Der Eventbus zählt Drops pro Subscriber/Stream-Familie und exponiert diese diagnostisch.
- Gleichzeitige Stream- und Poll-Ergebnisse werden über einen einzigen Reducer publiziert; ein älterer Stand darf keinen neueren überschreiben.
- Backoff, letzter Empfang, letzter Fehler und Reconnect-Anzahl sind beobachtbar.

**Abnahme**

- Ein externer OHPCF-Zustandswechsel erscheint ohne Fünf-Minuten-Wartezeit in HA.
- Ein künstlich erzeugter Event-Gap führt zu genau einem Vollabgleich und anschließend konsistentem Zustand.
- Tests beweisen, dass Event-Fluten keine unbeschränkte Task-Erzeugung auslösen.

### SPEC2-08 – Python-Laufzeit nach Verantwortungen schneiden (P1)

**Ist-Befund**

`coordinator.py` umfasst rund 680 Zeilen und kennt Channel-Lifecycle, Capability-Zustand, alle Write-RPCs, Stream-Erzeugung, Event-Parsing, Snapshot-Merge sowie Provider-Lifecycle. Der flache `CoordinatorSnapshot` besitzt mehr als 40 heterogene Felder. Stringbasierte `support_attr`-Zuweisung und dynamische Stub-/Methodennamen verschieben Fehler in die Laufzeit.

**Sollarchitektur**

```text
EebusCoordinator
  ├─ DeviceSession          # Channel, typisierte Stubs, Reads/Writes, Fehlerübersetzung
  ├─ SnapshotPoller         # parallele Reads ohne HA-Seiteneffekte
  ├─ StreamSupervisor       # Tasks, Backoff, Revisionen, Reconcile-Signale
  ├─ StateReducer           # pure, zeit-/revisionsbewusste Zustandsübergänge
  └─ ProviderManager        # HA-Sensoren -> frische Provider-Snapshots
```

- Der Coordinator orchestriert First Refresh, Veröffentlichung und Shutdown, enthält aber keine protokollspezifischen Einzelhandler mehr.
- Der Domänenzustand besteht aus unveränderlichen, gruppierten Dataclasses für Connection, Measurements, LPC, DHW, HVAC, OHPCF und Capabilities.
- Die HA-Entity-Schicht erhält schmale Selektoren auf diesen Zustand.
- Polling und Streaming verwenden dieselben Protobuf-Konverter und denselben Reducer.
- Stubs werden pro Channel/Session typisiert erzeugt und nicht pro Methode dynamisch per String gesucht.

**Abnahme**

- Jede der fünf Komponenten ist isoliert ohne laufendes Home Assistant testbar.
- Ein Stream- und ein Poll-Update desselben Feldes durchlaufen denselben Reducer.
- Shutdown beendet Provider und Streams, bevor der Channel geschlossen wird, und hinterlässt keine Tasks.

### SPEC2-09 – gRPC-Grenzen konsistent machen (P1)

**Ist-Befund**

Request-, Nil-, SKI- und Zahlenvalidierung sowie Entity-Auflösung wiederholen sich zwischen Services. Provider-Werte prüfen Endlichkeit, LPC-Writes derzeit nur `< 0`, wodurch NaN nicht abgewiesen wird. Einige Use-Case-Fehler werden differenziert gemappt, andere pauschal zu `INTERNAL`. Stream-Handler prüfen Bus und Request uneinheitlich.

**Soll**

- Gemeinsame Boundary-Helfer validieren Request, kanonische SKI, endliche Zahlen, Bereiche, Enums und Timestamps.
- Eine zentrale Fehlerpolicy bildet Domainfehler auf gRPC-Codes ab; erwartbare Geräteablehnungen sind nicht `INTERNAL`.
- Entity-Auflösung bleibt use-case-spezifisch, nutzt aber einen gemeinsamen Ablauf für explizite SKI, Mehrdeutigkeit, fehlende Registry und Not Found.
- Alle Streams prüfen Initialisierung, Request und Context gleichartig.
- Schreib-RPCs respektieren Context/Deadline bis in die Use-Case-Schicht, soweit die Bibliothek dies unterstützt.

**Abnahme**

- NaN/Inf werden in allen numerischen Write- und Publish-RPCs als `INVALID_ARGUMENT` abgewiesen.
- Mehrdeutige leere SKI liefert überall `FAILED_PRECONDITION`; unbekannte explizite SKI liefert `NOT_FOUND`.
- Tabellengetriebene Contract-Tests prüfen dieselbe Fehlerklasse über LPC, DHW, HVAC, OHPCF und Provider.

### SPEC2-10 – Go-Komposition und Provider gezielt deduplizieren (P2)

**Ist-Befund**

`Application` hält viele konkrete Use-Case-Felder und registriert diese einzeln. MGCP, VAPD und VABD wiederholen Measurement-Setup, ElectricalConnection, Publish-Mechanik, Debug-Logging und Consumer-Eventbehandlung. Auch gRPC-Streams wiederholen Subscribe/Filter/Context/Send-Schleifen.

**Soll**

- Die Application-Komposition verwendet kleine Registrierungsdeskriptoren für Name, Setup, Use Case und optionalen Service, ohne eine universelle Use-Case-Abstraktion einzuführen.
- Gemeinsame Provider-Helfer kapseln nur nachweislich identische Mechanik:
  - Measurement-Beschreibung und Update;
  - dreiphasige ElectricalConnection-Verknüpfung;
  - Initialisierungsprüfung;
  - serialisierte Snapshot-Publikation.
- Szenarien, Actor-/Entity-Typen, Scopes und Pflichtfelder bleiben in MGCP/VAPD/VABD explizit sichtbar.
- Ein kleiner Stream-Loop-Helfer darf Lifecycle und SKI-Filter teilen; Event-Mapping und Payloadbau bleiben servicebezogen.

**Abnahme**

- Provider-Szenarien sind weiterhin aus dem jeweiligen File direkt lesbar.
- Deduplizierung verändert keine Actor-, Scenario-, Scope-, Unit- oder Mandatory-Angabe; Golden-/Contract-Tests sichern dies.
- `NewApplication` besitzt keine vorgezogenen Laufzeitaktion wie einen gestarteten Heartbeat; Konstruktion und Start sind getrennt.

### SPEC2-11 – Konfiguration strikt und diagnostizierbar machen (P1)

**Ist-Befund**

Unbekannte YAML-Felder werden toleriert. Ungültige Integer-/Bool-Environmentwerte werden still ignoriert. Portbereiche und mehrere fachliche Kombinationen werden nicht zentral validiert. Teile der experimentellen Konfiguration sind in Code und Beispielkonfiguration unterschiedlich vollständig sichtbar.

**Soll**

- YAML-Decoding lehnt unbekannte Felder ab.
- Gesetzte, aber ungültige Environmentvariablen verhindern den Start mit einer präzisen Fehlermeldung.
- Ports, Bind-Adresse, Security-Dateien, Zertifikatsoptionen und Featureabhängigkeiten werden nach Defaults und Overrides gemeinsam validiert.
- Effektive Konfiguration kann ohne Geheimnisse strukturiert geloggt beziehungsweise diagnostiziert werden.
- HA validiert Host, Port, Securitykombination und kanonische SKI vor dem Speichern.
- Experimentelle Provider zeigen ihren Zustand `disabled`, `configured`, `ready`, `stale` oder `error` diagnostisch.

**Abnahme**

- Tippfehler in YAML und ungültige Env-Werte führen deterministisch zu einem Startfehler.
- Tests decken Priorität `defaults < YAML < environment` und alle Security-Modi ab.
- Token, Private Key und CA-Inhalt erscheinen nie in Logs oder Diagnosen.

### SPEC2-12 – Beobachtbarkeit und Recovery ausbauen (P1)

**Soll**

- Diagnosen enthalten getrennt:
  - Bridge-Erreichbarkeit und lokales SKI-Kürzel;
  - Remote-Verbindung, letzte Verbindung/Trennung und Readiness;
  - Capability-Zustände mit letztem Fehlercode;
  - Poll-Dauer und Zeitpunkt des letzten erfolgreichen Vollabgleichs;
  - Streamstatus, Revision, Gap-/Reconnect-Zähler;
  - Eventbus-Drops;
  - Provider-Freshness und letzte erfolgreiche Publikation.
- Hochfrequente gleiche Fehler werden zustandsbasiert geloggt: einmal beim Übergang in Fehler, Debug während des Fehlers, einmal bei Recovery.
- Der Monitoring-Watchdog markiert zunächst das betroffene Gerät als degraded. Ein kompletter Prozessrestart bleibt letzte Recovery-Stufe, da er alle verbundenen Geräte betrifft.
- Recovery-Entscheidungen sind mit Fake Clock deterministisch testbar.

**Abnahme**

- Ein Diagnose-Dump erklärt ohne Debug-Log, ob ein Wert wegen Bridge, Remote-Verbindung, Capability, Stream, Poll oder Provider-Freshness fehlt.
- Keine vollständige SKI und kein Secret wird ausgegeben.

### SPEC2-13 – Risikobasierte Teststrategie (P1)

**Pflichttests**

- Identität: gemeinsame Normalisierungsvektoren in Go und Python, Config-Migration, Dublettenfall.
- Connectivity: Bridge/Device-Zustandsmatrix, Disconnect, Trust-Removal, Reconnect.
- Heartbeat: genau einmal pro Bridge-Lifecycle und keine Cross-Entry-Steuerung.
- StateReducer: Poll/Stream-Reihenfolge, alte Revision, Capability-Übergänge, atomare Veröffentlichung.
- Streaming: OHPCF, Gap, Drop, Reconnect, Cancel/Shutdown und Event-Flut.
- Provider: Einheiten, Wertebereiche, Snapshot-Ersatz, Freshness, Ablauf, Invalidierung, Parallelität.
- gRPC: Statuscode-Matrix, Security für Unary/Stream/Health/Reflection und Multi-Device-Isolation.
- Proto: Lint, Breaking, Go-/Python-Drift und Generierungsreproduzierbarkeit.

**Hardware-/Systemtests**

- Pairing und Reconnect mit einem unterstützten Gateway.
- Remote-Disconnect bei laufender Bridge.
- LPC-Write und Failsafe-Verhalten mit aktivem Bridge-Heartbeat.
- DHW-/HVAC-Read und Write einschließlich Geräteablehnung.
- OHPCF-Änderung von der Geräteseite bis zur HA-Entity.
- Grid-Provider mit frischem Wert, unverändertem Wert und absichtlich veraltetem HA-Sensor.

Eine globale Coverage-Zahl ist kein Abnahmekriterium. Neue oder geänderte Zustandsautomaten, Fehlerzweige und Concurrency-Pfade müssen jedoch vollständig verhaltensbasiert getestet sein.

## 5. Zielzustand der Schnittstellen

### 5.1 Status

Der Statusvertrag soll fachlich getrennte Ressourcen verwenden:

```text
BridgeStatus
  running
  local_ski
  health

DeviceStatus(ski)
  connected
  ready
  last_transition
  capabilities[]
```

`running` darf niemals als Ersatz für `connected` verwendet werden.

### 5.2 Provider-Publikation

Jede Publikation ist ein vollständiger, zeitlich begrenzter Snapshot:

```text
ProviderSnapshot
  observed_at
  valid_for
  values...
```

Omission löscht den optionalen Wert aus dem aktuellen Snapshot. Eine explizite Invalidierung beendet die Gültigkeit sofort. Die Bridge prüft Ablauf auch ohne einen weiteren Clientaufruf.

### 5.3 Ereignisse

Jede Eventfamilie liefert:

```text
Event
  ski
  type
  revision
  observed_at
  payload
```

Revisionen gelten pro SKI und Streamfamilie. Dadurch erzeugen gefilterte Ereignisse anderer Familien keine falschen Lücken.

## 6. Umsetzungsreihenfolge

### Phase A – Semantische Sicherheit

1. SPEC2-04 kanonische SKI plus Migration.
2. SPEC2-01 echter Remote-Verbindungszustand.
3. SPEC2-02 Heartbeat als Bridge-Lifecycle.
4. SPEC2-03 Provider-Freshness und Invalidierung.
5. SPEC2-05 Vertrags-CI für beide Sprachen.

Diese Phase soll möglichst kleine, einzeln releasebare Änderungen enthalten. Strukturelle Großumbauten sind keine Voraussetzung für die Korrekturen.

### Phase B – Einheitlicher Laufzeitzustand

1. SPEC2-06 Capability-State-Machine und deterministische Entities.
2. StateReducer und gruppiertes Domänenmodell aus SPEC2-08.
3. SPEC2-07 OHPCF-Stream, Revisionen und Gap-Reconcile.
4. SPEC2-09 konsistente Boundary- und Fehlersemantik.

### Phase C – Struktur und Betrieb

1. Restliche Zerlegung aus SPEC2-08.
2. SPEC2-11 strikte Konfiguration.
3. SPEC2-12 Diagnostik und abgestufte Recovery.
4. SPEC2-10 gezielte Go-Deduplizierung.

### Phase D – Gemessene Optimierung (P3)

Erst nach Instrumentierung prüfen:

- Poll-Latenz und Zahl der RPCs;
- Kosten von `ListPairedDevices` in jedem Vollabgleich;
- Zahl und Speicherbedarf separater Eventbus-Subscriptions;
- Stub-Erzeugung und Snapshot-Kopien;
- Logvolumen bei längeren Ausfällen.

Optimierungen werden nur umgesetzt, wenn Messwerte einen relevanten Engpass zeigen. Mögliche Maßnahmen sind Caching unveränderlicher Device-Klassifikation, Wiederverwendung typisierter Stubs und feinere Poll-Intervalle pro Freshness-Klasse.

## 7. Nicht-Ziele

- Keine neuen EEBUS-Use-Cases oder Hardwareversprechen.
- Keine Cloud-Anbindung.
- Kein Austausch von gRPC, eebus-go, Home Assistants Coordinator-Modell oder der Container-Topologie.
- Keine automatische Stabilerklärung der experimentellen MGCP/VAPD/VABD-Provider.
- Keine universelle Abstraktion, die fachlich verschiedene EEBUS-Szenarien versteckt.
- Keine Protobuf-v2-Migration allein wegen des Namens dieser Spezifikation.
- Keine Performanceänderung ohne Messung und vorher festgelegtes Ziel.

## 8. Definition of Done für jedes Arbeitspaket

Ein Arbeitspaket ist abgeschlossen, wenn:

- die fachliche Invariante und Fehlersemantik im Code sichtbar sind;
- Go- und Python-Seite bei Vertragsänderungen gemeinsam aktualisiert wurden;
- Unit-, Contract- und relevante Concurrency-Tests grün sind;
- `go test -race ./...`, Go-Lint/Vet, Ruff, mypy und Python-Tests grün sind;
- generierte Dateien reproduzierbar und driftfrei sind;
- Konfigurationsmigration und Rückwärtskompatibilität dokumentiert sind;
- Diagnosen keine Secrets oder vollständigen SKIs enthalten;
- bei hardwareabhängigem Verhalten ein reproduzierbares manuelles Testprotokoll vorliegt;
- keine obsolete Entity, Option oder RPC-Semantik ohne Migrationsweg zurückbleibt.

## 9. Erwarteter Gesamtnutzen

Nach Umsetzung der P0- und P1-Pakete zeigt Home Assistant den tatsächlichen Gerätezustand statt nur die Erreichbarkeit der Bridge, der Heartbeat besitzt eine eindeutige globale Verantwortung, und Energieoptimierung kann nicht unbegrenzt mit veralteten Eingangsdaten weiterlaufen. Ein gemeinsames Zustandsmodell macht Polling und Streaming konsistent; ein abgesicherter Protobuf-Workflow verhindert Sprachdrift. Die anschließende strukturelle Zerlegung reduziert Änderungsrisiken, ohne die fachlich unterschiedlichen EEBUS-Use-Cases hinter spekulativen Abstraktionen zu verstecken.
