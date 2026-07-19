from __future__ import annotations

import importlib.util
from pathlib import Path
import sys
import unittest


SCRIPT = Path(__file__).parents[1] / "check_go_patch_coverage.py"
SPEC = importlib.util.spec_from_file_location("check_go_patch_coverage", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
coverage = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = coverage
SPEC.loader.exec_module(coverage)


class ChangedLinesTest(unittest.TestCase):
    def test_parses_added_and_modified_lines_and_excludes_non_productive_files(self) -> None:
        diff = """\
diff --git a/eebus-bridge/internal/example.go b/eebus-bridge/internal/example.go
--- a/eebus-bridge/internal/example.go
+++ b/eebus-bridge/internal/example.go
@@ -10,2 +10,3 @@
+changed
diff --git a/eebus-bridge/internal/example_test.go b/eebus-bridge/internal/example_test.go
--- a/eebus-bridge/internal/example_test.go
+++ b/eebus-bridge/internal/example_test.go
@@ -1 +1,2 @@
+test
diff --git a/eebus-bridge/gen/proto/generated.go b/eebus-bridge/gen/proto/generated.go
--- a/eebus-bridge/gen/proto/generated.go
+++ b/eebus-bridge/gen/proto/generated.go
@@ -1 +1,2 @@
+generated
"""
        self.assertEqual(
            coverage.parse_changed_lines(diff),
            {"eebus-bridge/internal/example.go": {10, 11, 12}},
        )

    def test_zero_length_deletion_hunk_adds_no_lines(self) -> None:
        diff = """\
diff --git a/eebus-bridge/internal/example.go b/eebus-bridge/internal/example.go
--- a/eebus-bridge/internal/example.go
+++ b/eebus-bridge/internal/example.go
@@ -4,2 +4,0 @@
"""
        self.assertEqual(coverage.parse_changed_lines(diff), {})


class CoverageProfileTest(unittest.TestCase):
    def test_parses_module_paths_and_ignores_excluded_paths(self) -> None:
        blocks = coverage.parse_coverage_profile(
            [
                "mode: set\n",
                "github.com/volschin/eebus-bridge/internal/example.go:10.1,12.2 3 1\n",
                "github.com/volschin/eebus-bridge/gen/proto/generated.go:1.1,2.2 2 0\n",
                "github.com/volschin/eebus-bridge/cmd/eebus-contract-testserver/main.go:1.1,2.2 2 0\n",
            ]
        )
        self.assertEqual(
            blocks,
            [coverage.CoverageBlock("eebus-bridge/internal/example.go", 10, 12, 3, 1)],
        )

    def test_rejects_invalid_profile_lines(self) -> None:
        with self.assertRaises(ValueError):
            coverage.parse_coverage_profile(["not a profile line"])


class PatchCoverageTest(unittest.TestCase):
    def test_counts_each_overlapping_block_once(self) -> None:
        changed = {"eebus-bridge/internal/example.go": {11, 12, 20}}
        blocks = [
            coverage.CoverageBlock("eebus-bridge/internal/example.go", 10, 12, 3, 1),
            coverage.CoverageBlock("eebus-bridge/internal/example.go", 20, 20, 2, 0),
            coverage.CoverageBlock("eebus-bridge/internal/example.go", 30, 30, 5, 0),
        ]
        result = coverage.calculate_patch_coverage(changed, blocks)
        self.assertEqual((result.covered, result.total), (3, 5))
        self.assertAlmostEqual(result.percentage, 60.0)
        self.assertEqual(result.per_file, {"eebus-bridge/internal/example.go": (3, 5)})
        self.assertEqual(result.uncovered, (blocks[1],))
        self.assertFalse(coverage.meets_threshold(result, 90.0))
        self.assertTrue(coverage.meets_threshold(result, 60.0))

    def test_no_changed_statements_passes_at_one_hundred_percent(self) -> None:
        result = coverage.calculate_patch_coverage({}, [])
        self.assertEqual(result.total, 0)
        self.assertEqual(result.percentage, 100.0)


class CoverageBadgeTest(unittest.TestCase):
    def test_renders_total_statement_coverage_without_external_service(self) -> None:
        blocks = [
            coverage.CoverageBlock("eebus-bridge/internal/example.go", 1, 1, 3, 1),
            coverage.CoverageBlock("eebus-bridge/internal/example.go", 2, 2, 2, 0),
        ]
        covered, total, percentage = coverage.total_statement_coverage(blocks)
        self.assertEqual((covered, total), (3, 5))
        self.assertAlmostEqual(percentage, 60.0)
        badge = coverage.render_coverage_badge(percentage)
        self.assertIn("Go coverage: 60.0%", badge)
        self.assertIn("#e05d44", badge)

    def test_target_coverage_uses_green_badge(self) -> None:
        badge = coverage.render_coverage_badge(83.34)
        self.assertIn("83.3%", badge)
        self.assertIn("#4c1", badge)


if __name__ == "__main__":
    unittest.main()
