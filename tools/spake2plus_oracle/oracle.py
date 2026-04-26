"""DEC-084 SPAKE2+ reference oracle for MASH.

This module is a *third* independent implementation of MASH's SPAKE2+ profile
(separate from mash-py and mash-go). It is used to generate algorithmic
conformance vectors that both runtimes must reproduce byte-for-byte.

Sources:
    docs/decision-log.md DEC-084 (algorithm definition).
    docs/testing/behavior/commissioning-pase.md sections 2.1-2.5 (normative spec).

Design constraints:
    * Standard library only (hashlib, hmac, secrets, struct). No cryptography
      dependency, no ecdsa dependency. Raw P-256 affine arithmetic implemented
      below from FIPS 186-4 / SEC 2. The point of avoiding shared crypto
      libraries is that mash-go (crypto/elliptic) and mash-py (cryptography)
      cannot accidentally agree with the oracle by sharing a library bug.
    * No reference to mash-py or mash-go internals. Read the spec, type the
      math.
    * Total file size kept under 400 lines so the algorithm fits in one
      readable artifact.
"""

from __future__ import annotations

import hashlib
import hmac
import secrets
import struct
from dataclasses import dataclass
from typing import Tuple

# ----------------------------------------------------------------------------
# P-256 (secp256r1) parameters. FIPS 186-4 D.1.2.3 / SEC 2 2.4.2.
# ----------------------------------------------------------------------------

P = 0xFFFFFFFF00000001000000000000000000000000FFFFFFFFFFFFFFFFFFFFFFFF
A = -3 % P
B = 0x5AC635D8AA3A93E7B3EBBD55769886BC651D06B0CC53B0F63BCE3C3E27D2604B
N = 0xFFFFFFFF00000000FFFFFFFFFFFFFFFFBCE6FAADA7179E84F3B9CAC2FC632551
GX = 0x6B17D1F2E12C4247F8BCE6E563A440F277037D812DEB33A0F4A13945D898C296
GY = 0x4FE342E2FE1A7F9B8EE7EB4A7C0F9E162BCE33576B315ECECBB6406837BF51F5

# RFC 9383 §4 fixed P-256 SPAKE2+ generator points (DEC-084 §2.2).
M_X = 0x886E2F97ACE46E55BA9DD7242579F2993B64E16EF3DCAB95AFD497333D8FA12F
M_Y = 0x5FF355163E43CE224E0B0E65FF02AC8E5C7BE09419C785E0CA547D55A12E2D20
N_X = 0xD8BBD6C639C62937B04D997F38C3770719C629D7014D49A24B4F98BAA1292B49
N_Y = 0x07D60AA6BFADE45008A636337F5168C64D9BD36034808CD564490B1E656EDBE7

# Protocol-constant identity strings (DEC-084 §2.4).
CONTROLLER_IDENTITY = b"mash-controller"
DEVICE_IDENTITY = b"mash-device"

# Memory-Hard KDF parameters (DEC-084 §2.3).
PBKDF2_ITERATIONS = 100_000
PBKDF2_DKLEN = 80

# Salt derivation prefix (DEC-084 §2.3).
SALT_PREFIX = b"MASH PBKDF2 salt v1"
SALT_LEN = 16

# HKDF info strings (DEC-084 §2.4).
INFO_K_MAIN = b"MASH PASE Session Key"
INFO_K_CONFIRM = b"MASH PASE Confirmation"
INFO_K_ENCRYPT = b"MASH PASE Encryption"
INFO_KCA_KCB = b"ConfirmationKeys"


# ----------------------------------------------------------------------------
# P-256 affine point arithmetic.
# ----------------------------------------------------------------------------

# Identity (point at infinity) is represented as None. Every other point is a
# tuple (x, y) of integers in [0, P).
Point = Tuple[int, int]


class PointError(ValueError):
    """Raised when a point fails parsing or validation."""


def _inv_mod_p(x: int) -> int:
    # P is prime, so Fermat's little theorem suffices.
    return pow(x % P, P - 2, P)


def point_eq(p: Point | None, q: Point | None) -> bool:
    return p == q


