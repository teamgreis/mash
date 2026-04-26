"""Unit tests for mash_py.wire.framing.

Mirror the Go framing tests in mash-go/pkg/transport/framing_test.go.
Boundary acceptance/rejection (1, 8192, 0, 8193), truncated reads,
multi-frame streams, and EOF semantics.

Sync and async APIs share the same exception model, so the bulk of the
behavioural tests live on the sync side and the async tests just verify
that the same invariants hold over an asyncio StreamReader/StreamWriter.
"""

from __future__ import annotations

import asyncio
import io
import threading

import pytest

from mash_py.wire.framing import (
    DEFAULT_MAX_MESSAGE_SIZE,
    LENGTH_PREFIX_SIZE,
    AsyncFrameReader,
    AsyncFrameWriter,
    Framer,
    FrameReader,
    FrameTruncatedError,
    FrameWriter,
    MessageEmptyError,
    MessageTooLargeError,
    frame_size,
)

# ---------------------------------------------------------------------------
# Constants — assert the wire-visible numbers up front so a refactor that
# silently changes them blows up immediately.
# ---------------------------------------------------------------------------


class TestFramingConstants:
    def test_default_max_is_8k(self) -> None:
        # DEC-069: aligns with 256 KB MCU memory budget (DEC-003).
        assert DEFAULT_MAX_MESSAGE_SIZE == 8192

    def test_length_prefix_is_4_bytes(self) -> None:
        assert LENGTH_PREFIX_SIZE == 4

    def test_frame_size_helper(self) -> None:
        assert frame_size(0) == 4
        assert frame_size(100) == 104
        assert frame_size(DEFAULT_MAX_MESSAGE_SIZE) == 8196


# ---------------------------------------------------------------------------
# Sync round-trip — payload variants
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    ("name", "payload"),
    [
        ("small_text", b"hello"),
        ("medium", b"x" * 1000),
        ("max_size", b"y" * DEFAULT_MAX_MESSAGE_SIZE),
        ("single_byte", b"\x42"),
        ("binary_data", bytes([0x00, 0xFF, 0x7F, 0x80])),
    ],
)
def test_round_trip(name: str, payload: bytes) -> None:
    buf = io.BytesIO()
    FrameWriter(buf).write_frame(payload)

    # Wire format check: 4-byte length + payload
    assert buf.tell() == LENGTH_PREFIX_SIZE + len(payload)

    buf.seek(0)
    got = FrameReader(buf).read_frame()
    assert got == payload


# ---------------------------------------------------------------------------
# Sync boundary tests — these mirror Go's TestFraming_AcceptAt8192Bytes /
# TestFraming_RejectAt8193Bytes one-for-one.
# ---------------------------------------------------------------------------


def test_accept_at_8192_bytes() -> None:
    """Exactly-at-boundary writes succeed."""
    buf = io.BytesIO()
    FrameWriter(buf).write_frame(b"z" * 8192)
    # Implicit assertion: no exception raised.


def test_reject_at_8193_bytes_on_write() -> None:
    """One byte over boundary is rejected on write."""
    with pytest.raises(MessageTooLargeError) as exc_info:
        FrameWriter(io.BytesIO()).write_frame(b"z" * 8193)
    assert exc_info.value.size == 8193
    assert exc_info.value.maximum == 8192


def test_reject_at_8193_bytes_on_read() -> None:
    """A reader configured with the default cap rejects an 8193-byte frame."""
    # Hand-build the prefix so we don't have to bypass the writer's check.
    buf = io.BytesIO((8193).to_bytes(4, "big") + b"z" * 8193)
    with pytest.raises(MessageTooLargeError):
        FrameReader(buf).read_frame()


def test_reject_above_custom_max() -> None:
    """Custom max_message_size is honoured by both sides."""
    buf = io.BytesIO()
    with pytest.raises(MessageTooLargeError):
        FrameWriter(buf, max_message_size=100).write_frame(b"x" * 101)


def test_accept_minimum_size() -> None:
    """A 1-byte payload is the smallest valid frame."""
    buf = io.BytesIO()
    FrameWriter(buf).write_frame(b"\x00")
    buf.seek(0)
    assert FrameReader(buf).read_frame() == b"\x00"


# ---------------------------------------------------------------------------
# Empty message rejection — both write and read paths.
# ---------------------------------------------------------------------------


def test_write_rejects_empty_payload() -> None:
    with pytest.raises(MessageEmptyError):
        FrameWriter(io.BytesIO()).write_frame(b"")


def test_read_rejects_zero_length_prefix() -> None:
    """A length=0 prefix on the wire is rejected by the reader."""
    buf = io.BytesIO((0).to_bytes(4, "big"))
    with pytest.raises(MessageEmptyError):
        FrameReader(buf).read_frame()


# ---------------------------------------------------------------------------
# Truncation — partial length prefix and partial payload.
# ---------------------------------------------------------------------------


def test_truncated_length_prefix() -> None:
    """Two bytes of a 4-byte length prefix is a truncated frame."""
    buf = io.BytesIO(b"\x00\x01")
    with pytest.raises(FrameTruncatedError):
        FrameReader(buf).read_frame()


