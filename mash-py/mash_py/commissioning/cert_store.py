"""Controller-side Zone CA store and CSR signing for Phase 9 (DEC-085).

A Zone CA is the trust anchor of a MASH zone. The controller generates one
when it first creates the zone, and then uses it to sign every device's
operational certificate during commissioning (and renewal). This module
wraps the lower-level X.509 helpers from :mod:`mash_py.crypto.certs` in a
small typed container so callers don't juggle ``(cert, key, zone_name)``
tuples by hand.

Spec references:

- ``docs/security.md §2`` — certificate hierarchy.
- ``docs/security.md §2.2`` — Zone CA validity (20 years).
- ``docs/testing/behavior/commissioning-pase.md §4.3 / §4.4`` — operational
  cert subject, extensions, and validity per DEC-085.
- ``docs/decision-log.md`` DEC-060 — zone types (GRID=1, LOCAL=2, TEST=3).
- ``docs/decision-log.md`` DEC-085 — commissioning cert install envelope.

Public surface:

- :class:`ZoneType` — IntEnum mirroring DEC-060.
- :class:`ZoneCA` — cert + private key + zone metadata.
- :func:`generate_zone_ca` — produces a fresh ``ZoneCA``.
- :func:`sign_device_csr` — derive device ID from a CSR public key, sign
  with the Zone CA, return the operational cert.
"""

from __future__ import annotations

import datetime as _dt
from dataclasses import dataclass
from enum import IntEnum

from cryptography import x509
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.hazmat.primitives.asymmetric.ec import (
    EllipticCurvePrivateKey,
    EllipticCurvePublicKey,
)

from mash_py.crypto.certs import (
    OPERATIONAL_CERT_VALIDITY,
    ZONE_CA_VALIDITY,
    derive_device_id,
)
from mash_py.crypto.certs import generate_zone_ca as _build_zone_ca_cert
from mash_py.crypto.certs import sign_csr as _sign_csr


class ZoneType(IntEnum):
    """Zone types per DEC-060.

    Wire-encoded as a single ``uint8`` in ``CertInstall.zoneType``. See
    ``docs/decision-log.md`` DEC-043 for the "max one zone per type"
    rule and DEC-060 for the TEST extension.
    """

    GRID = 1
    LOCAL = 2
    TEST = 3


@dataclass(frozen=True, slots=True)
class ZoneCA:
    """Trust anchor + signing key for a MASH zone.

    The controller holds this for the lifetime of the zone. It is used to:

    1. Sign device operational certs during commissioning (this module).
    2. Sign the controller's own operational cert (out-of-scope for Phase 9).
    3. Renew certs in-session on the operational ALPN (out-of-scope; Phase 19).

    The private key MUST stay on the controller — it is never sent on the
    wire. ``CertInstall (12)`` carries only the *public* CA cert.
    """

    certificate: x509.Certificate
    private_key: EllipticCurvePrivateKey
    zone_name: str
    zone_type: ZoneType

    @property
    def cert_der(self) -> bytes:
        """DER bytes of the Zone CA cert — the form sent on the wire in
        ``CertInstall.ca_cert``."""
        from cryptography.hazmat.primitives import serialization

        return self.certificate.public_bytes(serialization.Encoding.DER)


def generate_zone_ca(
    zone_name: str,
    zone_type: ZoneType = ZoneType.LOCAL,
    *,
    validity: _dt.timedelta = ZONE_CA_VALIDITY,
    now: _dt.datetime | None = None,
) -> ZoneCA:
    """Generate a fresh Zone CA (key pair + self-signed CA cert).

    The CA's CommonName is ``MASH Zone CA - <zone_name>`` per
    :func:`mash_py.crypto.certs.generate_zone_ca`. ``zone_type`` is stored
    on the returned object so the controller can echo it in
    ``CertInstall.zone_type`` without having to remember it elsewhere.
    """
    private_key = ec.generate_private_key(ec.SECP256R1())
    cert = _build_zone_ca_cert(
        private_key,
        zone_name=zone_name,
        validity=validity,
        now=now,
    )
    return ZoneCA(
        certificate=cert,
        private_key=private_key,
        zone_name=zone_name,
        zone_type=zone_type,
    )


def sign_device_csr(
    zone_ca: ZoneCA,
    csr: x509.CertificateSigningRequest,
    *,
    validity: _dt.timedelta = OPERATIONAL_CERT_VALIDITY,
    now: _dt.datetime | None = None,
) -> tuple[x509.Certificate, str]:
    """Sign a device CSR with the Zone CA, returning ``(cert, device_id)``.

    The device ID is derived from the CSR's public key per
    ``commissioning-pase.md §4.4`` (``SHA256(SPKI_DER)[:8].hex().upper()``)
    and embedded as the operational cert's CommonName. This is the
    Matter-style "commissioner assigns the Node ID" pattern.

    Validates that the CSR uses ECDSA P-256 before signing — non-P-256
    keys are rejected with :class:`ValueError`.
    """
    public_key = csr.public_key()
    if not isinstance(public_key, EllipticCurvePublicKey):
        raise ValueError("CSR public key must be ECDSA")
    if not isinstance(public_key.curve, ec.SECP256R1):
        raise ValueError(
            f"CSR public key must use P-256, got {public_key.curve.name}"
        )

    device_id = derive_device_id(public_key)
    cert = _sign_csr(
        zone_ca.certificate,
        zone_ca.private_key,
        csr,
        zone_name=zone_ca.zone_name,
        device_id=device_id,
        validity=validity,
        now=now,
    )
    return cert, device_id


__all__ = [
    "ZoneCA",
    "ZoneType",
    "generate_zone_ca",
    "sign_device_csr",
]