def point_double(p: Point | None) -> Point | None:
    if p is None:
        return None
    x, y = p
    if y == 0:
        return None  # tangent vertical -> infinity
    s = ((3 * x * x + A) * _inv_mod_p(2 * y)) % P
    rx = (s * s - 2 * x) % P
    ry = (s * (x - rx) - y) % P
    return rx, ry


def point_add(p: Point | None, q: Point | None) -> Point | None:
    if p is None:
        return q
    if q is None:
        return p
    x1, y1 = p
    x2, y2 = q
    if x1 == x2:
        if (y1 + y2) % P == 0:
            return None  # P + (-P) = O
        return point_double(p)
    s = ((y2 - y1) * _inv_mod_p(x2 - x1)) % P
    rx = (s * s - x1 - x2) % P
    ry = (s * (x1 - rx) - y1) % P
    return rx, ry


def scalar_mult(k: int, p: Point | None) -> Point | None:
    """Compute k * p using double-and-add. Constant-timeness is not required
    here — this is a vector generator, not production crypto."""
    if p is None or k % N == 0:
        return None
    k = k % N
    result: Point | None = None
    addend: Point | None = p
    while k:
        if k & 1:
            result = point_add(result, addend)
        addend = point_double(addend)
        k >>= 1
    return result


def is_on_curve(p: Point | None) -> bool:
    if p is None:
        return False  # identity is on the curve mathematically, but DEC-084
        # requires explicit rejection -- treat it as off-curve for callers.
    x, y = p
    if not (0 <= x < P and 0 <= y < P):
        return False
    return (y * y - (x * x * x + A * x + B)) % P == 0


# ----------------------------------------------------------------------------
# SEC1 uncompressed encoding (65 bytes: 0x04 || X(32) || Y(32)).
# ----------------------------------------------------------------------------

def point_to_bytes(p: Point) -> bytes:
    if p is None:
        raise PointError("cannot encode point at infinity")
    x, y = p
    return b"\x04" + x.to_bytes(32, "big") + y.to_bytes(32, "big")


def point_from_bytes(b: bytes) -> Point:
    """Parse a SEC1 uncompressed point. DEC-084 §2.5 mandates rejection of:
    * non-65-byte / wrong-prefix encodings (parse failure)
    * off-curve points
    * the point at infinity (identity element)
    """
    if len(b) != 65 or b[0] != 0x04:
        raise PointError("not SEC1 uncompressed (need 65 bytes, leading 0x04)")
    x = int.from_bytes(b[1:33], "big")
    y = int.from_bytes(b[33:65], "big")
    if x == 0 and y == 0:
        raise PointError("point at infinity rejected (DEC-084 §2.5)")
    if not is_on_curve((x, y)):
        raise PointError("point not on curve")
    return (x, y)


def scalar_to_bytes(s: int) -> bytes:
    """32-byte big-endian encoding of a scalar in [0, n)."""
    return (s % N).to_bytes(32, "big")


# ----------------------------------------------------------------------------
# HKDF-SHA256 (RFC 5869). Stdlib doesn't ship HKDF, so we inline it.
# ----------------------------------------------------------------------------

def hkdf_extract(salt: bytes, ikm: bytes) -> bytes:
    if not salt:
        salt = b"\x00" * hashlib.sha256().digest_size
    return hmac.new(salt, ikm, hashlib.sha256).digest()


def hkdf_expand(prk: bytes, info: bytes, length: int) -> bytes:
    out = b""
    t = b""
    counter = 1
    while len(out) < length:
        t = hmac.new(prk, t + info + bytes([counter]), hashlib.sha256).digest()
        out += t
        counter += 1
    return out[:length]


def hkdf(salt: bytes, ikm: bytes, info: bytes, length: int) -> bytes:
    return hkdf_expand(hkdf_extract(salt, ikm), info, length)


# ----------------------------------------------------------------------------
# DEC-084 §2.3 — Salt, MHF, w0, w1, L.
# ----------------------------------------------------------------------------

