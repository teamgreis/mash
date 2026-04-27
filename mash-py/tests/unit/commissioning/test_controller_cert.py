"""Phase 10 unit tests for ``generate_controller_cert``.

The Phase 10 acceptance gate (``mash-py-tdd-plan.md``) lives at the
interop layer — we connect to a live Go device with mTLS and Read
DeviceInfo. These unit tests pin the *cert shape* the spec demands so
the interop test only fails for transport/handshake reasons, not because
of a misshapen leaf cert.

Spec source: ``docs/security.md §2.3``::

    Issuer:    Zone CA (self-issued by controller)
    Validity:  1 year (auto-renewed)
    KeyUsage:  DigitalSignature, KeyEncipherment
    ExtKeyUsage: ClientAuth, ServerAuth

The controller cert MUST also chain to its Zone CA (verified by every
mash-go device in ``device_service_lifecycle.go:verifyClientCert``).
"""

from __future__ import annotations

import datetime as dt

import pytest
from cryptography import x509
from cryptography.x509.oid import ExtendedKeyUsageOID, ExtensionOID, NameOID

from mash_py.commissioning.cert_store import (
    ControllerOperationalCert,
    ZoneType,
    generate_controller_cert,
    generate_zone_ca,
)
from mash_py.crypto.certs import (
    OPERATIONAL_CERT_VALIDITY,
    authority_key_id,
    subject_key_id,
    verify_cert_chain,
)


@pytest.fixture
def zone_ca():
    return generate_zone_ca("phase10-zone", ZoneType.LOCAL)


class TestSubjectAndIdentity:
    def test_returns_controller_operational_cert(self, zone_ca):
        cc = generate_controller_cert(zone_ca, controller_id="Controller-01")
        assert isinstance(cc, ControllerOperationalCert)
        assert cc.controller_id == "Controller-01"
        assert cc.zone_id == "phase10-zone"
        assert cc.zone_type == ZoneType.LOCAL
        assert cc.zone_ca_cert is zone_ca.certificate

    def test_subject_dn_per_security_md(self, zone_ca):
        # security.md §2.3 says "self-issued by controller" but does not
        # pin the subject DN; we mirror sign_csr's pattern using OU =
        # "MASH Controller" (the device side uses "MASH Device").
        cc = generate_controller_cert(zone_ca, controller_id="My-EMS")
        cn = cc.certificate.subject.get_attributes_for_oid(NameOID.COMMON_NAME)[0].value
        org = cc.certificate.subject.get_attributes_for_oid(
            NameOID.ORGANIZATION_NAME
        )[0].value
        ou = cc.certificate.subject.get_attributes_for_oid(
            NameOID.ORGANIZATIONAL_UNIT_NAME
        )[0].value
        assert cn == "My-EMS"
        assert org == "phase10-zone"
        assert ou == "MASH Controller"

    def test_rejects_empty_controller_id(self, zone_ca):
        with pytest.raises(ValueError, match="controller_id"):
            generate_controller_cert(zone_ca, controller_id="")


