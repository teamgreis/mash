"""Device-side PASE state machine (Phase 14).

Mirrors :class:`mash_py.commissioning.pase_client.PASEClient` from the
device's perspective: drives the four-message SPAKE2+ exchange where the
device is the responder.

Spec source: ``docs/testing/behavior/commissioning-pase.md §4.1``
(message flow) and ``§4.2`` (wire format), with §6.5 timing requirements
and §2.5 implementation requirements applied throughout.

Lifecycle::

    server = PASEServer(verifier)
    k_main = await server.handshake(reader, writer)

The verifier is the persisted ``(salt, w0, L)`` triple from factory
provisioning (DEC-084 §2.3); the device does NOT hold the setup code at
runtime. On success, ``shared_secret`` (K_main) and ``encryption_key``
(K_encrypt) are available for the post-PASE cert-install phase.

Strict-to-spec behaviour
------------------------

The state machine emits only spec-conformant message envelopes for every
in-PASE failure:

- Wrong ``ClientIdentity`` → ``PASEComplete{ErrCodeAuthFailed}`` (§4.2 +
  DEC-019 reservation).
- Invalid ``pA`` (off-curve, identity element, wrong length) →
  ``PASEComplete{ErrCodeAuthFailed}`` (§2.5 + DEC-047 consolidated codes).
- ``confirmA`` MAC mismatch → ``PASEComplete{ErrCodeAuthFailed}``
  (§2.5 + DEC-047).

In every failure case the confirmation field is zero-padded to 32 bytes
(matching mash-go's wire shape; the SPAKE2+ keys are not derived for the
identity / point-validation paths, and the spec is silent on the byte
content of confirmation on failure). DEC-047 mandates a single generic
auth-failure code, so the wire MUST NOT distinguish which check failed.

CommissioningError (MsgType 255) is **never** emitted from inside the
PASE handshake — it is reserved for cross-cutting rejections (DEC-063
device busy, etc.) that happen *before* a PASE attempt is processed.
Those are the responsibility of the listener that accepts the TLS
connection, not this state machine.

Timing protection (§6.5 / Issue #14)
------------------------------------

Spec §6.5 mandates "random delay (100-500ms) to all error responses"; on
constrained devices (§5.3 ESP32 class, 2-10 s SPAKE2+ compute) bounded
random delay does not eliminate the timing oracle. mash-go ships a
deterministic minimum-duration floor instead, and mash-py does the same.

``error_delay_floor_s`` (default 0.2 s, matches mash-go) holds every
error path to wall-clock time ≥ the floor measured from handshake start.
Set to ``0`` in tests for fast suites; on constrained devices, raise to
≥ the SPAKE2+ compute upper bound (§5.3) so all error paths are
indistinguishable.

See ``mash-py-spec-issues.md`` Issue #14 for the proposed §6.5 spec edit
that codifies the constant-time floor as an explicitly-conformant
mitigation.

Per-phase timeout (§5.2 / §3.2)
-------------------------------

``phase_timeout_s`` (default 30 s) bounds how long the server waits for
the next inbound PASE message. Spec §3.2 names PASE_WAIT_X /
PASE_WAIT_V as 30 s each; this implementation applies the same budget to
both inbound reads. Hitting the timeout closes the handshake without
emitting an error envelope (the caller can release the commissioning
window and return to ADVERTISING).

Transport coupling
------------------

Like :class:`PASEClient`, this consumes ``asyncio.StreamReader`` /
``asyncio.StreamWriter`` directly. Phase 14 does not need full
:class:`mash_py.transport.Connection` machinery — the commissioning flow
is a short, linear exchange.
"""

from __future__ import annotations

import asyncio
import contextlib
import time
from typing import Final

from mash_py.commissioning.messages import (
    ERR_CODE_AUTH_FAILED,
    ERR_CODE_SUCCESS,
    InvalidMessageError,
    PASEComplete,
    PASEConfirm,
    PASERequest,
    PASEResponse,
    decode,
    encode,
)
from mash_py.commissioning.spake2plus import (
    CONFIRM_MAC_SIZE,
    CONTROLLER_IDENTITY,
    ConfirmationFailedError,
    SPAKE2PlusError,
    SPAKE2PlusServer,
    Verifier,
)
from mash_py.wire.framing import (
    DEFAULT_MAX_MESSAGE_SIZE,
    AsyncFrameReader,
    AsyncFrameWriter,
    FramingError,
)

_DEFAULT_PHASE_TIMEOUT_S: Final[float] = 30.0
"""§3.2 PASE_WAIT_X / PASE_WAIT_V budget."""

_DEFAULT_ERROR_DELAY_FLOOR_S: Final[float] = 0.2
"""§6.5 / Issue #14 timing-oracle floor; matches mash-go's
``minHandshakeDuration``."""