def derive_salt(discriminator: int, setup_code: str) -> bytes:
    if not (0 <= discriminator < (1 << 12)):
        raise ValueError("discriminator must be a 12-bit value")
    if len(setup_code) != 8 or not setup_code.isdigit():
        raise ValueError("setup code must be 8 decimal digits")
    msg = SALT_PREFIX + discriminator.to_bytes(2, "big") + setup_code.encode("utf-8")
    return hashlib.sha256(msg).digest()[:SALT_LEN]


def mhf(setup_code: str, salt: bytes, iterations: int = PBKDF2_ITERATIONS) -> bytes:
    if len(salt) != SALT_LEN:
        raise ValueError(f"salt must be {SALT_LEN} bytes")
    return hashlib.pbkdf2_hmac(
        "sha256", setup_code.encode("utf-8"), salt, iterations, dklen=PBKDF2_DKLEN
    )


def derive_w0_w1(mhf_out: bytes) -> tuple[int, int]:
    if len(mhf_out) != PBKDF2_DKLEN:
        raise ValueError("mhf_out must be 80 bytes")
    w0 = int.from_bytes(mhf_out[:40], "big") % N
    w1 = int.from_bytes(mhf_out[40:], "big") % N
    if w0 == 0 or w1 == 0:
        raise ValueError("w0 or w1 reduced to zero (probability ~2^-256)")
    return w0, w1


def compute_L(w1: int) -> Point:
    L = scalar_mult(w1, (GX, GY))
    if L is None:
        raise ValueError("L is identity (w1 == 0)")
    return L


# ----------------------------------------------------------------------------
# DEC-084 §2.4 — Public values, shared secrets, transcript, key chain.
# ----------------------------------------------------------------------------

def client_public(w0: int, x: int) -> Point:
    """pA = x*G + w0*M  (controller side)."""
    xG = scalar_mult(x, (GX, GY))
    w0M = scalar_mult(w0, (M_X, M_Y))
    pA = point_add(xG, w0M)
    if pA is None:
        raise ValueError("pA is identity (degenerate scalar)")
    return pA


def server_public(w0: int, y: int) -> Point:
    """pB = y*G + w0*N  (device side)."""
    yG = scalar_mult(y, (GX, GY))
    w0N = scalar_mult(w0, (N_X, N_Y))
    pB = point_add(yG, w0N)
    if pB is None:
        raise ValueError("pB is identity (degenerate scalar)")
    return pB


def _negate_point(p: Point) -> Point:
    x, y = p
    return (x, (P - y) % P)


def controller_shared(w0: int, w1: int, x: int, pB: Point) -> tuple[Point, Point]:
    """Controller computes Z = x*(pB - w0*N) and V = w1*(pB - w0*N)."""
    w0N = scalar_mult(w0, (N_X, N_Y))
    Y_unmasked = point_add(pB, _negate_point(w0N))  # type: ignore[arg-type]
    if Y_unmasked is None:
        raise ValueError("Y_unmasked is identity")
    Z = scalar_mult(x, Y_unmasked)
    V = scalar_mult(w1, Y_unmasked)
    if Z is None or V is None:
        raise ValueError("Z or V is identity")
    return Z, V


def device_shared(w0: int, y: int, L: Point, pA: Point) -> tuple[Point, Point]:
    """Device computes Z = y*(pA - w0*M) and V = y*L."""
    w0M = scalar_mult(w0, (M_X, M_Y))
    X_unmasked = point_add(pA, _negate_point(w0M))  # type: ignore[arg-type]
    if X_unmasked is None:
        raise ValueError("X_unmasked is identity")
    Z = scalar_mult(y, X_unmasked)
    V = scalar_mult(y, L)
    if Z is None or V is None:
        raise ValueError("Z or V is identity")
    return Z, V


def _len_prefix(b: bytes) -> bytes:
    """8-byte little-endian unsigned-integer length prefix per RFC 9383 §3.3."""
    return struct.pack("<Q", len(b))


def transcript(
    controller_id: bytes,
    device_id: bytes,
    M: Point,
    N_pt: Point,
    pA: Point,
    pB: Point,
    Z: Point,
    V: Point,
    w0: int,
) -> bytes:
    fields = [
        controller_id,
        device_id,
        point_to_bytes(M),
        point_to_bytes(N_pt),
        point_to_bytes(pA),
        point_to_bytes(pB),
        point_to_bytes(Z),
        point_to_bytes(V),
        scalar_to_bytes(w0),
    ]
    out = b""
    for f in fields:
        out += _len_prefix(f) + f
    return out


