"""Tests for canonical EEBUS SKI handling."""

import pytest

from custom_components.eebus.ski import is_valid_ski, normalize_ski


@pytest.mark.parametrize(
    ("ski", "expected"),
    [
        ("abcdef", "ABCDEF"),
        ("  ab cd ef  ", "ABCDEF"),
        ("ab:cd-ef", "ABCDEF"),
        ("ab\tcd\nef", "ABCDEF"),
        ("AbCdEf", "ABCDEF"),
        ("\t ab:CD-ef \r\n", "ABCDEF"),
        (" aB:cD-ef\t12:34 ", "ABCDEF1234"),
        ("", ""),
        ("\x1c", "\x1c"),
    ],
)
def test_normalize_ski(ski: str, expected: str) -> None:
    """Normalization matches the bridge's canonical test vectors."""
    assert normalize_ski(ski) == expected


def test_normalize_ski_does_not_expand_unicode_ligatures() -> None:
    """str.upper() would fold 'ﬀ' into 'FF', forging a fake valid SKI.

    Go's rune-wise strings.ToUpper never does this; normalize_ski must not
    diverge from that by using Python's full Unicode case folding.
    """
    ligature_ski = "ﬀ" * 20
    normalized = normalize_ski(ligature_ski)
    assert normalized != "F" * 40
    assert not is_valid_ski(normalized)


@pytest.mark.parametrize(
    "ski",
    [
        "682f708ceba5df9adcb9e6787ea911d9fc3ac490",
        "682F708CEBA5DF9ADCB9E6787EA911D9FC3AC490",
    ],
)
def test_is_valid_ski_accepts_40_hex_characters(ski: str) -> None:
    """Both hexadecimal cases are valid."""
    assert is_valid_ski(ski)


@pytest.mark.parametrize(
    "ski",
    [
        "",
        "682F708CEBA5DF9ADCB9E6787EA911D9FC3AC49",
        "682F708CEBA5DF9ADCB9E6787EA911D9FC3AC4900",
        "682F708CEBA5DF9ADCB9E6787EA911D9FC3AC49Z",
        "68:2F:70:8C:EB:A5:DF:9A:DC:B9:E6:78:7E:A9:11:D9:FC:3A:C4:90",
    ],
)
def test_is_valid_ski_rejects_malformed_values(ski: str) -> None:
    """Non-canonical lengths, separators, and non-hex characters are invalid."""
    assert not is_valid_ski(ski)
