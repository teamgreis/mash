"""DEC-084 SPAKE2+ implementation for MASH commissioning.

Normative spec: ``docs/testing/behavior/commissioning-pase.md §2``
(rewritten by DEC-084). This module is a Python port that produces
byte-identical intermediate values to mash-go's
``pkg/commissioning/spake2plus.go`` and the third-party reference oracle
in ``tools/spake2plus_oracle/oracle.py``.

The algorithmic surface mirrors the spec's section structure so a reader
can trace each step back to its normative source:

§2.3 (verifier derivation):
    :func:`derive_salt`, :func:`derive_w0_w1`, :func:`compute_L`,
    :func:`generate_verifier`, :class:`Verifier`

§2.4 (handshake):
    :func:`client_public`, :func:`server_public`,
    :func:`compose_transcript`, :func:`k_shared`, :func:`key_chain`,
    :func:`confirmation_keys`, :func:`confirm_macs`

§2.5 (point validation):
    :func:`validate_point`,
    :class:`InvalidPointError`, :class:`IdentityElementError`

State-machine surface (handshake driver):
    :class:`SPAKE2PlusClient`, :class:`SPAKE2PlusServer`

Identity strings, HKDF info strings, and the salt prefix are protocol
constants — see DEC-084 §2.3-§2.4.

**Why a parallel pure-function and stateful API.** Pure functions enable
the Phase 7B conformance vectors to test individual algorithm stages
(MHF, transcript, key-chain) in isolation. The stateful classes wrap
those primitives with random ephemeral generation and key caching so
the eventual PASE-message driver in Phase 8 has a small surface to
call.
"""

from __future__ import annotations

import hashlib
import secrets
import struct
from dataclasses import dataclass
from typing import Final

from mash_py.commissioning.mhf import MHF_DKLEN, MHF_ITERATIONS, pbkdf2_mhf
from mash_py.crypto.ec import (
    P256_ORDER,
    SPAKE2_M,
    SPAKE2_N,
    Point,
    encode_point_uncompressed,
    is_on_curve,
    point_add,
    point_negate,
    scalar_base_mult,
    scalar_mult,
)
from mash_py.crypto.kdf import hkdf_sha256
from mash_py.crypto.mac import hmac_sha256, hmac_sha256_verify

# ---------------------------------------------------------------------------
# Protocol constants (DEC-084 §2.3-§2.4)
# ---------------------------------------------------------------------------

SALT_PREFIX: Final[bytes] = b"MASH PBKDF2 salt v1"
"""Domain-separation tag for PBKDF2 salt derivation (DEC-084 §2.3)."""

SALT_LEN: Final[int] = 16
"""Bytes of SHA-256 output retained as the salt."""

CONTROLLER_IDENTITY: Final[bytes] = b"mash-controller"
DEVICE_IDENTITY: Final[bytes] = b"mash-device"

INFO_K_MAIN: Final[bytes] = b"MASH PASE Session Key"
INFO_K_CONFIRM: Final[bytes] = b"MASH PASE Confirmation"
INFO_K_ENCRYPT: Final[bytes] = b"MASH PASE Encryption"
INFO_KCA_KCB: Final[bytes] = b"ConfirmationKeys"

POINT_SIZE: Final[int] = 65
"""SEC1 uncompressed P-256 point size."""

SCALAR_SIZE: Final[int] = 32
"""Big-endian P-256 scalar encoding size."""

SHARED_SECRET_SIZE: Final[int] = 32
"""SHA-256 / HKDF-SHA256 / HMAC-SHA256 output size."""

CONFIRM_KEY_SIZE: Final[int] = 16
"""Half of HKDF output split into KcA || KcB."""

CONFIRM_MAC_SIZE: Final[int] = 32
"""HMAC-SHA256 output size."""

DISCRIMINATOR_BITS: Final[int] = 12
"""DEC-014 / DEC-084: discriminator is 12 bits, encoded BE16."""


# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------


class SPAKE2PlusError(ValueError):
    """Base class for SPAKE2+ protocol errors.

    Subclass of ``ValueError`` so callers that already catch
    ``ValueError`` for input validation continue to handle these.
    """


