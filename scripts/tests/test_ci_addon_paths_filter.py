from __future__ import annotations

from pathlib import Path
import re
import unittest


WORKFLOW = Path(__file__).parents[2] / ".github/workflows/ci.yml"
REQUIRED_ADDON_PATHS = {
    ".github/workflows/release.yml",
    "scripts/check_addon_tool_platforms.py",
    "scripts/tests/test_check_addon_tool_platforms.py",
    "scripts/tests/test_ci_addon_paths_filter.py",
    "scripts/tests/test_release_addon_gate.py",
}


class AddonPathsFilterTest(unittest.TestCase):
    def test_addon_filter_triggers_for_platform_checker_changes(self) -> None:
        workflow = WORKFLOW.read_text()
        addon_filter = workflow.split("            addon:\n", 1)[1].split(
            "            go:\n", 1
        )[0]

        for path in REQUIRED_ADDON_PATHS:
            self.assertIn(f"              - '{path}'", addon_filter)

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


if __name__ == "__main__":
    unittest.main()
