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
