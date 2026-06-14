FROM golang:1.26-alpine AS builder

# hadolint ignore=DL3018
RUN apk add --no-cache git

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /eebus-bridge ./cmd/eebus-bridge

FROM alpine:3.24

# hadolint ignore=DL3018
RUN apk add --no-cache ca-certificates \
 && addgroup -S eebus \
 && adduser -S -G eebus eebus \
 && mkdir -p /data /etc/eebus-bridge \
 && chown eebus:eebus /data

COPY --from=builder /eebus-bridge /usr/local/bin/eebus-bridge

VOLUME /data
EXPOSE 50051 4712

USER eebus
ENTRYPOINT ["eebus-bridge"]
CMD ["--config", "/etc/eebus-bridge/config.yaml"]
