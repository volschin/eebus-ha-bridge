FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /eebus-bridge ./cmd/eebus-bridge

FROM alpine:3.23

RUN apk add --no-cache ca-certificates
COPY --from=builder /eebus-bridge /usr/local/bin/eebus-bridge

VOLUME /data
EXPOSE 50051 4712

# Environment variables:
# - DEBUG_EEBUS_PROTOCOL: Set to "true" to enable detailed EEBUS protocol logging
#   Shows all measurements, signals, and control commands sent/received from EEBUS devices
# - EEBUS_DEBUG_EVENTS: Set to "true" to enable debug event logging
# - EEBUS_GRPC_PORT: Override gRPC port (default: 50051)
# - EEBUS_PORT: Override EEBUS port (default: 4712)

ENTRYPOINT ["eebus-bridge"]
CMD ["--config", "/etc/eebus-bridge/config.yaml"]
