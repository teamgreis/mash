"""Cross-language vector tests for the framing layer.

Acceptance gate for Phase 2 (mash-py-tdd-plan.md §7.2):

> Frame a known CBOR message; assert the bytes match Go's framing.

For each vector recorded by ``tools/gen_vectors.go`` we run two checks:

1. **Encode** — pass the payload to mash-py's :class:`FrameWriter` and
   assert the emitted bytes are byte-identical to ``wire_hex``. This is
   the strict acceptance test: catches endianness and prefix-width
   bugs that would otherwise lurk until live interop.
2. **Decode** — feed ``wire_hex`` to mash-py's :class:`FrameReader` and
   assert the recovered payload matches ``payload_hex``. This proves
   the reverse direction, i.e. mash-py can consume Go-emitted frames.

Multi-frame vectors (with ``multi_payloads_hex``) verify back-to-back
framing on a single stream — a regression net for any future buffering
change in either language's writer.
"""

from __future__ import annotations

import io
import json
from pathlib import Path
from typing import Any

import pytest

from mash_py.wire.framing import FrameReader, FrameWriter

VECTORS_DIR = Path(__file__).parent.parent.parent / "vectors" / "wire"


def _load_framing_vectors() -> list[dict[str, Any]]:
    path = VECTORS_DIR / "framing_vectors.json"
    if not path.exists():
        pytest.fail(
            f"vector file missing: {path}\n"
            f"run: cd mash-py/tools && go run gen_vectors.go -out ../tests/vectors/wire"
        )
    return json.loads(path.read_text())


_VECTORS = _load_framing_vectors()
_SINGLE = [v for v in _VECTORS if not v.get("multi_payloads_hex")]
_MULTI = [v for v in _VECTORS if v.get("multi_payloads_hex")]


@pytest.mark.parametrize("vec", _SINGLE, ids=lambda v: v["name"])
def test_python_emits_byte_identical_frame(vec: dict[str, Any]) -> None:
    """mash-py FrameWriter must produce byte-identical bytes to Go's."""
    payload = bytes.fromhex(vec["payload_hex"])
    expected = bytes.fromhex(vec["wire_hex"])

    buf = io.BytesIO()
    FrameWriter(buf).write_frame(payload)
    assert buf.getvalue() == expected, (
        f"{vec['name']}: framed bytes diverge from Go.\n"
        f"  python: {buf.getvalue().hex()}\n"
        f"  go:     {expected.hex()}"
    )


@pytest.mark.parametrize("vec", _SINGLE, ids=lambda v: v["name"])
def test_python_decodes_go_emitted_frame(vec: dict[str, Any]) -> None:
    """mash-py FrameReader must recover the original payload from Go's wire."""
    expected_payload = bytes.fromhex(vec["payload_hex"])
    wire_bytes = bytes.fromhex(vec["wire_hex"])

    got = FrameReader(io.BytesIO(wire_bytes)).read_frame()
    assert got == expected_payload


@pytest.mark.parametrize("vec", _MULTI, ids=lambda v: v["name"])
def test_python_decodes_go_emitted_multi_frame_stream(vec: dict[str, Any]) -> None:
    """A stream of N back-to-back Go frames decodes to N payloads in order."""
    wire_bytes = bytes.fromhex(vec["wire_hex"])
    expected_payloads = [bytes.fromhex(h) for h in vec["multi_payloads_hex"]]

    reader = FrameReader(io.BytesIO(wire_bytes))
    got = [reader.read_frame() for _ in expected_payloads]
    assert got == expected_payloads


@pytest.mark.parametrize("vec", _MULTI, ids=lambda v: v["name"])
def test_python_emits_byte_identical_multi_frame_stream(vec: dict[str, Any]) -> None:
    """Writing the same N payloads through mash-py FrameWriter reproduces
    Go's multi-frame stream exactly."""
    expected = bytes.fromhex(vec["wire_hex"])
    payloads = [bytes.fromhex(h) for h in vec["multi_payloads_hex"]]

    buf = io.BytesIO()
    writer = FrameWriter(buf)
    for p in payloads:
        writer.write_frame(p)
    assert buf.getvalue() == expected
