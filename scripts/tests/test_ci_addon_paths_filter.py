from __future__ import annotations

from pathlib import Path
import unittest


WORKFLOW = Path(__file__).parents[2] / ".github/workflows/ci.yml"
REQUIRED_ADDON_PATHS = {
    "scripts/check_addon_tool_platforms.py",
    "scripts/tests/test_check_addon_tool_platforms.py",
}


class AddonPathsFilterTest(unittest.TestCase):
    def test_addon_filter_triggers_for_platform_checker_changes(self) -> None:
        workflow = WORKFLOW.read_text()
        addon_filter = workflow.split("            addon:\n", 1)[1].split(
            "            go:\n", 1
        )[0]

        for path in REQUIRED_ADDON_PATHS:
            self.assertIn(f"              - '{path}'", addon_filter)


if __name__ == "__main__":
    unittest.main()
