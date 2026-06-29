# Vaillant EEBUS manual → integration analysis

Source: `docs/eebusManual.pdf` = Vaillant operating instructions `0020257203_07`
(multilingual user manual, not a protocol spec). EEBUS section = ch. 1.

## What Vaillant exposes via EEBUS (manual §1.3)

| App group | Manual text | EEBUS mechanism | Bridge status |
|-----------|-------------|-----------------|---------------|
| Limit electrical HP output | §1.3.2 — external EM / DSO limits output (aroTHERM split/plus, recoCOMPACT from MY2023+) | LPC (§14a) | **DONE** (`eg/lpc`) |
| Communicate HP consumption | §1.3.3 — HP provides current electrical consumption for others to display | MPC | **DONE** (`ma/mpc`) |
| Heat pump energy management | §1.3.1 — charge DHW + heating-buffer cylinder on **PV surplus** to raise self-consumption | grid/PV signal (MGCP) + HP internal optimizer | **MISSING** |
| Display PV data | §1.3.3 — "if connected EM communicates PV operating data via EEBUS" → shown in myVAILLANT app | VAPD | **MISSING** |
| Set operating mode + target temps | §1.3.4 — VRC700/VRC720: DHW mode/boost/setpoint/actual, zones 1-3 mode/setpoint/room temp, outside temp | HVAC/setpoint SPINE features | **MISSING + not in eebus-go** |

## Direct answer: PV data from HA via EEBUS — worth it?

Two very different value levels. Don't conflate them.

1. **Display-only (Transparency, §1.3.3 "Displaying the PV data")**
   - Just makes myVAILLANT app show PV numbers HA already has.
   - HA dashboards already better. **Low value. Skip.**

2. **Functional energy management (§1.3.1) — the real prize**
   - Feed HA's grid/PV surplus to the HP so it auto-charges DHW + buffer
     cylinder with surplus solar (raise self-consumption, cut cost).
   - No Vaillant-blessed third-party energy manager needed — bridge becomes
     the CEM/data source.
   - **High value.** This is why someone wants PV over EEBUS.

**Verdict: yes, but only the functional path (#2) is worth building.**

## Feasibility blocker (important)

eebus-go v0.7.0 ships only the **reader** side of the relevant use cases:
- `ma/mgcp` — Monitoring of Grid Connection Point: local entity = reader,
  actor = GridConnectionPoint.
- `cem/vapd` — Visualization of Aggregated PV Data: local entity = reader,
  actor = PVSystem.

For the HP to *consume* HA's PV/grid data, the bridge must be the **provider**:
- **Grid Connection Point server** (MGCP server side) — publishes grid power
  at connection point; negative = export = surplus. HP reads as MA.
- and/or **PV System server** (VAPD server side) — publishes aggregated PV
  production. HP/app reads.

Neither provider side is a ready use case in eebus-go v0.7.0. Building it =
hand-wiring SPINE server features (Measurement, ElectricalConnection,
DeviceConfiguration) on a PVSystem / GridConnectionPoint entity. Non-trivial,
and must pass eebus-go per-use-case entity compatibility (cf. issue #47).

Open question to settle first: does the Vaillant HP read MGCP (grid point)
or VAPD (PV system) — or require an actual CEM use case — to trigger §1.3.1
cylinder charging? Manual says "energy management system communicates" but
not the exact UC. Needs protocol capture / eebus-go discovery against the
real VR940 to confirm before committing.

## CLAUDE.md correction

CLAUDE.md states: *"Vaillant exposes no HVAC control (modes/setpoints) over
EEBUS — out of scope by design; LPC + measurement only."*

Manual §1.3.4 contradicts this: Vaillant **does** offer set/display of
operating mode + target temps (VRC700/VRC720) over EEBUS. Practically still
out of scope because eebus-go has **no** HVAC/setpoint use cases (energy
domain only) — but the reason is "stack can't speak it," not "Vaillant
doesn't expose it." Update the wording.

## Recommended next steps (ordered)

1. **Confirm protocol** — *implemented*: `internal/eebus/discovery.go` dumps the
   live device's advertised use-case map (actor, name, version, available,
   scenarios) + every entity's features (Measurement / ElectricalConnection /
   DeviceConfiguration). Logged once per SKI on first use-case callback.

   Enable + capture:
   ```bash
   EEBUS_DEBUG_EVENTS=true ./eebus-bridge --config config.yaml   # or logging.debug_events: true
   # pair the VR940, then:
   docker-compose logs eebus-bridge | grep '\[DISCOVERY\]'
   ```
   Read the dump → decide whether the HP advertises an MGCP / VAPD / energy-mgmt
   path before building any provider-side use case.

   **Result (captured 2026-06-27, full dump: `docs/vr940-usecase-dump.txt`):**
   confirmed against the live VR940 (SKI 682F708C…, 10 entities). The gateway
   advertises **all three** target paths as `available=true`. The `actor` field
   is the role *the VR940 plays*, so for the data-feed use cases it is the
   consumer/reader — the bridge must implement the complementary **provider**.

   | Use case | VR940 actor (role it plays) | Bridge must provide | In eebus-go v0.7.0? |
   |----------|-----------------------------|---------------------|---------------------|
   | `monitoringOfGridConnectionPoint` (MGCP), scen 1-7 | MonitoringAppliance (reader) | Grid-connection-point **server** | only reader `ma/mgcp` → **no provider** |
   | `visualizationOfAggregatedPhotovoltaicData` (VAPD), scen 1-3 | VisualizationAppliance (reader) | PV-system **server** | only reader `cem/vapd` → **no provider** |
   | `visualizationOfAggregatedBatteryData` (VABD), scen 1-4 | VisualizationAppliance (reader) | Battery-system **server** | only reader `cem/vabd` → **no provider** |
   | `optimizationOfSelfConsumptionByHeatPumpCompressorFlexibility` (OSCF), scen 1,2 | Compressor (flexibility provider) | EM/optimizer side | **not in eebus-go at all** |

   Plus a major surprise: the VR940 exposes **full HVAC/DHW control** —
   `configurationOfDhw{SystemFunction,Temperature}`,
   `configurationOfRoomHeating{SystemFunction,Temperature}` and their monitoring
   counterparts, backed by `HVAC/server` + `Setpoint/server` features on the
   `DHWCircuit` (addr=4) and `HVACRoom` (addr=5:1:1) entities. **This directly
   confirms the CLAUDE.md correction above is needed** — Vaillant does speak
   HVAC; eebus-go simply lacks those use cases.

