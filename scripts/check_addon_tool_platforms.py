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
    images = {
        match.group("stage"): match.group("image") for match in FROM_RE.finditer(dockerfile)
    }
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
        if isinstance(variant, str) and variant and (architecture, variant) != ("arm64", "v8"):
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
