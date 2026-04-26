"""Controller-side PASE state machine (Phase 8).

Drives the four-message SPAKE2+ exchange — :class:`SPAKE2PlusClient`
provides the cryptographic primitives, this module sequences them onto
the wire.

Spec source: ``docs/testing/behavior/commissioning-pase.md §4.1`` (message
flow) and ``§4.2`` (wire format). The handshake runs over a length-prefixed
framed transport — the same 4-byte big-endian framing used elsewhere in
the protocol — sitting on top of the commissioning TLS connection.

Strict-to-spec behavior
-----------------------

The state machine accepts only spec-conformant message envelopes:

- After ``PASERequest``: expects ``PASEResponse`` (success) or
  ``CommissioningError{ErrCodeBusy}`` (DEC-063 — device temporarily
  rejects the attempt). Anything else, including ``CommissioningError``
  with a non-busy code, is a :class:`PASEProtocolError`.
- After ``PASEConfirm``: expects ``PASEComplete``. A non-zero
  ``error_code`` raises :class:`PASEAuthFailedError`; any other message
  type is a :class:`PASEProtocolError`.

mash-go currently mis-uses ``CommissioningError`` for failures *inside*
the PASE handshake (invalid pA, wrong client identity) where the spec
mandates ``PASEComplete``. mash-py refuses to silently absorb that bug —
the failure surfaces as :class:`PASEProtocolError`, which is what the
Phase 8 interop test asserts. See ``mash-py-spec-issues.md`` Issue #6 for
the planned mash-go fix.

Transport coupling
------------------

:class:`PASEClient` consumes a pair of :class:`asyncio.StreamReader` /
:class:`asyncio.StreamWriter` directly. Phase 8 doesn't need the full
:class:`mash_py.transport.Connection` machinery (control frames,
ping/pong, application dispatch) — those layers come into play only on
the operational ALPN. The commissioning flow is a short, linear
exchange of four messages with no concurrency, so a thin framer on top
of the streams is sufficient.
"""

from __future__ import annotations

import asyncio
from typing import Final

