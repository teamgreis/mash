"""Unit tests for the renewal state machines.

Drives :class:`ControllerRenewalDriver` and :class:`DeviceRenewalHandler`
end-to-end (controller ↔ device, both in-process), exercising the happy
path and every documented failure mode:

- Nonce binding: tampered NonceHash on the CSR aborts on the controller
  side (DEC-047).
- Public-key binding: a CertRenewalInstall whose cert.public_key does
  not match the device's pending key produces a InvalidNonce ack.
- Sequence monotonicity: each successful renewal increments the
  controller's sequence and the device's active_sequence.
- Renewal-in-progress flag: device tracks an in-flight pending CSR
  until install.

Spec sources: ``docs/security.md §11.3``, ``docs/stack-architecture.md
§11.2``, DEC-047, DEC-076.
"""

from __future__ import annotations

import pytest
from cryptography import x509
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.x509.oid import NameOID

from mash_py.commissioning.cert_store import (
    ZoneCA,
    ZoneType,
    generate_zone_ca,
    sign_device_csr,
)
from mash_py.commissioning.renewal import (
    ControllerRenewalDriver,
    DeviceRenewalHandler,
    NonceBindingError,
    RenewalFailedError,
    RenewalProtocolError,
)
from mash_py.commissioning.renewal_messages import (
    NONCE_HASH_SIZE,
    NONCE_SIZE,
    RENEWAL_STATUS_INSTALL_FAILED,
    RENEWAL_STATUS_INVALID_CERT,
    RENEWAL_STATUS_INVALID_NONCE,
    RENEWAL_STATUS_SUCCESS,
    CertRenewalAck,
    CertRenewalCSR,
    CertRenewalInstall,
    CertRenewalRequest,
    compute_nonce_hash,
)
from mash_py.crypto.certs import create_csr


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def zone_ca() -> ZoneCA:
    return generate_zone_ca("test-zone", ZoneType.LOCAL)


@pytest.fixture
def initial_device_cert(zone_ca: ZoneCA) -> tuple[x509.Certificate, ec.EllipticCurvePrivateKey]:
    """Mint a device operational cert as if commissioning had just completed."""
    key = ec.generate_private_key(ec.SECP256R1())
    csr = create_csr(key)
    cert, _device_id = sign_device_csr(zone_ca, csr)
    return cert, key


# ---------------------------------------------------------------------------
# Happy path — full controller ↔ device renewal
# ---------------------------------------------------------------------------


def test_full_renewal_flow_succeeds(
    zone_ca: ZoneCA,
    initial_device_cert: tuple[x509.Certificate, ec.EllipticCurvePrivateKey],
):
    initial_cert, initial_key = initial_device_cert
    device = DeviceRenewalHandler(
        active_cert=initial_cert, active_key=initial_key, active_sequence=1
    )
    driver = ControllerRenewalDriver(zone_ca, initial_sequence=1)

    # 1. Controller -> Device: CertRenewalRequest
    req = driver.build_request()
    assert isinstance(req, CertRenewalRequest)
    assert len(req.nonce) == NONCE_SIZE

    # 2. Device -> Controller: CertRenewalCSR (with NonceHash binding)
    csr_msg = device.handle_request(req)
    assert isinstance(csr_msg, CertRenewalCSR)
    assert len(csr_msg.nonce_hash) == NONCE_HASH_SIZE
    assert device.renewal_in_progress is True

    # 3. Controller -> Device: CertRenewalInstall
    install = driver.handle_csr(csr_msg)
    assert isinstance(install, CertRenewalInstall)
    assert install.sequence == 2  # bumped from initial 1

    # 4. Device -> Controller: CertRenewalAck
    ack = device.handle_install(install)
    assert ack.status == RENEWAL_STATUS_SUCCESS
    assert ack.active_sequence == 2
    assert device.renewal_in_progress is False

    # Controller validates ack and recovers the signed cert.
    cert = driver.handle_ack(ack)
    assert isinstance(cert, x509.Certificate)

    # Device's active state has been atomically updated to the new cert.
    assert device.active_cert is not None
    assert device.active_cert.serial_number == cert.serial_number
    assert device.active_cert.serial_number != initial_cert.serial_number
    assert device.active_sequence == 2