class TestExtensions:
    """Spec: KU = digital_signature|key_encipherment, EKU = client+server."""

    def test_basic_constraints_end_entity(self, zone_ca):
        cc = generate_controller_cert(zone_ca, controller_id="C1")
        bc = cc.certificate.extensions.get_extension_for_oid(
            ExtensionOID.BASIC_CONSTRAINTS
        )
        assert isinstance(bc.value, x509.BasicConstraints)
        assert bc.critical is True
        # mash-go's verifyPeerCertificate explicitly rejects CA=True peer
        # certs with the message "must be end-entity"
        # (device_service_lifecycle.go:262).
        assert bc.value.ca is False

    def test_key_usage_digital_signature_and_key_encipherment(self, zone_ca):
        cc = generate_controller_cert(zone_ca, controller_id="C1")
        ku_ext = cc.certificate.extensions.get_extension_for_oid(
            ExtensionOID.KEY_USAGE
        )
        ku = ku_ext.value
        assert ku_ext.critical is True
        assert ku.digital_signature is True
        assert ku.key_encipherment is True
        # All other KU bits MUST be off.
        assert ku.content_commitment is False
        assert ku.data_encipherment is False
        assert ku.key_agreement is False
        assert ku.key_cert_sign is False
        assert ku.crl_sign is False

    def test_extended_key_usage_client_and_server_auth(self, zone_ca):
        cc = generate_controller_cert(zone_ca, controller_id="C1")
        eku = cc.certificate.extensions.get_extension_for_oid(
            ExtensionOID.EXTENDED_KEY_USAGE
        ).value
        assert ExtendedKeyUsageOID.CLIENT_AUTH in eku
        assert ExtendedKeyUsageOID.SERVER_AUTH in eku

    def test_authority_key_id_matches_zone_ca_subject_key_id(self, zone_ca):
        # mash-go's matchZoneByPeerCert uses the controller cert's AKI
        # to look up the zone -- if AKI != Zone CA SKI, the operational
        # connection is rejected with "no matching zones to reconnect".
        cc = generate_controller_cert(zone_ca, controller_id="C1")
        aki = authority_key_id(cc.certificate)
        ski = subject_key_id(zone_ca.certificate)
        assert aki is not None
        assert ski is not None
        assert aki == ski

    def test_no_dns_sans(self, zone_ca):
        # Spec is silent on DNS SANs. mash-go adds DNSNames=[controllerID]
        # for "Go TLS verification" but its mTLS ClientAuth path does not
        # actually require a SAN (RequireAndVerifyClientCert validates
        # the chain, not hostnames). Omitting SANs keeps mash-py's cert
        # spec-clean and portable.
        cc = generate_controller_cert(zone_ca, controller_id="C1")
        with pytest.raises(x509.ExtensionNotFound):
            cc.certificate.extensions.get_extension_for_oid(
                ExtensionOID.SUBJECT_ALTERNATIVE_NAME
            )


class TestValidityAndChain:
    def test_validity_one_year_per_spec(self, zone_ca):
        # security.md §3 lists 1 year; the global constant
        # OPERATIONAL_CERT_VALIDITY is the single source of truth.
        now = dt.datetime(2026, 4, 27, tzinfo=dt.UTC)
        cc = generate_controller_cert(
            zone_ca, controller_id="C1", now=now
        )
        # NotBefore is now - 5min clock-skew per commissioning-pase.md
        assert cc.certificate.not_valid_before_utc <= now
        # NotAfter is exactly now + OPERATIONAL_CERT_VALIDITY (no skew on
        # the upper bound).
        assert (
            cc.certificate.not_valid_after_utc
            == now + OPERATIONAL_CERT_VALIDITY
        )

    def test_chains_to_zone_ca(self, zone_ca):
        cc = generate_controller_cert(zone_ca, controller_id="C1")
        assert verify_cert_chain(cc.certificate, zone_ca.certificate)


class TestKeyMaterialIsolation:
    def test_each_call_produces_a_fresh_keypair(self, zone_ca):
        a = generate_controller_cert(zone_ca, controller_id="C1")
        b = generate_controller_cert(zone_ca, controller_id="C1")
        # Same controller_id, different key material.
        assert (
            a.private_key.public_key().public_numbers()
            != b.private_key.public_key().public_numbers()
        )
        assert a.certificate.serial_number != b.certificate.serial_number

    def test_zone_ca_private_key_is_not_in_returned_object(self, zone_ca):
        cc = generate_controller_cert(zone_ca, controller_id="C1")
        # The dataclass holds the controller's *own* private key, NOT the
        # CA's signing key. Spec ("Zone CA private key MUST stay on the
        # controller" — security.md §2.2) is not violated by this object.
        assert cc.private_key is not zone_ca.private_key
