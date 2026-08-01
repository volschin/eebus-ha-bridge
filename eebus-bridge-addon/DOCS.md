# EEBUS Bridge Add-on

## What it does

Runs the Go EEBUS bridge inside Home Assistant OS. The bridge speaks SHIP/SPINE
on the LAN to heat pumps and other EEBUS devices, and serves a local gRPC API
that the `eebus` custom integration connects to.

The add-on packages the bridge binary from the release image published by this
repository; the add-on version is always the bridge version it contains.

## Prerequisites

- Home Assistant OS or Supervised. HA Core and HA Container cannot install
  add-ons — run the bridge with `docker-compose` instead.
- The `eebus` integration installed via HACS.

## Networking

The add-on runs with host networking, which is not optional: SHIP discovery
uses `_ship._tcp` mDNS multicast, and multicast does not cross a Docker bridge
network. Two ports are used on the host:

- `50051/tcp` — gRPC API, bound to `127.0.0.1`
- `4712/tcp` — EEBUS SHIP, bound to all interfaces so the heat pump can connect

Because HA Core also shares the host network namespace, the integration reaches
the gRPC API on `localhost` even though it is loopback-bound. That is why the
add-on needs no certificates or token for gRPC: the socket is not reachable
from anywhere but the machine itself.

## Setting up the integration

**Settings → Devices & Services → Add Integration → EEBUS**, then:

- **Host:** `localhost`
- **Port:** `50051` (or whatever `grpc_port` is set to)

The add-on log prints the bridge's own SKI at startup — that is the value the
heat pump's app asks for when pairing.

## Options

| Option | Default | Description |
|--------|---------|-------------|
| `grpc_port` | `50051` | Port for the gRPC API the integration connects to |
| `eebus_port` | `4712` | Port the EEBUS SHIP protocol listens on |
| `vendor` | `HomeAssistant` | Vendor name announced over EEBUS |
| `brand` | `Home Assistant` | Brand name; this is what the heat pump's app shows |
| `model` | `eebus-bridge` | Model name announced over EEBUS |
| `serial` | *(generated)* | Serial number announced over EEBUS |

Leaving `serial` empty makes the add-on generate one on first start and keep it
in `/data`. Changing `serial`, `vendor`, `brand` or `model` later changes the
announced device identity, and the heat pump may then treat the bridge as a new
device that has to be paired again.

## Persistent data

`/data` holds the SHIP certificate and key (under `certs/`) and the generated
serial. It survives restarts and add-on updates, but is deleted when the add-on
is uninstalled — after a reinstall the bridge has a new SKI and has to be
paired again.

## Troubleshooting

The add-on log is under **Settings → Add-ons → EEBUS Bridge → Log**.

**No devices discovered.** Check that the heat pump and the HA host are on the
same subnet and VLAN, and that nothing blocks `4712/tcp` or mDNS between them.

**Bridge never reaches `Trusted`, reconnects endlessly.** Vaillant gateways
accept exactly one EEBUS connection at a time. If the myVAILLANT cloud client
(sensoNET) already holds that slot, nothing else can connect — this is a device
limitation, not an add-on problem.

**Integration cannot connect.** Confirm the add-on is running and that the port
in the integration matches `grpc_port`.
