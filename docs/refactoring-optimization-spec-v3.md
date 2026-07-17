# Refactoring- und Optimierungsspezifikation v3

Status: Entwurf

Analysebasis: Softwarestand `1a0d93128895` auf `main`, 17.07.2026

Geltungsbereich: `custom_components/eebus`, `eebus-bridge`, Protobuf-Vertrag und zugehörige Build-/Test-Automatisierung

## 1. Zweck und Herleitung

Diese Spezifikation beschreibt die aus dem aktuellen Quellcode abgeleiteten Refactoring- und Optimierungsmaßnahmen. Maßgeblich sind die gegenwärtigen Laufzeitpfade, Verträge und Tests. Frühere Spezifikationen sind weder fachliche Quelle noch Fortschrittsnachweis für dieses Dokument.

Die Reihenfolge richtet sich nach Bedeutung, nicht nach Änderungsgröße:

1. semantische Korrektheit und Schutz vor falschen Gerätezuständen,
2. Datenintegrität und Ausfallsicherheit,
3. Ressourcenverbrauch und Skalierung,
4. Wartbarkeit und Entwicklungsdurchsatz.

Reine Stiländerungen ohne messbaren Nutzen sind nicht Teil der Spec.

## 2. Aktuelles Systemverständnis

Das System besteht aus zwei Prozessen:

- Die Home-Assistant-Integration verwaltet je Config Entry ein EEBUS-Zielgerät, liest dessen Zustand über gRPC, verarbeitet Push-Streams und stellt HA-Entitäten bereit. Optional überträgt sie Netz-, PV- und Batteriewerte aus HA-Sensoren an die Bridge.
- Die Go-Bridge verwaltet die lokale EEBUS-Identität, SHIP/SPINE-Verbindungen, Geräte- und Entity-Auflösung, EEBUS-Use-Cases sowie den gRPC-Server.

Der Protobuf-Vertrag unter `eebus-bridge/proto/eebus/v1` verbindet beide Teile. Der aktuelle Leseweg kombiniert mehrere Unary-RPCs mit sieben dauerhaft laufenden Streams je HA-Geräteeintrag. Intern verteilt die Bridge EEBUS-Änderungen über einen gepufferten, verlustbehafteten Event Bus an die gRPC-Streams.

### 2.1 Positive Ausgangslage

- SKIs werden in beiden Sprachen kanonisiert und gerätebezogene Auflösungen vermeiden bei mehreren Geräten eine zufällige Auswahl.
- Transport-Sicherheit trennt Loopback-Klartext und TLS mit Bearer-Token; mutierende Provider-RPCs werden nicht ungesichert exponiert.
- Go-Registry, Stream- und Provider-Lebenszyklen besitzen bereits isolierte Tests.
- Python verwendet unveränderliche, gruppierte Zustandsobjekte und kennt vier Capability-Zustände.
- Provider-Pushes auf HA-Seite werden serialisiert und zusammengefasst.
- Go-Tests, `go vet` und Ruff sind auf dem analysierten Stand grün.

Diese Mechanismen sollen erhalten und als Grundlage der Zielarchitektur genutzt werden.

## 3. Zentrale Befunde

### 3.1 Mehrere Wahrheiten über denselben Python-Zustand

`state.py` definiert einen gruppierten `DomainState`, `models.py` zusätzlich einen flachen `CoordinatorSnapshot`. `snapshot.py` erzeugt beim Poll einen neuen vollständigen Domain-State, flacht ihn ab und gibt nur den flachen Snapshot zurück. `coordinator.py` übernimmt daraus lediglich Capability-Werte in seinen eigenen `_domain_state`. Stream-Ereignisse verändern wiederum diesen zweiten, nicht durch den Poll hydratisierten Domain-State und mischen einzelne flache Felder direkt in `coordinator.data`.

Folgen:

- Poll und Stream haben keine gemeinsame, autoritative Zustandsinstanz.
- Ein während eines laufenden Polls empfangenes neueres Stream-Ereignis kann beim Abschluss des älteren Polls überschrieben werden.
- Reducer-Regeln gelten nicht für alle Aktualisierungspfade gleich.
- Neue Felder müssen in Dataclass, TypedDict, Flattening, Polling, Stream-Merge und Entitäten synchron gehalten werden.

### 3.2 Capability und Fehler werden aus indirekten Signalen abgeleitet

Die Python-Seite leitet Support überwiegend aus gRPC-Statuscodes ab. Die Bridge verwendet Statuscodes jedoch nicht einheitlich:

