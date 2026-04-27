"""Unit tests for ``mash_py.commissioning.pase_server`` (Phase 14).

Round-trip: a Python :class:`PASEServer` accepts a handshake driven by
the existing :class:`PASEClient`, and both sides converge on the same
``K_main`` / ``K_encrypt``. Negative cases exercise the spec-strict
failure paths the eventual interop test against ``mash-controller``
will rely on.

The harness uses an in-memory pipe pair (the same pattern as
``test_cert_exchange.py``) so tests run without a TCP socket. Server
and client run as concurrent asyncio tasks; assertions are made on the
return values and on what reached the wire.

Spec references woven through the tests:

- §2.5 — point validation; spec mandates ``ErrCodeAuthFailed`` (1) on
  every in-PASE failure.
- §3.2 / §5.2 — PASE_WAIT_X / PASE_WAIT_V phase timeouts (30 s).
- §4.2 — message wire formats and the ``ClientIdentity`` constraint.
- §6.5 / Issue #14 — error-response timing floor.
"""

from __future__ import annotations

import asyncio
import contextlib
import time

import pytest

from mash_py.commissioning import (
    CONTROLLER_IDENTITY,
    ERR_CODE_AUTH_FAILED,
    PASEClient,
    PASEComplete,
    PASEConfirm,
    PASERequest,
    PASEResponse,
    PASEServer,
    PASEServerAuthFailedError,
    PASEServerError,
    decode_message,
    encode_message,
    generate_verifier,
)
from mash_py.commissioning.pase_server import PASEPhaseTimeoutError
from mash_py.commissioning.pase_server import PASEProtocolError as ServerProtocolError
from mash_py.commissioning.spake2plus import Verifier
from mash_py.wire.framing import AsyncFrameReader, AsyncFrameWriter

# Match the fast-iteration count used in test_spake2plus.py so the suite
# stays fast (PBKDF2 100k would add ~3s per test).
FAST_ITERS = 1000

# Floor of zero in unit tests so the suite isn't quantised to 200 ms per
# error path. Issue #14 timing is exercised with an explicit non-zero
# floor in `test_error_delay_floor_*` below.
NO_DELAY_FLOOR = 0.0


# ---------------------------------------------------------------------------
# Harness helpers
# ---------------------------------------------------------------------------


def _make_pipe_pair() -> tuple[
    tuple[asyncio.StreamReader, asyncio.StreamWriter],
    tuple[asyncio.StreamReader, asyncio.StreamWriter],
]:
    """Build two `(reader, writer)` halves of an in-memory full-duplex
    pipe. Each side reads what the other writes. Mirrors the helper in
    ``test_cert_exchange.py``."""
    reader_a = asyncio.StreamReader()
    proto_a = asyncio.StreamReaderProtocol(reader_a)
    reader_b = asyncio.StreamReader()
    proto_b = asyncio.StreamReaderProtocol(reader_b)

    transport_a_to_b = _MemoryTransport(reader_b)
    transport_b_to_a = _MemoryTransport(reader_a)

    writer_a = asyncio.StreamWriter(
        transport_a_to_b, proto_a, reader_a, asyncio.get_event_loop()
    )
    writer_b = asyncio.StreamWriter(
        transport_b_to_a, proto_b, reader_b, asyncio.get_event_loop()
    )
    return (reader_a, writer_a), (reader_b, writer_b)


class _MemoryTransport(asyncio.WriteTransport):
    """Tiny in-memory transport that funnels writes into a peer's
    StreamReader."""

    def __init__(self, peer_reader: asyncio.StreamReader) -> None:
        super().__init__()
        self._peer_reader = peer_reader
        self._closed = False

    def write(self, data: bytes) -> None:
        if self._closed:
            raise ConnectionResetError("transport closed")
        self._peer_reader.feed_data(data)

    def close(self) -> None:
        if not self._closed:
            self._closed = True
            self._peer_reader.feed_eof()

    def is_closing(self) -> bool:
        return self._closed

    def get_extra_info(self, name: str, default=None):  # type: ignore[no-untyped-def]
        return default


def _build_test_verifier(
    setup_code: str = "20202021",
    discriminator: int = 3840,
) -> Verifier:
    return generate_verifier(setup_code, discriminator, iterations=FAST_ITERS)


# ---------------------------------------------------------------------------
# Round-trip — PASEClient ↔ PASEServer (the load-bearing happy path)
# ---------------------------------------------------------------------------