_ZERO_CONFIRMATION: Final[bytes] = bytes(CONFIRM_MAC_SIZE)
"""Zero-padded 32 bytes for the failure-path ``PASEComplete.confirmation``
field. Matches mash-go's wire shape."""


# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------


class PASEServerError(Exception):
    """Base class for device-side PASE handshake failures."""


class PASEProtocolError(PASEServerError):
    """The peer sent an envelope or sequence the spec does not allow.

    Examples:

    - The first inbound frame was not a ``PASERequest``.
    - A ``PASEConfirm`` was expected but a different message arrived.
    - Truncated frame, malformed CBOR, unknown ``msgType``.

    Distinguished from :class:`PASEAuthFailedError` because the latter
    represents a successfully-formatted attempt that failed
    cryptographic verification (i.e. wrong setup code) — the wire
    interaction completed, just not in the controller's favour.
    """


class PASEAuthFailedError(PASEServerError):
    """A spec-defined authentication-failure path triggered.

    Raised after the device has already emitted
    ``PASEComplete{ErrCodeAuthFailed}`` on the wire. The connection is
    expected to close immediately; the listener should return to
    ADVERTISING per §6.1.

    The ``reason`` argument is for logging and metrics only — it MUST
    NOT influence what is sent on the wire (DEC-047: peers MUST NOT
    learn which check failed).
    """

    def __init__(self, reason: str) -> None:
        super().__init__(f"PASE authentication failed: {reason}")
        self.reason = reason


class PASEPhaseTimeoutError(PASEServerError):
    """The peer did not send the expected next message within the
    per-phase timeout (§3.2 PASE_WAIT_X / PASE_WAIT_V = 30 s).

    The server does not emit an error envelope on phase timeout — the
    spec's prescription is "→ ADVERTISING" with no explicit message.
    The caller should close the connection.
    """


# ---------------------------------------------------------------------------
# State machine
# ---------------------------------------------------------------------------


