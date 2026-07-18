# Refactoring- und Optimierungsspezifikation v4

- **Status:** Entwurf
- **Stand:** 2026-07-17
- **Geltungsbereich:** aktueller Softwarestand auf `main` (`v0.13.1`, Commit `2fdf831`)
- **Zielgruppe:** Maintainer der Home-Assistant-Integration und der Go-Bridge

## 1. Einordnung

Diese Spezifikation wurde ausschließlich aus dem aktuellen Quellcode, den
aktuellen Protobuf-Verträgen, Tests und CI-Definitionen abgeleitet. Frühere
Refactoring-Spezifikationen sind weder Eingabe noch fachliche Grundlage dieses
Dokuments. Die Bezeichnung "v4" versioniert dieses Dokument; sie fordert keine
Umbenennung des bestehenden Protobuf-Pakets `eebus.v1`.

Der Softwarestand ist funktional weit entwickelt und bereits gut abgesichert.
Das größte Potenzial liegt deshalb nicht in pauschaler Deduplizierung, sondern
in vier fachlich relevanten Grenzen:

1. Der konsolidierte Gerätestream ist noch nicht für alle internen Events
   payload-vollständig und löst deshalb unnötige Vollabgleiche aus.
2. Ein vollständiger HA-Abgleich benötigt bis zu 16 einzelne Unary-RPCs und
   erhält dadurch keinen logisch einheitlichen Bridge-Snapshot.
3. Bridge- und HA-Versionen handeln unterstützte Vertragsmerkmale nur indirekt
   über `UNIMPLEMENTED` oder fachfremde Probe-RPCs aus.
4. Konstruktion, Start, Recovery und globale Prozessgesundheit der Bridge sind
   noch enger gekoppelt als für einen robusten Multi-Device-Betrieb sinnvoll.

Diese Punkte haben Vorrang vor kosmetischen Umbauten großer Dateien.

## 2. Analysierter Ist-Zustand

### 2.1 Systemgrenzen

```text
Home Assistant                  Go-Bridge                     EEBUS-Gerät
custom_components/eebus  <->   internal/grpc  <->  usecases  <-> SHIP/SPINE
      Python/gRPC                     Go/gRPC                    eebus-go
```

- Die Python-Integration besitzt HA-Lifecycle, Entity-Darstellung,
  Provider-Sensorzuordnung und den pro Gerät reduzierten Zustand.
- Die Go-Bridge besitzt SHIP/SPINE, EEBUS-Use-Cases, Geräte-/Entity-Auflösung,
  Capability-Ermittlung, Recovery und den gRPC-Vertrag.
- `eebus-bridge/proto/eebus/v1` ist der gemeinsame Transportvertrag.
- Die Bridge kann mehrere Geräte verwalten; HA verwendet einen Config Entry pro
  Remote-SKI und teilt Transport-Runtimes für identische Bridge-Endpunkte.

Diese Trennung ist fachlich richtig und bleibt erhalten.

### 2.2 Größen- und Qualitätsbild

| Bereich | Produktionscode | Tests | Beobachtung |
|---|---:|---:|---|
| Python | ca. 6.060 LOC / 25 Dateien | ca. 4.365 LOC / 183 Tests | immutable State Store, Strict-Typing-Konfiguration, Ruff sauber |
| Go | ca. 10.635 LOC / 48 Dateien | ca. 7.224 LOC / 237 Tests | Race-Test in CI, klar getrennte Packages |
| Protobuf | ca. 640 LOC | Governance- und Drift-Gates | additive v1-Evolution abgesichert |

Die lokale Analyse ergab:

- `ruff check custom_components/`: erfolgreich.
- `go vet ./...`: erfolgreich.
- `go test ./...` und `go test -race ./...`: erfolgreich.
- `PYTHONPATH=. .venv/bin/python -m pytest` (206 Tests) und
  `.venv/bin/python -m mypy custom_components/eebus` (strict): erfolgreich über
  die Projekt-Virtualenv `.venv`; beide Läufe sind zusätzlich Bestandteil der
  aktuellen CI.
- Die CI prüft Buf-Lint, Breaking Changes, Stub-Drift, Proto-Governance,
  Go-Race-Tests, Integrationstests, Security-Audits und HA/HACS-Konformität.

Die hohe Testzahl ist kein Grund, Integrationsrisiken zu unterschätzen: Die
meisten Tests prüfen die beiden Sprachen getrennt. Ein ausführbarer
Cross-Language-Verhaltenstest fehlt.

### 2.3 Konkrete Hotspots

| Hotspot | Aktueller Befund | Wirkung |
|---|---|---|
| `device_state_stream.go` | Nicht jeder interne Eventtyp wird in einen vollständigen, typisierten Payload übersetzt. OHPCF wird im konsolidierten Stream ohne Flexibility-Payload versendet; mehrere Detailmessungen werden zu `UNSPECIFIED`. | HA markiert Werte temporär unavailable und fordert einen Vollabgleich an. Push wird faktisch wieder zu Polling. |
| `snapshot.py` | Ein Abgleich kombiniert Status, Registrierung, sechs Basisreads, sieben weitere Reads und Capabilities in mehreren Wellen. `GetMeasurements` überschneidet sich zudem mit dedizierten Power-/Energy-Reads. | Mehr Latenz, mehr Bridge-Last und zeitlich gemischte Zustände. |
| `providers.py` | Provider-Invalidation wird aus der Existenz von `GetDeviceCapabilities` abgeleitet; Stub und Methode werden dynamisch per `getattr` gewählt. | Versionskompatibilität ist implizit, Typprüfung endet an einer wichtigen I/O-Grenze. |
| `app.go` | `NewApplication` verdrahtet, registriert, richtet ein und startet Module teilweise bereits während der Konstruktion. Use-Cases müssen wegen der Reihenfolge lazy und mit Reflection-Nil-Prüfung aufgelöst werden; der LPC-Heartbeat startet vor `Application.Start`. | Fragile Ordnungsabhängigkeit, unklare Commit-/Rollback-Grenze und erschwerte Lifecycle-Tests. |
| `app.go` / gRPC Health | Ein einzelnes stale Gerät setzt die globale gRPC-Health auf `NOT_SERVING` und kann nach Recovery-Erschöpfung den Prozess beenden. | Ein Gerätefehler vergrößert im Multi-Device-Betrieb den Ausfallradius. |
| `registry.go` | Gerätekatalog, Entity-Index, Connection-/Monitoring-Health und Capabilities teilen einen Store und einen Mutex. | Wachsende Invariantenmenge; schwer isoliert test- und änderbar. |
| `state.py` | Dataclasses, `StateField`, Getter, Replacer, Capability-Zuordnung und Freshness-Regeln beschreiben dieselben Felder mehrfach; einige Pfade benötigen `Any` und `type: ignore`. | Neue Felder erfordern synchrone Änderungen an mehreren Tabellen. |
| `__init__.py` / `coordinator.py` / `config_flow.py` / `providers.py` | Zehn Provider-Mappings werden als einzelne Argumente durch mehrere Schichten gereicht. | Hohe Änderungsbreite und leicht übersehbare Zuordnungen. |
| Provider-Use-Cases in Go | MGCP, VAPD und VABD duplizieren Snapshot-Mutex, Version, Expiry-Timer, Clone-/Publish-/Invalidate-Lifecycle. | Fehlerbehebungen am Gültigkeitsmodell müssen dreifach erfolgen; Timer besitzen keinen expliziten Shutdown. |
| DHW-/Room-HVAC-Use-Cases | Subscription, Binding, Refresh, SystemFunction-/Mode-Auflösung und Write-Flow sind ähnlich implementiert. | Mittlere Wartungskosten; domänenspezifische Unterschiede dürfen bei einer Deduplizierung nicht verloren gehen. |

