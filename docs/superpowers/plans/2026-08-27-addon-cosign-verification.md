# Verified Add-on Source Image Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Supervisor-built EEBUS add-ons package the bridge binary only after exact Cosign identity verification of the immutable signed release digest.

**Architecture:** A digest-pinned Cosign stage and digest-pinned regctl stage feed an ephemeral verifier stage. A tested shell script resolves the configured version tag once, verifies the resulting multi-platform index digest, extracts the native bridge binary from that same digest, and passes only that binary to the final Home Assistant image. CI builds this path against an already-signed bootstrap release, and release publication is gated on a post-sign add-on build.

**Tech Stack:** Docker BuildKit multi-stage builds, Cosign v3.1.3, regctl v0.11.5, POSIX shell, Python `unittest`, GitHub Actions, Home Assistant Supervisor app devcontainer

**Spec:** `docs/superpowers/specs/2026-08-27-addon-cosign-verification-design.md`

## Global Constraints

- Bootstrap v0.16.6 must be built and signed before fail-closed add-on verification reaches `main`.
- Version changes and release tags remain separate from feature commits.
- `manifest.json` and `eebus-bridge-addon/config.yaml` versions must always match.
- Accept only certificate identity `https://github.com/volschin/eebus-ha-bridge/.github/workflows/release.yml@refs/tags/vX.Y.Z` and issuer `https://token.actions.githubusercontent.com`.
- Verification and extraction must use the same validated `sha256:` digest reference.
- No unsigned fallback, identity regex, transparency-log bypass, or runtime verification.
- Cosign and regctl must remain outside the final add-on image.
- Preserve `amd64`, `aarch64`, and `armv7` support.
- Keep `ghcr.io/home-assistant/base:latest` unchanged in this feature.
- Do not change `run.sh`, EEBUS runtime configuration, host networking, or persistent data.
- Use real external commands only in integration gates; unit tests fake unavoidable registry CLIs through `PATH`.

---

### Task 1: Publish signed bootstrap release v0.16.6

**Files:**
- Modify: `custom_components/eebus/manifest.json:13`
- Modify: `eebus-bridge-addon/config.yaml:7`

**Interfaces:**
- Consumes: merged keyless signing workflow in `.github/workflows/release.yml`
- Produces: public signed image `ghcr.io/volschin/eebus-bridge:0.16.6`

- [ ] **Step 1: Start from current `main` in a clean release branch**

```bash
git switch main
git pull --ff-only origin main
git switch -c release/v0.16.6
```

Expected: clean branch based on `main`; no feature files included.

- [ ] **Step 2: Update both version declarations**

Change `custom_components/eebus/manifest.json`:

```json
"version": "0.16.6",
```

Change `eebus-bridge-addon/config.yaml`:

```yaml
version: "0.16.6"
```

- [ ] **Step 3: Run version-policy gates**

```bash
python3 -m unittest scripts.tests.test_check_addon_version -v
python3 scripts/check_addon_version.py
python3 scripts/check_addon_version.py v0.16.6
```

Expected: all commands exit 0.

- [ ] **Step 4: Commit the isolated release change**

```bash
git add custom_components/eebus/manifest.json eebus-bridge-addon/config.yaml
git commit -m "chore: prepare v0.16.6 release"
```

- [ ] **Step 5: Push, open, verify, and merge the release PR**

```bash
git push -u origin release/v0.16.6
gh pr create --base main --head release/v0.16.6 \
  --title "chore: prepare v0.16.6 release" \
  --body "Prepare v0.16.6 as the signed bootstrap image required by add-on source verification."
gh pr checks --watch --interval 30
```

Before merge, confirm all checks pass and no unresolved review threads exist, then run:

```bash
gh pr merge --squash --delete-branch
```

- [ ] **Step 6: Tag merged release commit**

```bash
git switch main
git pull --ff-only origin main
git tag -a v0.16.6 -m "v0.16.6"
git push origin v0.16.6
```

- [ ] **Step 7: Watch release workflow through image signing and publication**

```bash
run_id="$(gh run list \
  --workflow Release \
  --commit "$(git rev-list -n 1 v0.16.6)" \
  --limit 1 \
  --json databaseId \
  --jq '.[0].databaseId')"
test -n "${run_id}"
gh run watch "${run_id}" --exit-status
```

Expected: `publish-image`, `sign-image`, and `create-release` succeed; GitHub Release v0.16.6 is published.

- [ ] **Step 8: Verify the bootstrap image independently**

```bash
image="ghcr.io/volschin/eebus-bridge:0.16.6"
digest="$(docker buildx imagetools inspect "${image}" | awk '/^Digest:/ {print $2; exit}')"
test -n "${digest}"
docker run --rm \
  ghcr.io/sigstore/cosign/cosign:v3.1.3@sha256:9e5c2f2edc34351160407ca3416c61855bdf9403c3c5936e0f0be7fc261611b8 \
  verify \
  --certificate-identity \
  "https://github.com/volschin/eebus-ha-bridge/.github/workflows/release.yml@refs/tags/v0.16.6" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  "ghcr.io/volschin/eebus-bridge@${digest}"
```

