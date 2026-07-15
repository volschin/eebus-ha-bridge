# Spezifikation: priorisiertes Refactoring und Optimierung

**Status:** Vorgeschlagen  
**Stand:** 2026-07-15  
**Scope:** `custom_components/eebus`, `eebus-bridge`, Proto-Vertrag, CI und unmittelbar betroffene Dokumentation

## 1. Ziel

Diese Spezifikation beschreibt die nächsten Refactoring- und Optimierungsschritte nach fachlicher Bedeutung. Vorrang haben Sicherheit und die korrekte Zuordnung physischer Geräte, danach Zuverlässigkeit und Laufzeitverhalten und erst anschließend strukturelle Vereinfachung.

Das Ergebnis soll:

- Steuerbefehle und Messwerte auch bei mehreren gekoppelten Geräten eindeutig einem SKI zuordnen,
- alle von anderen Hosts erreichbaren gRPC-Operationen authentisieren und verschlüsseln,
- Pairing- und Unpairing-Befehle verlustfrei ausführen,
- Lastspitzen, überholte Provider-Werte und Hintergrund-Task-Lecks vermeiden,
- die Verantwortlichkeiten des Python-Coordinators und des Go-Startpfads klar trennen,
- Änderungen am Proto-Vertrag auf beiden Seiten reproduzierbar prüfen und
- Dokumentation, Konfiguration und tatsächliches Verhalten wieder synchronisieren.

Nicht Ziel dieses Vorhabens sind neue EEBUS-Use-Cases, Änderungen an der fachlichen LPC-/HVAC-/DHW-Semantik, eine neue Benutzeroberfläche oder ein Austausch von gRPC bzw. `eebus-go`.

## 2. Bewertungsgrundlage

Die Analyse basiert auf dem Stand `v0.10.7` (`04aa31f`). Berücksichtigt wurden die handgeschriebenen Python- und Go-Quellen, Tests, CI, Proto-Generierung sowie bestehende Architektur- und Forschungsdokumente.

Positive Ausgangslage:

- Ruff ist lokal fehlerfrei.
- `go vet ./...`, `go test ./...` und `go test -race ./...` sind fehlerfrei.
- Die jüngste Vereinfachung in PR #104 hat bereits wesentliche Duplikate bei Messwertlesern, Temperatur-Monitoring, Fehlerabbildung und Sensorbeschreibungen entfernt.
- Der Proto-Vertrag ist als gemeinsame Quelle dokumentiert und generierte Stubs werden eingecheckt.

Nicht lokal verifiziert wurden `mypy` und die Python-Tests, weil die lokale Python-Umgebung die dafür dokumentierten Abhängigkeiten nicht vollständig enthält. Die CI führt beide Prüfungen aus.

### 2.1 Priorisierungskriterien

| Priorität | Bedeutung | Einordnung |
|---|---|---|
| P0 | Schutz von Anlage und Datenrichtigkeit | Sicherheitslücke, falsches Gerät oder bestätigter, aber verlorener Steuerbefehl möglich |
| P1 | Betriebszuverlässigkeit und Änderbarkeit | Ausfälle, veraltete Werte, Task-Lecks oder hohes Regressionsrisiko bei Erweiterungen |
| P2 | Wartbarkeit und Betriebskomfort | Vereinfachung mit begrenzter unmittelbarer Laufzeitwirkung |

Aufwand wird relativ als S, M oder L angegeben. Er ist kein Terminversprechen.

## 3. Wesentliche Befunde

