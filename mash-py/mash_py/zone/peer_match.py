"""Post-handshake zone identification by peer-certificate AuthorityKeyId.

Mirrors mash-go's ``pkg/service/device_service_zone.go::matchZoneByPeerCert``.
Once mTLS completes, the device knows which Zone CA signed the
controller's operational cert by comparing the peer cert's
``AuthorityKeyId`` to each known Zone CA's ``SubjectKeyId`` — both are
the SHA-1 (RFC 5280 default) hash of the issuer's public key, so a
match is byte-equal.

The spec (``security.md §5.1``) requires identifying the zone by
"matching the certificate's issuer to a known Zone CA". Comparing key
IDs is the canonical X.509 way to do that; comparing the full DN would
also work, but key-ID matching is shorter, denominator-free
(no string normalisation), and is what mash-go uses.

Why we don't reach into ``cryptography.x509``-level matching anywhere
else: the same operation also fires from the operational read-loop, so
keeping it as a small standalone helper means tests can drive it with
synthetic certs without spinning up a TLS listener.
"""

from __future__ import annotations

from collections.abc import Mapping

from cryptography import x509
from cryptography.x509 import AuthorityKeyIdentifier, SubjectKeyIdentifier
from cryptography.x509.oid import ExtensionOID


__all__ = [
    "match_zone_by_peer_cert",
    "peer_authority_key_id",
    "zone_ca_subject_key_id",
]


def peer_authority_key_id(peer_cert: x509.Certificate) -> bytes | None:
    """Extract the ``AuthorityKeyIdentifier.key_identifier`` bytes
    from ``peer_cert``. Returns ``None`` if the extension is absent or
    has no key_identifier (RFC 5280 allows authorityKeyIdentifier to
    use issuer+serial instead, though MASH-issued certs always use
    keyIdentifier).
    """
    try:
        ext = peer_cert.extensions.get_extension_for_oid(
            ExtensionOID.AUTHORITY_KEY_IDENTIFIER
        )
    except x509.ExtensionNotFound:
        return None
    aki: AuthorityKeyIdentifier = ext.value  # type: ignore[assignment]
    return aki.key_identifier  # bytes | None


def zone_ca_subject_key_id(ca_cert: x509.Certificate) -> bytes | None:
    """Extract the ``SubjectKeyIdentifier.digest`` bytes from a Zone
    CA cert. Returns ``None`` if the extension is absent (the MASH
    cert generators always set it, so a missing SKI usually means a
    test cert built by hand; callers should treat absence as "not
    matchable" rather than crashing).
    """
    try:
        ext = ca_cert.extensions.get_extension_for_oid(
            ExtensionOID.SUBJECT_KEY_IDENTIFIER
        )
    except x509.ExtensionNotFound:
        return None
    ski: SubjectKeyIdentifier = ext.value  # type: ignore[assignment]
    return ski.digest


def match_zone_by_peer_cert(
    peer_cert: x509.Certificate,
    zone_cas: Mapping[str, x509.Certificate],
) -> str | None:
    """Return the zone-id of the Zone CA that signed ``peer_cert``,
    or ``None`` if no match.

    Args:
        peer_cert: The controller's operational certificate, as
            extracted via ``ssl_socket.getpeercert(binary_form=True)``
            and parsed with :func:`x509.load_der_x509_certificate`.
        zone_cas: Mapping of ``zone_id -> Zone CA cert``. Iteration
            order determines tiebreak in the impossible case of two
            CAs sharing a SubjectKeyId — but RFC 5280 SKIs are derived
            from the public key, so collisions imply key reuse, which
            MASH commissioning already prevents.

    Returns:
        The matching ``zone_id`` or ``None``.
    """
    aki = peer_authority_key_id(peer_cert)
    if not aki:
        return None
    for zone_id, ca_cert in zone_cas.items():
        ski = zone_ca_subject_key_id(ca_cert)
        if ski is not None and ski == aki:
            return zone_id
    return None
