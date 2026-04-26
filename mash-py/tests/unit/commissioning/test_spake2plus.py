"""Unit tests for ``mash_py.commissioning.spake2plus`` (DEC-084).

Round-trip: a Python controller and a Python device with the same
setup code + discriminator MUST converge on the same K_main, K_encrypt,
KcA, KcB and produce mutually-verifying confirmation MACs. Negative
cases exercise the §2.5 point-validation rules and the auth-failure
paths the eventual PASE driver will rely on.

These tests use small PBKDF2 iteration counts so the suite stays fast;
the end-to-end conformance vectors in
``tests/vectors/commissioning/`` exercise the protocol-constant
100,000-iteration path.
"""

from __future__ import annotations

import pytest

from mash_py.commissioning import (
    CONFIRM_KEY_SIZE,
    POINT_SIZE,
    SCALAR_SIZE,
    SHARED_SECRET_SIZE,
    ConfirmationFailedError,
    IdentityElementError,
    InvalidPointError,
    SPAKE2PlusClient,
    SPAKE2PlusError,
    SPAKE2PlusServer,
    derive_salt,
    derive_w0_w1,
    generate_verifier,
    pbkdf2_mhf,
    validate_point,
)
from mash_py.commissioning.mhf import MHF_DKLEN

# Use 1000 iterations for round-trip tests so a hundred handshakes
# don't take 5 minutes. Conformance vectors still test the production
# 100k count.
FAST_ITERS = 1000


# ---------------------------------------------------------------------------
# §2.3 — verifier derivation
# ---------------------------------------------------------------------------


def test_derive_salt_is_deterministic() -> None:
    s1 = derive_salt(3840, "20202021")
    s2 = derive_salt(3840, "20202021")
    assert s1 == s2
    assert len(s1) == 16


def test_derive_salt_unique_per_input() -> None:
    base = derive_salt(3840, "20202021")
    assert derive_salt(3841, "20202021") != base
    assert derive_salt(3840, "20202022") != base


@pytest.mark.parametrize("bad_disc", [-1, 4096, 1 << 16])
def test_derive_salt_rejects_oversized_discriminator(bad_disc: int) -> None:
    with pytest.raises(ValueError):
        derive_salt(bad_disc, "20202021")


@pytest.mark.parametrize("bad_code", ["1234567", "123456789", "abcdefgh", "1234abcd"])
def test_derive_salt_rejects_invalid_code(bad_code: str) -> None:
    with pytest.raises(ValueError):
        derive_salt(3840, bad_code)


def test_derive_w0_w1_rejects_wrong_length() -> None:
    with pytest.raises(ValueError):
        derive_w0_w1(b"\x00" * (MHF_DKLEN - 1))


def test_pbkdf2_mhf_roundtrip_known_inputs() -> None:
    """PBKDF2 is deterministic — repeated calls return the same bytes."""
    out1 = pbkdf2_mhf(b"20202021", b"\x00" * 16, iterations=100, dklen=80)
    out2 = pbkdf2_mhf(b"20202021", b"\x00" * 16, iterations=100, dklen=80)
    assert out1 == out2
    assert len(out1) == 80


def test_pbkdf2_mhf_rejects_bad_params() -> None:
    with pytest.raises(ValueError):
        pbkdf2_mhf(b"x", b"y", iterations=0)
    with pytest.raises(ValueError):
        pbkdf2_mhf(b"x", b"y", dklen=0)


def test_generate_verifier_produces_canonical_field_sizes() -> None:
    v = generate_verifier("20202021", 3840, iterations=FAST_ITERS)
    assert v.discriminator == 3840
    assert len(v.salt) == 16
    assert len(v.w0) == SCALAR_SIZE
    assert len(v.L) == POINT_SIZE
    assert v.iterations == FAST_ITERS