class InvalidPointError(SPAKE2PlusError):
    """Received SEC1 point failed §2.5 validation (parse or curve check)."""


class IdentityElementError(InvalidPointError):
    """Received point is the identity element (DEC-084 §2.5)."""


class ConfirmationFailedError(SPAKE2PlusError):
    """Peer's confirmation MAC did not verify."""


# ---------------------------------------------------------------------------
# §2.3 — Salt, MHF, w0 / w1, L
# ---------------------------------------------------------------------------


def _check_discriminator(discriminator: int) -> None:
    if not (0 <= discriminator < (1 << DISCRIMINATOR_BITS)):
        raise ValueError(
            f"discriminator must fit in {DISCRIMINATOR_BITS} bits, got {discriminator}"
        )


def _check_setup_code(setup_code: str) -> None:
    if len(setup_code) != 8 or not setup_code.isdigit():
        raise ValueError("setup code must be 8 decimal digits")


def derive_salt(discriminator: int, setup_code: str) -> bytes:
    """DEC-084 §2.3 salt derivation.

    ``salt = SHA-256("MASH PBKDF2 salt v1" || disc(BE16) || code_utf8)[0:16]``

    Both controller and device compute this independently from QR-code
    contents — no exchange needed (saves one round trip vs Matter's
    ``PBKDFParameter``).
    """
    _check_discriminator(discriminator)
    _check_setup_code(setup_code)
    h = hashlib.sha256()
    h.update(SALT_PREFIX)
    h.update(discriminator.to_bytes(2, "big"))
    h.update(setup_code.encode("utf-8"))
    return h.digest()[:SALT_LEN]


def derive_w0_w1(mhf_out: bytes) -> tuple[int, int]:
    """Split 80-byte MHF output into two 40-byte halves; reduce mod n.

    Returns ``(w0, w1)`` as integers in ``[1, n)``. 40-byte slices reduced
    modulo 256-bit ``n`` give near-uniform distribution per RFC 9383 §3.2.

    Raises :class:`SPAKE2PlusError` if either reduces to zero (probability
    ~2^-256; never observed in practice).
    """
    if len(mhf_out) != MHF_DKLEN:
        raise ValueError(f"mhf_out must be {MHF_DKLEN} bytes, got {len(mhf_out)}")
    w0 = int.from_bytes(mhf_out[:40], "big") % P256_ORDER
    w1 = int.from_bytes(mhf_out[40:], "big") % P256_ORDER
    if w0 == 0 or w1 == 0:
        raise SPAKE2PlusError("w0 or w1 reduced to zero (probability ~2^-256)")
    return w0, w1


def scalar_to_bytes(scalar: int) -> bytes:
    """32-byte big-endian encoding of a P-256 scalar (reduced mod n)."""
    return (scalar % P256_ORDER).to_bytes(SCALAR_SIZE, "big")


def compute_L(w1: int) -> bytes:
    """``L = w1 * G``, encoded SEC1 uncompressed (65 bytes).

    The device stores ``L`` as part of its verifier and discards ``w1``
    (DEC-084 §2.3).
    """
    if w1 % P256_ORDER == 0:
        raise SPAKE2PlusError("w1 must be non-zero mod n")
    return encode_point_uncompressed(scalar_base_mult(w1))


@dataclass(frozen=True, slots=True)
class Verifier:
    """Device-side stored material per DEC-084 §2.3.

    Persisted in NVS after factory provisioning; the setup code is then
    discarded from device storage. CBOR keying matches mash-go's
    :go:type:`commissioning.Verifier` field numbers (1..5) for storage
    interoperability with Go-provisioned devices.

    Attributes:
        discriminator: 12-bit device discriminator (CBOR key 1).
        salt: 16-byte derived PBKDF2 salt (CBOR key 2).
        w0: 32-byte big-endian scalar (CBOR key 3).
        L: 65-byte SEC1 uncompressed verifier point (CBOR key 4).
        iterations: PBKDF2 iteration count used (CBOR key 5).
    """

    discriminator: int
    salt: bytes
    w0: bytes
    L: bytes
    iterations: int


