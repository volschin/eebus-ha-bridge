# Proto contract matrix

The bridge API remains additive in `eebus.v1`. Removing deprecated RPCs or
existing streams requires a new API package such as `eebus.v2`.

| Bridge contract | First bridge release | Python client behavior |
| --- | --- | --- |
| Legacy v1 unary RPCs and per-domain streams | `<= 0.12.x` | Required baseline. |
| `DeviceService.GetDeviceCapabilities` | `0.13.0` | Preferred when available; older bridges are handled as implicit/unknown capability support. |
| `DeviceService.SubscribeDeviceState` consolidated stream with revisions/resync | `0.13.0` | Preferred when available; clients fall back to legacy streams on `UNIMPLEMENTED`. |
| Deprecated `LPCService.StartHeartbeat` / `StopHeartbeat` | `<= 0.12.x`, retained in `0.13.x` | Kept for old clients; removal is only allowed in a future non-v1 package. |

Compatibility rule: additive fields, messages, RPCs, and services may continue
to land in `eebus.v1`. Field deletion, field-number reuse, incompatible type
changes, or removal of deprecated heartbeat RPCs / existing streams must happen
only in a new API package.
