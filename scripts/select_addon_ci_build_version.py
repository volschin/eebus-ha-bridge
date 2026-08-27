#!/usr/bin/env python3
"""Select the bridge version for a verified add-on CI build."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
import re
import subprocess
from collections.abc import Collection, Mapping

VERSION_PATHS = frozenset(
    {
        "custom_components/eebus/manifest.json",
        "eebus-bridge-addon/config.yaml",
    }
)
_MANIFEST_PATH = "custom_components/eebus/manifest.json"
_ADDON_CONFIG_PATH = "eebus-bridge-addon/config.yaml"
_ADDON_VERSION_RE = re.compile(r'(?m)^version: "([^"\n]+)"[ \t]*$')
_VERSION_RE = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+$")


def _validate_version(path: str, version: object) -> str:
    if not isinstance(version, str) or not _VERSION_RE.fullmatch(version):
        raise ValueError(f"{path} has an invalid version")
    return version


def _parse_version(path: str, content: str) -> tuple[str, str]:
    if path == _MANIFEST_PATH:
        document = json.loads(content)
        version = _validate_version(path, document.get("version"))
        document["version"] = "__VERSION__"
        return version, json.dumps(document, sort_keys=True, separators=(",", ":"))

    if path == _ADDON_CONFIG_PATH:
        matches = list(_ADDON_VERSION_RE.finditer(content))
        if len(matches) != 1:
            raise ValueError(f"{path} must contain exactly one quoted version")
        version = _validate_version(path, matches[0].group(1))
        return version, _ADDON_VERSION_RE.sub('version: "__VERSION__"', content)

    raise ValueError(f"unsupported version declaration: {path}")


def _version_and_normalized_files(
    files: Mapping[str, str],
) -> tuple[str, dict[str, str]]:
    versions: set[str] = set()
    normalized: dict[str, str] = {}
    for path in VERSION_PATHS:
        version, normalized_content = _parse_version(path, files[path])
        versions.add(version)
        normalized[path] = normalized_content

    if len(versions) != 1:
        raise ValueError("version declarations are not synchronized")
    return versions.pop(), normalized


def select_build_version(
    changed_paths: Collection[str],
    base_files: Mapping[str, str],
    current_files: Mapping[str, str],
) -> str:
    """Use the signed base version only for a semantic version-only change."""
    current_version, current_normalized = _version_and_normalized_files(current_files)
    if set(changed_paths) != VERSION_PATHS:
        return current_version

    try:
        base_version, base_normalized = _version_and_normalized_files(base_files)
    except KeyError:
        return current_version

    if base_normalized != current_normalized:
        return current_version
    return base_version


def _git_output(*args: str) -> str:
    return subprocess.check_output(("git", *args), text=True).strip()


def _current_files() -> dict[str, str]:
    return {path: Path(path).read_text() for path in VERSION_PATHS}


def _base_files(base: str) -> dict[str, str]:
    return {path: _git_output("show", f"{base}:{path}") for path in VERSION_PATHS}


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", required=True, help="trusted event base commit")
    args = parser.parse_args()

    changed_paths = _git_output("diff", "--name-only", args.base, "HEAD").splitlines()
    current_files = _current_files()
    try:
        base_files = _base_files(args.base)
    except subprocess.CalledProcessError:
        base_files = {}

    print(select_build_version(changed_paths, base_files, current_files))


if __name__ == "__main__":
    main()
