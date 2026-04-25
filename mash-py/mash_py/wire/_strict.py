"""Strict CBOR validator.

cbor2 by default silently merges duplicate map keys (last wins) and decodes
NaN/Infinity floats to ``float('nan')`` / ``float('inf')``. The MASH wire
contract (see mash-go/pkg/wire/codec.go:33-43, options ``DupMapKeyEnforcedAPF``,
``NaNDecodeForbidden``, ``InfDecodeForbidden``) rejects all three.

This module walks the CBOR byte stream once and raises ``StrictDecodeError``
if any of those constraints is violated. It is run before ``cbor2.loads`` so
decode failures surface with a precise reason instead of relying on cbor2's
silent acceptance.

The walker only validates structure — it does not build Python values. That is
left to cbor2.loads.
"""

from __future__ import annotations

import struct


class StrictDecodeError(ValueError):
    """Raised when CBOR bytes violate the MASH strict-decode contract."""


def strict_validate(data: bytes) -> None:
    """Validate ``data`` against MASH strict-decode rules.

    Raises StrictDecodeError on duplicate map keys (any depth), NaN, Inf, or
    truncated/over-long input.
    """
    if len(data) == 0:
        raise StrictDecodeError("empty CBOR input")
    end = _walk(data, 0)
    if end != len(data):
        raise StrictDecodeError(
            f"trailing bytes after top-level CBOR value: parsed={end} total={len(data)}"
        )


def _read_count(data: bytes, pos: int, minor: int) -> tuple[int, int]:
    """Read the unsigned argument for a CBOR head byte.

    Returns (value, new_pos). For minor ``31`` (indefinite length), returns
    (-1, pos) so the caller can switch to break-terminated parsing.
    """
    if minor < 24:
        return minor, pos
    if minor == 24:
        if pos + 1 > len(data):
            raise StrictDecodeError("truncated 1-byte argument")
        return data[pos], pos + 1
    if minor == 25:
        if pos + 2 > len(data):
            raise StrictDecodeError("truncated 2-byte argument")
        return int.from_bytes(data[pos : pos + 2], "big"), pos + 2
    if minor == 26:
        if pos + 4 > len(data):
            raise StrictDecodeError("truncated 4-byte argument")
        return int.from_bytes(data[pos : pos + 4], "big"), pos + 4
    if minor == 27:
        if pos + 8 > len(data):
            raise StrictDecodeError("truncated 8-byte argument")
        return int.from_bytes(data[pos : pos + 8], "big"), pos + 8
    if minor == 31:
        return -1, pos
    raise StrictDecodeError(f"reserved CBOR minor type: {minor}")


def _walk(data: bytes, pos: int) -> int:
    if pos >= len(data):
        raise StrictDecodeError("unexpected end of CBOR data")
    head = data[pos]
    pos += 1
    major = head >> 5
    minor = head & 0x1F

    # Major 0/1: unsigned / negative integer (no children)
    if major in (0, 1):
        if minor == 31:
            raise StrictDecodeError("invalid integer with indefinite length")
        _, pos = _read_count(data, pos, minor)
        return pos

    # Major 2/3: byte string / text string
    if major in (2, 3):
        if minor == 31:
            # indefinite-length string: chunks until break (0xFF)
            return _walk_indefinite_string(data, pos, major)
        n, pos = _read_count(data, pos, minor)
        end = pos + n
        if end > len(data):
            raise StrictDecodeError("truncated string contents")
        return end

    # Major 4: array
    if major == 4:
        if minor == 31:
            return _walk_indefinite_array(data, pos)
        n, pos = _read_count(data, pos, minor)
        for _ in range(n):
            pos = _walk(data, pos)
        return pos

    # Major 5: map
    if major == 5:
        if minor == 31:
            return _walk_indefinite_map(data, pos)
        n, pos = _read_count(data, pos, minor)
        return _walk_map_pairs(data, pos, n)

    # Major 6: semantic tag — argument is the tag number, then one tagged value
    if major == 6:
        if minor == 31:
            raise StrictDecodeError("invalid tag with indefinite length")
        _, pos = _read_count(data, pos, minor)
        return _walk(data, pos)

    # Major 7: simple values, floats, break
    if major == 7:
        return _walk_simple(data, pos, minor)

    raise StrictDecodeError(f"unknown CBOR major type: {major}")


