# eebus-go Fork- und Upstream-Strategie — Design-Dokument

**Datum:** 2026-07-13
**Status:** Vorgeschlagen
**Ziel:** Fehlende EEBUS-Funktionalität früh im `eebus-ha-bridge` nutzbar machen, gleichzeitig als wartbare Beiträge für `enbility/eebus-go` entwickeln und eine dauerhafte Abspaltung vom Upstream vermeiden.

---

## 1. Kontext und Motivation

`eebus-ha-bridge` verwendet `github.com/enbility/eebus-go` als Protokoll- und Use-Case-Bibliothek. Der Bridge fehlen jedoch Funktionen, die noch nicht in einem stabilen `eebus-go`-Release oder noch gar nicht im Upstream enthalten sind.

Der bisherige Ansatz kombiniert drei Arten von Erweiterungen:

1. Einbindung von Pseudoversionen aus dem Upstream-`dev`-Branch, beispielsweise für OHPCF.
2. Bridge-eigene Implementierungen generischer EEBUS-Rollen wie die Provider-Seiten von MGCP, VAPD und VABD.
3. Bridge-spezifische Adapter, die `eebus-go`-Events und -Methoden auf gRPC und Home Assistant abbilden.

Parallel entstehen im Upstream weitere relevante Änderungen. Beispiele zum Zeitpunkt dieses Designs sind Per-Phase-Messungen, Monitoring of DHW Temperature (MDT), Monitoring of Room Temperature (MRT), Monitoring of Outdoor Temperature (MOT) und Korrekturen an der MGCP-Entity-Kompatibilität.

Ein einzelner fremder PR-Branch kann kurzfristig direkt konsumiert werden. Sobald mehrere noch nicht gemergte Änderungen gleichzeitig benötigt werden, fehlt jedoch ein reproduzierbarer, gemeinsam getesteter Dependency-Stand. Eigenständige generische Implementierungen im Bridge-Repository erschweren außerdem spätere Upstream-Beiträge, weil sie an Bridge-interne Interfaces und Lifecycle-Entscheidungen gekoppelt werden.

### Entscheidung

Es wird ein Fork von `enbility/eebus-go` unter der Projekt- beziehungsweise Maintainer-Organisation angelegt. Der Fork ist eine **temporäre Integrations- und Contribution-Ebene**, kein eigenständiges EEBUS-Produkt.

`enbility/eebus-go` bleibt:

- der kanonische Modul- und Importpfad,
- das Ziel für generische EEBUS-Implementierungen,
- die bevorzugte Quelle nach Merge der benötigten Änderungen.

Der Fork dient dazu:

- mehrere Upstream-PRs und eigene Beiträge kontrolliert zu kombinieren,
- einen hardwaregetesteten Commit für Bridge-Releases bereitzustellen,
- eigene Änderungen in kleinen, upstreamfähigen Branches zu entwickeln.

---

## 2. Ziele und Nicht-Ziele

### Ziele

- Noch nicht gemergte EEBUS-Funktionen reproduzierbar in der Bridge testen und ausliefern.
- Generische Use Cases in der Struktur und nach den Konventionen von `eebus-go` entwickeln.
- Jeden eigenen generischen Patch ohne nachträgliche Entflechtung als Upstream-PR einreichen können.
- Die Abweichung vom Upstream explizit, klein und entfernbar halten.
- Für jeden Bridge-Release exakt nachvollziehen können, welche Upstream- und Fork-Commits enthalten sind.
- Hardwareerkenntnisse aus VR920/VR921/VR940f-Tests in generische Tests und Upstream-Beiträge überführen.

### Nicht-Ziele

- Kein langfristig unabhängiger Fork von `eebus-go`.
- Keine Übernahme der allgemeinen Wartung von SHIP, SPINE oder sämtlichen EEBUS-Use-Cases.
- Keine Aufnahme spekulativer Use Cases ohne aktuellen Bridge- oder Hardwarebedarf.
- Keine Verlagerung von gRPC-, Home-Assistant- oder Produktlogik in `eebus-go`.
- Keine automatische Auslieferung ungeprüfter Upstream-PR-Heads.
- Keine Änderung der Modulpfade in den Bridge-Imports von `github.com/enbility/eebus-go` auf den Fork.