Expected: Cosign exits 0 and reports a verified signature.

---

### Task 2: Implement fail-closed bridge fetch script with TDD

**Files:**
- Create: `eebus-bridge-addon/fetch-verified-bridge.sh`
- Create: `scripts/tests/test_fetch_verified_bridge.py`
- Include in commit: `docs/superpowers/specs/2026-08-27-addon-cosign-verification-design.md`
- Include in commit: `docs/superpowers/plans/2026-08-27-addon-cosign-verification.md`

**Interfaces:**
- Consumes: `cosign` and `regctl` available on `PATH`
- Produces: `fetch-verified-bridge.sh <version> <output-path>`, exiting 0 only with a non-empty executable verified binary

- [ ] **Step 1: Rebase feature branch onto signed v0.16.6 `main`**

```bash
git switch feat/addon-image-verification
git rebase main
```

Expected: `manifest.json` and add-on `config.yaml` both contain `0.16.6`.

- [ ] **Step 2: Write failing script tests**

Create `scripts/tests/test_fetch_verified_bridge.py` with a `unittest.TestCase` that:

```python
from __future__ import annotations

import os
from pathlib import Path
import stat
import subprocess
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).parents[2]
SCRIPT = ROOT / "eebus-bridge-addon" / "fetch-verified-bridge.sh"
VALID_DIGEST = "sha256:" + "a" * 64
EXPECTED_IMAGE = f"ghcr.io/volschin/eebus-bridge@{VALID_DIGEST}"
EXPECTED_IDENTITY = (
    "https://github.com/volschin/eebus-ha-bridge/.github/workflows/"
    "release.yml@refs/tags/v0.16.6"
)


class FetchVerifiedBridgeTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        self.root = Path(self.temp_dir.name)
        self.bin_dir = self.root / "bin"
        self.bin_dir.mkdir()
        self.log = self.root / "calls.log"
        self.output = self.root / "eebus-bridge"
        self._write_fake(
            "regctl",
            r"""
            printf 'regctl %s\n' "$*" >>"${CALL_LOG}"
            if [ "$1 $2" = "image digest" ]; then
                [ "${FAKE_DIGEST_EXIT:-0}" -eq 0 ] || exit "${FAKE_DIGEST_EXIT}"
                printf '%s\n' "${FAKE_DIGEST:-sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}"
                exit 0
            fi
            if [ "$1 $2" = "image get-file" ]; then
                [ "${FAKE_GET_FILE_EXIT:-0}" -eq 0 ] || exit "${FAKE_GET_FILE_EXIT}"
                if [ "${FAKE_EMPTY_OUTPUT:-0}" -eq 0 ]; then
                    printf 'verified-binary' >"$7"
                else
                    : >"$7"
                fi
                exit 0
            fi
            exit 90
            """,
        )
        self._write_fake(
            "cosign",
            r"""
            printf 'cosign %s\n' "$*" >>"${CALL_LOG}"
            exit "${FAKE_COSIGN_EXIT:-0}"
            """,
        )

    def tearDown(self) -> None:
        self.temp_dir.cleanup()

    def _write_fake(self, name: str, body: str) -> None:
        path = self.bin_dir / name
        path.write_text("#!/bin/sh\nset -eu\n" + textwrap.dedent(body))
        path.chmod(0o755)

    def _run(self, version: str = "0.16.6", **extra_env: str) -> subprocess.CompletedProcess[str]:
        env = os.environ.copy()
        env.update(
            {
                "PATH": f"{self.bin_dir}:/usr/bin:/bin",
                "CALL_LOG": str(self.log),
                **extra_env,
            }
        )
        return subprocess.run(
            [str(SCRIPT), version, str(self.output)],
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )
```

Add focused test methods asserting:

```python
def test_rejects_non_semver_before_registry_access(self) -> None:
    result = self._run("v0.16.6")
    self.assertNotEqual(result.returncode, 0)
    self.assertFalse(self.log.exists())


def test_rejects_malformed_digest_before_cosign(self) -> None:
    result = self._run(FAKE_DIGEST="sha256:bad")
    self.assertNotEqual(result.returncode, 0)
    calls = self.log.read_text()
    self.assertIn("regctl image digest", calls)
    self.assertNotIn("cosign", calls)


def test_cosign_failure_prevents_extraction(self) -> None:
    result = self._run(FAKE_COSIGN_EXIT="17")
    self.assertEqual(result.returncode, 17)
    calls = self.log.read_text()
    self.assertIn("cosign verify", calls)
    self.assertNotIn("get-file", calls)


def test_success_verifies_and_extracts_same_digest(self) -> None:
    result = self._run()
    self.assertEqual(result.returncode, 0, result.stderr)
    calls = self.log.read_text()
    self.assertEqual(calls.count(EXPECTED_IMAGE), 2)
    self.assertIn(EXPECTED_IDENTITY, calls)
    self.assertIn("https://token.actions.githubusercontent.com", calls)
    self.assertIn("regctl image get-file --platform local", calls)
    self.assertEqual(self.output.read_text(), "verified-binary")
    self.assertTrue(self.output.stat().st_mode & stat.S_IXUSR)


def test_empty_extracted_file_fails_closed(self) -> None:
    result = self._run(FAKE_EMPTY_OUTPUT="1")
    self.assertNotEqual(result.returncode, 0)
```

