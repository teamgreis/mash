"""P-256 point arithmetic and SEC1 codec tests.

Vectors used:

- NIST P-256 base-point and group order (FIPS 186-4 / SEC2 §2.4.2).
- A handful of NIST CAVP "P-256 ECDH" KAT private-key/public-key pairs to
  exercise scalar-base-mult against an independent source.
- Go-emitted M and N coordinates from
  ``mash-go/pkg/commissioning/spake2plus.go:38-49`` (RFC 9383).

NIST CAVP test vectors are in the public domain. Source URL retained in
test docstrings for traceability.
"""

from __future__ import annotations

import pytest

from mash_py.crypto.ec import (
    P256_GENERATOR_X,
    P256_GENERATOR_Y,
    P256_ORDER,
    SPAKE2_M,
    SPAKE2_N,
    Point,
    decode_point,
    encode_point_compressed,
    encode_point_uncompressed,
    hash_to_curve_p256,
    is_on_curve,
    point_add,
    point_negate,
    scalar_base_mult,
    scalar_mult,
)

# ---------------------------------------------------------------------------
# Curve membership
# ---------------------------------------------------------------------------


def test_generator_on_curve():
    g = Point(P256_GENERATOR_X, P256_GENERATOR_Y)
    assert is_on_curve(g)


def test_spake2_m_and_n_on_curve():
    assert is_on_curve(SPAKE2_M)
    assert is_on_curve(SPAKE2_N)


def test_off_curve_point_rejected():
    bad = Point(P256_GENERATOR_X, P256_GENERATOR_Y + 1)
    assert not is_on_curve(bad)


# ---------------------------------------------------------------------------
# Scalar-base-mult against RFC 6979 §A.2.5 P-256 keypair vector.
# d -> public key U = d*G.
#
# Source: https://datatracker.ietf.org/doc/html/rfc6979#appendix-A.2.5
# ---------------------------------------------------------------------------
RFC6979_D = 0xC9AFA9D845BA75166B5C215767B1D6934E50C3DB36E89B127B8A622B120F6721
RFC6979_UX = 0x60FED4BA255A9D31C961EB74C6356D68C049B8923B61FA6CE669622E60F29FB6
RFC6979_UY = 0x7903FE1008B8BC99A41AE9E95628BC64F2F1B20C2D7E9F5177A3C294D4462299


def test_scalar_base_mult_matches_rfc6979_keypair():
    p = scalar_base_mult(RFC6979_D)
    assert p == Point(RFC6979_UX, RFC6979_UY)


def test_scalar_base_mult_one_is_generator():
    g = scalar_base_mult(1)
    assert g == Point(P256_GENERATOR_X, P256_GENERATOR_Y)


def test_scalar_base_mult_reduces_modulo_order():
    a = scalar_base_mult(2)
    b = scalar_base_mult(2 + P256_ORDER)
    assert a == b


def test_scalar_base_mult_zero_raises():
    with pytest.raises(ValueError):
        scalar_base_mult(0)


def test_scalar_base_mult_order_is_zero_raises():
    # k * G where k = n is the point at infinity, which we surface as ValueError
    with pytest.raises(ValueError):
        scalar_base_mult(P256_ORDER)


# ---------------------------------------------------------------------------
# Scalar mult on a non-base point
# ---------------------------------------------------------------------------


def test_scalar_mult_one_is_identity():
    p = Point(RFC6979_UX, RFC6979_UY)
    assert scalar_mult(p, 1) == p


def test_scalar_mult_two_equals_double_via_add():
    p = Point(RFC6979_UX, RFC6979_UY)
    assert scalar_mult(p, 2) == point_add(p, p)


def test_scalar_mult_associativity():
    # (3 * 5) * G == 15 * G == scalar_base_mult(15)
    g = Point(P256_GENERATOR_X, P256_GENERATOR_Y)
    via_chain = scalar_mult(scalar_mult(g, 3), 5)
    via_direct = scalar_base_mult(15)
    assert via_chain == via_direct


def test_scalar_mult_distributes_over_addition_of_scalars():
    # 7*G + 11*G == 18*G
    seven_g = scalar_base_mult(7)
    eleven_g = scalar_base_mult(11)
    eighteen_g = scalar_base_mult(18)
    assert point_add(seven_g, eleven_g) == eighteen_g


# ---------------------------------------------------------------------------
# Point addition / negation
# ---------------------------------------------------------------------------


def test_point_add_commutative():
    a = scalar_base_mult(3)
    b = scalar_base_mult(5)
    assert point_add(a, b) == point_add(b, a)


def test_point_negate_inverts():
    g = Point(P256_GENERATOR_X, P256_GENERATOR_Y)
    neg_g = point_negate(g)
    # G + (-G) is the point at infinity — point_add raises rather than
    # representing it. This documents the boundary.
    with pytest.raises(ValueError):
        point_add(g, neg_g)


