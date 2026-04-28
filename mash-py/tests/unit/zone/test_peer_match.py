"""Unit tests for ``mash_py.zone.peer_match``.

Verify the post-handshake zone-by-key-id matcher works against
realistic CA + signed-cert pairs (built via the same crypto helpers
the production code uses) and degrades cleanly when the cert lacks
the relevant extensions.

Spec: ``security.md §5.1`` — match by issuer; we use AuthorityKeyId
(matches mash-go's mechanism — see ``device_service_zone.go::matchZoneByPeerCert``).
"""

from __future__ import annotations

import datetime as dt

import pytest
from cryptography import x509
from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.asymmetric import ec

from mash_py.crypto.certs import (
    CSRInfo,
    create_csr,
    generate_ecdsa_p256_keypair,
    generate_zone_ca,
    sign_csr,
)
from mash_py.zone.peer_match import (
    match_zone_by_peer_cert,
    peer_authority_key_id,
    zone_ca_subject_key_id,
)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_zone(zone_name: str) -> tuple[x509.Certificate, x509.Certificate]:
    """Build (zone_ca_cert, op_cert) for a fresh zone."""
    ca_key = generate_ecdsa_p256_keypair()
    ca_cert = generate_zone_ca(ca_key, zone_name=zone_name)
    op_key = generate_ecdsa_p256_keypair()
    csr = create_csr(op_key, info=CSRInfo(common_name="placeholder"))
    op_cert = sign_csr(ca_cert, ca_key, csr, zone_name=zone_name)
    return ca_cert, op_cert


# ---------------------------------------------------------------------------
# Extension extractors
# ---------------------------------------------------------------------------


def test_zone_ca_subject_key_id_present() -> None:
    ca_cert, _ = _make_zone("z1")
    ski = zone_ca_subject_key_id(ca_cert)
    assert ski is not None
    assert isinstance(ski, bytes)
    assert len(ski) > 0


def test_peer_authority_key_id_matches_zone_ca() -> None:
    """The AuthorityKeyId on a CA-signed cert MUST equal the
    SubjectKeyId of the issuing CA — that's the whole point."""
    ca_cert, op_cert = _make_zone("z1")
    aki = peer_authority_key_id(op_cert)
    ski = zone_ca_subject_key_id(ca_cert)
    assert aki is not None
    assert ski is not None
    assert aki == ski


def test_peer_authority_key_id_returns_none_when_extension_missing() -> None:
    """A cert without AuthorityKeyIdentifier returns None — the
    matcher then falls through to "no match" rather than crashing."""
    key = generate_ecdsa_p256_keypair()
    builder = (
        x509.CertificateBuilder()
        .subject_name(
            x509.Name(
                [x509.NameAttribute(x509.oid.NameOID.COMMON_NAME, "no-aki")]
            )
        )
        .issuer_name(
            x509.Name(
                [x509.NameAttribute(x509.oid.NameOID.COMMON_NAME, "no-aki")]
            )
        )
        .public_key(key.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(dt.datetime.now(dt.timezone.utc))
        .not_valid_after(
            dt.datetime.now(dt.timezone.utc) + dt.timedelta(days=1)
        )
    )
    cert = builder.sign(private_key=key, algorithm=hashes.SHA256())
    assert peer_authority_key_id(cert) is None


# ---------------------------------------------------------------------------
# Zone matching
# ---------------------------------------------------------------------------


def test_match_picks_correct_zone() -> None:
    """Three zones, peer cert from the second one — matcher must
    return that zone's id."""
    ca1, _ = _make_zone("zone-1")
    ca2, op2 = _make_zone("zone-2")
    ca3, _ = _make_zone("zone-3")

    zone_cas = {"z1": ca1, "z2": ca2, "z3": ca3}
    assert match_zone_by_peer_cert(op2, zone_cas) == "z2"


def test_match_returns_none_when_no_ca_signed_peer() -> None:
    """A peer cert from a Zone CA that's not in the pool returns
    None (the device should reject the connection at the application
    layer)."""
    ca1, _ = _make_zone("zone-1")
    _, op_other = _make_zone("zone-other")
    zone_cas = {"z1": ca1}
    assert match_zone_by_peer_cert(op_other, zone_cas) is None


def test_match_returns_none_for_empty_zone_cas() -> None:
    _, op = _make_zone("zone-1")
    assert match_zone_by_peer_cert(op, {}) is None


def test_match_returns_none_when_peer_lacks_aki() -> None:
    """If the peer has no AKI extension, no match is possible."""
    key = generate_ecdsa_p256_keypair()
    builder = (
        x509.CertificateBuilder()
        .subject_name(
            x509.Name(
                [x509.NameAttribute(x509.oid.NameOID.COMMON_NAME, "no-aki")]
            )
        )
        .issuer_name(
            x509.Name(
                [x509.NameAttribute(x509.oid.NameOID.COMMON_NAME, "no-aki")]
            )
        )
        .public_key(key.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(dt.datetime.now(dt.timezone.utc))
        .not_valid_after(
            dt.datetime.now(dt.timezone.utc) + dt.timedelta(days=1)
        )
    )
    no_aki_cert = builder.sign(private_key=key, algorithm=hashes.SHA256())

    ca1, _ = _make_zone("zone-1")
    assert match_zone_by_peer_cert(no_aki_cert, {"z1": ca1}) is None