Add failure-propagation methods in the same class:

```python
def test_digest_resolution_failure_propagates(self) -> None:
    result = self._run(FAKE_DIGEST_EXIT="23")
    self.assertEqual(result.returncode, 23)
    calls = self.log.read_text()
    self.assertIn("regctl image digest", calls)
    self.assertNotIn("cosign", calls)
    self.assertNotIn("get-file", calls)


def test_extraction_failure_propagates(self) -> None:
    result = self._run(FAKE_GET_FILE_EXIT="29")
    self.assertEqual(result.returncode, 29)
    calls = self.log.read_text()
    self.assertIn("cosign verify", calls)
    self.assertIn("regctl image get-file", calls)
    self.assertFalse(self.output.exists())
```

Add `if __name__ == "__main__": unittest.main()` after the class.

- [ ] **Step 3: Run tests and verify RED**

```bash
python3 -m unittest scripts.tests.test_fetch_verified_bridge -v
```

Expected: FAIL because `eebus-bridge-addon/fetch-verified-bridge.sh` does not exist.

- [ ] **Step 4: Implement minimal production script**

Create `eebus-bridge-addon/fetch-verified-bridge.sh`:

```sh
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
```

Mark it executable:

```bash
chmod 0755 eebus-bridge-addon/fetch-verified-bridge.sh
```

- [ ] **Step 5: Run tests and verify GREEN**

```bash
python3 -m unittest scripts.tests.test_fetch_verified_bridge -v
```

Expected: all tests pass with no warnings.

- [ ] **Step 6: Commit script, tests, spec, and plan**

```bash
git add \
  eebus-bridge-addon/fetch-verified-bridge.sh \
  scripts/tests/test_fetch_verified_bridge.py \
  docs/superpowers/specs/2026-08-27-addon-cosign-verification-design.md \
  docs/superpowers/plans/2026-08-27-addon-cosign-verification.md
git commit -m "feat: add verified bridge image fetcher"
```

---

### Task 3: Replace bridge `FROM` with verified extraction stage

**Files:**
- Modify: `eebus-bridge-addon/Dockerfile:1-31`
- Create: `scripts/tests/test_addon_dockerfile_supply_chain.py`

**Interfaces:**
- Consumes: `fetch-verified-bridge.sh <version> <output-path>` from Task 2
- Produces: final add-on image containing only `/usr/local/bin/eebus-bridge` from verified source digest

- [ ] **Step 1: Write failing Dockerfile contract tests**

Create `scripts/tests/test_addon_dockerfile_supply_chain.py`:

```python
from pathlib import Path
import re
import unittest


DOCKERFILE = Path(__file__).parents[2] / "eebus-bridge-addon" / "Dockerfile"
COSIGN_REF = (
    "ghcr.io/sigstore/cosign/cosign:v3.1.3@sha256:"
    "9e5c2f2edc34351160407ca3416c61855bdf9403c3c5936e0f0be7fc261611b8"
)
REGCTL_REF = (
    "ghcr.io/regclient/regctl:v0.11.5@sha256:"
    "dbe356c6cf9f8f85e302b9e47fed481ef3f1b04807350e99b02ab2cadee0a993"
)


class AddonDockerfileSupplyChainTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.text = DOCKERFILE.read_text()

    def test_bridge_registry_is_not_a_from_source(self) -> None:
        self.assertNotRegex(
            self.text,
            r"(?m)^FROM\s+ghcr\.io/volschin/eebus-bridge",
        )

    def test_verifier_tools_are_tagged_and_digest_pinned(self) -> None:
        self.assertIn(f"FROM {COSIGN_REF} AS cosign", self.text)
        self.assertIn(f"FROM {REGCTL_REF} AS regctl", self.text)

    def test_final_stage_copies_only_verified_bridge(self) -> None:
        final_stage = self.text.rsplit("FROM ${BUILD_FROM}", 1)[1]
        self.assertIn(
            "COPY --from=verified-bridge /verified/eebus-bridge "
            "/usr/local/bin/eebus-bridge",
            final_stage,
        )
        self.assertNotIn("cosign", final_stage)
        self.assertNotIn("regctl", final_stage)
        self.assertNotIn("fetch-verified-bridge", final_stage)


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 2: Run tests and verify RED**

```bash
python3 -m unittest scripts.tests.test_addon_dockerfile_supply_chain -v
```

Expected: failures show the bridge registry is still a `FROM` source and pinned tool stages are absent.

- [ ] **Step 3: Replace Dockerfile stages**

Rewrite `eebus-bridge-addon/Dockerfile` to preserve existing comments where still accurate and use this structure:

```dockerfile
ARG BUILD_VERSION
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
```

This exact structure documents both properties: tag resolution happens once inside the script, and tool binaries never reach the final stage.

- [ ] **Step 4: Run contract tests and Hadolint**

```bash
python3 -m unittest scripts.tests.test_addon_dockerfile_supply_chain -v
docker run --rm -i hadolint/hadolint:v2.14.0-alpine \
  hadolint - < eebus-bridge-addon/Dockerfile