---

## 3. Zielarchitektur

```text
github.com/enbility/eebus-go (dev)
                │
                │ regelmäßiger Sync
                ▼
github.com/<owner>/eebus-go
                │
                ├── contrib/<thema-a> ─────────────── PR ──► enbility/eebus-go
                ├── contrib/<thema-b> ─────────────── PR ──► enbility/eebus-go
                ├── vendor/pr-<nummer>                fremder, unveränderter PR
                │
                └── bridge-integration
                         │
                         ├── Basis: enbility/dev
                         ├── ausgewählte fremde PRs
                         └── eigene offene contrib-Commits
                                  │
                                  │ gepinnte Pseudoversion
                                  ▼
                         eebus-ha-bridge
                                  │
                                  ├── eebus-go Wrapper
                                  ├── gRPC API
                                  └── Home-Assistant-Integration
```

Der Integrationsbranch ist eine explizite Patch-Queue über dem Upstream. Er enthält keine originären Änderungen. Jede Änderung muss aus einem eigenen `contrib/*`-Branch oder einem nachvollziehbaren fremden PR stammen.

### Abhängigkeitsgrenze

```text
eebus-go                         eebus-ha-bridge
──────────────────────────────  ──────────────────────────────
EEBUS Use-Case-Zustandsmodell    gRPC-Vertrag
SPINE Feature Client/Server      EventBus- und Stream-Mapping
Bind/Subscribe/Read/Write        HA-Entity-Modell
EEBUS-Datenvalidierung           Konfiguration und Feature Flags
Entity-/Szenario-Kompatibilität  Produktpolitiken
Protokollnahe Events             Vaillant-Diagnose und Workarounds
```

Ein Bridge-Wrapper darf ein Upstream-Use-Case-Objekt konfigurieren und dessen API übersetzen. Er soll keine zweite Implementierung desselben EEBUS-Zustandsmodells enthalten.

---

## 4. Repository- und Remote-Modell

### Fork

Der Fork übernimmt unverändert den Modulpfad aus dem Upstream:

```go
module github.com/enbility/eebus-go
```

Das ist bei Go-Forks beabsichtigt. Die Bridge importiert weiterhin `github.com/enbility/eebus-go/...`; nur die Auflösung der Dependency wird temporär auf den Fork umgeleitet.

Empfohlene Remotes in einem lokalen Fork-Checkout:

```text
origin    github.com/<owner>/eebus-go
upstream  github.com/enbility/eebus-go
```

### Geschützte Branches

| Branch | Zweck | Direkte Commits |
|---|---|---|
| `dev` | Spiegel beziehungsweise regelmäßig aktualisierte Basis von `upstream/dev` | nein |
| `bridge-integration` | Getestete Komposition für `eebus-ha-bridge` | nur Integrations-Merges/Cherry-picks |
| `contrib/<thema>` | Ein logisch isolierter eigener Upstream-Beitrag | ja |
| `vendor/pr-<nummer>` | Optionaler lokaler Zeiger auf einen fremden PR-Stand | nein |

Der Fork-`dev`-Branch darf keine projektspezifischen Patches enthalten. Dadurch bleibt der Abstand zum Upstream sofort sichtbar.

### Branch-Namen

Beispiele:

```text
contrib/gcp-mgcp-provider
contrib/ps-vapd-provider
contrib/es-vabd-provider
contrib/compatible-entity-resolution
vendor/pr-226-ma-mdt
```

Branches werden nach dem generischen EEBUS-Thema benannt, nicht nach einer Home-Assistant-Entity oder einem Vaillant-Modell.

---

## 5. Dependency-Pinning in der Bridge

Solange der Fork benötigt wird, verwendet `eebus-bridge/go.mod` einen `replace`-Eintrag:

```go
require github.com/enbility/eebus-go <upstream-compatible-version>

replace github.com/enbility/eebus-go => github.com/<owner>/eebus-go <fork-pseudo-version>
```

Regeln:

1. Die Fork-Pseudoversion zeigt immer auf einen konkreten Commit von `bridge-integration`.
2. Branch-Namen, unversionierte lokale Pfade und bewegliche Referenzen sind für Releases unzulässig.
3. `go.sum` wird gemeinsam mit `go.mod` committed.
4. `ship-go` und `spine-go` werden als Teil desselben getesteten Dependency-Satzes betrachtet.
5. Ein Dependency-Update nennt im PR alle neu aufgenommenen und entfernten Upstream-Patches.
6. Sobald kein nicht gemergter Patch mehr benötigt wird, wird `replace` entfernt und direkt auf einen Upstream-Commit oder ein Release gepinnt.

Der `replace`-Eintrag macht den Sonderzustand im Dependency-Vertrag sichtbar. Ein direktes Umschreiben aller Imports auf den Fork würde dagegen unnötige Quellcodeabweichung erzeugen.

---

## 6. Patch-Inventar

Im Bridge-Repository wird eine maschinenlesbar einfache Datei `eebus-bridge/UPSTREAM_PATCHES.md` geführt, solange der Fork aktiv ist. Sie dokumentiert ausschließlich Patches, die im aktuell gepinnten Fork-Commit zusätzlich zur angegebenen Upstream-Basis enthalten sind.

Vorgeschlagenes Format:

```markdown
# eebus-go integration patches

Upstream base: enbility/eebus-go@<commit>
Fork commit: <owner>/eebus-go@<commit>

| Patch | Source | Reason | Hardware status | Removal condition |
|---|---|---|---|---|
| MA-MDT | enbility/eebus-go#226 | DHW temperature | VR940 verified | merged upstream |
| GCP-MGCP provider | <owner>/eebus-go#12 | grid data provider | experimental | released upstream |
```

Für jeden Patch werden mindestens dokumentiert:

- eindeutige Quelle beziehungsweise Upstream-PR,
- fachlicher Grund für die frühe Integration,
- Teststatus einschließlich Hardware, sofern relevant,
- Bedingung für seine Entfernung,
- bekannte Abhängigkeiten zu `spine-go` oder `ship-go`.

Das Inventar ist Teil der Review-Checkliste jedes Fork-Pin-Updates. Es verhindert, dass historische Cherry-picks zu unsichtbaren Dauerpatches werden.

---

## 7. Entwicklungsworkflow

### 7.1 Eigene generische Funktion

1. Bedarf und EEBUS-Rolle im Bridge-Issue beschreiben.
2. Prüfen, ob bereits ein Upstream-Issue, PR oder inkompatibler bestehender Use Case existiert.
3. Bei größerer API- oder Rollenentscheidung frühzeitig mit dem Upstream abstimmen.
4. `contrib/<thema>` direkt von aktuellem `upstream/dev` erstellen.
5. Funktion in der Paketstruktur und mit den Interfaces von `eebus-go` implementieren.
6. Unit Tests und, soweit möglich, Protokoll-/Feature-Tests in `eebus-go` ergänzen.
7. Upstream-PR aus dem isolierten Branch eröffnen.
8. Den unveränderten Contribution-Commit in `bridge-integration` integrieren.
9. Bridge-Wrapper und gRPC-/HA-Funktion separat im Bridge-Repository entwickeln.
10. Hardwarestatus im Patch-Inventar und in der Upstream-PR dokumentieren.

Änderungen aus Upstream-Review werden zuerst in `contrib/<thema>` umgesetzt und anschließend erneut in `bridge-integration` übernommen. Der Integrationsbranch wird nie zur Quelle des Beitrags.

### 7.2 Fremder offener Upstream-PR

Vor der Aufnahme werden geprüft:

- konkreter Nutzen für einen aktuellen Bridge-Use-Case,
- Lizenz und Herkunft der Commits,
- Mergeability und Aktivität des PRs,
- Abhängigkeiten zu anderen nicht gemergten Änderungen,
- Upstream-CI und lokale Tests,
- API-Stabilität und Konfliktpotenzial,
- Hardwareverhalten, wenn der PR Gerätekommunikation verändert.

Der geprüfte PR-Commit wird unverändert in `bridge-integration` übernommen. Notwendige Korrekturen werden dem ursprünglichen Autor angeboten oder als separater Follow-up-Branch geführt; sie werden nicht unkenntlich in den fremden Patch eingearbeitet.