def test_renewal_preserves_device_id_via_csr_pubkey(
    zone_ca: ZoneCA,
    initial_device_cert: tuple[x509.Certificate, ec.EllipticCurvePrivateKey],
):
    """The new cert's CN is derived from the CSR public key — by design,
    each renewal generates a fresh key, so the device ID changes. Verify
    that mash-py exposes the new ID through ``driver.device_id``.
    """
    initial_cert, initial_key = initial_device_cert
    device = DeviceRenewalHandler(
        active_cert=initial_cert, active_key=initial_key, active_sequence=1
    )
    driver = ControllerRenewalDriver(zone_ca)

    req = driver.build_request()
    csr_msg = device.handle_request(req)
    install = driver.handle_csr(csr_msg)
    ack = device.handle_install(install)
    driver.handle_ack(ack)

    # The CN of the new cert should match driver.device_id and should
    # not equal the initial cert's CN (different keys).
    new_cn = driver.signed_cert.subject.get_attributes_for_oid(NameOID.COMMON_NAME)[0].value
    initial_cn = initial_cert.subject.get_attributes_for_oid(NameOID.COMMON_NAME)[0].value
    assert new_cn == driver.device_id
    assert new_cn != initial_cn


def test_sequence_increments_across_consecutive_renewals(
    zone_ca: ZoneCA,
    initial_device_cert: tuple[x509.Certificate, ec.EllipticCurvePrivateKey],
):
    initial_cert, initial_key = initial_device_cert
    device = DeviceRenewalHandler(
        active_cert=initial_cert, active_key=initial_key, active_sequence=5
    )

    sequences: list[int] = []
    seq_in = 5
    for _ in range(3):
        driver = ControllerRenewalDriver(zone_ca, initial_sequence=seq_in)
        req = driver.build_request()
        csr_msg = device.handle_request(req)
        install = driver.handle_csr(csr_msg)
        ack = device.handle_install(install)
        driver.handle_ack(ack)
        sequences.append(ack.active_sequence)
        seq_in = driver.sequence

    assert sequences == [6, 7, 8]
    assert device.active_sequence == 8


# ---------------------------------------------------------------------------
# Controller-side failures
# ---------------------------------------------------------------------------


def test_controller_aborts_on_tampered_nonce_hash(zone_ca: ZoneCA):
    device = DeviceRenewalHandler()
    driver = ControllerRenewalDriver(zone_ca)
    req = driver.build_request()
    csr_msg = device.handle_request(req)

    # Flip a bit in the nonce_hash to simulate tampering.
    bad_hash = bytearray(csr_msg.nonce_hash)
    bad_hash[0] ^= 0xFF
    tampered = CertRenewalCSR(csr=csr_msg.csr, nonce_hash=bytes(bad_hash))

    with pytest.raises(NonceBindingError):
        driver.handle_csr(tampered)


def test_controller_rejects_unparseable_csr(zone_ca: ZoneCA):
    driver = ControllerRenewalDriver(zone_ca)
    driver.build_request()

    bad = CertRenewalCSR(
        csr=b"\x00\x01\x02\x03",  # not a valid DER PKCS#10
        nonce_hash=compute_nonce_hash(driver.nonce),
    )
    with pytest.raises(RenewalProtocolError):
        driver.handle_csr(bad)


def test_handle_csr_before_build_request_is_protocol_error(zone_ca: ZoneCA):
    device = DeviceRenewalHandler()
    driver = ControllerRenewalDriver(zone_ca)
    csr_msg = device.handle_request(CertRenewalRequest(nonce=b"\x00" * NONCE_SIZE))
    with pytest.raises(RenewalProtocolError):
        driver.handle_csr(csr_msg)


def test_handle_ack_before_handle_csr_is_protocol_error(zone_ca: ZoneCA):
    driver = ControllerRenewalDriver(zone_ca)
    driver.build_request()
    with pytest.raises(RenewalProtocolError):
        driver.handle_ack(CertRenewalAck(status=0, active_sequence=1))


