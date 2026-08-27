from __future__ import annotations

import unittest

from scripts.select_addon_ci_build_version import (
    VERSION_PATHS,
    select_build_version,
)


BASE_FILES = {
    "custom_components/eebus/manifest.json": '{"domain":"eebus","version":"0.16.6"}\n',
    "eebus-bridge-addon/config.yaml": 'name: EEBUS\nversion: "0.16.6"\n',
}
HEAD_FILES = {
    "custom_components/eebus/manifest.json": '{"domain":"eebus","version":"0.16.7"}\n',
    "eebus-bridge-addon/config.yaml": 'name: EEBUS\nversion: "0.16.7"\n',
}


class SelectAddonCiBuildVersionTest(unittest.TestCase):
    def test_uses_signed_base_for_exact_version_only_change(self) -> None:
        self.assertEqual(
            select_build_version(VERSION_PATHS, BASE_FILES, HEAD_FILES), "0.16.6"
        )

    def test_uses_current_version_for_config_change(self) -> None:
        head_files = dict(HEAD_FILES)
        head_files["eebus-bridge-addon/config.yaml"] += "arch:\n  - amd64\n"

        self.assertEqual(
            select_build_version(VERSION_PATHS, BASE_FILES, head_files), "0.16.7"
        )

    def test_uses_current_version_when_only_one_version_file_changes(self) -> None:
        self.assertEqual(
            select_build_version(
                {"eebus-bridge-addon/config.yaml"}, BASE_FILES, HEAD_FILES
            ),
            "0.16.7",
        )

    def test_rejects_unsynchronized_current_version_declarations(self) -> None:
        head_files = dict(HEAD_FILES)
        head_files["eebus-bridge-addon/config.yaml"] = (
            'name: EEBUS\nversion: "0.16.8"\n'
        )

        with self.assertRaises(ValueError):
            select_build_version(VERSION_PATHS, BASE_FILES, head_files)


if __name__ == "__main__":
    unittest.main()
