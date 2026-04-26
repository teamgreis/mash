"""Length-prefixed framing for the MASH wire protocol.

Ports mash-go/pkg/transport/framing.go.

Wire format::

    +---------------+----------------------+
    |  length (4B)  |   payload (length B) |
    +---------------+----------------------+

- Length is a 4-byte **big-endian** unsigned integer.
- Length must satisfy ``1 <= length <= max_message_size``; the default
  max is 8192 bytes (DEC-069 — fits the 256 KB MCU memory budget from
  DEC-003).
- Payloads of length 0 are rejected on both write and read.
- Payloads exceeding the configured max are rejected on both write and
  read.

Two parallel APIs are exposed:

- :class:`FrameReader` / :class:`FrameWriter` — synchronous, take a
  file-like object (anything supporting ``.read(n)`` / ``.write(b)``).
  Useful in unit tests against ``io.BytesIO`` and in any blocking-IO
  context.
- :class:`AsyncFrameReader` / :class:`AsyncFrameWriter` — asynchronous,
  take an :class:`asyncio.StreamReader` / :class:`asyncio.StreamWriter`.
  This is what the transport layer (Phase 4) plugs into.

Both APIs raise the same :class:`FramingError` subclasses, so callers
get a consistent exception model regardless of mode.
"""

from __future__ import annotations

import asyncio
import threading
from typing import IO

# ---------------------------------------------------------------------------
# Constants — keep in sync with pkg/transport/framing.go
# ---------------------------------------------------------------------------

LENGTH_PREFIX_SIZE = 4
"""Size of the length prefix in bytes."""

DEFAULT_MAX_MESSAGE_SIZE = 8192
"""Default maximum payload size — DEC-069. Callers needing larger frames
must opt in via the ``max_message_size`` argument."""

MIN_MESSAGE_SIZE = 1
"""Smallest valid payload size (zero-length frames are rejected)."""


# ---------------------------------------------------------------------------
# Errors — mirror pkg/transport/framing.go's sentinels
# ---------------------------------------------------------------------------


class FramingError(Exception):
    """Base class for all framing errors."""


class MessageEmptyError(FramingError):
    """Raised on attempts to write or read a zero-length frame."""


class MessageTooLargeError(FramingError):
    """Raised when payload size exceeds the configured maximum."""

    def __init__(self, size: int, maximum: int) -> None:
        super().__init__(f"message too large: {size} > {maximum}")
        self.size = size
        self.maximum = maximum


class FrameTruncatedError(FramingError):
    """Raised when the underlying stream ends mid-frame.

    Maps to Go's ``ErrFrameTruncated`` (which itself wraps
    ``io.ErrUnexpectedEOF``). EOF *between* frames — i.e. before any
    bytes of the next length prefix have arrived — is signalled by the
    sync API raising :class:`EOFError` and the async API raising
    :class:`asyncio.IncompleteReadError` with ``partial == b""``.
    """


# ---------------------------------------------------------------------------
# Synchronous writer / reader
# ---------------------------------------------------------------------------


class FrameWriter:
    """Synchronous length-prefixed frame writer.

    Thread-safe: concurrent ``write_frame`` calls are serialised by an
    internal lock so that a length prefix and its payload are never
    interleaved with another frame's bytes (matches Go's
    ``FrameWriter.mu``).
    """

    def __init__(self, w: IO[bytes], max_message_size: int = DEFAULT_MAX_MESSAGE_SIZE) -> None:
        self._w = w
        self._max = max_message_size
        self._lock = threading.Lock()

    @property
    def max_message_size(self) -> int:
        return self._max

    def write_frame(self, data: bytes) -> None:
        """Write a single length-prefixed frame.

        Raises :class:`MessageEmptyError` for zero-length payloads and
        :class:`MessageTooLargeError` for payloads above the configured
        max — both checks happen before any bytes are written, so a
        rejected frame leaves the stream untouched.
        """
        size = len(data)
        if size == 0:
            raise MessageEmptyError("message is empty")
        if size > self._max:
            raise MessageTooLargeError(size, self._max)

        prefix = size.to_bytes(LENGTH_PREFIX_SIZE, "big")
        with self._lock:
            self._w.write(prefix)
            self._w.write(data)


