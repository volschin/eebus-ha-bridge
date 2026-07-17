"""Verify proto_stubs exports every protobuf symbol used by Python code."""

from __future__ import annotations

import ast
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PACKAGE = ROOT / "custom_components" / "eebus"


class ProtoStubUseVisitor(ast.NodeVisitor):
    """Collect proto_stubs.X attribute uses."""

    def __init__(self) -> None:
        self.names: set[str] = set()

    def visit_Attribute(self, node: ast.Attribute) -> None:
        if isinstance(node.value, ast.Name) and node.value.id == "proto_stubs":
            self.names.add(node.attr)
        self.generic_visit(node)


def used_proto_stub_names() -> set[str]:
    """Return every proto_stubs attribute referenced outside generated code."""
    names: set[str] = set()
    for path in sorted(PACKAGE.rglob("*.py")):
        if "generated" in path.parts or path.name == "proto_stubs.py":
            continue
        tree = ast.parse(path.read_text(), filename=str(path))
        visitor = ProtoStubUseVisitor()
        visitor.visit(tree)
        names.update(visitor.names)
    return names


def declared_all() -> set[str]:
    """Extract proto_stubs.__all__ without importing it (no HA/grpcio needed)."""
    stub_path = PACKAGE / "proto_stubs.py"
    tree = ast.parse(stub_path.read_text(), filename=str(stub_path))
    for node in tree.body:
        if isinstance(node, ast.Assign) and any(
            isinstance(target, ast.Name) and target.id == "__all__" for target in node.targets
        ):
            value = ast.literal_eval(node.value)
            return set(value)
    raise SystemExit(f"{stub_path} does not declare a literal __all__")


def main() -> None:
    public = declared_all()
    missing = sorted(name for name in used_proto_stub_names() if name not in public)
    if missing:
        joined = ", ".join(missing)
        raise SystemExit(f"proto_stubs.__all__ is missing used exports: {joined}")


if __name__ == "__main__":
    main()
