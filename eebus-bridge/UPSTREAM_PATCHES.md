# Upstream patch inventory

The bridge currently pins commit `8659252c6d26` from the
`github.com/volschin/eebus-go` branch `bridge-integration` because the required
upstream changes have not been merged.

Upstream base: `enbility/eebus-go@363db3c5c262`

| Patch | Upstream base | Pinned commit | Purpose | Removal condition |
|---|---|---|---|---|
| [enbility/eebus-go#232](https://github.com/enbility/eebus-go/pull/232) | `363db3c5c262` | `165f97dcd85d` | Monitoring of Room Temperature (MRT) client | Remove this patch after #232 is merged into an eebus-go revision used by the bridge. |
| [enbility/eebus-go#233](https://github.com/enbility/eebus-go/pull/233) | `363db3c5c262` | `34acc61a3015` | Monitoring of Outdoor Temperature (MOT) client | Remove this patch after #233 is merged into an eebus-go revision used by the bridge. |
| [enbility/eebus-go#239](https://github.com/enbility/eebus-go/pull/239) | `363db3c5c262` | `04aa1e965a31` | HVAC and Setpoint client features required by DHW system-function use cases | Remove this patch after #239 is merged into an eebus-go revision used by the bridge. |
| [enbility/eebus-go#246](https://github.com/enbility/eebus-go/pull/246) | `363db3c5c262` | `327bff5d0ac3` | Monitoring of DHW System Function (MDSF) client | Remove this patch after #246 is merged into an eebus-go revision used by the bridge. |
| [enbility/eebus-go#247](https://github.com/enbility/eebus-go/pull/247) | `363db3c5c262` | `2462aa3f3a3e` | Configuration of DHW System Function (CDSF) client | Remove this patch after #247 is merged into an eebus-go revision used by the bridge. |
| [enbility/eebus-go#249](https://github.com/enbility/eebus-go/pull/249) | `363db3c5c262` + #239 | `8659252c6d26` | Configuration of DHW Temperature (CDT) contribution with validated full-list writes | Remove this patch after #249 is merged into an eebus-go revision used by the bridge. |

All rows are unmodified cherry-picks of the feature commits from their upstream
pull requests; #249 is our replacement contribution for the obsolete draft #132.
Merge commits that only update a PR branch from `dev` are not
part of the patch queue because the integration branch already contains the
newer `enbility/dev` base. Monitoring of DHW Temperature (#226) is now part of
that base and has therefore been removed from the patch inventory. Remove the
`replace` directive and this inventory once all listed patches are available in
the eebus-go revision used by the bridge.
