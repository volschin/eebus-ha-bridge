# VR940 extended SPINE capture

The extended capture is an opt-in, one-shot diagnostic for discovering the data
behind the VR940's advertised EEBUS features. It reads only functions that the
remote feature explicitly advertises as readable. It never binds, subscribes or
writes.

## Enable the capture

In `eebus-bridge/config.yaml`:

```yaml
experimental:
  extended_capture: true
  extended_capture_dir: "/data/captures"
```

The equivalent environment variables are:

```text
EEBUS_EXP_EXTENDED_CAPTURE=true
EEBUS_EXP_EXTENDED_CAPTURE_DIR=/data/captures
```

The output directory must be writable by the bridge process and persisted from the
container when the files are needed outside it. Restart the bridge after enabling
the option. Client features required for the reads are announced during startup and
therefore cannot be armed on an already running EEBUS service.

After detailed discovery completes, the bridge logs:

```text
[EXTCAPTURE] completed device=<redacted:sha256:...> json=/data/captures/...json text=/data/captures/...txt
```

One JSON file and one text file are written per remote device and process start. The
JSON file is the complete canonical artifact; the text file is a compact, greppable
summary. File and directory permissions are restricted to the bridge account.

## Captured data

The artifact contains all discovered entities, features and advertised function
operations. Active reads cover:

- DeviceClassification;
- DeviceConfiguration;
- DeviceDiagnosis;
- ElectricalConnection;
- HVAC;
- LoadControl;
- Measurement;
- Setpoint;
- SmartEnergyManagementPs.

Other feature families remain visible with their operation metadata but are marked
`not_queried_unsupported_feature`. Write-only functions are marked `not_readable`.
Subscription capability is marked `not_attempted_read_only`, because testing it
would create remote subscription state.

Each readable function records its cached value before the request, its read status,
the returned data or SPINE error, and all read/write/partial flags. A timeout retains
any pre-existing cached data without claiming it was a fresh response.

## Redaction

The device SKI is replaced with a short SHA-256-derived identifier. These fields are
redacted recursively in use-case and function payloads:

- device addresses;
- SKIs;
- serial numbers;
- manufacturer node identifiers;
- user node identifiers.

Labels, descriptions, firmware versions and measurement metadata remain available
because they are required to identify pressure, runtime and vendor-specific values.
Review artifacts before publishing them if labels or descriptions contain personal
names.

## Hardware comparison sequence

Run separate bridge processes/captures for the states being compared. Preserve and
rename each artifact before the next run:

1. compressor stopped;
2. compressor running;
3. Vaillant energy management disabled and enabled;
4. one-day-away disabled and enabled;
5. before and after one controlled compressor start.

Diff the JSON artifacts. Candidate values are accepted only when their description,
entity, unit and controlled state change establish the intended meaning. Disable
`extended_capture` again after collecting the evidence.
