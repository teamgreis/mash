"""X.509 certificate helpers for MASH.

The protocol spec is the contract. Specifically:

- ``docs/security.md §2`` — certificate hierarchy: Zone CA (20 yr, CA),
  controller operational cert (1 yr), device operational cert (1 yr).
  Both operational certs are siblings under the Zone CA.
- ``docs/security.md §4.6`` — commissioning certificate is self-signed
  P-256, CN=``MASH-<discriminator>``, 20-year validity, content not
  security-relevant (PASE/SPAKE2+ provides authentication during the
  commissioning phase).
- ``docs/testing/behavior/commissioning-pase.md`` §"Certificate generation
  algorithm" — exact subject DN, extensions, SKI algorithm, and clock-skew
  allowance for operational certs (lines 388-475).
- ``docs/decision-log.md`` DEC-077 (7-day renewal window).

Anywhere the spec is silent, we make a sensible default and call it out
in the docstring. Anywhere mash-go diverges from the spec, mash-py
implements the spec — see ``mash-py-spec-issues.md`` Issue #4 for the
divergence list.

Public surface:

- :func:`generate_ecdsa_p256_keypair`
- :func:`compute_ski` — RFC 5280 method 1 (SHA-1 of pubkey BIT STRING),
  per ``commissioning-pase.md:413-414``
- :func:`derive_device_id` — ``SHA256(SPKI_DER)[:8].hex().upper()``,
  per ``commissioning-pase.md:419-426``
- :func:`generate_commissioning_cert` — self-signed per ``security.md §4.6``
- :func:`generate_zone_ca` — 20-year CA per ``security.md §2.2``
- :func:`create_csr`
- :func:`sign_csr` — produces an operational cert per
  ``commissioning-pase.md`` §"Certificate generation algorithm"
- :func:`verify_cert_chain` — single-level signature + validity-window check
- :func:`subject_key_id`, :func:`authority_key_id` — extension accessors
"""

from __future__ import annotations

import datetime as _dt
import hashlib
import secrets
from dataclasses import dataclass

from cryptography import x509
from cryptography.exceptions import InvalidSignature
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.hazmat.primitives.asymmetric.ec import (
    EllipticCurvePrivateKey,
    EllipticCurvePublicKey,
)
from cryptography.x509.oid import (
    ExtendedKeyUsageOID as _ExtendedKeyUsageOID,
)
from cryptography.x509.oid import (
    ExtensionOID,
    NameOID,
)

# ---------------------------------------------------------------------------
# Constants — driven by the spec, not by the Go reference
# ---------------------------------------------------------------------------

ZONE_CA_VALIDITY = _dt.timedelta(days=20 * 365)
"""20 years per ``security.md §2.2``."""

OPERATIONAL_CERT_VALIDITY = _dt.timedelta(days=365)
"""1 year per ``security.md §3`` table."""

COMMISSIONING_CERT_VALIDITY = _dt.timedelta(days=20 * 365)
"""20 years per ``security.md §4.6`` ("arbitrarily long since not
security-critical")."""

CLOCK_SKEW_ALLOWANCE = _dt.timedelta(minutes=5)
"""``NotBefore = now() - 5 minutes`` per ``commissioning-pase.md:446``."""

DEVICE_ID_HEX_LENGTH = 16
"""``commissioning-pase.md:425`` — 16 uppercase hex chars (64 bits)."""

_SERIAL_NUMBER_BITS = 128
"""``commissioning-pase.md:391`` and :444`` — random 128-bit serial."""


# ---------------------------------------------------------------------------
# Key generation
# ---------------------------------------------------------------------------


def generate_ecdsa_p256_keypair() -> EllipticCurvePrivateKey:
    """Generate a fresh ECDSA P-256 private key.

    P-256 is the only curve MASH uses (``security.md §4.6``). The
    public half is reachable via ``.public_key()`` on the returned
    object — we don't wrap the pair in a dataclass.
    """
    return ec.generate_private_key(ec.SECP256R1())


# ---------------------------------------------------------------------------
# Subject Key Identifier — RFC 5280 method 1, per spec
# ---------------------------------------------------------------------------


def compute_ski(public_key: EllipticCurvePublicKey) -> bytes:
    """Compute the Subject Key Identifier per spec.

    ``commissioning-pase.md:413-414`` specifies "SHA-1 of public key" —
    this is RFC 5280 §4.2.1.2 method 1: SHA-1 of the BIT STRING component
    of ``SubjectPublicKeyInfo`` (the raw key bits, with no algorithm-id
    wrapper, no leading "unused bits" length byte).

    Returns 20 bytes. We use ``cryptography``'s built-in computation to
    match what the rest of the X.509 ecosystem produces by default.
    """
    return x509.SubjectKeyIdentifier.from_public_key(public_key).digest


