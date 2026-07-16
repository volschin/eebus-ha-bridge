"""EEBUS Subject Key Identifier helpers."""

from __future__ import annotations

import string

_ASCII_UPPER_HEX = str.maketrans("abcdef", "ABCDEF")


def normalize_ski(ski: str) -> str:
    """Return an SKI in uppercase hexadecimal form without separators.

    Uses an ASCII-only case translation rather than str.upper(): the latter
    performs full Unicode case folding, which expands ligatures like 'ﬀ'
    (U+FB00) into two-character sequences ("FF") that Go's rune-wise
    strings.ToUpper never produces — letting non-hex input normalize into a
    string that looks like a valid 40-character SKI. Restricting to a-f/A-F
    keeps normalize_ski's output space identical to the Go implementation.
    """
    for separator in (" ", "\t", "\n", "\r", ":", "-"):
        ski = ski.replace(separator, "")
    return ski.translate(_ASCII_UPPER_HEX)


def is_valid_ski(ski: str) -> bool:
    """Return whether an SKI is a 40-character hexadecimal fingerprint."""
    return len(ski) == 40 and all(character in string.hexdigits for character in ski)
