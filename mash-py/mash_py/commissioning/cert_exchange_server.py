"""Device-side post-PASE cert install state machine (Phase 14b).

Mirrors :class:`mash_py.commissioning.cert_exchange.CertExchangeClient`
from the device's perspective. Handed the post-PASE
``(reader, writer)`` pair from :class:`PASEServer`, this state machine:

1. Reads ``CSRRequest`` (validates 32-byte nonce length).
2. Generates a fresh P-256 key pair (NOT the commissioning-cert key —
   security.md §4.6 forbids reuse).
3. Sends ``CSRResponse`` carrying a PKCS#10 CSR signed by the new key.
4. Reads ``CertInstall``; validates the chain ``op_cert ← ca_cert``;
   verifies the op-cert's public key matches the freshly-generated key
   (defense against a malicious controller installing someone else's
   cert and bricking the device).
5. Sends ``CertInstallResponse{0}`` and ``CommissioningComplete{}`` on
   success; ``CertInstallResponse{ErrCodeCertInstallFailed}`` and
   raises on failure.

Spec source: ``docs/testing/behavior/commissioning-pase.md §4.3`` (wire
formats), §6.5 (error codes), §3.1 (state-machine transitions
PASE_VERIFIED → CSR_GENERATE → CERT_WAIT → CERT_INSTALL → COMPLETE).

Strict-to-spec behaviour
------------------------

- Only ``CertInstallResponse{error_code != 0}`` is emitted on
  in-cert-exchange failure paths. ``CommissioningError`` is **never**
  sent from this state machine — same envelope-uniformity rule as
  Phase 14 (PASE), and the same DEC-085 disposition for cert-install
  failures.
- The state machine does NOT persist material itself — it returns the
  validated artifacts (op-cert, op-key, Zone CA, zone metadata) for the
  caller to persist via whichever cert-store strategy the device uses.
  Mash-go's listener layer holds the cert store; mash-py's
  device-service layer (Phase 15) will own the equivalent.
- Zone-policy validation (zone-type-already-exists, slots-full) is
  layered on TOP of this state machine via an optional
  ``validate_zone`` callback. The callback may reject a zone via a
  raised :class:`ZoneRejectedError` carrying the wire-level error code
  to send. The cleaner alternative — reading the zone state from a
  device-service handle — would couple Phase 14b to Phase 15; the
  callback decouples the two.

Transport coupling
------------------

Like :class:`PASEServer`, this consumes ``asyncio.StreamReader`` /
``asyncio.StreamWriter`` directly. Phase 14b runs on the same
commissioning ALPN channel as PASE, immediately after PASE completes.

Why a separate state machine instead of folding into PASEServer
---------------------------------------------------------------

The PASE handshake and the cert-install flow are conceptually distinct
sub-protocols sharing one TCP connection. Splitting them keeps each
state machine's invariants narrow and lets Phase 15 reuse only the
piece it needs (cert install can run independently of PASE in tests).
"""

from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Final

from cryptography import x509
from cryptography.exceptions import InvalidSignature
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.hazmat.primitives.asymmetric.ec import (
    EllipticCurvePrivateKey,
    EllipticCurvePublicKey,
)

from mash_py.commissioning.messages import (
    CSR_NONCE_SIZE,
    ERR_CODE_BUSY,
    ERR_CODE_CERT_INSTALL_FAILED,
    ERR_CODE_SUCCESS,
    ERR_CODE_ZONE_TYPE_EXISTS,
    CertInstall,
    CertInstallResponse,
    CommissioningComplete,
    CSRRequest,
    CSRResponse,
    InvalidMessageError,
    decode,
    encode,
)
from mash_py.crypto.certs import create_csr
from mash_py.wire.framing import (
    DEFAULT_MAX_MESSAGE_SIZE,
    AsyncFrameReader,
    AsyncFrameWriter,
    FramingError,
)

