# Offene Punkte — konsolidierte Spezifikation

**Datum:** 2026-07-22
**Status:** Aktiv — Sammelspec für Restarbeiten
**Basis:** `docs/refactoring-optimization-spec-v4.md`,
`docs/dhw-cdsf-upstream-migration-spec.md`,
`docs/room-heating-eebus-go-migration-spec.md`,
`eebus-bridge/UPSTREAM_PATCHES.md`, Review-Befunde aus PR #150–#156.

## 1. Zweck

Die drei laufenden Spezifikationen sind fachlich weitgehend umgesetzt, tragen
aber jeweils einen Rest an offenen Punkten, die sich überschneiden: alle drei
enden beim selben Thema, dem Abbau des `eebus-go`-Forks. Dieses Dokument führt
die Restpunkte an einer Stelle zusammen, ordnet sie nach Abhängigkeit und
benennt je Punkt ein prüfbares Exit-Kriterium. Es ersetzt die Quellspecs nicht;
diese bleiben die fachliche Herleitung.

## 2. Erledigter Stand (zur Abgrenzung)

Nicht mehr offen und deshalb hier nicht gelistet:

- **DHW CDSF:** Phasen 0–4 abgeschlossen; MDSF besitzt Reads, CDSF besitzt
  Negotiation, Boost- und Mode-Writes, lokale CDSF-Semantik entfernt (PR #143).
- **Room Heating:** Phasen 0–5b abgeschlossen; MRHSF/CRHSF/CRHT besitzen Reads,
  Mode- und Setpoint-Writes, Legacy-Code gelöscht (PR #152–#156, v0.15.0).
- **SPEC4 Welle A–C:** Contract-Negotiation (`GetServerInfo`/`BridgeContract`),
  payload-vollständiger Device-State-Stream, `GetDeviceSnapshot`, typisierte
  `MeasurementId`, transaktionaler Start mit Rollback-Ledger
  (`cmd/eebus-bridge/lifecycle.go`), `RecoverySupervisor`
  (`internal/eebus/recovery.go`), Health-Gating der RPCs
  (`internal/grpc/server.go:106`), `MaxConcurrentStreams`, Provider-Lifecycle,
  Registry-Diagnostik-RPC.
- **Welle-A-Review-Folgepunkt 2 (Vertragsneuverhandlung):** aufgelöst —
  `EebusRuntime._negotiate_contract` verhandelt pro Channel-Generation neu und
  benachrichtigt Sessions bei Vertragswechsel (`custom_components/eebus/runtime.py`).

## 3. Offene Punkte

### Gruppe A — Upstream und Fork-Abbau (blockierend für alle drei Specs)

Alle Punkte dieser Gruppe hängen an derselben Ursache: die Bridge pinnt
`volschin/eebus-go@3c6795b4d157` mit 15 Patchzeilen (9 enbility-PRs, 6 eigene
Härtungen). Solange der `replace`-Eintrag existiert, sind DHW-Phase 5,
RH-Phase 6 und SPEC4-11 nicht abschließbar, und Renovate bleibt für das Paket
deaktiviert.

**OPEN-A1 — enbility-PRs #232, #233, #239–#242, #246, #247, #249 upstream
mergen lassen.**
Quelle: `UPSTREAM_PATCHES.md`, DHW §9 Phase 5.1, RH §8 Phase 6.1.
Diese Zeilen sind unveränderte Cherry-Picks; der Merge liegt nicht in unserer
Hand, der Status muss aber verfolgt werden.
Exit: jede gemergte PR-Zeile ist aus `UPSTREAM_PATCHES.md` entfernt und der Pin
auf die neue `enbility/dev`-Basis gehoben.

**OPEN-A2 — Eigene Härtungspatches `volschin/eebus-go#1–#6` upstream
einreichen.**
Inhalte: fail-closed List-Merges, strukturierte CDSF-`WriteCapabilities` mit
Post-Acceptance-Refresh, unabhängige Capability-Auflösung für optionale
CDSF-Szenarien, fail-closed MRHSF-Auflösung bei mehreren Heating-System-
Functions, presence-sichere CRHT-Setpoint-States mit Shared-ID-Deduplizierung,
mode-unabhängige CRHT-Writes.
Diese decken sich mit den DHW-§13-Kandidaten 1, 3, 4 und 5.
Exit: je Patch entweder upstream gemergt oder als bewusst dauerhafter
Fork-Anteil dokumentiert.

**OPEN-A3 — `replace`-Direktive und Patch-Inventar entfernen.**
Quelle: DHW Phase 5.4, RH Phase 6.4, SPEC4-11.
Vorbedingung: OPEN-A1 und OPEN-A2 leer.
Exit: `go list -m github.com/enbility/eebus-go` löst direkt auf Upstream auf;
`UPSTREAM_PATCHES.md` ist leer oder gelöscht; Renovate für das Paket wieder
aktiviert (`renovate.json`); voller Testlauf plus Hardwarematrix gegen den
exakten Upstream-Commit; Patch-Release mit Multi-Arch-Image verifiziert.

### Gruppe B — Bekannte Upstream-Limitierungen mit Bridge-Workaround

Diese Punkte sind heute durch Bridge-seitige Workarounds abgefangen. Sie sind
nicht release-blockierend, aber jeder hält einen Codeanteil am Leben, den die
Migration eigentlich entfernen sollte.

**OPEN-B1 — CRHSF ohne öffentliche `WriteCapabilities`-API.**
Folge: `bridgeRoomHeatingSystemFunctionCapabilityInspector`
(`internal/usecases/roomheatingsysfn_configuration.go`) liest
`HvacSystemFunctionDescriptionListData`/`HvacSystemFunctionListData` roh und
fällt fail-closed aus. Analog zu `volschin/eebus-go#2` für CDSF.
Exit: CRHSF bietet eine fail-closed Capability-API; der Bridge-Inspector
entfällt ersatzlos.

**OPEN-B2 — CRHSF-Mode-ID-Auflösung und Post-Result-Refresh härten.**
Quelle: RH Phase 3, offene Checkbox; `UPSTREAM_PATCHES.md` Known limitations.
`crhsf.CRHSF.WriteOperationMode` wählt bei mehreren IDs desselben Mode-Typs den
ersten Treffer und fordert nach akzeptiertem Write keine Liste neu an. Am VR940
unkritisch (genau eine Heating-SystemFunction), für Multi-Zonen-Geräte nicht.
Exit: Patch analog `volschin/eebus-go#4` (MRHSF) für CRHSF; Ambiguität schlägt
fail-closed fehl, akzeptierte Writes lösen einen expliziten Refresh aus.

**OPEN-B3 — Sentinel für verschwundene Features.**
Quelle: RH §4.6 und Phase-3-Checkbox, DHW-Kontext, `UPSTREAM_PATCHES.md`.
`features/client.NewFeature` liefert im Disconnect-Rennen unklassifizierbare
`errors.New`-Texte; `mapUpstreamDHWWriteError` und
`mapUpstreamRoomHeatingWriteError` fallen deshalb auf `codes.Internal` statt
`codes.Unavailable` zurück. String-Matching ist bewusst ausgeschlossen.
Exit: exportierter Sentinel upstream; beide Mapper klassifizieren das
Disconnect-Rennen als `UNAVAILABLE`.

**OPEN-B4 — CDSF-`WriteCapabilities` Fail-closed-Ambiguität.**
Quelle: `UPSTREAM_PATCHES.md`, DHW §13 Kandidat 2.
Zero-Capabilities mit `nil`-Error sind nicht von „noch nicht verhandelt"
unterscheidbar; ein DHW-Write im Fenster direkt nach Connect meldet
`FAILED_PRECONDITION` statt `UNAVAILABLE`. Bridge-seitig nicht lösbar, ohne die
gerade entfernte Cache-Populationsverfolgung wieder einzuführen.
Exit: upstream unterscheidbarer Rückgabewert („not yet negotiated"); Bridge
mappt wieder auf `UNAVAILABLE`.

### Gruppe C — SPEC4-Restpakete

**OPEN-C1 — SPEC4-08: getypte Python-Session- und Zustandsgrenzen
(P2, nach Produktionstelemetrie).**
Bewusst zurückgestellt, bis Telemetrie den Nutzen belegt. Enthält als Teilfrage
den Welle-A-Review-Folgepunkt 1: OHPCF-Blatt-Frische. Die Welle-B-Entscheidung
hält `COMPRESSOR_FLEXIBILITY` bewusst als Aggregat; eine Blatt-Granularität
wird nur eingeführt, wenn reale Flaps (`ohpcfCoreReadMask`-Ausfall stempelt das
gesamte Aggregat `TEMPORARILY_UNAVAILABLE`) das rechtfertigen.
Exit: entweder Telemetriebeleg plus Umsetzung, oder dokumentierte Entscheidung,
das Aggregat dauerhaft beizubehalten und SPEC4-08 zu schließen.

**OPEN-C2 — SPEC4-11: gemeinsamer HVAC-Kern (P3).**
DHW- und Room-Heating-Facades teilen nach beiden Migrationen dieselbe Struktur
(Entity-Resolution, Capability-Inspector, Upstream-Writer, Error-Mapping,
Await-Result). Der Zuschnitt wird erst nach OPEN-A3 sinnvoll, weil der
Fork-Abbau die verbleibenden Unterschiede bestimmt.
Exit: geteilter Kern extrahiert oder begründet verworfen; `internal/usecases`
enthält keine duplizierte Await-/Mapping-Logik mehr.

**OPEN-C3 — Automatisierter Upstream-Patchstatus (SPEC4/Welle D.2).**
`UPSTREAM_PATCHES.md` wird heute manuell gepflegt und ist dadurch driftanfällig
(siehe OPEN-D2). Welle D sieht Automatisierung vor.
Exit: CI-Job prüft je Patchzeile den Upstream-Merge-Status und meldet
entfernbare Zeilen.

### Gruppe D — Aufgefallene Punkte aus Umsetzung und Review

**[x] OPEN-D1 — HA-Device-Registry liefert keine Gerätemetadaten.**
Quelle: RH Phase 0, Capture 2026-07-22:
`manufacturer`, `model`, `sw_version`, `hw_version` sind am
`climate.eebus_…`-Device `None`, obwohl die Bridge DeviceClassification
auswertet (`internal/usecases/classification.go`). In der RH-Spec ausdrücklich
als „separat zu klären" vertagt.
Exit: Ursache geklärt (liefert die Bridge die Felder nicht, oder setzt die
Integration `DeviceInfo` unvollständig?) und behoben oder als
Geräteeinschränkung dokumentiert.
Erledigt 2026-07-22: Es lagen drei Bridge-/Integrationslücken vor. Die Bridge
las nur einen zufällig bereits gefüllten Cache, ohne ein
DeviceClassification-Client-Feature anzulegen oder Manufacturer-Daten
anzufordern; Software- und Hardware-Revision fehlten im gRPC-Vertrag; und die
Integration schrieb nach dem Entity-Aufbau eintreffende Metadaten nicht in die
HA-Device-Registry nach. Ein eigener Classification-Client fordert die Daten
nun bei Detailed Discovery an, persistiert Brand, Modell, Seriennummer,
Gerätetyp sowie Software-/Hardware-Revision und löst einen Snapshot-Refresh
aus. HA übernimmt Initial- und Spätwerte. Nicht vom Gerät gesendete Felder
bleiben bewusst leer; es werden keine Vaillant-/VR940-Konstanten erfunden.

Hardwarebefund 2026-07-22 (VR940 `682F708C`, Stack 93, Dev-Image
`ghcr.io/volschin/eebus-bridge:d1-hwtest`): `manufacturer` = `Vaillant`,
`model` = `VWL 75/8.1 A 230V`, `serial_number` = `8000033711` erscheinen
Ende-zu-Ende im HA-Device-Registry (vorher alle `None`) und überstehen einen
Bridge-Neustart. **Geräteeinschränkung:** `sw_version`/`hw_version` bleiben
`null`. Der VR940 lehnt den aktiven `RequestManufacturerDetails`-Read ab
(`operation is not supported on function deviceClassificationManufacturerData`);
Software-/Hardware-Revision werden nur über diesen Read übertragen und sind auf
dem VR940 daher nicht verfügbar. Marke/Modell/Seriennummer kommen passiv aus der
Detailed Discovery. Die Bridge fordert den Read pro Gerät nur einmal an und
loggt die Ablehnung nur einmal statt bei jedem Reconnect.

**[x] OPEN-D2 — Spec- und Kommentar-Drift nach Phasenabschlüssen.**
Beobachtet in PR #156: Statuszeile der RH-Spec stand nach Phase 5b noch auf
„Phase 4 begonnen"; Doc-Kommentare verwiesen auf gelöschte Legacy-Writer;
`UPSTREAM_PATCHES.md` nannte eine bereits erfolgte Hardwareabnahme als
ausstehend; die CRHSF-`WriteCapabilities`-Lücke fehlte im Limitationsabschnitt.
Alles in `54ced05` korrigiert, die Ursache bleibt: Phasenabschlüsse aktualisieren
den Fließtext, nicht die Statusköpfe.
Exit: Checkliste „Phase abgeschlossen" in den Migrationsspecs verlangt
ausdrücklich Statuskopf, Kommentarbereinigung und Limitationsabschnitt.
Erledigt 2026-07-22: beide Migrationsspecs enthalten diese Abschlusscheckliste.

**[x] OPEN-D3 — Beleg für Write ohne zusätzliches Feature-Binding.**
Quelle: RH Phase 0, letzte offene Checkbox (Zeile 538).
De facto durch die Hardwarematrizen aus Phase 3 und Phase 5 erbracht: die
Upstream-Writer schrieben am VR940 ohne zusätzliches Binding. Der Nachweis ist
nur nicht an der Checkbox vermerkt.
Exit: Checkbox mit Verweis auf die Phase-3-/Phase-5-Matrix schließen.
Erledigt 2026-07-22: Die Phase-0-Checkbox verweist auf beide erfolgreichen
VR940-Hardwarematrizen und dokumentiert das fehlende Zusatz-Binding.

**[x] OPEN-D4 — Coverage-Badge blockiert CI wiederholt.**
Beobachtet in PR #143, #154, #155: `go check` schlägt fehl, weil das
eingecheckte `docs/badges/go-coverage.svg` um Zehntelprozente vom CI-Wert
abweicht; jedes Mal ein manueller Regenerationscommit.
Exit: Badge wird im CI generiert statt eingecheckt geprüft, oder die Prüfung
toleriert eine definierte Abweichung.
Erledigt 2026-07-22: Die CI toleriert eine dokumentierte Abweichung von 0,2
Prozentpunkten und verlangt erst darüber eine Regeneration des Badges.

**OPEN-D5 — Post-Write-Konvergenzlücke bei schneller Write-Folge.**
Quelle: RH Phase-3-Matrix. Bei 6 s Abstand zeigte ein Sample einmalig noch den
Vorzustand, bevor es korrekt konvergierte. Ursache ist der fehlende explizite
Post-Result-Refresh aus OPEN-B2; Konvergenz erfolgte sonst < 0,1 s über
Geräte-Notify.
Exit: nach OPEN-B2 erneut mit enger Write-Folge messen; kein Sample zeigt den
Vorzustand nach abgeschlossenem Write.

**OPEN-D6 — Lesbare Gerätefunktionen ohne Bridge-Nutzung.**
Quelle: Function-Inventar des VR940 (`docs/vr940-function-inventory.md`, Capture
2026-07-13). Die Schreibfläche des Geräts ist vollständig ausgeschöpft, vier
lesbare Functions werden aber nicht konsumiert: `deviceClassificationUserData`
auf `5`/`5:1`/`5:1:1` (Heizkreis-, Zonen- und Raumnamen; der zugehörige
`visualizationOfHeatingAreaName` wird annonciert),
`electricalConnectionCharacteristicListData` auf `3:f7`,
`measurementConstraintsListData` auf allen Measurement-Features sowie die
beiden HVAC-Relations-Listen.
Reine Bestandsaufnahme, kein Fehler: jede Umsetzung ist ein eigenes Feature mit
eigenem Hardwaretest. Priorität liegt bei den Namen, weil sie generische
Entity-Namen in HA ersetzen würden.
Exit: entweder pro Punkt ein Feature-Ticket mit Hardwareabnahme, oder eine
dokumentierte Entscheidung gegen die Umsetzung.

## 4. Reihenfolge und Abhängigkeiten

```text
OPEN-A1 ---+
           +--> OPEN-A3 --> OPEN-C2
OPEN-A2 ---+
   ^
   +-- OPEN-B1, OPEN-B2, OPEN-B4   (werden als Upstream-Patches Teil von A2)
           |
OPEN-B2 --> OPEN-D5
OPEN-B3 (unabhängig einreichbar)

OPEN-C1 wartet auf Produktionstelemetrie.
OPEN-C3 ist jederzeit unabhängig umsetzbar; OPEN-D1 bis OPEN-D4 sind erledigt.
OPEN-D6 ist unabhängig und reine Bestandsaufnahme.
```

Empfohlene Bearbeitung:

1. **Erledigt:** OPEN-D2, OPEN-D3, OPEN-D4 — Dokumentations- und CI-Hygiene.
2. **Erledigt:** OPEN-D1 — Geräteinformationen werden Ende-zu-Ende übernommen.
3. **Laufend:** OPEN-A1/A2 verfolgen und einreichen; OPEN-B3 ist der billigste
   Upstream-Beitrag mit echtem Nutzen (korrektes `UNAVAILABLE`-Mapping).
4. **Danach:** OPEN-A3, anschließend OPEN-C2 und OPEN-C3.
5. **Offen bis Telemetrie:** OPEN-C1.

## 5. Nicht-Ziele

- Kein Neuentwurf des gRPC- oder Home-Assistant-Vertrags; alle Punkte sind
  additiv oder rein interne Aufräumarbeiten.
- Keine neuen Use-Cases oder Gerätefunktionen (Cooling, Schedules,
  `hvac_action` bleiben unangetastet — das VR940 bietet sie nicht an).
- Kein String-Matching auf Upstream-Fehlertexte als Ersatz für OPEN-B3.
- Keine Wiedereinführung von Bridge-seitiger Cache-Populationsverfolgung als
  Ersatz für OPEN-B4.

## 6. Definition of Done für dieses Dokument

Das Dokument wird gelöscht, wenn alle OPEN-Punkte entweder umgesetzt oder in
den Quellspecs als bewusst verworfen dokumentiert sind. Einzelne Punkte werden
hier abgehakt, nicht in mehreren Specs parallel gepflegt; die Quellspecs
verweisen für ihren Restumfang auf dieses Dokument.
