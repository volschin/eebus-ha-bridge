# Room Heating auf eebus-go migrieren — Spec Proposal

**Datum:** 2026-07-22
**Status:** Abgeschlossen — Phase 5b (Legacy-Entfernung) umgesetzt
**Scope:** Schrittweise Ablösung der bridge-lokalen Implementierungen für
Configuration/Monitoring of Room Heating durch die bereits im gepinnten
`eebus-go`-Fork enthaltenen Upstream-PRs, ohne Änderung des bestehenden gRPC-
oder Home-Assistant-Vertrags.

## 1. Entscheidung

Die bestehende Room-Heating-Funktion wird nicht neu entworfen. Climate-Entity,
gRPC-Vertrag, Zustandsmodell und MRT-Temperaturpfad bleiben bestehen. Migriert
wird ausschließlich die Ownership der generischen EEBUS-Semantik:

1. [eebus-go #242](https://github.com/enbility/eebus-go/pull/242) (MRHSF)
   wird alleinige Quelle für den gelesenen Heizungsmodus und dessen Events.
2. [eebus-go #241](https://github.com/enbility/eebus-go/pull/241) (CRHSF)
   übernimmt danach Aushandlung, Schreibfähigkeit und Modus-Writes.
3. [eebus-go #240](https://github.com/enbility/eebus-go/pull/240) (CRHT)
   übernimmt zuletzt Sollwert-Metadaten und Sollwert-Writes. Dieser Schritt ist
   durch die noch ungeklärte `auto`-/`off`-Semantik und Lücken im öffentlichen
   Capability-Vertrag blockiert.
4. [eebus-go #239](https://github.com/enbility/eebus-go/pull/239) bleibt die
   gemeinsame Feature-Basis für HVAC und Setpoint.
5. Die bereits verwendeten MRT-/MOT-Pfade aus
   [#232](https://github.com/enbility/eebus-go/pull/232) und
   [#233](https://github.com/enbility/eebus-go/pull/233) werden nicht erneut
   implementiert.

Vorgeschaltet ist ein eigenständiger Fix zweier bereits vorhandener
Bridge-Defekte (§4.7): doppelte Mode-Typen und mehrdeutige
`roomAirTemperature`-Setpoints. Ohne ihn ist kein Phasen-Exit der Form
„identisch zu vorher“ auswertbar.

Die Migration erfolgt in getrennten, releaseweit rückrollbaren Phasen. Pro
Request gibt es niemals einen automatischen Fallback vom Upstream-Writer auf
den Legacy-Writer: Ein bereits am Gerät angekommener Befehl darf nicht über
einen zweiten Pfad wiederholt werden.

## 2. Ausgangslage

Room Heating ist bereits produktiv vorhanden:

- `MRT` liefert die Raumtemperatur über den eebus-go-Wrapper
  `TemperatureMonitoringWrapper`.
- `RoomHeatingTemperature` implementiert CRHT noch bridge-lokal auf rohen
  Setpoint-/HVAC-Caches.
- `RoomHeatingSystemFunction` implementiert CRHSF noch bridge-lokal.
- `HVACService` stellt `GetRoomHeating`, Sollwert-/Modus-Writes und den
  Room-Heating-Eventstream bereit.
- Home Assistant bildet `auto`, `on` und `off` auf `AUTO`, `HEAT` und `OFF` ab.
- Das aktuelle Protobuf adressiert genau eine ausgehandelte `HVACRoom` per SKI.

Der produktive Dependency-Satz enthält die benötigten Upstream-Beiträge bereits
über:

```text
replace github.com/enbility/eebus-go =>
        github.com/volschin/eebus-go@3c6795b4d157
```

`eebus-bridge/UPSTREAM_PATCHES.md` inventarisiert #239–#242. Die Bridge importiert
CRHT, CRHSF und MRHSF aus diesem Stand bisher jedoch nicht. Es existieren daher
zwei getestete Implementationen derselben generischen EEBUS-Semantik, während
nur die bridge-lokale produktiv verwendet wird.

### 2.1 Aktuelle fachliche Evidenz

Der VR940-Dump und die bisherigen Live-Probes belegen für `HVACRoom 5:1:1`:

- MRT, CRHT, MRHSF und CRHSF werden als Szenario 1 angeboten.
- Der Setpoint hat Scope `roomAirTemperature`, Einheit `degC`, Wert 21 °C,
  Grenzen 5–30 °C und Schrittweite 0,5 K.
- Die Modi `auto`, `on` und `off` werden angeboten.
- Derselbe Setpoint ist mit `auto` und `on` verknüpft; `off` hat keine eigene
  Setpoint-Relation.
- `setpointListData` und `hvacSystemFunctionListData` werden als schreibbar
  angekündigt.
- Der bisherige Bridge-Pfad bindet die Configuration-Features und wartet auf
  das direkte SPINE-Schreibergebnis.

Diese Daten sind Hardwareevidenz, keine zulässigen Konstanten. Entity-Adressen,
Setpoint-, SystemFunction- und Mode-IDs werden weiterhin ausschließlich aus
Descriptions und Relations aufgelöst.

### 2.2 Verifizierte Befunde im aktuellen Bridge-Code

Stand `322a9bc`. Diese Befunde sind am Code belegt und binden die Phasen-Exits;
sie sind nicht identisch mit „so soll es bleiben“:

| Ort | Verhalten heute | Bewertung |
|---|---|---|
| `internal/usecases/roomheatingsysfn.go:299-315` (`roomHeatingSystemFunctionID`) | genau eine Heating-SystemFunction, sonst `ErrRoomHeatingSysFnDataUnavailable` | bereits fail-closed; MRHSF muss dieses Niveau erreichen, nicht unterschreiten |
| `internal/usecases/hvac_cache.go:51-61` (`operationModesForSystem`) | mehrere Relation-Einträge desselben Mode-Typs erzeugen doppelte Einträge in `AvailableModes`, und `idForType` überschreibt — **es gewinnt der letzte**, nicht der erste | Defekt, siehe §4.7 |
| `internal/usecases/roomheatingtemp.go:221-233` (`roomHeatingSetpointID`) | erster Setpoint mit Scope `roomAirTemperature` gewinnt; mehrere Kandidaten werden nicht erkannt | Defekt, siehe §4.7 |
| `internal/usecases/setpoint_flow.go` (`readSetpointState`/`validateSetpointWrite`) | fehlende Value/Range/Step-Felder ⇒ `ErrRoomHeatingDataUnavailable` | fail-closed; muss erhalten bleiben (§4.1) |
| `internal/grpc/hvac_service.go:258-260` | `*OutOfRange`/`*InvalidStep`/`*InvalidMode` ⇒ `INVALID_ARGUMENT`; `*NotWritable`/`*Rejected` ⇒ `FAILED_PRECONDITION`; `*DataUnavailable` ⇒ `UNAVAILABLE` | Referenzverhalten für §7 |
| `custom_components/eebus/climate.py:74-81` | `TARGET_TEMPERATURE` an Setpoint-Writable, `TURN_ON`/`TURN_OFF` an `mode_writable && available_modes` gebunden | Capability-Regression wäre in HA sofort sichtbar |

Die Bridge importiert `usecases/ca/crht`, `usecases/ca/crhsf` und
`usecases/ma/mrhsf` bislang nicht (verifiziert über die Import-Liste in
`internal/`); importiert sind nur `ma/mrt`, `ma/mot`, `ma/mdt`, `ma/mpc`,
`ma/mdsf` und `ca/cdsf`.

## 3. Analyse der relevanten eebus-go-PRs

Stand 2026-07-22 sind alle folgenden PRs offen und gegen `enbility/eebus-go:dev`
mergebar. Die Upstream-Checks `Build` und `gosec` sind grün; formale Reviews
fehlen noch.

Die Spalte „analysierter Head“ nennt den PR-Head bei `enbility`. Sie ist **nicht**
identisch mit den in `eebus-bridge/UPSTREAM_PATCHES.md` gepinnten Cherry-picks im
Fork (#239 → `237461d19a74`, #240 → `c72bfd76e95a`, #241 → `c6415cc4b453`,
#242 → `a5640012fbd6`). Beide Listen müssen bei jedem Fork-Rebase gemeinsam
aktualisiert werden.

| PR | analysierter Head | Bedeutung | Bewertung für diese Migration |
|---|---|---|---|
| [#239 HVAC/Setpoint clients](https://github.com/enbility/eebus-go/pull/239) | `74d7622ce857` | Read-/Request-/Write-Helfer, Write-Gate sowie Partial-/Full-List-Handling | Notwendige P0-Basis. Die spätere Korrektur verhindert, dass ein unvollständiger Payload bei fehlendem `WritePartial` fremde Listeneinträge löscht. |
| [#240 CRHT](https://github.com/enbility/eebus-go/pull/240) | `88551dba4cd0` | Setpoints, Constraints und Write nach Operation Mode | Fachlich passend, aber noch kein Drop-in-Ersatz für den produktiven Bridge-Vertrag. Vor Write-Ownership sind API- und Hardware-Gates nötig. |
| [#241 CRHSF](https://github.com/enbility/eebus-go/pull/241) | `74c69ef9c56d` | verfügbare/aktuelle Modi und relation-sicherer Write | Nahe an produktionsreif. Result-Callback, Write-Gate, eindeutige Heating-SystemFunction und Full-List-Merge sind vorhanden. Capability und Refresh müssen noch vervollständigt werden. |
| [#242 MRHSF](https://github.com/enbility/eebus-go/pull/242) | `e5909c079e98` | read-only Monitoring von aktuellem und verfügbaren Modi | Richtige langfristige State-/Event-Quelle. Vor Übernahme muss die Auswahl mehrerer Heating-SystemFunctions genauso fail-closed werden wie in #241. |
| [#232 MRT](https://github.com/enbility/eebus-go/pull/232) | `5a9be2d1545a` | aktuelle Raumtemperatur | Bereits im Fork und in der Bridge produktiv; am VR940 bestätigt. Kein neuer Migrationsschritt. |
| [#233 MOT](https://github.com/enbility/eebus-go/pull/233) | `f304f83a4e0c` | Außentemperatur | Bereits integriert, aber kein Teil der Room-Heating-Ownership. |
| [#248 VHAN](https://github.com/enbility/eebus-go/pull/248) | `da0e479c8333` | Namen für HeatingCircuit, HeatingZone und HVACRoom | Optionaler Multi-Zone-Baustein. Der bekannte VR940 kündigt VHAN mit `available=false` an; deshalb kein Gate und kein Bestandteil der ersten Migration. |
| [#251 Write gate](https://github.com/enbility/eebus-go/pull/251) | `4102a3a8dd67` | konsistentes `Write()`-Gate für andere Client-Features | Gutes generisches Hardening, aber #239 enthält das Gate für HVAC/Setpoint bereits. Separat integrieren; kein Heating-Blocker. |

Zusätzlich enthält der Fork drei bereits gemergte Integrations-Härtungen, die
für Heating als Vorlage dienen und deren Heating-Äquivalente noch fehlen:
`volschin/eebus-go#1` (fail-closed List-Merges, Payload-Tests), `#2`
(strukturierte CDSF-`WriteCapabilities` plus Post-Acceptance-Refresh) und `#3`
(unabhängige, fail-closed Capability-Auflösung optionaler Szenarien). §4.2 und
§4.4 beschreiben genau die Heating-Fassung dieser drei Patches.

Die Cooling-PRs #243–#245 sind absichtlich nicht Teil dieses Scopes. Sie sind
strukturell ähnlich, aber weder durch die aktuelle Hardware noch durch das
Home-Assistant-Modell angefordert.

### 3.1 Abhängigkeits- und Merge-Reihenfolge

Empfohlene Upstream-Reihenfolge:

```text
#239 HVAC/Setpoint feature clients
        |
        +--> #242 MRHSF
        +--> #241 CRHSF
        +--> #240 CRHT (nach Klärung der offenen Vertragsfragen)

#232 MRT, #233 MOT und #248 VHAN sind davon unabhängig.
#251 ist ein separates, domänenübergreifendes Hardening.
```

Die gestapelten PRs enthalten aktuell Kopien der #239-Commits. Nach Merge von
#239 müssen #240–#242 auf `dev` neu aufgebaut werden, damit Feature-Commits
nicht mehrfach in der Review-Historie erscheinen. Der Bridge-Fork bleibt bis
dahin auf konkrete, gemeinsam getestete Cherry-picks gepinnt.

## 4. Noch offene Verträge vor vollständiger Migration

### 4.1 CRHT verliert Presence-Information und dupliziert geteilte IDs

`CRHT.Setpoints` und `SetpointConstraints` geben skalare `float64`-Felder
zurück. Fehlt im Remote-Payload beispielsweise `Value`, `SetpointRangeMin`,
`SetpointRangeMax` oder `SetpointStepSize`, ist dies öffentlich nicht von einem
echten Wert `0` unterscheidbar.

Die heutige Bridge verwirft unvollständige Metadaten fail-closed. Diese
Eigenschaft darf bei der Migration nicht verloren gehen.

Erforderliches Upstream-Follow-up, eine der beiden Varianten:

1. Presence in den öffentlichen Setpoint-Typen erhalten, beispielsweise über
   Pointer/Optional-Felder; oder
2. einen aggregierten `RoomHeatingSetpointState` liefern, der nur bei
   vollständigem Wert, Range und positiver Step-Size erfolgreich ist.

Ein Bridge-Adapter darf fehlende Werte nicht über `0`-Heuristiken erraten.

Zusätzlich sammelt #240 die Setpoint-IDs aus jeder Mode-Relation, ohne sie zu
deduplizieren. Beim VR940 referenzieren `auto` und `on` dieselbe Setpoint-ID.
`Setpoints` und `SetpointConstraints` können deshalb denselben fachlichen
Setpoint mehrfach zurückgeben. Der Upstream-State muss nach ID deduplizieren
und die Mode-Relation separat erhalten. Eine Bridge darf doppelte IDs weder als
mehrere Zonen interpretieren noch willkürlich den ersten Eintrag auswählen.

### 4.2 Schreibfähigkeit ist nicht öffentlich abfragbar

Die aktuellen CRHT-/CRHSF-APIs prüfen `Write()` und Changeability erst beim
Schreibaufruf. Home Assistant benötigt die Entscheidung vorher für
`TARGET_TEMPERATURE`, `TURN_ON` und `TURN_OFF`.

Benötigte öffentliche, strukturierte Capability-APIs:

```go
type RoomHeatingTemperatureWriteCapabilities struct {
    Setpoint bool
}

type RoomHeatingSystemFunctionWriteCapabilities struct {
    OperationMode bool
}
```

`Setpoint` ist nur wahr, wenn CRHT ausgehandelt ist, genau ein für das
Produktmodell nutzbarer Setpoint vollständig aufgelöst ist,
`SetpointListData.Write()` gilt und der Setpoint nicht explizit unveränderbar
ist. `OperationMode` folgt analog aus CRHSF-Szenario, eindeutiger
Heating-SystemFunction, Relation, `HvacSystemFunctionListData.Write()` und
`IsOperationModeIdChangeable != false`.

Bis diese API existiert, darf vorübergehend ein read-only Capability-Inspector
in der Bridge verbleiben. Er sendet keine Befehle und wird nach dem Muster der
abgeschlossenen CDSF-Migration wieder entfernt.

#### 4.2.1 Bekannter CDSF-Fehler, der hier nicht wiederholt werden darf

`cdsf.CDSF.WriteCapabilities` liefert laut „Known limitations“ in
`eebus-bridge/UPSTREAM_PATCHES.md` bei fehlenden, mehrdeutigen *und* bei
schlicht noch nicht befüllten Caches identisch `zero capabilities, nil error`.
Ergebnis in Produktion: Ein DHW-Write im Fenster direkt nach Connect meldet
`FAILED_PRECONDITION` („nicht schreibbar“) statt vormals `UNAVAILABLE`
(„später erneut“) — eine bewusst akzeptierte Regression ohne bridge-seitigen
Fix.

Die Heating-`WriteCapabilities` müssen diese drei Fälle unterscheidbar machen,
bevor Phase 2 den Capability-Owner wechselt. Verbindliche Zielform:

```go
// Fehler statt stiller Nullwerte, solange nichts entschieden werden kann.
// ErrDataNotAvailable  -> Use Case/Cache noch nicht befüllt   -> UNAVAILABLE
// Capabilities{false}  -> ausgehandelt, Gerät erlaubt es nicht -> FAILED_PRECONDITION
WriteCapabilities(spineapi.EntityRemoteInterface) (
    ucapi.RoomHeatingSystemFunctionWriteCapabilities, error,
)
```

Mehrdeutige Metadaten (mehrere Heating-SystemFunctions, mehrere Setpoint-
Kandidaten) sind ein Fehler, keine „false“-Capability. Wird diese Trennung
upstream nicht erreicht, bleibt der Bridge-Inspector Capability-Owner und
Phase 2 gilt als nicht erreicht — ein Übernehmen der CDSF-Semantik wäre eine
wissentliche Wiederholung derselben Regression.

### 4.3 CRHT-API und bestehender SKI-basierter gRPC-Write passen nicht zusammen

Der bestehende RPC erhält nur `ski` und `value_celsius`. `CRHT.WriteSetpoint`
verlangt dagegen zusätzlich einen Operation Mode und lehnt `auto` grundsätzlich
ab. Für den beobachteten VR940 gilt gleichzeitig:

- `auto` und `on` referenzieren denselben Setpoint;
- `off` besitzt keine Setpoint-Relation;
- die heutige Bridge kann denselben `roomAirTemperature`-Setpoint unabhängig
  vom aktuellen Modus ändern;
- Home Assistant bietet die Solltemperatur derzeit auch in `auto` und `off` an.

Die Bridge darf dies nicht durch den versteckten Trick umgehen, bei `auto` oder
`off` einfach den Modus `on` an eebus-go zu übergeben. Das wäre ein
geräteabhängiger Zusammenhang außerhalb des öffentlichen Vertrags.

Vor Phase 5 ist zu entscheiden und per Hardwaretest zu belegen:

1. Ist ein CRHT-Write im Modus `auto` gemäß Use-Case-Spezifikation tatsächlich
   unzulässig, obwohl der Setpoint geteilt wird?
2. Ändert ein Setpoint-Write während `auto` den laufenden Sollwert, eine
   Basistemperatur oder nur den späteren manuellen Wert?
3. Soll ein Write während `off` den gespeicherten `on`-Setpoint ändern dürfen?

Konservative Produktregel bis zur Klärung: Der bestehende CRHT-Writer bleibt
Owner. Wird die Upstream-Regel bestätigt, muss HA `TARGET_TEMPERATURE`
modusabhängig ausblenden beziehungsweise Writes in `auto`/`off` als
`FAILED_PRECONDITION` ablehnen. Wird das aktuelle Geräteverhalten als zulässige
CRHT-Semantik bestätigt, braucht eebus-go eine explizite, relation-sichere API
für den adressierten Setpoint statt eines falschen Mode-Alias.

### 4.4 Post-Write-Konvergenz fehlt

#240 und #241 liefern Message Counter und `ResultData`-Callback, fordern nach
einem akzeptierten Resultat aber nicht garantiert die betroffene Setpoint- oder
HVAC-Liste neu an. Ein erfolgreiches Resultat allein aktualisiert weder sicher
den eebus-go-Cache noch Home Assistant.

Wie bei CDSF muss eebus-go vor Ausführung des Bridge-Callbacks die betroffene
Liste erneut anfordern oder eine gleichwertige frische Notification
garantieren:

- CRHT: `SetpointListData`
- CRHSF: `HvacSystemFunctionListData`

Die Bridge wartet weiterhin contextgebunden auf das Resultat, führt aber nach
dem Ownership-Wechsel keine zweite rohe SPINE-Refresh-Implementierung.

### 4.5 SystemFunction-, Mode- und Setpoint-Auflösung muss fail-closed sein

CRHSF verlangt bereits genau eine Heating-SystemFunction. MRHSF verwendet
aktuell den ersten Treffer. Monitoring und Configuration könnten dadurch bei
einem Gerät mit mehreren Heating-SystemFunctions unterschiedliche Einträge
anzeigen beziehungsweise steuern. Außerdem lösen die aktuellen CRHT-/CRHSF-
Writes mehrere verschiedene Mode-IDs mit demselben Mode-Typ nicht eindeutig auf.

#242 muss ebenfalls `ErrDataNotAvailable` liefern, wenn nicht exakt eine
passende Heating-SystemFunction auf der ausgehandelten `HVACRoom` existiert.
Ein Write darf einen Mode-Typ nur dann auflösen, wenn innerhalb der Relation
genau eine semantisch eindeutige Mode-ID übrig bleibt. Geteilte identische
Setpoint-IDs werden dedupliziert; mehrere verschiedene Kandidaten bleiben ein
Fehler. Operation Modes werden im Bridge-Facade zusätzlich dedupliziert;
unbekannte Mode-Typen bleiben protokollnah erhalten und werden erst in HA
gefiltert.

### 4.6 Fehler bei verschwundenen Features brauchen Sentinels

`features/client.NewFeature` liefert bei einem Disconnect-Rennen noch einfache
Textfehler wie `local feature not found` und `remote feature not found`. Ohne
exportierte Sentinel-Fehler kann die Bridge diese nicht stabil als temporär
`UNAVAILABLE` klassifizieren.

Dieses Follow-up gilt für alle eebus-go-Use-Cases, wird für Heating aber Teil
der Exit-Kriterien, weil Reconnects während eines Climate-Writes realistisch
sind. String-Matching in der Bridge ist ausgeschlossen.

Der Punkt ist bereits als „Known limitation“ in `UPSTREAM_PATCHES.md` für die
DHW-Writer (`mapUpstreamDHWWriteError`) dokumentiert: Solche Fehler landen dort
heute in `codes.Internal` statt `codes.Unavailable`. Heating erbt diesen Defekt
mit dem ersten Upstream-Write. Ein gemeinsames Upstream-Sentinel-PR löst beide
Domänen zugleich und ist deshalb vor Phase 3 einzureichen.

### 4.7 Bestehende Bridge-Defekte, die vor dem Ownership-Wechsel zu klären sind

Zwei Defekte existieren bereits im lokalen Pfad (§2.2). Sie sind kein Ergebnis
der Migration, dürfen aber weder mitgeschleppt noch stillschweigend durch
abweichendes Upstream-Verhalten „gefixt“ werden — sonst ist der
Phasen-Exit „Verhalten identisch zu vorher“ nicht auswertbar.

1. **Doppelte Mode-Typen (`hvac_cache.go:51-61`).** Enthält die Relation zwei
   IDs mit demselben `OperationModeType`, erscheint der Typ doppelt in
   `AvailableModes` und `idForType` behält die **zuletzt** gesehene ID. Ein
   Mode-Write trifft damit eine nicht determinierte ID. Zielverhalten:
   `AvailableModes` wird nach Typ dedupliziert; bleibt für einen angeforderten
   Typ mehr als eine ID übrig, ist der Write `ErrRoomHeatingSysFnInvalidMode`
   statt einer Zufallsauswahl. Der bereits gemergte DHW-Pfad
   (`dhwsysfn_upstream_mode.go:37-45`) delegiert die ID-Auflösung an Upstream und
   dokumentiert dieselbe Einschränkung — Heating muss dieselbe Regel treffen.
2. **Erster `roomAirTemperature`-Setpoint gewinnt (`roomheatingtemp.go:221-233`).**
   Zielverhalten: genau ein vollständig auflösbarer Kandidat, sonst
   `ErrRoomHeatingDataUnavailable`.

Beide Korrekturen gehören in **Phase 0** und in den lokalen Pfad, damit die
Charakterisierungstests aus Phase 0 die *gewollte* Semantik festschreiben und
die späteren Exits auf Gleichheit prüfen können. Am beobachteten VR940 ändert
das nichts (ein Setpoint, disjunkte Mode-Typen); es ist deshalb ein risikoarmer,
eigenständig releasebarer Vorabfix.

## 5. Zielarchitektur

```text
eebus-go MRT              eebus-go MRHSF        eebus-go CRHSF
Raum-Isttemperatur        Modus + State-Events  Capability + Mode-Write
        |                         |                       |
        |                         +-----------+-----------+
        |                                     |
        |                         RoomHeatingSystemFunctionAdapter
        |                                     |
        +------------------+------------------+
                           |
                    eebus-go CRHT
                    Setpoint + Constraints + Write
                           |
                           v
                  bestehender HVACService
                           |
                  unverändertes Protobuf
                           |
                  bestehende HA Climate-Entity
```

### 5.1 Read- und Event-Ownership

- MRT bleibt alleinige Quelle von `current_temperature_celsius`.
- MRHSF wird alleinige Quelle von aktuellem Modus, verfügbaren Modi und
  SystemFunction-State-Events.
- CRHSF publiziert keine zweite Benutzerzustandsquelle. Es liefert nur
  Configuration-Support, Capability und Writes.
- CRHT bleibt Quelle für Zielwert, Constraints und deren Events, weil kein
  separater Monitoring-Use-Case für diesen Configuration-State vorgesehen ist.
- Support-Events lösen weiterhin eine vollständige Reconciliation aus.

### 5.2 Getrennte Entity-Auflösung

MRHSF-, CRHSF-, CRHT- und MRT-Entity werden jeweils über ihre eigenen
`RemoteEntitiesScenarios()` und den normalisierten Geräte-SKI aufgelöst.

Auch wenn der VR940 alle vier Use Cases auf derselben `HVACRoom` anbietet, darf
kein Monitoring-Entity-Objekt direkt an einen Configuration-Client
weitergereicht werden. Nach Reconnect können die Referenzen unabhängig ersetzt
worden sein.

### 5.3 Schmale, mockbare Facades

Die Bridge hängt nicht direkt überall von den konkreten Typen ab. Analog zur
abgeschlossenen DHW-CDSF-Migration werden schmale Interfaces eingeführt. Die
konkreten Vorlagen im Repo sind `internal/usecases/dhwsysfn_adapter.go`
(Monitoring-State + Configuration-Capability zu einem Facade-State komponiert),
`dhwsysfn_configuration.go` (Client-Interface, Capability-Inspector,
`awaitDHWWrite`) und `dhwsysfn_upstream_mode.go` (Vorvalidierung, Write,
Fehlerabbildung):

```go
type maMRHSFClient interface {
    eebusapi.UseCaseInterface
    RemoteEntitiesScenarios() []eebusapi.RemoteEntityScenarios
    OperationModes(spineapi.EntityRemoteInterface) ([]ucapi.HvacOperationModeType, error)
    CurrentOperationMode(spineapi.EntityRemoteInterface) (ucapi.HvacOperationModeType, error)
}

type caCRHSFClient interface {
    eebusapi.UseCaseInterface
    RemoteEntitiesScenarios() []eebusapi.RemoteEntityScenarios
    WriteCapabilities(spineapi.EntityRemoteInterface) (ucapi.RoomHeatingSystemFunctionWriteCapabilities, error)
    WriteOperationMode(
        spineapi.EntityRemoteInterface,
        ucapi.HvacOperationModeType,
        func(model.ResultDataType, model.MsgCounterType),
    ) (*model.MsgCounterType, error)
}

type caCRHTClient interface {
    eebusapi.UseCaseInterface
    RemoteEntitiesScenarios() []eebusapi.RemoteEntityScenarios
    State(spineapi.EntityRemoteInterface) (ucapi.RoomHeatingSetpointState, error)
    WriteCapabilities(spineapi.EntityRemoteInterface) (ucapi.RoomHeatingTemperatureWriteCapabilities, error)
    // endgültige Signatur erst nach Klärung von auto/off
}
```

Die vorgeschlagenen `State`-/`WriteCapabilities`-Methoden sind Ziel-APIs und
existieren in #240/#241 noch nicht vollständig.

### 5.4 Context- und Result-Adapter

Die asynchronen eebus-go-Callbacks werden in der Bridge in den bestehenden
synchronen gRPC-Vertrag übersetzt. Ein kleiner `awaitRoomHeatingWrite`-Helper:

- verwendet einen gepufferten Result-Channel;
- akzeptiert Callbacks vor und nach Rückkehr der Write-Methode;
- verlangt einen nichtleeren Message Counter;
- gleicht den Callback-Counter ab;
- behandelt `ErrorNumber == nil || 0` als Erfolg;
- bildet Geräteablehnung inklusive Description ab;
- respektiert Caller-Cancellation/-Deadline;
- besitzt einen begrenzten internen Timeout.

Der Helper darf später mit `awaitDHWWrite` vereinheitlicht werden, wenn dadurch
keine domänenspezifischen Fehlerklassen vermischt werden.

## 6. Unveränderte öffentliche Verträge

Die Migration benötigt keine Proto-Regeneration und keine Änderung an Home
Assistant.

Unverändert bleiben:

- `HVACService.GetRoomHeating`
- `SetRoomHeatingTemperature(ski, value_celsius)`
- `SetRoomHeatingMode(ski, mode)`
- `SubscribeRoomHeatingEvents`
- `RoomHeatingState`, `RoomHeatingSetpoint` und
  `RoomHeatingSystemFunction`
- HA Unique ID `${ski}_room_heating`
- Mapping `auto/on/off` zu `AUTO/HEAT/OFF`
- Coordinator-Felder und bestehende Snapshot-/Consolidated-Stream-Verträge

Eine spätere Multi-Zone-Unterstützung ist eine eigene API-Versionierung: Dafür
wären Entity-/Zone-Identifier in Requests, State und Unique IDs nötig. VHAN
#248 allein rechtfertigt diese Änderung nicht.

## 7. Fehler- und Capability-Mapping

| Ursache | Bridge-Fehler | gRPC-Code |
|---|---|---|
| Temperatur nicht endlich, außerhalb Range oder Step-Raster | `ErrRoomHeatingOutOfRange` / `ErrRoomHeatingInvalidStep` | `INVALID_ARGUMENT` |
| angeforderter Modus nicht in MRHSF-/CRHSF-Relation | `ErrRoomHeatingSysFnInvalidMode` | `INVALID_ARGUMENT` |
| Use Case/Szenario nicht ausgehandelt, `Write()` fehlt oder Changeability explizit false | `ErrRoomHeatingNotWritable` / `ErrRoomHeatingSysFnNotWritable` | `FAILED_PRECONDITION` |
| Gerät liefert non-zero `ResultData.ErrorNumber` | `ErrRoomHeatingRejected` / `ErrRoomHeatingSysFnRejected` | `FAILED_PRECONDITION` |
| Entity, Presence, Relation, Constraints oder Counter fehlen/mehrdeutig | bestehende Data-Unavailable-Sentinels | `UNAVAILABLE` beziehungsweise `NOT_FOUND` bei fehlender Entity |
| Capability upstream noch nicht befüllt (kein Negotiation-Ergebnis) | `ErrRoomHeating*DataUnavailable` | `UNAVAILABLE`, ausdrücklich **nicht** `FAILED_PRECONDITION` (§4.2.1) |
| Feature-Binding während des Writes verschwunden (Disconnect-Race) | Upstream-Sentinel (§4.6) | `UNAVAILABLE`; ohne Sentinel heute `Internal` — Exit-Kriterium |
| Context abgebrochen/Deadline überschritten | `context.Canceled` / `context.DeadlineExceeded` | bestehendes Context-Mapping |

`api.ErrNotSupported` ist ohne Vorvalidierung mehrdeutig. Deshalb validiert der
Facade Modi und Capabilities vor dem Write. Danach bedeutet
`ErrNotSupported` „nicht schreibbar“, nicht „ungültige Benutzereingabe“.

## 8. Inkrementelle Umsetzung

### Phase 0 — Bestehendes Verhalten charakterisieren

Noch kein Ownership-Wechsel.

Arbeiten:

1. Contract-Tests für den aktuellen lokalen CRHT-/CRHSF-State ergänzen.
2. Aktuelle Result-, Timeout-, Range-, Step-, Relation- und Reconnect-Semantik
   als Invarianten festschreiben.
3. Hardwareseitig `auto`/`on`/`off` und Setpoint-Verhalten erfassen, jeweils mit
   Restore auf den Ausgangswert.
4. Prüfen, ob Upstream-Configuration ohne Feature-Binding am VR940 schreiben
   kann; #240/#241 subscriben, der heutige lokale Pfad bindet zusätzlich.
5. Die beiden Defekte aus §4.7 im lokalen Pfad beheben (Mode-Dedup mit
   fail-closed Mehrdeutigkeit, eindeutiger `roomAirTemperature`-Setpoint) und
   mit Tabellentests belegen.

Exit:

- Ein reproduzierbarer Capture dokumentiert Entity, Firmware, Ausgangswerte,
  Write-Result und Read-back.
- Kein öffentlicher Vertrag wurde geändert.
- Die §4.7-Semantik ist als Invariante getestet und dient allen folgenden
  „identisch zu vorher“-Exits als Referenz.

Umsetzungsstand 2026-07-22:

- [x] §4.7 lokal gehärtet: Mode-Typen werden dedupliziert, verschiedene IDs
  desselben Typs sind nicht schreibbar, und mehrere verschiedene
  `roomAirTemperature`-Setpoints liefern Data-Unavailable.
- [x] Automatisierte Contract-Tests für State/Presence, Range, Step, Relation,
  Result/Reject, Cancellation, internen Timeout, Reconnect-Refresh und das
  bestehende gRPC-Fehlermapping ergänzt.
- [x] Reproduzierbaren VR940-Capture mit Ausgangswerten, `auto`/`on`/`off`,
  Setpoint-Read-back und Restore durchgeführt (2026-07-22, Stack 93,
  `climate.eebus_682f708c_raumheizung`): Baseline `off`/21,0 °C
  (Range 5–30 °C, Step 0,5 °C, `hvac_modes` = `auto`/`heat`/`off`);
  Transitions `auto` → `heat` → `off` → `auto` je mit Read-back bestätigt;
  Setpoint 21,0 → 21,5 °C geschrieben und zurückgelesen; Baseline
  (`off`/21,0 °C) wiederhergestellt. Firmware/Modell konnten nicht erfasst
  werden — das HA-Device-Registry-Objekt liefert `manufacturer`, `model`,
  `sw_version` und `hw_version` als `None` (separat zu klären, nicht Teil
  dieser Phase).
- [x] Upstream-CRHT/CRHSF-Write ohne zusätzliches Feature-Binding am VR940
  geprüft. Die Hardwarematrizen aus Phase 3 (`:crhsf-phase3`) und Phase 5
  (`:crht-phase5`) belegen erfolgreiche Mode- und Setpoint-Writes über die
  Upstream-Writer; ein zusätzliches Bridge-seitiges Feature-Binding war dabei
  nicht registriert.

### Phase 1 — MRHSF übernimmt Reads und State-Events

Arbeiten:

1. #242 auf eindeutige Heating-SystemFunction härten.
2. `RoomHeatingSystemFunctionMonitoring` um `mrhsf.NewMRHSF` aufbauen.
3. Einen Adapter aus MRHSF-State und der bestehenden lokalen CRHSF-
   Configuration einführen.
4. MRHSF als eigenen Use Case registrieren.
5. Den lokalen CRHSF-EventBus deaktivieren; er bleibt vorübergehend nur
   Configuration-/Write-Owner.
6. Event-Mapping:
   `DataUpdateOperationMode` → `room_heating.system_function_updated`,
   `UseCaseSupportUpdate` → bestehendes Support-Event.

Exit:

- Mode und Mode-Liste sind vor/nach der Migration identisch.
- Pro Remote-Update entsteht genau ein Benutzerzustands-Event.
- Fresh start, drei Reconnects und drei Bridge-Restarts repopulieren den State.
- Modus-Writes laufen noch ausschließlich über den Legacy-Writer.

Umsetzungsstand 2026-07-22:

- [x] #242 auf eindeutige Heating-SystemFunction gehärtet: Fork-PR
  `volschin/eebus-go#4` verlangt genau einen Treffer und ist über den
  `bridge-integration`-Commit `b40877d34a63` gepinnt.
- [x] `RoomHeatingSystemFunctionMonitoring` um `mrhsf.NewMRHSF` eingeführt;
  Operation Mode und deduplizierte Mode-Liste kommen aus MRHSF.
- [x] `RoomHeatingSystemFunctionAdapter` komponiert MRHSF-Reads mit
  Legacy-CRHSF-Writeability und -Writes über separat per SKI aufgelöste
  Entities.
- [x] MRHSF und Legacy-CRHSF separat registriert; der Legacy-CRHSF-EventBus ist
  deaktiviert, sodass nur MRHSF SystemFunction-State-Events publiziert.
- [x] Unit-, Composition-, vollständige Go-, Vet- und Race-Suite grün.
- [x] Fresh Start, drei Reconnects und drei Bridge-Restarts auf Zielhardware
  geprüft; Mode- und Event-Gleichheit im Hardware-Capture bestätigt.

Hardware-Capture 2026-07-22 (VR940 `682F708C`, Stack 93, Dev-Image
`ghcr.io/volschin/eebus-bridge:mrhsf-phase1`):

- Fresh Start: `Registered EEBUS use cases: … RoomHeatingTemperature, MRHSF,
  RoomHeatingSystemFunctionConfiguration, …`; Discovery meldet
  `monitoringOfRoomHeatingSystemFunction v1.0.0 available=true scenarios=[1]`
  neben `configurationOfRoomHeatingSystemFunction`.
- Mode-Gleichheit: `hvac_modes` bleibt `['auto','heat','off']`, identisch zum
  Legacy-Capture aus Phase 0; Setpoint-Bereich 5–30 °C, Schritt 0,5 °C.
- Mode-Transitions über MRHSF-Reads plus Legacy-Writer: `off→auto→heat→off`
  jeweils mit Readback bestätigt; nach den Restarts erneut `off→auto→off`.
- Events: nur MRHSF publiziert SystemFunction-Events
  (`ma-mrhsf-UseCaseSupportUpdate`, `ma-mrhsf-DataUpdateOperationMode`); pro
  Remote-Update genau ein Event, keine Legacy-CRHSF-Events, keine Fehler im
  Log.
- Drei Bridge-Restarts (ein Stack-Redeploy, zwei Container-Restarts) inklusive
  der zugehörigen SHIP-Reconnects repopulieren Mode, Mode-Liste und Setpoint
  vollständig; Writes funktionieren nach den Restarts unverändert.
- Setpoint-Round-Trip 21,0 → 21,5 → 21,0 °C erfolgreich. Zwei Setpoint-Writes
  in kurzer Folge (< 15 s) verwirft das Gerät gelegentlich; der Wiederholungs-
  Write greift. Betrifft den unveränderten `RoomHeatingTemperature`-Pfad, nicht
  MRHSF.
- Baseline wiederhergestellt (`off`, 21,0 °C); Stack 93 zurück auf `:latest`.

### Phase 2 — Upstream CRHSF übernimmt Negotiation und Capability

Arbeiten:

1. Öffentliche CRHSF-`WriteCapabilities` ergänzen oder vorübergehend einen
   read-only Bridge-Inspector verwenden.
2. `CRHSFConfigurationFacade` einführen und `crhsf.NewCRHSF` registrieren.
3. Nicht gleichzeitig lokalen und Upstream-CRHSF als denselben Use Case
   registrieren.
4. MRHSF-Entity und CRHSF-Entity explizit per SKI komponieren.
5. Den alten Writer als releaseweit ausgewählte Strategie behalten; keine
   Request-Fallbacks.

Exit:

- `mode_writable` ist konservativ und stimmt mit dem tatsächlichen Write-Pfad
  überein.
- Upstream CRHSF füllt nach Start/Reconnect alle benötigten Caches.
- Der Legacy-Writer arbeitet mit dem von CRHSF installierten HVAC-Client weiter.

Umsetzungsstand 2026-07-22:

- [x] `CRHSFConfigurationFacade` eingeführt und `crhsf.NewCRHSF` als alleinigen
  Owner für Negotiation, HVAC-Client-Feature und Cache-Population registriert;
  der lokale CRHSF-Use-Case wird nicht parallel registriert.
- [x] Temporären read-only Bridge-Inspector beibehalten, weil der gepinnte
  CRHSF noch keine öffentliche fail-closed `WriteCapabilities`-API anbietet.
  Unvollständige Caches bleiben `UNAVAILABLE`, ein ausgehandeltes read-only
  Gerät liefert dagegen konservativ `mode_writable=false`.
- [x] Den Legacy-Writer als releaseweit einzige Write-Strategie ohne eigenen
  `UseCaseBase`, Event-Subscriber oder Request-Fallback extrahiert. MRHSF bleibt
  alleiniger Owner von Reads und benutzersichtbaren State-Events.
- [x] MRHSF- und CRHSF-Entity werden weiterhin getrennt per normalisiertem SKI
  aufgelöst und erst im bestehenden Adapter komponiert.
- [x] Focused Unit- und Composition-Root-Tests für Use-Case-Auswahl,
  Entity-Auflösung, Capability-Trennung und Writer-Delegation ergänzt.
- [x] Auf Zielhardware verifiziert (VR940, SKI `682f708c…`, Stack 93,
  Image `crhsf-phase2`, 2026-07-22): Nach Fresh Start füllt Upstream CRHSF alle
  Caches (`hvac_modes=[auto, heat, off]`, Setpoint 21.0 °C); der Legacy-Writer
  schreibt und liest `auto`/`heat`/`off` sowie Setpoints 21.5/21.0 °C korrekt
  zurück. Drei Bridge-Restarts reproduzieren Modi und Setpoint unverändert,
  ein Write nach dem letzten Restart bleibt erfolgreich; keine
  `ROOMHEATINGSYSFN`-Fehler oder Rejects im Log. Stack danach auf `:latest`
  zurückgesetzt.

### Phase 3 — Upstream CRHSF übernimmt Mode-Writes

Arbeiten:

1. Angeforderten Mode gegen die MRHSF-/CRHSF-Relation vorvalidieren.
2. `CRHSF.WriteOperationMode` aufrufen und Result contextgebunden abwarten.
3. Post-Result-Refresh durch eebus-go sicherstellen.
4. Die bridge-lokale Mode-ID-, Relation-, List-Merge- und Write-Logik bleibt in
   diesem PR unverändert im Baum, nur nicht mehr verdrahtet. Ihre Löschung ist
   ein eigener PR **nach** bestandener Hardwarematrix (§10, „Der alte Writer
   wird erst nach Hardware-Abnahme entfernt“) — andernfalls wäre der unten
   genannte Rollback im Folgerelease nicht möglich.

Exit:

- `auto`, `on` und `off` werden jeweils geschrieben und zurückgelesen.
- Mindestens zehn Übergänge, darunter jeder angebotene Modus, sind erfolgreich.
- Unrelated/ambiguous Modes, read-only und Geräteablehnung behalten ihre
  bisherigen Fehlerklassen.
- Drei Reconnect-/Restart-Zyklen bleiben schreibfähig.

Rollback: In einem Folgerelease wird die Legacy-Strategie wieder ausgewählt.
Ein fehlgeschlagener Upstream-Write wird niemals im selben Request wiederholt.

Umsetzungsstand 2026-07-22:

- [x] Bridge-seitigen CRHSF-Writer eingeführt und als releaseweit einzige
  Mode-Write-Strategie ausgewählt; der Legacy-Writer bleibt unverändert im
  Baum und es existiert kein Request-Fallback.
- [x] Angeforderte Modi werden vor dem Senden sowohl gegen die MRHSF- als auch
  gegen die CRHSF-Modusliste geprüft. Abweichende oder fremde Modi senden
  keinen Write.
- [x] CRHSF-Result wird anhand des Message Counters context- und
  timeoutgebunden abgewartet; Geräteablehnung, read-only, Data-Unavailable und
  Context-Fehler bleiben auf die bestehenden Bridge-Sentinels abgebildet.
- [x] Unit-, Composition- und vollständige Go-Suite für den Bridge-Teil grün.
- [ ] CRHSF upstream auf eindeutige Mode-ID-Auflösung und Post-Result-Refresh
  härten. Der aktuelle Pin `930469d6dd8e` wählt
  bei mehreren IDs desselben Mode-Typs noch den ersten Treffer und refreshed
  nach einem akzeptierten Write nicht explizit.
- [ ] Den gemeinsamen `features/client.NewFeature`-Sentinel aus §4.6 upstream
  einreichen und im Bridge-Mapping übernehmen; der aktuelle Pin liefert im
  Disconnect-Rennen weiterhin nicht klassifizierbare Textfehler.
- [x] Hardwarematrix am VR940 (SKI `682f708c…`, Stack 93, Image
  `crhsf-phase3`) am 2026-07-22 durchgeführt:
  - Frischstart füllt Caches ohne Legacy-Writer: `hvac_modes=[auto, heat, off]`,
    Setpoint 21.0 °C.
  - 18 Mode-Übergänge über alle drei angebotenen Modi, alle vom Gerät
    übernommen; keine Ablehnung, kein Fehler im Bridge-Log.
  - Setpoint-Write (Legacy-Pfad, unverändert) 21.0 → 21.5 → 21.0 erfolgreich.
  - Drei Container-Restarts: Modi, Setpoint und Schreibfähigkeit identisch
    reproduziert; Post-Restart-Write erfolgreich.
  - Konvergenz nach akzeptiertem Write < 0,1 s (Geräte-Notify), obwohl der
    Upstream-Writer keinen expliziten Post-Result-Refresh sendet. Einmalig
    zeigte ein Sample bei sehr schneller Write-Folge (6 s Abstand) noch den
    Vorzustand; der Zustand konvergierte anschließend korrekt.
  - Stack nach dem Test auf `:latest` zurückgesetzt, Ausgangszustand
    (`auto`, 21.0 °C) wiederhergestellt.

### Phase 4 — Upstream CRHT übernimmt Negotiation und Reads

Arbeiten:

1. Presence-erhaltenden CRHT-State upstream bereitstellen.
2. `CRHTConfigurationFacade` einführen und `crht.NewCRHT` registrieren.
3. Den produktiven Setpoint aus vollständigem Value/Constraint-State
   deterministisch wählen; bei mehreren aktiven oder mehreren gleichwertigen
   Kandidaten fail-closed bleiben.
4. CRHT-Events auf bestehende Setpoint-/Support-Events abbilden.
5. Den extrahierten Legacy-Writer vorübergehend beibehalten.

Exit:

- Wert, Minimum, Maximum, Step und Writable-State sind identisch zur lokalen
  Implementierung *in der nach §4.7 korrigierten Fassung*. Für den VR940
  (genau ein `roomAirTemperature`-Setpoint) ist das wörtlich identisch; ein
  Gerät mit mehreren Kandidaten wird beidseitig unavailable statt
  erstbester Treffer.
- Fehlende einzelne Remote-Felder machen den Setpoint unavailable und werden
  nicht zu Nullwerten.
- Constraint- und Value-Events konvergieren ohne doppelte Publikation.

Umsetzungsstand 2026-07-22:

- [x] Presence-sicheren CRHT-Aggregatzustand im Fork ergänzt: `State` liefert
  Wert, Minimum, Maximum, Step und Write-Operation nur bei vollständig
  vorhandenen, validen Remote-Feldern. Geteilte IDs werden dedupliziert;
  mehrere verschiedene `roomAirTemperature`-Kandidaten bleiben fail-closed.
  Fork-PR `volschin/eebus-go#5` ist im aktuellen Pin `3c6795b4d157` enthalten.
- [x] `CRHTConfigurationFacade` eingeführt und `crht.NewCRHT` als alleinigen
  Owner für Negotiation, Cache-Population und Reads registriert.
- [x] CRHT-Value-/Constraint-Events auf das bestehende Setpoint-Event und
  Support-Updates auf das bestehende Support-Event abgebildet. Partielle
  Zustände publizieren kein zwischenzeitliches Null-/Teil-Snapshot.
- [x] Der extrahierte Legacy-Writer bleibt bis Phase 5 der einzige Writer und
  besitzt keinen eigenen Use Case oder Event-Subscriber mehr.
- [x] Unit-, Composition-, vollständige Go-, Vet-, Race-, Integrations- und
  Go/Python-Contract-Suite grün; Patch-Coverage 93,4 %.
- [x] Hardwarematrix am VR940 (SKI `682f708c…`, Stack 93, Image
  `:crht-phase4`) abgeschlossen:
  - Fresh Start: CRHT registriert (`Registered EEBUS use cases: … CRHT …`),
    vollständige Setpoint-Metadaten in Home Assistant (`temperature=21.0`,
    `min_temp=5.0`, `max_temp=30.0`, `target_temp_step=0.5`) — kein
    Fail-closed, kein Teil-Snapshot.
  - Setpoint-Writes über den Legacy-Writer: 21.0 → 21.5 → 22.0 → 21.0,
    jeweils Round-Trip-Bestätigung innerhalb von 8 s.
  - Modus-Writes (CRHSF, Phase 3) unverändert funktionsfähig: `heat` → `auto`
    ohne Rückwirkung auf den Setpoint.
  - Restart: 3 Zyklen; nach jedem Neustart erneut vollständige Metadaten und
    ein erfolgreicher Write (21.5 → 21.0).
  - Bridge-Logs über den gesamten Lauf ohne Error-Einträge.
  - Stack anschließend auf `:latest` zurückgesetzt, Baseline `auto` / 21.0 °C
    wiederhergestellt.

### Phase 5 — Upstream CRHT übernimmt Setpoint-Writes

Voraussetzungen:

- `auto`-/`off`-Semantik ist anhand Spezifikation und Hardware entschieden.
- Die eebus-go-API kann den daraus resultierenden Write explizit ausdrücken.
- Write-Capability, Range/Step-Validierung, Result und Refresh sind testbar.

Arbeiten:

1. Wert in der Bridge weiterhin auf finite/range/step validieren.
2. Upstream-Write ohne Mode-Alias ausführen.
3. Result contextgebunden abwarten; eebus-go refreshed danach die Liste.
4. Bridge-lokale Setpoint-ID-, Constraint-, Full-List- und Write-Logik
   abklemmen; Löschung wie in Phase 3 erst im Folge-PR nach Hardware-Abnahme.

Exit:

- Sollwertänderungen in jedem als schreibbar angebotenen HA-Modus sind
  spezifikationskonform getestet.
- Zehn Writes mit Read-back und Wiederherstellung sind erfolgreich.
- Out-of-range, falscher Step, read-only, non-zero Result, Timeout und
  Disconnect behalten ihre Fehlercodes.

Umsetzungsstand 2026-07-22:

- [x] Mode-unabhängige, relation-sichere CRHT-API
  `WriteRoomAirTemperatureSetpoint` im Fork ergänzt. Sie adressiert genau den
  von `State` eindeutig ausgewählten `roomAirTemperature`-Setpoint und benötigt
  weder für `auto` noch für `off` einen falschen Mode-Alias. Fork-PR
  `volschin/eebus-go#6` ist über `3c6795b4d157` gepinnt.
- [x] Upstream prüft Write-Operation, Changeability, finite Range und Step,
  erhält bei Full-List-Writes fremde Setpoints und fordert nach akzeptiertem
  Result die Setpoint-Liste vor dem Bridge-Callback erneut an.
- [x] Die Bridge validiert Wert, Range und Step weiterhin selbst, wartet
  Message-Counter und Result contextgebunden ab und bildet read-only,
  Data-Unavailable, Geräteablehnung, Cancellation, Timeout sowie Disconnect
  auf die bestehenden Sentinels ab. Es existiert kein Legacy-Fallback im
  Request.
- [x] Focused-, vollständige-, Vet- und Race-Suite für Fork und Bridge grün.
- [x] Hardwarematrix am VR940 (Stack 93, Image `:crht-phase5`) durchgeführt:
  zehn Writes mit Read-back 10/10 erfolgreich (Konvergenz 0,1–10 s), Writes in
  `off`, `heat` und `auto` ohne Mode-Alias akzeptiert, Out-of-range (40 °C) und
  Step-Verletzung (21,25 °C) abgelehnt ohne Setpoint-Änderung, drei
  Restart-Zyklen mit vollständiger Metadaten-Repopulation und funktionierendem
  Post-Restart-Write, Ausgangswert 21,0 °C wiederhergestellt, null Fehler im
  Bridge-Log. Die Restart-Zyklen decken Disconnect und Wiederherstellung ab;
  Stack anschließend auf `:latest` zurückgesetzt. Der
  Legacy-Typ `RoomHeatingTemperature` bleibt bis Phase 5b als Release-Rollback
  im Baum; sein Strategie-Konstruktor entfällt, da der Rollback über einen
  Revert erfolgt.

### Phase 5b — Legacy-Code löschen

Erst nach bestandener Hardwarematrix für Phase 3 und Phase 5, als eigener PR
ohne Verhaltensänderung. Zu erwartende Löschungen bzw. Restumfänge:

| Datei | erwartetes Ergebnis |
|---|---|
| `internal/usecases/roomheatingsysfn.go` | Facade + Entity-Resolution bleiben; Mode-ID-, Relation-, List-Merge- und Write-Teile entfallen |
| `internal/usecases/roomheatingtemp.go` | Facade bleibt; Setpoint-ID-Auflösung und Write entfallen |
| `internal/usecases/hvac_cache.go` | vollständig entfernbar, sobald Room Heating der letzte Nutzer war (DHW nutzt es nach der CDSF-Migration nicht mehr) |
| `internal/usecases/hvac_write_flow.go` | entfällt mit dem letzten rohen HVAC-Write |
| `internal/usecases/setpoint_flow.go` | entfällt mit dem letzten rohen Setpoint-Write; Range-/Step-Validierung wandert in den Heating-Facade |
| `internal/grpc/hvac_service.go` | unverändert — Sentinel-Namen und Mapping bleiben Vertrag |

Ein Nachweis, dass keine dieser Dateien mehr referenziert wird, ist Teil des
PRs (`go build ./... && go vet ./...` plus Grep auf die entfernten Symbole).

Umsetzungsstand 2026-07-22:

- [x] Die Legacy-Use-Cases `RoomHeatingSystemFunction` und
  `RoomHeatingTemperature` einschließlich eigener Negotiation, Events,
  Cache-Auflösung und roher Writes sind gelöscht. In den bisherigen Dateien
  bleiben ausschließlich die stabilen Bridge-State-/Sentinel-Verträge sowie
  die Heating-spezifische Range-/Step-Validierung.
- [x] Mode-ID-, Relations-, Full-List-Merge- und HVAC-Write-Code ist entfernt;
  `hvac_cache.go` und `hvac_write_flow.go` entfallen vollständig. Der bis zu
  einer öffentlichen CRHSF-`WriteCapabilities`-API nötige read-only Inspector
  prüft nur noch eindeutige Heating-SystemFunction, Write-Operation und
  Changeability.
- [x] `setpoint_flow.go` ist entfernt. Der noch aktive rohe DHW-Setpoint-Pfad
  besitzt nun DHW-spezifische Helfer; CRHT validiert weiterhin separat in der
  Heating-Facade und führt ausschließlich den Upstream-Write aus.
- [x] Legacy-only Tests und Lifecycle-Abdeckung sind entfernt bzw. auf die
  verbleibenden Upstream-Facades, Writer und DHW-Verträge zugeschnitten.
- [x] Grep auf die entfernten internen Symbole ist leer; `go test ./...`,
  `go test -race ./...`, `go build ./...` und `go vet ./...` sind grün. Die
  Coverage-Gates liegen bei 85,4 % gesamt und 90,6 % für geänderte produktive
  Statements.

### Phase 6 — Fork-Patches abbauen

1. Nach Upstream-Merge #239–#242 und der nötigen Hardening-Follow-ups den
   `bridge-integration`-Branch auf die neue `enbility/dev`-Basis setzen.
2. Gemergte Zeilen aus `UPSTREAM_PATCHES.md` entfernen.
3. Bridge gegen den exakten Upstream-Commit vollständig testen.
4. `replace github.com/enbility/eebus-go` erst entfernen, wenn keine benötigten
   Fork-Patches verbleiben.

## 9. Test-Spezifikation

### 9.1 eebus-go

- genau eine versus mehrere Heating-SystemFunctions in MRHSF/CRHSF/CRHT;
- vollständige und partiell fehlende Setpoint-/Constraint-Felder mit Presence;
- ein, mehrere, aktive und inaktive Setpoints;
- Operation Modes nur über die Relation der gewählten Heating-SystemFunction;
- `Write()` vorhanden/fehlend sowie Changeability true/false/nil;
- WritePartial-Payload und vollständiger Cache-Merge ohne Verlust fremder
  Einträge;
- Merge-Fehler muss den Write abbrechen;
- `auto`-/`off`-Verhalten gemäß finaler API-Entscheidung;
- non-zero Result und Post-Result-Refresh;
- verschwundene Features liefern matchbare Sentinel-Fehler;
- Race-, Build-, Use-Case- und gosec-Suite.

### 9.2 Bridge/Go

- MRHSF- und CRHSF-Entity sind verschiedene Objekte desselben SKI;
- CRHT-, CRHSF- und MRHSF-Resolver fehlen oder sind mehrdeutig;
- Adapter kombiniert MRHSF-State ausschließlich mit CRHSF-Capability;
- vollständige und unvollständige CRHT-State-Konvertierung;
- Mode-Deduplizierung und unbekannte Mode-Typen;
- zwei Mode-IDs mit gleichem Typ ⇒ ein Eintrag in `AvailableModes`, Write auf
  diesen Typ ⇒ `INVALID_ARGUMENT` statt Zufallsauswahl (§4.7);
- mehrere `roomAirTemperature`-Setpoints ⇒ `UNAVAILABLE` (§4.7);
- Capability noch nicht ausgehandelt ⇒ `UNAVAILABLE`, nicht
  `FAILED_PRECONDITION` (§4.2.1);
- synchroner und asynchroner Callback;
- falscher/nil Message Counter;
- Sendefehler, Geräteablehnung, Cancellation, Deadline und interner Timeout;
- keine doppelten Support-/State-Events;
- bestehende gRPC-Status- und Snapshot-/Stream-Tests bleiben unverändert grün;
- `go test -race ./...` und Cross-Language-Test.

### 9.3 Home Assistant

Solange der öffentliche Vertrag unverändert bleibt, sind keine neuen Entities
nötig. Bestehende Tests müssen zusätzlich als Regression belegen:

- `AUTO`, `HEAT`, `OFF` und unbekannte Modi;
- dynamische `TARGET_TEMPERATURE`-/Turn-On-/Turn-Off-Capabilities;
- Wert/Range/Step und Availability bei partiellen Zuständen;
- Write-Fehler behalten `ServiceValidationError` beziehungsweise den
  bestehenden gRPC-Fehler;
- Consolidated Stream und Polling ergeben denselben State.

### 9.4 Hardware

Je Ownership-Phase werden Modell, Firmware, Bridge-Commit und eebus-go-Commit
festgehalten. Mindestmatrix:

1. frisches Pairing, Bridge-Restart ohne Re-Pairing und SHIP-Reconnect;
2. initiale Werte und mindestens eine Push-Aktualisierung;
3. `auto → on → off → Ausgangsmodus` mit Result und Read-back;
4. Setpoint-Echo in jedem Modus;
5. verändernder Setpoint-Test in `on`, anschließend Restore;
6. kontrollierter Test der `auto`-/`off`-Semantik, anschließend Restore;
7. Write während Disconnect darf weder Erfolg noch doppelten Befehl erzeugen;
8. keine doppelten Events oder veralteten Werte nach Reconnect.

Sanitisierte SPINE-Traces werden als Upstream-Evidenz verlinkt, nicht mit
vollständigen SKIs oder Zertifikatsdaten committed.

## 10. Rollout- und Review-Policy

- Jede Phase ist ein eigener Bridge-PR.
- Phase 3 (Mode-Write) und Phase 5 (Setpoint-Write) werden nicht kombiniert.
- Ein PR nennt explizit State-, Capability- und Write-Owner.
- Hardware-Gates sind getrennt von grüner CI zu dokumentieren.
- Kein beweglicher PR-Branch wird direkt für ein Release verwendet.
- Der alte Writer wird erst nach Hardware-Abnahme entfernt.
- Rollback erfolgt durch einen neuen Build/Release, nie durch automatisches
  Retry im laufenden Request.
- Änderungen an #239–#242 werden zuerst im jeweiligen Contribution-Branch und
  danach im Fork-Integrationsbranch übernommen.

### Checkliste für jeden Phasenabschluss

- [ ] Den Statuskopf dieser Spezifikation auf die tatsächlich erreichte Phase
  aktualisieren.
- [ ] Kommentare und Dokumentation von Verweisen auf gelöschte Writer,
  veraltete Ownership oder bereits abgeschlossene Abnahmen bereinigen.
- [ ] Den Abschnitt zu bekannten Upstream-Limitierungen und das Patch-Inventar
  gegen den neuen Stand prüfen und verbleibende Lücken ausdrücklich festhalten.

## 11. Nicht Teil dieses Proposals

- Room Cooling (#243–#245)
- mehrere Heating Zones oder Rooms
- VHAN-basierte Umbenennung bestehender Entities
- Zeitpläne, Presets oder Calendar-Entities
- `hvac_action` ohne belastbaren Heating/Idle-Datenpunkt
- Änderungen an Flow-/Return-Temperatursensoren
- neue gRPC-Felder oder Proto-Versionen
- automatische Integration von #251 zusammen mit der Heating-Migration

## 12. Completion Definition

Die Migration ist abgeschlossen, wenn:

- MRT ausschließlich die Raum-Isttemperatur liefert;
- MRHSF ausschließlich Modus-Reads und State-Events besitzt;
- CRHSF ausschließlich Configuration-Capability und Mode-Writes besitzt;
- CRHT Setpoint-State, Constraints und Setpoint-Writes besitzt;
- die Bridge keine generische Heating-ID-, Relation-, Cache- oder List-Merge-
  Semantik mehr implementiert (nachgewiesen über die Löschliste in Phase 5b);
- die Bridge nur Entity-Komposition, Context/Result-Adaptation, Fehlerabbildung,
  EventBus, gRPC und HA-Policy behält;
- bestehende gRPC-/HA-Verträge und Unique IDs unverändert sind;
- die Hardwarematrix erfolgreich abgeschlossen ist;
- alle benötigten Änderungen im verwendeten Upstream-Stand vorliegen; und
- die Heating-bezogenen Fork-Patches aus `UPSTREAM_PATCHES.md` entfernt werden
  können.