2. **Pick the lever.** Two distinct goals, both now confirmed possible:
   - *Functional PV-surplus* (§1.3.1, the high-value goal): the real control
     lever is **OSCF** (`…CompressorFlexibility`). It is the §1.3.1 mechanism
     but is **absent from eebus-go** → fully custom SPINE. **MGCP** (grid power,
     negative = export) is the data the HP's own optimizer reads to act on
     surplus — lower-effort than OSCF and reuses the Measurement/
     ElectricalConnection server features. **Recommend MGCP provider first.**
   - *Display PV/battery* (§1.3.3): VAPD/VABD provider. Cosmetic (myVAILLANT
     app); low priority but cheap once the provider-server scaffolding exists.
3. **Spike the provider-server side** (none ship in eebus-go): a local
   GridConnectionPoint / PVSystem entity exposing Measurement +
   ElectricalConnection + DeviceConfiguration server features, fed from HA.
   *Done (MGCP):* `internal/usecases/mgcp.go` serves scenarios 2/3/4 on a
   local grid-connection-point, gated by `experimental.mgcp_provider`.
4. New proto RPCs (`SetGridData` / `SetPVData`) both sides, regen Go + Python
   per CLAUDE.md proto contract.
   *Done (grid):* `GridService.PublishGridData` (`grid_service.proto`) +
   `internal/grpc/grid_service.go`; stubs regenerated both sides.
5. HA side: config option to map existing PV/grid power sensors → push stream.
   *Done:* options flow maps grid power (+ optional feed-in/consumption energy)
   sensors; the coordinator normalises to W/Wh and pushes on every state change.
   Remaining: live confirmation the commissioned VR940 acts on the data
   (cylinder charging on simulated surplus), then VAPD/VABD for PV/battery.
6. HVAC/DHW setpoint control is a *separate, larger* track (needs four custom
   SPINE use cases). Out of scope for the PV work; note it as a future milestone.

## MGCP provider hardware result + the commissioning gate (2026-06-28)