async def test_handshake_round_trip_returns_matching_keys() -> None:
    """Client and server converge on identical K_main and K_encrypt
    after a successful four-message exchange."""
    setup_code = "20202021"
    discriminator = 3840
    verifier = _build_test_verifier(setup_code, discriminator)

    (cli_r, cli_w), (srv_r, srv_w) = _make_pipe_pair()

    client = PASEClient(setup_code, discriminator, iterations=FAST_ITERS)
    server = PASEServer(verifier, error_delay_floor_s=NO_DELAY_FLOOR)

    cli_task = asyncio.create_task(client.handshake(cli_r, cli_w))
    srv_task = asyncio.create_task(server.handshake(srv_r, srv_w))

    cli_k_main, srv_k_main = await asyncio.gather(cli_task, srv_task)

    assert isinstance(cli_k_main, bytes) and len(cli_k_main) == 32
    assert cli_k_main == srv_k_main
    assert client.encryption_key == server.encryption_key
    # Different HKDF info strings — collision would be a chain bug.
    assert client.shared_secret != client.encryption_key


async def test_shared_secret_unavailable_until_completion() -> None:
    """``shared_secret`` / ``encryption_key`` raise before handshake completes."""
    server = PASEServer(_build_test_verifier(), error_delay_floor_s=NO_DELAY_FLOOR)
    with pytest.raises(PASEServerError):
        _ = server.shared_secret
    with pytest.raises(PASEServerError):
        _ = server.encryption_key


# ---------------------------------------------------------------------------
# Failure paths — all collapse to PASEComplete{ErrCodeAuthFailed} (DEC-047)
# ---------------------------------------------------------------------------


async def test_wrong_setup_code_emits_pase_complete_auth_failed() -> None:
    """A controller using the wrong setup code triggers the §2.5 MAC
    mismatch path. Server emits ``PASEComplete{ErrCodeAuthFailed}`` and
    raises :class:`PASEAuthFailedError`."""
    correct_code = "20202021"
    wrong_code = "11111111"
    discriminator = 3840
    verifier = _build_test_verifier(correct_code, discriminator)

    (cli_r, cli_w), (srv_r, srv_w) = _make_pipe_pair()

    bad_client = PASEClient(wrong_code, discriminator, iterations=FAST_ITERS)
    server = PASEServer(verifier, error_delay_floor_s=NO_DELAY_FLOOR)

    # Client raises *something* (its own auth failure error or a protocol
    # error wrapping the failure envelope); we only assert the server's
    # behaviour here.
    cli_task = asyncio.create_task(bad_client.handshake(cli_r, cli_w))
    srv_task = asyncio.create_task(server.handshake(srv_r, srv_w))

    with pytest.raises(PASEServerAuthFailedError) as excinfo:
        await srv_task
    assert "confirmA mismatch" in excinfo.value.reason

    # Drain the client task so pytest-asyncio doesn't warn about leaked
    # tasks. We don't care whether the client raised auth-failed (that's
    # Phase 8's contract, separately tested).
    with contextlib.suppress(Exception):
        await cli_task


async def test_invalid_pa_emits_pase_complete_auth_failed() -> None:
    """A malformed pA (off-curve point) triggers the §2.5 point-validation
    failure. Server emits ``PASEComplete{ErrCodeAuthFailed}`` without
    revealing which check failed (DEC-047)."""
    verifier = _build_test_verifier()
    (cli_r, cli_w), (srv_r, srv_w) = _make_pipe_pair()
    server = PASEServer(verifier, error_delay_floor_s=NO_DELAY_FLOOR)

    # Hand-craft a PASERequest with a 65-byte payload that's not on the
    # curve. The 0x04 prefix passes the cheap shape check; (1,1) is not
    # a valid P-256 point.
    bad_pa = b"\x04" + (1).to_bytes(32, "big") + (1).to_bytes(32, "big")
    bad_request = PASERequest(public_value=bad_pa)
    fw = AsyncFrameWriter(cli_w)
    fr = AsyncFrameReader(cli_r)

    srv_task = asyncio.create_task(server.handshake(srv_r, srv_w))

    await fw.write_frame(encode_message(bad_request))

    # Read the server's response from the client side; expect
    # PASEComplete{ErrCodeAuthFailed}.
    reply = decode_message(await fr.read_frame())
    assert isinstance(reply, PASEComplete)
    assert reply.error_code == ERR_CODE_AUTH_FAILED
    assert reply.confirmation == bytes(32)  # zero-padded on failure

    with pytest.raises(PASEServerAuthFailedError) as excinfo:
        await srv_task
    assert "invalid PASERequest pA" in excinfo.value.reason


