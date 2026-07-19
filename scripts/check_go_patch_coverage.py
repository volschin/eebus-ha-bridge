#!/usr/bin/env python3
"""Fail when changed production Go statements are covered below a threshold."""

from __future__ import annotations

import argparse
from collections import defaultdict
from dataclasses import dataclass
from pathlib import Path
import re
import subprocess
import sys
from typing import Iterable


MODULE_PREFIX = "github.com/volschin/eebus-bridge/"
REPOSITORY_MODULE_PREFIX = "eebus-bridge/"
EXCLUDED_PREFIXES = (
    "eebus-bridge/gen/",
    "eebus-bridge/cmd/eebus-contract-testserver/",
)
HUNK_RE = re.compile(r"^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@")
PROFILE_RE = re.compile(
    r"^(?P<path>.+):(?P<start_line>\d+)\.(?P<start_col>\d+),"
    r"(?P<end_line>\d+)\.(?P<end_col>\d+) "
    r"(?P<statements>\d+) (?P<count>\d+)$"
)


@dataclass(frozen=True)
class CoverageBlock:
    path: str
    start_line: int
    end_line: int
    statements: int
    count: int


@dataclass(frozen=True)
class PatchCoverage:
    covered: int
    total: int
    per_file: dict[str, tuple[int, int]]
    uncovered: tuple[CoverageBlock, ...]

    @property
    def percentage(self) -> float:
        return 100.0 if self.total == 0 else 100.0 * self.covered / self.total


def total_statement_coverage(blocks: Iterable[CoverageBlock]) -> tuple[int, int, float]:
    covered = 0
    total = 0
    for block in blocks:
        total += block.statements
        if block.count > 0:
            covered += block.statements
    percentage = 100.0 if total == 0 else 100.0 * covered / total
    return covered, total, percentage


def render_coverage_badge(percentage: float) -> str:
    value = f"{percentage:.1f}%"
    if percentage >= 83.0:
        color = "#4c1"
    elif percentage >= 70.0:
        color = "#dfb317"
    else:
        color = "#e05d44"
    return f"""<svg xmlns="http://www.w3.org/2000/svg" width="172" height="28" role="img" aria-label="Go coverage: {value}">
  <title>Go coverage: {value}</title>
  <linearGradient id="s" x2="0" y2="100%">
    <stop offset="0" stop-color="#fff" stop-opacity=".15"/>
    <stop offset="1" stop-opacity=".15"/>
  </linearGradient>
  <clipPath id="r"><rect width="172" height="28" rx="3" fill="#fff"/></clipPath>
  <g clip-path="url(#r)">
    <rect width="112" height="28" fill="#555"/>
    <rect x="112" width="60" height="28" fill="{color}"/>
    <rect width="172" height="28" fill="url(#s)"/>
  </g>
  <g fill="#fff" text-anchor="middle" font-family="Verdana,DejaVu Sans,sans-serif" font-size="10" font-weight="700">
    <text x="56" y="18">GO COVERAGE</text>
    <text x="142" y="18">{value}</text>
  </g>
</svg>
"""


def is_productive_go_path(path: str) -> bool:
    return (
        path.startswith(REPOSITORY_MODULE_PREFIX)
        and path.endswith(".go")
        and not path.endswith("_test.go")
        and not path.startswith(EXCLUDED_PREFIXES)
    )


def parse_changed_lines(diff: str) -> dict[str, set[int]]:
    changed: dict[str, set[int]] = defaultdict(set)
    current_path: str | None = None
    for line in diff.splitlines():
        if line.startswith("+++ "):
            target = line[4:]
            current_path = None if target == "/dev/null" else target.removeprefix("b/")
            if current_path is not None and not is_productive_go_path(current_path):
                current_path = None
            continue
        match = HUNK_RE.match(line)
        if match is None or current_path is None:
            continue
        start = int(match.group(1))
        count = int(match.group(2) or "1")
        if count > 0:
            changed[current_path].update(range(start, start + count))
    return dict(changed)


def repository_path(profile_path: str) -> str | None:
    if profile_path.startswith(MODULE_PREFIX):
        return REPOSITORY_MODULE_PREFIX + profile_path[len(MODULE_PREFIX) :]
    if profile_path.startswith(REPOSITORY_MODULE_PREFIX):
        return profile_path
    return None


