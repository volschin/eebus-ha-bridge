FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /eebus-bridge ./cmd/eebus-bridge

FROM alpine:3.19

RUN apk add --no-cache ca-certificates
COPY --from=builder /eebus-bridge /usr/local/bin/eebus-bridge

VOLUME /data
EXPOSE 50051 4712

ENTRYPOINT ["eebus-bridge"]
CMD ["--config", "/etc/eebus-bridge/config.yaml"]