def test_truncated_payload() -> None:
    """A length prefix declaring 100 bytes followed by 50 is truncated."""
    buf = io.BytesIO((100).to_bytes(4, "big") + b"x" * 50)
    with pytest.raises(FrameTruncatedError):
        FrameReader(buf).read_frame()


def test_clean_eof_at_boundary_raises_eof_error() -> None:
    """Empty stream → EOFError. Distinguishes 'stream done' from 'frame torn'."""
    with pytest.raises(EOFError):
        FrameReader(io.BytesIO()).read_frame()


# ---------------------------------------------------------------------------
# Short-read handling — file-like .read(n) may legally return < n bytes.
# ---------------------------------------------------------------------------


class _ChunkingReader:
    """A reader that hands out at most ``chunk`` bytes per call.

    Models a slow pipe / socket where ``read(n)`` returns less than ``n``
    even though more data is available. The frame reader must loop to
    drain it.
    """

    def __init__(self, data: bytes, chunk: int = 1) -> None:
        self._buf = data
        self._chunk = chunk
        self._pos = 0

    def read(self, n: int = -1) -> bytes:
        if self._pos >= len(self._buf):
            return b""
        take = min(self._chunk, n if n > 0 else self._chunk)
        out = self._buf[self._pos : self._pos + take]
        self._pos += len(out)
        return out


def test_drains_short_reads() -> None:
    """A reader returning 1 byte at a time still yields a whole frame."""
    payload = b"hello world"
    wire = len(payload).to_bytes(4, "big") + payload
    reader = FrameReader(_ChunkingReader(wire, chunk=1))  # type: ignore[arg-type]
    assert reader.read_frame() == payload


# ---------------------------------------------------------------------------
# Multiple frames in a single stream.
# ---------------------------------------------------------------------------


def test_multiple_back_to_back_frames() -> None:
    buf = io.BytesIO()
    writer = FrameWriter(buf)
    messages = [b"first", b"second", b"third"]
    for m in messages:
        writer.write_frame(m)

    buf.seek(0)
    reader = FrameReader(buf)
    for m in messages:
        assert reader.read_frame() == m

    # And one more read drains to EOF — not a truncation.
    with pytest.raises(EOFError):
        reader.read_frame()


# ---------------------------------------------------------------------------
# Framer convenience class.
# ---------------------------------------------------------------------------


def test_framer_round_trip() -> None:
    buf = io.BytesIO()
    framer = Framer(buf)
    framer.write_frame(b"ping")
    buf.seek(0)
    assert framer.read_frame() == b"ping"


# ---------------------------------------------------------------------------
# Thread safety — concurrent writers must not interleave a frame.
# ---------------------------------------------------------------------------


def test_concurrent_writes_dont_interleave() -> None:
    """Two threads writing 4 KB frames concurrently must produce 2 readable
    frames, not one corrupted blob."""
    buf = io.BytesIO()
    writer = FrameWriter(buf)
    payload_a = b"A" * 4096
    payload_b = b"B" * 4096

    barrier = threading.Barrier(2)

    def writer_thread(payload: bytes) -> None:
        barrier.wait()  # maximise contention
        writer.write_frame(payload)

    t1 = threading.Thread(target=writer_thread, args=(payload_a,))
    t2 = threading.Thread(target=writer_thread, args=(payload_b,))
    t1.start()
    t2.start()
    t1.join()
    t2.join()

    buf.seek(0)
    reader = FrameReader(buf)
    got = sorted([reader.read_frame(), reader.read_frame()])
    assert got == sorted([payload_a, payload_b])


# ---------------------------------------------------------------------------
# Async API — same invariants over asyncio.StreamReader/Writer.
#
# StreamReader.feed_data() lets us drive the reader without a real socket;
# feed_eof() signals clean end-of-stream the same way a closed peer would.
# ---------------------------------------------------------------------------


async def _make_stream_reader(data: bytes, *, eof: bool = True) -> asyncio.StreamReader:
    reader = asyncio.StreamReader()
    reader.feed_data(data)
    if eof:
        reader.feed_eof()
    return reader


@pytest.mark.asyncio
async def test_async_read_frame() -> None:
    payload = b"hello async"
    wire = len(payload).to_bytes(4, "big") + payload
    sr = await _make_stream_reader(wire)
    assert await AsyncFrameReader(sr).read_frame() == payload


@pytest.mark.asyncio
async def test_async_read_multiple_frames() -> None:
    parts = [b"alpha", b"beta", b"gamma"]
    wire = b"".join(len(p).to_bytes(4, "big") + p for p in parts)
    sr = await _make_stream_reader(wire)
    reader = AsyncFrameReader(sr)
    for p in parts:
        assert await reader.read_frame() == p


@pytest.mark.asyncio
async def test_async_clean_eof_raises_incomplete_read() -> None:
    sr = await _make_stream_reader(b"")
    with pytest.raises(asyncio.IncompleteReadError) as exc_info:
        await AsyncFrameReader(sr).read_frame()
    assert exc_info.value.partial == b""