```

Expected: all tests and Hadolint pass.

- [ ] **Step 5: Build against signed v0.16.6 on amd64**

```bash
docker build \
  --platform linux/amd64 \
  --build-arg BUILD_VERSION=0.16.6 \
  --tag eebus-bridge-addon:verified-test \
  eebus-bridge-addon

docker run --rm --entrypoint /bin/sh eebus-bridge-addon:verified-test -c \
  'test -x /usr/local/bin/eebus-bridge &&
   test ! -e /usr/local/bin/cosign &&
   test ! -e /usr/local/bin/regctl &&
   test ! -e /usr/local/bin/fetch-verified-bridge'
```

Expected: build log shows exact v0.16.6 digest and successful Cosign verification; final-image assertions pass.

- [ ] **Step 6: Commit Dockerfile integration**

```bash
git add eebus-bridge-addon/Dockerfile scripts/tests/test_addon_dockerfile_supply_chain.py
git commit -m "feat: verify add-on bridge image during build"
```

---

### Task 4: Add recurring tool-platform and PR integration gates

**Files:**
- Create: `scripts/check_addon_tool_platforms.py`
- Create: `scripts/tests/test_check_addon_tool_platforms.py`
- Modify: `.github/workflows/ci.yml:215-236`

**Interfaces:**
- Consumes: digest-pinned Dockerfile `FROM` references with stage aliases `cosign` and `regctl`
- Produces: CLI `python3 scripts/check_addon_tool_platforms.py eebus-bridge-addon/Dockerfile`

- [ ] **Step 1: Write failing pure-function tests for platform validation**

Create `scripts/tests/test_check_addon_tool_platforms.py`:

```python
from __future__ import annotations

import unittest

from scripts.check_addon_tool_platforms import (
    manifest_platforms,
    missing_platforms,
    tool_images,
)


COSIGN = "ghcr.io/sigstore/cosign/cosign:v3.1.3@sha256:" + "a" * 64
REGCTL = "ghcr.io/regclient/regctl:v0.11.5@sha256:" + "b" * 64
DOCKERFILE = f"""\
FROM {COSIGN} AS cosign
FROM {REGCTL} AS regctl
FROM base AS final
"""
INDEX = {
    "manifests": [
        {"platform": {"os": "linux", "architecture": "amd64"}},
        {"platform": {"os": "linux", "architecture": "arm64"}},
        {
            "platform": {
                "os": "linux",
                "architecture": "arm",
                "variant": "v7",
            }
        },
    ]
}


class AddonToolPlatformsTest(unittest.TestCase):
    def test_extracts_digest_pinned_tool_stages(self) -> None:
        self.assertEqual(tool_images(DOCKERFILE), [COSIGN, REGCTL])

    def test_rejects_missing_tool_stage(self) -> None:
        with self.assertRaisesRegex(ValueError, "missing tool stage: regctl"):
            tool_images(f"FROM {COSIGN} AS cosign\n")

    def test_rejects_tool_without_full_digest(self) -> None:
        dockerfile = DOCKERFILE.replace(
            COSIGN,
            "ghcr.io/sigstore/cosign/cosign:v3.1.3",
        )
        with self.assertRaisesRegex(ValueError, "not digest-pinned"):
            tool_images(dockerfile)

    def test_normalizes_manifest_platforms(self) -> None:
        self.assertEqual(
            manifest_platforms(INDEX),
            {"linux/amd64", "linux/arm64", "linux/arm/v7"},
        )

    def test_reports_missing_arm_v7(self) -> None:
        self.assertEqual(
            missing_platforms({"linux/amd64", "linux/arm64"}),
            {"linux/arm/v7"},
        )


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 2: Run tests and verify RED**

```bash
python3 -m unittest scripts.tests.test_check_addon_tool_platforms -v
```

Expected: import failure because checker does not exist.

- [ ] **Step 3: Implement platform checker**

Create `scripts/check_addon_tool_platforms.py`:

```python
#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
from pathlib import Path
import re
import subprocess
import sys
from typing import Any


REQUIRED_PLATFORMS = {"linux/amd64", "linux/arm64", "linux/arm/v7"}
TOOL_STAGES = ("cosign", "regctl")
FROM_RE = re.compile(
    r"(?mi)^FROM\s+(?P<image>\S+)\s+AS\s+(?P<stage>cosign|regctl)\s*$"
)
DIGEST_RE = re.compile(r"@sha256:[0-9a-f]{64}$")


def tool_images(dockerfile: str) -> list[str]:
    images = {match.group("stage"): match.group("image") for match in FROM_RE.finditer(dockerfile)}
    for stage in TOOL_STAGES:
        if stage not in images:
            raise ValueError(f"missing tool stage: {stage}")
        if DIGEST_RE.search(images[stage]) is None:
            raise ValueError(f"tool stage {stage} is not digest-pinned: {images[stage]}")
    return [images[stage] for stage in TOOL_STAGES]


def manifest_platforms(index: dict[str, Any]) -> set[str]:
    manifests = index.get("manifests")
    if not isinstance(manifests, list):
        raise ValueError("image reference did not resolve to an OCI index")

    platforms: set[str] = set()
    for descriptor in manifests:
        if not isinstance(descriptor, dict):
            continue
        platform = descriptor.get("platform")
        if not isinstance(platform, dict):
            continue
        os_name = platform.get("os")
        architecture = platform.get("architecture")
        variant = platform.get("variant")
        if not isinstance(os_name, str) or not isinstance(architecture, str):
            continue
        value = f"{os_name}/{architecture}"
        if isinstance(variant, str) and variant:
            value = f"{value}/{variant}"
        platforms.add(value)
    return platforms


def missing_platforms(platforms: set[str]) -> set[str]:
    return REQUIRED_PLATFORMS - platforms


def inspect_index(image: str) -> dict[str, Any]:
    result = subprocess.run(
        ["docker", "buildx", "imagetools", "inspect", "--raw", image],
        check=True,
        capture_output=True,
        text=True,
    )
    value = json.loads(result.stdout)
    if not isinstance(value, dict):
        raise ValueError(f"invalid OCI index for {image}")
    return value


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("dockerfile", type=Path)
    args = parser.parse_args()

    try:
        images = tool_images(args.dockerfile.read_text())
        for image in images:
            missing = missing_platforms(manifest_platforms(inspect_index(image)))
            if missing:
                names = ", ".join(sorted(missing))
                raise ValueError(f"{image} is missing platforms: {names}")
            print(f"{image}: required platforms present")
    except (OSError, ValueError, json.JSONDecodeError, subprocess.CalledProcessError) as error:
        print(error, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
```

Mark it executable:

```bash
chmod 0755 scripts/check_addon_tool_platforms.py
```

- [ ] **Step 4: Run tests and verify GREEN**

```bash
python3 -m unittest scripts.tests.test_check_addon_tool_platforms -v
python3 scripts/check_addon_tool_platforms.py eebus-bridge-addon/Dockerfile
```

Expected: tests pass; both pinned tool images report all required platforms.

- [ ] **Step 5: Extend add-on CI job**

After checkout, add the existing SHA-pinned Buildx setup action:

```yaml
      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@37fe631027851001ddb9b187196cc803df7f5f0e # v4.3.0 # renovate: datasource=github-releases depName=docker/setup-buildx-action
```

After version synchronization, add:

```yaml
      - name: Test verified bridge fetcher
        run: |
          python3 -m unittest scripts.tests.test_fetch_verified_bridge -v
          python3 -m unittest scripts.tests.test_addon_dockerfile_supply_chain -v
          python3 -m unittest scripts.tests.test_check_addon_tool_platforms -v

      - name: Check verifier tool platforms
        run: python3 scripts/check_addon_tool_platforms.py eebus-bridge-addon/Dockerfile

      - name: Build verified add-on image
        run: |
          set -euo pipefail
          version="$(python3 -c 'import re; from pathlib import Path; text=Path("eebus-bridge-addon/config.yaml").read_text(); print(re.search(r"(?m)^version: \"([^\"]+)\"$", text).group(1))')"
          docker build \
            --platform linux/amd64 \
            --build-arg "BUILD_VERSION=${version}" \
            --tag eebus-bridge-addon:ci \
            eebus-bridge-addon
          docker run --rm --entrypoint /bin/sh eebus-bridge-addon:ci -c \
            'test -x /usr/local/bin/eebus-bridge &&
             test ! -e /usr/local/bin/cosign &&
             test ! -e /usr/local/bin/regctl &&
             test ! -e /usr/local/bin/fetch-verified-bridge'
```

- [ ] **Step 6: Validate workflow and full add-on gate locally**

```bash
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/ci.yml
python3 -m unittest \
  scripts.tests.test_fetch_verified_bridge \
  scripts.tests.test_addon_dockerfile_supply_chain \
  scripts.tests.test_check_addon_tool_platforms -v
python3 scripts/check_addon_tool_platforms.py eebus-bridge-addon/Dockerfile
```

