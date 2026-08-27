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

    def test_normalizes_arm64_v8_manifest_variant(self) -> None:
        self.assertEqual(
            manifest_platforms(
                {
                    "manifests": [
                        {
                            "platform": {
                                "os": "linux",
                                "architecture": "arm64",
                                "variant": "v8",
                            }
                        }
                    ]
                }
            ),
            {"linux/arm64"},
        )

    def test_reports_missing_arm_v7(self) -> None:
        self.assertEqual(
            missing_platforms({"linux/amd64", "linux/arm64"}),
            {"linux/arm/v7"},
        )


if __name__ == "__main__":
    unittest.main()