# ---------------------------------------------------------------------------
# Device-ID derivation — per spec
# ---------------------------------------------------------------------------


def derive_device_id(public_key: EllipticCurvePublicKey) -> str:
    """Derive a device ID from a CSR public key per spec.

    Algorithm: ``SHA256(SubjectPublicKeyInfo_DER)[:8].hex().upper()``.

    ``commissioning-pase.md:419-426`` specifies this as a 16-character
    uppercase-hex string (64 bits of entropy). The controller computes
    this from the CSR public key during commissioning and embeds it as
    the operational certificate's CommonName.
    """
    spki_der = public_key.public_bytes(
        encoding=serialization.Encoding.DER,
        format=serialization.PublicFormat.SubjectPublicKeyInfo,
    )
    return hashlib.sha256(spki_der).digest()[:8].hex().upper()


# ---------------------------------------------------------------------------
# Commissioning cert — self-signed per security.md §4.6
# ---------------------------------------------------------------------------


def generate_commissioning_cert(
    private_key: EllipticCurvePrivateKey,
    discriminator: str,
    *,
    validity: _dt.timedelta = COMMISSIONING_CERT_VALIDITY,
    now: _dt.datetime | None = None,
) -> x509.Certificate:
    """Build a self-signed commissioning certificate per ``security.md §4.6``.

    Per the spec this certificate is **not** security-relevant — PASE
    (SPAKE2+) provides authentication during commissioning. We produce
    a structurally valid self-signed P-256 cert with the spec-mandated
    CommonName ``MASH-<discriminator>`` and a 20-year validity, and that
    is the entire contract.

    ``now`` is injectable for test determinism.
    """
    public_key = private_key.public_key()

    if now is None:
        now = _dt.datetime.now(_dt.UTC)

    cn = f"MASH-{discriminator}"
    subject = issuer = x509.Name(
        [x509.NameAttribute(NameOID.COMMON_NAME, cn)]
    )

    builder = (
        x509.CertificateBuilder()
        .subject_name(subject)
        .issuer_name(issuer)
        .public_key(public_key)
        .serial_number(_random_serial_number())
        .not_valid_before(now - CLOCK_SKEW_ALLOWANCE)
        .not_valid_after(now + validity)
        .add_extension(
            x509.BasicConstraints(ca=False, path_length=None),
            critical=True,
        )
        .add_extension(
            x509.SubjectKeyIdentifier.from_public_key(public_key),
            critical=False,
        )
    )

    return builder.sign(private_key, hashes.SHA256())


# ---------------------------------------------------------------------------
# Zone CA — security.md §2.2
# ---------------------------------------------------------------------------


def generate_zone_ca(
    private_key: EllipticCurvePrivateKey,
    zone_name: str,
    *,
    validity: _dt.timedelta = ZONE_CA_VALIDITY,
    now: _dt.datetime | None = None,
) -> x509.Certificate:
    """Build a self-signed Zone CA certificate.

    The spec (``security.md §2.2``) prescribes:

    - 20-year validity (matches device lifetime).
    - CA basic constraint, ``key_cert_sign`` + ``crl_sign`` key usage.
    - P-256 ECDSA-SHA256 signature.

    The spec does **not** prescribe a Subject DN format for the Zone CA;
    we use ``CN = "MASH Zone CA - <zone_name>"`` because the Zone CA's
    subject becomes the Issuer of every certificate it signs and a
    descriptive CN makes diagnostics readable. Implementations are free
    to choose differently as long as the subject is unique within the
    zone.
    """
    public_key = private_key.public_key()

    if now is None:
        now = _dt.datetime.now(_dt.UTC)

    cn = f"MASH Zone CA - {zone_name}"
    subject = issuer = x509.Name(
        [x509.NameAttribute(NameOID.COMMON_NAME, cn)]
    )

    builder = (
        x509.CertificateBuilder()
        .subject_name(subject)
        .issuer_name(issuer)
        .public_key(public_key)
        .serial_number(_random_serial_number())
        .not_valid_before(now - CLOCK_SKEW_ALLOWANCE)
        .not_valid_after(now + validity)
        .add_extension(
            x509.BasicConstraints(ca=True, path_length=0),
            critical=True,
        )
        .add_extension(
            x509.KeyUsage(
                digital_signature=True,
                content_commitment=False,
                key_encipherment=False,
                data_encipherment=False,
                key_agreement=False,
                key_cert_sign=True,
                crl_sign=True,
                encipher_only=False,
                decipher_only=False,
            ),
            critical=True,
        )
        .add_extension(
            x509.SubjectKeyIdentifier.from_public_key(public_key),
            critical=False,
        )
    )

    return builder.sign(private_key, hashes.SHA256())