## 3. Zielprinzipien

Alle Arbeitspakete müssen die folgenden Regeln einhalten:

1. **Semantik vor Deduplizierung.** Ein Umbau muss Konsistenz, Fehlerisolation,
   Typisierung, Testbarkeit oder messbare Laufzeitkosten verbessern.
2. **Eine autoritative Zustandsquelle pro Prozess.** In Go entsteht der
   Bridge-Snapshot aus einem klaren Reader/Assembler; in Python bleibt
   `DeviceStateStore` die einzige veröffentlichte Zustandsreferenz.
3. **Explizite Kompatibilität.** Feature-Negotiation ersetzt Rückschlüsse aus
   fachfremden RPCs. Alte Bridge-/HA-Kombinationen erhalten einen definierten
   Fallback-Zeitraum.
4. **Teilresultate sind Daten, keine Transportfehler.** Ein nicht unterstützter
   oder vorübergehend nicht lesbarer Use-Case darf einen ansonsten verwertbaren
   Gerätesnapshot nicht verwerfen.
5. **Lifecycle ist transaktional.** Start hat einen Commit-Punkt und rollt
   vorher gestartete Ressourcen in umgekehrter Reihenfolge zurück. Stop ist
   idempotent und beendet auch Timer und Hintergrundtasks.
6. **Gerätefehler bleiben gerätebezogen.** Globale Health repräsentiert die
   Prozess- und Transportfähigkeit, nicht die Datenfrische eines einzelnen SKI.
7. **Additive Protobuf-Evolution.** Bestehende v1-Felder und RPCs werden nicht
   umnummeriert oder ohne veröffentlichte Migrationsphase entfernt.
8. **Keine spekulative Use-Case-Plattform.** Gemeinsame Abstraktionen werden nur
   dort eingeführt, wo mindestens zwei aktuelle Pfade dieselbe Invariante teilen.
9. **Entity-Stabilität.** Unique IDs, Entity-Typen, Standardaktivierung und
   öffentliche HA-Semantik ändern sich nur mit eigener fachlicher Begründung.
10. **Security bleibt invariant.** Loopback-vs.-TLS/Token-Regeln, Redaction und
    Auth-Interceptors gelten auch für neue Status-, Snapshot- und Diagnose-RPCs.

## 4. Priorisierte Roadmap

Die Priorität folgt dem erwarteten fachlichen Schaden, nicht der Dateigröße.
Bewertet wurden in dieser Reihenfolge:

1. Risiko falscher Zustände, verlorener Events oder inkompatibler Versionen,
2. Ausfallradius für Benutzer und mehrere Geräte,
3. Häufigkeit und Laufzeitkosten des betroffenen Pfads,
4. Änderungsbreite bei neuen Messwerten oder Use-Cases,
5. reine Lesbarkeit/Deduplizierung.

Der Aufwand beeinflusst die Slice-Größe und Reihenfolge innerhalb einer
Prioritätsklasse, stuft ein semantisch kritisches Thema aber nicht herab.

| ID | Thema | Priorität | Nutzen | Aufwand | Hauptrisiko |
|---|---|---:|---:|---:|---|
| SPEC4-01 | Payload-vollständiger Device-State-Stream | P0 | sehr hoch | M | additive Eventmodell-Erweiterung |
| SPEC4-02 | Explizite API-/Feature-Negotiation | P0 | sehr hoch | M | Versionsmatrix |
| SPEC4-03 | Aggregierter Gerätesnapshot | P0 | sehr hoch | XL | partielle Statussemantik |
| SPEC4-04 | Transaktionaler Application-Lifecycle | P0 | sehr hoch | L | Start-/Shutdown-Reihenfolge |
| SPEC4-05 | Gerätebezogene Recovery und Readiness | P1 | hoch | L | Verhalten bei einem vs. mehreren Geräten |
| SPEC4-06 | Typisierter Messwertkatalog | P1 | hoch | L | Migration freier String-Typen |
| SPEC4-07 | Cross-Language-Vertrags- und Qualitätsgates | P1 | hoch | M | deterministische Testumgebung |
| SPEC4-08 | Getypte Python-Session- und Zustandsgrenzen | P2 | mittel | L | unnötige State-Abstraktion |
| SPEC4-09 | Einheitlicher Provider-Pipeline-Lifecycle | P2 | mittel | M | Nebenläufigkeit/Expiry |
| SPEC4-10 | Registry-Projektionen und operative Diagnostik | P2 | mittel | L | schrittweise Migration vieler Aufrufer |
| SPEC4-11 | Gemeinsamer HVAC-Kern und Fork-Abbau | P3 | mittel | M | versteckte Gerätebesonderheiten |

`P0` verhindert falschen oder unnötig teuren Laufzeitbetrieb. `P1` reduziert
Ausfallradius und Vertragsdrift. `P2` senkt Änderungs- und Diagnosekosten. `P3`
ist sinnvoll, aber nicht vor den Vertrags- und Lifecycle-Themen.

## 5. Arbeitspakete

### SPEC4-01: Payload-vollständiger Device-State-Stream

**Problem**

`SubscribeDeviceState` soll die Legacy-Domainstreams ersetzen, führt aber für
mehrere reale Events weiterhin zum Vollabgleich:

- OHPCF-State-/Data-Events erhalten im konsolidierten Stream keinen
  `CompressorFlexibility`-Payload.
- Power-per-phase, Current-per-phase, Voltage-per-phase, Frequency und
  Energy-produced besitzen im aktuellen `MeasurementEventType` keinen
  vollständigen konsolidierten Mappingpfad.
- Ein Data-Update ohne Payload bedeutet für Python derzeit "temporarily
  unavailable" plus Refresh, obwohl häufig nur der Adapter unvollständig ist.
