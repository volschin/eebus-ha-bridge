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

FROM ghcr.io/sigstore/cosign/cosign:v3.1.3@sha256:9e5c2f2edc34351160407ca3416c61855bdf9403c3c5936e0f0be7fc261611b8 AS cosign
FROM ghcr.io/regclient/regctl:v0.11.5@sha256:dbe356c6cf9f8f85e302b9e47fed481ef3f1b04807350e99b02ab2cadee0a993 AS regctl

# hadolint ignore=DL3006
FROM ${BUILD_FROM} AS verified-bridge
ARG BUILD_VERSION
# Resolve the mutable version tag once; verification and extraction then use one digest.
COPY --from=cosign /ko-app/cosign /usr/local/bin/cosign
COPY --from=regctl /regctl /usr/local/bin/regctl
COPY fetch-verified-bridge.sh /usr/local/bin/fetch-verified-bridge
RUN chmod 0755 /usr/local/bin/fetch-verified-bridge \
    && /usr/local/bin/fetch-verified-bridge \
        "${BUILD_VERSION}" /verified/eebus-bridge

# Tool binaries stay in the verifier stage; only verified output enters runtime.
# hadolint ignore=DL3006
FROM ${BUILD_FROM}
COPY --from=verified-bridge /verified/eebus-bridge /usr/local/bin/eebus-bridge
COPY run.sh /run.sh
RUN chmod a+x /run.sh
CMD [ "/run.sh" ]