### 7.3 Upstream-Merge

Nach Merge eines enthaltenen Patches:

1. `upstream/dev` in den Fork spiegeln.
2. `bridge-integration` auf die neue Basis neu aufbauen oder rebasen.
3. Den nun upstream enthaltenen Patch aus der Patch-Queue entfernen.
4. Das Patch-Inventar aktualisieren.
5. Bridge-Tests und relevante Hardwaretests wiederholen.
6. Die Bridge auf den neuen Integrationscommit pinnen.
7. Wenn die Patch-Queue leer ist, den `replace`-Eintrag entfernen.

Die Entfernung eines Patches ist eine echte Migration und kein bloßes Versionsupdate: Upstream kann den Beitrag während des Reviews verändert haben.

---

## 8. Code-Ownership

### Kandidaten für Upstream

Die folgenden aktuell Bridge-eigenen Bereiche werden auf Upstreamfähigkeit geprüft:

| Bridge-Bereich | Ziel | Begründung |
|---|---|---|
| MGCP Provider | `eebus-go` Use Case für die passende Provider-Rolle | generisches EEBUS-Verhalten |
| VAPD Provider | `eebus-go` Use Case für die passende Provider-Rolle | generische Feature-/Szenario-Implementierung |
| VABD Provider | `eebus-go` Use Case für die passende Provider-Rolle | generische Feature-/Szenario-Implementierung |
| Entity-Kompatibilitätslogik | bestehende `eebus-go` Use Cases beziehungsweise gemeinsame API | Multi-Entity-Geräte sind kein HA-spezifisches Problem |

Die bestehenden Dateien `internal/usecases/mgcp.go`, `vapd.go` und `vabd.go` werden nicht unverändert verschoben. Zuerst werden EEBUS-Zustandsmodell, Feature-Server und Szenariologik von Bridge-Lifecycle, Logging und EventBus getrennt.

### Verbleib in der Bridge

- Wrapper wie `LPCWrapper`, `MonitoringWrapper` und `OHPCFWrapper`.
- Übersetzung von `eebus-go`-Events in interne Events und gRPC-Streams.
- Auswahl der für Home Assistant sichtbaren Fähigkeiten.
- Konfiguration experimenteller Provider.
- Mapping von HA-Sensorwerten auf Provider-Eingaben.
- Wiederverbindung, Polling-Fallback und Bridge-Health.
- Gerätespezifische Diagnoseinformationen.

### Hersteller-Workarounds

Ein Vaillant-spezifischer Workaround bleibt zunächst in der Bridge, wenn seine Ursache nicht generisch belegt ist. Er wird nur upstream vorgeschlagen, wenn mindestens eine der folgenden Bedingungen erfüllt ist:

- das Verhalten folgt aus der EEBUS-Spezifikation,
- es betrifft nachweislich mehrere Geräte oder Hersteller,
- der Upstream akzeptiert eine klar gekapselte Kompatibilitätsregel,
- es handelt sich um einen generischen Robustheitsfehler unabhängig vom Gerät.

---

## 9. CI- und Teststrategie

### Fork-CI

Jeder `contrib/*`-Branch und `bridge-integration` muss mindestens die vorhandene `eebus-go`-CI bestehen. Zusätzliche projektbezogene Tests dürfen ergänzt werden, sofern sie auch als Upstream-Tests geeignet sind.

### Bridge-CI

Die normale CI testet gegen den in `go.mod` gepinnten produktiven Dependency-Satz:

```text
go vet ./...
golangci-lint
go test -race ./...
govulncheck
```

Zusätzlich wird ein nicht blockierender regelmäßiger Kompatibilitätslauf vorgesehen:

```text
Bridge gegen aktuellen enbility/eebus-go:dev
```

Dieser Lauf beantwortet zwei Fragen:

1. Ist die Bridge weiterhin mit dem Upstream kompatibel?
2. Kann der Fork-`replace` inzwischen entfernt werden?

Ein Test gegen `dev` darf nicht bei jedem normalen PR unkontrolliert neue Fremdänderungen einführen. Er läuft zeitgesteuert oder manuell und meldet Drift, ohne die reproduzierbare Haupt-CI zu ersetzen.

