#!/usr/bin/env bash
# Fail if regenerated protobuf outputs differ, including untracked files.
set -euo pipefail

status="$(git status --porcelain -- eebus-bridge/gen/proto/eebus/v1 custom_components/eebus/generated)"
if [[ -n "${status}" ]]; then
  echo "Generated protobuf files are not up to date:" >&2
  echo "${status}" >&2
  exit 1
fi
