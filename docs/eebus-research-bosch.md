# EEBUS Research: Bosch Compress 5800i

**Device**: Bosch Compress 5800i Heat Pump  
**Research Date**: 2026-06-06  
**EEBUS Support**: Yes (MPC + LPC)  
**Bridge Version**: fix/bosch-compress-lpc

---

## Executive Summary

The Bosch Compress 5800i is a heat pump with **full EEBUS support** for:
- ✅ **MPC** (Monitoring of Power Consumption): Detailed power and energy metrics
- ✅ **LPC** (Limitation of Power Consumption): Power limiting with failsafe protection
- ✅ **Heartbeat**: Continuous connection monitoring

### Key Capabilities
- Power consumption monitoring (total)
- Energy tracking (consumed, produced, heating, DHW)
- Power limiting via LPC (0-device_max watts)
- Failsafe power limit handling
- Heartbeat/keepalive signaling

---

## Supported Use Cases

### 1. MPC (Monitoring of Power Consumption)
**Status**: ✅ Fully Supported

#### Available Measurements
| Measurement | Value | Unit | Notes |
|---|---|---|---|
| **Power Consumption (Total)** | ~3000-5500 | W | Always available |
| **Power L1/L2/L3** | Variable | W | ⚠️ Often "data not available" |
| **Current L1/L2/L3** | Variable | A | ⚠️ Often "data not available" |
| **Voltage L1/L2/L3** | ~230 | V | ⚠️ Often "data not available" |
| **Grid Frequency** | ~50 | Hz | Sometimes available |
| **Energy Consumed (Total)** | Cumulative | kWh | Always available |
| **Energy Consumed (Heating)** | Cumulative | kWh | Via scoped reading |
| **Energy Consumed (DHW)** | Cumulative | kWh | Via scoped reading |
| **Energy Produced** | Cumulative | kWh | For devices with PV |

**Per-Phase Data Notes**: 
- The Bosch Compress 5800i is a **single-phase device** at ~230V
- Per-phase measurements (L1, L2, L3) may return "data not available"
- This is **expected and normal** for this device class
- Total power consumption is always reliable
- HA shows "Unknown" for unavailable values (correct behavior)

#### Protocol Logging
Enable detailed MPC logging with environment variable:
```bash
DEBUG_EEBUS_PROTOCOL=true
```

Output example:
```
[PROTOCOL] Bosch device measurements (SKI=...):
  [PROTOCOL]   power_consumption = 3400.50 W
  [PROTOCOL]   current_l1 = data_not_available A
  [PROTOCOL]   voltage_l1 = data_not_available V
  [PROTOCOL]   frequency = 50.00 Hz
  [PROTOCOL]   energy_consumed = 12345.67 kWh
  [PROTOCOL]   energy_consumed_heating = 8900.45 kWh
  [PROTOCOL]   energy_consumed_dhw = 2100.22 kWh
```

### 2. LPC (Limitation of Power Consumption)
**Status**: ✅ Fully Supported

#### Write Operations
| Operation | Range | Notes |
|---|---|---|
| **Set Power Limit** | 0-32000 W | Device enforces max (typically 5500W) |
| **Set Duration** | 1-3600+ s | Limit validity period |
| **Activate/Deactivate** | Boolean | Enable/disable limiting |
| **Set Failsafe Limit** | 0-device_max | Fallback if connection lost |
| **Set Failsafe Duration** | Seconds | Minimum failsafe timeout |

#### Read Operations
| Property | Value | Notes |
|---|---|---|
| **Current Limit** | ~5500 W | Device maximum |
| **Nominal Max Power** | ~5500 W | Device specification |
| **Failsafe Settings** | Configurable | Safety feature |
| **Heartbeat Status** | Running | Ongoing connection check |

**Failsafe Behavior**:
- If connection lost, device falls back to failsafe limit
- Prevents device runaway without controller
- Requires explicit duration configuration
- Critical for safety in real deployments

#### Protocol Logging
Same environment variable enables LPC logging:
```bash
DEBUG_EEBUS_PROTOCOL=true
```

