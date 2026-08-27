from __future__ import annotations

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

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

    def test_rejects_current_shell_payload(self) -> None:
        head_files = {
            "custom_components/eebus/manifest.json": (
                '{"domain":"eebus","version":"0.16.7;$(touch /tmp/pwned)"}\n'
            ),
            "eebus-bridge-addon/config.yaml": (
                'name: EEBUS\nversion: "0.16.7;$(touch /tmp/pwned)"\n'
            ),
        }

        with self.assertRaises(ValueError):
            select_build_version(VERSION_PATHS, BASE_FILES, head_files)

    def test_rejects_base_newline_payload(self) -> None:
        base_files = dict(BASE_FILES)
        base_files["eebus-bridge-addon/config.yaml"] = (
            'name: EEBUS\nversion: "0.16.6\nmalicious"\n'
        )

        with self.assertRaises(ValueError):
            select_build_version(VERSION_PATHS, base_files, HEAD_FILES)

    def _git(self, directory: Path, *args: str) -> str:
        return subprocess.check_output(
            ("git", *args), cwd=directory, text=True
        ).strip()

    def _write_version_files(self, directory: Path, version: str, config_suffix: str = "") -> None:
        manifest = directory / "custom_components/eebus/manifest.json"
        config = directory / "eebus-bridge-addon/config.yaml"
        manifest.parent.mkdir(parents=True, exist_ok=True)
        config.parent.mkdir(parents=True, exist_ok=True)
        manifest.write_text(f'{{"domain":"eebus","version":"{version}"}}\n')
        config.write_text(f'name: EEBUS\nversion: "{version}"\n{config_suffix}')

    def _select_from_git_repository(
        self, directory: Path, base: str
    ) -> subprocess.CompletedProcess[str]:
        selector = Path(__file__).parents[1] / "select_addon_ci_build_version.py"
        return subprocess.run(
            (sys.executable, str(selector), "--base", base),
            cwd=directory,
            text=True,
            capture_output=True,
            check=False,
        )

    def test_cli_uses_base_for_exact_version_only_git_commit(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            directory = Path(temporary_directory)
            self._git(directory, "init", "--quiet")
            self._git(directory, "config", "user.email", "test@example.invalid")
            self._git(directory, "config", "user.name", "Test User")
            self._write_version_files(directory, "0.16.6")
            self._git(directory, "add", ".")
            self._git(directory, "commit", "--quiet", "-m", "base")
            base = self._git(directory, "rev-parse", "HEAD")
            self._write_version_files(directory, "0.16.7")
            self._git(directory, "add", ".")
            self._git(directory, "commit", "--quiet", "-m", "version")

            result = self._select_from_git_repository(directory, base)

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "0.16.6\n")

    def test_cli_uses_current_for_mixed_git_commit(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            directory = Path(temporary_directory)
            self._git(directory, "init", "--quiet")
            self._git(directory, "config", "user.email", "test@example.invalid")
            self._git(directory, "config", "user.name", "Test User")
            self._write_version_files(directory, "0.16.6")
            self._git(directory, "add", ".")
            self._git(directory, "commit", "--quiet", "-m", "base")
            base = self._git(directory, "rev-parse", "HEAD")
            self._write_version_files(directory, "0.16.7", "arch:\n  - amd64\n")
            self._git(directory, "add", ".")
            self._git(directory, "commit", "--quiet", "-m", "mixed")

            result = self._select_from_git_repository(directory, base)

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "0.16.7\n")


if __name__ == "__main__":
    unittest.main()
