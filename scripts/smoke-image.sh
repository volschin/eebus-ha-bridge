#!/usr/bin/env bash
# Boot the built container image and wait for its own HEALTHCHECK to report
# healthy.
#
# Every static gate can be green while the binary panics on the first line of
# main(): v0.13.0 shipped a typed-nil panic in the use-case wiring with vet,
# lint, race tests and coverage all passing, because nothing ever started the
# thing. This runs the image exactly as docker-compose does — shipped config
# mounted read-only, /data from the image's own VOLUME so cert generation gets
# a writable directory owned by uid 100 — and fails if it does not come up.
#
# Usage: scripts/smoke-image.sh <image-ref> [config-path]
set -euo pipefail

IMAGE="${1:?usage: smoke-image.sh <image-ref> [config-path]}"
CONFIG="${2:-eebus-bridge/config-default.yaml}"
TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-120}"

if [[ ! -f "$CONFIG" ]]; then
  echo "smoke: config not found: $CONFIG" >&2
  exit 1
fi
CONFIG_ABS="$(cd "$(dirname "$CONFIG")" && pwd)/$(basename "$CONFIG")"

NAME="eebus-smoke-$$"

cleanup() {
  echo "--- container logs ---"
  docker logs "$NAME" 2>&1 | tail -40 || true
  docker rm -f "$NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "smoke: starting $IMAGE"
docker run -d --name "$NAME" \
  -v "$CONFIG_ABS":/etc/eebus-bridge/config.yaml:ro \
  "$IMAGE" >/dev/null

deadline=$((SECONDS + TIMEOUT_SECONDS))
while ((SECONDS < deadline)); do
  running="$(docker inspect -f '{{.State.Running}}' "$NAME")"
  if [[ "$running" != "true" ]]; then
    code="$(docker inspect -f '{{.State.ExitCode}}' "$NAME")"
    echo "smoke: container exited early with code $code" >&2
    exit 1
  fi

  # No HEALTHCHECK would leave this at <nil> forever, so treat it as a failure
  # rather than looping until the timeout.
  status="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$NAME")"
  case "$status" in
    healthy)
      echo "smoke: healthy after $((SECONDS))s"
      exit 0
      ;;
    unhealthy)
      echo "smoke: healthcheck reported unhealthy" >&2
      exit 1
      ;;
    none)
      echo "smoke: image defines no HEALTHCHECK" >&2
      exit 1
      ;;
  esac
  sleep 3
done

echo "smoke: still not healthy after ${TIMEOUT_SECONDS}s" >&2
exit 1
