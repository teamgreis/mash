"""HKDF-SHA256 RFC 5869 vectors.

The first three appendix-A test cases use SHA-256 (the only digest mash-py
needs). Cases A.4 to A.7 use SHA-1 and are intentionally not ported.

Source: https://datatracker.ietf.org/doc/html/rfc5869#appendix-A
"""

from __future__ import annotations

import pytest

from mash_py.crypto.kdf import hkdf_sha256


def _hex(s: str) -> bytes:
    return bytes.fromhex(s)


# ---------------------------------------------------------------------------
# RFC 5869 §A.1 — Basic test case with SHA-256
# ---------------------------------------------------------------------------
RFC5869_A1_IKM = _hex("0b" * 22)
RFC5869_A1_SALT = _hex("000102030405060708090a0b0c")
RFC5869_A1_INFO = _hex("f0f1f2f3f4f5f6f7f8f9")
RFC5869_A1_L = 42
RFC5869_A1_OKM = _hex(
    "3cb25f25faacd57a90434f64d0362f2a"
    "2d2d0a90cf1a5a4c5db02d56ecc4c5bf"
    "34007208d5b887185865"
)


def test_rfc5869_a1_basic_sha256():
    okm = hkdf_sha256(RFC5869_A1_IKM, RFC5869_A1_SALT, RFC5869_A1_INFO, RFC5869_A1_L)
    assert okm == RFC5869_A1_OKM


# ---------------------------------------------------------------------------
# RFC 5869 §A.2 — Test with SHA-256 and longer inputs/outputs
# ---------------------------------------------------------------------------
RFC5869_A2_IKM = _hex(
    "000102030405060708090a0b0c0d0e0f"
    "101112131415161718191a1b1c1d1e1f"
    "202122232425262728292a2b2c2d2e2f"
    "303132333435363738393a3b3c3d3e3f"
    "404142434445464748494a4b4c4d4e4f"
)
RFC5869_A2_SALT = _hex(
    "606162636465666768696a6b6c6d6e6f"
    "707172737475767778797a7b7c7d7e7f"
    "808182838485868788898a8b8c8d8e8f"
    "909192939495969798999a9b9c9d9e9f"
    "a0a1a2a3a4a5a6a7a8a9aaabacadaeaf"
)
RFC5869_A2_INFO = _hex(
    "b0b1b2b3b4b5b6b7b8b9babbbcbdbebf"
    "c0c1c2c3c4c5c6c7c8c9cacbcccdcecf"
    "d0d1d2d3d4d5d6d7d8d9dadbdcdddedf"
    "e0e1e2e3e4e5e6e7e8e9eaebecedeeef"
    "f0f1f2f3f4f5f6f7f8f9fafbfcfdfeff"
)
RFC5869_A2_L = 82
RFC5869_A2_OKM = _hex(
    "b11e398dc80327a1c8e7f78c596a4934"
    "4f012eda2d4efad8a050cc4c19afa97c"
    "59045a99cac7827271cb41c65e590e09"
    "da3275600c2f09b8367793a9aca3db71"
    "cc30c58179ec3e87c14c01d5c1f3434f"
    "1d87"
)


def test_rfc5869_a2_long_inputs_sha256():
    okm = hkdf_sha256(RFC5869_A2_IKM, RFC5869_A2_SALT, RFC5869_A2_INFO, RFC5869_A2_L)
    assert okm == RFC5869_A2_OKM


# ---------------------------------------------------------------------------
# RFC 5869 §A.3 — Test with SHA-256 and zero-length salt/info
# ---------------------------------------------------------------------------
RFC5869_A3_IKM = _hex("0b" * 22)
RFC5869_A3_SALT: bytes | None = b""
RFC5869_A3_INFO = b""
RFC5869_A3_L = 42
RFC5869_A3_OKM = _hex(
    "8da4e775a563c18f715f802a063c5a31"
    "b8a11f5c5ee1879ec3454e5f3c738d2d"
    "9d201395faa4b61a96c8"
)


def test_rfc5869_a3_zero_length_salt_info():
    okm = hkdf_sha256(RFC5869_A3_IKM, RFC5869_A3_SALT, RFC5869_A3_INFO, RFC5869_A3_L)
    assert okm == RFC5869_A3_OKM


# ---------------------------------------------------------------------------
# RFC 5869 §2.2 — salt=None must equal salt=zeros(HashLen).
# This is the behaviour the Go ``hkdf.New(.., nil, ..)`` call relies on
# (mash-go/pkg/commissioning/spake2plus.go:254 passes ``nil`` salt).
# ---------------------------------------------------------------------------
def test_none_salt_equals_zero_hashlen_salt():
    ikm = b"some keying material"
    info = b"context"
    a = hkdf_sha256(ikm, None, info, 32)
    b = hkdf_sha256(ikm, bytes(32), info, 32)
    assert a == b


# ---------------------------------------------------------------------------
# Length validation
# ---------------------------------------------------------------------------
@pytest.mark.parametrize("bad_length", [0, -1, 255 * 32 + 1, 10_000_000])
def test_invalid_length_raises(bad_length):
    with pytest.raises(ValueError):
        hkdf_sha256(b"ikm", b"salt", b"info", bad_length)


def test_max_length_accepted():
    okm = hkdf_sha256(b"ikm", b"salt", b"info", 255 * 32)
    assert len(okm) == 255 * 32


# ---------------------------------------------------------------------------
# Determinism — same inputs always produce same output (no nonce).
# ---------------------------------------------------------------------------
def test_determinism():
    ikm = b"\x42" * 32
    a = hkdf_sha256(ikm, b"salt", b"info", 64)
    b = hkdf_sha256(ikm, b"salt", b"info", 64)
    assert a == b