### Hardwaretests

Hardwaretests bleiben wegen der exklusiven SHIP-Verbindung manuell. Für jeden protokollrelevanten Patch wird festgehalten:

- Gateway und Firmware,
- getestete Rolle und Szenarien,
- Bind-/Subscribe-Verhalten,
- Read-/Write-Ergebnis,
- Reconnect-Verhalten,
- relevante SHIP-/SPINE-Trace-Beobachtungen.

„CI grün“ und „mit VR940 verifiziert“ sind getrennte Statuswerte.

---

## 10. Integrations- und Release-Regeln

Ein neuer Fork-Commit darf in einen Bridge-Release aufgenommen werden, wenn:

- seine Upstream-Basis eindeutig ist,
- alle zusätzlichen Patches im Inventar stehen,
- Fork- und Bridge-CI grün sind,
- für schreibende oder steuernde Funktionen ein Hardwaretest erfolgt ist oder die Funktion weiterhin explizit experimentell bleibt,
- Rollback auf den vorherigen Fork-Pin durch einen einzelnen Dependency-Revert möglich ist.

Release Notes nennen nutzerrelevante Funktionen, nicht die interne Fork-Mechanik. Das Dependency-Inventar bleibt die technische Quelle für Maintainer.

Der Fork-Pin wird nicht zusammen mit sachlich unabhängigen Bridge-Änderungen aktualisiert. So bleiben Regressionen und Reverts überschaubar.

---

## 11. Migration des bestehenden Projekts

### Phase 1 — Fork und Transparenz

1. Fork anlegen und `upstream`-Remote dokumentieren.
2. `dev` auf den aktuell verwendeten Upstream-Commit synchronisieren.
3. `bridge-integration` von dieser Basis anlegen.
4. `eebus-bridge/UPSTREAM_PATCHES.md` einführen.
5. Go-Dependency zunächst nur dann per `replace` umstellen, wenn der Integrationsbranch tatsächlich zusätzliche Patches enthält.

In dieser Phase ändert sich keine Laufzeitfunktion.

### Phase 2 — Relevante Fremd-PRs integrieren

1. Benötigte PRs einzeln priorisieren.
2. Pro PR Tests und Abhängigkeiten prüfen.
3. Nur aktuell benötigte PRs in `bridge-integration` aufnehmen.
4. Bridge-Adapter erst nach erfolgreicher Dependency-Integration ergänzen.

Die bloße Aussicht auf einen zukünftigen Sensor rechtfertigt keine Aufnahme.

### Phase 3 — Eigene Provider upstreamfähig machen

Empfohlene Reihenfolge:

1. MGCP Provider, weil dafür bereits ein Consumer-Gegenstück und reale Hardwarebeobachtungen existieren.
2. VAPD Provider.
3. VABD Provider.

Jeder Provider wird separat extrahiert und upstream angeboten. Die Bridge wechselt erst nach API- und Verhaltenstests vom internen Use Case auf den Fork-/Upstream-Use-Case.

### Phase 4 — Fork-Abhängigkeit abbauen

Nach jedem Upstream-Merge wird die Patch-Queue verkleinert. Zielzustand ist:

```text
eebus-ha-bridge ── require ──► github.com/enbility/eebus-go
                                  keine replace-Direktive
                                  keine zusätzlichen Fork-Patches
```

Der Fork kann als Contribution-Fork bestehen bleiben, darf aber kein notwendiger Bestandteil des produktiven Builds sein, wenn die Patch-Queue leer ist.

---

## 12. Risiken und Gegenmaßnahmen

