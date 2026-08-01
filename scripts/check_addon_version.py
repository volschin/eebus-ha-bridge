"""Fail when the add-on version and the integration version disagree.

The Supervisor hands the add-on's ``version`` to the build as ``BUILD_VERSION``,
and eebus-bridge-addon/Dockerfile uses that as the tag of the published bridge
image. Bumping manifest.json without bumping the add-on therefore leaves add-on
users on the previous binary with no update offered, and bumping the add-on
first points it at an image tag that does not exist yet.

Read with a regex rather than a YAML parser: the runner has no third-party
packages installed for this job, and the field is a plain scalar.
"""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
ADDON_CONFIG = ROOT / "eebus-bridge-addon" / "config.yaml"
MANIFEST = ROOT / "custom_components" / "eebus" / "manifest.json"

ADDON_VERSION_RE = re.compile(r"""^version:\s*["']?([^"'\s#]+)["']?\s*(?:#.*)?$""", re.MULTILINE)


def addon_version() -> str:
    """Return the ``version`` scalar from the add-on config."""
    match = ADDON_VERSION_RE.search(ADDON_CONFIG.read_text())
    if match is None:
        raise SystemExit(f"no top-level 'version:' found in {ADDON_CONFIG.relative_to(ROOT)}")
    return match.group(1)


def integration_version() -> str:
    """Return the ``version`` field from the integration manifest."""
    manifest = json.loads(MANIFEST.read_text())
    version = manifest.get("version")
    if not isinstance(version, str):
        raise SystemExit(f"no string 'version' in {MANIFEST.relative_to(ROOT)}")
    return version


def main() -> int:
    """Compare both versions and report the mismatch."""
    addon = addon_version()
    integration = integration_version()
    if addon != integration:
        print(
            f"add-on version {addon} != integration version {integration}\n"
            f"  {ADDON_CONFIG.relative_to(ROOT)}: {addon}\n"
            f"  {MANIFEST.relative_to(ROOT)}: {integration}\n"
            "Both are bumped together in the release commit.",
            file=sys.stderr,
        )
        return 1
    print(f"add-on and integration agree on version {addon}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