Expected: every command exits 0.

- [ ] **Step 7: Commit CI gates**

```bash
git add \
  scripts/check_addon_tool_platforms.py \
  scripts/tests/test_check_addon_tool_platforms.py \
  .github/workflows/ci.yml
git commit -m "ci: test verified add-on image builds"
```

---

### Task 5: Gate future releases on verified add-on build

**Files:**
- Modify: `.github/workflows/release.yml:12-26,117-end`
- Create: `scripts/tests/test_release_addon_gate.py`

**Interfaces:**
- Consumes: signed version tag image produced by `sign-image`
- Produces: `build-verified-addon` release job required before `create-release`

- [ ] **Step 1: Write failing workflow contract test**

Create `scripts/tests/test_release_addon_gate.py`:

```python
from __future__ import annotations

from pathlib import Path
import re
import unittest


WORKFLOW = Path(__file__).parents[2] / ".github" / "workflows" / "release.yml"


def job_block(workflow: str, name: str) -> str:
    match = re.search(
        rf"(?ms)^  {re.escape(name)}:\n.*?(?=^  [a-z][a-z0-9-]*:\n|\Z)",
        workflow,
    )
    if match is None:
        raise AssertionError(f"job not found: {name}")
    return match.group(0)


class ReleaseAddonGateTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.workflow = WORKFLOW.read_text()

    def test_release_waits_for_verified_addon_build(self) -> None:
        create_release = job_block(self.workflow, "create-release")
        self.assertIn("needs: build-verified-addon", create_release)

    def test_verified_addon_build_waits_for_signing(self) -> None:
        build_job = job_block(self.workflow, "build-verified-addon")
        self.assertIn("needs: sign-image", build_job)
        self.assertIn('version="${RELEASE_TAG#v}"', build_job)
        self.assertIn('--build-arg "BUILD_VERSION=${version}"', build_job)
        self.assertIn("eebus-bridge-addon", build_job)

    def test_verified_addon_build_has_read_only_permissions(self) -> None:
        build_job = job_block(self.workflow, "build-verified-addon")
        permissions = build_job.split("    steps:", 1)[0]
        self.assertIn("contents: read", permissions)
        self.assertNotIn("id-token: write", permissions)
        self.assertNotIn("packages: write", permissions)


if __name__ == "__main__":
    unittest.main()
```

The test locates two-space workflow job blocks directly and requires no PyYAML dependency.

- [ ] **Step 2: Run test and verify RED**

```bash
python3 -m unittest scripts.tests.test_release_addon_gate -v
```

Expected: failures show `create-release` still depends on `sign-image` and no verified add-on job exists.

- [ ] **Step 3: Add release job and dependency**

Change `create-release` to:

```yaml
    needs: build-verified-addon
```

Append after `sign-image`:

```yaml
  build-verified-addon:
    needs: sign-image
    runs-on: ubuntu-latest
    permissions:
      contents: read
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false

      - name: Build add-on from verified release image
        env:
          RELEASE_TAG: ${{ github.ref_name }}
        run: |
          set -euo pipefail
          version="${RELEASE_TAG#v}"
          docker build \
            --platform linux/amd64 \
            --build-arg "BUILD_VERSION=${version}" \
            --tag eebus-bridge-addon:release-verify \
            eebus-bridge-addon
          docker run --rm --entrypoint /bin/sh \
            eebus-bridge-addon:release-verify -c \
            'test -x /usr/local/bin/eebus-bridge &&
             test ! -e /usr/local/bin/cosign &&
             test ! -e /usr/local/bin/regctl &&
             test ! -e /usr/local/bin/fetch-verified-bridge'
```

The environment indirection prevents direct GitHub-expression interpolation inside shell code and preserves command-injection hardening.

- [ ] **Step 4: Run workflow tests and actionlint**

```bash
python3 -m unittest scripts.tests.test_release_addon_gate -v
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 \
  .github/workflows/release.yml
```

Expected: tests and actionlint pass.

- [ ] **Step 5: Run complete relevant verification set**

```bash
git diff --check
python3 -m unittest \
  scripts.tests.test_check_addon_version \
  scripts.tests.test_fetch_verified_bridge \
  scripts.tests.test_addon_dockerfile_supply_chain \
  scripts.tests.test_check_addon_tool_platforms \
  scripts.tests.test_release_addon_gate -v
python3 scripts/check_addon_version.py
python3 scripts/check_addon_tool_platforms.py eebus-bridge-addon/Dockerfile
```

Expected: all checks pass.

- [ ] **Step 6: Commit release gate**

```bash
git add .github/workflows/release.yml scripts/tests/test_release_addon_gate.py
git commit -m "ci: gate releases on verified add-on build"
```

---

### Task 6: Validate real Supervisor install, rebuild, and startup

**Files:**
- No committed file changes