- Der Default-Zweig wandelt nicht eigens behandelte interne Events pauschal in
  `DEVICE_EVENT_PROVIDER_UPDATED` um. Damit ist Exhaustiveness nicht prüfbar.

**Ziel**

Jedes fachliche Update im konsolidierten Stream enthält entweder den aktuellen
Wert, eine explizite Invalidierung dieses Werts oder eine explizite
`resync_required`-Nachricht. Ein fehlender Adapter-Payload darf nicht implizit
als Geräte-Unavailability interpretiert werden.

**Anforderungen**

1. Es gibt eine exhaustive Zuordnung aller produktiven `eebus.EventType`-Werte
   zu genau einer der Kategorien `state delta`, `capability delta`, `provider
   acknowledgement`, `resync` oder `ignored`.
2. OHPCF wird als PayloadSource in den Device-Service eingebunden und verwendet
   denselben Converter wie der Legacy-OHPCF-Stream.
3. Detailmessungen erhalten additive, typisierte Eventwerte oder einen
   generischen, typisierten Measurement-Entry-Eventpfad.
4. Eventadapter lesen den EEBUS-Cache höchstens einmal pro Eventdomäne. Ein
   fehlgeschlagener Cache-Read erzeugt einen expliziten Availability-Status.
5. Python fordert bei normalen, vollständigen Deltas keinen Poll an.
6. Polling wird nur noch ausgelöst bei Initial-Sync alter Bridges, explizitem
   Resync, Revision-Gap oder einem dokumentierten Legacy-Fallback.
7. Legacy-Streams bleiben zunächst unverändert verfügbar.

**Akzeptanzkriterien**

- Ein tabellengetriebener Go-Test schlägt fehl, sobald ein neuer interner
  Eventtyp keine explizite Streamklassifikation besitzt.
- Für jeden aktuellen Measurement-, LPC-, DHW-, HVAC-, OHPCF-, Capability- und
  Device-Eventtyp existiert ein Payload-Vertragstest.
- Python-Tests beweisen, dass vollständige Events den Store genau einmal
  aktualisieren und keinen Refresh planen.
- Ein absichtlich payloadloser Update-Event wird nicht als erfolgreicher Wert
  behandelt und nicht stillschweigend einer fachfremden Eventart zugeordnet.

### SPEC4-02: Explizite API-/Feature-Negotiation

**Problem**

HA und Bridge werden getrennt ausgeliefert. Der Client erkennt Features aktuell
über `UNIMPLEMENTED` oder, bei Provider-Invalidation, über die erfolgreiche
Existenz eines anderen RPCs. Das ist bei additiver Protobuf-Evolution nicht
ausreichend: Ein Server kann einen bekannten RPC anbieten und ein später
hinzugefügtes Feld trotzdem ignorieren.

**Ziel**

Die Bridge veröffentlicht einen stabilen, nicht geheimen Serververtrag. Der
Client entscheidet ausschließlich anhand dieses Vertrags, welchen optimalen
Pfad und welchen Fallback er verwendet.

**Anforderungen**

1. `GetStatus` wird additiv erweitert oder durch `GetServerInfo` ergänzt mit:
   - stabiler API-Major-/Minor-Revision,
   - Bridge-Buildversion,
   - wiederholter Liste expliziter Feature-IDs.
2. Mindestens folgende Features sind einzeln erkennbar:
   `EXPLICIT_CAPABILITIES`, `CONSOLIDATED_DEVICE_STREAM`,
   `DEVICE_SNAPSHOT`, `PROVIDER_SAMPLE_INVALIDATION` und
   `TYPED_MEASUREMENTS`.
3. Feature-IDs sind append-only. Unbekannte IDs werden vom Client ignoriert.
4. Der HA-Config-Flow lehnt nur eine wirklich inkompatible Major-Version mit
   einem übersetzten Fehler ab. Fehlende optionale Features aktivieren einen
   dokumentierten Fallback.
5. Die ausgehandelte Information wird pro `BridgeRuntime` gecacht und nicht pro
   SKI wiederholt gelesen.
6. Secrets, vollständige Remote-SKIs und Dateipfade sind nicht Bestandteil der
   ServerInfo.

**Akzeptanzkriterien**

- Matrix-Tests decken mindestens `alter Client/neue Bridge`, `neuer Client/alte
  Bridge`, `gleiche Version` und `inkompatible Major-Version` ab.
- Provider-Invalidation verwendet keine Capability-RPC-Existenz mehr als Proxy.
- Stream- und Snapshot-Fallbacks werden aus demselben gecachten Vertrag gewählt.
- Das Config-Flow-Probing besitzt weiterhin ein hartes Timeout und schließt den
  Kanal in jedem Pfad.

### SPEC4-03: Aggregierter Gerätesnapshot

**Problem**

`async_build_snapshot` erzeugt zwar eine atomare Veröffentlichung in Python,
aber keinen atomaren Bridge-Read. Ein Erstabgleich kann inklusive Registrierung
bis zu 16 Unary-Aufrufe benötigen. Daten aus mehreren RPC-Wellen können
verschiedene Cache-Zeitpunkte repräsentieren. Teilfehler werden über eine
Mischung aus gRPC-Status, Capability-Inferenz und fehlenden Messages abgebildet.

**Ziel**

Ein einziger gerätebezogener Bridge-Snapshot liefert alle für HA relevanten
Werte, deren Availability/Freshness, Capabilities und eine Eventrevision. Der
Snapshot ist zugleich der Initialzustand des konsolidierten Streams.

**Vertragsanforderungen**

1. Ein additiver `GetDeviceSnapshot(DeviceRequest)`-RPC liefert mindestens:
   - kanonischen Remote-SKI und Erfassungszeit,
   - aktuelle Eventrevision,
   - Connection-/Transition-Status,
   - Geräteklassifikation,
   - Capabilities inklusive Reason/LastChanged,
   - bekannte Messwerte,
   - LPC/Failsafe/Heartbeat,
   - DHW, HVAC und OHPCF.
2. Optionale Werte verwenden Presence. `0`, `false` und ein fehlender Wert sind
   unterscheidbar.
3. Jeder Teilbereich oder jedes State-Feld hat einen expliziten Status:
   `available`, `temporarily_unavailable`, `unsupported` oder `unknown`.
4. Ein Teilfehler bleibt im Snapshot. Der RPC selbst schlägt nur für
   Transport/Auth, ungültige Anfrage, unbekannten expliziten SKI oder einen
   nicht initialisierten Gesamtservice fehl.
5. Snapshot-Aufbau registriert oder vertraut kein Gerät. Registrierung bleibt
   ein explizites Kommando.
6. Die Bridge verwendet eine zentrale Snapshot-Assembler-Komponente; gRPC-
   Serviceadapter rufen nicht gegenseitig ihre öffentlichen RPC-Methoden auf.