async def test_wrong_client_identity_emits_pase_complete_auth_failed() -> None:
    """A PASERequest with a non-protocol-constant ClientIdentity is
    rejected per §4.2 / DEC-019 reservation. Failure envelope is
    ``PASEComplete{ErrCodeAuthFailed}`` — NOT ``CommissioningError``
    (Issue #6 behaviour, now spec-strict on both sides)."""
    setup_code = "20202021"
    discriminator = 3840
    verifier = _build_test_verifier(setup_code, discriminator)
    (cli_r, cli_w), (srv_r, srv_w) = _make_pipe_pair()
    server = PASEServer(verifier, error_delay_floor_s=NO_DELAY_FLOOR)

    # A real PASEClient won't send a wrong identity, so build the
    # request by hand. pA is computed from a real SPAKE2+ client so the
    # frame parses; the server should reject on the identity check
    # before touching pA.
    real_client = PASEClient(setup_code, discriminator, iterations=FAST_ITERS)
    bad_request = PASERequest(
        public_value=real_client._spake.public_value(),
        client_identity=b"definitely-not-mash-controller",
    )

    fw = AsyncFrameWriter(cli_w)
    fr = AsyncFrameReader(cli_r)

    srv_task = asyncio.create_task(server.handshake(srv_r, srv_w))
    await fw.write_frame(encode_message(bad_request))

    reply = decode_message(await fr.read_frame())
    assert isinstance(reply, PASEComplete)
    assert reply.error_code == ERR_CODE_AUTH_FAILED
    assert reply.confirmation == bytes(32)

    with pytest.raises(PASEServerAuthFailedError) as excinfo:
        await srv_task
    assert "client identity rejected" in excinfo.value.reason


async def test_first_frame_must_be_pase_request() -> None:
    """If the first inbound frame isn't a ``PASERequest``, the server
    raises :class:`PASEProtocolError` without emitting any envelope —
    we never authenticated, so there's no auth-failure to report."""
    verifier = _build_test_verifier()
    (_cli_r, cli_w), (srv_r, srv_w) = _make_pipe_pair()
    server = PASEServer(verifier, error_delay_floor_s=NO_DELAY_FLOOR)

    # Send a PASEConfirm (which is the C→D MsgType 3) where a PASERequest
    # is expected. Wire-valid CBOR but spec-wrong slot.
    bad_first = PASEConfirm(confirmation=bytes(32))
    fw = AsyncFrameWriter(cli_w)

    srv_task = asyncio.create_task(server.handshake(srv_r, srv_w))
    await fw.write_frame(encode_message(bad_first))

    with pytest.raises(ServerProtocolError, match="expected PASERequest"):
        await srv_task


async def test_phase_timeout_raises_without_envelope() -> None:
    """If PASE_WAIT_V (the wait for PASEConfirm) elapses, the server
    raises :class:`PASEPhaseTimeoutError` without emitting any envelope.

    Spec §3.2 prescribes "→ ADVERTISING" on phase timeout — no error
    message is sent.
    """
    verifier = _build_test_verifier()
    (cli_r, cli_w), (srv_r, srv_w) = _make_pipe_pair()

    # 100 ms timeout to keep the test fast.
    server = PASEServer(
        verifier,
        phase_timeout_s=0.1,
        error_delay_floor_s=NO_DELAY_FLOOR,
    )

    # Drive the client far enough to send PASERequest, then stop —
    # never deliver PASEConfirm.
    real_client = PASEClient("20202021", 3840, iterations=FAST_ITERS)
    fw = AsyncFrameWriter(cli_w)
    fr = AsyncFrameReader(cli_r)

    srv_task = asyncio.create_task(server.handshake(srv_r, srv_w))

    await fw.write_frame(
        encode_message(
            PASERequest(
                public_value=real_client._spake.public_value(),
                client_identity=CONTROLLER_IDENTITY,
            )
        )
    )

    # Drain the server's PASEResponse so the framer doesn't trip on
    # ordering; the server is now waiting on PASEConfirm with a 100 ms
    # budget.
    response = decode_message(await fr.read_frame())
    assert isinstance(response, PASEResponse)

    with pytest.raises(PASEPhaseTimeoutError):
        await srv_task