- ein deaktivierter lokaler Use-Case kann `UNAVAILABLE` liefern,
- eine noch nicht gebundene Remote-Entity liefert häufig `NOT_FOUND`,
- fehlende Geräteauswahl kann `FAILED_PRECONDITION` sein,
- einzelne aggregierte Reads können erfolgreich sein, obwohl Teilwerte fehlen,
- nicht alle Use-Cases bilden Domänenfehler gleich differenziert ab.

Damit lassen sich „vom Gerät nicht unterstützt“, „lokal deaktiviert“, „noch nicht gebunden“, „vorübergehend nicht lesbar“ und „veralteter letzter Wert“ nicht zuverlässig unterscheiden.

### 3.3 Push ist effizient, aber nicht zuverlässig geordnet

Der Event Bus hat pro Subscriber einen Puffer von 64 Elementen und verwirft bei Überlast Ereignisse. Ein Sequenzzähler, ein Gap-Signal oder ein Drop-Zähler pro Subscriber existiert nicht. Die Python-Seite startet sieben voneinander unabhängige Stream-Tasks und erhält weder einen konsistenten Initialzustand noch eine domänenübergreifende Reihenfolge. Der Fünf-Minuten-Poll korrigiert Fehler nur verzögert.

### 3.4 Ressourcen werden pro Geräte-Config-Entry vervielfacht

Jeder HA-Eintrag besitzt einen eigenen Channel Manager, eine globale Device-Event-Subscription und sechs gerätebezogene Subscriptions. Mehrere Geräte an derselben Bridge teilen weder Kanal noch Bridge-Lebenszyklus. Die Zahl dauerhafter Tasks und Subscriptions wächst damit ungefähr um sieben je Gerät, obwohl Endpunkt und Credentials identisch sein können.

### 3.5 Recovery ist prozessweit statt gerätebezogen

Der Monitoring-Watchdog erkennt festhängende Entity-Bindings gerätebezogen, beendet bei einem Treffer aber den gesamten Bridge-Prozess. Dadurch verlieren auch gesunde Geräte und alle gRPC-Clients kurzzeitig ihre Verbindung. Vor dem Neustart gibt es keinen gezielten Rebind-/Reconnect-Versuch für das betroffene Gerät.

### 3.6 Provider-Daten können teilweise oder veraltet publiziert werden

Grid-, PV- und Battery-RPCs validieren Requests vollständig, schreiben deren Felder anschließend aber nacheinander in die jeweiligen Provider. Ein Fehler nach dem ersten Feld hinterlässt einen Mischzustand. Wenn ein benötigter HA-Sensor ausfällt, sendet die Integration keinen expliziten Invalidierungszustand; der zuletzt publizierte Wert kann in der Bridge unbegrenzt weiterleben.

### 3.7 Wiederholung bindet Fachlogik an Infrastruktur

- `Application` verdrahtet und registriert eine große Zahl konkreter Use-Cases und gRPC-Services manuell.
- DHW- und Room-Heating-Setpoints sowie die beiden System-Function-Implementierungen wiederholen Lifecycle-, Entity-, Request- und Write-Abläufe.
- MGCP, VAPD und VABD wiederholen Provider- und Measurement-Server-Muster.
- gRPC-Services wiederholen Request-Prüfung, SKI-Auflösung, Event-Filterung und Stream-Schleifen.

Eine pauschale generische Abstraktion wäre riskant; die wiederkehrenden technischen Abläufe lassen sich jedoch kompositorisch extrahieren.

### 3.8 Konfiguration und Vertragsprüfung sind lückenhaft

- YAML akzeptiert unbekannte Schlüssel.
- Ungültige Boolean-/Port-Umgebungsvariablen werden stillschweigend ignoriert.
- Portbereiche und mehrere Feldkombinationen werden nicht zentral validiert.
- Der Config Flow fasst TLS-, Authentifizierungs-, Protokoll- und Erreichbarkeitsfehler weitgehend als `cannot_connect` zusammen.
- Die Proto-Drift-Prüfung regeneriert nur Python-Stubs. Buf-Lint, Breaking-Change-Prüfung und Go-Stub-Drift sind nicht Teil desselben Gates.

### 3.9 Evidenzzuordnung