7. `SubscribeDeviceState` erhält additiv einen Initial-Snapshot-Payload.

**Revisions- und Race-Regeln**

1. Die Bridge abonniert zuerst atomar den EventBus und erfasst Revision `R`.
2. Danach wird der Initial-Snapshot gebaut und mit Basisrevision `R` gesendet.
3. Währenddessen gepufferte Events `> R` werden anschließend ausgeliefert.
4. Doppelte Werte sind zulässig und idempotent; verlorene Zustandsänderungen
   sind nicht zulässig.
5. Nach Event-Drops folgt genau ein koaleszierter Snapshot-Abgleich.

**Python-Zielbild**

- `DevicePoller` liest bevorzugt genau einen Snapshot-RPC.
- Der bisherige Multi-RPC-Builder bleibt nur als Legacy-Adapter bestehen.
- Der Legacy-Adapter führt voneinander unabhängige Reads in einem einzigen
  `asyncio.gather` aus statt in mehreren sequentiellen Wellen; die heutige
  Drei-Wellen-Struktur hat keine Datenabhängigkeit und verdreifacht nur die
  Poll-Latenz gegen alte Bridges.
- Jeder Aufruf im Legacy-Adapter, einschließlich `GetStatus`, besitzt ein
  explizites hartes Timeout und respektiert Cancellation.
- Poll- und Streamdaten werden in denselben typisierten Patch konvertiert.
- Der 5-Minuten-Abgleich bleibt als günstige Konsistenzprüfung, besteht aber aus
  einem RPC pro Gerät.

**Akzeptanzkriterien**

- Neuer Server/neuer Client: Erstabgleich benötigt höchstens
  `RegisterRemoteSKI` plus einen Snapshot-/Streamaufruf.
- Periodischer Abgleich und Gap-Recovery benötigen jeweils genau einen
  gerätebezogenen Read-RPC.
- Ein nicht unterstützter OHPCF-Use-Case verhindert weder Monitoring- noch
  Connection-Daten im selben Snapshot.
- Ein Event zwischen Snapshot-Abonnement und Snapshot-Send geht im
  Nebenläufigkeitstest nicht verloren.
- Eine nicht antwortende Legacy-Bridge kann den HA-Erstabgleich nicht über das
  konfigurierte RPC-Timeout hinaus blockieren.
- Alte Bridges funktionieren über den bisherigen Multi-RPC-/Legacy-Stream-Pfad
  weiter.

### SPEC4-04: Transaktionaler Application-Lifecycle

**Problem**

`NewApplication` besitzt neben Verdrahtung bereits Setup-, Registrierungs- und
Startwirkungen. Insbesondere kann der LPC-Heartbeat laufen, bevor
`Application.Start` beginnt. Die aktuelle lazy Use-Case-Auflösung mit
Reflection-Nil-Prüfung kompensiert dabei eine Ordnungsabhängigkeit zwischen
Moduldefinition und Setup, beseitigt sie aber nicht. Provider-Expiry-Timer
besitzen keinen expliziten Stop-Vertrag. Der gRPC-Healthserver startet initial
als `SERVING`, noch bevor die EEBUS-Bridge erfolgreich gestartet wurde.

**Ziel**

Konstruktion bereitet Abhängigkeiten vor; `Start` besitzt sämtliche
langlaufenden Wirkungen; `Stop` beendet jede gestartete Ressource exakt einmal.

**Anforderungen**

1. Nach Rückkehr aus `NewApplication` laufen keine Listener, Heartbeats,
   Watchdogs, Provider-Timer oder sonstigen Goroutines.
2. Modul-Setup/Use-Case-Registrierung ist von Modul-Start getrennt. Falls Setup
   zwingend externe Ressourcen aktiviert, wird es Teil der Starttransaktion.
3. `Start` führt Komponenten in dokumentierter Reihenfolge hoch und protokolliert
   erfolgreiche Stufen intern für Rollback.
4. Ein Fehler rollt nur bereits gestartete Komponenten in exakt umgekehrter
   Reihenfolge zurück.
5. Der globale gRPC-Healthstatus ist bis zum vollständigen Start
   `NOT_SERVING`, danach `SERVING`, und wird vor Shutdown wieder
   `NOT_SERVING`.
6. Jeder Provider implementiert einen idempotenten Stop/Close-Pfad, der
   Expiry-Timer beendet.
7. `Stop` wartet begrenzt auf Goroutines und blockiert nicht unbegrenzt auf
   Hintergrundfehlerkanälen.
8. Konstruktor und Lifecycle werden über injizierbare, schmale Interfaces
   getestet; Produktionsverdrahtung bleibt an einer Composition Root.

**Akzeptanzkriterien**

- Tests prüfen Fehler nach jeder einzelnen Startstufe und die jeweilige
  Rollback-Reihenfolge.
- Ein Boot-Test instanziiert die **produktive Composition Root**
  (`NewApplication` mit realer Modulverdrahtung, minimaler Config und
  Scratch-Zertifikaten) und prüft, dass alle erwarteten Use-Cases registriert
  werden — nicht nur injizierte Fakes. Hintergrund: Der v0.13.0-Startup-Crash
  (typed-nil Use-Case durch eager Capture) passierte bei vollständig grünen
  Unit-, Race- und Lint-Gates, weil kein Test den Kompositionspfad bootet.
- Ein fehlgeschlagener gRPC-Start beendet Heartbeat und EEBUS sauber.
- Ein fehlgeschlagener EEBUS-Start veröffentlicht nie `SERVING`.
- Nach `Stop` können keine Provider-Expiry-Callbacks mehr auf EEBUS-Features
  schreiben.
- Doppelte und konkurrierende Stop-Aufrufe bleiben race-frei und idempotent.

### SPEC4-05: Gerätebezogene Recovery und Readiness

**Problem**

Der aktuelle Watchdog erkennt stale Monitoring-Bindings pro SKI, koppelt das
Ergebnis aber an globale gRPC-Health und nach drei Fehlversuchen an einen
Prozessneustart. Damit kann ein einzelnes Problemgerät gesunde Geräte und deren
HA-Sessions beeinträchtigen. Recovery-State liegt außerdem in `Application`,
während Connection-/Monitoring-State in `DeviceRegistry` liegt.

**Ziel**

Recovery ist ein eigener, gerätebezogener Zustandsautomat. Prozess-Liveness,
Bridge-Readiness und Device-Readiness sind getrennte Signale.

**Anforderungen**

1. Eine `RecoverySupervisor`-Komponente besitzt Zustände, Attempts, Backoff und
   Übergangszeiten pro kanonischem SKI.
2. Keine Recovery-Mutex bleibt während `ClearEntities`, Unregister/Register,
   gRPC-Health-Updates oder Logging externer Komponenten gehalten.
