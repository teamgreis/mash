"""Unit tests for ``mash_py.commissioning.messages`` and ``pase_client``.

Spec source: ``docs/testing/behavior/commissioning-pase.md §4.2`` (wire
format) and ``§2.5`` (failure-envelope rules — see also
``mash-py-spec-issues.md`` Issue #6).

Two test surfaces:

1. **Encode/decode round-trip per MsgType.** Drives every typed dataclass
   through ``encode → decode`` and asserts byte sizes, key positions, and
   field values. Vector-style assertions on the raw CBOR bytes catch
   accidental drift in the canonical encoding.

2. **PASEClient state machine.** End-to-end runs of the four-message
   exchange across a pair of ``asyncio.StreamReader/Writer`` pipes — one
   side runs ``PASEClient.handshake``, the other side runs a stub
   "device" (``SPAKE2PlusServer`` + hand-rolled framer) so we don't
   need a full Phase 14 device implementation. The stub is only as
   complete as Phase 8 needs: success path, MAC mismatch, busy
   rejection, Issue #6 envelope mis-use.
"""

from __future__ import annotations

import asyncio

import cbor2
import pytest

from mash_py.commissioning import (
    CONTROLLER_IDENTITY,
    DEVICE_IDENTITY,
    ERR_CODE_AUTH_FAILED,
    ERR_CODE_BUSY,
    ERR_CODE_SUCCESS,
    MSG_COMMISSIONING_ERROR,
    MSG_PASE_COMPLETE,
    MSG_PASE_CONFIRM,
    MSG_PASE_REQUEST,
    MSG_PASE_RESPONSE,
    POINT_SIZE,
    CommissioningError,
    InvalidMessageError,
    PASEAuthFailedError,
    PASEBusyError,
    PASEClient,
    PASEComplete,
    PASEConfirm,
    PASEProtocolError,
    PASERequest,
    PASEResponse,
    SPAKE2PlusServer,
    decode_message,
    encode_message,
    generate_verifier,
    peek_msg_type,
)
from mash_py.commissioning.messages import CONFIRM_MAC_SIZE
from mash_py.wire.framing import AsyncFrameReader, AsyncFrameWriter

# A 4-iteration count is fast enough for unit tests while keeping the
# code path identical. Production / interop uses MHF_ITERATIONS=100_000.
TEST_ITERATIONS = 4
TEST_SETUP_CODE = "20202021"
TEST_DISCRIMINATOR = 1234


# ---------------------------------------------------------------------------
# Encode / decode tests
# ---------------------------------------------------------------------------


def _stub_pA() -> bytes:
    """65-byte SEC1-uncompressed point. Not on the curve — the codec only
    enforces *length*, not curve membership; algorithmic validation lives
    in :func:`spake2plus.validate_point`."""
    return b"\x04" + b"\x01" * 32 + b"\x02" * 32


def _stub_mac() -> bytes:
    return b"\xab" * CONFIRM_MAC_SIZE