**Interfaces:**
- Consumes: feature branch with signed v0.16.6 source image
- Produces: recorded acceptance evidence for PR description

- [ ] **Step 1: Configure official Home Assistant app devcontainer in an isolated test clone**

Create a disposable clone from the fully committed feature branch, then copy official devcontainer files into that clone:

```bash
test_root="$(mktemp -d /tmp/eebus-supervisor-cosign.XXXXXX)"
printf '%s\n' "${test_root}" > /tmp/eebus-supervisor-cosign.path
git clone --local . "${test_root}"
mkdir -p "${test_root}/.devcontainer" "${test_root}/.vscode"
curl -fsSL \
  https://github.com/home-assistant/devcontainer/raw/main/apps/devcontainer.json \
  -o "${test_root}/.devcontainer/devcontainer.json"
curl -fsSL \
  https://github.com/home-assistant/devcontainer/raw/main/apps/tasks.json \
  -o "${test_root}/.vscode/tasks.json"
code "${test_root}"
```

In VS Code, run `Dev Containers: Reopen in Container`, then task `Start Home Assistant`. All later Task 6 commands run inside this disposable devcontainer.

- [ ] **Step 2: Install local app through real Supervisor**

```bash
ha apps install local_eebus_bridge
ha apps info local_eebus_bridge
ha apps logs --follow local_eebus_bridge
```

Expected: Supervisor builds the local Dockerfile, verification succeeds, and app reaches started/running state.

- [ ] **Step 3: Verify restart has no registry dependency**

```bash
ha apps restart local_eebus_bridge
ha apps info local_eebus_bridge
```

Expected: restart does not rebuild and logs contain no Cosign/regctl operation.

- [ ] **Step 4: Verify forced rebuild succeeds**

```bash
ha apps rebuild --force local_eebus_bridge
ha apps start local_eebus_bridge
ha apps info local_eebus_bridge
```

Expected: rebuild logs show v0.16.6 digest resolution and exact identity verification.

- [ ] **Step 5: Verify wrong identity fails closed**

Temporarily change repository name in the disposable clone's expected certificate identity:

```bash
python3 - <<'PY'
from pathlib import Path

path = Path("eebus-bridge-addon/fetch-verified-bridge.sh")
old = "github.com/volschin/eebus-ha-bridge/.github/workflows"
new = "github.com/volschin/eebus-ha-bridge-invalid/.github/workflows"
text = path.read_text()
if text.count(old) != 1:
    raise SystemExit(f"expected one identity occurrence, found {text.count(old)}")
path.write_text(text.replace(old, new))
PY
ha apps rebuild --force local_eebus_bridge
```

Expected: nonzero rebuild result; logs show Cosign identity mismatch; no unverified replacement image is produced.

Restore production identity and prove recovery:

```bash
git restore eebus-bridge-addon/fetch-verified-bridge.sh
ha apps rebuild --force local_eebus_bridge
ha apps start local_eebus_bridge
ha apps info local_eebus_bridge
```

Expected: rebuild, start, and info commands succeed.

- [ ] **Step 6: Remove isolated test clone**

Close devcontainer and VS Code window. From original checkout:

```bash
test_root="$(tr -d '\n' < /tmp/eebus-supervisor-cosign.path)"
test -n "${test_root}"
rm -rf "${test_root}"
rm -f /tmp/eebus-supervisor-cosign.path
git status --short
```

Expected: original feature checkout contains no devcontainer files or acceptance-test edits.

---

### Task 7: Review and merge feature PR

**Files:**
- Review all feature changes

**Interfaces:**
- Consumes: Tasks 2-6
- Produces: merged fail-closed verifier on `main`

- [ ] **Step 1: Run fresh final verification**

```bash
git diff --check
python3 -m unittest \
  scripts.tests.test_check_addon_version \
  scripts.tests.test_fetch_verified_bridge \
  scripts.tests.test_addon_dockerfile_supply_chain \
  scripts.tests.test_check_addon_tool_platforms \
  scripts.tests.test_release_addon_gate -v
python3 scripts/check_addon_version.py
python3 scripts/check_addon_tool_platforms.py eebus-bridge-addon/Dockerfile
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 \
  .github/workflows/ci.yml .github/workflows/release.yml
docker build \
  --platform linux/amd64 \
  --build-arg BUILD_VERSION=0.16.6 \
  --tag eebus-bridge-addon:final-verify \
  eebus-bridge-addon
```

Expected: all commands exit 0.

- [ ] **Step 2: Request independent functional and security reviews**

Review requirements:

- same digest reaches Cosign and regctl extraction;
- no fail-open path;
- exact identity and issuer;
- no tool binary in final image;
- job permissions remain least privilege;
- release publication waits for verified add-on build;
- all three architectures remain covered.

Fix every Critical or Important finding and rerun Step 1.

- [ ] **Step 3: Push feature branch and create PR**