| ID | Priorität | Befund und Evidenz | Auswirkung |
|---|---:|---|---|
| B-01 | P0 | `config.yaml` empfiehlt für einen separaten Bridge-Host `grpc.bind: 0.0.0.0`. Nur Grid-/PV-/Batterie-Push wird dann deaktiviert; Pairing, LPC-, DHW-, HVAC- und OHPCF-Schreib-RPCs bleiben ohne Transportverschlüsselung oder Authentisierung registriert (`internal/grpc/security.go`, `cmd/eebus-bridge/main.go:255-269`). | Jeder erreichbare Client kann Geräte koppeln/entkoppeln und die Wärmepumpe steuern. |
| B-02 | P0 | Die HA-Abfragen wiederholen bei `NOT_FOUND` mehrere gerätebezogene Reads mit leerem SKI. Die Bridge wählt bei leerem SKI ein beliebiges kompatibles bzw. zuerst gefundenes Gerät; Map-/Szenario-Reihenfolgen sind nicht als Geräteidentität geeignet (`coordinator.py:275-468`, `monitoring_service.go:346-376`, `registry.go`). | Bei mehreren Geräten können Mess- oder LPC-Daten dem falschen HA-Gerät zugeordnet werden. `read_fallback_used` erweitert zusätzlich die Ereignisannahme. |
| B-03 | P0 | `RegisterRemoteSKI` und `UnregisterRemoteSKI` publizieren Befehle über denselben Event-Bus wie Zustandsereignisse. Der Bus verwirft bei vollem 64er-Puffer absichtlich Ereignisse, während der RPC trotzdem Erfolg liefert (`device_service.go:47-66`, `eventbus.go:37-55`). | Pairing/Unpairing kann still verloren gehen. |
| B-04 | P1 | Änderungen hochfrequenter HA-Sensoren starten je Provider unabhängige asynchrone Pushes. Es gibt keine Serialisierung oder Latest-wins-Koaleszierung (`coordinator.py:1052-1227`). | Mehrere RPCs können parallel laufen; ältere Werte können nach neueren eintreffen und die EEBUS-Provider zurücksetzen. |
| B-05 | P1 | `EebusCoordinator` umfasst rund 1.500 Zeilen und 54 Methoden für Channel, Polling, Writes, Streams, Zustandskonvertierung und drei Provider. Der Zustand ist überwiegend `dict[str, Any]`. Stream- und Initial-Push-Tasks teilen sich eine Liste und werden beim Shutdown abgebrochen, aber nicht abgewartet. | Hohe Änderungskopplung, schwache statische Absicherung und erschwerte Lifecycle-Tests. |
| B-06 | P1 | Der Monitoring-Watchdog verwendet einen globalen Zeitstempel. Erfolg eines Geräts kann ein anderes festgefahrenes Gerät maskieren. Umgekehrt bleibt ein absichtlich offline befindliches, aber registriertes Gerät ein Neustartgrund (`registry.go:24-50`, `main.go:297-310`). | Falsch-negative oder wiederholte falsch-positive Health-Entscheidungen im Mehrgerätebetrieb. |
| B-07 | P1 | Der CI-Job `proto-drift` regeneriert nur Python-Stubs. Go-Stubs werden weder regeneriert und verglichen noch werden `buf lint` und eine Breaking-Change-Prüfung ausgeführt. `generate_proto.sh` zählt Proto-Dateien manuell auf. | Ein Proto-Commit kann nur eine Vertragsseite aktualisieren oder eine neue Datei in Python auslassen. |
| B-08 | P1 | Go-Start, Wiring, Watchdog und Shutdown liegen in `main.go` (323 Zeilen). Fehler in Hintergrund-Goroutinen rufen `log.Fatalf` auf; dadurch wird der Prozess ohne kontrollierten Shutdown beendet. | Komponenten-Lifecycle und Fehlerpfade sind kaum isoliert testbar; Ressourcen werden bei Fehlern nicht geordnet beendet. |
| B-09 | P2 | YAML verwendet nicht `KnownFields`; ungültige Environment-Werte werden still ignoriert. Ports und sicherheitsrelevante Kombinationen werden nicht zentral validiert (`internal/config/config.go`). | Tippfehler führen zu unerwarteten Defaults statt zu einem klaren Startfehler. |
| B-10 | P2 | README und Entwicklerdokumentation widersprechen dem Code: 30-Sekunden- statt 5-Minuten-Polling, Raumheizungssteuerung als nicht unterstützt und HVAC als „out of scope“, obwohl sie implementiert ist. Mehrere produktive Pfade tragen noch `SPIKE`-Kommentare. | Betrieb und weitere Planung stützen sich auf veraltete Annahmen. |

## 4. Zielarchitektur

