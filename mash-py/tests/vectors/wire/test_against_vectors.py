"""Cross-language vector tests for the wire codec.

Acceptance gate for Phase 1 (mash-py-tdd-plan.md §7.1):

> All 30 vector tests pass. peek_message_type correctly classifies every
> vector without false-routing a Notification as a Response.

Vectors are produced by ``mash-py/tools/gen_vectors.go`` (a Go program that
imports mash-go's pkg/wire and emits canonical encodings). For each vector
we run two checks:

1. **Decode** — feed the recorded ``cbor_hex`` bytes through mash-py's decoder
   and verify the resulting dataclass matches the recorded ``expected``
   fields.
2. **Re-encode** — take the decoded message and pass it back through
   mash-py's encoder; the output must be byte-identical to ``cbor_hex``.

Step 2 catches divergences in canonical ordering, integer-encoding width,
and ``omitempty`` semantics — the most common interop failure modes per
plan §7.1's "refinement contingency".

Peek vectors additionally verify that ``peek_message_type`` returns the
recorded ``message_type``, including known-ambiguous corners
(see mash-py-spec-issues.md Issue #2).

Strict vectors verify that mash-py rejects the same malformed inputs that
mash-go does (NaN, Infinity, duplicate keys, string-keyed maps).
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest

from mash_py.wire import (
    MessageType,
    decode_control_message,
    decode_notification,
    decode_request,
    decode_response,
    encode_control_message,
    encode_notification,
    encode_request,
    encode_response,
    peek_message_type,
)
from mash_py.wire._strict import StrictDecodeError

VECTORS_DIR = Path(__file__).parent.parent.parent / "vectors" / "wire"


def _load(name: str) -> list[dict[str, Any]]:
    """Load a vectors JSON file produced by tools/gen_vectors.go."""
    path = VECTORS_DIR / name
    if not path.exists():
        pytest.fail(
            f"vector file missing: {path}\n"
            f"run: cd mash-py/tools && go run gen_vectors.go -out ../tests/vectors/wire"
        )
    return json.loads(path.read_text())


def _to_int_keyed(value: Any) -> Any:
    """Recursively rewrite string-int keys to int keys.

    JSON serialisation of vectors forces map keys to strings; mash-py's
    decoder produces int keys (matching the wire). Normalise so structural
    comparison sees through the JSON quirk.
    """
    if isinstance(value, dict):
        return {
            (int(k) if isinstance(k, str) and k.lstrip("-").isdigit() else k): _to_int_keyed(v)
            for k, v in value.items()
        }
    if isinstance(value, list):
        return [_to_int_keyed(v) for v in value]
    return value


# ---------------------------------------------------------------------------
# Request vectors
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("v", _load("request_vectors.json"), ids=lambda v: v["name"])
def test_request_vector(v: dict[str, Any]) -> None:
    raw = bytes.fromhex(v["cbor_hex"])

    # 1. Decode and check structure.
    decoded = decode_request(raw)
    expected = _to_int_keyed(v["expected"])
    assert decoded.message_id == expected["message_id"]
    assert int(decoded.operation) == expected["operation"]
    assert decoded.endpoint_id == expected["endpoint_id"]
    assert decoded.feature_id == expected["feature_id"]
    assert decoded.payload == expected["payload"], (
        f"payload mismatch in {v['name']}: "
        f"got {decoded.payload!r}, expected {expected['payload']!r}"
    )

    # 2. Re-encode — bytes must match exactly.
    re_encoded = encode_request(decoded)
    assert re_encoded.hex() == v["cbor_hex"], (
        f"re-encode drift in {v['name']}: "
        f"got {re_encoded.hex()}, expected {v['cbor_hex']}"
    )


# ---------------------------------------------------------------------------
# Response vectors
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("v", _load("response_vectors.json"), ids=lambda v: v["name"])
def test_response_vector(v: dict[str, Any]) -> None:
    raw = bytes.fromhex(v["cbor_hex"])
    decoded = decode_response(raw)

    expected = _to_int_keyed(v["expected"])
    assert decoded.message_id == expected["message_id"]
    assert int(decoded.status) == expected["status"]
    assert decoded.payload == expected["payload"]

    re_encoded = encode_response(decoded)
    assert re_encoded.hex() == v["cbor_hex"], (
        f"re-encode drift in {v['name']}: got {re_encoded.hex()}, expected {v['cbor_hex']}"
    )


# ---------------------------------------------------------------------------
# Notification vectors
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "v", _load("notification_vectors.json"), ids=lambda v: v["name"]
)
def test_notification_vector(v: dict[str, Any]) -> None:
    raw = bytes.fromhex(v["cbor_hex"])
    decoded = decode_notification(raw)

    expected = _to_int_keyed(v["expected"])
    assert decoded.subscription_id == expected["subscription_id"]
    assert decoded.endpoint_id == expected["endpoint_id"]
    assert decoded.feature_id == expected["feature_id"]
    assert decoded.changes == expected["changes"]

    re_encoded = encode_notification(decoded)
    assert re_encoded.hex() == v["cbor_hex"]


# ---------------------------------------------------------------------------
# Control vectors
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("v", _load("control_vectors.json"), ids=lambda v: v["name"])
def test_control_vector(v: dict[str, Any]) -> None:
    raw = bytes.fromhex(v["cbor_hex"])
    decoded = decode_control_message(raw)

    expected = v["expected"]
    assert int(decoded.type) == expected["type"]
    assert decoded.sequence == expected["sequence"]

    re_encoded = encode_control_message(decoded)
    assert re_encoded.hex() == v["cbor_hex"]


# ---------------------------------------------------------------------------
# Peek classification vectors
# ---------------------------------------------------------------------------

_PEEK_TYPE_TO_ENUM = {
    "request": MessageType.REQUEST,
    "response": MessageType.RESPONSE,
    "notification": MessageType.NOTIFICATION,
    "control": MessageType.CONTROL,
}


@pytest.mark.parametrize("v", _load("peek_vectors.json"), ids=lambda v: v["name"])
def test_peek_vector(v: dict[str, Any]) -> None:
    raw = bytes.fromhex(v["cbor_hex"])
    expected_type = _PEEK_TYPE_TO_ENUM[v["message_type"]]
    assert peek_message_type(raw) == expected_type, (
        f"peek misclassified {v['name']}: bytes {v['cbor_hex']} "
        f"got {peek_message_type(raw).name}, expected {expected_type.name}"
    )


# ---------------------------------------------------------------------------
# Strict-decode negative vectors
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("v", _load("strict_vectors.json"), ids=lambda v: v["name"])
def test_strict_vector_rejected(v: dict[str, Any]) -> None:
    raw = bytes.fromhex(v["cbor_hex"])
    err_substr = v["expect_error"]
    # Try decoding as each message kind — at least one route must reject.
    # In practice the strict_validate pre-pass catches most of these before
    # any specific decoder runs. Use decode_request as the canonical entrypoint.
    with pytest.raises((StrictDecodeError, ValueError)) as exc:
        decode_request(raw)
    assert err_substr.lower() in str(exc.value).lower(), (
        f"{v['name']}: expected error containing {err_substr!r}, got {exc.value!r}"
    )


# ---------------------------------------------------------------------------
# Acceptance gate visibility — fails CI loudly if total vectors drop below 30.
# Per plan §7.1: "All 30 vector tests pass."
# ---------------------------------------------------------------------------


def test_total_wire_vector_count_meets_acceptance_gate() -> None:
    total = sum(
        len(_load(f))
        for f in (
            "request_vectors.json",
            "response_vectors.json",
            "notification_vectors.json",
            "control_vectors.json",
        )
    )
    assert total >= 30, (
        f"Phase 1 acceptance gate requires >= 30 wire vectors, got {total}. "
        f"Regenerate via: cd mash-py/tools && go run gen_vectors.go"
    )