| Befund | Primäre Implementierungsstellen |
|---|---|
| parallele Python-Zustände | `custom_components/eebus/state.py`, `models.py`, `snapshot.py`, `coordinator.py` |
| sieben Streams je Eintrag | `EebusCoordinator.async_start_streams`, `StreamManager` |
| verlustbehafteter Event Bus | `eebus-bridge/internal/eebus/eventbus.go` |
| indirekte Capability-Semantik | `snapshot.py:_poll_read`, `state.py:next_capability_state`, gRPC-Services |
| prozessweiter Watchdog | `eebus-bridge/cmd/eebus-bridge/app.go:startWatchdog` |
| partielle Provider-Publikation | `grid_service.go`, `visualization_service.go`, `mgcp.go`, `vapd.go`, `vabd.go` |
| wiederholte Use-Case-Abläufe | `dhw.go`, `roomheatingtemp.go`, `dhwsysfn.go`, `roomheatingsysfn.go` |
| lockere Konfiguration | `eebus-bridge/internal/config/config.go` |
| unvollständiges Proto-Gate | `.github/workflows/ci.yml`, `generate_proto.sh`, `eebus-bridge/buf.gen.yaml` |

## 4. Zielarchitektur

### 4.1 Zustandsfluss

```text
gRPC Poll-Ergebnis ─┐
                    ├─> geordnete Observation Queue ─> DeviceStateStore ─> HA-Entitäten
gRPC Stream-Event ──┘                                  │
                                                       └─> ein unveränderlicher DeviceState
```

Es gibt je Zielgerät genau einen autoritativen, unveränderlichen `DeviceState`. Polls und Events erzeugen typisierte Observations/Patches, verändern aber niemals selbst `coordinator.data`. Ein serieller Reducer wendet diese Observations in Empfangsreihenfolge an und veröffentlicht den resultierenden Zustand atomar.

### 4.2 Bridge-Client-Lebenszyklus

```text
HA Config Entries ─> BridgeRuntime (pro Endpunkt + Credential-Satz)
                         ├─ ein gRPC-Kanal
                         ├─ ein globaler Bridge-Lebenszyklus
                         └─ DeviceSession (pro SKI)
                                ├─ Poller
                                ├─ ein konsolidierter Event-Stream
                                └─ Writer
```

Provider-Zuordnungen bleiben geräte-/eintragsbezogen. Transport, globale Bridge-Ereignisse und Kanal-Reconnect werden geteilt und referenzgezählt.

### 4.3 Bridge-interne Schichten

```text
SHIP/SPINE Adapter -> Capability/Device Registry -> Use-Case Controller
                                              └──> versionierter Event-Stream
gRPC Adapter ------------------------------------> Controller/Registry
```

gRPC-Handler übersetzen nur noch Vertrag, Validierung und Fehler. Entity-Auflösung, Capability-Zustand und Schreibtransaktionen liegen in testbaren Controllern. Die Composition Root registriert Module deklarativ, enthält aber keine Use-Case-Fachlogik.

## 5. Priorisierte Arbeitspakete

| ID | Maßnahme | Priorität | Hauptnutzen | Aufwand | Abhängigkeit |
|---|---|---:|---|---:|---|
| SPEC3-01 | Autoritativer Python-DeviceState | P0 | Korrektheit | L | – |
| SPEC3-02 | Explizite Capability- und Fehlersemantik | P0 | Korrektheit, Diagnose | L | – |
| SPEC3-03 | Konsolidierter, lückenerkennender Event-Stream | P1 | Datenintegrität, Effizienz | L | 01, 02 |
| SPEC3-04 | Geteilter HA-BridgeRuntime | P1 | Skalierung, Lifecycle | M | 03 |
| SPEC3-05 | Gestufte gerätebezogene Recovery | P1 | Verfügbarkeit | M | 02 |
| SPEC3-06 | Atomare Provider-Snapshots mit Gültigkeit | P1 | Datenintegrität | M | 02 |
| SPEC3-07 | Einheitliche gRPC-Adapter und Fehlerabbildung | P1 | Korrektheit, Wartbarkeit | M | 02 |
| SPEC3-08 | Modulare Go-Composition und Use-Case-Bausteine | P2 | Wartbarkeit | L | 07 |
| SPEC3-09 | Strikte Konfiguration und sichere Diagnose | P2 | Betriebssicherheit | M | – |
| SPEC3-10 | Proto-Governance und deterministische Generierung | P2 | Änderungssicherheit | M | 02, 03 |

`P0` blockiert weitere funktionale Erweiterungen in betroffenen Pfaden. `P1` soll vor einer Ausweitung auf mehrere Geräte oder stabil beworbene Provider-Funktionen abgeschlossen sein. `P2` verbessert den nachhaltigen Änderungsprozess.

### SPEC3-01 – Autoritativer Python-DeviceState

#### Ziel

`coordinator.data` enthält direkt einen unveränderlichen, gruppierten `DeviceState`. `CoordinatorSnapshot`, `flatten`, `_push_data` und parallele interne Zustandskopien entfallen nach abgeschlossener Migration.