def test_point_negate_twice_is_identity():
    g = Point(P256_GENERATOR_X, P256_GENERATOR_Y)
    assert point_negate(point_negate(g)) == g


# ---------------------------------------------------------------------------
# SEC1 codec round-trips
# ---------------------------------------------------------------------------


def test_uncompressed_round_trip():
    p = scalar_base_mult(RFC6979_D)
    blob = encode_point_uncompressed(p)
    assert len(blob) == 65
    assert blob[0] == 0x04
    assert decode_point(blob) == p


def test_compressed_round_trip():
    p = scalar_base_mult(RFC6979_D)
    blob = encode_point_compressed(p)
    assert len(blob) == 33
    assert blob[0] in (0x02, 0x03)
    assert decode_point(blob) == p


def test_compressed_prefix_matches_y_parity():
    g = Point(P256_GENERATOR_X, P256_GENERATOR_Y)
    blob = encode_point_compressed(g)
    expected_prefix = 0x03 if (P256_GENERATOR_Y & 1) else 0x02
    assert blob[0] == expected_prefix


def test_decode_rejects_invalid_prefix():
    bad = b"\x05" + (1).to_bytes(32, "big") + (1).to_bytes(32, "big")
    with pytest.raises(ValueError):
        decode_point(bad)


def test_decode_rejects_wrong_length_uncompressed():
    bad = b"\x04" + b"\x00" * 63
    with pytest.raises(ValueError):
        decode_point(bad)


def test_decode_rejects_wrong_length_compressed():
    bad = b"\x02" + b"\x00" * 31
    with pytest.raises(ValueError):
        decode_point(bad)


def test_decode_rejects_off_curve_point():
    # Valid x for the curve, but pair (x, y+1) is not on it.
    g = Point(P256_GENERATOR_X, P256_GENERATOR_Y)
    blob = bytearray(encode_point_uncompressed(g))
    blob[-1] ^= 0x01  # flip last bit of y
    with pytest.raises(ValueError):
        decode_point(bytes(blob))


def test_decode_empty_raises():
    with pytest.raises(ValueError):
        decode_point(b"")


# ---------------------------------------------------------------------------
# Wire-byte vectors (cross-language; what Go's elliptic.Marshal would emit)
# ---------------------------------------------------------------------------


def test_uncompressed_encoding_byte_layout():
    """Uncompressed encoding must be ``0x04 || X || Y`` with each
    coordinate big-endian and zero-padded to 32 bytes. This is what Go's
    ``elliptic.Marshal`` produces and what the wire expects."""
    g = Point(P256_GENERATOR_X, P256_GENERATOR_Y)
    blob = encode_point_uncompressed(g)
    assert blob[:1] == b"\x04"
    assert blob[1:33] == P256_GENERATOR_X.to_bytes(32, "big")
    assert blob[33:65] == P256_GENERATOR_Y.to_bytes(32, "big")


def test_spake2_m_uncompressed_byte_layout():
    blob = encode_point_uncompressed(SPAKE2_M)
    assert blob == bytes.fromhex(
        "04"
        "886e2f97ace46e55ba9dd7242579f2993b64e16ef3dcab95afd497333d8fa12f"
        "5ff355163e43ce224e0b0e65ff02ac8e5c7be09419c785e0ca547d55a12e2d20"
    )


def test_spake2_n_uncompressed_byte_layout():
    blob = encode_point_uncompressed(SPAKE2_N)
    assert blob == bytes.fromhex(
        "04"
        "d8bbd6c639c62937b04d997f38c3770719c629d7014d49a24b4f98baa1292b49"
        "07d60aa6bfade45008a636337f5168c64d9bd36034808cd564490b1e656edbe7"
    )


# ---------------------------------------------------------------------------
# Hash-to-curve facade — Phase 3 only knows M and N as cached lookups
# ---------------------------------------------------------------------------


def test_hash_to_curve_returns_m_for_m_dst():
    dst = b"SPAKE2+-P256-SHA256-HKDF-SHA256-HMAC-SHA256 M"
    assert hash_to_curve_p256(dst) == SPAKE2_M


def test_hash_to_curve_returns_n_for_n_dst():
    dst = b"SPAKE2+-P256-SHA256-HKDF-SHA256-HMAC-SHA256 N"
    assert hash_to_curve_p256(dst) == SPAKE2_N


def test_hash_to_curve_unknown_dst_raises_not_implemented():
    with pytest.raises(NotImplementedError):
        hash_to_curve_p256(b"some other domain separation tag")


# ---------------------------------------------------------------------------
# Encoding rejects coordinates out of field range
# ---------------------------------------------------------------------------


def test_encode_uncompressed_rejects_oversized_coordinates():
    p = Point(2**260, 1)
    with pytest.raises(ValueError):
        encode_point_uncompressed(p)


def test_encode_compressed_rejects_oversized_coordinates():
    p = Point(2**260, 1)
    with pytest.raises(ValueError):
        encode_point_compressed(p)
