"""Deterministic postprocessing for generated Python protobuf files."""

from __future__ import annotations

from pathlib import Path

GENERATED_IMPORT_PREFIX = "from eebus.v1 import "


def rewrite_python_proto_imports(root: Path) -> list[Path]:
    """Rewrite generated absolute eebus.v1 imports to package-relative imports."""
    rewritten: list[Path] = []
    for path in sorted(root.rglob("*")):
        if path.suffix not in {".py", ".pyi"}:
            continue
        original = path.read_text()
        updated = _rewrite_imports(original)
        if updated == original:
            continue
        path.write_text(updated)
        rewritten.append(path)
    return rewritten


def _rewrite_imports(content: str) -> str:
    lines = []
    changed = False
    for line in content.splitlines(keepends=True):
        if line.startswith(GENERATED_IMPORT_PREFIX) and " as " in line:
            line = line.replace(GENERATED_IMPORT_PREFIX, "from . import ", 1)
            changed = True
        lines.append(line)
    return "".join(lines) if changed else content


def main() -> None:
    import argparse

    parser = argparse.ArgumentParser()
    parser.add_argument("root", type=Path)
    args = parser.parse_args()
    rewrite_python_proto_imports(args.root)


if __name__ == "__main__":
    main()