```text
Home-Assistant-Plattformen
          |
          v
 EebusCoordinator  ---- nur HA-Zustand und öffentliche Aktionen
    |       |       \
    |       |        +-- ProviderPublisher (je Provider genau ein Worker, latest wins)
    |       +----------- StreamSupervisor (Start, Reconnect, Stop)
    +------------------- SnapshotReader
                            |
                       EebusGrpcClient
                  (Channel, TLS/Auth, RPC-Fehler)
                            |
====================== Proto-v1-Vertrag ======================
                            |
                    Bridge Application
             (Wiring, Lifecycle, Health, Shutdown)
                |                         |
       gRPC-Adapter                 StateEventBus
                |                  (nur Beobachtungen,
       TrustController              Verlust tolerierbar)
                |                         |
             EEBUS-/Use-Case-Adapter und DeviceRegistry
```

Zentrale Regel: Ein verlustbehafteter Fan-out-Bus darf nur Beobachtungen transportieren, deren Zustand anschließend erneut gelesen werden kann. Befehle, Sicherheitsentscheidungen und Identitätsauflösung müssen einen synchronen, fehlerfähigen Pfad besitzen.

## 5. Anforderungen und Arbeitspakete

### RF-01: Remote-gRPC absichern

**Priorität/Aufwand:** P0/L  
**Abhängigkeiten:** keine; vor einer weiteren Empfehlung von `0.0.0.0` umzusetzen

1. Die Bridge erhält zwei explizite Betriebsarten:
   - `loopback`: Plaintext ist ausschließlich auf Loopback zulässig.
   - `tls_token`: TLS und ein Auth-Token sind für alle RPCs einschließlich Health und Reflection verpflichtend.
2. Ein Nicht-Loopback-Bind ohne `tls_token` muss beim Konfigurationsladen mit einer klaren Fehlermeldung scheitern. Es gibt keinen stillen unsicheren Fallback.
3. TLS-Zertifikat/Key und Token werden aus Dateien gelesen. Geheimnisse dürfen weder in Logs noch HA-Diagnosen erscheinen.
4. Unary- und Stream-Interceptor prüfen das Token mit konstantzeitlichem Vergleich. Reflection ist entweder deaktiviert oder durch dieselbe Prüfung geschützt.
5. HA-Integration, Config Flow, Reconfigure Flow, Live-Watcher und Healthcheck verwenden denselben sicheren Channel-Aufbau. Plaintext-Tokenübertragung ist nicht zulässig.
6. Bestehende Loopback-Installationen funktionieren ohne Migration weiter. Für Remote-Installationen dokumentiert ein eigener Migrationsabschnitt (Teil derselben PR, nicht nachgereicht) Zertifikatsbereitstellung, Token-Erzeugung und HA-Konfiguration.
7. Der neue Security-Modus in Config Flow/Reconfigure Flow ist ein neuer Konfigurationsschritt im Sinne der HA-Quality-Scale. `custom_components/eebus/quality_scale.yaml` wird in derselben PR auf die betroffenen Regeln (u.a. Config-Flow-Vollständigkeit, Reauthentication/Reconfigure-Flow, Diagnostics-Redaction) geprüft und aktualisiert.

**Akzeptanzkriterien:**

- Die Bridge startet mit `bind: 0.0.0.0` ohne TLS/Auth nicht.
- Ohne oder mit falschem Token liefern Unary- und Stream-RPCs `UNAUTHENTICATED`.
- Mit gültigem Token funktionieren Read, Write, Stream und Healthcheck.
- Ein Integrationstest deckt mindestens `RegisterRemoteSKI`, einen LPC-/HVAC-Write und einen Stream ab.
- Token, Key-Material und vollständige Credentials sind in Diagnose- und Logtests als abwesend belegt.
- Der Migrationsabschnitt für Remote-Installationen (Zertifikat, Token, HA-Konfiguration) ist im README vorhanden, bevor RF-01 als abgeschlossen gilt.
- `quality_scale.yaml` spiegelt den neuen Security-Modus wider; keine betroffene Regel bleibt unkommentiert `done`, wenn sich ihr Verhalten geändert hat.

### RF-02: Geräteidentität strikt isolieren

**Priorität/Aufwand:** P0/M

