# Proto contract matrix

The bridge API remains additive in `eebus.v1`. Removing deprecated RPCs or
existing streams requires a new API package such as `eebus.v2`.

| Bridge contract | First bridge release | Python client behavior |
| --- | --- | --- |
| Legacy v1 unary RPCs and per-domain streams | `<= 0.12.x` | Required baseline. |
| `DeviceService.GetDeviceCapabilities` | `0.13.0` | Preferred when available; older bridges are handled as implicit/unknown capability support. |
| `DeviceService.SubscribeDeviceState` consolidated stream with revisions/resync | `0.13.0` | Pre-negotiation clients fall back to legacy streams on `UNIMPLEMENTED`. Clients with `GetServerInfo` negotiation select it only when `FEATURE_CONSOLIDATED_DEVICE_STREAM` is advertised (a `0.13.x` bridge without `GetServerInfo` is therefore served through legacy streams); `UNIMPLEMENTED` remains a secondary guard. |
| `DeviceService.GetServerInfo` API `1.0` and append-only `FeatureId` values | next release after `0.13.1` | Read and cached once per bridge runtime. A missing RPC selects the legacy profile; only an incompatible API major is rejected. |
| Payload-complete `SubscribeDeviceState` events with explicit availability and detail-measurement lists | next release after `0.13.1` | Selected through `FEATURE_CONSOLIDATED_DEVICE_STREAM`; complete deltas never trigger polling. |
| Deprecated `LPCService.StartHeartbeat` / `StopHeartbeat` | `<= 0.12.x`, retained in `0.13.x` | Kept for old clients; removal is only allowed in a future non-v1 package. |

Compatibility rule: additive fields, messages, RPCs, and services may continue
to land in `eebus.v1`. Field deletion, field-number reuse, incompatible type
changes, or removal of deprecated heartbeat RPCs / existing streams must happen
only in a new API package.