#### Anforderungen

1. Ein `DeviceStateStore` besitzt den einzigen veränderbaren Verweis auf den aktuellen `DeviceState`.
2. Poller und Stream-Consumer liefern typisierte Patches an eine gemeinsame serielle Queue.
3. Poll-Ergebnisse dürfen nach Poll-Start eingetroffene Stream-Werte nicht überschreiben. Dies wird über Queue-Reihenfolge oder eine pro Feld/Domain erfasste Revision garantiert.
4. Capability-Übergänge, Wertlöschung und Last-known-value-Regeln werden ausschließlich im Reducer implementiert.
5. Entitäten lesen über kleine, typisierte Selektoren aus `DeviceState`, nicht über String-Keys und verschachtelte `TypedDict.get`-Aufrufe.
6. Der Coordinator wird zur HA-Fassade: Start/Stop, Refresh-Anforderung und Zustandspublikation. Poll-, Stream-, Write- und Provider-Logik liegen in eigenen Komponenten.
7. Ein Schreibvorgang nutzt eine langlebige `DeviceSession`; er erzeugt sie nicht für jeden Aufruf neu.

#### Zustandsregeln

- `AVAILABLE` mit Wert: Wert aktualisieren und als frisch markieren.
- `AVAILABLE` ohne erwarteten Wert: Wert explizit leeren; ein erfolgreicher leerer Read ist kein Last-known-value-Signal.
- `TEMPORARILY_UNAVAILABLE`: letzten Wert behalten, aber die betroffene Entität als nicht frisch/nicht verfügbar kennzeichnen.
- `UNSUPPORTED`: Wert löschen und Capability dauerhaft deaktivieren, bis ein explizites Support-Ereignis das Gegenteil meldet.
- Disconnect: fachliche Werte dürfen für Diagnosezwecke intern erhalten bleiben, operative Entitäten sind jedoch nicht verfügbar.

#### Abnahme

- Ein deterministischer Test startet einen Poll, verarbeitet vor dessen Abschluss einen neueren Stream-Wert und weist nach, dass der Stream-Wert bestehen bleibt.
- Poll und Stream erzeugen für äquivalente Eingaben exakt denselben `DeviceState`.
- Kein Produktionspfad verändert `coordinator.data` über Dictionary-Merge.
- Das Hinzufügen eines Messfeldes erfordert höchstens Domain-Modell, Protobuf-Konverter und Entitätsbeschreibung, nicht zusätzlich eine flache Spiegelstruktur.

### SPEC3-02 – Explizite Capability- und Fehlersemantik

#### Ziel

Support, Bindungszustand und Datenfrische sind Teil des Bridge-Vertrags und werden nicht mehr aus zufälligen RPC-Fehlern rekonstruiert.

#### Anforderungen

1. Die Bridge führt je SKI und Capability einen Registry-Eintrag mit mindestens:
   - Capability-ID als Enum,
   - Zustand `UNKNOWN`, `AVAILABLE`, `TEMPORARILY_UNAVAILABLE`, `UNSUPPORTED`,
   - Grund als Enum, beispielsweise `LOCAL_DISABLED`, `REMOTE_NOT_ADVERTISED`, `ENTITY_NOT_BOUND`, `READ_FAILED`,
   - Zeitpunkt der letzten Änderung.
2. Ein additiver `GetDeviceCapabilities(DeviceRequest)`-RPC liefert diese Daten. Bestehende RPCs bleiben zunächst kompatibel.
3. Use-Case-Support- und Disconnect-Ereignisse aktualisieren dieselbe Registry.
4. Alle gRPC-Services verwenden eine gemeinsame Fehlerklassifikation:
   - fehlerhafte Eingabe → `INVALID_ARGUMENT`,
   - fachlich nicht zulässige Aktion → `FAILED_PRECONDITION`,
   - explizites Gerät/Entity nicht gefunden → `NOT_FOUND`,
   - lokaler Dienst vorübergehend nicht betriebsbereit → `UNAVAILABLE`,
   - unbekannter interner Fehler → `INTERNAL` ohne sensible Details.
5. Ein erfolgreicher Aggregate-Read darf eine Capability nicht als verfügbar markieren, wenn alle zugehörigen Teilreads fehlgeschlagen sind.
6. Python verwendet den Capability-RPC als Wahrheit. Statuscode-Inferenz bleibt nur als klar gekennzeichneter Kompatibilitätsweg für ältere Bridges.

#### Abnahme

