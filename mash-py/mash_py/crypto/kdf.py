"""HKDF-SHA256 — RFC 5869.

The MASH spec uses HKDF-SHA256 in SPAKE2+ key derivation
(``docs/security.md §4.4``). The function signature matches RFC 5869
naming: Extract takes ``(salt, IKM)`` and Expand takes ``(PRK, info,
length)``. We expose the combined Extract-then-Expand call as
``hkdf_sha256(ikm, salt, info, length)`` because every MASH caller uses
the combined form.

The implementation delegates to ``cryptography.hazmat.primitives.kdf.hkdf``
(RFC-conformant). Keeping this as a thin facade lets us swap the back-end
later (e.g., CircuitPython port, mash-py-tdd-plan.md §2 Decision B)
without touching call sites.
"""

from __future__ import annotations

from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.kdf.hkdf import HKDF

# RFC 5869 §2.3: the maximum output length is 255 * HashLen. For SHA-256
# that is 255 * 32 = 8160 bytes. Callers asking for more is a programmer
# error — we surface it as ``ValueError`` rather than letting the back-end
# raise something opaque.
_MAX_HKDF_SHA256_LENGTH = 255 * 32


def hkdf_sha256(
    ikm: bytes,
    salt: bytes | None,
    info: bytes,
    length: int,
) -> bytes:
    """Derive ``length`` bytes from ``ikm`` using HKDF-SHA256.

    Args:
        ikm: Input keying material. May be empty per RFC 5869 §2.2 but
            in practice is always non-empty in MASH (a setup code or a
            transcript hash).
        salt: Optional salt. ``None`` is mapped to a zero-filled string
            of HashLen bytes per RFC 5869 §2.2.
        info: Application-specific context string. Conventionally a
            domain-separation tag (``"SPAKE2+-P256-SHA256 w"``,
            ``"SPAKE2+-P256-SHA256"``, etc.).
        length: Number of output bytes to produce. Must satisfy
            ``1 <= length <= 255 * 32``.

    Returns:
        Derived key material of exactly ``length`` bytes.

    Raises:
        ValueError: ``length`` is outside the valid range.
    """
    if length < 1 or length > _MAX_HKDF_SHA256_LENGTH:
        raise ValueError(
            f"HKDF-SHA256 length must be in [1, {_MAX_HKDF_SHA256_LENGTH}], got {length}"
        )

    hkdf = HKDF(
        algorithm=hashes.SHA256(),
        length=length,
        salt=salt,
        info=info,
    )
    return hkdf.derive(ikm)