class PASEServer:
    """Device-side driver for a single PASE handshake.

    Single-use: one instance handles one attempt. A new attempt requires
    a new ``PASEServer`` instance (and typically a new TLS connection,
    because the device closes the socket on auth failure per §6.1).

    Args:
        verifier: Persisted device verifier from factory provisioning
            (DEC-084 §2.3). Holds ``(salt, w0, L, iterations,
            discriminator)``.
        phase_timeout_s: Per-message read timeout in seconds (§3.2
            PASE_WAIT_X / PASE_WAIT_V). Default 30 s.
        error_delay_floor_s: Minimum wall-clock duration for any error
            response, measured from handshake start (§6.5 / Issue #14).
            Default 0.2 s. Set to 0 to skip the floor (test suites
            only); raise to ≥ §5.3 SPAKE2+ compute upper bound on
            constrained devices.
        max_message_size: Framing-layer cap; default
            ``DEFAULT_MAX_MESSAGE_SIZE`` (8 KB per DEC-069).
        y: Optional fixed ephemeral scalar for vector-based tests.
            Production code MUST NOT supply this (a fresh CSPRNG draw
            is the spec requirement).
    """

    DEFAULT_MAX_MESSAGE_SIZE: Final[int] = DEFAULT_MAX_MESSAGE_SIZE
    DEFAULT_PHASE_TIMEOUT_S: Final[float] = _DEFAULT_PHASE_TIMEOUT_S
    DEFAULT_ERROR_DELAY_FLOOR_S: Final[float] = _DEFAULT_ERROR_DELAY_FLOOR_S

    def __init__(
        self,
        verifier: Verifier,
        *,
        phase_timeout_s: float = _DEFAULT_PHASE_TIMEOUT_S,
        error_delay_floor_s: float = _DEFAULT_ERROR_DELAY_FLOOR_S,
        max_message_size: int = DEFAULT_MAX_MESSAGE_SIZE,
        y: int | None = None,
    ) -> None:
        if phase_timeout_s <= 0:
            raise ValueError("phase_timeout_s must be positive")
        if error_delay_floor_s < 0:
            raise ValueError("error_delay_floor_s must be non-negative")
        self._spake = SPAKE2PlusServer(verifier, y=y)
        self._phase_timeout_s = phase_timeout_s
        self._error_delay_floor_s = error_delay_floor_s
        self._max_message_size = max_message_size
        self._completed = False
        self._handshake_start: float | None = None

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def read_request(
        self,
        reader: asyncio.StreamReader,
    ) -> PASERequest:
        """Read just the first frame and return it as a :class:`PASERequest`.

        Splits step 1 of :meth:`handshake` so callers can inspect the
        request *before* spending CPU on SPAKE2+ math — for example to
        apply DEC-047 backoff delays or DEC-063 busy-rejections after the
        peer has demonstrated intent (sent a PASERequest) but before any
        secret material is processed. Mirrors mash-go's
        ``WaitForPASERequest`` + ``CompleteHandshake`` split.

        The handshake-start clock starts ticking on entry (so the §6.5
        timing-oracle floor still measures from "first byte processed"
        when the handler later calls :meth:`handshake_with_request`).

        Raises:
            PASEProtocolError: same conditions as :meth:`handshake`
                (truncated frame, malformed CBOR, wrong message type).
        """
        frame_reader = AsyncFrameReader(reader, max_message_size=self._max_message_size)
        if self._handshake_start is None:
            self._handshake_start = time.monotonic()
        return await self._recv_pase_request(frame_reader)

    async def handshake(
        self,
        reader: asyncio.StreamReader,
        writer: asyncio.StreamWriter,
        request: PASERequest | None = None,
    ) -> bytes:
        """Run the four-message PASE exchange, return the derived ``K_main``.

        On success, returns the 32-byte ``K_main``. On any failure path
        the matching error envelope (or none, for phase timeout) has
        already been written to the wire by the time the exception is
        raised; the caller's job is to close the TLS connection and
        return to ADVERTISING.

        ``request`` may be supplied by callers that have already drained
        the first frame via :meth:`read_request` — that path skips the
        initial read but reuses the same handshake-start clock.

        Raises:
            PASEProtocolError: peer sent a frame the spec does not
                allow in the current slot, or the underlying transport
                failed (truncated frame, EOF, etc.).
            PASEAuthFailedError: a spec-defined auth-failure path
                triggered (wrong identity, invalid pA, MAC mismatch).
                ``PASEComplete{ErrCodeAuthFailed}`` has been sent.
            PASEPhaseTimeoutError: the peer did not send the next
                message within ``phase_timeout_s``.
        """
        frame_reader = AsyncFrameReader(reader, max_message_size=self._max_message_size)
        frame_writer = AsyncFrameWriter(writer, max_message_size=self._max_message_size)
        if self._handshake_start is None:
            self._handshake_start = time.monotonic()

        # Step 1: read PASERequest with the controller's pA + ClientIdentity
        # (skipped if the caller already drained the first frame via
        # :meth:`read_request`).
        if request is None:
            request = await self._recv_pase_request(frame_reader)

        # §4.2 / DEC-019 reservation: ClientIdentity MUST equal
        # b"mash-controller" in v1. Rejection emits PASEComplete (NOT
        # CommissioningError) per §2.5 envelope-uniformity rule —
        # CommissioningError is reserved for pre-PASE cross-cutting
        # rejections (DEC-063 busy, etc.). See module docstring.
        if request.client_identity != CONTROLLER_IDENTITY:
            await self._fail_auth(
                frame_writer, writer, reason="client identity rejected"
            )

        # Step 2: process pA → derive K_shared, K_main, K_confirm, K_encrypt.
        # §2.5 point validation runs inside SPAKE2PlusServer; any failure
        # collapses to ErrCodeAuthFailed without revealing which check
        # tripped (DEC-047).
        try:
            self._spake.process_client_value(request.public_value)
        except SPAKE2PlusError as exc:
            await self._fail_auth(
                frame_writer, writer, reason=f"invalid PASERequest pA: {exc}"
            )

        # Step 3: send PASEResponse with our pB.
        response = PASEResponse(public_value=self._spake.public_value())
        await self._send(frame_writer, writer, encode(response))

        # Step 4: read PASEConfirm (subject to phase timeout — §3.2
        # PASE_WAIT_V).
        confirm = await self._recv_pase_confirm(frame_reader)

        # Step 5: verify confirmA. On mismatch, emit
        # PASEComplete{ErrCodeAuthFailed} with zero-padded confirmation
        # (the spec's PASEComplete schema has confirmation as bytes; on
        # failure the device's own confirmB is computed but the device
        # MUST NOT leak it — sending zeros prevents giving the attacker
        # a valid MAC for a nearly-correct attempt).
        try:
            self._spake.verify_client_confirmation(confirm.confirmation)
        except ConfirmationFailedError as exc:
            await self._fail_auth(
                frame_writer, writer, reason=f"confirmA mismatch: {exc}"
            )

        # Step 6: send PASEComplete with our confirmB (success path).
        complete = PASEComplete(
            confirmation=self._spake.confirmation(),
            error_code=ERR_CODE_SUCCESS,
        )
        await self._send(frame_writer, writer, encode(complete))

        self._completed = True
        return self._spake.shared_secret

    @property
    def shared_secret(self) -> bytes:
        """K_main (32 bytes). Available only after a successful handshake."""
        if not self._completed:
            raise PASEServerError("handshake has not completed successfully")
        return self._spake.shared_secret

    @property
    def encryption_key(self) -> bytes:
        """K_encrypt (32 bytes). Available only after a successful handshake."""
        if not self._completed:
            raise PASEServerError("handshake has not completed successfully")
        return self._spake.encryption_key

    # ------------------------------------------------------------------
    # Internals
    # ------------------------------------------------------------------

    async def _recv_pase_request(
        self, frame_reader: AsyncFrameReader
    ) -> PASERequest:
        """Read the first frame; require it to be a ``PASERequest``.

        The first frame is not subject to ``phase_timeout_s`` — the
        listener has already accepted the TLS connection and the spec's
        PASE_WAIT_X timer starts the moment the inbound TLS handshake
        completes. Higher layers can wrap this call in their own timer
        if needed; bundling the timeout here would double-budget.
        """
        msg = await self._recv_message(frame_reader, phase_timeout=False)
        if not isinstance(msg, PASERequest):
            raise PASEProtocolError(
                f"expected PASERequest, got {type(msg).__name__}"
            )
        return msg

    async def _recv_pase_confirm(
        self, frame_reader: AsyncFrameReader
    ) -> PASEConfirm:
        """Read the third frame under §3.2 PASE_WAIT_V (30 s)."""
        msg = await self._recv_message(frame_reader, phase_timeout=True)
        if not isinstance(msg, PASEConfirm):
            raise PASEProtocolError(
                f"expected PASEConfirm, got {type(msg).__name__}"
            )
        return msg

    async def _recv_message(
        self,
        frame_reader: AsyncFrameReader,
        *,
        phase_timeout: bool,
    ) -> object:
        """Read one framed PASE message. Surfaces wire-level errors as
        :class:`PASEProtocolError` and timeouts as
        :class:`PASEPhaseTimeoutError`.

        The return type is intentionally ``object`` (not the
        :data:`PASEMessage` union) — callers re-narrow with ``isinstance``
        because each call site expects a specific message type and
        raising on type mismatch is part of the wire-format contract.
        """
        try:
            if phase_timeout:
                payload = await asyncio.wait_for(
                    frame_reader.read_frame(), timeout=self._phase_timeout_s
                )
            else:
                payload = await frame_reader.read_frame()
        except TimeoutError as exc:
            raise PASEPhaseTimeoutError(
                f"timed out after {self._phase_timeout_s}s waiting for next PASE message"
            ) from exc
        except (
            FramingError,
            asyncio.IncompleteReadError,
            ConnectionError,
            OSError,
        ) as exc:
            raise PASEProtocolError(f"failed to read PASE frame: {exc}") from exc
        try:
            return decode(payload)
        except InvalidMessageError as exc:
            raise PASEProtocolError(f"malformed PASE message: {exc}") from exc

    async def _fail_auth(
        self,
        frame_writer: AsyncFrameWriter,
        writer: asyncio.StreamWriter,
        *,
        reason: str,
    ) -> None:
        """Apply the §6.5 timing floor, send PASEComplete{ErrCodeAuthFailed}
        with zero-padded confirmation, then raise :class:`PASEAuthFailedError`.

        Padding happens BEFORE sending so the peer is held in a read on
        the wire for the full floor duration — the same shape mash-go
        uses (session.go:200-216). Padding after sending would let the
        peer measure a fast wire response and infer that the server
        bailed early.
        """
        await self._apply_error_delay_floor()
        complete = PASEComplete(
            confirmation=_ZERO_CONFIRMATION,
            error_code=ERR_CODE_AUTH_FAILED,
        )
        # Best-effort: a writer error here doesn't change the auth
        # outcome (peer hung up before we acked the failure). The
        # logical outcome — auth failed — is what we surface.
        with contextlib.suppress(PASEProtocolError):
            await self._send(frame_writer, writer, encode(complete))
        raise PASEAuthFailedError(reason)

    async def _apply_error_delay_floor(self) -> None:
        """Sleep until ``error_delay_floor_s`` has elapsed since handshake start.

        See module docstring + Issue #14 for why a constant-time floor
        is preferred over the spec-text random 100-500 ms.
        """
        if self._error_delay_floor_s <= 0 or self._handshake_start is None:
            return
        elapsed = time.monotonic() - self._handshake_start
        remaining = self._error_delay_floor_s - elapsed
        if remaining > 0:
            await asyncio.sleep(remaining)

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


__all__ = [
    "PASEAuthFailedError",
    "PASEPhaseTimeoutError",
    "PASEProtocolError",
    "PASEServer",
    "PASEServerError",
]
