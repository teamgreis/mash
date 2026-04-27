"""Controller-side Zone CA store and CSR signing for Phase 9 / Phase 10.

A Zone CA is the trust anchor of a MASH zone. The controller generates one
when it first creates the zone, and then uses it to sign every device's
operational certificate during commissioning (and renewal). This module
wraps the lower-level X.509 helpers from :mod:`mash_py.crypto.certs` in a
small typed container so callers don't juggle ``(cert, key, zone_name)``
tuples by hand.

Spec references:

- ``docs/security.md §2`` — certificate hierarchy.
- ``docs/security.md §2.2`` — Zone CA validity (20 years).
- ``docs/security.md §2.3`` — controller operational cert (sibling of
  device operational cert under the Zone CA, KeyUsage =
  DigitalSignature|KeyEncipherment, EKU = ClientAuth|ServerAuth, 1-year
  validity, generated alongside the Zone CA).
- ``docs/testing/behavior/commissioning-pase.md §4.3 / §4.4`` — operational
  cert subject, extensions, and validity per DEC-085.
- ``docs/decision-log.md`` DEC-060 — zone types (GRID=1, LOCAL=2, TEST=3).
- ``docs/decision-log.md`` DEC-085 — commissioning cert install envelope.

Public surface:

- :class:`ZoneType` — IntEnum mirroring DEC-060.
- :class:`ZoneCA` — cert + private key + zone metadata.
- :class:`ControllerOperationalCert` — controller's identity in a zone:
  Zone-CA-signed operational cert + private key.
- :func:`generate_zone_ca` — produces a fresh ``ZoneCA``.
- :func:`sign_device_csr` — derive device ID from a CSR public key, sign
  with the Zone CA, return the operational cert.
- :func:`generate_controller_cert` — self-issue the controller's
  operational cert from the Zone CA (Phase 10).
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


@dataclass(frozen=True, slots=True)
class ControllerOperationalCert:
    """Controller's zone-bound identity for operational mTLS.

    Generated by :func:`generate_controller_cert`. Mirrors the device-side
    operational cert: signed by the Zone CA, end-entity (CA=False), with
    ``KeyUsage = DigitalSignature|KeyEncipherment`` and
    ``ExtendedKeyUsage = ClientAuth|ServerAuth`` per ``security.md §2.3``.
    The private key MUST stay on the controller; only the certificate is
    presented during the TLS handshake.

    The cert's ``AuthorityKeyIdentifier`` matches the Zone CA's
    ``SubjectKeyIdentifier`` (RFC 5280 method 1, SHA-1 of the CA pubkey),
    which is how mash-go's device side routes a reconnecting controller
    to the right zone (``device_service_zone.go:matchZoneByPeerCert``).
    """

    certificate: x509.Certificate
    private_key: EllipticCurvePrivateKey
    zone_ca_cert: x509.Certificate
    zone_id: str
    zone_type: ZoneType
    controller_id: str


def generate_controller_cert(
    zone_ca: ZoneCA,
    *,
    controller_id: str,
    validity: _dt.timedelta = OPERATIONAL_CERT_VALIDITY,
    now: _dt.datetime | None = None,
) -> ControllerOperationalCert:
    """Self-issue the controller's operational cert from the Zone CA.

    Per ``docs/security.md §2.3`` the controller cert is "self-issued by
    controller" — the same controller that owns the Zone CA mints its own
    end-entity cert without going through a CSR exchange (devices do
    use a CSR — see :func:`sign_device_csr`). The cert is a sibling of
    the device operational cert under the same Zone CA.

    The Subject CN is the caller-supplied ``controller_id``. The spec
    does not pin the controller's identity scheme, so we accept any
    string and treat it as opaque (mash-go does the same). The
    ``Organization`` field is set to the zone name to mirror
    :func:`sign_device_csr`'s subject DN; ``OrganizationalUnit`` is
    ``"MASH Controller"`` (the spec-aligned analogue of the device
    side's ``"MASH Device"``).

    DNS SANs are intentionally **not** included. The mash-go reference
    sets ``DNSNames=[controllerID]`` with a comment claiming Go TLS
    requires it; in practice Go's ``RequireAndVerifyClientCert`` mTLS
    validates the chain and not the hostname, and the spec is silent on
    DNS SANs. Omitting them keeps the cert spec-clean and keeps the
    Python implementation portable.
    """
    if not controller_id:
        raise ValueError("controller_id must be a non-empty string")

    # Self-issue: the controller already owns the Zone CA private key, so
    # there is no CSR exchange — we mint the leaf cert directly. This
    # mirrors mash-go's ``GenerateControllerOperationalCert`` and
    # security.md §2.3 ("self-issued by controller"). The subject DN uses
    # ``OU = "MASH Controller"`` to distinguish controller leaves from
    # device leaves (which use ``OU = "MASH Device"``).
    private_key = ec.generate_private_key(ec.SECP256R1())
    cert = _sign_with_subject(
        zone_ca,
        public_key=private_key.public_key(),
        common_name=controller_id,
        org_name=zone_ca.zone_name,
        ou_name="MASH Controller",
        validity=validity,
        now=now,
    )
    return ControllerOperationalCert(
        certificate=cert,
        private_key=private_key,
        zone_ca_cert=zone_ca.certificate,
        zone_id=zone_ca.zone_name,
        zone_type=zone_ca.zone_type,
        controller_id=controller_id,
    )


def _sign_with_subject(
    zone_ca: ZoneCA,
    *,
    public_key: EllipticCurvePublicKey,
    common_name: str,
    org_name: str,
    ou_name: str,
    validity: _dt.timedelta,
    now: _dt.datetime | None,
) -> x509.Certificate:
    """Sign a fresh end-entity cert with a custom subject DN.

    Mirrors the cert template in :func:`mash_py.crypto.certs.sign_csr`
    (BasicConstraints/KeyUsage/EKU/SKI/AKI, validity, serial) but lets the
    caller pick the OU. Used by :func:`generate_controller_cert` to set
    ``OU = "MASH Controller"`` per ``security.md §2.3`` rather than
    ``sign_csr``'s hardcoded ``OU = "MASH Device"``.
    """
    from cryptography import x509 as _x509
    from cryptography.hazmat.primitives import hashes as _hashes
    from cryptography.x509.oid import (
        ExtendedKeyUsageOID as _EKU,
    )
    from cryptography.x509.oid import (
        NameOID as _NameOID,
    )

    from mash_py.crypto.certs import (
        CLOCK_SKEW_ALLOWANCE as _SKEW,
    )
    from mash_py.crypto.certs import (
        compute_ski as _compute_ski,
    )
    from mash_py.crypto.certs import (
        subject_key_id as _ca_ski,
    )

    if now is None:
        now = _dt.datetime.now(_dt.UTC)

    ca_ski = _ca_ski(zone_ca.certificate)
    if ca_ski is None:
        raise ValueError("Zone CA cert has no SubjectKeyIdentifier extension")

    serial = int.from_bytes(_random_serial_bytes(), "big") | 1

    builder = (
        _x509.CertificateBuilder()
        .subject_name(
            _x509.Name(
                [
                    _x509.NameAttribute(_NameOID.COMMON_NAME, common_name),
                    _x509.NameAttribute(_NameOID.ORGANIZATION_NAME, org_name),
                    _x509.NameAttribute(_NameOID.ORGANIZATIONAL_UNIT_NAME, ou_name),
                ]
            )
        )
        .issuer_name(zone_ca.certificate.subject)
        .public_key(public_key)
        .serial_number(serial)
        .not_valid_before(now - _SKEW)
        .not_valid_after(now + validity)
        .add_extension(
            _x509.BasicConstraints(ca=False, path_length=None), critical=True
        )
        .add_extension(
            _x509.KeyUsage(
                digital_signature=True,
                content_commitment=False,
                key_encipherment=True,
                data_encipherment=False,
                key_agreement=False,
                key_cert_sign=False,
                crl_sign=False,
                encipher_only=False,
                decipher_only=False,
            ),
            critical=True,
        )
        .add_extension(
            _x509.ExtendedKeyUsage([_EKU.SERVER_AUTH, _EKU.CLIENT_AUTH]),
            critical=False,
        )
        .add_extension(
            _x509.SubjectKeyIdentifier(_compute_ski(public_key)), critical=False
        )
        .add_extension(
            _x509.AuthorityKeyIdentifier(
                key_identifier=ca_ski,
                authority_cert_issuer=None,
                authority_cert_serial_number=None,
            ),
            critical=False,
        )
    )
    return builder.sign(zone_ca.private_key, _hashes.SHA256())


def _random_serial_bytes() -> bytes:
    """Random 128-bit serial number bytes (commissioning-pase.md §4.4)."""
    import secrets as _secrets

    return _secrets.token_bytes(16)


__all__ = [
    "ControllerOperationalCert",
    "ZoneCA",
    "ZoneType",
    "generate_controller_cert",
    "generate_zone_ca",
    "sign_device_csr",
]
