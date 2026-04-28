"""Cert-renewal state machines — Phase 19.

Two single-direction state machines that drive the four-message renewal
exchange (MsgTypes 30→31→32→33) on the operational ALPN ``mash/1``:

- :class:`ControllerRenewalDriver` — controller-side. Builds a
  ``CertRenewalRequest`` (with a fresh 32-byte nonce), verifies the
  device's ``CertRenewalCSR`` (nonce-hash binding + CSR signature),
  signs the CSR with the Zone CA, ships ``CertRenewalInstall`` with a
  monotonic sequence number, and validates ``CertRenewalAck``.

- :class:`DeviceRenewalHandler` — device-side. Generates a fresh P-256
  key pair on ``CertRenewalRequest``, returns a CSR over the new key
  with ``SHA256(nonce)[:16]``, and on ``CertRenewalInstall`` validates
  that the new cert's public key matches the pending key before the
  atomic swap to active.

Both machines are message-level — they consume decoded
:mod:`mash_py.commissioning.renewal_messages` dataclasses and produce
decoded responses. The transport-bridge is a separate concern; see
:func:`run_controller_renewal` and :func:`dispatch_renewal_frame` for
thin async wrappers that hop frames through a
:class:`mash_py.transport.connection.Connection`.

Spec sources: ``docs/security.md §3.1`` (lifecycle), ``§11.3`` (nonce
binding + status table), ``docs/stack-architecture.md §11`` (sequence
diagram), ``docs/decision-log.md`` DEC-047 (nonce binding), DEC-076
(in-session — no TLS reconnection), DEC-077 (7-day window — policy,
not protocol).
"""

from __future__ import annotations

import secrets
import threading
from dataclasses import dataclass

from cryptography import x509
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.hazmat.primitives.asymmetric.ec import (
    EllipticCurvePrivateKey,
    EllipticCurvePublicKey,
)

from mash_py.commissioning.cert_store import ZoneCA, sign_device_csr
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
    renewal_status_string,
    validate_nonce_hash,
)
from mash_py.crypto.certs import create_csr


# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------


class RenewalError(Exception):
    """Base class for renewal state-machine failures."""


class RenewalProtocolError(RenewalError):
    """Peer sent a frame the spec does not allow in the current slot.

    Examples: ``CertRenewalAck`` arriving where ``CertRenewalCSR`` was
    expected; CSR with a malformed PKCS#10 body; nonce hash that doesn't
    bind to the request nonce; ack status != 0.
    """


class NonceBindingError(RenewalProtocolError):
    """Device's CertRenewalCSR carried a NonceHash that does not match the
    request's nonce. Per DEC-047 the controller MUST abort renewal.
    """


class RenewalFailedError(RenewalError):
    """Device returned a non-success status in CertRenewalAck."""

    def __init__(self, status: int, active_sequence: int) -> None:
        super().__init__(
            f"device rejected renewal: {renewal_status_string(status)} "
            f"(status={status}, active_sequence={active_sequence})"
        )
        self.status = status
        self.active_sequence = active_sequence


# ---------------------------------------------------------------------------
# Controller-side driver
# ---------------------------------------------------------------------------


