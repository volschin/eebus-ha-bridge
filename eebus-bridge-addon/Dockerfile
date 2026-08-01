# BUILD_VERSION is injected by the Supervisor from `version:` in config.yaml.
# Using it as the image tag means the add-on version and the bridge binary can
# never drift apart: bumping config.yaml is what both offers users an update
# and selects the release they get. No default on purpose — a build that does
# not pass it should fail loudly rather than silently pin a stale release.
ARG BUILD_VERSION
# Supervisor supplies BUILD_FROM only for add-ons that still ship a build.yaml,
# which it now warns about as deprecated. The default is what it would have
# passed. Unpinned by digest unlike eebus-bridge/Dockerfile's distroless base:
# there is no digest to pin against here without a lookup, and Renovate can
# pin it later if that becomes worth the churn.
ARG BUILD_FROM=ghcr.io/home-assistant/base:latest

# The release workflow already publishes this binary for linux/amd64,
# linux/arm64 and linux/arm/v7 — the same three platforms `arch:` lists — so
# the add-on copies it out instead of rebuilding. The Supervisor builds add-ons
# on the user's own hardware, where a Go toolchain run costs minutes of CPU and
# can plausibly OOM on a Pi.
FROM ghcr.io/volschin/eebus-bridge:${BUILD_VERSION} AS bridge

# hadolint ignore=DL3006
FROM ${BUILD_FROM}

# The bridge is a static CGO_ENABLED=0 binary that never shells out, so nothing
# else from the release image is needed and the HA base's userland (s6, bashio)
# is only here to read the add-on options.
COPY --from=bridge /usr/local/bin/eebus-bridge /usr/local/bin/eebus-bridge
COPY run.sh /run.sh
RUN chmod a+x /run.sh

CMD [ "/run.sh" ]