def test_generate_verifier_deterministic_for_same_inputs() -> None:
    v1 = generate_verifier("20202021", 3840, iterations=FAST_ITERS)
    v2 = generate_verifier("20202021", 3840, iterations=FAST_ITERS)
    assert v1 == v2


def test_generate_verifier_changes_with_setup_code() -> None:
    v1 = generate_verifier("20202021", 3840, iterations=FAST_ITERS)
    v2 = generate_verifier("99887766", 3840, iterations=FAST_ITERS)
    assert v1.salt != v2.salt
    assert v1.w0 != v2.w0
    assert v1.L != v2.L


# ---------------------------------------------------------------------------
# §2.5 — point validation
# ---------------------------------------------------------------------------


def test_validate_point_rejects_short_input() -> None:
    with pytest.raises(InvalidPointError):
        validate_point(b"\x04" + b"\x00" * 10)


def test_validate_point_rejects_compressed_prefix() -> None:
    """Compressed encoding (0x02 / 0x03) is not used in MASH."""
    payload = b"\x02" + (1).to_bytes(32, "big")
    with pytest.raises(InvalidPointError):
        validate_point(payload)


def test_validate_point_rejects_identity_element() -> None:
    """``0x04 || 0..0 || 0..0`` MUST be rejected (DEC-084 §2.5)."""
    with pytest.raises(IdentityElementError):
        validate_point(b"\x04" + b"\x00" * 64)


def test_validate_point_rejects_off_curve_point() -> None:
    # Take generator x but flip the last byte of y -> off-curve.
    Gx = 0x6B17D1F2E12C4247F8BCE6E563A440F277037D812DEB33A0F4A13945D898C296
    Gy = 0x4FE342E2FE1A7F9B8EE7EB4A7C0F9E162BCE33576B315ECECBB6406837BF51F5
    bad = b"\x04" + Gx.to_bytes(32, "big") + (Gy ^ 1).to_bytes(32, "big")
    with pytest.raises(InvalidPointError):
        validate_point(bad)


def test_validate_point_accepts_generator() -> None:
    Gx = 0x6B17D1F2E12C4247F8BCE6E563A440F277037D812DEB33A0F4A13945D898C296
    Gy = 0x4FE342E2FE1A7F9B8EE7EB4A7C0F9E162BCE33576B315ECECBB6406837BF51F5
    G_bytes = b"\x04" + Gx.to_bytes(32, "big") + Gy.to_bytes(32, "big")
    p = validate_point(G_bytes)
    assert p.x == Gx
    assert p.y == Gy


# ---------------------------------------------------------------------------
# Round-trip handshake (mutual K_main / K_encrypt / confirmation)
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    ("setup_code", "discriminator"),
    [
        ("20202021", 3840),
        ("00000001", 0),
        ("99999999", 4095),
        ("12345678", 2048),
    ],
)
def test_roundtrip_converges_on_same_keys(setup_code: str, discriminator: int) -> None:
    verifier = generate_verifier(setup_code, discriminator, iterations=FAST_ITERS)
    client = SPAKE2PlusClient(setup_code, discriminator, iterations=FAST_ITERS)
    server = SPAKE2PlusServer(verifier)

    pA = client.public_value()
    pB = server.public_value()
    assert len(pA) == POINT_SIZE
    assert len(pB) == POINT_SIZE

    server.process_client_value(pA)
    client.process_server_value(pB)

    # Both sides agree on every key in the chain.
    assert client.shared_secret == server.shared_secret
    assert client.encryption_key == server.encryption_key
    assert len(client.shared_secret) == SHARED_SECRET_SIZE
    assert len(client.encryption_key) == SHARED_SECRET_SIZE

    # Confirmation MACs round-trip and verify on the peer.
    confirmA = client.confirmation()
    confirmB = server.confirmation()
    assert len(confirmA) == 32
    assert len(confirmB) == 32
    server.verify_client_confirmation(confirmA)
    client.verify_server_confirmation(confirmB)