1. HA sendet für alle gerätebezogenen Reads und Writes ausschließlich den konfigurierten, kanonisierten Remote-SKI. Der automatische Retry mit `ski=""` entfällt.
2. Leere SKIs bleiben nur für ausdrücklich geräteagnostische Diagnosewerkzeuge erlaubt. Die Bridge darf sie nur auflösen, wenn genau ein kompatibles Gerät existiert; bei Mehrdeutigkeit folgt `FAILED_PRECONDITION`.
3. Gerätebezogene Bus-Ereignisse müssen am Bridge-Rand einen kanonischen, nicht leeren SKI erhalten. Ist die Identität nicht auflösbar, wird das Ereignis verworfen und gezählt, nicht als Wildcard verteilt.
4. `_event_matches` akzeptiert ausschließlich den konfigurierten SKI. `read_fallback_used` und die dadurch aufgeweitete Ereignisannahme entfallen.
5. `ListDevices` und andere sichtbare Registry-Ausgaben werden deterministisch nach SKI sortiert. Eine Sortierung ersetzt jedoch nie die explizite Identitätsprüfung.

**Akzeptanzkriterien:**

- Ein Test mit zwei simulierten Geräten und identischen Use-Cases weist nach, dass beide HA-Entries nur eigene Mess-, LPC- und Stream-Daten sehen.
- Ein unbekannter SKI bleibt `NOT_FOUND` und fällt nicht auf ein anderes Gerät zurück.
- Ein leerer SKI ist bei zwei kompatiblen Geräten mehrdeutig und liefert `FAILED_PRECONDITION`.
- Gemischte Schreibweisen mit Doppelpunkten, Bindestrichen, Leerzeichen und Groß-/Kleinschreibung werden einmalig zur selben kanonischen Identität normalisiert.

### RF-03: Pairing-Befehle aus dem Event-Bus entfernen

**Priorität/Aufwand:** P0/S

1. `DeviceService` erhält ein schmales `TrustController`-Interface mit synchronen, fehlerfähigen Methoden für Register und Unregister.
2. Der Controller ruft `BridgeService` direkt auf und führt beim Unregister die Registry-Bereinigung aus. Der RPC antwortet erst, wenn der Befehl angenommen wurde.
3. `EventTypeDeviceRegisterSKI` und `EventTypeDeviceUnregisterSKI` sowie die Router-Goroutine in `main.go` entfallen.
4. Ergebnisereignisse wie „connected“, „disconnected“ und „trust removed“ bleiben Beobachtungen auf dem State-Event-Bus.
5. Der State-Event-Bus erfasst pro Eventtyp und Subscriber verworfene Ereignisse als Metrik bzw. mindestens als rate-limitierten Warnhinweis.

**Akzeptanzkriterien:**

- Auch bei vollständig gefüllten Event-Subscriber-Puffern werden Register und Unregister genau einmal ausgeführt.
- Fehler des Controllers werden als passender gRPC-Status zurückgegeben und nicht als Erfolg bestätigt.
- Unit-Tests prüfen Reihenfolge und Registry-Bereinigung beim Unregister.

### RF-04: Provider-Push serialisieren und koaleszieren

**Priorität/Aufwand:** P1/M

1. Grid, PV und Batterie erhalten je einen langlebigen Publisher-Worker.
2. Ein State-Change signalisiert nur „Daten neu lesen“. Während ein RPC läuft, werden weitere Signale zu genau einem Folge-Push zusammengefasst; der Worker liest dann den neuesten HA-Zustand.
3. Pro Provider ist höchstens ein RPC gleichzeitig aktiv. Ein älterer Snapshot darf keinen neueren Wert überschreiben.
4. Initial-Push, Fehlerbehandlung und Unsubscribe gehören zum Lifecycle des jeweiligen Publishers.
5. `UNAVAILABLE` und `UNIMPLEMENTED` bleiben erwartbare, rate-limitierte Zustände. Andere Fehler werden beobachtbar, ohne jeden Sensorwechsel mit einer Warnung zu protokollieren.

**Akzeptanzkriterien:**