def test_controller_raises_on_failure_status(zone_ca: ZoneCA):
    device = DeviceRenewalHandler()
    driver = ControllerRenewalDriver(zone_ca)
    req = driver.build_request()
    csr_msg = device.handle_request(req)
    driver.handle_csr(csr_msg)

    fail = CertRenewalAck(
        status=RENEWAL_STATUS_INVALID_CERT, active_sequence=0
    )
    with pytest.raises(RenewalFailedError) as info:
        driver.handle_ack(fail)
    assert info.value.status == RENEWAL_STATUS_INVALID_CERT


def test_controller_raises_on_sequence_mismatch(zone_ca: ZoneCA):
    device = DeviceRenewalHandler()
    driver = ControllerRenewalDriver(zone_ca)
    req = driver.build_request()
    csr_msg = device.handle_request(req)
    install = driver.handle_csr(csr_msg)

    bad_ack = CertRenewalAck(
        status=RENEWAL_STATUS_SUCCESS,
        active_sequence=install.sequence + 5,  # device echoed wrong sequence
    )
    with pytest.raises(RenewalProtocolError):
        driver.handle_ack(bad_ack)


# ---------------------------------------------------------------------------
# Device-side failures (always emit a wire-shaped CertRenewalAck)
# ---------------------------------------------------------------------------


def test_device_handle_install_without_pending_returns_install_failed():
    device = DeviceRenewalHandler()
    ack = device.handle_install(
        CertRenewalInstall(new_cert=b"\x30\x82", sequence=2)
    )
    assert ack.status == RENEWAL_STATUS_INSTALL_FAILED


def test_device_returns_invalid_cert_on_unparseable_install():
    device = DeviceRenewalHandler()
    device.handle_request(CertRenewalRequest(nonce=b"\x00" * NONCE_SIZE))
    ack = device.handle_install(
        CertRenewalInstall(new_cert=b"\xff\x00\x01", sequence=2)
    )
    assert ack.status == RENEWAL_STATUS_INVALID_CERT


def test_device_returns_invalid_nonce_when_cert_pubkey_mismatches(
    zone_ca: ZoneCA,
):
    """Controller signed a CSR whose public key does not match the
    device's pending key — DEC-047 maps that to InvalidNonce."""
    device = DeviceRenewalHandler()
    device.handle_request(CertRenewalRequest(nonce=b"\x00" * NONCE_SIZE))

    # Sign a cert for a different (random) key pair — the device's
    # pending key is something else entirely.
    foreign_key = ec.generate_private_key(ec.SECP256R1())
    foreign_csr = create_csr(foreign_key)
    foreign_cert, _ = sign_device_csr(zone_ca, foreign_csr)
    foreign_der = foreign_cert.public_bytes(serialization.Encoding.DER)

    ack = device.handle_install(
        CertRenewalInstall(new_cert=foreign_der, sequence=2)
    )
    assert ack.status == RENEWAL_STATUS_INVALID_NONCE
    # Pending state cleared? No — only on success per spec — let's check.
    # mash-go clears pending state ONLY on success; mash-py matches that.
    assert device.renewal_in_progress is True


def test_device_request_rejects_short_nonce():
    device = DeviceRenewalHandler()
    with pytest.raises(RenewalProtocolError):
        device.handle_request(CertRenewalRequest(nonce=b"\x00" * 16))


def test_device_renewal_replaces_prior_pending_on_new_request():
    """A second CertRenewalRequest before Install replaces the pending key.
    The first session is effectively abandoned."""
    device = DeviceRenewalHandler()
    csr_a = device.handle_request(CertRenewalRequest(nonce=b"\x00" * NONCE_SIZE))
    csr_b = device.handle_request(CertRenewalRequest(nonce=b"\x01" * NONCE_SIZE))
    # CSR DERs differ because the underlying key pairs are different.
    assert csr_a.csr != csr_b.csr
    assert device.renewal_in_progress is True


def test_set_active_cert_clears_pending(
    zone_ca: ZoneCA,
    initial_device_cert: tuple[x509.Certificate, ec.EllipticCurvePrivateKey],
):
    initial_cert, initial_key = initial_device_cert
    device = DeviceRenewalHandler()
    device.handle_request(CertRenewalRequest(nonce=b"\x00" * NONCE_SIZE))
    assert device.renewal_in_progress is True
    device.set_active_cert(initial_cert, initial_key, 1)
    assert device.renewal_in_progress is False
    assert device.active_cert is initial_cert
