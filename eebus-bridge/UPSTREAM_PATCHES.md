# Upstream patch inventory

The bridge currently pins commit `1f909a8b465f` from the
`github.com/volschin/eebus-go` branch `bridge-integration` because the required
upstream changes have not been merged.

| Patch | Upstream base | Pinned commit | Purpose | Removal condition |
|---|---|---|---|---|
| [enbility/eebus-go#226](https://github.com/enbility/eebus-go/pull/226) | `0134afee5953` | `29523adca344` | Monitoring of DHW Temperature (MDT) client | Remove this patch after #226 is merged into an eebus-go revision used by the bridge. |
| [enbility/eebus-go#232](https://github.com/enbility/eebus-go/pull/232) | `0134afee5953` | `425abcd56f6f` | Monitoring of Room Temperature (MRT) client | Remove this patch after #232 is merged into an eebus-go revision used by the bridge. |
| [enbility/eebus-go#233](https://github.com/enbility/eebus-go/pull/233) | `0134afee5953` | `1f909a8b465f` | Monitoring of Outdoor Temperature (MOT) client | Remove this patch after #233 is merged into an eebus-go revision used by the bridge. |

The MRT and MOT rows are unmodified cherry-picks of the feature commits from
their upstream pull requests. The merge commit from #233 is intentionally not
part of the patch queue because the integration branch already contains the
same `enbility/dev` base. Remove the `replace` directive and this inventory once
all listed patches are available in the eebus-go revision used by the bridge.
