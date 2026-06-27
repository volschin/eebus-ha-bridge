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
2. If MGCP/VAPD provider is the path: spike a provider-side use case (grid
   connection point measurements fed from HA via a new gRPC `SetGridData` /
   `SetPVData` RPC streamed from the coordinator).
3. New proto RPCs both sides (regen Go + Python per CLAUDE.md proto contract).
4. HA side: config option to map existing PV/grid power sensors → push stream.
5. Skip display-only PV. Skip HVAC setpoints until eebus-go gains the UCs.