3. `DeviceStatus` wird additiv um Readiness/Recovery-Diagnose erweitert oder
   erhält einen separaten gerätebezogenen Status-RPC.
4. Globale gRPC-Health meldet nur, ob der Prozess Anfragen sicher annehmen kann.
5. Ein einzelnes stale Gerät bei mehreren aktiven Geräten beendet den Prozess
   nicht und setzt gesunde Geräte nicht unavailable.
6. Ein Prozessneustart ist erst zulässig, wenn eine globale Bridge-Invariante
   verletzt ist oder alle aktuell relevanten Geräte ihre zielgerichtete
   Recovery ausgeschöpft haben. Beim einzigen Gerät darf die bestehende
   Restart-Eskalation erhalten bleiben.
7. Disconnected, untrusted, in Grace Period, recovering und healthy sind
   unterscheidbare Zustände.
8. Attempts verwenden begrenzten Backoff; es laufen nie zwei Recoveries für
   denselben SKI parallel.

**Akzeptanzkriterien**

- Zwei-Geräte-Test: Gerät A bleibt lesbar, während Gerät B Recovery und
  Exhaustion durchläuft.
- Ein einzelnes gesundes Monitoring-Read nach Recovery beendet den Recovery-
  Zustand deterministisch.
- Globale Liveness bleibt bei einem isolierten Gerätefehler `SERVING`.
- Alle Statusdaten werden ohne vollständige SKIs oder Secrets in Logs und HA-
  Diagnostik dargestellt.

### SPEC4-06: Typisierter Messwertkatalog

**Problem**

`MeasurementEntry.type` und `.unit` sind freie Strings. Go erzeugt Strings wie
`power_l1`; Python pflegt eine zweite Zuordnung zu `StateField`. Scoped Energy
für Heating/DHW wird in Python per Substring-Heuristik erkannt. Das ist
erweiterbar, aber nicht vertragsfest.

**Ziel**

Alle vom Produkt unterstützten Messwerte besitzen stabile IDs und festgelegte
Einheiten. Freie Typen bleiben ausschließlich als Extension-Pfad für unbekannte
oder experimentelle Messwerte bestehen.

**Anforderungen**

1. Protobuf erhält additiv einen append-only `MeasurementId`-Enum oder eine
   äquivalente stabile ID mit Presence.
2. Der Katalog umfasst mindestens Gesamtleistung/-energie, drei Phasen für
   Power/Current/Voltage, Frequency, produced energy, DHW/Room/Outdoor/Flow/
   Return/Compressor temperature, Compressor power sowie Heating-/DHW-Energy.
3. Für bekannte IDs ist die kanonische Einheit fest definiert. Abweichende
   Bridge-Einheiten werden am Go-Adapter normalisiert oder als Fehler markiert.
4. Go besitzt genau eine Zuordnung von EEBUS Measurement-/Scope-Metadaten zu
   stabiler ID und Einheit.
5. Python besitzt genau eine Zuordnung von stabiler ID zu `StateField`; bekannte
   Werte werden nicht mehr anhand freier Strings oder Substrings erkannt.
6. Das bisherige Stringfeld bleibt für die Kompatibilitätsphase erhalten. Neue
   Clients bevorzugen die ID, alte Clients erhalten weiterhin den String.
7. Unbekannte IDs und Extension-Strings werden sicher ignoriert und in Debug-
   Diagnostik gezählt, nicht als falscher Sensorwert übernommen.

**Akzeptanzkriterien**

- Tabellengetriebene Tests in Go und Python verwenden denselben Satz kanonischer
  IDs und Einheiten.
- Heating- und DHW-Energy benötigen keine Namensheuristik mehr.
- `0 W`, `0 kWh` und `0 °C` bleiben vorhandene, gültige Werte.
- Buf-Breaking- und Cardinality-Gates bleiben grün.

### SPEC4-07: Cross-Language-Vertrags- und Qualitätsgates

**Problem**

Go- und Python-Seite besitzen umfangreiche Unit-Tests, aber kein CI-Szenario,
das einen realen Python-gRPC-Client gegen einen gestarteten Go-Testserver
ausführt. Stub-Kompatibilität ist dadurch geprüft, semantische Adapter-
Kompatibilität jedoch nur indirekt. Coverage wird berichtet, aber nicht gegen
Regressionen geratcheted.

**Ziel**

Die wichtigsten Bridge-HA-Verhaltensverträge laufen sprachübergreifend und
ohne reale Hardware deterministisch in CI.

**Anforderungen**

1. Ein kleiner Go-Testserver verwendet Fake-Use-Cases/StateBackend, aber den
   produktiven gRPC-Server, Security-Interceptor und Eventadapter.
2. Ein Python-Test verwendet die committed/generated Stubs und den produktiven
   `GrpcChannelManager`, Snapshot-/Stream-Converter und `DeviceStateStore`.
3. Pflichtszenarien sind:
   - ServerInfo/Feature-Negotiation,
   - vollständiger Initial-Snapshot,
   - je ein Delta aller Domänen,
   - Revision-Gap/Event-Drop mit genau einem Resync,
   - Unsupported vs. TemporarilyUnavailable,
   - zwei isolierte SKIs,
   - Loopback sowie TLS/Token mit validem und ungültigem Token,
   - neuer Client gegen Legacy-Featureprofil.
4. Der Test benötigt kein mDNS, SHIP, echte Zertifikatsidentität eines Geräts
   oder Netz außerhalb des CI-Runners.
5. Python- und Go-Coverage erhalten Ratchets: Die Baseline wird aus dem ersten
   stabilen Lauf festgelegt; spätere PRs dürfen sie nicht unbemerkt senken.
6. Generated Code bleibt aus der Coverage-Berechnung ausgeschlossen.
7. Die Proto-Governance-Gates selbst werden gehärtet: `check_proto_governance.py`
   und `check_proto_cardinality.py` parsen Protos nicht mehr per Regex
   (Zeilenheuristiken, 220-Zeichen-Fenster für `deprecated`, `MESSAGE_RE` bricht
   an der ersten Spalte-0-Klammer), sondern über von `buf` erzeugte Descriptor-
   Sets der bereits gepinnten Toolchain. Die Inventarliste zu schützender RPCs
   (`REQUIRED_V1_RPCS`) wird aus dem Baseline-Descriptor des Vergleichs-Refs
   abgeleitet statt von Hand gepflegt; ein nach diesem Commit hinzugefügter
   Stream-RPC ist damit automatisch geschützt.

**Akzeptanzkriterien**

- Ein absichtlich entferntes Eventmapping lässt den Cross-Language-Test
  fehlschlagen.
- Ein testweise in eine nested Message verschobenes Feld sowie ein entfernter
  Stream-RPC werden von den gehärteten Governance-Gates erkannt.