@pytest.mark.asyncio
async def test_async_truncated_prefix() -> None:
    sr = await _make_stream_reader(b"\x00\x01")
    with pytest.raises(FrameTruncatedError):
        await AsyncFrameReader(sr).read_frame()


@pytest.mark.asyncio
async def test_async_truncated_payload() -> None:
    sr = await _make_stream_reader((100).to_bytes(4, "big") + b"x" * 50)
    with pytest.raises(FrameTruncatedError):
        await AsyncFrameReader(sr).read_frame()


@pytest.mark.asyncio
async def test_async_zero_length_rejected() -> None:
    sr = await _make_stream_reader((0).to_bytes(4, "big"))
    with pytest.raises(MessageEmptyError):
        await AsyncFrameReader(sr).read_frame()


@pytest.mark.asyncio
async def test_async_oversize_rejected_on_read() -> None:
    sr = await _make_stream_reader((8193).to_bytes(4, "big") + b"z" * 8193)
    with pytest.raises(MessageTooLargeError):
        await AsyncFrameReader(sr).read_frame()


@pytest.mark.asyncio
async def test_async_writer_rejects_empty() -> None:
    # Spin up a transport that accepts writes without crashing.
    rsock, wsock = await _make_stream_pair()
    try:
        with pytest.raises(MessageEmptyError):
            await AsyncFrameWriter(wsock).write_frame(b"")
    finally:
        wsock.close()
        await wsock.wait_closed()
        del rsock


@pytest.mark.asyncio
async def test_async_writer_rejects_oversize() -> None:
    rsock, wsock = await _make_stream_pair()
    try:
        with pytest.raises(MessageTooLargeError):
            await AsyncFrameWriter(wsock).write_frame(b"x" * 8193)
    finally:
        wsock.close()
        await wsock.wait_closed()
        del rsock


@pytest.mark.asyncio
async def test_async_full_round_trip_loopback() -> None:
    """End-to-end: AsyncFrameWriter on one socket end, AsyncFrameReader on
    the other. Catches any subtle mismatch between the in-memory feed_data
    path and a real transport."""
    sender_reader, sender_writer = await _make_stream_pair()
    payload = b"round trip across sockets"
    await AsyncFrameWriter(sender_writer).write_frame(payload)
    await sender_writer.drain()

    got = await AsyncFrameReader(sender_reader).read_frame()
    assert got == payload

    sender_writer.close()
    await sender_writer.wait_closed()


async def _make_stream_pair() -> tuple[asyncio.StreamReader, asyncio.StreamWriter]:
    """Connect a (StreamReader, StreamWriter) pair over a localhost socket.

    The peer end echoes everything back, so a frame written to the writer
    can be read off the reader. Used for round-trip and writer-error
    tests where we need the writer to actually accept bytes.
    """
    server_reader: asyncio.StreamReader | None = None

    async def _echo(r: asyncio.StreamReader, w: asyncio.StreamWriter) -> None:
        nonlocal server_reader
        server_reader = r
        # Pipe everything received back to the client until EOF.
        while True:
            chunk = await r.read(4096)
            if not chunk:
                break
            w.write(chunk)
            await w.drain()
        w.close()

    server = await asyncio.start_server(_echo, "127.0.0.1", 0)
    sockets = server.sockets or ()
    if not sockets:
        raise RuntimeError("server has no listening sockets")
    host, port = sockets[0].getsockname()[:2]
    reader, writer = await asyncio.open_connection(host, port)
    return reader, writer


# ---------------------------------------------------------------------------
# Endianness — pure-Python regression net for the single most likely framing
# bug: a future contributor swaps ``'big'`` for ``'little'`` (or vice-versa)
# in FrameReader or FrameWriter. The cross-language vector tests would also
# catch this, but only when ``make gen-vectors`` has been freshly run; this
# test fires regardless of vector file freshness.
#
# We use length 256 so BE (``\x00\x00\x01\x00``) and LE (``\x00\x01\x00\x00``)
# differ in two byte positions — catches both a full BE↔LE swap and any
# subtler byte-shuffling regression.
# ---------------------------------------------------------------------------


class TestLengthPrefixEndianness:
    """Lock the wire-visible byte order of the length prefix to big-endian."""

    # Hand-built reference: 256-byte payload of 'A's, framed in big-endian.
    PAYLOAD = b"A" * 256
    BE_WIRE = b"\x00\x00\x01\x00" + PAYLOAD

    def test_reader_interprets_prefix_as_big_endian(self) -> None:
        # A reader that mistakenly uses little-endian would parse the prefix
        # as 0x00010000 (65536) and either truncate or raise MessageTooLarge.
        got = FrameReader(io.BytesIO(self.BE_WIRE)).read_frame()
        assert got == self.PAYLOAD

    def test_writer_emits_prefix_as_big_endian(self) -> None:
        # A writer that mistakenly uses little-endian would emit
        # b"\x00\x01\x00\x00" + payload, which fails this byte-equality.
        buf = io.BytesIO()
        FrameWriter(buf).write_frame(self.PAYLOAD)
        assert buf.getvalue() == self.BE_WIRE
