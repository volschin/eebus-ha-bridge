# Upstream patch inventory

The bridge currently pins commit `b40877d34a63` from the
`github.com/volschin/eebus-go` branch `bridge-integration` because the required
upstream changes have not been merged.

Upstream base: `enbility/eebus-go@363db3c5c262`

| Patch | Upstream base | Pinned commit | Purpose | Removal condition |
|---|---|---|---|---|
| [enbility/eebus-go#232](https://github.com/enbility/eebus-go/pull/232) | `363db3c5c262` | `165f97dcd85d` | Monitoring of Room Temperature (MRT) client | Remove this patch after #232 is merged into an eebus-go revision used by the bridge. |
| [enbility/eebus-go#233](https://github.com/enbility/eebus-go/pull/233) | `363db3c5c262` | `34acc61a3015` | Monitoring of Outdoor Temperature (MOT) client | Remove this patch after #233 is merged into an eebus-go revision used by the bridge. |
| [enbility/eebus-go#239](https://github.com/enbility/eebus-go/pull/239) | `363db3c5c262` | `237461d19a74` | HVAC and Setpoint client features, including write-operation gates and partial/full-list handling | Remove this patch after #239 is merged into an eebus-go revision used by the bridge. |
| [enbility/eebus-go#240](https://github.com/enbility/eebus-go/pull/240) | `363db3c5c262` + #239 | `c72bfd76e95a` | Configuration of Room Heating Temperature (CRHT), including result callbacks and fail-closed entity resolution | Remove this patch after #240 is merged into an eebus-go revision used by the bridge. |
| [enbility/eebus-go#241](https://github.com/enbility/eebus-go/pull/241) | `363db3c5c262` + #239 | `c6415cc4b453` | Configuration of Room Heating System Function (CRHSF), including relation-safe writes and result callbacks | Remove this patch after #241 is merged into an eebus-go revision used by the bridge. |
| [enbility/eebus-go#242](https://github.com/enbility/eebus-go/pull/242) | `363db3c5c262` + #239 | `a5640012fbd6` | Monitoring of Room Heating System Function (MRHSF) client | Remove this patch after #242 is merged into an eebus-go revision used by the bridge. |
| [enbility/eebus-go#246](https://github.com/enbility/eebus-go/pull/246) | `363db3c5c262` | `327bff5d0ac3` | Monitoring of DHW System Function (MDSF) client | Remove this patch after #246 is merged into an eebus-go revision used by the bridge. |
| [enbility/eebus-go#247](https://github.com/enbility/eebus-go/pull/247) | `363db3c5c262` | `7dc3d134d968` | Configuration of DHW System Function (CDSF), including relation-safe writes and result callbacks | Remove this patch after #247 is merged into an eebus-go revision used by the bridge. |
| [enbility/eebus-go#249](https://github.com/enbility/eebus-go/pull/249) | `363db3c5c262` + #239 | `8659252c6d26` | Configuration of DHW Temperature (CDT) contribution with validated full-list writes | Remove this patch after #249 is merged into an eebus-go revision used by the bridge. |
| [volschin/eebus-go#1](https://github.com/volschin/eebus-go/pull/1) | `7dc3d134d968` | `8a29b1ba99f0` | Fail-closed list merges, payload-level write tests, and stricter CDSF/CDT integration | Remove this patch after the corresponding follow-ups are merged upstream. |
| [volschin/eebus-go#2](https://github.com/volschin/eebus-go/pull/2) | `a5640012fbd6` | `8f497bffeeb4` | Structured CDSF write capabilities and post-acceptance list refresh | Remove this patch after equivalent CDSF capability and refresh APIs are merged upstream. |
| [volschin/eebus-go#3](https://github.com/volschin/eebus-go/pull/3) | `8f497bffeeb4` | `2845a153ae11` | Independent fail-closed capability resolution for optional CDSF scenarios | Remove this patch together with #2 after equivalent capability semantics are merged upstream. |
| [volschin/eebus-go#4](https://github.com/volschin/eebus-go/pull/4) | `2845a153ae11` | `b40877d34a63` | Fail-closed MRHSF resolution when an HVAC room exposes multiple heating system functions | Remove this patch after equivalent MRHSF ambiguity handling is merged upstream. |

The enbility PR rows are unmodified cherry-picks of their feature commits;
#249 is our replacement contribution for the obsolete draft #132. The
volschin rows contain the bridge integration hardening that is still being
prepared for upstream follow-ups. Merge commits that only update a PR branch from `dev` are not
part of the patch queue because the integration branch already contains the
newer `enbility/dev` base. Monitoring of DHW Temperature (#226) is now part of
that base and has therefore been removed from the patch inventory. Remove the
`replace` directive and this inventory once all listed patches are available in
the eebus-go revision used by the bridge.

## Known limitations (not yet filed upstream)

- `crhsf.CRHSF.WriteOperationMode` currently selects the first related mode ID
  whose description has the requested type. It does not deduplicate identical
  IDs or fail closed when several distinct IDs share that type. Unlike the
  hardened CDSF writer, it also does not request
  `HvacSystemFunctionListData` again after an accepted result. Room-heating
  Phase 3 therefore still needs an upstream hardening patch for unambiguous
  mode resolution and post-result refresh before hardware acceptance.
- `cdsf.CDSF.WriteCapabilities` deliberately returns zero capabilities with a
  nil error whenever required CDSF metadata is missing, ambiguous, or the
  device genuinely doesn't support the write (`usecases/ca/cdsf/public.go`).
  The bridge (`upstreamDHWSystemFunctionCapabilityInspector.State`,
  `internal/usecases/dhwsysfn_configuration.go`) has no way to tell these
  cases apart, so a DHW boost/mode write attempted in the narrow window right
  after connect — before CDSF's cache has populated — now surfaces as
  `FAILED_PRECONDITION` ("not writable") instead of the pre-migration
  `UNAVAILABLE` ("try again"). Fixing this needs an upstream API change
  (e.g. a distinguishable "not yet negotiated" return) — no bridge-side fix
  is possible without re-implementing the cache-population tracking this
  migration was designed to remove.
- `features/client.NewFeature` returns plain, unwrapped `errors.New(...)`
  values (e.g. `"local feature not found"`, `"remote feature not found"`) when
  a feature binding disappears mid-write (a disconnect race). These reach
  `mapUpstreamDHWWriteError` (`internal/usecases/dhwsysfn_upstream_boost.go`,
  `dhwsysfn_upstream_mode.go`) with no matchable sentinel, so they fall
  through to `codes.Internal` instead of `codes.Unavailable`. A bridge-side
  fix would require string-matching the error text, which is fragile across
  upstream revisions; the correct fix is an exported sentinel error from
  upstream's `NewFeature`.