- Ein falscher Capability-/Availability-Status lässt den Test auf der
  veröffentlichten Python-State-Semantik fehlschlagen.
- Der Test läuft in CI reproduzierbar ohne Sleeps als Synchronisationsvertrag;
  Readiness wird über Ports/Events/Contexts signalisiert.

### SPEC4-08: Getypte Python-Session- und Zustandsgrenzen

**Problem**

Der Coordinator ist bereits überwiegend Fassade, enthält aber noch
Kompatibilitätsaliases und Handler-Proxys für Tests. Diagnostics greifen über
`getattr` auf private Felder zu. Der State-Reducer beschreibt Felder über
heterogene Enum-/Reflection-Tabellen und benötigt deshalb `Any` und mehrere
`type: ignore`. Connection- und Provider-Konfiguration werden als lange
Parameterlisten transportiert.

**Ziel**

Öffentliche, getypte Session- und Diagnoseobjekte ersetzen Test-Seams und
private Reflection. State-Updates sind domänenweise typisiert.

**Anforderungen**

1. Es gibt immutable Konfigurationsobjekte mindestens für
   `BridgeConnectionConfig` und `ProviderMappings`.
2. Setup, Reload und Runtime-Key-Bildung verwenden diese Objekte statt paralleler
   Einzelargumentlisten.
3. `RuntimeDeviceSession` veröffentlicht schmale, getypte Ports für Poll,
   Streams, Writes, State und Diagnostics.
4. `EebusCoordinator` besitzt keine privaten Aliasfelder nur für Tests und keine
   Eventhandler-Proxys. Nur für Tests existierende oder komplett tote öffentliche
   API wird entfernt (aktuell z. B. `BridgeRuntimeRegistry.retain()` ohne
   einen einzigen Produktionsaufrufer).
5. Ein Poll-Ergebnis benachrichtigt Entities genau einmal: Der Store-Dispatch
   im Poll-Pfad und das `DataUpdateCoordinator`-eigene Update nach
   `_async_update_data` dürfen nicht beide `async_set_updated_data`-äquivalente
   Listener-Läufe für denselben identischen Zustand auslösen.
6. Diagnostics verwenden eine öffentliche, immutable `SessionDiagnostics`-
   Projektion; kein Zugriff auf `_device_streams` oder optionale Attribute per
   `getattr`.
7. State-Updates werden als getypte Domain-Patches modelliert. Eine Abwesenheits-
   Sentinel unterscheidet "nicht beobachtet" von "explizit None".
8. Der Reducer behält Feldrevisionen, Freshness und den Schutz vor verspäteten
   Poll-Ergebnissen bei.
9. Reflection/`Any` an der Reducer-I/O-Grenze wird beseitigt oder auf einen
   einzigen, vollständig getesteten Adapter begrenzt.

**Akzeptanzkriterien**

- Bestehende Entity-Unique-IDs und Entity-Verfügbarkeit bleiben unverändert.
- Poll und Stream erzeugen denselben Patchtyp und dieselben Reducer-Ergebnisse.
- Die Tests importieren produktive Ports statt private Coordinator-Interna.
- `mypy --strict` benötigt im handgeschriebenen State-/Sessioncode keine neuen
  `type: ignore`-Ausnahmen.
- Runtime-Reconfigure bleibt atomar und cancellation-sicher.

### SPEC4-09: Einheitlicher Provider-Pipeline-Lifecycle

**Problem**

Python dupliziert für Grid, PV und Battery Sensorlesen, Requestbau,
Invalidierung und Startmethoden. Die dynamische Stub-/Methodenauswahl schwächt
Typing. Go dupliziert Snapshot-State, Versionierung, Expiry-Timer und
Invalidierung. Provider sind experimentell, ihr Gültigkeitsmodell schützt aber
reale Verbraucher vor stale Daten und ist deshalb sicherheitsrelevant.

**Ziel**

Gemeinsame Lifecycle-Invarianten sind einmal implementiert; domänenspezifische
Messwerte und EEBUS-Features bleiben explizit.

**Anforderungen Python**

1. Grid, PV und Battery implementieren einen getypten `ProviderPublisher`-Port
   mit `start`, `signal`, `invalidate` und `stop`.
2. Stub und RPC-Methode werden statisch typisiert aufgerufen; kein
   `getattr(proto_stubs, ...)` an der Netzwerkgrenze.
3. Ein gemeinsamer Worker serialisiert/coalesciert Pushes. Pro Provider existiert
   höchstens ein In-flight-Push und ein nachlaufender Dirty-Push.
4. `ProviderMappings` ist die einzige Konfigurationseingabe für Manager, Reload
   und Options Flow.
5. Ungültige Pflichtsensoren senden genau eine koaleszierte Invalidierung, bis
   wieder ein gültiger Sample vorliegt.

**Anforderungen Go**

1. Ein getesteter generischer oder komponierter Snapshot-Store besitzt Mutex,
   Version, Clone, Current, Expiry und Close.
2. Externe EEBUS-Schreiboperationen werden nicht unter einem reinen State-Mutex
   ausgeführt. Eine separate Publish-Serialisierung erhält Atomizität.
3. Expiry ist versionsgebunden; ein alter Timer kann keinen neueren Sample
   invalidieren.
4. `Close` stoppt Timer und verhindert spätere Writes.
5. MGCP/VAPD/VABD behalten eigene Feature-/Scenario-Definitionen und
   Messwertlisten; diese werden nicht in eine schwer lesbare Meta-DSL verlagert.

**Akzeptanzkriterien**

- Gemeinsame Contract-Tests laufen gegen alle drei Provider-Implementierungen.
- Race-Tests decken Publish vs. Expire vs. Close ab.
- Provider-Reconfigure verliert keinen nach Commit gültigen Sample und sendet
  nach Shutdown keine RPCs.
- Das Hinzufügen eines Mappings erfordert keine Änderung an Setup-, Reload- und
  Coordinator-Signaturen.

### SPEC4-10: Registry-Projektionen und operative Diagnostik

**Problem**

`DeviceRegistry` verwaltet vier Verantwortungen unter einem Mutex und gibt an
einigen Stellen Structs mit Slices zurück. Der EventBus zählt Drops und die
Recovery besitzt wertvolle Zustände, aber die Bridge stellt diese Informationen
dem HA-Diagnosepfad nicht strukturiert bereit.

**Ziel**

Writes bleiben intern und synchronisiert; Leser erhalten immutable Projektionen.
Katalog, Health, Capabilities und Eventtransport können unabhängig getestet und
beobachtet werden.

**Anforderungen**

1. Die Registry wird intern mindestens in folgende Verantwortungen zerlegt:
   - Device-/Entity-Katalog,
   - Connection-/Monitoring-Health,
   - Capability-State.