- Ein Burst-Test mit mindestens 100 Zustandsänderungen beweist `max_in_flight == 1` und dass zuletzt der neueste Wert publiziert wurde.
- Unload während eines laufenden RPC beendet den Worker ohne zurückbleibende Tasks.
- Die Einheiten- und Bereichsvalidierung der bestehenden Provider bleibt unverändert abgedeckt.

### RF-05: Python-Lifecycle und Verantwortlichkeiten trennen

**Priorität/Aufwand:** P1/L  
**Abhängigkeit:** RF-02 und RF-04, damit die neuen Grenzen fachlich stabil sind

Die aktuelle öffentliche Oberfläche der Entities bleibt erhalten, intern werden folgende Module eingeführt:

- `grpc_client.py`: Channel-Erzeugung, TLS/Auth-Metadaten, Stub-Zugriff, Timeouts und zentrale gRPC-Fehlerklassifikation.
- `models.py`: `TypedDict`/Dataclasses für Coordinator-Snapshot, Setpoints, Systemfunktionen, Supportstatus und Provider-Konfiguration. Handgeschriebener Code verwendet `Any` nur dort, wo Home-Assistant- oder generierte gRPC-Schnittstellen keine bessere Typisierung zulassen.
- `snapshot.py`: parallele, gerätestrikte Reads und Proto-zu-Domain-Konvertierung ohne HA-Nebenwirkungen.
- `streams.py`: Stream-Definitionen, Event-Decoding, Reconnect und kontrollierter Stop.
- `providers.py`: HA-Sensorlesen, Einheitenumrechnung und die Worker aus RF-04.
- `coordinator.py`: Zusammenführen von Snapshot-/Stream-Updates, HA-Benachrichtigung und delegierende öffentliche Aktionen.

Lifecycle-Regeln:

1. Stream-, Provider- und einmalige Tasks werden getrennt verwaltet.
2. Shutdown kündigt alle Tasks, wartet sie mit `gather(..., return_exceptions=True)` ab und schließt danach genau einmal den Channel.
3. Reconnect verwendet begrenztes exponentielles Backoff mit Jitter. Auch ein normal beendeter Stream darf keine enge Neustartschleife erzeugen.
4. Channel-Neuanlage und -Invalidierung werden zentral serialisiert; sechs Streams erzeugen nach einem Ausfall nicht sechs voneinander unabhängige Channel-Lifecycles.
5. Polling erstellt erst einen vollständigen Snapshot und veröffentlicht ihn atomar. Hilfsreads dürfen währenddessen `self.data` nicht verändern.

Umsetzung als Folge kleiner, unabhängig mergbarer PRs (ein Modul bzw. eine klar abgegrenzte Verantwortlichkeit pro PR), nicht als ein Big-Bang-Umbau. Nach jedem Zwischenschritt bleiben Ruff, `mypy --strict` und die volle Testsuite grün; ein Zwischenschritt, der die 1.500-Zeilen-Datei nur teilweise entkoppelt, ist ein gültiger Merge-Zustand, kein Grund den PR offen zu halten. Zeigt ein Zwischenschritt unerwartete Kopplung (z. B. gemeinsam mutierter `self.data`-Zustand zwischen Snapshot- und Provider-Workern), wird die betroffene Modulgrenze angepasst statt sie zu erzwingen.

**Akzeptanzkriterien:**

- Setup/Unload/Reload hinterlässt weder offene Channels noch laufende `eebus_*`-Tasks.
- Tests decken Stream-Abbruch, `UNIMPLEMENTED`, normalen Stream-Abschluss, Cancellation während Backoff und Channel-Neuanlage ab.
- `mypy --strict`, Ruff und alle bestehenden Tests bleiben grün.
- Entity-Klassen greifen auf typisierte Zustandsfelder zu; freie String-Keys sind an einer Modellgrenze konzentriert.

### RF-06: Watchdog pro Gerät und zustandsbezogen auslegen

**Priorität/Aufwand:** P1/M