```bash
python3 - <<'PY'
from pathlib import Path

Path("/tmp/addon-verification-pr.md").write_text("""\
## Summary

- replace unsigned bridge `FROM` stage with digest resolution, exact Cosign identity verification, and same-digest binary extraction
- keep Cosign, regctl, and verifier script outside final add-on image
- gate pull requests and releases with verified add-on builds

## Security boundary

This rejects registry tag substitution and unsigned or differently signed images. It does not protect against compromise of authorized `release.yml` workflow.

## Verification

- signed bootstrap image v0.16.6 verified against exact workflow identity and GitHub OIDC issuer
- shell, Dockerfile contract, platform manifest, version, and release workflow tests pass
- amd64 add-on build passes and final image contains no verifier tools
- Supervisor devcontainer install, restart, forced rebuild, negative identity, and recovery checks pass

## Rollout

After merge, separate v0.16.7 release delivers verified build path to existing installations.
""")
PY
git push -u origin feat/addon-image-verification
gh pr create --base main --head feat/addon-image-verification \
  --title "feat: verify add-on bridge image provenance" \
  --body-file /tmp/addon-verification-pr.md
```

Expected: PR body records architecture, v0.16.6 bootstrap evidence, automated checks, Supervisor result, threat-model boundary, and v0.16.7 rollout.

- [ ] **Step 4: Babysit CI and review threads**

Wait until all checks complete. Read comments and unresolved threads, verify each finding, apply focused fixes with tests, and push. Repeat until checks are green and unresolved actionable thread count is zero.

- [ ] **Step 5: Merge feature PR**

After all checks pass and unresolved actionable thread count is zero:

```bash
gh pr merge --squash --delete-branch
git switch main
git pull --ff-only origin main
git status --short --branch
```

Expected: PR merged, remote feature branch deleted, local `main` clean and synchronized.

---

### Task 8: Release v0.16.7 and verify end-to-end gate

**Files:**
- Modify: `custom_components/eebus/manifest.json:13`
- Modify: `eebus-bridge-addon/config.yaml:7`

**Interfaces:**
- Consumes: merged verifier and release gate
- Produces: v0.16.7 update delivered to existing add-on installations

- [ ] **Step 1: Create isolated release branch and bump both versions**

```bash
git switch main
git pull --ff-only origin main
git switch -c release/v0.16.7
```

Set `custom_components/eebus/manifest.json` to:

```json
"version": "0.16.7",
```

Set `eebus-bridge-addon/config.yaml` to:

```yaml
version: "0.16.7"
```

Run and commit:

```bash
python3 -m unittest scripts.tests.test_check_addon_version -v
python3 scripts/check_addon_version.py
python3 scripts/check_addon_version.py v0.16.7
git add custom_components/eebus/manifest.json eebus-bridge-addon/config.yaml
git commit -m "chore: prepare v0.16.7 release"
```

- [ ] **Step 2: Open and merge release PR after green CI**

```bash
git push -u origin release/v0.16.7
gh pr create --base main --head release/v0.16.7 \
  --title "chore: prepare v0.16.7 release" \
  --body "Prepare v0.16.7 to deliver verified add-on source image builds."
gh pr checks --watch --interval 30
```

Confirm all checks pass, changed files are only two version declarations, and no unresolved review threads exist. Then run:

```bash
gh pr merge --squash --delete-branch
```

- [ ] **Step 3: Tag v0.16.7 and watch full gated release**

```bash
git switch main
git pull --ff-only origin main
git tag -a v0.16.7 -m "v0.16.7"
git push origin v0.16.7
run_id="$(gh run list \
  --workflow Release \
  --commit "$(git rev-list -n 1 v0.16.7)" \
  --limit 1 \
  --json databaseId \
  --jq '.[0].databaseId')"
test -n "${run_id}"
gh run watch "${run_id}" --exit-status
```

Expected job order and result:

```text
publish-image: success
sign-image: success
build-verified-addon: success
create-release: success
```

- [ ] **Step 4: Verify published image identity independently**

```bash
image="ghcr.io/volschin/eebus-bridge:0.16.7"
digest="$(docker buildx imagetools inspect "${image}" | awk '/^Digest:/ {print $2; exit}')"
test -n "${digest}"
docker run --rm \
  ghcr.io/sigstore/cosign/cosign:v3.1.3@sha256:9e5c2f2edc34351160407ca3416c61855bdf9403c3c5936e0f0be7fc261611b8 \
  verify \
  --certificate-identity \
  "https://github.com/volschin/eebus-ha-bridge/.github/workflows/release.yml@refs/tags/v0.16.7" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  "ghcr.io/volschin/eebus-bridge@${digest}"
```

Expected: Cosign exits 0 and reports signature from exact v0.16.7 release workflow identity.

- [ ] **Step 5: Confirm final repository and release state**

```bash
python3 scripts/check_addon_version.py
gh release view v0.16.7
git status --short --branch
```

Expected: versions match, release is published, local `main` is clean and synchronized with `origin/main`.
