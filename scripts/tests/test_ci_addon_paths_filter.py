from __future__ import annotations

from pathlib import Path
import re
import unittest


WORKFLOW = Path(__file__).parents[2] / ".github/workflows/ci.yml"
REQUIRED_ADDON_PATHS = {
    ".github/workflows/release.yml",
    "scripts/check_addon_tool_platforms.py",
    "scripts/tests/test_addon_dockerfile_supply_chain.py",
    "scripts/tests/test_check_addon_tool_platforms.py",
    "scripts/tests/test_ci_addon_paths_filter.py",
    "scripts/tests/test_fetch_verified_bridge.py",
    "scripts/tests/test_release_addon_gate.py",
}
REQUIRED_ADDON_BUILD_PATHS = {
    ".github/workflows/ci.yml",
    ".github/workflows/release.yml",
    "eebus-bridge-addon/Dockerfile",
    "eebus-bridge-addon/fetch-verified-bridge.sh",
    "eebus-bridge-addon/run.sh",
    "scripts/check_addon_tool_platforms.py",
}
VERSION_DECLARATION_PATHS = {
    "custom_components/eebus/manifest.json",
    "eebus-bridge-addon/config.yaml",
}


class AddonPathsFilterTest(unittest.TestCase):
    def test_addon_filter_triggers_for_platform_checker_changes(self) -> None:
        workflow = WORKFLOW.read_text()
        addon_filter = workflow.split("            addon:\n", 1)[1].split(
            "            go:\n", 1
        )[0]

        for path in REQUIRED_ADDON_PATHS:
            self.assertIn(f"              - '{path}'", addon_filter)

    def test_addon_build_filter_skips_version_only_changes(self) -> None:
        workflow = WORKFLOW.read_text()
        changes_job = workflow.split("  changes:\n", 1)[1].split("  policy:\n", 1)[0]
        addon_build_filter = workflow.split("            addon_build:\n", 1)[1].split(
            "            go:\n", 1
        )[0]

        self.assertIn(
            "      addon_build: ${{ steps.filter.outputs.addon_build }}", changes_job
        )
        for path in REQUIRED_ADDON_BUILD_PATHS:
            self.assertIn(f"              - '{path}'", addon_build_filter)
        for path in VERSION_DECLARATION_PATHS:
            self.assertNotIn(f"              - '{path}'", addon_build_filter)

    def test_addon_job_runs_filter_contract_test(self) -> None:
        workflow = WORKFLOW.read_text()
        addon_job = re.search(
            r"(?ms)^  addon:\n(?P<body>.*?)(?=^  \w+:|\Z)", workflow
        ).group("body")

        self.assertIn(
            "python3 -m unittest scripts.tests.test_ci_addon_paths_filter -v",
            addon_job,
        )
        self.assertIn(
            "python3 -m unittest scripts.tests.test_release_addon_gate -v",
            addon_job,
        )
        build_step = addon_job.split("      - name: Build verified add-on image\n", 1)[1]
        self.assertIn(
            "        if: needs.changes.outputs.addon_build == 'true'\n",
            build_step,
        )


if __name__ == "__main__":
    unittest.main()