def k_shared(TT: bytes) -> bytes:
    return hashlib.sha256(TT).digest()


def key_chain(K_shared: bytes) -> tuple[bytes, bytes, bytes]:
    K_main = hkdf(b"", K_shared, INFO_K_MAIN, 32)
    K_confirm = hkdf(b"", K_shared, INFO_K_CONFIRM, 32)
    K_encrypt = hkdf(b"", K_main, INFO_K_ENCRYPT, 32)
    return K_main, K_confirm, K_encrypt


def confirmation_keys(K_confirm: bytes) -> tuple[bytes, bytes]:
    """Per RFC 9383 §3.4 / DEC-084: KcA||KcB = HKDF(K_confirm, "ConfirmationKeys")."""
    blob = hkdf(b"", K_confirm, INFO_KCA_KCB, 32)
    return blob[:16], blob[16:]


def confirm_macs(KcA: bytes, KcB: bytes, pA: Point, pB: Point) -> tuple[bytes, bytes]:
    confirmA = hmac.new(KcA, point_to_bytes(pB), hashlib.sha256).digest()
    confirmB = hmac.new(KcB, point_to_bytes(pA), hashlib.sha256).digest()
    return confirmA, confirmB


# ----------------------------------------------------------------------------
# Convenience: full handshake from inputs.
# ----------------------------------------------------------------------------

@dataclass
class Handshake:
    setup_code: str
    discriminator: int
    salt: bytes
    mhf_out: bytes
    w0: int
    w1: int
    L: Point
    x: int
    y: int
    pA: Point
    pB: Point
    Z: Point
    V: Point
    TT: bytes
    K_shared: bytes
    K_main: bytes
    K_confirm: bytes
    K_encrypt: bytes
    KcA: bytes
    KcB: bytes
    confirmA: bytes
    confirmB: bytes


def full_handshake(
    setup_code: str,
    discriminator: int,
    x: int,
    y: int,
    *,
    iterations: int = PBKDF2_ITERATIONS,
) -> Handshake:
    salt = derive_salt(discriminator, setup_code)
    mhf_out = mhf(setup_code, salt, iterations=iterations)
    w0, w1 = derive_w0_w1(mhf_out)
    L = compute_L(w1)
    pA = client_public(w0, x)
    pB = server_public(w0, y)

    # Sanity: controller and device must compute the same Z, V.
    Zc, Vc = controller_shared(w0, w1, x, pB)
    Zd, Vd = device_shared(w0, y, L, pA)
    if Zc != Zd or Vc != Vd:
        raise AssertionError("controller/device disagree on Z or V (oracle bug)")

    TT = transcript(
        CONTROLLER_IDENTITY,
        DEVICE_IDENTITY,
        (M_X, M_Y),
        (N_X, N_Y),
        pA,
        pB,
        Zc,
        Vc,
        w0,
    )
    K_sh = k_shared(TT)
    K_main, K_confirm, K_encrypt = key_chain(K_sh)
    KcA, KcB = confirmation_keys(K_confirm)
    confirmA, confirmB = confirm_macs(KcA, KcB, pA, pB)

    return Handshake(
        setup_code=setup_code,
        discriminator=discriminator,
        salt=salt,
        mhf_out=mhf_out,
        w0=w0,
        w1=w1,
        L=L,
        x=x,
        y=y,
        pA=pA,
        pB=pB,
        Z=Zc,
        V=Vc,
        TT=TT,
        K_shared=K_sh,
        K_main=K_main,
        K_confirm=K_confirm,
        K_encrypt=K_encrypt,
        KcA=KcA,
        KcB=KcB,
        confirmA=confirmA,
        confirmB=confirmB,
    )


def random_scalar() -> int:
    """Uniform random in [1, n-1]. For *vector inputs* use fixed scalars; this
    is provided for the round-trip self-test only."""
    while True:
        s = secrets.randbelow(N)
        if s != 0:
            return s