async def test_truncated_inbound_frame_is_protocol_error() -> None:
    """An EOF mid-handshake surfaces as :class:`PASEProtocolError`."""
    verifier = _build_test_verifier()
    (_cli_r, cli_w), (srv_r, srv_w) = _make_pipe_pair()
    server = PASEServer(verifier, error_delay_floor_s=NO_DELAY_FLOOR)

    srv_task = asyncio.create_task(server.handshake(srv_r, srv_w))
    # Close the client side immediately — server hits EOF reading the
    # first frame.
    cli_w.close()

    with pytest.raises(ServerProtocolError):
        await srv_task


# ---------------------------------------------------------------------------
# §6.5 / Issue #14 — error-response timing floor
# ---------------------------------------------------------------------------


async def test_error_delay_floor_holds_failure_path() -> None:
    """With a non-zero floor, an auth-failure path takes wall-clock time
    ≥ floor before the envelope reaches the wire.

    Verifies the constant-time-floor mitigation Issue #14 documents.
    """
    verifier = _build_test_verifier()
    (cli_r, cli_w), (srv_r, srv_w) = _make_pipe_pair()

    floor_s = 0.05  # 50 ms — enough margin to detect, fast enough not to slow CI.
    server = PASEServer(verifier, error_delay_floor_s=floor_s)

    # Wrong identity → fast bail (no SPAKE2+ math). Without the floor
    # this path would respond in microseconds.
    bad_request = PASERequest(
        public_value=b"\x04" + bytes(64),  # invalid pA, but we bail on identity first
        client_identity=b"wrong",
    )
    fw = AsyncFrameWriter(cli_w)
    fr = AsyncFrameReader(cli_r)

    srv_task = asyncio.create_task(server.handshake(srv_r, srv_w))

    t0 = time.monotonic()
    await fw.write_frame(encode_message(bad_request))
    reply = decode_message(await fr.read_frame())
    elapsed = time.monotonic() - t0

    assert isinstance(reply, PASEComplete)
    assert reply.error_code == ERR_CODE_AUTH_FAILED
    # Allow a small slop below the floor for monotonic-clock granularity
    # and event-loop scheduling jitter (~5 ms) before flagging.
    assert elapsed >= floor_s * 0.9, (
        f"error response landed in {elapsed*1000:.1f} ms, "
        f"expected ≥ {floor_s*1000:.1f} ms (Issue #14 floor)"
    )

    with pytest.raises(PASEServerAuthFailedError):
        await srv_task


async def test_error_delay_floor_zero_skips_padding() -> None:
    """With ``error_delay_floor_s=0`` the failure path returns
    immediately. The test suite uses this everywhere else; verify the
    knob actually works."""
    verifier = _build_test_verifier()
    (cli_r, cli_w), (srv_r, srv_w) = _make_pipe_pair()
    server = PASEServer(verifier, error_delay_floor_s=0.0)

    bad_request = PASERequest(
        public_value=b"\x04" + bytes(64),
        client_identity=b"wrong",
    )
    fw = AsyncFrameWriter(cli_w)
    fr = AsyncFrameReader(cli_r)

    srv_task = asyncio.create_task(server.handshake(srv_r, srv_w))

    t0 = time.monotonic()
    await fw.write_frame(encode_message(bad_request))
    _ = decode_message(await fr.read_frame())
    elapsed = time.monotonic() - t0

    # Comfortable upper bound for "no floor applied" — the actual work
    # is microseconds; 50 ms gives wide headroom for slow CI.
    assert elapsed < 0.05, (
        f"unfloored failure took {elapsed*1000:.1f} ms — floor leaked through"
    )

    with pytest.raises(PASEServerAuthFailedError):
        await srv_task


# ---------------------------------------------------------------------------
# Constructor validation
# ---------------------------------------------------------------------------


def test_constructor_rejects_non_positive_phase_timeout() -> None:
    verifier = _build_test_verifier()
    with pytest.raises(ValueError, match="phase_timeout_s"):
        PASEServer(verifier, phase_timeout_s=0)
    with pytest.raises(ValueError, match="phase_timeout_s"):
        PASEServer(verifier, phase_timeout_s=-1)


def test_constructor_rejects_negative_error_delay_floor() -> None:
    verifier = _build_test_verifier()
    with pytest.raises(ValueError, match="error_delay_floor_s"):
        PASEServer(verifier, error_delay_floor_s=-0.1)