1. Die Registry führt Verbindungszustand und letzten erfolgreichen Monitoring-Resolve pro kanonischem SKI.
2. Ein Monitoring-Erfolg aktualisiert nur das betroffene Gerät.
3. Staleness beginnt mit einer erfolgreichen SPINE-Verbindung bzw. Neuverbindung und wird nur für aktuell verbundene, vertrauenswürdige Geräte bewertet. Ein ausgeschaltetes, sauber getrenntes Gerät erzeugt keine Neustartschleife.
4. Aggregate Health ist `NOT_SERVING`, wenn mindestens ein aktuell verbundenes Gerät nach seiner Grace Period stale ist. Logs nennen betroffene SKIs gekürzt/redigiert und Alter des letzten Erfolgs.
5. Der Watchdog meldet den Fehler zunächst über Health und löst anschließend über den Application-Lifecycle einen kontrollierten Prozessabbruch aus; kein `log.Fatalf` aus der Goroutine.

**Akzeptanzkriterien:**

- Erfolge von Gerät A maskieren ein stale Gerät B nicht.
- Ein sauber getrenntes Gerät löst keinen periodischen Neustart aus.
- Reconnect setzt eine neue Grace Period, ohne historische Staleness dauerhaft zu verlieren.
- Zeitabhängige Tests verwenden eine injizierte Clock und kein `time.Sleep`.

### RF-07: Proto-Vertrag vollständig in CI absichern

**Priorität/Aufwand:** P1/S

1. Bei Proto-Änderungen führt CI `buf lint` aus.
2. Auf Pull Requests prüft `buf breaking` gegen den Proto-Stand des Zielbranches. Bewusste Breaking Changes benötigen eine neue API-Version statt einer stillen Änderung in `eebus.v1`.
3. CI regeneriert Go- und Python-Stubs in derselben Prüfung und vergleicht beide eingecheckten Verzeichnisse.
4. Die Python-Generierung entdeckt alle `eebus/v1/*.proto` deterministisch, statt eine manuelle Dateiliste zu pflegen.
5. Tool- und Runtime-Versionen bleiben gepinnt; die bestehende grpcio-Synchronisierung bleibt maßgeblich.

**Akzeptanzkriterien:**

- Eine absichtlich veraltete Go- oder Python-Stubdatei lässt CI fehlschlagen.
- Eine neue Proto-Datei erzeugt ohne Scriptänderung Stubs auf beiden Seiten.
- Feldlöschung oder inkompatible Feldtypänderung in `v1` wird als Breaking Change abgelehnt.

### RF-08: Go-Application-Lifecycle extrahieren

**Priorität/Aufwand:** P1/M

1. `main()` verarbeitet nur Flags, lädt Konfiguration und ruft `run(ctx, cfg)` auf.
2. Eine `Application` besitzt Bridge, Use-Cases, gRPC-Server, Watchdog und deren Stop-Reihenfolge.
3. Optionale Use-Cases werden über kleine, explizite Setup-Funktionen registriert. Fehler enthalten Use-Case-Kontext und werden zurückgegeben.
4. Hintergrundfehler laufen über einen gemeinsamen Fehlerkanal bzw. eine `errgroup`; Signal oder erster fataler Komponentenfehler starten genau einen Shutdown.
5. gRPC erhält einen begrenzten Graceful-Stop und fällt nach Timeout auf `Stop` zurück. Heartbeat und EEBUS werden auch bei Start-/Runtime-Fehlern beendet.

**Akzeptanzkriterien:**

- Tests prüfen partiellen Startfehler, gRPC-Serve-Fehler, Watchdog-Fehler, Signal-Shutdown und idempotenten Stop.
- In Hintergrund-Goroutinen gibt es kein `log.Fatal`/`os.Exit`.
- `go test -race ./...` bleibt fehlerfrei.

### RF-09: Konfiguration, Logging und Dokumentation härten

**Priorität/Aufwand:** P2/S

