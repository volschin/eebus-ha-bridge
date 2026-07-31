# Pinning the builder to BUILDPLATFORM keeps the toolchain running natively on
# the runner. Without it, buildx runs the whole builder stage — including the Go
# compiler — under QEMU for every non-native target, which dominated release
# build time. The binary is CGO_ENABLED=0, so cross-compiling is a plain
# GOARCH/GOARM switch and produces the same static output.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

# hadolint ignore=DL3018
RUN apk add --no-cache git

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

FROM alpine:3.24

# hadolint ignore=DL3018
RUN apk add --no-cache ca-certificates \
 && addgroup -g 101 -S eebus \
 && adduser -u 100 -S -G eebus eebus \
 && mkdir -p /data /etc/eebus-bridge \
 && chown eebus:eebus /data

COPY --from=builder /eebus-bridge /usr/local/bin/eebus-bridge

VOLUME /data
EXPOSE 50051 4712

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["eebus-bridge", "-healthcheck", "--config", "/etc/eebus-bridge/config.yaml"]

# Numeric form satisfies hadolint DL3066 and is resolvable without /etc/passwd.
# 100:101 is exactly what `adduser -S` assigned before, so ownership of the
# existing /data volume is unchanged and no re-pairing is triggered.
USER 100:101
ENTRYPOINT ["eebus-bridge"]
CMD ["--config", "/etc/eebus-bridge/config.yaml"]
