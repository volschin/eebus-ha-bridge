from __future__ import annotations

import importlib.util
from pathlib import Path
import sys
import unittest


SCRIPT = Path(__file__).parents[1] / "check_addon_version.py"
SPEC = importlib.util.spec_from_file_location("check_addon_version", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
versions = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = versions
SPEC.loader.exec_module(versions)


class ReleaseTagTest(unittest.TestCase):
    def test_accepts_tag_matching_manifest_version(self) -> None:
        versions.validate_release_tag("v0.16.2", "0.16.2")

    def test_rejects_tag_without_v_prefix(self) -> None:
        with self.assertRaisesRegex(ValueError, "must have the form"):
            versions.validate_release_tag("0.16.2", "0.16.2")

    def test_rejects_tag_not_matching_manifest_version(self) -> None:
        with self.assertRaisesRegex(ValueError, "does not match"):
            versions.validate_release_tag("v0.16.3", "0.16.2")


if __name__ == "__main__":
    unittest.main()
