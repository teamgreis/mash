"""Memory-Hard KDF for DEC-084 SPAKE2+.

MASH v1 uses **PBKDF2-HMAC-SHA256** as the MHF run before any HKDF
chain (DEC-084 §2.1, §2.3). The MHF defends against offline brute-force
of stolen verifiers: without it, a 27-bit setup code falls in seconds
on a GPU; with 100,000 PBKDF2 iterations the same attack costs
~3 hours per device.

Future protocol versions may introduce alternative MHFs (Argon2id is
the obvious candidate for hardware that can spare 64KB+ of memory).
Such additions belong in this module behind a discriminator argument
so call sites in :mod:`mash_py.commissioning.spake2plus` keep a single
import path.

The function signature is parametric (``iterations``, ``dklen``) only
so the Phase 7B conformance vectors can exercise edge-case inputs.
v1 callers always pass the protocol-constant values defined here.
"""

from __future__ import annotations

from typing import Final

from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.kdf.pbkdf2 import PBKDF2HMAC

# DEC-084 §2.1 — protocol constants. Both implementations (mash-go and
# mash-py) MUST use these exact values; the verifier `(w0, L)` is
# different under any other choice and devices would not interoperate.
MHF_ITERATIONS: Final[int] = 100_000
"""PBKDF2-HMAC-SHA256 iteration count (DEC-084 §2.1)."""

MHF_DKLEN: Final[int] = 80
"""Output length of the MHF in bytes; consumed as 40 || 40 by w0 / w1."""


def pbkdf2_mhf(
    setup_code_bytes: bytes,
    salt: bytes,
    iterations: int = MHF_ITERATIONS,
    dklen: int = MHF_DKLEN,
) -> bytes:
    """Run PBKDF2-HMAC-SHA256 with the DEC-084 parameters.

    Args:
        setup_code_bytes: UTF-8 bytes of the 8-digit setup code.
        salt: Per-device salt (16 bytes for v1; ``derive_salt`` produces
            this from discriminator + setup code).
        iterations: PBKDF2 iteration count. Defaults to
            :data:`MHF_ITERATIONS`. Pass a smaller value only in tests
            where conformance is checked against a vector with a
            matching iteration count.
        dklen: Output length. Defaults to :data:`MHF_DKLEN`.

    Returns:
        ``dklen`` bytes of derived key material.

    Raises:
        ValueError: ``iterations`` or ``dklen`` is non-positive.
    """
    if iterations <= 0:
        raise ValueError(f"iterations must be positive, got {iterations}")
    if dklen <= 0:
        raise ValueError(f"dklen must be positive, got {dklen}")
    kdf = PBKDF2HMAC(
        algorithm=hashes.SHA256(),
        length=dklen,
        salt=salt,
        iterations=iterations,
    )
    return kdf.derive(setup_code_bytes)