def generate_verifier(
    setup_code: str,
    discriminator: int,
    *,
    iterations: int = MHF_ITERATIONS,
) -> Verifier:
    """Run the full §2.3 derivation and return the device-side ``Verifier``.

    Both controller and device run this from QR-code data; the device
    persists the result, while the controller re-derives it on every
    commissioning attempt and discards it after the session ends.
    """
    salt = derive_salt(discriminator, setup_code)
    mhf_out = pbkdf2_mhf(setup_code.encode("utf-8"), salt, iterations=iterations)
    w0_int, w1_int = derive_w0_w1(mhf_out)
    L_bytes = compute_L(w1_int)
    return Verifier(
        discriminator=discriminator,
        salt=salt,
        w0=scalar_to_bytes(w0_int),
        L=L_bytes,
        iterations=iterations,
    )


# ---------------------------------------------------------------------------
# §2.5 — Point validation
# ---------------------------------------------------------------------------


def validate_point(point_bytes: bytes) -> Point:
    """Apply DEC-084 §2.5 point validation.

    Three rejection cases (each fatal, all classified as auth failure
    on the wire — peers MUST NOT learn which check failed):

    1. Not 65-byte SEC1 uncompressed (length or prefix wrong).
    2. The identity element (``0x04 || 0..0 || 0..0``).
    3. Off the P-256 curve.

    Returns the parsed :class:`Point` if all checks pass.
    """
    if len(point_bytes) != POINT_SIZE or point_bytes[0] != 0x04:
        raise InvalidPointError(
            f"not SEC1 uncompressed (need {POINT_SIZE} bytes, leading 0x04)"
        )
    x = int.from_bytes(point_bytes[1:33], "big")
    y = int.from_bytes(point_bytes[33:65], "big")
    if x == 0 and y == 0:
        # Spec: explicit identity-element rejection. Done before
        # ``is_on_curve`` so the diagnostic is precise. (The curve check
        # would also reject (0,0) since b != 0, but DEC-084 §2.5 names
        # this case explicitly because pre-DEC-084 implementations
        # missed it.)
        raise IdentityElementError("point at infinity rejected (DEC-084 §2.5)")
    point = Point(x, y)
    if not is_on_curve(point):
        raise InvalidPointError("point not on P-256 curve")
    return point


# ---------------------------------------------------------------------------
# §2.4 — Public values, transcript, key chain
# ---------------------------------------------------------------------------


def client_public(w0: int, x: int) -> bytes:
    """``pA = x * G + w0 * M`` (controller side). 65-byte SEC1 uncompressed."""
    xG = scalar_base_mult(x)
    w0M = scalar_mult(SPAKE2_M, w0)
    return encode_point_uncompressed(point_add(xG, w0M))


def server_public(w0: int, y: int) -> bytes:
    """``pB = y * G + w0 * N`` (device side). 65-byte SEC1 uncompressed."""
    yG = scalar_base_mult(y)
    w0N = scalar_mult(SPAKE2_N, w0)
    return encode_point_uncompressed(point_add(yG, w0N))


def _len_prefix(field: bytes) -> bytes:
    """8-byte little-endian unsigned length prefix per RFC 9383 §3.3.

    ``struct.pack("<Q", n)`` raises ``struct.error`` if ``n >= 2**64``;
    no MASH transcript field exceeds 65 bytes, so this is safe.
    """
    return struct.pack("<Q", len(field))


def compose_transcript(
    controller_identity: bytes,
    device_identity: bytes,
    M_bytes: bytes,
    N_bytes: bytes,
    pA: bytes,
    pB: bytes,
    Z_bytes: bytes,
    V_bytes: bytes,
    w0_bytes: bytes,
) -> bytes:
    """RFC 9383 §3.3 length-prefixed transcript ``TT`` per DEC-084 §2.4.

    Field order is fixed; both sides MUST construct ``TT`` identically.
    Each field is preceded by its 8-byte little-endian length.
    """
    fields = [
        controller_identity,
        device_identity,
        M_bytes,
        N_bytes,
        pA,
        pB,
        Z_bytes,
        V_bytes,
        w0_bytes,
    ]
    return b"".join(_len_prefix(f) + f for f in fields)


def k_shared(transcript: bytes) -> bytes:
    """``K_shared = SHA-256(TT)`` (DEC-084 §2.4)."""
    return hashlib.sha256(transcript).digest()


