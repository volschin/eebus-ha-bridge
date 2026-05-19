# EEBUS Bridge Add-on

## Overview

This add-on runs the Go-based EEBUS bridge inside Home Assistant OS. It
exposes a local gRPC API consumed by the `eebus` custom integration and
speaks the EEBUS SHIP/SPINE protocol on the LAN to heat pumps and other
EEBUS-capable devices.

Host networking is required so the bridge can discover devices via mDNS
and accept incoming SHIP connections on TCP/4712.

## Prerequisites

- Home Assistant OS or Supervised (this add-on does not apply to Core or
  Container installations).
- The `eebus` custom integration installed via HACS.

## Configuration

| Option | Default | Description |
|--------|---------|-------------|
| `GRPC_PORT` | `50051` | Port for the gRPC API |
| `EEBUS_PORT` | `4712` | Port for EEBUS SHIP protocol |
| `EEBUS_VENDOR` | `HomeAssistant` | Vendor name announced via EEBUS |
| `EEBUS_BRAND` | `eebus-bridge` | Brand name announced via EEBUS |
| `EEBUS_MODEL` | `eebus-bridge` | Model name announced via EEBUS |
| `EEBUS_SERIAL` | *(auto)* | Serial number (leave empty for default) |

## Integration setup

When configuring the EEBUS integration, use:

- **Host:** `localhost`
- **Port:** `50051` (or whatever you set `GRPC_PORT` to)

## Certificate persistence

The bridge persists its SHIP certificate and private key under `/data/certs`
inside the add-on. This directory survives add-on restarts and upgrades but
is removed if the add-on is uninstalled. Re-pairing with the heat pump may
be required after uninstall/reinstall.

## Troubleshooting

Check the add-on logs in **Settings → Add-ons → EEBUS Bridge → Log**.
The bridge logs the local SKI at startup — use this for pairing with the
heat pump.

If devices are not discovered:

- Confirm `host_network: true` was not disabled.
- Verify the heat pump and HA host are on the same VLAN/subnet.
- Check port 4712/tcp is not blocked by a firewall.