- Für deaktiviert, remote nicht unterstützt, noch nicht gebunden und temporären Lesefehler existieren getrennte Contract-Tests.
- DHW, HVAC, LPC, Monitoring und OHPCF bilden äquivalente Domänenfehler gleich ab.
- HA zeigt bei `UNSUPPORTED` keine bedienbare Entität und bei `TEMPORARILY_UNAVAILABLE` keinen fälschlich aktuellen Wert.

### SPEC3-03 – Konsolidierter, lückenerkennender Event-Stream

#### Ziel

Ein Gerät verwendet einen geordneten Stream statt sechs fachlicher und eines globalen Streams. Verlorene Ereignisse sind erkennbar und führen sofort zu einem Resync.

#### Anforderungen

1. Ein additiver `SubscribeDeviceState(DeviceRequest)`-RPC liefert ein Envelope mit:
   - kanonischer SKI,
   - monotoner gerätebezogener `revision`,
   - Event-Zeitpunkt,
   - `oneof` für Device-, Measurement-, LPC-, DHW-, HVAC-, OHPCF- und Capability-Payload,
   - explizitem `resync_required`-Payload.
2. Die erste Nachricht enthält die aktuelle Revision und fordert bei fehlendem vollständigem Initialzustand einen Poll an.
3. Der Event Bus zählt Drops pro Subscriber. Nach einem Drop wird nicht still fortgesetzt, sondern genau ein `resync_required` signalisiert, sobald der Subscriber wieder schreiben kann.
4. Python erkennt Revisionslücken und startet einen zusammengefassten Refresh; parallele Refresh-Stürme werden koalesziert.
5. Reconnect beginnt mit Backoff bei Basiswert 2 Sekunden. Der erste Retry darf nicht unnötig auf 4 Sekunden verdoppelt werden.
6. Die bisherigen Streams bleiben für eine Übergangsphase bestehen und werden erst in einer neuen breaking API-Version entfernt.

#### Abnahme

- Überlauf-, Reconnect-, doppelte Event-, Revisionslücken- und Resync-Tests sind deterministisch.
- Im Normalbetrieb läuft pro Geräte-Session genau ein fachlicher Stream-Task.
- Nach absichtlich verworfenen Events stimmt der HA-Zustand nach einem Resync mit einem frischen Poll überein.

### SPEC3-04 – Geteilter HA-BridgeRuntime

#### Ziel

HA-Einträge mit demselben Bridge-Endpunkt und identischen Credentials teilen Transport und globale Ressourcen.

#### Anforderungen

1. Ein `BridgeRuntimeRegistry` verwaltet referenzgezählte Runtimes nach kanonischem Host, Port, Security Mode sowie Hashes von CA und Token. Geheimnisse erscheinen weder im Schlüsseltext noch in Logs.
2. Der Runtime gehören Channel Manager, Reconnect-Zustand und bridgeweite Statusdaten.
3. Je SKI existiert eine `DeviceSession` mit Store, Poller, Stream und Writer.
4. Provider Manager bleiben je Config Entry getrennt, damit HA-Sensorzuordnungen nicht zwischen Geräten vermischt werden.
5. Unload beendet eine Runtime erst nach Freigabe des letzten Nutzers.
6. Credential- oder Endpoint-Änderungen erzeugen atomar eine neue Runtime; die alte wird nach erfolgreicher Übergabe geschlossen.

#### Abnahme

- Zwei Einträge an derselben Bridge verwenden nachweislich einen Channel.
- Das Entladen eines Eintrags unterbricht den anderen nicht.
- Unterschiedliche Credential-Sätze werden niemals zusammengelegt.

### SPEC3-05 – Gestufte gerätebezogene Recovery

#### Ziel

Ein festhängendes Gerät beeinträchtigt zunächst nur seine eigene Session. Ein Prozessneustart ist die letzte Eskalationsstufe.

#### Anforderungen

1. Der Watchdog führt pro SKI einen Zustand `healthy`, `stale`, `recovering`, `failed` sowie Versuchszähler und Zeitpunkte.
2. Bei Staleness werden in Reihenfolge versucht:
   1. Registry-Entity-Cache des Geräts invalidieren,
   2. gezielten SHIP/SPINE-Reconnect beziehungsweise Unregister/Register auslösen,
   3. eine neue Grace Period abwarten,
   4. erst nach begrenzten erfolglosen Versuchen einen Prozessneustart anfordern.
3. Parallele Recovery desselben Geräts ist ausgeschlossen.
4. Gesamt-Health und Geräte-Health werden getrennt. Ein gesund arbeitender gRPC-Server wird nicht allein wegen eines einzelnen Geräts sofort `NOT_SERVING`.
5. Recovery-Ereignisse werden mit gekürzter SKI, Stufe, Versuch und Dauer protokolliert.

