"""Controller-side certificate-install state machine (Phase 9, DEC-085).

Drives the post-PASE cert exchange on the commissioning ALPN
(``mash-comm/1``) using the spec-strict commissioning envelope
(MsgTypes 10/11/12/13/20). Distinct from in-session renewal
(MsgTypes 30–33 on the operational ALPN) — see ``mash-py-spec-issues.md``
Issue #7 and DEC-085 for the namespace-separation rationale.

Spec source: ``docs/testing/behavior/commissioning-pase.md §4.3``.

Wire sequence::

    Controller → Device: CSRRequest{nonce[32]}                           (MsgType 10)
    Device     → Controller: CSRResponse{csr, errorCode}                 (MsgType 11)
    Controller → Device: CertInstall{operationalCert, caCert,
                                     zoneType, zonePriority}             (MsgType 12)
    Device     → Controller: CertInstallResponse{errorCode}              (MsgType 13)
    Device     → Controller: CommissioningComplete{}                     (MsgType 20)

Strict-to-spec behaviour
------------------------

- ``CSRResponse`` with ``error_code != 0`` raises
  :class:`CertInstallFailedError`. The controller does NOT proceed to
  ``CertInstall`` — the device has signalled it cannot generate a CSR.
- ``CertInstallResponse`` with ``error_code != 0`` raises
  :class:`CertInstallFailedError`. Per spec the device MUST NOT send
  ``CommissioningComplete`` on this path; the state machine errors out
  immediately.
- ``CommissioningError`` (MsgType 255) arriving in any read slot is a
  cross-cutting rejection (DEC-063) and surfaces as
  :class:`CommissioningRejectedError`.
- Any other envelope is a :class:`CertExchangeProtocolError` — same
  spirit as :class:`PASEProtocolError`, refusing to silently absorb a
  spec violation from the peer.
"""

from __future__ import annotations

import asyncio
import secrets
from typing import Final

from cryptography import x509
from cryptography.hazmat.primitives import serialization

from mash_py.commissioning.cert_store import ZoneCA, ZoneType, sign_device_csr
from mash_py.commissioning.messages import (
    CSR_NONCE_SIZE,
    ERR_CODE_SUCCESS,
    CertInstall,
    CertInstallResponse,
    CommissioningComplete,
    CommissioningError,
    CSRRequest,
    CSRResponse,
    InvalidMessageError,
    decode,
    encode,
    error_code_string,
)
from mash_py.wire.framing import (
    DEFAULT_MAX_MESSAGE_SIZE,
    AsyncFrameReader,
    AsyncFrameWriter,
    FramingError,
)


# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------


class CertExchangeError(Exception):
    """Base class for controller-side cert-install failures."""


class CertExchangeProtocolError(CertExchangeError):
    """Peer sent an envelope or sequence the spec does not allow.

    Examples: ``PASEResponse`` arriving in the ``CSRResponse`` slot;
    ``CertInstallResponse`` arriving before a ``CSRResponse``; truncated
    frame; malformed CBOR; an unknown ``msgType``.
    """


class CertInstallFailedError(CertExchangeError):
    """Device rejected CSR generation or cert installation.

    Carries the wire ``error_code`` and the protocol step that produced
    it so callers can distinguish "device couldn't sign a CSR" from
    "device rejected the operational cert we sent."
    """

    def __init__(self, step: str, error_code: int) -> None:
        super().__init__(
            f"device rejected {step}: {error_code_string(error_code)} "
            f"(error_code={error_code})"
        )
        self.step = step
        self.error_code = error_code


class CommissioningRejectedError(CertExchangeError):
    """Device sent a cross-cutting CommissioningError (MsgType 255).

    DEC-063 reserves this envelope for "device cannot start commissioning
    right now" (busy, internal error, zone slots full). Surfaces here for
    callers who want to react to back-off hints.
    """

    def __init__(
        self, error_code: int, message: str = "", retry_after_ms: int = 0
    ) -> None:
        super().__init__(
            f"device commissioning error: {error_code_string(error_code)} "
            f"(error_code={error_code}, retry_after_ms={retry_after_ms})"
            + (f": {message}" if message else "")
        )
        self.error_code = error_code
        self.message = message
        self.retry_after_ms = retry_after_ms


