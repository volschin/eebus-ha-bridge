# Pinning the builder to BUILDPLATFORM keeps the toolchain running natively on
# the runner. Without it, buildx runs the whole builder stage — including the Go
# compiler — under QEMU for every non-native target, which dominated release
# build time. The binary is CGO_ENABLED=0, so cross-compiling is a plain
# GOARCH/GOARM switch and produces the same static output.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

# hadolint ignore=DL3018
RUN apk add --no-cache git

# Stage the runtime filesystem here, where a shell still exists. distroless has
# neither shell nor package manager, so /data and its ownership cannot be
# created in the final stage. Empty directories are arch-independent, so this
# belongs in the BUILDPLATFORM-pinned builder rather than an extra stage that
# buildx would rebuild under QEMU for every non-native target. Kept above the
# source COPY so it stays cached across code changes.
RUN mkdir -p /rootfs/data /rootfs/etc/eebus-bridge \
 && chown -R 100:101 /rootfs/data

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG BUILD_VERSION=dev
# Provided by buildx: TARGETARCH is arm64/amd64/arm, TARGETVARIANT is v7 for
# linux/arm/v7 and empty elsewhere. GOARM only applies to GOARCH=arm and is
# ignored otherwise, so the stripped variant can be passed unconditionally.
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOARM=${TARGETVARIANT#v} go build \
  -ldflags "-X github.com/volschin/eebus-bridge/internal/grpc.BuildVersion=${BUILD_VERSION}" \
  -o /eebus-bridge ./cmd/eebus-bridge

# distroless/static carries ca-certificates, tzdata and /etc/passwd, and nothing
# else: no shell, no package manager, no busybox. The bridge is a static
# CGO_ENABLED=0 binary that makes no outbound HTTP calls and never shells out,
# so none of alpine's userland was reachable at runtime anyway.
# Pinned by digest: distroless publishes no semver tags, so a digest is the only
# way to get a reproducible base and satisfy hadolint DL3006/DL3007.
# renovate: datasource=docker depName=gcr.io/distroless/static-debian12
FROM gcr.io/distroless/static-debian12@sha256:6447365a6337c3732f412d1b74357b30a633831955b2bc45552b0086be907687

COPY --from=builder /rootfs/ /
COPY --from=builder /eebus-bridge /usr/local/bin/eebus-bridge

VOLUME /data
EXPOSE 50051 4712

# Absolute paths: distroless has no shell, so nothing resolves these via PATH
# on our behalf if the image's PATH ever changes.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["/usr/local/bin/eebus-bridge", "-healthcheck", "--config", "/etc/eebus-bridge/config.yaml"]

# Numeric form satisfies hadolint DL3066 and is resolvable without /etc/passwd.
# 100:101 is exactly what `adduser -S` assigned before, so ownership of the
# existing /data volume is unchanged and no re-pairing is triggered.
USER 100:101
ENTRYPOINT ["/usr/local/bin/eebus-bridge"]
CMD ["--config", "/etc/eebus-bridge/config.yaml"]