class FrameReader:
    """Synchronous length-prefixed frame reader."""

    def __init__(self, r: IO[bytes], max_message_size: int = DEFAULT_MAX_MESSAGE_SIZE) -> None:
        self._r = r
        self._max = max_message_size

    @property
    def max_message_size(self) -> int:
        return self._max

    def set_max_message_size(self, size: int) -> None:
        """Update the maximum frame size accepted by future reads."""
        self._max = size

    def read_frame(self) -> bytes:
        """Read a single length-prefixed frame.

        Returns the payload (length prefix stripped). Raises:

        - :class:`EOFError` when the stream is at EOF *between* frames
          (zero bytes of length prefix consumed). Mirrors Go's
          ``io.EOF`` return at the frame boundary.
        - :class:`FrameTruncatedError` when the stream ends partway
          through a length prefix or payload.
        - :class:`MessageEmptyError` when the prefix declares length 0.
        - :class:`MessageTooLargeError` when the prefix declares a
          length above the configured max.
        """
        prefix = self._read_exact(LENGTH_PREFIX_SIZE, eof_ok=True)
        length = int.from_bytes(prefix, "big")

        if length == 0:
            raise MessageEmptyError("message is empty")
        if length > self._max:
            raise MessageTooLargeError(length, self._max)

        return self._read_exact(length, eof_ok=False)

    def _read_exact(self, n: int, *, eof_ok: bool) -> bytes:
        """Read exactly ``n`` bytes, draining short reads.

        ``read(n)`` on a file-like may legally return fewer than ``n``
        bytes even when the stream still has data — particularly true
        for sockets and pipes. We loop until we have all ``n`` or we
        hit EOF.

        ``eof_ok=True`` lets a clean EOF *before any byte is read*
        propagate as :class:`EOFError`; mid-read EOF always becomes
        :class:`FrameTruncatedError`.
        """
        chunks: list[bytes] = []
        remaining = n
        while remaining > 0:
            chunk = self._r.read(remaining)
            if not chunk:
                if eof_ok and remaining == n:
                    raise EOFError("end of stream")
                raise FrameTruncatedError("frame truncated")
            chunks.append(chunk)
            remaining -= len(chunk)
        return b"".join(chunks) if len(chunks) > 1 else chunks[0]


class Framer:
    """Convenience pairing of :class:`FrameReader` and :class:`FrameWriter`.

    Mirrors Go's ``NewFramer(rw io.ReadWriter) *Framer`` for symmetry
    with the test patterns; not strictly required since most call sites
    will use the reader/writer directly.
    """

    def __init__(self, rw: IO[bytes], max_message_size: int = DEFAULT_MAX_MESSAGE_SIZE) -> None:
        self.reader = FrameReader(rw, max_message_size=max_message_size)
        self.writer = FrameWriter(rw, max_message_size=max_message_size)

    def read_frame(self) -> bytes:
        return self.reader.read_frame()

    def write_frame(self, data: bytes) -> None:
        self.writer.write_frame(data)


# ---------------------------------------------------------------------------
# Async writer / reader (asyncio.StreamReader / StreamWriter)
# ---------------------------------------------------------------------------


class AsyncFrameWriter:
    """Async length-prefixed frame writer over :class:`asyncio.StreamWriter`.

    ``write_frame`` does **not** call ``drain()``; callers that need
    flow control should ``await drain()`` themselves on the underlying
    writer, matching the asyncio convention. The internal
    :class:`asyncio.Lock` only serialises the prefix+payload pair so a
    concurrent writer cannot interleave a frame.
    """

    def __init__(
        self, w: asyncio.StreamWriter, max_message_size: int = DEFAULT_MAX_MESSAGE_SIZE
    ) -> None:
        self._w = w
        self._max = max_message_size
        self._lock = asyncio.Lock()

    @property
    def max_message_size(self) -> int:
        return self._max

    async def write_frame(self, data: bytes) -> None:
        size = len(data)
        if size == 0:
            raise MessageEmptyError("message is empty")
        if size > self._max:
            raise MessageTooLargeError(size, self._max)

        prefix = size.to_bytes(LENGTH_PREFIX_SIZE, "big")
        async with self._lock:
            self._w.write(prefix)
            self._w.write(data)


class AsyncFrameReader:
    """Async length-prefixed frame reader over :class:`asyncio.StreamReader`."""

    def __init__(
        self, r: asyncio.StreamReader, max_message_size: int = DEFAULT_MAX_MESSAGE_SIZE
    ) -> None:
        self._r = r
        self._max = max_message_size

    @property
    def max_message_size(self) -> int:
        return self._max

    def set_max_message_size(self, size: int) -> None:
        self._max = size

    async def read_frame(self) -> bytes:
        """Read a single length-prefixed frame.

        Raises :class:`asyncio.IncompleteReadError` (with
        ``partial == b""``) at clean end-of-stream — same semantics as
        :meth:`asyncio.StreamReader.readexactly`. A mid-frame EOF is
        translated to :class:`FrameTruncatedError`.
        """
        try:
            prefix = await self._r.readexactly(LENGTH_PREFIX_SIZE)
        except asyncio.IncompleteReadError as exc:
            if not exc.partial:
                raise
            raise FrameTruncatedError("frame truncated") from exc

        length = int.from_bytes(prefix, "big")
        if length == 0:
            raise MessageEmptyError("message is empty")
        if length > self._max:
            raise MessageTooLargeError(length, self._max)

        try:
            return await self._r.readexactly(length)
        except asyncio.IncompleteReadError as exc:
            raise FrameTruncatedError("frame truncated") from exc


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def frame_size(payload_size: int) -> int:
    """Return the wire size of a frame including the length prefix."""
    return LENGTH_PREFIX_SIZE + payload_size


__all__ = [
    "DEFAULT_MAX_MESSAGE_SIZE",
    "LENGTH_PREFIX_SIZE",
    "MIN_MESSAGE_SIZE",
    "AsyncFrameReader",
    "AsyncFrameWriter",
    "FrameReader",
    "FrameTruncatedError",
    "FrameWriter",
    "Framer",
    "FramingError",
    "MessageEmptyError",
    "MessageTooLargeError",
    "frame_size",
]