from mash_py.commissioning.messages import (
    ERR_CODE_BUSY,
    ERR_CODE_SUCCESS,
    CommissioningError,
    InvalidMessageError,
    PASEComplete,
    PASEConfirm,
    PASERequest,
    PASEResponse,
    decode,
    encode,
    error_code_string,
)
from mash_py.commissioning.spake2plus import (
    ConfirmationFailedError,
    SPAKE2PlusClient,
    SPAKE2PlusError,
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


class PASEClientError(Exception):
    """Base class for controller-side PASE handshake failures."""


class PASEProtocolError(PASEClientError):
    """The peer sent an envelope or sequence the spec does not allow.

    Examples:

    - A ``CommissioningError`` with code != ``ERR_CODE_BUSY`` arriving in
      the slot for ``PASEResponse``.
    - A ``PASEConfirm`` arriving where ``PASEComplete`` was expected.
    - A truncated frame, malformed CBOR, or an unknown ``msgType``.

    This class is what mash-py raises when it observes mash-go's Issue #6
    bug — the controller refuses to treat a non-conformant envelope as a
    valid auth failure.
    """


class PASEAuthFailedError(PASEClientError):
    """The device returned a non-zero ``error_code`` in ``PASEComplete``.

    DEC-047 / spec §2.5 mandate ``ERR_CODE_AUTH_FAILED`` (1) for every
    PASE-internal failure. Older mash-go releases also send the
    deprecated ``ErrCodeConfirmFailed = 2`` on MAC mismatch (Issue #6);
    both are surfaced as this single error class to discourage callers
    from branching on a value that is supposed to be unified.
    """

    def __init__(self, error_code: int) -> None:
        super().__init__(
            f"device rejected PASE: {error_code_string(error_code)} "
            f"(error_code={error_code})"
        )
        self.error_code = error_code


class PASEBusyError(PASEClientError):
    """Device responded to ``PASERequest`` with DEC-063 busy rejection.

    Carries the controller-suggested ``retry_after_ms`` hint so callers
    can implement back-off without parsing the underlying envelope.
    """

    def __init__(self, retry_after_ms: int, message: str = "") -> None:
        super().__init__(
            f"device busy (retry_after_ms={retry_after_ms})"
            + (f": {message}" if message else "")
        )
        self.retry_after_ms = retry_after_ms
        self.message = message


# ---------------------------------------------------------------------------
# State machine
# ---------------------------------------------------------------------------


class PASEClient:
    """Controller-side driver for a single PASE handshake.

    Single-use: one instance handles one attempt. Re-running the
    handshake on the same connection requires a new ``PASEClient``
    instance (and typically a new TLS connection — the device closes the
    socket on auth failure per spec §2.5).

    Lifecycle::

        client = PASEClient(setup_code="20202021", discriminator=1234)
        k_main = await client.handshake(reader, writer)

    On success returns the 32-byte ``K_main`` derived in the SPAKE2+
    chain. ``K_encrypt`` is also available via :attr:`encryption_key`
    after a successful handshake.

    Parameters mirror the QR-code surface (DEC-068 setup code +
    discriminator). Keep ``x`` unset in production — the deterministic
    override exists for cross-impl conformance vectors.
    """

    DEFAULT_MAX_MESSAGE_SIZE: Final[int] = DEFAULT_MAX_MESSAGE_SIZE

    def __init__(
        self,
        setup_code: str,
        discriminator: int,
        *,
        iterations: int | None = None,
        x: int | None = None,
        max_message_size: int = DEFAULT_MAX_MESSAGE_SIZE,
    ) -> None:
        if iterations is None:
            self._spake = SPAKE2PlusClient(setup_code, discriminator, x=x)
        else:
            self._spake = SPAKE2PlusClient(
                setup_code, discriminator, iterations=iterations, x=x
            )
        self._max_message_size = max_message_size
        self._completed = False

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def handshake(
        self,
        reader: asyncio.StreamReader,
        writer: asyncio.StreamWriter,
    ) -> bytes:
        """Run the four-message PASE exchange, return the derived ``K_main``.

        Raises:
            PASEAuthFailedError: device returned a non-zero error in
                ``PASEComplete`` (wrong setup code, etc.).
            PASEBusyError: device sent ``CommissioningError{ErrCodeBusy}``
                in lieu of ``PASEResponse`` (DEC-063 busy rejection).
            PASEProtocolError: peer violated the wire-format / sequence
                contract — including mash-go's Issue #6 envelope mis-use.
            ConfirmationFailedError: device's ``confirmB`` did not verify
                against ``KcB`` (controller's view of MAC mismatch — the
                device-side mirror of this is reported via
                ``PASEAuthFailedError``).
        """
        frame_reader = AsyncFrameReader(reader, max_message_size=self._max_message_size)
        frame_writer = AsyncFrameWriter(writer, max_message_size=self._max_message_size)

        # Step 1: send PASERequest with our pA + ClientIdentity.
        # ClientIdentity defaults to b"mash-controller" per DEC-084 §4.2 —
        # the dataclass default; we don't override it.
        request = PASERequest(public_value=self._spake.public_value())
        await self._send(frame_writer, writer, encode(request))

        # Step 2: read PASEResponse (or DEC-063 CommissioningError{busy}).
        msg = await self._recv(frame_reader)
        if isinstance(msg, CommissioningError):
            self._handle_response_phase_error(msg)
        if not isinstance(msg, PASEResponse):
            raise PASEProtocolError(
                f"expected PASEResponse, got {type(msg).__name__}"
            )

        # Step 3: process pB → derive K_shared, K_main, K_confirm, K_encrypt.
        try:
            self._spake.process_server_value(msg.public_value)
        except SPAKE2PlusError as exc:
            # The peer sent a malformed or degenerate pB. Surface as a
            # protocol error rather than auth failure — there is no
            # ambiguity here, this is wire-level corruption.
            raise PASEProtocolError(f"invalid PASEResponse pB: {exc}") from exc

        # Step 4: send PASEConfirm with our confirmA = HMAC(KcA, pB).
        confirm = PASEConfirm(confirmation=self._spake.confirmation())
        await self._send(frame_writer, writer, encode(confirm))

        # Step 5: read PASEComplete (only — CommissioningError here would
        # be the Issue #6 mash-go bug, which we surface as a protocol error).
        msg = await self._recv(frame_reader)
        if not isinstance(msg, PASEComplete):
            raise PASEProtocolError(
                f"expected PASEComplete, got {type(msg).__name__} "
                f"(spec §2.5 mandates PASEComplete for all in-PASE failures; "
                f"see mash-py-spec-issues.md Issue #6)"
            )

        # Step 6: check error_code, then verify confirmB.
        if msg.error_code != ERR_CODE_SUCCESS:
            raise PASEAuthFailedError(msg.error_code)

        # ConfirmationFailedError propagates verbatim — this is the
        # controller's local view of MAC mismatch (the device's view is
        # reflected in PASEComplete.error_code, handled above).
        self._spake.verify_server_confirmation(msg.confirmation)

        self._completed = True
        return self._spake.shared_secret

    @property
    def shared_secret(self) -> bytes:
        """``K_main`` (32 bytes). Available only after a successful handshake."""
        if not self._completed:
            raise PASEClientError("handshake has not completed successfully")
        return self._spake.shared_secret

    @property
    def encryption_key(self) -> bytes:
        """``K_encrypt`` (32 bytes). Available only after a successful handshake."""
        if not self._completed:
            raise PASEClientError("handshake has not completed successfully")
        return self._spake.encryption_key

    # ------------------------------------------------------------------
    # Internals
    # ------------------------------------------------------------------

    @staticmethod
    def _handle_response_phase_error(err: CommissioningError) -> None:
        """Process a CommissioningError received in the PASEResponse slot.

        Spec-strict: only ``ERR_CODE_BUSY`` (DEC-063) is a valid
        cross-cutting error here. Anything else means the peer either
        spoke a non-conformant envelope (Issue #6) or returned an
        otherwise-undefined error — either way, surface it as a protocol
        error so the operator notices the spec violation.
        """
        if err.error_code == ERR_CODE_BUSY:
            raise PASEBusyError(err.retry_after_ms, err.message)
        raise PASEProtocolError(
            f"unexpected CommissioningError(error_code={err.error_code}, "
            f"{error_code_string(err.error_code)!r}) in PASEResponse slot — "
            f"spec §2.5 mandates PASEComplete for in-PASE failures "
            f"(see mash-py-spec-issues.md Issue #6)"
        )

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
            raise PASEProtocolError(f"failed to send PASE frame: {exc}") from exc

    @staticmethod
    async def _recv(
        frame_reader: AsyncFrameReader,
    ) -> PASEResponse | PASEComplete | CommissioningError:
        """Read one framed PASE message; surface any wire-level error as
        :class:`PASEProtocolError` so the caller has a single error class
        for "the peer broke the contract"."""
        try:
            payload = await frame_reader.read_frame()
        except (
            FramingError,
            asyncio.IncompleteReadError,
            ConnectionError,
            OSError,
        ) as exc:
            raise PASEProtocolError(f"failed to read PASE frame: {exc}") from exc
        try:
            msg = decode(payload)
        except InvalidMessageError as exc:
            raise PASEProtocolError(f"malformed PASE message: {exc}") from exc
        # Restrict the type so mypy / readers see the controller-relevant
        # subset; PASERequest/PASEConfirm are device-side inbound messages
        # and would themselves be a spec violation here.
        if not isinstance(msg, (PASEResponse, PASEComplete, CommissioningError)):
            raise PASEProtocolError(
                f"controller received unexpected PASE message type: "
                f"{type(msg).__name__}"
            )
        return msg


__all__ = [
    "PASEAuthFailedError",
    "PASEBusyError",
    "PASEClient",
    "PASEClientError",
    "PASEProtocolError",
]
