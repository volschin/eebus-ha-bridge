# EEBUS Bridge Add-on for Home Assistant

Runs the EEBUS Bridge (Go) as a Home Assistant add-on, enabling EEBUS
SHIP/SPINE protocol communication with heat pumps directly from your HA
instance. Pairs with the `eebus` custom integration.

See [DOCS.md](DOCS.md) for configuration and pairing details.

This add-on is only required for Home Assistant OS (and Supervised) users.
HA Core or HA Container deployments should run the bridge as a standalone
Docker container via `docker-compose` — see the project root `README.md`.