class ControllerRenewalDriver:
    """Controller-side state machine for a single renewal.

    Single-use: one instance drives one CertRenewalRequest →
    CertRenewalAck round trip. Reusing requires a fresh instance with a
    fresh nonce. The Zone CA is supplied by the caller — this state
    machine does not own zone storage.

    Sequence numbers are monotonic per-device; the caller threads
    ``initial_sequence`` from its local cert-store. After a successful
    renewal, :attr:`active_sequence` exposes the issued value so the
    caller can persist it for the next renewal.

    Lifecycle::

        driver = ControllerRenewalDriver(zone_ca, initial_sequence=N)
        req = driver.build_request()
        # send `encode(req)`, await `decode(rx)` -> CertRenewalCSR
        install = driver.handle_csr(csr_msg)
        # send `encode(install)`, await `decode(rx)` -> CertRenewalAck
        cert = driver.handle_ack(ack_msg)
    """

    def __init__(self, zone_ca: ZoneCA, *, initial_sequence: int = 1) -> None:
        if initial_sequence < 0 or initial_sequence > 0xFFFFFFFF:
            raise ValueError(
                f"initial_sequence out of uint32 range: {initial_sequence}"
            )
        self._zone_ca = zone_ca
        self._sequence = initial_sequence
        self._nonce: bytes | None = None
        self._signed_cert: x509.Certificate | None = None
        self._device_id: str | None = None
        self._issued_sequence: int | None = None

    # ------------------------------------------------------------------
    # Step 1 — build CertRenewalRequest with a fresh nonce.
    # ------------------------------------------------------------------

    def build_request(self) -> CertRenewalRequest:
        """Generate a fresh 32-byte nonce and return a CertRenewalRequest.

        Stores the nonce internally for later binding-check on the CSR.
        Calling this more than once on the same driver replaces the
        previous nonce — useful for retries within the same instance,
        but invalidates any in-flight CSR.
        """
        self._nonce = secrets.token_bytes(NONCE_SIZE)
        self._signed_cert = None
        self._device_id = None
        self._issued_sequence = None
        return CertRenewalRequest(nonce=self._nonce)

    # ------------------------------------------------------------------
    # Step 2 — validate CSR, sign with Zone CA, build CertRenewalInstall.
    # ------------------------------------------------------------------

    def handle_csr(self, csr_msg: CertRenewalCSR) -> CertRenewalInstall:
        """Verify nonce binding, parse the CSR, sign with the Zone CA, and
        return a ``CertRenewalInstall`` for the device.

        Raises :class:`NonceBindingError` when the device's NonceHash
        does not match this driver's nonce — DEC-047 mandates the
        controller MUST abort renewal in that case.
        """
        if self._nonce is None:
            raise RenewalProtocolError(
                "build_request must be called before handle_csr"
            )
        if len(csr_msg.nonce_hash) != NONCE_HASH_SIZE:
            # Defence-in-depth: the decoder already enforces this; rejecting
            # again here keeps the state-machine layer self-contained.
            raise RenewalProtocolError(
                f"CertRenewalCSR.nonce_hash must be {NONCE_HASH_SIZE} bytes, "
                f"got {len(csr_msg.nonce_hash)}"
            )
        if not validate_nonce_hash(self._nonce, csr_msg.nonce_hash):
            raise NonceBindingError(
                "CertRenewalCSR.nonce_hash does not match request nonce"
            )

        try:
            csr_x509 = x509.load_der_x509_csr(csr_msg.csr)
        except ValueError as exc:
            raise RenewalProtocolError(
                f"CertRenewalCSR.csr is not valid DER PKCS#10: {exc}"
            ) from exc

        try:
            new_cert, device_id = sign_device_csr(self._zone_ca, csr_x509)
        except ValueError as exc:
            raise RenewalProtocolError(f"failed to sign device CSR: {exc}") from exc

        new_cert_der = new_cert.public_bytes(serialization.Encoding.DER)

        # Monotonic per-zone counter. We commit the increment here even
        # though the install hasn't been ack'd yet — a failed install is
        # still a "this sequence was issued" event, and the next renewal
        # MUST use a higher number to keep the device's monotonicity
        # invariant intact. Mirrors mash-go's `RenewDevice` behaviour.
        self._sequence = (self._sequence + 1) & 0xFFFFFFFF
        seq = self._sequence

        self._signed_cert = new_cert
        self._device_id = device_id
        self._issued_sequence = seq

        return CertRenewalInstall(new_cert=new_cert_der, sequence=seq)

    # ------------------------------------------------------------------
    # Step 3 — validate the ack and return the freshly signed cert.
    # ------------------------------------------------------------------

    def handle_ack(self, ack: CertRenewalAck) -> x509.Certificate:
        """Validate the ack status and sequence; return the signed cert.

        Raises :class:`RenewalFailedError` for any status != 0 and
        :class:`RenewalProtocolError` when ``active_sequence`` does not
        echo the controller's issued sequence.
        """
        if self._signed_cert is None or self._issued_sequence is None:
            raise RenewalProtocolError(
                "handle_csr must succeed before handle_ack"
            )
        if ack.status != RENEWAL_STATUS_SUCCESS:
            raise RenewalFailedError(ack.status, ack.active_sequence)
        if ack.active_sequence != self._issued_sequence:
            raise RenewalProtocolError(
                f"ack.active_sequence={ack.active_sequence} does not match "
                f"issued sequence {self._issued_sequence}"
            )
        return self._signed_cert

    # ------------------------------------------------------------------
    # Accessors
    # ------------------------------------------------------------------

    @property
    def nonce(self) -> bytes:
        """The current request nonce. Available after :meth:`build_request`."""
        if self._nonce is None:
            raise RenewalError("build_request has not been called")
        return self._nonce

    @property
    def signed_cert(self) -> x509.Certificate:
        """The Zone-CA-signed cert produced by :meth:`handle_csr`. Only
        valid after a successful :meth:`handle_ack`."""
        if self._signed_cert is None:
            raise RenewalError("renewal has not produced a signed cert")
        return self._signed_cert

    @property
    def device_id(self) -> str:
        """Matter-style device ID derived from the CSR public key."""
        if self._device_id is None:
            raise RenewalError("renewal has not produced a signed cert")
        return self._device_id

    @property
    def sequence(self) -> int:
        """Current next-issued sequence number. Increments on each
        successful :meth:`handle_csr`."""
        return self._sequence


