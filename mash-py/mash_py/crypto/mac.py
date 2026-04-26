"""HMAC-SHA256 — RFC 4231.

The MASH spec uses HMAC-SHA256 for SPAKE2+ confirmation MACs
(``docs/security.md §4.4``).

Two helpers are exposed: :func:`hmac_sha256` for computing a tag, and
:func:`hmac_sha256_verify` for constant-time comparison. Callers MUST
use :func:`hmac_sha256_verify` rather than ``==`` for tag verification —
the ``==`` operator on ``bytes`` short-circuits on the first mismatching
byte and leaks timing information.

The implementation uses the Python standard library's ``hmac`` and
``hashlib`` modules; ``hmac.compare_digest`` is constant-time.
"""

from __future__ import annotations

import hashlib
import hmac


def hmac_sha256(key: bytes, data: bytes) -> bytes:
    """Compute HMAC-SHA256 over ``data`` with ``key``.

    Returns a 32-byte tag.

    The key may be of any non-zero length; per RFC 2104 §2 longer keys
    are pre-hashed. An empty key is permitted by the standard but flagged
    here as a programmer error — every MASH caller derives the key from
    HKDF first.
    """
    if not key:
        raise ValueError("HMAC-SHA256 key must be non-empty")
    return hmac.new(key, data, hashlib.sha256).digest()


def hmac_sha256_verify(key: bytes, data: bytes, expected_tag: bytes) -> bool:
    """Constant-time verification of an HMAC-SHA256 tag.

    Returns ``True`` iff ``HMAC-SHA256(key, data) == expected_tag``. The
    comparison is constant-time with respect to ``expected_tag`` and the
    computed tag.

    Mismatched lengths return ``False`` without raising.
    """
    computed = hmac_sha256(key, data)
    return hmac.compare_digest(computed, expected_tag)