class TestEncodeDecode:
    def test_pase_request_round_trip(self) -> None:
        msg = PASERequest(public_value=_stub_pA())
        data = encode_message(msg)
        # Verify the wire layout matches spec §4.2 — keys 1, 2, 3 in
        # canonical order, msgType field == 1.
        decoded = cbor2.loads(data)
        assert decoded == {1: MSG_PASE_REQUEST, 2: _stub_pA(), 3: CONTROLLER_IDENTITY}

        round_tripped = decode_message(data)
        assert isinstance(round_tripped, PASERequest)
        assert round_tripped.public_value == msg.public_value
        assert round_tripped.client_identity == CONTROLLER_IDENTITY

    def test_pase_request_rejects_wrong_pa_length(self) -> None:
        msg = PASERequest(public_value=b"\x04" + b"\x00" * 31)  # 32 bytes total
        with pytest.raises(InvalidMessageError, match="must be 65 bytes"):
            encode_message(msg)

    def test_pase_response_round_trip(self) -> None:
        msg = PASEResponse(public_value=_stub_pA())
        data = encode_message(msg)
        decoded = cbor2.loads(data)
        assert decoded == {1: MSG_PASE_RESPONSE, 2: _stub_pA()}

        round_tripped = decode_message(data)
        assert isinstance(round_tripped, PASEResponse)
        assert round_tripped.public_value == msg.public_value

    def test_pase_confirm_round_trip(self) -> None:
        msg = PASEConfirm(confirmation=_stub_mac())
        data = encode_message(msg)
        decoded = cbor2.loads(data)
        assert decoded == {1: MSG_PASE_CONFIRM, 2: _stub_mac()}

        round_tripped = decode_message(data)
        assert isinstance(round_tripped, PASEConfirm)
        assert round_tripped.confirmation == msg.confirmation

    def test_pase_complete_success_round_trip(self) -> None:
        msg = PASEComplete(confirmation=_stub_mac(), error_code=ERR_CODE_SUCCESS)
        data = encode_message(msg)
        decoded = cbor2.loads(data)
        assert decoded == {1: MSG_PASE_COMPLETE, 2: _stub_mac(), 3: 0}

        round_tripped = decode_message(data)
        assert isinstance(round_tripped, PASEComplete)
        assert round_tripped.confirmation == msg.confirmation
        assert round_tripped.error_code == ERR_CODE_SUCCESS

    def test_pase_complete_failure_round_trip(self) -> None:
        # On the failure path the spec doesn't require confirmation to be
        # 32 bytes — encode / decode tolerate any byte string. (The state
        # machine never reads the field on failure anyway.)
        msg = PASEComplete(confirmation=b"\x00" * 4, error_code=ERR_CODE_AUTH_FAILED)
        data = encode_message(msg)
        round_tripped = decode_message(data)
        assert isinstance(round_tripped, PASEComplete)
        assert round_tripped.error_code == ERR_CODE_AUTH_FAILED
        assert round_tripped.confirmation == b"\x00" * 4

    def test_pase_complete_decode_rejects_bad_confirmation_on_success(self) -> None:
        # Construct a malformed PASEComplete by hand (encode would
        # validate). On the success path the controller MUST refuse a
        # short confirmation field — the MAC verifier would otherwise
        # silently fail.
        bad = cbor2.dumps({1: MSG_PASE_COMPLETE, 2: b"\xff" * 16, 3: 0}, canonical=True)
        with pytest.raises(InvalidMessageError, match="must be 32 bytes on success"):
            decode_message(bad)

    def test_commissioning_error_round_trip(self) -> None:
        msg = CommissioningError(
            error_code=ERR_CODE_BUSY,
            message="device busy",
            retry_after_ms=2500,
        )
        data = encode_message(msg)
        decoded = cbor2.loads(data)
        assert decoded == {
            1: MSG_COMMISSIONING_ERROR,
            2: ERR_CODE_BUSY,
            3: "device busy",
            4: 2500,
        }
        round_tripped = decode_message(data)
        assert isinstance(round_tripped, CommissioningError)
        assert round_tripped.error_code == ERR_CODE_BUSY
        assert round_tripped.message == "device busy"
        assert round_tripped.retry_after_ms == 2500

    def test_commissioning_error_omits_empty_message(self) -> None:
        # Mirrors mash-go's ``cbor:",omitempty"`` on the Message field —
        # the canonical bytes must NOT include key 3 when message == "".
        msg = CommissioningError(error_code=ERR_CODE_BUSY)
        decoded = cbor2.loads(encode_message(msg))
        assert 3 not in decoded
        assert decoded[1] == MSG_COMMISSIONING_ERROR
        assert decoded[2] == ERR_CODE_BUSY
        assert decoded[4] == 0

    def test_decode_rejects_unknown_msg_type(self) -> None:
        bad = cbor2.dumps({1: 42, 2: b"x"}, canonical=True)
        with pytest.raises(InvalidMessageError, match="unsupported PASE message type"):
            decode_message(bad)

    def test_decode_rejects_non_map(self) -> None:
        bad = cbor2.dumps([1, 2, 3], canonical=True)
        with pytest.raises(InvalidMessageError, match="must be a CBOR map"):
            decode_message(bad)

    def test_decode_rejects_missing_msg_type(self) -> None:
        bad = cbor2.dumps({2: b"x"}, canonical=True)
        with pytest.raises(InvalidMessageError, match="missing or non-integer msgType"):
            decode_message(bad)

    def test_peek_msg_type(self) -> None:
        data = encode_message(PASEResponse(public_value=_stub_pA()))
        assert peek_msg_type(data) == MSG_PASE_RESPONSE

    def test_peek_msg_type_rejects_garbage(self) -> None:
        with pytest.raises(InvalidMessageError):
            peek_msg_type(b"\xff\xff\xff\xff")


