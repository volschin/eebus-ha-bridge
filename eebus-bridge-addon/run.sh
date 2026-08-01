#!/usr/bin/with-contenv bashio
# shellcheck shell=bash

CONFIG_FILE="/etc/eebus-bridge/config.yaml"
SERIAL_FILE="/data/serial"
CERT_STORAGE="/data/certs"

# Options reach the bridge as EEBUS_* overrides (internal/config
# applyEnvOverrides) rather than being written into the YAML. Every one of them
# is a free-text field, and the bridge parses its config strictly: a quote or a
# newline in, say, the brand would either abort the start or inject keys.
export EEBUS_GRPC_PORT="$(bashio::config 'grpc_port')"
export EEBUS_PORT="$(bashio::config 'eebus_port')"
export EEBUS_VENDOR="$(bashio::config 'vendor')"
export EEBUS_BRAND="$(bashio::config 'brand')"
export EEBUS_MODEL="$(bashio::config 'model')"
export EEBUS_CERT_STORAGE="${CERT_STORAGE}"

# The serial is part of the announced EEBUS device identity, so it has to
# survive restarts and updates: a new serial makes the heat pump treat the
# bridge as a different device and forces re-pairing in the vendor app.
# eebus-go rejects an empty one, so generate a stable value into /data when the
# user has not pinned their own.
serial="$(bashio::config 'serial')"
if ! bashio::var.has_value "${serial}"; then
    if [ ! -s "${SERIAL_FILE}" ]; then
        head -c 8 /dev/urandom | od -An -tx1 | tr -d ' \n' > "${SERIAL_FILE}"
    fi
    serial="$(cat "${SERIAL_FILE}")"
    bashio::log.info "No serial configured; using generated serial ${serial}"
fi
export EEBUS_SERIAL="${serial}"

mkdir -p "${CERT_STORAGE}" "$(dirname "${CONFIG_FILE}")"

# LoadFromFile needs a file that parses — an empty one fails with EOF — but
# everything else is already covered by the environment. What is left out here
# falls back to the bridge defaults, in particular grpc.bind 127.0.0.1 with
# security.mode loopback. That is what keeps the plaintext gRPC socket safe:
# the add-on and HA Core share the host network namespace, so the integration
# reaches it on localhost and nothing off-box can reach it at all.
cat > "${CONFIG_FILE}" <<'EOF'
certificates:
  auto_generate: true
EOF

bashio::log.info "Starting EEBUS Bridge (gRPC ${EEBUS_GRPC_PORT}, SHIP ${EEBUS_PORT})"
exec /usr/local/bin/eebus-bridge --config "${CONFIG_FILE}"