Output example:
```
[PROTOCOL] Bosch LPC consumption limit (SKI=...): value=5000 W, active=true, changeable=true, duration=3600 s
[PROTOCOL] Bosch failsafe limit (SKI=...): value=3000 W, min_duration=300 s
[PROTOCOL] Bosch nominal max power (SKI=...): 5500.00 W
[PROTOCOL] Bosch LPC write (SKI=...): value=4500 W, duration=1800 s, active=true
```

---

## EEBUS Protocol Details

### Device Discovery
The device registers itself on the EEBUS network:
- **SKI** (Sender Key Identifier): Unique device identifier
- **Vendor**: Bosch (varies by model)
- **Brand/Model**: Identifies device type
- **Local Entity**: CEM (Central Energy Manager) communication endpoint

### Communication
- **Protocol**: EEBUS (based on SPINE/SHIP)
- **Port**: 4712 (default EEBUS)
- **Security**: TLS 1.2+ with certificate exchange
- **Frequency**: Polling + event-driven updates

### Use Case Support Updates
The bridge receives callbacks when device capabilities change:
- Device registration/re-registration
- Use case support updates (MPC, LPC availability)
- Connection state changes

---

## Home Assistant Integration

### Sensors Created
| Entity | Type | Source | Device |
|---|---|---|---|
| `sensor.bosch_compress_power` | Power | MPC | Compressor |
| `sensor.bosch_compress_energy_total` | Energy | MPC | Compressor |
| `sensor.bosch_compress_energy_heating` | Energy | MPC | Compressor |
| `sensor.bosch_compress_energy_dhw` | Energy | MPC | Compressor |
| `sensor.bosch_compress_frequency` | Frequency | MPC | Compressor |
| `number.bosch_compress_lpc_limit` | Number | LPC | Control |
| `switch.bosch_compress_lpc_active` | Switch | LPC | Control |
| `switch.bosch_compress_heartbeat` | Switch | LPC | Status |

### Configuration
```yaml
# .homeassistant/configuration.yaml
eebus:
  host: 192.168.x.x
  port: 50051
  ski: "DEVICE_SKI_HERE"
```

### Energy Dashboard
- ✅ Total energy consumed tracked
- ✅ Per-scope breakdown (heating, DHW)
- ✅ Per-phase data shows "Unknown" (expected for single-phase device)

---

## Known Issues & Solutions

### Issue 1: SKI Transmission Reliability (FIXED ✅)
**Problem**: Bridge receiving empty SKI values, causing "requested_ski=" errors  
**Root Cause**: Fallback logic in services using FirstAvailableEntity when SKI not found  
**Solution**: Removed fallback to nil-entity; now explicitly rejects empty SKI  
**Fix Applied**: 
- Added SKI empty validation in all gRPC methods
- Removed dangerous fallback entity resolution
- Added InvalidArgument error for empty SKI

### Issue 2: Per-Phase Measurements Showing "Unknown"
**Problem**: L1, L2, L3 measurements not available  
**Root Cause**: Bosch Compress 5800i is single-phase; doesn't expose per-phase data  
**Solution**: This is **correct behavior** - device doesn't support it  
**HA Behavior**: Shows "Unknown" (correct)

### Issue 3: Energy Counter "Data Not Available"
**Problem**: Energy readings sometimes return "data not available"  
**Root Cause**: Device-specific measurement unavailability  
**Solution**: Code handles gracefully, returns NotFound instead of error  
**HA Behavior**: Shows "Unknown" or 0 (expected)

---

## Debug & Monitoring

### Environment Variables

#### Protocol Logging
```bash
DEBUG_EEBUS_PROTOCOL=true
```
- Logs all EEBUS measurements and control commands
- Shows exact values sent/received from device
- Useful for discovering new capabilities
- **Only enable in debug scenarios** (high log volume)

#### Event Logging
```bash
EEBUS_DEBUG_EVENTS=true
```
- Logs EventBus routing and callbacks
- Shows device discovery/re-registration events
- Useful for connection troubleshooting

#### Other Variables
```bash
EEBUS_GRPC_PORT=50051              # Override gRPC port
EEBUS_PORT=4712                    # Override EEBUS port
EEBUS_VENDOR="HomeAssistant"       # Bridge vendor name
EEBUS_BRAND="eebus-bridge"         # Bridge brand/model
```