def key_chain(K_shared_bytes: bytes) -> tuple[bytes, bytes, bytes]:
    """Three-key HKDF chain from K_shared (DEC-084 §2.4).

    ``K_encrypt`` is derived from ``K_main`` (not from K_shared directly)
    so that K_main can be exposed to other layers — for session
    identification, e.g. — without revealing K_encrypt.
    """
    if len(K_shared_bytes) != SHARED_SECRET_SIZE:
        raise ValueError(f"K_shared must be {SHARED_SECRET_SIZE} bytes")
    K_main = hkdf_sha256(K_shared_bytes, salt=None, info=INFO_K_MAIN, length=SHARED_SECRET_SIZE)
    K_confirm = hkdf_sha256(
        K_shared_bytes, salt=None, info=INFO_K_CONFIRM, length=SHARED_SECRET_SIZE
    )
    K_encrypt = hkdf_sha256(K_main, salt=None, info=INFO_K_ENCRYPT, length=SHARED_SECRET_SIZE)
    return K_main, K_confirm, K_encrypt


def confirmation_keys(K_confirm: bytes) -> tuple[bytes, bytes]:
    """Per RFC 9383 §3.4 / DEC-084: ``KcA || KcB = HKDF(K_confirm, ...).``"""
    if len(K_confirm) != SHARED_SECRET_SIZE:
        raise ValueError(f"K_confirm must be {SHARED_SECRET_SIZE} bytes")
    blob = hkdf_sha256(K_confirm, salt=None, info=INFO_KCA_KCB, length=SHARED_SECRET_SIZE)
    return blob[:CONFIRM_KEY_SIZE], blob[CONFIRM_KEY_SIZE:]


def confirm_macs(KcA: bytes, KcB: bytes, pA: bytes, pB: bytes) -> tuple[bytes, bytes]:
    """``confirmA = HMAC(KcA, pB)``, ``confirmB = HMAC(KcB, pA)``.

    Each side MACs the *peer's* point with its *own* MAC key. Distinct
    keys (KcA vs KcB) prevent reflection — the controller's confirmation
    cannot be replayed as the device's even though both sides know
    K_confirm.
    """
    return hmac_sha256(KcA, pB), hmac_sha256(KcB, pA)


# ---------------------------------------------------------------------------
# Random ephemeral scalar
# ---------------------------------------------------------------------------


def random_ephemeral() -> int:
    """Uniform random scalar in ``[1, n - 1]`` from a CSPRNG.

    Uses :mod:`secrets` per DEC-084 §2.5 ("cryptographically secure
    RNG"). The retry loop guards the negligible-probability case
    ``s == 0``.
    """
    while True:
        s = secrets.randbelow(P256_ORDER)
        if s != 0:
            return s


# ---------------------------------------------------------------------------
# Stateful client / server (handshake driver)
# ---------------------------------------------------------------------------


def _spake2_M_bytes() -> bytes:
    return encode_point_uncompressed(SPAKE2_M)


def _spake2_N_bytes() -> bytes:
    return encode_point_uncompressed(SPAKE2_N)


