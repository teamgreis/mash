"""Shared fixtures for the transport unit tests.

These fixtures generate the cert material the TLS contexts need, using the
mash-py crypto helpers (which are themselves spec-driven). All certs and
keys live entirely in memory; no temp files leak between tests.
"""

from __future__ import annotations

from dataclasses import dataclass

import pytest
from cryptography import x509
from cryptography.hazmat.primitives.asymmetric.ec import EllipticCurvePrivateKey

from mash_py.crypto.certs import (
    CSRInfo,
    create_csr,
    generate_commissioning_cert,
    generate_ecdsa_p256_keypair,
    generate_zone_ca,
    sign_csr,
)


@dataclass
class CommissioningCertBundle:
    """Self-signed device commissioning cert + its private key."""

    cert: x509.Certificate
    key: EllipticCurvePrivateKey


@dataclass
class OperationalCertBundle:
    """Zone CA + a Zone-CA-signed operational cert + its private key."""

    zone_ca: x509.Certificate
    zone_ca_key: EllipticCurvePrivateKey
    cert: x509.Certificate
    key: EllipticCurvePrivateKey


@pytest.fixture
def commissioning_bundle() -> CommissioningCertBundle:
    key = generate_ecdsa_p256_keypair()
    cert = generate_commissioning_cert(key, discriminator="1234")
    return CommissioningCertBundle(cert=cert, key=key)


@pytest.fixture
def zone_ca_bundle() -> tuple[x509.Certificate, EllipticCurvePrivateKey]:
    """The zone CA — used to sign both the device and controller op certs."""
    ca_key = generate_ecdsa_p256_keypair()
    ca_cert = generate_zone_ca(ca_key, zone_name="test-zone")
    return ca_cert, ca_key


@pytest.fixture
def device_op_bundle(
    zone_ca_bundle: tuple[x509.Certificate, EllipticCurvePrivateKey],
) -> OperationalCertBundle:
    ca_cert, ca_key = zone_ca_bundle
    key = generate_ecdsa_p256_keypair()
    csr = create_csr(key, info=CSRInfo(common_name="device-csr"))
    cert = sign_csr(ca_cert, ca_key, csr, zone_name="test-zone")
    return OperationalCertBundle(
        zone_ca=ca_cert, zone_ca_key=ca_key, cert=cert, key=key
    )


@pytest.fixture
def controller_op_bundle(
    zone_ca_bundle: tuple[x509.Certificate, EllipticCurvePrivateKey],
) -> OperationalCertBundle:
    """A separate operational keypair signed by the same Zone CA — i.e. the
    controller's identity in the same zone as the device."""
    ca_cert, ca_key = zone_ca_bundle
    key = generate_ecdsa_p256_keypair()
    csr = create_csr(key, info=CSRInfo(common_name="controller-csr"))
    cert = sign_csr(ca_cert, ca_key, csr, zone_name="test-zone")
    return OperationalCertBundle(
        zone_ca=ca_cert, zone_ca_key=ca_key, cert=cert, key=key
    )