# ---------------------------------------------------------------------------
# State-machine tests (PASEClient driven across an asyncio pipe)
# ---------------------------------------------------------------------------


# Fixed verifier so client and server agree on (salt, w0, L). Generated
# once per session because PBKDF2(4) is still ~50 ms and we run it across
# many tests.
_VERIFIER = generate_verifier(TEST_SETUP_CODE, TEST_DISCRIMINATOR, iterations=TEST_ITERATIONS)


async def _make_pipe() -> tuple[
    asyncio.StreamReader,
    asyncio.StreamWriter,
    asyncio.StreamReader,
    asyncio.StreamWriter,
]:
    """Build two paired (reader, writer) ends backed by ``asyncio`` plumbing.

    Used to drive the PASE client's framed exchange in-process. Returns
    ``(client_reader, client_writer, server_reader, server_writer)``.
    """
    # We back each direction with a real socket pair so ``drain()`` and
    # ``readexactly()`` exercise the same code paths as a TLS connection
    # would. ``asyncio.open_connection`` over a localhost socket is the
    # cheapest way to get true StreamReader/StreamWriter pairs.
    server_started = asyncio.Event()
    captured: dict[str, tuple[asyncio.StreamReader, asyncio.StreamWriter]] = {}

    async def _on_connect(
        reader: asyncio.StreamReader, writer: asyncio.StreamWriter
    ) -> None:
        captured["server"] = (reader, writer)
        server_started.set()
        # Block forever — the test will close both sides explicitly.
        await asyncio.Event().wait()

    server = await asyncio.start_server(_on_connect, host="127.0.0.1", port=0)
    addr = server.sockets[0].getsockname()
    client_reader, client_writer = await asyncio.open_connection(*addr[:2])
    await server_started.wait()
    server_reader, server_writer = captured["server"]
    server.close()
    # Don't await server.wait_closed() — that would also close the
    # accepted socket on some platforms. The listening socket is what we
    # want to release; the accepted streams stay open.
    return client_reader, client_writer, server_reader, server_writer


async def _close_pipe(*streams: asyncio.StreamWriter) -> None:
    for w in streams:
        w.close()
        try:
            await w.wait_closed()
        except (ConnectionError, OSError):
            pass


async def _read_decoded(reader: asyncio.StreamReader) -> object:
    """Tiny test-side helper: framed read + PASE decode."""
    payload = await AsyncFrameReader(reader).read_frame()
    return decode_message(payload)


async def _write_encoded(writer: asyncio.StreamWriter, msg: object) -> None:
    await AsyncFrameWriter(writer).write_frame(encode_message(msg))  # type: ignore[arg-type]
    await writer.drain()


class TestHandshakeSuccess:
    async def test_full_handshake(self) -> None:
        """Happy path: client and server complete PASE; both derive the
        same K_main."""
        cr, cw, sr, sw = await _make_pipe()
        try:
            client = PASEClient(
                TEST_SETUP_CODE,
                TEST_DISCRIMINATOR,
                iterations=TEST_ITERATIONS,
            )
            server = SPAKE2PlusServer(_VERIFIER)

            async def run_server() -> bytes:
                # Step 1: receive PASERequest
                req = await _read_decoded(sr)
                assert isinstance(req, PASERequest)
                assert req.client_identity == CONTROLLER_IDENTITY

                # Step 2: process pA, send PASEResponse with pB
                server.process_client_value(req.public_value)
                await _write_encoded(sw, PASEResponse(public_value=server.public_value()))

                # Step 3: receive PASEConfirm, verify, send PASEComplete
                confirm = await _read_decoded(sr)
                assert isinstance(confirm, PASEConfirm)
                server.verify_client_confirmation(confirm.confirmation)

                await _write_encoded(
                    sw,
                    PASEComplete(
                        confirmation=server.confirmation(),
                        error_code=ERR_CODE_SUCCESS,
                    ),
                )
                return server.shared_secret

            client_task = asyncio.create_task(client.handshake(cr, cw))
            server_task = asyncio.create_task(run_server())
            client_secret, server_secret = await asyncio.gather(client_task, server_task)

            assert client_secret == server_secret
            assert len(client_secret) == 32
            assert client.encryption_key == server.encryption_key
            assert len(client.encryption_key) == 32
        finally:
            await _close_pipe(cw, sw)

    async def test_first_message_carries_controller_identity(self) -> None:
        """DEC-084 §4.2 wire check: PASERequest.clientIdentity == b"mash-controller"."""
        cr, cw, sr, sw = await _make_pipe()
        try:
            client = PASEClient(
                TEST_SETUP_CODE,
                TEST_DISCRIMINATOR,
                iterations=TEST_ITERATIONS,
            )
            client_task = asyncio.create_task(client.handshake(cr, cw))
            req = await _read_decoded(sr)
            assert isinstance(req, PASERequest)
            assert req.client_identity == CONTROLLER_IDENTITY
            assert req.client_identity != DEVICE_IDENTITY
        finally:
            client_task.cancel()
            await asyncio.gather(client_task, return_exceptions=True)
            await _close_pipe(cw, sw)


