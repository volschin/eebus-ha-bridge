"""Enforce v1 field cardinality except documented historical presence waivers."""

from __future__ import annotations

import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PROTO_ROOT = ROOT / "eebus-bridge" / "proto"

ALLOWED_CARDINALITY_CHANGES = {
    ("eebus/v1/grid_service.proto", "GridData", 1, "power_w"),
    ("eebus/v1/visualization_service.proto", "PVData", 1, "power_w"),
    ("eebus/v1/visualization_service.proto", "BatteryData", 1, "power_w"),
}

MESSAGE_RE = re.compile(r"message\s+(?P<name>[A-Za-z_][A-Za-z0-9_]*)\s*\{(?P<body>.*?)\n\}", re.DOTALL)
FIELD_RE = re.compile(
    r"^\s*(?:(?P<label>optional|repeated|required)\s+)?"
    r"(?P<type>[A-Za-z_][A-Za-z0-9_.<>]*)\s+"
    r"(?P<name>[A-Za-z_][A-Za-z0-9_]*)\s*=\s*"
    r"(?P<number>[0-9]+)\b",
    re.MULTILINE,
)


@dataclass(frozen=True, slots=True)
class Field:
    file: str
    message: str
    number: int
    name: str
    cardinality: str

    @property
    def waiver_key(self) -> tuple[str, str, int, str]:
        return (self.file, self.message, self.number, self.name)


def main() -> None:
    if len(sys.argv) != 2:
        raise SystemExit("usage: check_proto_cardinality.py <baseline-git-ref>")
    baseline_ref = sys.argv[1]
    current = _current_fields()
    baseline = _baseline_fields(baseline_ref)

    failures: list[str] = []
    for key, old in sorted(baseline.items()):
        new = current.get(key)
        if new is None or old.cardinality == new.cardinality:
            continue
        if (
            new.waiver_key in ALLOWED_CARDINALITY_CHANGES
            and old.cardinality == "implicit"
            and new.cardinality == "optional"
        ):
            continue
        failures.append(
            f"{new.file}:{new.message}.{new.name} field {new.number} "
            f"changed cardinality {old.cardinality!r} -> {new.cardinality!r}"
        )
    if failures:
        raise SystemExit("unapproved proto field cardinality changes:\n" + "\n".join(failures))


def _current_fields() -> dict[tuple[str, str, int], Field]:
    fields: dict[tuple[str, str, int], Field] = {}
    for path in sorted(PROTO_ROOT.rglob("*.proto")):
        rel = path.relative_to(PROTO_ROOT).as_posix()
        fields.update(_parse_fields(rel, path.read_text()))
    return fields


def _baseline_fields(ref: str) -> dict[tuple[str, str, int], Field]:
    fields: dict[tuple[str, str, int], Field] = {}
    for path in sorted(PROTO_ROOT.rglob("*.proto")):
        rel = path.relative_to(PROTO_ROOT).as_posix()
        try:
            content = subprocess.check_output(
                ["git", "show", f"{ref}:eebus-bridge/proto/{rel}"],
                cwd=ROOT,
                text=True,
            )
        except subprocess.CalledProcessError:
            continue
        fields.update(_parse_fields(rel, content))
    return fields


def _parse_fields(file: str, content: str) -> dict[tuple[str, str, int], Field]:
    fields: dict[tuple[str, str, int], Field] = {}
    for message in MESSAGE_RE.finditer(content):
        message_name = message.group("name")
        for field in FIELD_RE.finditer(message.group("body")):
            number = int(field.group("number"))
            label = field.group("label") or "implicit"
            fields[(file, message_name, number)] = Field(
                file=file,
                message=message_name,
                number=number,
                name=field.group("name"),
                cardinality=label,
            )
    return fields


if __name__ == "__main__":
    main()