| Risiko | Auswirkung | Gegenmaßnahme |
|---|---|---|
| Dauerhafte Fork-Abspaltung | wachsender Wartungsaufwand | keine originären Commits auf `bridge-integration`, Upstream-first, leere Patch-Queue als Ziel |
| Konflikte zwischen mehreren PRs | schwer reproduzierbare Fehler | isolierte Branches, dokumentierte Reihenfolge, gepinnter Integrationscommit |
| Upstream ändert einen PR vor Merge | Bridge verhält sich nach Update anders | nach Merge erneut integrieren und vollständig testen |
| Fremder PR wird nie gemergt | dauerhafter Patch | regelmäßig Nutzen prüfen; übernehmen, neu upstreamen oder Funktion entfernen |
| Fork-Pin verschleiert Dependency-Stand | Supply-Chain- und Debugging-Risiko | `replace`, Commit-Pin und Patch-Inventar gemeinsam reviewen |
| Bridge-spezifische Logik gelangt upstream | schwer wartbare API | Ownership-Grenze und getrennte Wrapper beibehalten |
| Hardwaretest blockiert durch exklusive SHIP-Verbindung | unvollständige Validierung | Hardwarestatus separat ausweisen, andere EEBUS-Clients vor Test stoppen |
| Zu große Upstream-PRs | langsames oder erfolgloses Review | ein Use Case beziehungsweise eine Rolle pro PR, keine Nebenrefactorings |

---

## 13. Entscheidungsregeln für neue Arbeit

Vor jeder neuen EEBUS-Funktion werden diese Fragen in Reihenfolge beantwortet:

1. Gibt es einen aktuellen, durch Hardware oder Produktfunktion belegten Bedarf?
2. Existiert die Funktion bereits im Upstream, in einem offenen PR oder in einem bekannten Fork?
3. Ist die fehlende Logik generisches EEBUS-Verhalten oder Bridge-Produktlogik?
4. Kann der generische Teil unabhängig von Home Assistant getestet werden?
5. Ist eine frühe Fork-Integration nötig, oder kann auf den Upstream-Merge gewartet werden?
6. Was ist die konkrete Entfernungskondition des Fork-Patches?

Wenn Frage 1 mit „nein“ beantwortet wird, wird die Funktion nach YAGNI nicht implementiert. Wenn Frage 3 „generisches EEBUS-Verhalten“ ergibt, beginnt die Entwicklung im `eebus-go`-Fork und nicht unter `eebus-bridge/internal/usecases`.

---

## 14. Erfolgskriterien

Die Umstrukturierung ist erfolgreich, wenn:

- jeder zusätzliche produktive `eebus-go`-Patch öffentlich nachvollziehbar ist,
- eigene generische Implementierungen als isolierte Upstream-PRs vorliegen,
- die Bridge weiterhin ausschließlich den kanonischen `enbility/eebus-go`-Modulpfad importiert,
- ein Bridge-Release aus einem festen Dependency-Commit reproduzierbar gebaut werden kann,
- Upstream-Merges die Patch-Queue verkleinern statt neue dauerhafte Abweichungen zu erzeugen,
- `internal/usecases` langfristig überwiegend Adapter statt vollständiger EEBUS-Use-Case-Implementierungen enthält,
- der Fork jederzeit ohne Verlust von Bridge-spezifischer Produktlogik aus dem Build entfernt werden kann.

---

## 15. Offene organisatorische Entscheidungen

Vor Umsetzung sind noch zwei Namen festzulegen:

1. GitHub-Owner des Forks (`volschin` oder eine Projektorganisation).
2. Verbindlicher Name des Integrationsbranches (`bridge-integration` wird empfohlen).

Diese Entscheidungen verändern das Architekturmodell nicht. Alle Dokumentationsbeispiele verwenden deshalb bis dahin `<owner>` als Platzhalter.

---

## Referenzen

- [`enbility/eebus-go`](https://github.com/enbility/eebus-go)
- [`eebus-go` PR #154 — Per-phase measurements](https://github.com/enbility/eebus-go/pull/154)
- [`eebus-go` PR #226 — Monitoring of DHW Temperature](https://github.com/enbility/eebus-go/pull/226)
- [`eebus-go` PR #232 — Monitoring of Room Temperature](https://github.com/enbility/eebus-go/pull/232)
- [`eebus-go` PR #233 — Monitoring of Outdoor Temperature](https://github.com/enbility/eebus-go/pull/233)
- [`eebus-go` PR #236 — MGCP actor type fix](https://github.com/enbility/eebus-go/pull/236)
- [`docs/eebus-vaillant-improvements.md`](../../eebus-vaillant-improvements.md)
- [`2026-04-06-eebus-bridge-design.md`](2026-04-06-eebus-bridge-design.md)