class TestHandshakeFailures:
    async def test_pase_complete_with_auth_failed_raises(self) -> None:
        """When the device returns ``PASEComplete{error_code != 0}`` —
        spec-conformant failure envelope — the client raises
        :class:`PASEAuthFailedError`."""
        cr, cw, sr, sw = await _make_pipe()
        try:
            client = PASEClient(
                TEST_SETUP_CODE,
                TEST_DISCRIMINATOR,
                iterations=TEST_ITERATIONS,
            )
            server = SPAKE2PlusServer(_VERIFIER)

            async def run_server() -> None:
                req = await _read_decoded(sr)
                assert isinstance(req, PASERequest)
                server.process_client_value(req.public_value)
                await _write_encoded(sw, PASEResponse(public_value=server.public_value()))
                await _read_decoded(sr)  # discard PASEConfirm
                # Spec §2.5: device sends PASEComplete with auth-failed
                # code on any in-PASE failure. Confirmation field can be
                # zero-padded on failure.
                await _write_encoded(
                    sw,
                    PASEComplete(
                        confirmation=b"\x00" * CONFIRM_MAC_SIZE,
                        error_code=ERR_CODE_AUTH_FAILED,
                    ),
                )

            client_task = asyncio.create_task(client.handshake(cr, cw))
            server_task = asyncio.create_task(run_server())

            with pytest.raises(PASEAuthFailedError) as excinfo:
                await client_task
            await server_task
            assert excinfo.value.error_code == ERR_CODE_AUTH_FAILED
        finally:
            await _close_pipe(cw, sw)

    async def test_dec063_busy_response_raises_busy_error(self) -> None:
        """DEC-063: a device that's already busy answers ``PASERequest``
        with ``CommissioningError{ErrCodeBusy, retryAfter}`` — the spec's
        legitimate use of the CommissioningError envelope. The client
        surfaces it as :class:`PASEBusyError`, distinct from auth failure."""
        cr, cw, sr, sw = await _make_pipe()
        try:
            client = PASEClient(
                TEST_SETUP_CODE,
                TEST_DISCRIMINATOR,
                iterations=TEST_ITERATIONS,
            )

            async def run_server() -> None:
                await _read_decoded(sr)  # discard PASERequest
                await _write_encoded(
                    sw,
                    CommissioningError(
                        error_code=ERR_CODE_BUSY,
                        message="device busy",
                        retry_after_ms=1500,
                    ),
                )

            client_task = asyncio.create_task(client.handshake(cr, cw))
            server_task = asyncio.create_task(run_server())

            with pytest.raises(PASEBusyError) as excinfo:
                await client_task
            await server_task
            assert excinfo.value.retry_after_ms == 1500
            assert "busy" in excinfo.value.message
        finally:
            await _close_pipe(cw, sw)

    async def test_issue6_commissioning_error_in_pase_slot_is_protocol_error(
        self,
    ) -> None:
        """Reproduces mash-go Issue #6: device sends ``CommissioningError``
        with a non-busy code (e.g. ``ErrCodeAuthFailed``) where the spec
        mandates ``PASEComplete``. mash-py refuses to silently absorb
        the wrong envelope — surfaces a protocol error so the operator
        notices the spec violation."""
        cr, cw, sr, sw = await _make_pipe()
        try:
            client = PASEClient(
                TEST_SETUP_CODE,
                TEST_DISCRIMINATOR,
                iterations=TEST_ITERATIONS,
            )

            async def run_server() -> None:
                await _read_decoded(sr)  # discard PASERequest
                # Issue #6 reproducer: send CommissioningError with
                # AuthFailed instead of PASEComplete.
                await _write_encoded(
                    sw,
                    CommissioningError(error_code=ERR_CODE_AUTH_FAILED),
                )

            client_task = asyncio.create_task(client.handshake(cr, cw))
            server_task = asyncio.create_task(run_server())

            with pytest.raises(PASEProtocolError, match="Issue #6"):
                await client_task
            await server_task
        finally:
            await _close_pipe(cw, sw)

    async def test_issue6_commissioning_error_in_complete_slot_is_protocol_error(
        self,
    ) -> None:
        """Same Issue #6 surface, later in the flow: the device sends
        ``CommissioningError`` *after* PASEResponse, where the spec
        mandates ``PASEComplete``."""
        cr, cw, sr, sw = await _make_pipe()
        try:
            client = PASEClient(
                TEST_SETUP_CODE,
                TEST_DISCRIMINATOR,
                iterations=TEST_ITERATIONS,
            )
            server = SPAKE2PlusServer(_VERIFIER)

            async def run_server() -> None:
                req = await _read_decoded(sr)
                assert isinstance(req, PASERequest)
                server.process_client_value(req.public_value)
                await _write_encoded(sw, PASEResponse(public_value=server.public_value()))
                await _read_decoded(sr)  # discard PASEConfirm
                # Wrong envelope at the spec-mandated PASEComplete slot.
                await _write_encoded(
                    sw,
                    CommissioningError(error_code=ERR_CODE_AUTH_FAILED),
                )

            client_task = asyncio.create_task(client.handshake(cr, cw))
            server_task = asyncio.create_task(run_server())

            with pytest.raises(PASEProtocolError, match="expected PASEComplete"):
                await client_task
            await server_task
        finally:
            await _close_pipe(cw, sw)

    async def test_invalid_pb_raises_protocol_error(self) -> None:
        """A malformed pB (off-curve / wrong length) is a wire-level
        problem, not auth failure. The client maps it to
        :class:`PASEProtocolError`."""
        cr, cw, sr, sw = await _make_pipe()
        try:
            client = PASEClient(
                TEST_SETUP_CODE,
                TEST_DISCRIMINATOR,
                iterations=TEST_ITERATIONS,
            )

            async def run_server() -> None:
                await _read_decoded(sr)  # discard PASERequest
                # 65-byte SEC1-uncompressed but not on the curve.
                bogus = b"\x04" + b"\x00" * 32 + b"\x01" * 32
                await _write_encoded(sw, PASEResponse(public_value=bogus))

            client_task = asyncio.create_task(client.handshake(cr, cw))
            server_task = asyncio.create_task(run_server())

            with pytest.raises(PASEProtocolError, match="invalid PASEResponse"):
                await client_task
            await server_task
        finally:
            await _close_pipe(cw, sw)

    async def test_truncated_response_raises_protocol_error(self) -> None:
        """Connection close mid-flow surfaces as a protocol error."""
        cr, cw, sr, sw = await _make_pipe()
        try:
            client = PASEClient(
                TEST_SETUP_CODE,
                TEST_DISCRIMINATOR,
                iterations=TEST_ITERATIONS,
            )
            client_task = asyncio.create_task(client.handshake(cr, cw))
            await _read_decoded(sr)  # discard PASERequest
            sw.close()  # peer hangs up before sending PASEResponse
            with pytest.raises(PASEProtocolError):
                await client_task
        finally:
            await _close_pipe(cw)


class TestProperties:
    async def test_shared_secret_unavailable_before_handshake(self) -> None:
        client = PASEClient(
            TEST_SETUP_CODE,
            TEST_DISCRIMINATOR,
            iterations=TEST_ITERATIONS,
        )
        from mash_py.commissioning import PASEClientError

        with pytest.raises(PASEClientError):
            _ = client.shared_secret
        with pytest.raises(PASEClientError):
            _ = client.encryption_key

    def test_pa_size_constant_matches_spec(self) -> None:
        """Spec §4.2: pA / pB are exactly 65 bytes (SEC1 uncompressed).
        Guarding the constant against accidental drift."""
        assert POINT_SIZE == 65