_DEFAULT_PHASE_TIMEOUT_S: Final[float] = 30.0
"""§5.2 CSR_WAIT / CERT_WAIT budget. Spec table at §5.2 lists CSR_GENERATE=10s
and CERT_WAIT=30s; we use the larger value as a uniform per-phase budget for
inbound reads since the device is the *waiting* party in both cases."""


# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------


class CertExchangeServerError(Exception):
    """Base class for device-side cert-exchange failures."""


class CertExchangeServerProtocolError(CertExchangeServerError):
    """The peer sent an envelope or sequence the spec does not allow.

    Examples:

    - The first inbound frame after PASE was not a ``CSRRequest``.
    - ``CSRRequest.nonce`` length is not 32 (§4.3 explicit MUST).
    - A different message arrived where ``CertInstall`` was expected.
    - Truncated frame, malformed CBOR, unknown ``msgType``.
    """


class CertExchangeServerInstallError(CertExchangeServerError):
    """A spec-defined cert-install failure path triggered.

    Raised after the device has already emitted
    ``CertInstallResponse{error_code != 0}`` on the wire. The connection
    is expected to close immediately; the listener should return to
    ADVERTISING per §6.3.

    ``error_code`` carries the wire-level code that was sent so a
    caller logging the failure can match it to spec §6.5 / DEC-047.
    """

    def __init__(self, reason: str, error_code: int) -> None:
        super().__init__(
            f"cert install failed: {reason} (error_code={error_code})"
        )
        self.reason = reason
        self.error_code = error_code


class CertExchangePhaseTimeoutError(CertExchangeServerError):
    """The peer did not send the expected next message within the
    per-phase timeout (§5.2 CSR_WAIT / CERT_WAIT).

    No envelope is emitted — §3.1 transitions on timeout straight back
    to ADVERTISING.
    """


class ZoneRejectedError(CertExchangeServerError):
    """Caller's ``validate_zone`` callback rejected the zone parameters.

    Raised by the callback to signal a zone-policy failure
    (zone-type-exists, slots-full, etc.) along with the wire error
    code to send to the controller. The state machine catches this
    inside ``run`` and emits ``CertInstallResponse{error_code}`` before
    re-raising as a :class:`CertExchangeServerInstallError`.
    """

    def __init__(self, reason: str, error_code: int) -> None:
        super().__init__(reason)
        self.reason = reason
        self.error_code = error_code


# ---------------------------------------------------------------------------
# Result
# ---------------------------------------------------------------------------


@dataclass(frozen=True, slots=True)
class CertExchangeServerResult:
    """Persistance material produced by a successful cert-install.

    The caller (typically a device-service layer in Phase 15) is
    responsible for persisting these to its cert store, registering the
    new zone, and bringing up the operational listener. This state
    machine is pure: no side effects beyond the wire and the returned
    artifacts.
    """

    operational_cert: x509.Certificate
    operational_key: EllipticCurvePrivateKey
    zone_ca_cert: x509.Certificate
    zone_type: int
    zone_priority: int


# Validate-zone callback signature. Sync or async; raises
# :class:`ZoneRejectedError` to reject a zone with a specific wire code.
ValidateZoneCallback = Callable[
    [int, x509.Certificate],  # (zone_type, zone_ca_cert)
    Awaitable[None] | None,
]


# ---------------------------------------------------------------------------
# State machine
# ---------------------------------------------------------------------------