#### Abnahme

- Ein Test mit zwei Geräten weist nach, dass die Recovery eines Geräts das andere nicht aus Registry oder Streams entfernt.
- Erfolgreicher Rebind verhindert den Prozessneustart.
- Dauerhaftes Versagen eskaliert nach der definierten Zahl von Versuchen genau einmal.

### SPEC3-06 – Atomare Provider-Snapshots mit Gültigkeit

#### Ziel

Downstream-Geräte sehen nur vollständige, gültige Grid-/PV-/Battery-Samples und verwenden keine unbegrenzt alten Optimierungsdaten.

#### Anforderungen

1. Jeder Publish-Request repräsentiert ein vollständiges Provider-Sample mit `observed_at` und `valid_until` oder einer gleichwertigen Gültigkeitsangabe.
2. Der Vertrag unterstützt explizite Invalidierung, wenn der erforderliche Power-Sensor nicht verfügbar ist.
3. Die Bridge validiert zuerst den gesamten Request und übernimmt ihn anschließend unter einem Provider-Lock als neue unveränderliche Last-good-Snapshot-Version.
4. SPINE-Publikation eines Samples wird serialisiert. Bei Teilfehler bleibt der veröffentlichte Registry-Zustand auf der vorherigen vollständigen Version und der RPC schlägt fehl.
5. Nach Ablauf oder expliziter Invalidierung wird das Sample nicht weiter als aktuell angeboten.
6. Optionale Felder besitzen definierte Semantik: „nicht im Sample enthalten“ bedeutet entweder „löschen“ oder „letzten Wert behalten“; die Entscheidung wird im Proto durch Presence/FieldMask eindeutig ausgedrückt.
7. Python sendet beim Wechsel des erforderlichen Sensors auf unavailable/unknown eine Invalidierung und beim Shutdown best effort ebenfalls.

#### Abnahme

- Fehler beim zweiten oder dritten Feld erzeugen keinen gemischten sichtbaren Snapshot.
- Ablauf und Invalidierung sind mit Fake Clock testbar.
- Burst-Coalescing auf HA-Seite bleibt erhalten.

### SPEC3-07 – Einheitliche gRPC-Adapter und Fehlerabbildung

#### Ziel

gRPC-Services enthalten keine duplizierte Entity- oder Stream-Infrastruktur und verhalten sich über alle Fachbereiche gleich.

#### Anforderungen

1. Gemeinsame Helfer validieren Request und SKI und lösen genau eine kompatible Entity oder einen klassifizierten Fehler auf.
2. Die Convenience-Auflösung mit leerer SKI bleibt ausschließlich für Reads mit exakt einem kompatiblen Gerät erlaubt. Writes verlangen immer eine valide SKI.
3. Ein generischer Stream-Loop übernimmt Subscribe/Unsubscribe, SKI-Filter, Context-Ende, Revision und Drop-/Resync-Verhalten; fachlich bleibt nur die Event-Konvertierung.
4. Alle Controller-Fehler werden über die Taxonomie aus SPEC3-02 abgebildet. LPC und OHPCF dürfen erwartbare Geräteablehnungen nicht pauschal als `INTERNAL` ausgeben.
5. Log-Level respektieren die konfigurierte Debug-Einstellung. Erfolgreiche Monitoring-Reads schreiben nicht bedingungslos Debug-Zeilen über den Standardlogger.
6. Nicht verwendete untypisierte Event-Payloads werden entfernt oder durch konkrete Payload-Typen ersetzt.

#### Abnahme

- Tabellengetriebene Tests prüfen dieselbe Request-/SKI-/Fehlermatrix für alle Services.
- Kein Service implementiert eine eigene Kopie des vollständigen Subscribe-Select-Loops.
- Fehlertexte enthalten keine Tokens, Zertifikatinhalte oder vollständigen SKIs.

### SPEC3-08 – Modulare Go-Composition und Use-Case-Bausteine

#### Ziel

Die Composition Root beschreibt Module; wiederkehrende technische Use-Case-Abläufe werden geteilt, ohne unterschiedliche Fachsemantik zu vereinheitlichen.

#### Anforderungen

1. Ein Modul bündelt Setup, EEBUS-Registrierung, gRPC-Registrierung, Lifecycle und Diagnosebezeichnung eines fachlichen Bereichs.
2. `Application` orchestriert Module und globale Ressourcen, kennt aber nicht jeden konkreten Wrapper als eigenes Feld.
3. Gemeinsam extrahiert werden nur nachweislich gleiche Abläufe:
   - Remote-Entity-Kompatibilitätsauflösung,
   - Feature-Request und Await/Result-Behandlung,
   - Setpoint-Lesen/Constraints/Write-Grundablauf,
   - System-Function-Lesen und Write-Antwort,
   - Measurement-Server-Erzeugung und skalierte Wertpublikation.
