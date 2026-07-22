# Proto contract matrix

The bridge API remains additive in `eebus.v1`. Removing deprecated RPCs or
existing streams requires a new API package such as `eebus.v2`.

| Bridge contract | First bridge release | Python client behavior |
| --- | --- | --- |
| Legacy v1 unary RPCs and per-domain streams | `<= 0.12.x` | Required baseline. |
| `DeviceService.GetDeviceCapabilities` | `0.13.0` | Preferred when available; older bridges are handled as implicit/unknown capability support. |
| `DeviceService.SubscribeDeviceState` consolidated stream with revisions/resync | `0.13.0` | Pre-negotiation clients fall back to legacy streams on `UNIMPLEMENTED`. Clients with `GetServerInfo` negotiation select it only when `FEATURE_CONSOLIDATED_DEVICE_STREAM` is advertised (a `0.13.x` bridge without `GetServerInfo` is therefore served through legacy streams); `UNIMPLEMENTED` remains a secondary guard. |
| `DeviceService.GetServerInfo` API `1.0` and append-only `FeatureId` values | next release after `0.13.1` | Negotiated and cached for each transport-channel generation. A rebuilt channel renegotiates the contract, a missing RPC selects the legacy profile, and only an incompatible API major is rejected. |
| Payload-complete `SubscribeDeviceState` events with explicit availability and detail-measurement lists | next release after `0.13.1` | Selected through `FEATURE_CONSOLIDATED_DEVICE_STREAM`; complete deltas never trigger polling. |
| `MeasurementEntry.id` / append-only `MeasurementId` catalog | next release after `0.14.0` | Selected through `FEATURE_TYPED_MEASUREMENTS`. New clients prefer the stable ID and canonical unit; old clients keep using the populated `type` string. Unknown IDs and extension strings are ignored and counted in diagnostics. |
| `DeviceService.GetDeviceSnapshot` and `DeviceStateEvent.initial_snapshot` | next release after `0.14.0` | Selected through `FEATURE_DEVICE_SNAPSHOT`. Initial and periodic reconciliation use one device read; `field_states` preserves partial availability. Bridges without the feature retain the bounded multi-RPC and legacy-stream fallback. |
| `LPCEvent.heartbeat_update` | next release after `0.14.0` | Additive payload on heartbeat deltas; old clients ignore it, new clients no longer wait for periodic reconciliation. |
| `DeviceStatus.readiness` / `recovery` | next release after `0.14.0` | Additive device-scoped states distinguish untrusted, disconnected, grace-period, recovering, ready, and exhausted without changing process-wide gRPC health. |
| `DeviceService.GetDeviceDiagnostics` / `FEATURE_OPERATIONAL_DIAGNOSTICS` | next release after `0.14.0` | Authenticated, feature-negotiated operational projection with redacted SKI, event/drop/resync counters, connection/monitoring and snapshot freshness, recovery, provider sample state, and advertised features. Older bridges simply omit this diagnostics section. |
| `PairedDevice` software and hardware revisions | next release after `0.15.0` | Additive classification fields. New clients map values reported through SPINE DeviceClassification into the Home Assistant device registry; old clients ignore them. Missing remote fields remain unset rather than being guessed. |
| Deprecated `LPCService.StartHeartbeat` / `StopHeartbeat` | `<= 0.12.x`, retained in `0.13.x` | Kept for old clients; removal is only allowed in a future non-v1 package. |

Compatibility rule: additive fields, messages, RPCs, and services may continue
to land in `eebus.v1`. Field deletion, field-number reuse, incompatible type
changes, or removal of deprecated heartbeat RPCs / existing streams must happen
only in a new API package.