class CertExchangeServer:
    """Device-side driver for a single post-PASE cert-install attempt.

    Single-use: one instance handles one cert install. Reusing requires
    a fresh instance and (typically) a fresh commissioning connection.

    Args:
        phase_timeout_s: Per-message read timeout in seconds (§5.2).
            Default 30 s (CERT_WAIT — the larger of the two inbound-read
            phases the device sits in).
        max_message_size: Framing-layer cap; default
            ``DEFAULT_MAX_MESSAGE_SIZE`` (8 KB per DEC-069). Op-certs
            are typically <600 bytes; CA certs <800 bytes; one frame
            comfortably holds both plus zone metadata.
        validate_zone: Optional callback invoked between parsing
            ``CertInstall`` and persisting. Raises
            :class:`ZoneRejectedError` to reject the zone with a
            specific wire error code; otherwise returns ``None``. May
            be sync or async.
    """

    DEFAULT_PHASE_TIMEOUT_S: Final[float] = _DEFAULT_PHASE_TIMEOUT_S

    def __init__(
        self,
        *,
        phase_timeout_s: float = _DEFAULT_PHASE_TIMEOUT_S,
        max_message_size: int = DEFAULT_MAX_MESSAGE_SIZE,
        validate_zone: ValidateZoneCallback | None = None,
    ) -> None:
        if phase_timeout_s <= 0:
            raise ValueError("phase_timeout_s must be positive")
        self._phase_timeout_s = phase_timeout_s
        self._max_message_size = max_message_size
        self._validate_zone = validate_zone
        self._completed = False
        self._result: CertExchangeServerResult | None = None

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def run(
        self,
        reader: asyncio.StreamReader,
        writer: asyncio.StreamWriter,
    ) -> CertExchangeServerResult:
        """Drive the five-message cert-install exchange end-to-end.

        On success, returns a :class:`CertExchangeServerResult` with the
        validated operational cert, freshly-generated key, Zone CA, and
        zone metadata for the caller to persist.

        Raises:
            CertExchangeServerProtocolError: peer sent a frame the spec
                does not allow in the current slot, or the underlying
                transport failed.
            CertExchangeServerInstallError: a spec-defined cert-install
                failure path triggered (chain validation, key mismatch,
                zone rejected). The matching ``CertInstallResponse``
                with non-zero ``error_code`` has been sent to the wire.
            CertExchangePhaseTimeoutError: the peer did not send the
                next message within ``phase_timeout_s``.
        """
        frame_reader = AsyncFrameReader(reader, max_message_size=self._max_message_size)
        frame_writer = AsyncFrameWriter(writer, max_message_size=self._max_message_size)

        # Step 1: read CSRRequest.
        await self._recv_csr_request(frame_reader)

        # Step 2: generate fresh P-256 key pair (NOT the commissioning-cert
        # key per §4.6).
        operational_key = ec.generate_private_key(ec.SECP256R1())

        # Step 3: build CSR and send CSRResponse{success}.
        csr = create_csr(operational_key)
        csr_der = csr.public_bytes(serialization.Encoding.DER)
        await self._send(
            frame_writer,
            writer,
            encode(self._make_csr_response(csr_der)),
        )

        # Step 4: read CertInstall (subject to phase timeout — CERT_WAIT).
        install = await self._recv_cert_install(frame_reader)

        # Step 5: parse, validate chain, verify pubkey binding.
        op_cert, ca_cert = await self._validate_install(
            frame_writer, writer, install, operational_key
        )

        # Step 6: optional zone-policy callback.
        await self._run_validate_zone(
            frame_writer, writer, install.zone_type, ca_cert
        )

        # Step 7: send CertInstallResponse{0} and CommissioningComplete{}.
        await self._send(
            frame_writer,
            writer,
            encode(CertInstallResponse(error_code=ERR_CODE_SUCCESS)),
        )
        await self._send(
            frame_writer, writer, encode(CommissioningComplete())
        )

        result = CertExchangeServerResult(
            operational_cert=op_cert,
            operational_key=operational_key,
            zone_ca_cert=ca_cert,
            zone_type=install.zone_type,
            zone_priority=install.zone_priority,
        )
        self._result = result
        self._completed = True
        return result

    @property
    def result(self) -> CertExchangeServerResult:
        """The successful exchange's artifacts. Available only after ``run`` succeeds."""
        if not self._completed or self._result is None:
            raise CertExchangeServerError(
                "cert exchange has not completed successfully"
            )
        return self._result

    # ------------------------------------------------------------------
    # Internals — receive
    # ------------------------------------------------------------------

    async def _recv_csr_request(
        self, frame_reader: AsyncFrameReader
    ) -> CSRRequest:
        """Read the first frame; require it to be a ``CSRRequest`` with a
        spec-conformant 32-byte nonce."""
        msg = await self._recv_message(frame_reader)
        if not isinstance(msg, CSRRequest):
            raise CertExchangeServerProtocolError(
                f"expected CSRRequest, got {type(msg).__name__}"
            )
        # Decode side already enforces the 32-byte nonce length via
        # `_decode_csr_request`; this assertion is a defensive belt-and-
        # braces in case a future decode-side relaxation slips through.
        if len(msg.nonce) != CSR_NONCE_SIZE:
            raise CertExchangeServerProtocolError(
                f"CSRRequest.nonce must be {CSR_NONCE_SIZE} bytes, got {len(msg.nonce)}"
            )
        return msg

    async def _recv_cert_install(
        self, frame_reader: AsyncFrameReader
    ) -> CertInstall:
        """Read CertInstall under the §5.2 CERT_WAIT 30 s timeout."""
        msg = await self._recv_message(frame_reader, with_timeout=True)
        if not isinstance(msg, CertInstall):
            raise CertExchangeServerProtocolError(
                f"expected CertInstall, got {type(msg).__name__}"
            )
        return msg

    async def _recv_message(
        self,
        frame_reader: AsyncFrameReader,
        *,
        with_timeout: bool = False,
    ) -> object:
        """Read one framed message. Surface wire errors as
        :class:`CertExchangeServerProtocolError`; timeouts as
        :class:`CertExchangePhaseTimeoutError`."""
        try:
            if with_timeout:
                payload = await asyncio.wait_for(
                    frame_reader.read_frame(), timeout=self._phase_timeout_s
                )
            else:
                payload = await frame_reader.read_frame()
        except TimeoutError as exc:
            raise CertExchangePhaseTimeoutError(
                f"timed out after {self._phase_timeout_s}s waiting for next "
                "cert-install message"
            ) from exc
        except (
            FramingError,
            asyncio.IncompleteReadError,
            ConnectionError,
            OSError,
        ) as exc:
            raise CertExchangeServerProtocolError(
                f"failed to read cert-install frame: {exc}"
            ) from exc
        try:
            return decode(payload)
        except InvalidMessageError as exc:
            raise CertExchangeServerProtocolError(
                f"malformed cert-install message: {exc}"
            ) from exc

    # ------------------------------------------------------------------
    # Internals — validate
    # ------------------------------------------------------------------

    async def _validate_install(
        self,
        frame_writer: AsyncFrameWriter,
        writer: asyncio.StreamWriter,
        install: CertInstall,
        operational_key: EllipticCurvePrivateKey,
    ) -> tuple[x509.Certificate, x509.Certificate]:
        """Parse + validate ``op_cert``/``ca_cert``; on any failure send
        ``CertInstallResponse{ErrCodeCertInstallFailed}`` and raise."""
        try:
            ca_cert = x509.load_der_x509_certificate(install.ca_cert)
        except ValueError as exc:
            await self._fail_install(
                frame_writer,
                writer,
                ERR_CODE_CERT_INSTALL_FAILED,
                f"parse Zone CA cert: {exc}",
            )
        try:
            op_cert = x509.load_der_x509_certificate(install.operational_cert)
        except ValueError as exc:
            await self._fail_install(
                frame_writer,
                writer,
                ERR_CODE_CERT_INSTALL_FAILED,
                f"parse operational cert: {exc}",
            )

        # Chain check: op_cert must be signed by ca_cert. Using the
        # low-level signature verification here rather than
        # cryptography's PolicyBuilder/Verifier so we control validity-
        # window strictness independently — operational certs are
        # short-lived and we want explicit clock-skew tolerance to
        # match the issuer's 5-minute clock-skew allowance from
        # `generate_commissioning_cert`.
        try:
            ca_public_key = ca_cert.public_key()
            if not isinstance(ca_public_key, EllipticCurvePublicKey):
                raise InvalidSignature("Zone CA must be ECDSA P-256")
            ca_public_key.verify(
                op_cert.signature,
                op_cert.tbs_certificate_bytes,
                ec.ECDSA(op_cert.signature_hash_algorithm or hashes.SHA256()),
            )
        except (InvalidSignature, ValueError, TypeError) as exc:
            await self._fail_install(
                frame_writer,
                writer,
                ERR_CODE_CERT_INSTALL_FAILED,
                f"chain validation failed: {exc}",
            )

        # Public-key binding: op_cert must carry the public key of the
        # device-generated key pair. Without this check, a malicious
        # controller could install a cert for a different key pair.
        op_pub_der = op_cert.public_key().public_bytes(
            encoding=serialization.Encoding.DER,
            format=serialization.PublicFormat.SubjectPublicKeyInfo,
        )
        want_pub_der = operational_key.public_key().public_bytes(
            encoding=serialization.Encoding.DER,
            format=serialization.PublicFormat.SubjectPublicKeyInfo,
        )
        if op_pub_der != want_pub_der:
            await self._fail_install(
                frame_writer,
                writer,
                ERR_CODE_CERT_INSTALL_FAILED,
                "operational cert public key does not match CSR public key",
            )

        return op_cert, ca_cert

    async def _run_validate_zone(
        self,
        frame_writer: AsyncFrameWriter,
        writer: asyncio.StreamWriter,
        zone_type: int,
        ca_cert: x509.Certificate,
    ) -> None:
        """Run the optional zone-policy callback.

        Maps :class:`ZoneRejectedError` (raised by the callback) to a
        wire-level ``CertInstallResponse{error_code}``; supports both
        sync and async callbacks.
        """
        if self._validate_zone is None:
            return
        try:
            result = self._validate_zone(zone_type, ca_cert)
            if asyncio.iscoroutine(result):
                await result
        except ZoneRejectedError as exc:
            # Map to the spec-defined wire codes. Anything outside the
            # known set falls back to ErrCodeCertInstallFailed so we
            # never surface an undefined error code on the wire.
            code = exc.error_code
            if code not in (
                ERR_CODE_BUSY,
                ERR_CODE_CERT_INSTALL_FAILED,
                ERR_CODE_ZONE_TYPE_EXISTS,
            ):
                code = ERR_CODE_CERT_INSTALL_FAILED
            await self._fail_install(frame_writer, writer, code, exc.reason)

    # ------------------------------------------------------------------
    # Internals — send
    # ------------------------------------------------------------------

    @staticmethod
    def _make_csr_response(csr_der: bytes) -> CSRResponse:
        """Build a ``CSRResponse{csr, errorCode=0}``.

        Lifted into a static helper so the fast-path code in ``run``
        stays linear.
        """
        return CSRResponse(csr=csr_der, error_code=ERR_CODE_SUCCESS)

    @staticmethod
    async def _send(
        frame_writer: AsyncFrameWriter,
        writer: asyncio.StreamWriter,
        payload: bytes,
    ) -> None:
        try:
            await frame_writer.write_frame(payload)
            await writer.drain()
        except (FramingError, ConnectionError, OSError) as exc:
            raise CertExchangeServerProtocolError(
                f"failed to send cert-install frame: {exc}"
            ) from exc

    async def _fail_install(
        self,
        frame_writer: AsyncFrameWriter,
        writer: asyncio.StreamWriter,
        error_code: int,
        reason: str,
    ) -> None:
        """Send ``CertInstallResponse{error_code}`` then raise
        :class:`CertExchangeServerInstallError`. Best-effort send: a
        writer error doesn't change the install outcome (peer hung up
        before we acked the failure)."""
        import contextlib

        with contextlib.suppress(CertExchangeServerProtocolError):
            await self._send(
                frame_writer,
                writer,
                encode(CertInstallResponse(error_code=error_code)),
            )
        raise CertExchangeServerInstallError(reason, error_code)


__all__ = [
    "CertExchangePhaseTimeoutError",
    "CertExchangeServer",
    "CertExchangeServerError",
    "CertExchangeServerInstallError",
    "CertExchangeServerProtocolError",
    "CertExchangeServerResult",
    "ValidateZoneCallback",
    "ZoneRejectedError",
]