4. Entity-Typ, Scope, Feature und erlaubte Modi bleiben explizite domänenspezifische Parameter.
5. Die Provider MGCP, VAPD und VABD teilen einen serialisierten Measurement Publisher, behalten aber eigene IDs, Einheiten und Validierungsregeln.
6. Keine Reflection-basierte oder `any`-zentrierte Meta-Abstraktion wird eingeführt.

#### Abnahme

- Ein neuer ähnlich strukturierter Setpoint- oder Measurement-Use-Case benötigt keine Kopie eines vollständigen Lifecycle-/Write-Gerüsts.
- Vorhandene Use-Case- und Hardware-nahe Contract-Tests bleiben unverändert grün.
- `NewApplication` ist primär eine lesbare Liste von Modulen und globalen Policies.

### SPEC3-09 – Strikte Konfiguration und sichere Diagnose

#### Ziel

Fehlkonfigurationen scheitern beim Start mit einer konkreten, sicheren Meldung statt still auf Defaults oder alte Werte zurückzufallen.

#### Anforderungen

1. YAML wird mit `KnownFields(true)` dekodiert.
2. Gesetzte, aber ungültige Umgebungsvariablen liefern einen Fehler mit Variablenname; sie werden nicht ignoriert.
3. Beide Ports werden auf `1..65535` geprüft. Sicherheits-, Zertifikats- und Provider-Kombinationen besitzen zentrale Validierungsfunktionen.
4. `auto_generate`, explizite Zertifikatspfade und Storage Path haben widerspruchsfreie, getestete Regeln.
5. Experimentelle Startvertrauens-Konfiguration wird klar von Produktionsoptionen getrennt und in Release-Builds entweder sichtbar gewarnt oder entfernt.
6. Der HA Config Flow unterscheidet mindestens Netzwerkfehler, TLS-Vertrauen, Authentifizierung und inkompatiblen gRPC-Endpunkt.
7. Diagnosedaten enthalten Capability-/Recovery-/Stream-Zustand und Alter des letzten erfolgreichen Reads, aber keine Auth-Tokens, PEM-Inhalte oder vollständigen SKIs.

#### Abnahme

- Tests decken unbekannte YAML-Schlüssel, ungültige Env-Werte, Portgrenzen und widersprüchliche Zertifikatsoptionen ab.
- Reauth wird nur für `UNAUTHENTICATED` ausgelöst; TLS- und Netzwerkfehler führen zu passenden Flow-Fehlern.

### SPEC3-10 – Proto-Governance und deterministische Generierung

#### Ziel

Jede Vertragsänderung ist kompatibilitätsgeprüft und erzeugt reproduzierbar beide Stub-Sätze.

#### Anforderungen

1. CI führt `buf lint` aus.
2. CI führt eine Breaking-Change-Prüfung gegen den Vertrag auf dem Zielbranch aus.
3. Ein gemeinsamer Generate-Check regeneriert Go- und Python-Stubs in einer sauberen Arbeitskopie und prüft beide Verzeichnisse auf Drift.
4. Generator- und Plugin-Versionen sind gepinnt. Das Python-Postprocessing ist deterministisch und separat getestet.
5. `proto_stubs.py`-Exports werden automatisch gegen alle von Python genutzten Services und Nachrichten geprüft.
6. Additive Änderungen bleiben in `eebus.v1`. Das Entfernen der deprecated Heartbeat-RPCs und bestehender Streams erfolgt ausschließlich in einem neuen API-Package.
7. Für die Übergangsphase dokumentiert eine Contract-Matrix, ab welcher Bridge-Version Capability-RPC und konsolidierter Stream verfügbar sind.

#### Abnahme

- Eine absichtlich nicht regenerierte Go- oder Python-Datei lässt CI fehlschlagen.
- Feldlöschung, Feldnummern-Wiederverwendung und inkompatible Typänderung werden vor Merge erkannt.
- Alte Python-Clients funktionieren weiterhin gegen eine Bridge mit den additiven v1-Erweiterungen.

## 6. Querschnittliche Test- und Beobachtbarkeitsanforderungen

Jedes Arbeitspaket ergänzt Tests auf der niedrigsten sinnvollen Ebene. Zusätzlich sind folgende Szenarien als Integrationssuite erforderlich:

