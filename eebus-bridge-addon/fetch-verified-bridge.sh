#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
    echo "usage: $0 <version> <output-path>" >&2
    exit 64
fi

version="$1"
output="$2"
repository="ghcr.io/volschin/eebus-bridge"
issuer="https://token.actions.githubusercontent.com"

if ! printf '%s\n' "${version}" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
    echo "invalid bridge version: ${version}" >&2
    exit 65
fi

tag_ref="${repository}:${version}"
digest="$(regctl image digest "${tag_ref}")"
if ! printf '%s\n' "${digest}" | grep -Eq '^sha256:[0-9a-f]{64}$'; then
    echo "invalid bridge image digest: ${digest}" >&2
    exit 66
fi

image="${repository}@${digest}"
identity="https://github.com/volschin/eebus-ha-bridge/.github/workflows/release.yml@refs/tags/v${version}"

echo "Verifying ${image} from ${identity}"
cosign verify \
    --certificate-identity "${identity}" \
    --certificate-oidc-issuer "${issuer}" \
    "${image}" >/dev/null

mkdir -p "$(dirname "${output}")"
rm -f "${output}"
regctl image get-file --platform local \
    "${image}" \
    /usr/local/bin/eebus-bridge \
    "${output}"
chmod 0755 "${output}"

if [ ! -s "${output}" ] || [ ! -x "${output}" ]; then
    echo "verified bridge output is missing or not executable" >&2
    exit 67
fi