def _walk_map_pairs(data: bytes, pos: int, n: int) -> int:
    """Walk ``n`` (key, value) pairs. Detects duplicate keys by comparing the
    encoded byte slices — this matches CBOR equality and avoids materialising
    Python objects for a strictness check."""
    seen: set[bytes] = set()
    for _ in range(n):
        key_start = pos
        pos = _walk(data, pos)
        key_bytes = bytes(data[key_start:pos])
        if key_bytes in seen:
            raise StrictDecodeError("duplicate map key in CBOR map")
        seen.add(key_bytes)
        pos = _walk(data, pos)
    return pos


def _walk_indefinite_array(data: bytes, pos: int) -> int:
    while True:
        if pos >= len(data):
            raise StrictDecodeError("truncated indefinite array")
        if data[pos] == 0xFF:
            return pos + 1
        pos = _walk(data, pos)


def _walk_indefinite_map(data: bytes, pos: int) -> int:
    seen: set[bytes] = set()
    while True:
        if pos >= len(data):
            raise StrictDecodeError("truncated indefinite map")
        if data[pos] == 0xFF:
            return pos + 1
        key_start = pos
        pos = _walk(data, pos)
        key_bytes = bytes(data[key_start:pos])
        if key_bytes in seen:
            raise StrictDecodeError("duplicate map key in CBOR map")
        seen.add(key_bytes)
        pos = _walk(data, pos)


def _walk_indefinite_string(data: bytes, pos: int, major: int) -> int:
    while True:
        if pos >= len(data):
            raise StrictDecodeError("truncated indefinite string")
        if data[pos] == 0xFF:
            return pos + 1
        chunk_head = data[pos]
        if chunk_head >> 5 != major:
            raise StrictDecodeError("indefinite string chunk has wrong major type")
        pos = _walk(data, pos)


def _walk_simple(data: bytes, pos: int, minor: int) -> int:
    # 20=false, 21=true, 22=null, 23=undefined
    if minor < 20:
        return pos
    if minor in (20, 21, 22, 23):
        return pos
    if minor == 24:  # simple value with 1-byte argument
        if pos + 1 > len(data):
            raise StrictDecodeError("truncated simple value argument")
        return pos + 1
    if minor == 25:  # half float
        if pos + 2 > len(data):
            raise StrictDecodeError("truncated half-float")
        f = _decode_half_float(data[pos : pos + 2])
        _check_finite(f)
        return pos + 2
    if minor == 26:  # single float
        if pos + 4 > len(data):
            raise StrictDecodeError("truncated single-float")
        (f,) = struct.unpack(">f", data[pos : pos + 4])
        _check_finite(f)
        return pos + 4
    if minor == 27:  # double float
        if pos + 8 > len(data):
            raise StrictDecodeError("truncated double-float")
        (f,) = struct.unpack(">d", data[pos : pos + 8])
        _check_finite(f)
        return pos + 8
    if minor == 31:
        # break — only valid inside an indefinite container
        raise StrictDecodeError("unexpected break (0xFF) outside indefinite container")
    raise StrictDecodeError(f"reserved simple/float minor: {minor}")


def _check_finite(f: float) -> None:
    if f != f:  # NaN
        raise StrictDecodeError("NaN floats are not allowed in MASH wire data")
    if f in (float("inf"), float("-inf")):
        raise StrictDecodeError("Infinity floats are not allowed in MASH wire data")


def _decode_half_float(b: bytes) -> float:
    """IEEE 754 binary16 → Python float.

    Mirrors RFC 8949 Appendix D. cbor2's C extension does this internally but
    is not exposed; doing it here keeps the strict-validator self-contained.
    """
    raw = (b[0] << 8) | b[1]
    sign = (raw >> 15) & 0x1
    exponent = (raw >> 10) & 0x1F
    mantissa = raw & 0x3FF
    if exponent == 0:
        # subnormal or zero
        value = (mantissa / 1024.0) * (2.0**-14)
    elif exponent == 0x1F:
        # Inf or NaN
        value = float("inf") if mantissa == 0 else float("nan")
    else:
        value = (1 + mantissa / 1024.0) * (2.0 ** (exponent - 15))
    return -value if sign else value
