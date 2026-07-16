"""EEBUS Subject Key Identifier helpers."""

from __future__ import annotations

import string


def normalize_ski(ski: str) -> str:
    """Return an SKI in uppercase hexadecimal form without separators."""
    for separator in (" ", "\t", "\n", "\r", ":", "-"):
        ski = ski.replace(separator, "")
    return ski.strip().upper()


def is_valid_ski(ski: str) -> bool:
    """Return whether an SKI is a 40-character hexadecimal fingerprint."""
    return len(ski) == 40 and all(character in string.hexdigits for character in ski)
