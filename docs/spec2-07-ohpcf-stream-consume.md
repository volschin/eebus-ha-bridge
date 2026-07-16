# SPEC2-07 Slice 1 — consume the OHPCF event stream

Source: `docs/refactoring-optimization-spec-v2.md` SPEC2-07 (P1). This slice covers the
first bullet of its Ist-Befund only — full revision/gap-counter machinery (later
bullets) is a separate follow-up slice, same incremental pattern as SPEC2-08.

## Problem

The bridge implements `OHPCFService.SubscribeOHPCFEvents` (streams `OHPCFEvent`,
see `eebus-bridge/proto/eebus/v1/ohpcf_service.proto`), but
`EebusCoordinator.async_start_streams` (`custom_components/eebus/coordinator.py`)
never starts it. External OHPCF state changes (compressor offer appears, process
gets scheduled/paused/resumed by another CEM, etc.) are invisible in HA until the
next 5-minute poll.

Every other push-capable use case (LPC, DHW, DHW-sysfn, room-heating, measurements)
already has this wiring. OHPCF is the one gap.

## What to build

Add OHPCF stream consumption in `custom_components/eebus/coordinator.py`, mirroring
the existing `consume_dhw` / `_handle_dhw_event` pair exactly (see
`async_start_streams` and `_handle_dhw_event` as the reference implementation):

1. `consume_ohpcf(channel)` — subscribes via
   `proto_stubs.ohpcf_service_stub(channel).SubscribeOHPCFEvents(DeviceRequest(ski=self.ski))`,
   dispatches each event to a new `_handle_ohpcf_event(event)`. Register it in the
   `streams` dict passed to `self._stream_manager.start(...)` under key
   `"ohpcf_events"`.

2. `_handle_ohpcf_event(event)`:
   - Skip if `not self._event_matches(event.ski)`.
   - `OHPCF_EVENT_SUPPORT_UPDATED` → no reliable payload semantics, reconcile via
     `self.hass.async_create_task(self.async_request_refresh())` (same as the
     `LPC_EVENT_HEARTBEAT_TIMEOUT` / measurement "support" branch).
   - `OHPCF_EVENT_STATE_UPDATED` and `OHPCF_EVENT_DATA_UPDATED` → both carry the
     current `flexibility` payload (`CompressorFlexibility`, proto3 message field,
     so `event.HasField("flexibility")` is valid). If unset, reconcile via poll
     (same "signalled without payload" fallback used everywhere else). If set,
     build a `CompressorFlexibilityState` dict with the same field mapping
     `_async_read_compressor_flexibility` uses in `snapshot.py` (`available`,
     `state` via `CompressorPowerConsumptionState.Name(...)`,
     `requested_power_estimate_w`/`requested_power_max_w` via `HasField` on the
     optional scalar fields, `is_pausable`, `is_stoppable`,
     `minimal_run_seconds`, `minimal_pause_seconds`), then route it through the
     shared reducer:
     ```python
     ohpcf, capabilities = apply_reading(
         self._domain_state.ohpcf,
         "compressor_flexibility",
         value,
         self._domain_state.capabilities,
         "ohpcf",
         None,
     )
     self._domain_state = replace(self._domain_state, ohpcf=ohpcf, capabilities=capabilities)
     self._push_domain_fields("compressor_flexibility", "ohpcf_supported")
     ```
   - Any other/unknown event type: ignore.

3. If the bridge doesn't implement the RPC (older bridge version), the existing
   `StreamManager._run_stream` UNIMPLEMENTED handling already degrades to
   polling-only for that one stream — no special-casing needed here.

## Out of scope for this slice

- Monotone per-SKI stream revisions and coalesced gap-reconcile (SPEC2-07 Soll
  bullets 2–3).
- Go eventbus per-subscriber/per-stream-family drop counters (bullet 4).
- Cross-stream/poll "newer wins" ordering guarantee via a single publish point
  (bullet 5) — today's pattern (last write wins via `_push_domain_fields`) is
  unchanged, consistent with how LPC/DHW/room-heating already behave.
- Backoff/last-received/last-error/reconnect-count observability (bullet 6).

## Acceptance

- New/updated unit test(s) in `custom_components/eebus/tests/test_coordinator.py`
  (or `test_ohpcf.py`) proving: a `OHPCF_EVENT_STATE_UPDATED` event with a
  `flexibility` payload updates `compressor_flexibility` in the coordinator
  snapshot without a poll; a `OHPCF_EVENT_SUPPORT_UPDATED` event or a
  state/data event with no payload triggers `async_request_refresh`.
- `ohpcf_events` appears in `async_start_streams`'s `streams` dict.
- No changes to `eebus-bridge/` (Go side already implements the RPC).
- No changes to `models.py` or any platform file — same flatten shape as today.

## Verification

- `ruff check custom_components/`
- `mypy custom_components/eebus`
- `PYTHONPATH=. pytest`