1. Poll-/Event-Race mit garantiert neuerem Event.
2. Stream-Überlauf, Revisionslücke, Reconnect und Resync.
3. Zwei Geräte an einer Bridge, einschließlich mehrdeutiger leerer SKI.
4. Disconnect/Reconnect mit neuen Remote-Entity-Referenzen.
5. Temporär fehlende und dauerhaft nicht unterstützte Capability.
6. Provider-Sensor unavailable, Sample-Ablauf und Teilfehler.
7. Shutdown mit offenen Streams und laufendem Provider-Push.
8. Authentifizierter und Loopback-Betrieb einschließlich Health und Reflection.

Mindestens folgende Betriebsdaten sollen als strukturierte Logs oder Diagnosedaten verfügbar sein:

- aktive Bridge-Runtimes, Channels und Device-Sessions,
- letzte erfolgreiche Poll-Zeit und Poll-Dauer je Gerät,
- Stream-Reconnects, letzte Revision, erkannte Lücken und Drops,
- Capability-Zustand mit Grund,
- Recovery-Stufe und Versuchszähler,
- Alter und Gültigkeit des letzten Provider-Samples.

Vollständige SKIs und geheime Inhalte dürfen dabei nicht ausgegeben werden.

## 7. Umsetzungsreihenfolge

### Phase A – Korrekte lokale Zustandsführung

1. Charakterisierungstests für Poll-/Event-Races ergänzen.
2. SPEC3-01 implementieren, zunächst auf dem bestehenden gRPC-Vertrag.
3. Alle HA-Entitäten auf typisierte Selektoren migrieren.

Ergebnis: Auch ohne Protokolländerung existiert nur noch eine Zustandswahrheit.

### Phase B – Expliziter und zuverlässiger Vertrag

1. SPEC3-02 Capability Registry und additiven RPC implementieren.
2. SPEC3-07 Fehlerabbildung vereinheitlichen.
3. SPEC3-03 konsolidierten Stream mit Revision/Resync einführen.
4. SPEC3-10 CI-Gates vor Nutzung der neuen Proto-Felder aktivieren.

Ergebnis: Support und Ereignisverlust sind eindeutig erkennbar.

### Phase C – Lifecycle und Betrieb

1. SPEC3-04 BridgeRuntime teilen.
2. SPEC3-05 gerätebezogene Recovery einführen.
3. SPEC3-06 Provider-Gültigkeit und atomare Samples umsetzen.
4. SPEC3-09 Konfiguration und Diagnose härten.

Ergebnis: Mehrgerätebetrieb und Provider-Daten sind belastbar.

### Phase D – Strukturelle Konsolidierung

1. Erst nach grünen Charakterisierungs- und Integrationssuiten SPEC3-08 umsetzen.
2. In kleinen Slices migrieren: gRPC-Helfer, Provider Publisher, Setpoint, System Function, Module.

Ergebnis: Weniger Wiederholung ohne gleichzeitige Verhaltensänderung.

## 8. Nicht-Ziele

- Keine neuen EEBUS-Funktionen, Kühlungs-, Zeitplan- oder Cloud-Features.
- Kein Austausch von gRPC, eebus-go oder Home Assistants Coordinator-Modell ohne nachgewiesene Notwendigkeit.
- Keine Rotation der bestehenden EEBUS-Zertifikatsidentität im Rahmen des Refactorings.
- Keine generische Framework-Schicht für hypothetische Use-Cases.
- Keine Entfernung bestehender v1-RPCs innerhalb einer kompatiblen Release-Linie.
- Keine kosmetische Umbenennung stabiler Entity-Unique-IDs.

## 9. Definition of Done der Gesamtinitiative

Die Initiative ist abgeschlossen, wenn:

1. HA pro Gerät genau einen autoritativen Zustand und einen fachlichen Event-Stream besitzt,
2. Polls keine neueren Events überschreiben können,
3. Capability, temporäre Nichtverfügbarkeit und Datenfrische explizit unterscheidbar sind,
4. Event-Verlust erkannt und automatisch resynchronisiert wird,
5. mehrere Geräte Transportressourcen teilen und getrennt recovern können,
6. Provider-Daten vollständig, zeitlich gültig und invalidierbar sind,
7. Go-Services dieselbe Validierungs- und Fehlersemantik verwenden,
8. Konfigurations- und Proto-Fehler vor dem Release durch CI erkannt werden,
9. Python-Lint, strikte Typprüfung und Tests sowie Go-Vet, Race-Tests und Integrationssuite grün sind,
10. bestehende HA-Unique-IDs und kompatible v1-Clients unverändert weiterarbeiten.
