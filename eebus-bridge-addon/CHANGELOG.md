# Changelog

Add-on versions track the bridge release they package, so this file records
only what changed in the packaging. Bridge changes are in the
[project releases](https://github.com/volschin/eebus-ha-bridge/releases).

## Unreleased

- New option `experimental_mgcp_provider` (default off) exports
  `EEBUS_EXP_MGCP_PROVIDER` so the experimental MGCP grid provider can be
  enabled from the add-on configuration.

## 0.18.0

- Package bridge 0.18.0 alongside the integration requiring Home Assistant
  2026.11.0 or newer.
- Update signed-image verification tools to Cosign 3.1.4 and regctl 0.11.7.

## 0.16.2

- Initial release of the add-on.