# ---------------------------------------------------------------------------
# Device-side handler
# ---------------------------------------------------------------------------


@dataclass(frozen=True, slots=True)
class _PendingRenewal:
    private_key: EllipticCurvePrivateKey
    nonce: bytes


class DeviceRenewalHandler:
    """Device-side state machine for incoming renewal traffic.

    Owns the active operational cert + key + sequence and a single
    in-flight pending key pair. All public methods are message-level
    (input is a decoded renewal dataclass; output is a decoded reply
    dataclass) and thread-safe. The transport bridge is a separate
    concern — see :func:`dispatch_renewal_frame`.

    The handler does NOT verify the chain ``new_cert ← Zone CA``. The
    install is delivered over an authenticated TLS session against the
    controller cert that previously commissioned this device, so the
    controller is already trusted to sign certs. We DO verify that the
    new cert's public key matches the pending key — without this check,
    a malicious controller could install a cert for an unrelated key
    pair (mirrors mash-go's
    :go:func:`service.DeviceRenewalHandler.HandleCertInstall`).
    """

    def __init__(
        self,
        *,
        active_cert: x509.Certificate | None = None,
        active_key: EllipticCurvePrivateKey | None = None,
        active_sequence: int = 1,
    ) -> None:
        if active_sequence < 0 or active_sequence > 0xFFFFFFFF:
            raise ValueError(
                f"active_sequence out of uint32 range: {active_sequence}"
            )
        if (active_cert is None) != (active_key is None):
            raise ValueError(
                "active_cert and active_key must be both set or both None"
            )
        self._mu = threading.Lock()
        self._active_cert = active_cert
        self._active_key = active_key
        self._active_sequence = active_sequence
        self._pending: _PendingRenewal | None = None

    # ------------------------------------------------------------------
    # Public message-level API
    # ------------------------------------------------------------------

    def handle_request(self, req: CertRenewalRequest) -> CertRenewalCSR:
        """Generate a fresh P-256 key pair and return a CSR with nonce binding.

        Replaces any prior pending state — if a previous CSR was never
        followed by an Install, the controller has effectively abandoned
        that renewal session and we start fresh.
        """
        if len(req.nonce) != NONCE_SIZE:
            raise RenewalProtocolError(
                f"CertRenewalRequest.nonce must be {NONCE_SIZE} bytes, "
                f"got {len(req.nonce)}"
            )
        new_key = ec.generate_private_key(ec.SECP256R1())
        csr = create_csr(new_key)
        csr_der = csr.public_bytes(serialization.Encoding.DER)
        nonce_hash = compute_nonce_hash(req.nonce)
        with self._mu:
            self._pending = _PendingRenewal(
                private_key=new_key, nonce=bytes(req.nonce)
            )
        return CertRenewalCSR(csr=csr_der, nonce_hash=nonce_hash)

    def handle_install(self, install: CertRenewalInstall) -> CertRenewalAck:
        """Validate the new cert against the pending key pair and (on
        success) atomically swap to active. Always returns a wire-shaped
        ``CertRenewalAck`` — failures encode as a non-success status code,
        not as exceptions, so callers can ship the ack and let the
        controller decide whether to retry.
        """
        with self._mu:
            pending = self._pending

        if pending is None:
            return CertRenewalAck(
                status=RENEWAL_STATUS_INSTALL_FAILED,
                active_sequence=self._active_sequence,
            )

        try:
            new_cert = x509.load_der_x509_certificate(install.new_cert)
        except ValueError:
            return CertRenewalAck(
                status=RENEWAL_STATUS_INVALID_CERT,
                active_sequence=self._active_sequence,
            )

        cert_pub = new_cert.public_key()
        if not isinstance(cert_pub, EllipticCurvePublicKey):
            return CertRenewalAck(
                status=RENEWAL_STATUS_INVALID_CERT,
                active_sequence=self._active_sequence,
            )
        if not _public_keys_equal(cert_pub, pending.private_key.public_key()):
            # Mismatched public key implies the controller signed a CSR
            # from a different session — surface that as InvalidNonce
            # (mash-go does the same).
            return CertRenewalAck(
                status=RENEWAL_STATUS_INVALID_NONCE,
                active_sequence=self._active_sequence,
            )

        with self._mu:
            self._active_cert = new_cert
            self._active_key = pending.private_key
            self._active_sequence = install.sequence
            self._pending = None

        return CertRenewalAck(
            status=RENEWAL_STATUS_SUCCESS,
            active_sequence=install.sequence,
        )

    # ------------------------------------------------------------------
    # State accessors / mutators
    # ------------------------------------------------------------------

    def set_active_cert(
        self,
        cert: x509.Certificate,
        key: EllipticCurvePrivateKey,
        sequence: int,
    ) -> None:
        """Initialize the active cert/key/sequence (e.g. on device boot
        from the cert store). Clears any in-flight pending renewal.
        """
        if sequence < 0 or sequence > 0xFFFFFFFF:
            raise ValueError(f"sequence out of uint32 range: {sequence}")
        with self._mu:
            self._active_cert = cert
            self._active_key = key
            self._active_sequence = sequence
            self._pending = None

    @property
    def active_cert(self) -> x509.Certificate | None:
        with self._mu:
            return self._active_cert

    @property
    def active_key(self) -> EllipticCurvePrivateKey | None:
        with self._mu:
            return self._active_key

    @property
    def active_sequence(self) -> int:
        with self._mu:
            return self._active_sequence

    @property
    def renewal_in_progress(self) -> bool:
        with self._mu:
            return self._pending is not None


def _public_keys_equal(
    a: EllipticCurvePublicKey, b: EllipticCurvePublicKey
) -> bool:
    """Compare two ECDSA public keys via SubjectPublicKeyInfo DER.

    cryptography's ``EllipticCurvePublicKey`` does not implement
    ``__eq__`` reliably across versions; comparing the canonical
    SPKI DER avoids depending on internal structure.
    """
    a_der = a.public_bytes(
        encoding=serialization.Encoding.DER,
        format=serialization.PublicFormat.SubjectPublicKeyInfo,
    )
    b_der = b.public_bytes(
        encoding=serialization.Encoding.DER,
        format=serialization.PublicFormat.SubjectPublicKeyInfo,
    )
    return a_der == b_der


__all__ = [
    "ControllerRenewalDriver",
    "DeviceRenewalHandler",
    "NonceBindingError",
    "RenewalError",
    "RenewalFailedError",
    "RenewalProtocolError",
]