class SPAKE2PlusClient:
    """Controller-side state machine for the DEC-084 SPAKE2+ exchange.

    Lifecycle::

        client = SPAKE2PlusClient(setup_code, discriminator)
        pA = client.public_value()                        # send to device
        client.process_server_value(pB)                    # device's pB
        confirm_a = client.confirmation()                  # send to device
        client.verify_server_confirmation(confirm_b)       # device's MAC

    After ``process_server_value`` succeeds, ``shared_secret`` (K_main)
    and ``encryption_key`` (K_encrypt) are available.
    """

    def __init__(
        self,
        setup_code: str,
        discriminator: int,
        *,
        iterations: int = MHF_ITERATIONS,
        x: int | None = None,
    ) -> None:
        """Derive verifier scalars and select the ephemeral.

        Args:
            setup_code: 8-digit setup code from QR / printed label.
            discriminator: 12-bit device discriminator from QR.
            iterations: PBKDF2 iteration count. Override only in tests.
            x: Optional fixed ephemeral scalar (used by conformance
                vectors for deterministic outputs). Production code
                must let this default to a fresh CSPRNG draw.
        """
        salt = derive_salt(discriminator, setup_code)
        mhf_out = pbkdf2_mhf(setup_code.encode("utf-8"), salt, iterations=iterations)
        self._w0, self._w1 = derive_w0_w1(mhf_out)
        ephemeral = x if x is not None else random_ephemeral()
        if ephemeral % P256_ORDER == 0:
            raise ValueError("ephemeral scalar x reduces to zero")
        self._x = ephemeral

        self._pA: bytes | None = None
        self._pB: bytes | None = None
        self._k_main: bytes | None = None
        self._k_confirm: bytes | None = None
        self._k_encrypt: bytes | None = None
        self._kcA: bytes | None = None
        self._kcB: bytes | None = None

    def public_value(self) -> bytes:
        """Return ``pA = x*G + w0*M`` (cached after first call)."""
        if self._pA is None:
            self._pA = client_public(self._w0, self._x)
        return self._pA

    def process_server_value(self, pB: bytes) -> None:
        """Validate ``pB``, compute Z, V, and derive the session-key chain.

        Raises :class:`InvalidPointError` / :class:`IdentityElementError`
        on invalid input. Any internal arithmetic failure (extremely
        unlikely outside an active attack) is wrapped as
        :class:`SPAKE2PlusError` so the caller does not need to know
        about :class:`ValueError` from the EC primitives.
        """
        pB_point = validate_point(pB)
        self._pB = pB
        self.public_value()  # cache pA

        try:
            # Y_unmasked = pB - w0 * N
            w0N = scalar_mult(SPAKE2_N, self._w0)
            Y_unmasked = point_add(pB_point, point_negate(w0N))
            # Z = x * Y_unmasked, V = w1 * Y_unmasked
            Z = scalar_mult(Y_unmasked, self._x)
            V = scalar_mult(Y_unmasked, self._w1)
        except ValueError as exc:
            # Surface as auth failure — see DEC-084 §2.5 ("MUST NOT
            # leak which specific check failed").
            raise SPAKE2PlusError(f"degenerate handshake state: {exc}") from exc

        self._derive_keys(Z, V)

    def _derive_keys(self, Z: Point, V: Point) -> None:
        assert self._pA is not None and self._pB is not None
        TT = compose_transcript(
            CONTROLLER_IDENTITY,
            DEVICE_IDENTITY,
            _spake2_M_bytes(),
            _spake2_N_bytes(),
            self._pA,
            self._pB,
            encode_point_uncompressed(Z),
            encode_point_uncompressed(V),
            scalar_to_bytes(self._w0),
        )
        K = k_shared(TT)
        self._k_main, self._k_confirm, self._k_encrypt = key_chain(K)
        self._kcA, self._kcB = confirmation_keys(self._k_confirm)

    def confirmation(self) -> bytes:
        """Compute ``confirmA = HMAC(KcA, pB)`` for the controller→device MAC."""
        if self._kcA is None or self._pB is None:
            raise SPAKE2PlusError("call process_server_value first")
        return hmac_sha256(self._kcA, self._pB)

    def verify_server_confirmation(self, confirmB: bytes) -> None:
        """Constant-time-verify the device's confirmB; raise on mismatch."""
        if self._kcB is None or self._pA is None:
            raise SPAKE2PlusError("call process_server_value first")
        if not hmac_sha256_verify(self._kcB, self._pA, confirmB):
            raise ConfirmationFailedError("device confirmation MAC did not verify")

    @property
    def shared_secret(self) -> bytes:
        """K_main from the three-key chain (DEC-084 §2.4)."""
        if self._k_main is None:
            raise SPAKE2PlusError("call process_server_value first")
        return self._k_main

    @property
    def encryption_key(self) -> bytes:
        """K_encrypt = HKDF(K_main, "MASH PASE Encryption", 32)."""
        if self._k_encrypt is None:
            raise SPAKE2PlusError("call process_server_value first")
        return self._k_encrypt


