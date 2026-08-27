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
                if [ "${FAKE_GET_FILE_EXIT:-0}" -ne 0 ]; then
                    printf 'partial-binary' >"$7"
                    exit "${FAKE_GET_FILE_EXIT}"
                fi
                if [ "${FAKE_SYMLINK_OUTPUT:-0}" -eq 1 ]; then
                    ln -s "${CALL_LOG}" "$7"
                    exit 0
                fi
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
        self.assertEqual(list(self.root.glob(".eebus-bridge.*")), [])

    def test_empty_extracted_file_fails_closed(self) -> None:
        result = self._run(FAKE_EMPTY_OUTPUT="1")
        self.assertNotEqual(result.returncode, 0)

    def test_symlink_extracted_file_fails_closed(self) -> None:
        result = self._run(FAKE_SYMLINK_OUTPUT="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.output.exists())
        self.assertEqual(list(self.root.glob(".eebus-bridge.*")), [])

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
        self.assertEqual(list(self.root.glob(".eebus-bridge.*")), [])


if __name__ == "__main__":
    unittest.main()
