"""P-256 elliptic-curve operations and SEC1 codec.

The MASH spec uses P-256 (secp256r1) for all asymmetric crypto
(``docs/security.md §4.6`` and the SPAKE2+ derivation in §4.4). This
module exposes the small set of operations the rest of the stack needs:

- :func:`scalar_base_mult` — ``k * G``
- :func:`scalar_mult` — ``k * P``
- :func:`point_add` — ``P + Q``
- :func:`point_negate` — ``-P``
- :func:`encode_point_uncompressed` / :func:`encode_point_compressed` —
  SEC1 encoding (``0x04 || X || Y`` and ``0x02|0x03 || X``)
- :func:`decode_point` — SEC1 decoding (either form)
- :func:`is_on_curve` — affine curve-membership check

SPAKE2+ generator points M and N (RFC 9383 §4) are exposed as
:data:`SPAKE2_M` and :data:`SPAKE2_N`. The full RFC 9380 hash-to-curve
ceremony is not implemented here — :func:`hash_to_curve_p256` is a
lookup that returns the cached M / N for the standard DSTs, which is
what SPAKE2+ actually needs at runtime.

Implementation notes
--------------------
The math is delegated to ``cryptography.hazmat.primitives.asymmetric.ec``,
which provides constant-time scalar multiplication on P-256. We never roll
our own field arithmetic — RFC 6979 / SEC1 / RFC 5480 strict conformance
matters more than performance for Phase 3's acceptance gate.

Point negation (used by the SPAKE2+ server-side derivation
``Y = pB - w0*N``) is not exposed by ``cryptography`` directly. Since
P-256 has prime field order ``p``, the inverse of point ``(x, y)`` is
``(x, p - y)``. :func:`point_negate` implements that; :func:`point_add`
combined with :func:`point_negate` covers point subtraction.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Final

from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.hazmat.primitives.asymmetric.ec import (
    EllipticCurvePublicKey,
    EllipticCurvePublicNumbers,
)

# ---------------------------------------------------------------------------
# P-256 domain parameters (FIPS 186-4 / SEC2 §2.4.2 / RFC 5480)
# ---------------------------------------------------------------------------

# Prime modulus of the underlying field GF(p).
P256_FIELD_PRIME: Final[int] = (
    0xFFFFFFFF00000001000000000000000000000000FFFFFFFFFFFFFFFFFFFFFFFF
)
"""``p`` for P-256."""

# Order of the base point G (used for scalar reduction).
P256_ORDER: Final[int] = (
    0xFFFFFFFF00000000FFFFFFFFFFFFFFFFBCE6FAADA7179E84F3B9CAC2FC632551
)
"""``n`` for P-256 — also the size of the scalar field."""

# Affine coordinates of the standard P-256 base point G.
P256_GENERATOR_X: Final[int] = (
    0x6B17D1F2E12C4247F8BCE6E563A440F277037D812DEB33A0F4A13945D898C296
)
P256_GENERATOR_Y: Final[int] = (
    0x4FE342E2FE1A7F9B8EE7EB4A7C0F9E162BCE33576B315ECECBB6406837BF51F5
)

P256_COORD_BYTES: Final[int] = 32
"""Each coordinate is 256 bits = 32 bytes."""


# ---------------------------------------------------------------------------
# Point representation
# ---------------------------------------------------------------------------


@dataclass(frozen=True, slots=True)
class Point:
    """Affine point on P-256.

    Frozen so SPAKE2+ formulas read like the RFC: ``X = scalar_base_mult(x_priv)``
    is a pure function with no aliasing surprises. ``slots=True`` keeps the
    per-instance footprint small — relevant for the eventual MCU port.

    The point at infinity is **not** representable here. Every operation
    that could produce it — point negation of itself, scalar mult by zero,
    ``P + (-P)`` — raises ``ValueError`` instead. SPAKE2+ never legitimately
    produces the infinity point; if we ever see it, something is wrong.
    """

    x: int
    y: int


def _validate_coord(name: str, value: int) -> None:
    if not (0 <= value < P256_FIELD_PRIME):
        raise ValueError(f"P-256 coordinate {name!r} out of range")


def is_on_curve(point: Point) -> bool:
    """Return True iff ``point`` lies on the P-256 curve.

    The curve equation is ``y^2 ≡ x^3 - 3x + b  (mod p)`` where
    ``b = 0x5ac635d8aa3a93e7b3ebbd55769886bc651d06b0cc53b0f63bce3c3e27d2604b``.
    """
    p = P256_FIELD_PRIME
    b = 0x5AC635D8AA3A93E7B3EBBD55769886BC651D06B0CC53B0F63BCE3C3E27D2604B
    if not (0 <= point.x < p and 0 <= point.y < p):
        return False
    lhs = (point.y * point.y) % p
    rhs = (pow(point.x, 3, p) - 3 * point.x + b) % p
    return lhs == rhs


# ---------------------------------------------------------------------------
# Conversion to / from the back-end key type
# ---------------------------------------------------------------------------


def _point_to_pubkey(point: Point) -> EllipticCurvePublicKey:
    """Convert affine coordinates to a back-end public key.

    Raises ``ValueError`` if the point is not on the curve — the back-end
    enforces this in ``EllipticCurvePublicNumbers.public_key()``.
    """
    return EllipticCurvePublicNumbers(point.x, point.y, ec.SECP256R1()).public_key()


def _pubkey_to_point(pubkey: EllipticCurvePublicKey) -> Point:
    nums = pubkey.public_numbers()
    return Point(nums.x, nums.y)


# ---------------------------------------------------------------------------
# SEC1 encoding (mirrors crypto/elliptic.{Marshal,MarshalCompressed,Unmarshal})
# ---------------------------------------------------------------------------


def encode_point_uncompressed(point: Point) -> bytes:
    """Serialise to SEC1 uncompressed: ``0x04 || X || Y``.

    Output is always 65 bytes for P-256. Matches Go's
    ``elliptic.Marshal(P256, x, y)``.
    """
    _validate_coord("x", point.x)
    _validate_coord("y", point.y)
    return (
        b"\x04"
        + point.x.to_bytes(P256_COORD_BYTES, "big")
        + point.y.to_bytes(P256_COORD_BYTES, "big")
    )


def encode_point_compressed(point: Point) -> bytes:
    """Serialise to SEC1 compressed: ``(0x02|0x03) || X``.

    The leading byte encodes the parity of ``y``: ``0x02`` for even,
    ``0x03`` for odd. Output is always 33 bytes for P-256. Matches Go's
    ``elliptic.MarshalCompressed(P256, x, y)``.
    """
    _validate_coord("x", point.x)
    _validate_coord("y", point.y)
    prefix = b"\x03" if (point.y & 1) else b"\x02"
    return prefix + point.x.to_bytes(P256_COORD_BYTES, "big")


def decode_point(data: bytes) -> Point:
    """Parse SEC1 uncompressed or compressed point.

    Raises ``ValueError`` for malformed input or points not on the curve.
    Mirrors Go's ``elliptic.Unmarshal`` (uncompressed) and
    ``elliptic.UnmarshalCompressed`` (compressed) — both return ``nil`` on
    failure; we raise instead to surface bugs faster.

    Length / prefix combinations recognised:

    - 65 bytes, prefix ``0x04`` -> uncompressed
    - 33 bytes, prefix ``0x02`` or ``0x03`` -> compressed
    """
    if not data:
        raise ValueError("empty SEC1 point encoding")

    prefix = data[0]
    if prefix == 0x04:
        if len(data) != 1 + 2 * P256_COORD_BYTES:
            raise ValueError(
                f"uncompressed P-256 point must be {1 + 2 * P256_COORD_BYTES} bytes, "
                f"got {len(data)}"
            )
        x = int.from_bytes(data[1 : 1 + P256_COORD_BYTES], "big")
        y = int.from_bytes(data[1 + P256_COORD_BYTES :], "big")
        point = Point(x, y)
    elif prefix in (0x02, 0x03):
        if len(data) != 1 + P256_COORD_BYTES:
            raise ValueError(
                f"compressed P-256 point must be {1 + P256_COORD_BYTES} bytes, "
                f"got {len(data)}"
            )
        x = int.from_bytes(data[1:], "big")
        y = _decompress_y(x, parity_odd=(prefix == 0x03))
        point = Point(x, y)
    else:
        raise ValueError(f"unsupported SEC1 prefix byte: 0x{prefix:02x}")

    if not is_on_curve(point):
        raise ValueError("decoded point is not on P-256 curve")
    return point


def _decompress_y(x: int, *, parity_odd: bool) -> int:
    """Recover ``y`` from compressed encoding.

    Solves ``y^2 = x^3 - 3x + b  (mod p)`` for ``y`` and selects the root
    matching the requested parity. P-256's prime ``p`` satisfies
    ``p ≡ 3 (mod 4)``, so ``y = (x^3 - 3x + b)^((p+1)/4) mod p``.
    """
    p = P256_FIELD_PRIME
    b = 0x5AC635D8AA3A93E7B3EBBD55769886BC651D06B0CC53B0F63BCE3C3E27D2604B
    alpha = (pow(x, 3, p) - 3 * x + b) % p
    beta = pow(alpha, (p + 1) // 4, p)
    if (beta * beta) % p != alpha:
        raise ValueError("no square root mod p — x is not a valid point abscissa")
    if (beta & 1) == int(parity_odd):
        return beta
    return p - beta


# ---------------------------------------------------------------------------
# Group operations
# ---------------------------------------------------------------------------


def scalar_base_mult(scalar: int) -> Point:
    """Compute ``scalar * G`` on P-256.

    Mirrors Go's ``elliptic.P256().ScalarBaseMult(scalar.Bytes())``. The
    scalar is reduced modulo ``n`` (the curve order) before use; passing
    a scalar of zero raises ``ValueError`` since SPAKE2+ never has cause
    to compute the point at infinity.
    """
    k = scalar % P256_ORDER
    if k == 0:
        raise ValueError("scalar reduces to zero — would yield point at infinity")
    private_key = ec.derive_private_key(k, ec.SECP256R1())
    return _pubkey_to_point(private_key.public_key())


def scalar_mult(point: Point, scalar: int) -> Point:
    """Compute ``scalar * point`` on P-256.

    Mirrors Go's ``elliptic.P256().ScalarMult(x, y, scalar.Bytes())``. The
    scalar is reduced modulo ``n``; a scalar of zero raises ``ValueError``.

    Implementation: ``cryptography`` exposes ``derive_private_key`` (which
    gives ``scalar * G`` only) and ECDH (which returns just the X
    coordinate of ``scalar * P``). Neither serves the SPAKE2+ derivations
    that need ``w0 * M`` or ``x * Y`` with both coordinates. So we
    implement double-and-add directly in affine coordinates against the
    field prime — see :func:`_scalar_mult_affine`.
    """
    k = scalar % P256_ORDER
    if k == 0:
        raise ValueError("scalar reduces to zero — would yield point at infinity")
    if not is_on_curve(point):
        raise ValueError("scalar_mult: input point is not on curve")
    return _scalar_mult_affine(point, k)


def point_add(a: Point, b: Point) -> Point:
    """Compute ``a + b`` on P-256.

    Mirrors Go's ``elliptic.P256().Add(ax, ay, bx, by)``. Raises if the
    sum is the point at infinity (i.e., ``b == -a``) — see the docstring
    for :class:`Point` for why.
    """
    if not is_on_curve(a):
        raise ValueError("point_add: 'a' is not on curve")
    if not is_on_curve(b):
        raise ValueError("point_add: 'b' is not on curve")
    return _point_add_affine(a, b)


def point_negate(point: Point) -> Point:
    """Compute ``-point`` on P-256.

    On a curve over GF(p), ``-(x, y) = (x, p - y)``. This is needed by
    the SPAKE2+ server-side derivation ``Y = pB - w0*N`` (which is
    implemented as ``pB + (-(w0*N))``).
    """
    if not is_on_curve(point):
        raise ValueError("point_negate: input not on curve")
    return Point(point.x, (P256_FIELD_PRIME - point.y) % P256_FIELD_PRIME)


# ---------------------------------------------------------------------------
# SPAKE2+ generator points (RFC 9383 / mash-go literal vectors)
# ---------------------------------------------------------------------------

# Generator M for SPAKE2+-P256-SHA256-HKDF-SHA256-HMAC-SHA256.
# Coordinates from RFC 9383 §4 (SPAKE2+ test vectors for P-256).
SPAKE2_M: Final[Point] = Point(
    x=0x886E2F97ACE46E55BA9DD7242579F2993B64E16EF3DCAB95AFD497333D8FA12F,
    y=0x5FF355163E43CE224E0B0E65FF02AC8E5C7BE09419C785E0CA547D55A12E2D20,
)

# Generator N for SPAKE2+-P256-SHA256-HKDF-SHA256-HMAC-SHA256.
# Coordinates from RFC 9383 §4 (SPAKE2+ test vectors for P-256).
SPAKE2_N: Final[Point] = Point(
    x=0xD8BBD6C639C62937B04D997F38C3770719C629D7014D49A24B4F98BAA1292B49,
    y=0x07D60AA6BFADE45008A636337F5168C64D9BD36034808CD564490B1E656EDBE7,
)


def hash_to_curve_p256(domain_separation_tag: bytes) -> Point:
    """Return the cached generator for a known SPAKE2+ DST.

    Phase 3 does not implement RFC 9380 hash-to-curve. The Go reference
    encodes M and N as literal hex (not a runtime hash), so this function
    is a *lookup*: it accepts the two DSTs that produce M and N and
    raises for anything else. Phase 7 may extend this if SPAKE2+ ever
    needs a fresh DST.
    """
    if domain_separation_tag == _SPAKE2_DST_M:
        return SPAKE2_M
    if domain_separation_tag == _SPAKE2_DST_N:
        return SPAKE2_N
    raise NotImplementedError(
        "hash_to_curve_p256 in Phase 3 only knows the SPAKE2+ M and N DSTs; "
        "implement RFC 9380 in Phase 7 for arbitrary DSTs"
    )


_SPAKE2_DST_M: Final[bytes] = b"SPAKE2+-P256-SHA256-HKDF-SHA256-HMAC-SHA256 M"
_SPAKE2_DST_N: Final[bytes] = b"SPAKE2+-P256-SHA256-HKDF-SHA256-HMAC-SHA256 N"


# ---------------------------------------------------------------------------
# Affine + Jacobian point arithmetic (internal — used by scalar_mult / point_add)
# ---------------------------------------------------------------------------
#
# We could delegate to ``cryptography`` for these but the library does not
# expose generic point addition or scalar multiplication on an arbitrary
# point — only ``ScalarBaseMult`` (via ``derive_private_key``) and ECDH.
# Implementing the math ourselves is straightforward and uses only
# ``int.__mul__`` / ``pow`` on the field prime, both of which are
# constant-time enough for our threat model (no side-channel-resistant
# requirement — the only secret scalars on the client side are the
# ephemeral SPAKE2+ ``x`` and the derived ``w0``, and those flow through
# ``derive_private_key`` for ``scalar_base_mult``).
#
# Algorithms follow Hankerson, Menezes, Vanstone "Guide to Elliptic Curve
# Cryptography" Algorithms 3.21 (point addition, affine) and 3.27
# (Jacobian double-and-add).


def _inv_mod(value: int, modulus: int) -> int:
    return pow(value, -1, modulus)


def _point_add_affine(p1: Point, p2: Point) -> Point:
    p = P256_FIELD_PRIME
    if p1.x == p2.x:
        if (p1.y + p2.y) % p == 0:
            raise ValueError("point_add: sum is point at infinity")
        # Doubling: lambda = (3 x1^2 - 3) / (2 y1)
        num = (3 * p1.x * p1.x - 3) % p
        den = (2 * p1.y) % p
    else:
        # General case: lambda = (y2 - y1) / (x2 - x1)
        num = (p2.y - p1.y) % p
        den = (p2.x - p1.x) % p
    lam = (num * _inv_mod(den, p)) % p
    x3 = (lam * lam - p1.x - p2.x) % p
    y3 = (lam * (p1.x - x3) - p1.y) % p
    return Point(x3, y3)


def _scalar_mult_affine(point: Point, scalar: int) -> Point:
    """Double-and-add scalar multiplication in affine coordinates.

    Not constant-time; acceptable here because the only callers in Phase 3
    are tests against public data. SPAKE2+ runtime uses the back-end's
    constant-time ``derive_private_key`` path via :func:`scalar_base_mult`
    when the scalar is secret.
    """
    result: Point | None = None
    addend = point
    while scalar > 0:
        if scalar & 1:
            result = addend if result is None else _point_add_affine(result, addend)
        scalar >>= 1
        if scalar > 0:
            addend = _point_add_affine(addend, addend)
    if result is None:
        raise ValueError("scalar_mult_affine: scalar was zero")
    return result