1. YAML wird mit `KnownFields(true)` dekodiert. Unbekannte Felder, ungültige Bool-/Port-Environment-Werte, Ports außerhalb 1–65535 und inkonsistente Security-Konfigurationen verhindern den Start mit handlungsorientierter Meldung.
2. Debug-Ausgaben in `MonitoringService` respektieren `logging.debug_events`; das Präfix `[DEBUG]` allein ist keine Log-Level-Steuerung. Wiederholte Fehler werden rate-limitiert.
3. README, `CLAUDE.md`, `config.yaml` und relevante Forschungsdokumente werden gegen den implementierten Stand abgeglichen. Mindestens zu korrigieren sind:
   - Polling-Fallback: 5 Minuten statt 30 Sekunden,
   - produktive Raumheizungs-Sollwert-/Modussteuerung,
   - aktuelle HVAC-/DHW-Unterstützung,
   - Sicherheitsanforderungen für einen separaten Bridge-Host,
   - Status der noch experimentellen Provider.
4. Historische Forschungsnotizen bleiben als Historie erhalten, erhalten aber deutlich sichtbare „superseded by“-Hinweise statt widersprüchlicher aktueller Handlungsanweisungen.

**Akzeptanzkriterien:**

- Tests decken unbekannte YAML-Felder, ungültige Environment-Werte und jede Security-Modus-Kombination ab.
- Im normalen Betrieb entstehen durch die 5-Minuten-Polls keine `[DEBUG]`-Zeilen.
- Eine Dokumentationssuche findet keine aktive Aussage mehr, nach der Raumheizungssteuerung nicht unterstützt sei oder Polling alle 30 Sekunden erfolge.

### RF-10: Nur belegte Go-Duplikate weiter abstrahieren

**Priorität/Aufwand:** P2/M  
**Abhängigkeit:** Charakterisierungstests der betroffenen Use-Cases

1. MGCP, VAPD und VABD teilen Aufbau und Publikation lokaler Measurement-Server. Eine interne, datengetriebene Provider-Basis darf Feature-Aufbau, Measurement-Description/Data-Paare und Publish-Validierung übernehmen; Use-Case-Szenarien und öffentliche Provider-Typen bleiben explizit.
2. Wiederholtes Entity-/Feature-Binding und Request-Verhalten der Setpoint-/HVAC-Clients darf in kleine Hilfsfunktionen extrahiert werden.
3. DHW- und Room-Heating-Systemfunktionen werden nicht in einen universellen generischen Use-Case gepresst: Boost/Overrun, Operation-Mode-Beziehungen und unterschiedliche Fehlermodelle sind fachliche Unterschiede.
4. Die Composition-Root-Vereinfachung aus RF-08 hat Vorrang vor einer weiteren Reduktion einzelner Dateien.

**Akzeptanzkriterien:**

- Alle bisherigen Szenario-, Measurement-ID-, Unit- und Event-Mappings sind vor dem Umbau durch tabellengetriebene Tests charakterisiert.
- Die Abstraktion reduziert Duplikation, ohne neue Konfigurationsoptionen oder ungenutzte Erweiterungspunkte einzuführen.
- Hardware-relevante Wire-Daten und Use-Case-Advertisements bleiben unverändert.

## 6. Umsetzungsreihenfolge

### Phase 0: Guardrails und Charakterisierung

1. Zwei-Geräte-Tests für Read-, Write- und Stream-Isolation ergänzen.
2. Event-Bus-Sättigung und Provider-Bursts reproduzierbar testen.
3. Aktuellen Proto-Drift beider Sprachen prüfen.
4. Security-Integrationstestgerüst mit Loopback und Remote-Listener aufbauen.

Phase 0 ändert kein Produktverhalten und liefert die Regressionstests für die folgenden Phasen.

### Phase 1: Schutz und Datenrichtigkeit

1. RF-02 Geräteisolation.
2. RF-03 direkter Pairing-Befehlspfad.
3. RF-01 TLS/Auth einschließlich Remote-Migration.
4. Den sicherheitskritischen Teil von RF-09 unmittelbar mit RF-01 dokumentieren.

Diese Phase ist release-relevant und darf nicht mit strukturellen Großumbauten vermischt werden.

### Phase 2: Asynchrone Zuverlässigkeit

1. RF-04 Provider-Worker.
2. RF-06 gerätebezogener Watchdog.
3. Lifecycle-Anteil von RF-05.
4. RF-08 Go-Application-Lifecycle.

### Phase 3: Struktur und Typisierung