### Log Monitoring

**Enable protocol logging in docker-compose.yml:**
```yaml
environment:
  - DEBUG_EEBUS_PROTOCOL=true
  - EEBUS_DEBUG_EVENTS=true
```

**View logs:**
```bash
docker-compose logs -f eebus-bridge
```

**Expected output with protocol logging:**
```
[DEBUG] Monitoring.GetMeasurements success: requested_ski=... entries=7
[PROTOCOL] Bosch device measurements (SKI=...):
  [PROTOCOL]   power_consumption = 3400.50 W
  [PROTOCOL]   frequency = 50.00 Hz
  [PROTOCOL]   energy_consumed = 12345.67 kWh
```

---

## Testing Results

### Live Container Testing
- ✅ Bridge running 6+ hours without errors
- ✅ Measurements reading correctly
- ✅ LPC limits settable and applied
- ✅ Heartbeat maintaining connection
- ✅ Failsafe protection active
- ✅ No SKI transmission failures (post-fix)
- ✅ Energy dashboard working

### Test Scenarios
1. **Power Limiting**: Set limit to 3000W → Device accepted and enforced
2. **Duration Handling**: Set 1-hour duration → Device maintained limit for period
3. **Heartbeat**: 30-second intervals → Connection stable
4. **Device Re-registration**: Bridge handled gracefully
5. **Per-Phase Measurements**: Returned "data not available" (expected)

---

## Architecture Integration

### Bridge Components
```
eebus-bridge (Go)
├── DeviceRegistry: Maps SKI → device entities
├── EventBus: Pub/Sub for internal routing
├── MonitoringService: MPC gRPC endpoint
│   └── Reads power, energy, measurements
├── LPCService: LPC gRPC endpoint
│   └── Writes limits, failsafe, heartbeat
└── DeviceService: Device discovery/registration
    └── Routes EEBUS callbacks

Home Assistant (Python)
├── Coordinator: Async gRPC client
│   └── 30-second polling cycle
├── Sensors: Power, energy, frequency entities
├── Switches: LPC active, heartbeat switches
└── Numbers: LPC limit, failsafe limit numbers
```

### Data Flow
```
Bosch Device
    ↓ (EEBUS, port 4712)
eebus-bridge (Go backend)
    ├── Processes MPC/LPC
    └── Exposes via gRPC (port 50051)
        ↓ (gRPC, async polling)
    HA Coordinator (Python)
        ├── Polls measurements every 30s
        └── Updates entity states
            ↓
        HA UI & Energy Dashboard
```

---

## Future Enhancement Opportunities

### Measurements
- [ ] Per-phase breakdown (if device gains capability)
- [ ] Additional HVAC measurements (temperature, mode)
- [ ] Real-time power curve data
- [ ] Device diagnostics/health status

### Control
- [ ] Temperature setpoint control
- [ ] Mode switching (heat/cool/auto)
- [ ] Seasonal efficiency adjustments
- [ ] Advanced failsafe strategies

### Monitoring
- [ ] Device firmware version tracking
- [ ] Performance metrics collection
- [ ] Anomaly detection
- [ ] Historical data aggregation

---

## References

### EEBUS Documentation
- EEBUS Specification: [eebus-go library](https://github.com/enbility/eebus-go)
- SPINE Protocol: [spine-go library](https://github.com/enbility/spine-go)
- Ship Protocol: [ship-go library](https://github.com/enbility/ship-go)

### Device-Specific
- Bosch Compress 5800i datasheet
- EEBUS certified devices list

### Integration
- Home Assistant EEBUS documentation
- gRPC protocol buffers (proto files in `gen/proto/`)

---

## Document History

| Date | Version | Changes |
|---|---|---|
| 2026-06-06 | 1.0 | Initial research documentation |
| - | 1.1 | SKI transmission bug fix documentation |
| - | 1.2 | Protocol logging system documentation |

---

**Last Updated**: 2026-06-06  
**Maintained By**: eebus-ha-bridge team  
**Status**: Active Development