def test_wrong_setup_code_diverges() -> None:
    """A controller with the wrong code derives a different K_main than
    the device — and the confirmation MACs do not verify either way."""
    verifier = generate_verifier("20202021", 3840, iterations=FAST_ITERS)
    bad_client = SPAKE2PlusClient("00000000", 3840, iterations=FAST_ITERS)
    server = SPAKE2PlusServer(verifier)

    pA = bad_client.public_value()
    pB = server.public_value()
    server.process_client_value(pA)
    bad_client.process_server_value(pB)

    # Different w0 / w1 -> different transcript -> different K_main.
    assert bad_client.shared_secret != server.shared_secret

    # Confirmation MACs reject in both directions.
    with pytest.raises(ConfirmationFailedError):
        server.verify_client_confirmation(bad_client.confirmation())
    with pytest.raises(ConfirmationFailedError):
        bad_client.verify_server_confirmation(server.confirmation())


def test_client_rejects_identity_pB() -> None:
    """A device that returned the identity element as pB is rejected
    before any key derivation runs."""
    client = SPAKE2PlusClient("20202021", 3840, iterations=FAST_ITERS)
    client.public_value()
    with pytest.raises(IdentityElementError):
        client.process_server_value(b"\x04" + b"\x00" * 64)


def test_server_rejects_off_curve_pA() -> None:
    verifier = generate_verifier("20202021", 3840, iterations=FAST_ITERS)
    server = SPAKE2PlusServer(verifier)
    server.public_value()
    Gx = 0x6B17D1F2E12C4247F8BCE6E563A440F277037D812DEB33A0F4A13945D898C296
    Gy = 0x4FE342E2FE1A7F9B8EE7EB4A7C0F9E162BCE33576B315ECECBB6406837BF51F5
    off = b"\x04" + Gx.to_bytes(32, "big") + (Gy ^ 1).to_bytes(32, "big")
    with pytest.raises(InvalidPointError):
        server.process_client_value(off)


def test_confirmation_before_processing_raises() -> None:
    """``confirmation()`` requires that ``process_*_value`` ran first."""
    client = SPAKE2PlusClient("20202021", 3840, iterations=FAST_ITERS)
    with pytest.raises(SPAKE2PlusError):
        client.confirmation()


def test_shared_secret_before_processing_raises() -> None:
    verifier = generate_verifier("20202021", 3840, iterations=FAST_ITERS)
    server = SPAKE2PlusServer(verifier)
    with pytest.raises(SPAKE2PlusError):
        _ = server.shared_secret
    with pytest.raises(SPAKE2PlusError):
        _ = server.encryption_key


def test_client_uses_random_ephemeral_when_unset() -> None:
    """Two clients with the same setup code produce *different* pA when
    no ``x`` is injected — they each draw a fresh ephemeral."""
    a = SPAKE2PlusClient("20202021", 3840, iterations=FAST_ITERS)
    b = SPAKE2PlusClient("20202021", 3840, iterations=FAST_ITERS)
    assert a.public_value() != b.public_value()


def test_kca_kcb_split_sizes() -> None:
    verifier = generate_verifier("20202021", 3840, iterations=FAST_ITERS)
    client = SPAKE2PlusClient("20202021", 3840, iterations=FAST_ITERS)
    server = SPAKE2PlusServer(verifier)
    server.process_client_value(client.public_value())
    client.process_server_value(server.public_value())

    # KcA and KcB are private to the state machines; round-trip them
    # by exercising the verify path (which would fail if the split
    # were wrong) and check key-derivation lengths via the public
    # encryption key (which is sized like KcA||KcB combined).
    server.verify_client_confirmation(client.confirmation())
    client.verify_server_confirmation(server.confirmation())
    assert len(client.encryption_key) == SHARED_SECRET_SIZE
    # Sanity: SHARED_SECRET_SIZE = 2 * CONFIRM_KEY_SIZE in the spec.
    assert SHARED_SECRET_SIZE == 2 * CONFIRM_KEY_SIZE