1. RF-05 modulare Python-Grenzen und typisierter Zustand.
2. RF-07 vollständige Contract-CI.
3. RF-09 strikte Konfiguration, Logging und Dokumentationsabgleich.
4. RF-10 nur, wenn die Charakterisierungstests eine stabile gemeinsame Form bestätigen.

## 7. Qualitäts- und Abnahmestrategie

Für jedes Arbeitspaket gelten mindestens:

- Python: Ruff, `mypy --strict`, vollständige Pytest-Suite und Coverage ohne Rückgang gegenüber dem vor dem Paket erfassten Wert.
- Go: `go vet ./...`, `golangci-lint`, `go test -race ./...` und die vorhandenen Integrationstests.
- Proto: `buf lint`, Breaking-Change-Prüfung, Regeneration und Drift-Prüfung beider Stub-Sätze.
- Security: Negativtests sind verpflichtend; „Happy Path funktioniert“ genügt nicht.
- Nebenläufigkeit: keine zeitbasierten Sleeps in neuen Unit-Tests, wenn Clock, Channel oder Synchronisationspunkt injizierbar ist.
- Bestehende Unique IDs, Entity-Namen, Config-Entry-Daten und Proto-v1-Feldnummern bleiben kompatibel, sofern eine Anforderung nicht ausdrücklich eine Migration definiert.

Übergreifende Erfolgsmetriken:

| Metrik | Ziel |
|---|---|
| unauthentisierte mutierende RPCs auf Nicht-Loopback | 0 |
| an falschen SKI gelieferte Reads/Events/Writes im Zwei-Geräte-Test | 0 |
| verlorene Pairing-Befehle bei Event-Bus-Sättigung | 0 |
| parallele Provider-RPCs pro Provider | maximal 1 |
| verbleibende `eebus_*`-Tasks nach HA-Unload | 0 |
| von CI ungeprüfte generierte Stub-Verzeichnisse | 0 |
| `log.Fatal`/`os.Exit` in Bridge-Hintergrund-Goroutinen | 0 |

## 8. Migration und Kompatibilität

- **Loopback-Nutzer:** keine Änderung an der Verbindung; Plaintext bleibt innerhalb des Hosts zulässig.
- **Remote-Nutzer:** benötigen vor dem Upgrade TLS-Zertifikat und Token. Die Release Notes müssen dies als Breaking Deployment Change ausweisen. Ein unsicherer automatischer Kompatibilitätsmodus ist ausgeschlossen.
- **Mehrgeräte-Nutzer:** temporär fehlende Daten bleiben künftig korrekt `unavailable`, statt von einem anderen Gerät übernommen zu werden.
- **Proto:** kompatible additive Änderungen bleiben in `eebus.v1`; inkompatible Änderungen erfordern `eebus.v2` und eine explizite Übergangsphase.
- **HA-Entities:** Refactoring ändert weder Unique IDs noch standardmäßig aktivierte Entities.

## 9. Bewusst zurückgestellte Punkte

- Austausch des verlustbehafteten State-Event-Busses durch garantierte Persistenz: Polling kann Beobachtungen rekonstruieren; nach RF-03 laufen keine Befehle mehr darüber.
- Vollständige generische Use-Case-Engine: Die fachlichen Unterschiede und Hardwarebesonderheiten überwiegen den aktuellen Nutzen.
- Änderung des 5-Minuten-Pollintervalls: Nach stabilen Streams ist es ein Reconciliation-Intervall, kein primärer Aktualisierungspfad.
- Neue Telemetrieplattform: Zunächst genügen strukturierte Logs, Health-Status und interne Drop-/Reconnect-Zähler.
- Neue EEBUS-Funktionen oder weitere Gerätemodelle: Sie sind separate Feature-Spezifikationen.

## 10. Definition of Done des Gesamtvorhabens

Das Vorhaben ist abgeschlossen, wenn alle P0- und P1-Pakete umgesetzt sind, die Abnahmemetriken erfüllt werden, Remote- und Mehrgeräte-Migration dokumentiert sind und die vollständige CI einschließlich Race-, Security-, Proto- und HA-Lifecycle-Tests grün ist. P2/RF-10 ist optional und darf nur folgen, wenn es die dann gemessene Komplexität tatsächlich reduziert.
