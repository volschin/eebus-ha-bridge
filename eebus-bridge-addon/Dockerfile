ARG BUILD_FROM=ghcr.io/home-assistant/amd64-base:3.20
ARG EEBUS_BRIDGE_VERSION=v0.5.3

# --- Build stage ---
FROM golang:1.25-alpine AS builder

ARG EEBUS_BRIDGE_VERSION
RUN apk add --no-cache git

RUN git clone https://github.com/volschin/eebus-ha-bridge.git /src && \
    cd /src && \
    git switch --detach ${EEBUS_BRIDGE_VERSION}

WORKDIR /src/eebus-bridge
RUN go mod download
RUN CGO_ENABLED=0 go build -o /eebus-bridge ./cmd/eebus-bridge

# --- Runtime stage ---
FROM $BUILD_FROM

RUN apk add --no-cache ca-certificates

COPY --from=builder /eebus-bridge /usr/local/bin/eebus-bridge
COPY run.sh /
RUN chmod a+x /run.sh

CMD [ "/run.sh" ]