class SPAKE2PlusServer:
    """Device-side state machine for the DEC-084 SPAKE2+ exchange.

    Lifecycle::

        server = SPAKE2PlusServer(verifier)
        pB = server.public_value()                  # send to controller
        server.process_client_value(pA)              # controller's pA
        server.verify_client_confirmation(confirmA)  # controller's MAC
        confirmB = server.confirmation()             # send to controller

    Constructed from the persisted :class:`Verifier` (no setup code on
    the device after factory provisioning per DEC-084 §2.3).
    """

    def __init__(
        self,
        verifier: Verifier,
        *,
        y: int | None = None,
    ) -> None:
        """Load verifier scalars and select the ephemeral.

        Args:
            verifier: Persisted device verifier.
            y: Optional fixed ephemeral scalar (vectors only).
        """
        if len(verifier.w0) != SCALAR_SIZE:
            raise ValueError(f"verifier.w0 must be {SCALAR_SIZE} bytes")
        # Validate L per §2.5 — the verifier is supposed to have been
        # validated at provisioning time, but checking again here makes
        # the server robust against corrupted NVS.
        L_point = validate_point(verifier.L)
        self._verifier = verifier
        self._w0 = int.from_bytes(verifier.w0, "big")
        if self._w0 == 0:
            raise SPAKE2PlusError("verifier w0 is zero")
        self._L = L_point

        ephemeral = y if y is not None else random_ephemeral()
        if ephemeral % P256_ORDER == 0:
            raise ValueError("ephemeral scalar y reduces to zero")
        self._y = ephemeral

        self._pA: bytes | None = None
        self._pB: bytes | None = None
        self._k_main: bytes | None = None
        self._k_confirm: bytes | None = None
        self._k_encrypt: bytes | None = None
        self._kcA: bytes | None = None
        self._kcB: bytes | None = None

    def public_value(self) -> bytes:
        """Return ``pB = y*G + w0*N`` (cached)."""
        if self._pB is None:
            self._pB = server_public(self._w0, self._y)
        return self._pB

    def process_client_value(self, pA: bytes) -> None:
        """Validate ``pA``, compute Z, V, and derive the session-key chain."""
        pA_point = validate_point(pA)
        self._pA = pA
        self.public_value()  # cache pB

        try:
            # X_unmasked = pA - w0 * M
            w0M = scalar_mult(SPAKE2_M, self._w0)
            X_unmasked = point_add(pA_point, point_negate(w0M))
            # Z = y * X_unmasked, V = y * L
            Z = scalar_mult(X_unmasked, self._y)
            V = scalar_mult(self._L, self._y)
        except ValueError as exc:
            raise SPAKE2PlusError(f"degenerate handshake state: {exc}") from exc

        self._derive_keys(Z, V)

    def _derive_keys(self, Z: Point, V: Point) -> None:
        assert self._pA is not None and self._pB is not None
        TT = compose_transcript(
            CONTROLLER_IDENTITY,
            DEVICE_IDENTITY,
            _spake2_M_bytes(),
            _spake2_N_bytes(),
            self._pA,
            self._pB,
            encode_point_uncompressed(Z),
            encode_point_uncompressed(V),
            scalar_to_bytes(self._w0),
        )
        K = k_shared(TT)
        self._k_main, self._k_confirm, self._k_encrypt = key_chain(K)
        self._kcA, self._kcB = confirmation_keys(self._k_confirm)

    def confirmation(self) -> bytes:
        """Compute ``confirmB = HMAC(KcB, pA)`` for the device→controller MAC."""
        if self._kcB is None or self._pA is None:
            raise SPAKE2PlusError("call process_client_value first")
        return hmac_sha256(self._kcB, self._pA)

    def verify_client_confirmation(self, confirmA: bytes) -> None:
        """Constant-time-verify the controller's confirmA; raise on mismatch."""
        if self._kcA is None or self._pB is None:
            raise SPAKE2PlusError("call process_client_value first")
        if not hmac_sha256_verify(self._kcA, self._pB, confirmA):
            raise ConfirmationFailedError("controller confirmation MAC did not verify")

    @property
    def shared_secret(self) -> bytes:
        if self._k_main is None:
            raise SPAKE2PlusError("call process_client_value first")
        return self._k_main

    @property
    def encryption_key(self) -> bytes:
        if self._k_encrypt is None:
            raise SPAKE2PlusError("call process_client_value first")
        return self._k_encrypt