# ---------------------------------------------------------------------------
# CSR
# ---------------------------------------------------------------------------


@dataclass(frozen=True, slots=True)
class CSRInfo:
    """Subject information for a CSR.

    The spec does not heavily constrain CSR contents — the *signer*
    composes the operational certificate's subject from the CSR public
    key (``CommonName`` is replaced with the derived device ID). Most
    callers only need to pass a placeholder CN.
    """

    common_name: str = "MASH Device CSR"


def create_csr(
    private_key: EllipticCurvePrivateKey,
    info: CSRInfo | None = None,
) -> x509.CertificateSigningRequest:
    """Build an ECDSA-SHA256 CSR.

    The CSR's subject CN is informational only. :func:`sign_csr`
    overwrites it with the derived (or controller-assigned) device ID.
    """
    if info is None:
        info = CSRInfo()
    builder = x509.CertificateSigningRequestBuilder().subject_name(
        x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, info.common_name)])
    )
    return builder.sign(private_key, hashes.SHA256())


# ---------------------------------------------------------------------------
# Operational cert generation — commissioning-pase.md
# ---------------------------------------------------------------------------


def sign_csr(
    ca_cert: x509.Certificate,
    ca_key: EllipticCurvePrivateKey,
    csr: x509.CertificateSigningRequest,
    *,
    zone_name: str | None = None,
    device_id: str | None = None,
    validity: _dt.timedelta = OPERATIONAL_CERT_VALIDITY,
    now: _dt.datetime | None = None,
) -> x509.Certificate:
    """Sign a CSR per ``commissioning-pase.md`` §"Certificate generation algorithm".

    Subject DN (lines 397-400):
        ``CN = <device-id>``  — derived from CSR public key by default,
                                or controller-assigned via ``device_id``
        ``O  = <zone-name>``  — optional (omitted if ``zone_name`` is None)
        ``OU = "MASH Device"``

    Extensions (lines 404-416):
        BasicConstraints(ca=False, critical=True)
        KeyUsage(digital_signature, key_encipherment, critical=True)
        ExtendedKeyUsage(serverAuth, clientAuth)
        SubjectKeyIdentifier = SHA-1(public_key)
        AuthorityKeyIdentifier.keyIdentifier = Zone CA SKI

    Validity (lines 446-447):
        NotBefore = now() - 5 minutes  (clock-skew allowance)
        NotAfter  = now() + 365 days

    Serial: random 128-bit (line 444).

    The CSR's signature is verified before signing.
    """
    if not _csr_signature_valid(csr):
        raise ValueError("CSR signature is invalid")

    public_key = csr.public_key()
    if not isinstance(public_key, EllipticCurvePublicKey):
        raise ValueError("CSR public key must be ECDSA P-256")

    if now is None:
        now = _dt.datetime.now(_dt.UTC)

    cn = device_id if device_id is not None else derive_device_id(public_key)

    subject_attrs: list[x509.NameAttribute[str]] = [
        x509.NameAttribute(NameOID.COMMON_NAME, cn),
    ]
    if zone_name is not None:
        subject_attrs.append(x509.NameAttribute(NameOID.ORGANIZATION_NAME, zone_name))
    subject_attrs.append(
        x509.NameAttribute(NameOID.ORGANIZATIONAL_UNIT_NAME, "MASH Device")
    )

    ca_ski = subject_key_id(ca_cert)
    if ca_ski is None:
        raise ValueError("CA certificate has no SubjectKeyIdentifier extension")

    builder = (
        x509.CertificateBuilder()
        .subject_name(x509.Name(subject_attrs))
        .issuer_name(ca_cert.subject)
        .public_key(public_key)
        .serial_number(_random_serial_number())
        .not_valid_before(now - CLOCK_SKEW_ALLOWANCE)
        .not_valid_after(now + validity)
        .add_extension(
            x509.BasicConstraints(ca=False, path_length=None),
            critical=True,
        )
        .add_extension(
            x509.KeyUsage(
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
            x509.ExtendedKeyUsage(
                [
                    _ExtendedKeyUsageOID.SERVER_AUTH,
                    _ExtendedKeyUsageOID.CLIENT_AUTH,
                ]
            ),
            critical=False,
        )
        .add_extension(
            x509.SubjectKeyIdentifier.from_public_key(public_key),
            critical=False,
        )
        .add_extension(
            x509.AuthorityKeyIdentifier(
                key_identifier=ca_ski,
                authority_cert_issuer=None,
                authority_cert_serial_number=None,
            ),
            critical=False,
        )
    )

    return builder.sign(ca_key, hashes.SHA256())


# ---------------------------------------------------------------------------
# Chain verification
# ---------------------------------------------------------------------------


def verify_cert_chain(
    leaf: x509.Certificate,
    ca: x509.Certificate,
    *,
    now: _dt.datetime | None = None,
) -> bool:
    """Single-level path validation.

    Returns ``True`` iff:

    - ``leaf.issuer`` matches ``ca.subject``;
    - when both are present, ``leaf``'s AKI matches ``ca``'s SKI;
    - ``leaf`` is currently within its validity window;
    - ``leaf``'s signature verifies against ``ca``'s public key under
      whatever hash algorithm the cert declares (in practice ECDSA-SHA256
      — the spec's mandated signature algorithm).

    Multi-level chains, name constraints, CRL/OCSP, and policy OIDs are
    out of scope. TLS handshake validation in Phases 4 / 10 enforces the
    full RFC 5280 path-validation logic via the underlying TLS library.
    """
    if leaf.issuer != ca.subject:
        return False

    leaf_aki = authority_key_id(leaf)
    ca_ski = subject_key_id(ca)
    if leaf_aki is not None and ca_ski is not None and leaf_aki != ca_ski:
        return False

    if now is None:
        now = _dt.datetime.now(_dt.UTC)
    if not (_not_valid_before(leaf) <= now <= _not_valid_after(leaf)):
        return False

    ca_pub = ca.public_key()
    if not isinstance(ca_pub, EllipticCurvePublicKey):
        return False

    leaf_hash = leaf.signature_hash_algorithm
    if leaf_hash is None:
        return False

    try:
        ca_pub.verify(
            leaf.signature,
            leaf.tbs_certificate_bytes,
            ec.ECDSA(leaf_hash),
        )
    except InvalidSignature:
        return False
    return True


# ---------------------------------------------------------------------------
# Extension accessors
# ---------------------------------------------------------------------------


def subject_key_id(cert: x509.Certificate) -> bytes | None:
    """Return the certificate's SubjectKeyIdentifier digest, or ``None``."""
    try:
        ext = cert.extensions.get_extension_for_oid(ExtensionOID.SUBJECT_KEY_IDENTIFIER)
    except x509.ExtensionNotFound:
        return None
    value = ext.value
    assert isinstance(value, x509.SubjectKeyIdentifier)
    return value.digest


def authority_key_id(cert: x509.Certificate) -> bytes | None:
    """Return the certificate's AuthorityKeyIdentifier key id, or ``None``."""
    try:
        ext = cert.extensions.get_extension_for_oid(ExtensionOID.AUTHORITY_KEY_IDENTIFIER)
    except x509.ExtensionNotFound:
        return None
    value = ext.value
    assert isinstance(value, x509.AuthorityKeyIdentifier)
    return value.key_identifier


# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------


def _random_serial_number() -> int:
    n = secrets.randbits(_SERIAL_NUMBER_BITS)
    if n == 0:
        return 1
    return n


def _csr_signature_valid(csr: x509.CertificateSigningRequest) -> bool:
    if hasattr(csr, "is_signature_valid"):
        return bool(csr.is_signature_valid)
    pub = csr.public_key()
    if not isinstance(pub, EllipticCurvePublicKey):
        return False
    csr_hash = csr.signature_hash_algorithm
    if csr_hash is None:
        return False
    try:
        pub.verify(
            csr.signature,
            csr.tbs_certrequest_bytes,
            ec.ECDSA(csr_hash),
        )
    except InvalidSignature:
        return False
    return True


def _aware(dt: _dt.datetime) -> _dt.datetime:
    if dt.tzinfo is None:
        return dt.replace(tzinfo=_dt.UTC)
    return dt


def _not_valid_before(cert: x509.Certificate) -> _dt.datetime:
    """Read ``not_valid_before`` preferring the tz-aware accessor on
    cryptography ≥42; fall back to the naive accessor for older versions."""
    if hasattr(cert, "not_valid_before_utc"):
        return cert.not_valid_before_utc
    return _aware(cert.not_valid_before)


def _not_valid_after(cert: x509.Certificate) -> _dt.datetime:
    if hasattr(cert, "not_valid_after_utc"):
        return cert.not_valid_after_utc
    return _aware(cert.not_valid_after)


__all__ = [
    "CLOCK_SKEW_ALLOWANCE",
    "COMMISSIONING_CERT_VALIDITY",
    "DEVICE_ID_HEX_LENGTH",
    "OPERATIONAL_CERT_VALIDITY",
    "ZONE_CA_VALIDITY",
    "CSRInfo",
    "authority_key_id",
    "compute_ski",
    "create_csr",
    "derive_device_id",
    "generate_commissioning_cert",
    "generate_ecdsa_p256_keypair",
    "generate_zone_ca",
    "sign_csr",
    "subject_key_id",
    "verify_cert_chain",
]
