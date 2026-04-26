"""HMAC-SHA256 RFC 4231 vectors.

Source: https://datatracker.ietf.org/doc/html/rfc4231#section-4
Cases 1-7 cover: short keys, ASCII data, longer-than-block keys, longer-than-
block keys with longer-than-block data. Case 5 produces a truncated tag and
is omitted (we always emit the full 32-byte tag — truncation is a caller
concern, not a primitive concern).
"""

from __future__ import annotations

import pytest

from mash_py.crypto.mac import hmac_sha256, hmac_sha256_verify


def _hex(s: str) -> bytes:
    return bytes.fromhex(s)


# ---------------------------------------------------------------------------
# RFC 4231 §4.2 — Test Case 1
# ---------------------------------------------------------------------------
def test_rfc4231_case1():
    key = _hex("0b" * 20)
    data = b"Hi There"
    expected = _hex(
        "b0344c61d8db38535ca8afceaf0bf12b"
        "881dc200c9833da726e9376c2e32cff7"
    )
    assert hmac_sha256(key, data) == expected


# ---------------------------------------------------------------------------
# RFC 4231 §4.3 — Test Case 2 (key shorter than block size)
# ---------------------------------------------------------------------------
def test_rfc4231_case2():
    key = b"Jefe"
    data = b"what do ya want for nothing?"
    expected = _hex(
        "5bdcc146bf60754e6a042426089575c7"
        "5a003f089d2739839dec58b964ec3843"
    )
    assert hmac_sha256(key, data) == expected


# ---------------------------------------------------------------------------
# RFC 4231 §4.4 — Test Case 3 (key + data both 0xaa/0xdd, 50 bytes)
# ---------------------------------------------------------------------------
def test_rfc4231_case3():
    key = _hex("aa" * 20)
    data = _hex("dd" * 50)
    expected = _hex(
        "773ea91e36800e46854db8ebd09181a7"
        "2959098b3ef8c122d9635514ced565fe"
    )
    assert hmac_sha256(key, data) == expected


# ---------------------------------------------------------------------------
# RFC 4231 §4.5 — Test Case 4 (numbered key + data)
# ---------------------------------------------------------------------------
def test_rfc4231_case4():
    key = _hex("0102030405060708090a0b0c0d0e0f10111213141516171819")
    data = _hex("cd" * 50)
    expected = _hex(
        "82558a389a443c0ea4cc819899f2083a"
        "85f0faa3e578f8077a2e3ff46729665b"
    )
    assert hmac_sha256(key, data) == expected


# ---------------------------------------------------------------------------
# RFC 4231 §4.7 — Test Case 6 (key longer than block size)
# ---------------------------------------------------------------------------
def test_rfc4231_case6():
    key = _hex("aa" * 131)
    data = b"Test Using Larger Than Block-Size Key - Hash Key First"
    expected = _hex(
        "60e431591ee0b67f0d8a26aacbf5b77f"
        "8e0bc6213728c5140546040f0ee37f54"
    )
    assert hmac_sha256(key, data) == expected


# ---------------------------------------------------------------------------
# RFC 4231 §4.8 — Test Case 7 (long key + long data)
# ---------------------------------------------------------------------------
def test_rfc4231_case7():
    key = _hex("aa" * 131)
    data = (
        b"This is a test using a larger than block-size key and a larger"
        b" than block-size data. The key needs to be hashed before being"
        b" used by the HMAC algorithm."
    )
    expected = _hex(
        "9b09ffa71b942fcb27635fbcd5b0e944"
        "bfdc63644f0713938a7f51535c3a35e2"
    )
    assert hmac_sha256(key, data) == expected


# ---------------------------------------------------------------------------
# Constant-time verification
# ---------------------------------------------------------------------------
def test_verify_accepts_correct_tag():
    key = b"\x42" * 32
    data = b"some message"
    tag = hmac_sha256(key, data)
    assert hmac_sha256_verify(key, data, tag) is True


def test_verify_rejects_wrong_tag():
    key = b"\x42" * 32
    data = b"some message"
    bad = bytes(32)
    assert hmac_sha256_verify(key, data, bad) is False


def test_verify_rejects_truncated_tag():
    key = b"\x42" * 32
    data = b"some message"
    short = hmac_sha256(key, data)[:16]
    # Mismatched lengths must return False, not raise — matches Go's
    # ``hmac.Equal``.
    assert hmac_sha256_verify(key, data, short) is False


def test_empty_key_raises():
    with pytest.raises(ValueError):
        hmac_sha256(b"", b"data")