# ---------------------------------------------------------------------------
# State machine
# ---------------------------------------------------------------------------


class CertExchangeClient:
    """Controller-side driver for one commissioning cert-install attempt.

    Single-use: one instance handles one cert install. Re-using requires
    a fresh instance and (typically) a fresh commissioning connection —
    the device closes the connection after :class:`CommissioningComplete`
    per ``security.md §4.5``.

    Lifecycle::

        client = CertExchangeClient(zone_ca=ca, zone_priority=1)
        result = await client.run(reader, writer)
        # result.operational_cert is the device's signed cert
        # result.device_id is the Matter-style derived device ID

    The Zone CA is provided by the caller — Phase 9 doesn't deal with
    Zone CA persistence; that's a controller-bootstrap concern.
    """

    DEFAULT_MAX_MESSAGE_SIZE: Final[int] = DEFAULT_MAX_MESSAGE_SIZE

    def __init__(
        self,
        zone_ca: ZoneCA,
        *,
        zone_priority: int = 1,
        max_message_size: int = DEFAULT_MAX_MESSAGE_SIZE,
    ) -> None:
        if zone_priority < 1 or zone_priority > 255:
            raise ValueError(
                f"zone_priority must be in 1..255, got {zone_priority}"
            )
        self._zone_ca = zone_ca
        self._zone_priority = zone_priority
        self._max_message_size = max_message_size
        self._completed = False
        self._operational_cert: x509.Certificate | None = None
        self._device_id: str | None = None

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def run(
        self,
        reader: asyncio.StreamReader,
        writer: asyncio.StreamWriter,
    ) -> "CertExchangeResult":
        """Drive the five-message exchange end-to-end.

        Returns a :class:`CertExchangeResult` with the signed operational
        cert and the derived device ID. The caller is responsible for
        closing the commissioning connection and reconnecting on the
        operational ALPN; this state machine just produces the artifact.
        """
        frame_reader = AsyncFrameReader(reader, max_message_size=self._max_message_size)
        frame_writer = AsyncFrameWriter(writer, max_message_size=self._max_message_size)

        # Step 1: CSRRequest with a fresh 32-byte nonce.
        nonce = secrets.token_bytes(CSR_NONCE_SIZE)
        await self._send(
            frame_writer, writer, encode(CSRRequest(nonce=nonce))
        )

        # Step 2: CSRResponse.
        msg = await self._recv(frame_reader)
        if isinstance(msg, CommissioningError):
            raise CommissioningRejectedError(
                msg.error_code, msg.message, msg.retry_after_ms
            )
        if not isinstance(msg, CSRResponse):
            raise CertExchangeProtocolError(
                f"expected CSRResponse, got {type(msg).__name__}"
            )
        if msg.error_code != ERR_CODE_SUCCESS:
            raise CertInstallFailedError("CSR generation", msg.error_code)
        if not msg.csr:
            raise CertExchangeProtocolError(
                "CSRResponse.csr is empty on success path"
            )

        # Step 3: parse and sign the CSR.
        try:
            csr = x509.load_der_x509_csr(msg.csr)
        except ValueError as exc:
            raise CertExchangeProtocolError(
                f"CSRResponse.csr is not valid DER PKCS#10: {exc}"
            ) from exc

        # `sign_device_csr` derives the device ID from the CSR public key
        # per ``commissioning-pase.md §4.4`` and embeds it in the cert CN.
        try:
            operational_cert, device_id = sign_device_csr(self._zone_ca, csr)
        except ValueError as exc:
            raise CertExchangeProtocolError(
                f"failed to sign device CSR: {exc}"
            ) from exc

        operational_cert_der = operational_cert.public_bytes(
            serialization.Encoding.DER
        )

        # Step 4: CertInstall(operationalCert, caCert, zoneType, zonePriority).
        install = CertInstall(
            operational_cert=operational_cert_der,
            ca_cert=self._zone_ca.cert_der,
            zone_type=int(self._zone_ca.zone_type),
            zone_priority=self._zone_priority,
        )
        await self._send(frame_writer, writer, encode(install))

        # Step 5: CertInstallResponse.
        msg = await self._recv(frame_reader)
        if isinstance(msg, CommissioningError):
            raise CommissioningRejectedError(
                msg.error_code, msg.message, msg.retry_after_ms
            )
        if not isinstance(msg, CertInstallResponse):
            raise CertExchangeProtocolError(
                f"expected CertInstallResponse, got {type(msg).__name__}"
            )
        if msg.error_code != ERR_CODE_SUCCESS:
            raise CertInstallFailedError("cert install", msg.error_code)

        # Step 6: CommissioningComplete (terminator).
        msg = await self._recv(frame_reader)
        if isinstance(msg, CommissioningError):
            raise CommissioningRejectedError(
                msg.error_code, msg.message, msg.retry_after_ms
            )
        if not isinstance(msg, CommissioningComplete):
            raise CertExchangeProtocolError(
                f"expected CommissioningComplete, got {type(msg).__name__}"
            )

        self._operational_cert = operational_cert
        self._device_id = device_id
        self._completed = True

        return CertExchangeResult(
            operational_cert=operational_cert,
            device_id=device_id,
            zone_id=self._zone_ca.zone_name,
        )

    @property
    def operational_cert(self) -> x509.Certificate:
        """The signed operational cert. Available only after ``run`` succeeds."""
        if not self._completed or self._operational_cert is None:
            raise CertExchangeError("cert exchange has not completed successfully")
        return self._operational_cert

    @property
    def device_id(self) -> str:
        """The 16-hex-char Matter-style device ID derived from the CSR pubkey."""
        if not self._completed or self._device_id is None:
            raise CertExchangeError("cert exchange has not completed successfully")
        return self._device_id

    # ------------------------------------------------------------------
    # Internals
    # ------------------------------------------------------------------

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
            raise CertExchangeProtocolError(
                f"failed to send cert-install frame: {exc}"
            ) from exc

    @staticmethod
    async def _recv(
        frame_reader: AsyncFrameReader,
    ) -> CSRResponse | CertInstallResponse | CommissioningComplete | CommissioningError:
        try:
            payload = await frame_reader.read_frame()
        except (
            FramingError,
            asyncio.IncompleteReadError,
            ConnectionError,
            OSError,
        ) as exc:
            raise CertExchangeProtocolError(
                f"failed to read cert-install frame: {exc}"
            ) from exc
        try:
            msg = decode(payload)
        except InvalidMessageError as exc:
            raise CertExchangeProtocolError(
                f"malformed cert-install message: {exc}"
            ) from exc
        if not isinstance(
            msg,
            (CSRResponse, CertInstallResponse, CommissioningComplete, CommissioningError),
        ):
            raise CertExchangeProtocolError(
                f"controller received unexpected message type: {type(msg).__name__}"
            )
        return msg


# ---------------------------------------------------------------------------
# Result
# ---------------------------------------------------------------------------


class CertExchangeResult:
    """Outcome of a successful cert-install run."""

    __slots__ = ("operational_cert", "device_id", "zone_id")

    def __init__(
        self,
        operational_cert: x509.Certificate,
        device_id: str,
        zone_id: str,
    ) -> None:
        self.operational_cert = operational_cert
        self.device_id = device_id
        self.zone_id = zone_id


__all__ = [
    "CertExchangeClient",
    "CertExchangeError",
    "CertExchangeProtocolError",
    "CertExchangeResult",
    "CertInstallFailedError",
    "CommissioningRejectedError",
    "ZoneType",
]