def parse_coverage_profile(lines: Iterable[str]) -> list[CoverageBlock]:
    blocks: list[CoverageBlock] = []
    for raw_line in lines:
        line = raw_line.strip()
        if not line or line.startswith("mode:"):
            continue
        match = PROFILE_RE.match(line)
        if match is None:
            raise ValueError(f"invalid Go coverage profile line: {line}")
        path = repository_path(match.group("path"))
        if path is None or not is_productive_go_path(path):
            continue
        blocks.append(
            CoverageBlock(
                path=path,
                start_line=int(match.group("start_line")),
                end_line=int(match.group("end_line")),
                statements=int(match.group("statements")),
                count=int(match.group("count")),
            )
        )
    return blocks


def calculate_patch_coverage(
    changed_lines: dict[str, set[int]], blocks: Iterable[CoverageBlock]
) -> PatchCoverage:
    covered = 0
    total = 0
    per_file_counts: dict[str, list[int]] = defaultdict(lambda: [0, 0])
    uncovered: list[CoverageBlock] = []
    for block in blocks:
        lines = changed_lines.get(block.path)
        if not lines or not any(block.start_line <= line <= block.end_line for line in lines):
            continue
        total += block.statements
        per_file_counts[block.path][1] += block.statements
        if block.count > 0:
            covered += block.statements
            per_file_counts[block.path][0] += block.statements
        else:
            uncovered.append(block)
    return PatchCoverage(
        covered=covered,
        total=total,
        per_file={path: (values[0], values[1]) for path, values in per_file_counts.items()},
        uncovered=tuple(uncovered),
    )


def meets_threshold(result: PatchCoverage, threshold: float) -> bool:
    return result.percentage + 1e-9 >= threshold


def git_diff(base: str, head: str, repository: Path) -> str:
    result = subprocess.run(
        ["git", "diff", "--unified=0", "--no-ext-diff", base, head, "--", "*.go"],
        cwd=repository,
        check=True,
        text=True,
        stdout=subprocess.PIPE,
    )
    return result.stdout


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", required=True, type=Path, help="Go coverprofile path")
    parser.add_argument("--base", required=True, help="Git base commit or tree")
    parser.add_argument("--head", default="HEAD", help="Git head commit (default: HEAD)")
    parser.add_argument("--repository", default=Path.cwd(), type=Path, help="Git repository root")
    parser.add_argument("--threshold", default=90.0, type=float, help="required percentage")
    parser.add_argument("--badge-output", type=Path, help="write a self-contained total coverage SVG")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if not 0 <= args.threshold <= 100:
        print("error: threshold must be between 0 and 100", file=sys.stderr)
        return 2
    try:
        diff = git_diff(args.base, args.head, args.repository)
        changed = parse_changed_lines(diff)
        with args.profile.open(encoding="utf-8") as profile:
            blocks = parse_coverage_profile(profile)
        if args.badge_output is not None:
            _, _, total_percentage = total_statement_coverage(blocks)
            args.badge_output.parent.mkdir(parents=True, exist_ok=True)
            args.badge_output.write_text(render_coverage_badge(total_percentage), encoding="utf-8")
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(f"error: {error}", file=sys.stderr)
        return 2

    result = calculate_patch_coverage(changed, blocks)
    if result.total == 0:
        print("Go patch coverage: no changed production Go statements; gate passed")
        return 0

    print(
        f"Go patch coverage: {result.percentage:.1f}% "
        f"({result.covered}/{result.total} statements), required {args.threshold:.1f}%"
    )
    for path, (file_covered, file_total) in sorted(result.per_file.items()):
        print(f"  {path}: {100.0 * file_covered / file_total:.1f}% ({file_covered}/{file_total})")
    if result.uncovered:
        print("Uncovered changed blocks:")
        for block in result.uncovered:
            print(f"  {block.path}:{block.start_line}-{block.end_line} ({block.statements} statements)")
    return 0 if meets_threshold(result, args.threshold) else 1


if __name__ == "__main__":
    raise SystemExit(main())