2. Eine vorübergehende Fassade darf bestehende Use-Case-Aufrufer schrittweise
   migrieren; es erfolgt kein Big-Bang-Umbau.
3. Lese-APIs liefern tiefe, immutable Snapshots ihrer Slice-/Map-Daten.
4. Raw `EntityRemoteInterface` verlässt den Use-Case-/Resolution-Bereich nicht
   in gRPC-Diagnosemodelle.
5. Locks werden nie über EEBUS-, gRPC-, Logger- oder EventBus-Aufrufe gehalten.
6. Ein authentisierter Diagnose-/Statuspfad liefert mindestens:
   - Eventrevision und Drop-/Resync-Zähler,
   - Connection-/Monitoring-Alter,
   - Recovery-State/Attempts,
   - Snapshot-Read-Dauer und letzten Erfolg,
   - Stream-Reconnects,
   - Provider-Sample-Status/Expiry,
   - ausgehandelte API-Features.
7. Diagnosewerte verwenden gekürzte/geredactete SKIs und enthalten keine
   Tokens, PEM-Inhalte oder privaten Pfade.
8. HA konsumiert diese Daten über eine getypte Diagnoseprojektion, nicht über
   private Coordinator-Attribute.

**Akzeptanzkriterien**

- `go test -race` enthält parallele Read-/Upsert-/Disconnect-/Capability-Tests.
- Ein mutierter Rückgabeslice kann den internen Registry-Zustand nicht ändern.
- Ein verlorener Stream-Event ist in Diagnostics als Drop plus Resync sichtbar.
- Zwei Geräte zeigen getrennte Health-/Recovery-Daten.

### SPEC4-11: Gemeinsamer HVAC-Kern und Fork-Abbau

**Problem**

DHW- und Room-Heating-SystemFunction teilen HVAC-Subscription, Binding,
Metadatenrefresh, Mode-Auflösung und Write-Flow. Gleichzeitig verwendet die
Bridge einen gepinnten `eebus-go`-Fork für noch nicht upstream verfügbare
Temperature-Monitoring-Use-Cases. Beides ist beherrscht, verursacht aber
laufende Wartungskosten.

**Ziel**

Ein kleiner gemeinsamer HVAC-Kern kapselt nur nachweislich identische
Mechanik. Der Fork bleibt reproduzierbar und wird automatisch entfernbar,
sobald alle benötigten Upstream-Patches verfügbar sind.

**Anforderungen**

1. Gemeinsame HVAC-Helfer kapseln:
   - HVAC client feature lookup,
   - subscribe/bind,
   - standardisierten Metadata-/Value-Refresh,
   - SystemFunction-/OperationMode-Auflösung,
   - contextgebundenen Write-/Refresh-Flow.
2. DHW-spezifischer Boost/Overrun und die Auswahl des DHW-SystemFunction-Typs
   bleiben in DHW-Code.
3. Heating-spezifische Setpoint-/SystemFunction-Auswahl bleibt in Heating-Code.
4. Fehler-Sentinels und gRPC-Fehlerklassifikation bleiben domänenspezifisch und
   testbar.
5. CI prüft, ob die in `UPSTREAM_PATCHES.md` inventarisierten Änderungen in der
   gewählten Upstream-Version enthalten sind, und meldet einen entfernbaren
   `replace`-Block sichtbar.
6. Ein Fork-Wechsel muss weiterhin durch die vollständigen Go-Race-, Use-Case-
   und Cross-Language-Tests laufen.

**Akzeptanzkriterien**

- DHW- und Room-Heating-Verhalten bleibt anhand vorhandener Gerätefixtures
  identisch.
- Die gemeinsame Schicht enthält keine Vaillant-Modellabfragen.
- Nach Verfügbarkeit aller Upstream-Patches lässt sich `replace
  github.com/enbility/eebus-go` ohne manuelle Patchsuche entfernen.

## 6. Abhängigkeiten und Umsetzungsreihenfolge

```text
SPEC4-07 (Testharness, erster Slice)
        |
        +--> SPEC4-01 --> SPEC4-03 --> SPEC4-08
        |        ^            ^
        |        |            |
        +--> SPEC4-02 --------+
        |                     |
        +--> SPEC4-06 --------+

SPEC4-04 --> SPEC4-05 --> SPEC4-10
     |
     +--> SPEC4-09

SPEC4-11 ist nach SPEC4-04 unabhängig umsetzbar.
```

Empfohlene Lieferwellen:

### Welle A: Vertrag absichern

1. Minimalen Cross-Language-Testserver und Client-Harness aus SPEC4-07 bauen.
2. Kern-Slice aus SPEC4-04 vorziehen: keine Seiteneffekte mehr in
   `NewApplication` (kein Heartbeat-/Listener-Start vor `Start`), gRPC-Health
   `NOT_SERVING` bis zum vollständigen Start und der Composition-Root-Boot-Test.
   Begründung: Der v0.13.0-Startup-Crash hat gezeigt, dass diese Lücke ein
   akutes Produktionsrisiko ist, nicht nur ein Strukturthema. Die vollständige
   Start-Transaktion mit Rollback bleibt in Welle C.
3. Payload-Lücken und exhaustive Eventklassifikation aus SPEC4-01 schließen.
4. ServerInfo/Feature-Negotiation aus SPEC4-02 additiv veröffentlichen.

### Welle B: Zustandsübertragung vereinfachen

1. Typisierte Measurement-IDs aus SPEC4-06 additiv einführen.
2. Bridge-Snapshotassembler und Snapshot-RPC aus SPEC4-03 implementieren.
3. HA auf Snapshot/Initial-Stream umstellen; Legacy-Pfad beibehalten.
4. Nach Telemetrie in Produktion die Python-State-Grenzen aus SPEC4-08
   vereinfachen.

### Welle C: Lifecycle und Ausfallradius

1. Application-Start/Stop aus SPEC4-04 transaktional machen.
2. Provider-Close und gemeinsamen Expiry-Lifecycle aus SPEC4-09 anschließen.
3. RecoverySupervisor und getrennte Readiness aus SPEC4-05 einführen.
4. Registry-Projektionen und Diagnostik aus SPEC4-10 schrittweise migrieren.

### Welle D: Domänencode konsolidieren

1. HVAC-Gemeinsamkeiten aus SPEC4-11 extrahieren.
2. Upstream-Patchstatus automatisieren und Fork entfernen, sobald möglich.

Jede Welle ist separat releasefähig. Ein neuer optimaler Pfad wird erst zum
Default, wenn der alte Pfad weiterhin als getesteter Fallback funktioniert.

### Review-Folgepunkte aus dem Welle-A-Review (2026-07-18)

Zwei im Pre-Merge-Review der Welle-A-Umsetzung bestätigte, aber bewusst nicht
dort gefixte Punkte, da sie Spezifikationsentscheidungen statt Bugfixes sind:

1. **OHPCF-Blattfrische und Löschsemantik** (Ziel: SPEC4-01/SPEC4-08):
   Die konsolidierte OHPCF-Verfügbarkeit hängt an `ohpcfCoreReadMask` — fällt
   ein Kernread kurzzeitig aus, flappt das gesamte Aggregat als
   `TEMPORARILY_UNAVAILABLE`, obwohl nur ein Teilfeld betroffen ist. Zudem
   stempelt jedes Partial-Delta das gesamte `COMPRESSOR_FLEXIBILITY`-Aggregat
   als frisch, und optionale Felder (`requested_power_*`, `start_time`) können
   per Delta nie wieder auf `None` geleert werden. Benötigt eine explizite
   Entscheidung: Blatt-Granularität für OHPCF-Frische (analog zu den
   Per-Phase-Measurements) oder dokumentierte Aggregat-Semantik inklusive
   Clear-Pfad.

   **Entscheidung Welle B:** `COMPRESSOR_FLEXIBILITY` bleibt bewusst ein
   Aggregat, weil die HA-Entities Angebot, Prozesszustand und Constraints als
   zusammengehörigen Vertrag konsumieren. Ein typisiertes `update_field`
   ersetzt nur das betroffene Blatt; bei optionalen Blättern bedeutet fehlende
   Presence im zielgerichteten Delta explizit `None` und ist damit der
   Clear-Pfad. Der vollständige Initial-/Recovery-Snapshot ersetzt das gesamte
   Aggregat und löscht dort ebenfalls nicht mehr vorhandene optionale Werte.
   Die Aggregat-Frische setzt weiterhin erfolgreiche Kernreads plus den
   Zielread voraus; ein fehlender Kernread macht das Aggregat vorübergehend
   unavailable. Eine zusätzliche OHPCF-Blatt-Freshness wird erst mit dem nach
   Produktionstelemetrie vorgesehenen SPEC4-08-Slice eingeführt, falls reale
   Flaps dessen Nutzen belegen.

2. **Vertrags- und Stream-Lebenszyklus** (Ziel: SPEC4-02 Folge-Slice, SPEC4-04,
   SPEC4-05): Der `BridgeContract` wird pro `BridgeRuntime` genau einmal
   gelesen und nie neu verhandelt — ein Bridge-Upgrade bei bestehendem Channel
   wird erst nach HA-Reload sichtbar; Neuverhandlung beim Channel-Neuaufbau
   spezifizieren. Außerdem offen: Begrenzung der Stream-Fan-out-Verstärkung
   (`MaxConcurrentStreams`/Backpressure), tatsächliches Gaten der RPCs hinter
   gRPC-Health `NOT_SERVING` (heute nur advisory) und ein Heartbeat-Feld im
   konsolidierten Stream, damit Heartbeat-Status nicht bis zu einem
   Poll-Intervall veraltet.

   **Teilauflösung Welle B:** `LPCEvent.heartbeat_update` transportiert den
   aktuellen `HeartbeatStatus` additiv im konsolidierten Stream; HA wendet ihn
   ohne Poll an. Vertragsneuverhandlung, Fan-out-/Backpressure-Grenzen und
   tatsächliches Health-Gating bleiben entsprechend ihrer ursprünglichen
   Zuordnung in Welle C bzw. einem SPEC4-02-Folgeslice.

## 7. Globale Definition of Done

Ein Arbeitspaket gilt nur als abgeschlossen, wenn alle zutreffenden Punkte
erfüllt sind:

1. Anforderungen und Akzeptanzkriterien des Pakets sind durch Tests oder
   dokumentierte Hardware-Verifikation belegt.
2. `ruff`, `mypy --strict`, Python-Tests, `go vet`, `go test -race`, relevante
   Integrationstests und Proto-Governance laufen grün.
3. Neue Protobuf-Felder/RPCs sind additiv, beide Stub-Sätze sind regeneriert und
   Drift-/Breaking-Gates sind grün.
4. Cross-Language-Szenarien decken jede neue Vertragssemantik ab.
5. Kein neuer Hintergrundtask, Timer, Stream oder Kanal bleibt nach Unload/Stop
   aktiv.
6. Multi-Device-Tests beweisen SKI-Isolation für Reads, Writes, Events,
   Capabilities, Recovery und Diagnostics.
7. Sicherheitsmodi und Redaction sind für neue RPCs und Diagnosefelder getestet.
8. Bestehende HA-Entity-IDs, Defaultaktivierung und Availability ändern sich
   nicht unbeabsichtigt.
9. Performanceziele werden gemessen: RPC-Anzahl pro Sync, Snapshotdauer,
   Refreshanzahl pro Eventburst und Event-Drops.
10. README, Quality Scale und Vertragsdokumentation werden angepasst, sofern
    Benutzerverhalten, Diagnose oder Konfiguration betroffen sind.

## 8. Nicht-Ziele

Folgende Arbeiten sind ausdrücklich nicht Teil dieser Spezifikation:

- neue EEBUS-Use-Cases oder Unterstützung ungetesteter Hardware,
- Umstellung auf einen anderen Transport als gRPC,
- Zusammenlegung von Python-Integration und Go-Bridge in einen Prozess,
- Entfernung aller Legacy-RPCs ohne veröffentlichte Kompatibilitätsphase,
- Generierung der HA-Entities direkt aus Protobuf-Deskriptoren,
- eine universelle Meta-DSL für sämtliche EEBUS-Use-Cases,
- kosmetische Dateiaufteilung ohne Verantwortungs- oder Testverbesserung,
- Micro-Optimierungen an Dataclass-Allokationen oder kleinen Mappingtabellen,
- Änderung der TLS-/Token- oder Loopback-Sicherheitsgrundsätze.

## 9. Erwartetes Ergebnis

Nach Umsetzung der priorisierten Pakete besitzt das System:

- einen vollständig nutzbaren, geordneten Pushpfad ohne regelmäßige
  payloadbedingte Vollabgleiche,
- einen explizit ausgehandelten Bridge-Client-Vertrag,
- einen vollständigen Gerätezustand in einem statt bis zu 16 Read-RPCs,
- transaktionalen Start/Stop mit begrenztem Rollback und ohne verwaiste Timer,
- geräteisolierte Recovery statt unnötigem globalem Ausfall,
- stabile, typisierte Messwertsemantik ohne Stringheuristiken,
- sprachübergreifende Verhaltenstests,
- kleinere, getypte Verantwortungsgrenzen in Python und Go,
- diagnostizierbare Event-, Snapshot-, Provider- und Recoverypfade.

Damit sinken primär Inkonsistenz-, Versions- und Ausfallrisiken. Die Reduktion
von Codewiederholung ist ein nachgelagerter, aber messbarer Nebeneffekt.
