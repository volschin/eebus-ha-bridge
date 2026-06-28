#!/usr/bin/with-contenv bashio

export EEBUS_GRPC_PORT=$(bashio::config "GRPC_PORT")
export EEBUS_PORT=$(bashio::config "EEBUS_PORT")
export EEBUS_VENDOR=$(bashio::config "EEBUS_VENDOR")
export EEBUS_BRAND=$(bashio::config "EEBUS_BRAND")
export EEBUS_MODEL=$(bashio::config "EEBUS_MODEL")

SERIAL=$(bashio::config "EEBUS_SERIAL")
if ! bashio::var.has_value "${SERIAL}"; then
    # Bridge requires a serial; generate and persist a stable one in /data
    SERIAL_FILE="/data/serial"
    if [ ! -s "${SERIAL_FILE}" ]; then
        head -c 8 /dev/urandom | od -An -tx1 | tr -d ' \n' > "${SERIAL_FILE}"
    fi
    SERIAL=$(cat "${SERIAL_FILE}")
    bashio::log.info "No serial configured; using generated serial ${SERIAL}"
fi
export EEBUS_SERIAL="${SERIAL}"

export EEBUS_CERT_STORAGE="/data/certs"
mkdir -p "${EEBUS_CERT_STORAGE}"

CONFIG_FILE="/etc/eebus-bridge/config.yaml"
mkdir -p "$(dirname "${CONFIG_FILE}")"
cat > "${CONFIG_FILE}" <<EOF
grpc:
  port: ${EEBUS_GRPC_PORT}
eebus:
  port: ${EEBUS_PORT}
certificates:
  auto_generate: true
  storage_path: "${EEBUS_CERT_STORAGE}"
EOF

bashio::log.info "Starting EEBUS Bridge"
bashio::log.info "  gRPC port:  ${EEBUS_GRPC_PORT}"
bashio::log.info "  EEBUS port: ${EEBUS_PORT}"
bashio::log.info "  Cert store: ${EEBUS_CERT_STORAGE}"

exec /usr/local/bin/eebus-bridge --config "${CONFIG_FILE}"