The MGCP provider spike (`feat/mgcp-provider-spike`, PR #65) was deployed twice
against the live VR940 and SHIP-traced:

- The provider is **spec-complete**: entity[2] `GridConnectionPointOfPremises`
  with Measurement/server + ElectricalConnection/server; useCaseData advertises
  `GridConnectionPoint / monitoringOfGridConnectionPoint` scenarios [2,3,4]
  (all three mandatory scenarios — power, grid feed-in Wh, grid consumed Wh).
- VR940 reads our `nodeManagementUseCaseData`, fires `consumer support update`,
  then **never sends a bindingRequest or subscription to entity[2] Measurement**
  — it subscribes only to NodeManagement + DeviceDiagnosis. Published values
  (-1234 W / 12340 Wh / 56780 Wh) never crossed the wire.
- Adding the mandatory energy scenarios 3 & 4 changed nothing → the blocker is
  **not** scenario completeness. Per eebus-go `ma/mgcp`, our advert is fully
  conformant; binding/subscription is **application-driven, not automatic on
  support-update**. VR940's app layer declines to consume an unsolicited grid
  source.

**The real gate is myVAILLANT app commissioning** (confirmed via the SOLARWATT
Manager manual, which pairs a third-party HEMS to a Vaillant HP — the same role
the bridge plays):

1. The energy manager (our bridge) must appear in the myVAILLANT app under
   **Settings → Network settings → EEBUS → Available devices**, be **Connected**
   and confirmed **"Trust this device"** so it lands in **Trusted devices**.
   This is an *app-side, user-confirmed* trust — distinct from the SHIP-level
   SKI trust HA already establishes. (Mutual trust: both sides must trust.)
2. **Settings → Controller → Energy management → activate the sliders for
   Heating AND Hot water.** This is the application switch that makes the HP
   actually bind/subscribe to an external grid connection point and run the
   §1.3.1 PV-surplus cylinder charging. Without it the HP ignores the offered
   MGCP grid source even when it sees the use case.

Action items (next, in order):
- ~~Verify the bridge presents the right device type/mDNS so it shows up in the
  myVAILLANT app's EEBUS *Available devices* list as a manager.~~ **Done — the
  bridge already advertises correctly (see below); no code change needed.**
- User performs the two app-side steps above on the VR940 / myVAILLANT app
  (cannot be done from the bridge).
- Re-run the SHIP trace and confirm VR940 binds + subscribes to entity[2]
  Measurement and reads the published grid power.

### Device-type advertisement check (2026-06-28) — bridge is NOT the blocker

Confirmed the bridge already presents itself as a proper EEBUS energy manager,
so it will appear in and be trustable from the myVAILLANT app:

- **DeviceType = `EnergyManagementSystem`** (`internal/eebus/service.go:37`,
  passed to `api.NewConfiguration`) — the correct manager/HEMS type.
- Identity from `config.EEBUS.*`: vendor `HomeAssistant`, brand `eebus-bridge`,
  model `eebus-bridge`, serial `ha-001` → SHIP name
  `d:_n:HomeAssistant_eebus-bridge-ha-001` (matches the captured trace).
- Discoverable via `_ship._tcp` mDNS — both HA and the VR940 found it; SHIP
  connected and SPINE discovery completed, so it is visible on the network.
- Trust model: explicit SKI allow-list via `RegisterRemoteSKI` (HA-driven
  `device.register_ski`, or the spike `experimental.trust_ski`), and
  `Callbacks.AllowWaitingForTrust` returns `true` so inbound pairing requests
  are accepted. Mutual SHIP trust already exists (the connection + discovery
  prove it).

Conclusion: **no bridge-side code change is required for discovery/trust.** The
only remaining gate is the myVAILLANT app, user-side: confirm
`HomeAssistant_eebus-bridge` under *Trusted devices*, then enable the
**Energy management** sliders (Heating + Hot water). Optional polish: the
generic `eebus-bridge` brand/model could be branded clearer in the app list,
but it is not required for function.

Caveat: evcc users report the VR940 advertises energy use cases it does not
reliably deliver/consume (discussion #25058) — possible firmware-level limits
even after correct commissioning.

## VAPD/VABD display providers built (2026-06-29)

The PV (VAPD) and battery (VABD) **provider** sides now exist on the bridge,
mirroring the MGCP provider scaffolding (step 2/5 above):

- `internal/usecases/vapd.go` — local **PVSystem** entity, actor `PVSystem`,
  scenarios 1 (nominal peak power via DeviceConfiguration), 2 (AC total power),
  3 (AC yield energy). Gated by `experimental.vapd_provider`.
- `internal/usecases/vabd.go` — local **ElectricityStorageSystem** entity, actor
  `BatterySystem`, scenarios 1 (power), 2 (charged Wh), 3 (discharged Wh),
  4 (state of charge %). Gated by `experimental.vabd_provider`.
- gRPC `VisualizationService.PublishPVData` / `PublishBatteryData`
  (`visualization_service.proto`) + `internal/grpc/visualization_service.go`;
  stubs regenerated both sides; `proto_stubs` re-exports `PVData`/`BatteryData`.

Both accept the VR940's advertised `VisualizationAppliance` consumer role. This
is a deployable increment **independent of HA push wiring**: enabling the flags
lets a SHIP trace confirm whether the VR940 actually binds + subscribes to the
PV/battery providers (the same open question that gates MGCP — see the
commissioning gate and the evcc caveat above).

**HA push wiring done** (commit `6b9c190`): `coordinator.async_push_pv_data` /
`async_push_battery_data` with state-change tracking, `CONF_*` sensor-mapping
constants, options-flow selectors (PV power/yield, battery power/energy/SoC),
en/de/strings translations, and `test_visualization_push.py` — mirroring the
grid push. Each provider enables only when its power sensor is mapped; optional
fields are omitted (not zeroed) when unavailable.

Remaining: empirical validation. Deploy with `vapd_provider` / `vabd_provider`
flags on and SHIP-trace the VR940 to confirm it binds + subscribes to the
PV/battery providers — the same open question gating MGCP §1.3.1 (does the HP
actually act on pushed data?). Hardware/user step, not code.
