# Upstream patch inventory

The bridge currently pins `github.com/volschin/eebus-go` branch
`bridge-integration` because the required upstream change has not been merged.

| Patch | Upstream base | Pinned commit | Purpose | Removal condition |
|---|---|---|---|---|
| [enbility/eebus-go#226](https://github.com/enbility/eebus-go/pull/226) | `0134afee5953` | `29523adca344` | Monitoring of DHW Temperature (MDT) client | Remove the `replace` directive and this inventory after #226 is merged into an eebus-go revision used by the bridge. |

The pinned commit is the unmodified head of upstream PR #226.
