# EEBUS Bridge Add-on

Runs the Go EEBUS bridge as a Home Assistant add-on, so Home Assistant OS and
Supervised users get the `eebus` integration working without a separate Docker
host.

The add-on is **only** for HA OS / Supervised. HA Core and HA Container
installations keep running the bridge via `docker-compose` — see the project
[README](../README.md).

See [DOCS.md](DOCS.md) for options, pairing and troubleshooting.
