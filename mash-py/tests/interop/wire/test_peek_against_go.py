"""Phase 5 interop verification: ``peek_message_type`` against a live Go device.

Promotes the unit-test confidence built in Phase 1 (vector-based) to interop
confidence: every CBOR frame a Go device emits over a real TLS-framed
connection MUST classify exactly per the spec rules in
``docs/interaction-model.md §3`` and ``docs/transport.md §6.2``:

1. Frame begins with ``D8 4D`` (CBOR tag 77, DEC-083) → ``CONTROL``.
2. Otherwise the top-level CBOR object MUST be a map with integer keys.
3. ``messageId`` (key 1) == 0                          → ``NOTIFICATION``.
4. Map has key 4 (``featureId``)                       → ``REQUEST``.
5. Otherwise                                           → ``RESPONSE``.

The spec is the reference. The Go reference implementation is *one* producer
of correct frames; this test treats it as such — every frame Go emits is
asserted against the spec-defined classification rules above, not against
"whatever Go's `PeekMessageType` returns."

Why this file is mostly skipped today
-------------------------------------

A Go ``mash-device`` running in commissioning mode will not emit any
application-layer frame until the client completes the SPAKE2+ PASE
handshake (``docs/security.md §4.6``). Pre-PASE, the device's commissioning
handler is in ``WaitForPASERequest`` and rejects (closes) any frame that is
not a ``PASERequest`` — including ``ControlMessage{Ping}``. Driving the
Read/Write/Subscribe traffic the plan's acceptance criterion calls for
therefore requires Phase 8 (PASE client) and Phase 11 (interaction-model
client).

The verification *helper* — :func:`assert_classifies_per_spec` — is fully
implemented and spec-driven, so when Phase 8/11 land the only thing the
real interop test will need is the operational-session setup.
``test_go_binary_available`` is a CI smoke-check that the Go toolchain
will be ready when those phases unskip the body, mirroring the same
forward-looking pattern in :file:`tests/interop/test_phase4_tls.py`.
"""

from __future__ import annotations

import shutil
import subprocess
from collections.abc import Iterable

import cbor2
import pytest

from mash_py.wire.codec import CONTROL_MESSAGE_TAG
from mash_py.wire.operations import NOTIFICATION_MESSAGE_ID, MessageType
from mash_py.wire.peek import peek_message_type

pytestmark = pytest.mark.interop


# ---------------------------------------------------------------------------
# Spec-driven classification oracle
# ---------------------------------------------------------------------------


def classify_per_spec(frame: bytes) -> MessageType:
    """Return what the spec says ``frame`` is, computed independently of
    :func:`peek_message_type`.

    This is the *oracle* the interop test compares the production peek
    classifier against. It re-derives the answer from the rules in
    ``docs/interaction-model.md §3`` and ``docs/transport.md §6.2`` rather
    than calling the production implementation, so the test fails loudly if
    a future refactor of :mod:`mash_py.wire.peek` silently changes those
    rules.
    """
    # Rule 1 — CBOR tag 77 (DEC-083) is the unambiguous CONTROL marker.
    if len(frame) >= 2 and frame[0] == 0xD8 and frame[1] == CONTROL_MESSAGE_TAG:
        return MessageType.CONTROL

    # Rule 2 — top-level value must be a map with integer keys.
    decoded = cbor2.loads(frame)
    if not isinstance(decoded, dict):
        raise ValueError(
            f"non-conformant frame: top-level value must be a CBOR map, "
            f"got {type(decoded).__name__}"
        )
    for k in decoded:
        if not isinstance(k, int) or isinstance(k, bool):
            raise ValueError(f"non-conformant frame: non-integer top-level key {k!r}")

    # Rule 3 — messageId == 0 is the NOTIFICATION sentinel
    # (``docs/interaction-model.md §3.3``).
    if decoded.get(1) == NOTIFICATION_MESSAGE_ID:
        return MessageType.NOTIFICATION

    # Rule 4 — presence of featureId (key 4) marks a REQUEST
    # (``docs/interaction-model.md §3.1``).
    if 4 in decoded:
        return MessageType.REQUEST

    # Rule 5 — everything else is a RESPONSE
    # (``docs/interaction-model.md §3.2``).
    return MessageType.RESPONSE


def assert_classifies_per_spec(frames: Iterable[bytes]) -> list[MessageType]:
    """Assert that :func:`peek_message_type` agrees with the spec oracle on
    every frame.

    Returns the list of (agreed) classifications so callers can additionally
    assert distribution (e.g. "we saw at least one of each type").
    """
    out: list[MessageType] = []
    for i, frame in enumerate(frames):
        oracle = classify_per_spec(frame)
        actual = peek_message_type(frame)
        assert actual is oracle, (
            f"frame #{i}: peek_message_type returned {actual.name}, "
            f"spec oracle says {oracle.name}; "
            f"first 16 bytes = {frame[:16].hex()}"
        )
        out.append(actual)
    return out


# ---------------------------------------------------------------------------
# CI smoke checks (do not require PASE)
# ---------------------------------------------------------------------------


def _go_binary_available() -> bool:
    """The interop tests need a built ``mash-device`` binary on PATH or the
    Go toolchain in the sibling ``mash-go`` checkout. Skip when neither is
    present so the unit suite stays runnable without the Go toolchain."""
    return shutil.which("mash-device") is not None or shutil.which("go") is not None


@pytest.mark.skipif(not _go_binary_available(), reason="mash-go toolchain not available")
def test_go_toolchain_available() -> None:
    """Phase 5 will eventually drive a Go device through Read/Write/Subscribe
    operations and classify every reply. This precondition check confirms
    the Go toolchain is callable now, so when Phases 8 & 11 land the body
    below can be unskipped without a CI-environment surprise.
    """
    result = subprocess.run(
        ["go", "version"], capture_output=True, text=True, timeout=10
    )
    assert result.returncode == 0
    assert result.stdout.startswith("go version")


# ---------------------------------------------------------------------------
# Self-test: oracle vs. production peek on hand-crafted, spec-shaped frames
# ---------------------------------------------------------------------------


def test_oracle_and_peek_agree_on_canonical_examples() -> None:
    """Sanity-check the oracle itself against the canonical wire examples
    from ``docs/interaction-model.md §3``. This is not yet the live interop
    test — it's the regression guard that ensures the oracle and production
    peek classifier stay in sync independent of the Go device's availability.

    When Phases 8 & 11 land, the live :func:`assert_classifies_per_spec`
    helper runs the same agreement check against Go-emitted frames; if the
    oracle and peek diverge here first, this test fails before the live one
    can produce a misleading interop result.
    """
    # docs/interaction-model.md §3.1 — Request {1: msgId, 2: op, 3: ep, 4: feat}
    request_frame = cbor2.dumps({1: 1, 2: 1, 3: 0, 4: 1}, canonical=True)
    # docs/interaction-model.md §3.2 — Response {1: msgId, 2: status}
    response_frame = cbor2.dumps({1: 1, 2: 0}, canonical=True)
    # docs/interaction-model.md §3.3 — Notification {1: 0, 2: subId, 3: ep, 4: feat, 5: changes}
    notification_frame = cbor2.dumps(
        {1: NOTIFICATION_MESSAGE_ID, 2: 1, 3: 0, 4: 1, 5: {}}, canonical=True
    )
    # docs/transport.md §6.2 — Control 77({1: type, 2: seq})
    control_frame = cbor2.dumps(cbor2.CBORTag(CONTROL_MESSAGE_TAG, {1: 1, 2: 42}), canonical=True)

    classifications = assert_classifies_per_spec(
        [request_frame, response_frame, notification_frame, control_frame]
    )
    assert classifications == [
        MessageType.REQUEST,
        MessageType.RESPONSE,
        MessageType.NOTIFICATION,
        MessageType.CONTROL,
    ]


# ---------------------------------------------------------------------------
# The live interop body — deferred until Phase 8 lands
# ---------------------------------------------------------------------------


@pytest.mark.skip(reason="awaits Phase 8 (PASE client) + Phase 11 (interaction client)")
def test_peek_classifies_all_go_emitted_frames() -> None:
    """Drive a Go device through a Read / Write / Subscribe sequence and
    assert every emitted frame classifies per the spec oracle.

    Outline of the body that lands with Phase 11:

    1. Spawn ``mash-go/cmd/mash-device`` on a fixed port (mDNS via Phase 6
       is *also* live by then but the test pins a port for determinism).
    2. Open a commissioning TLS 1.3 connection (``mash-comm/1`` ALPN) via
       :func:`mash_py.transport.connection.connect`.
    3. Run PASE (Phase 8 client) and the cert exchange (Phase 9) to obtain
       an operational identity.
    4. Open an operational connection (``mash/1`` ALPN, Phase 10).
    5. Drive synthetic traffic (Phase 11 client):

       - ``Read``    DeviceInfo attributes on endpoint 0 → expect Response
       - ``Write``   a writable attribute              → expect Response
       - ``Subscribe`` to a Measurement attribute       → expect SubscribeResponse
                                                          and one Notification
       - send a Control ``Ping``                        → expect ``Pong``

    6. Capture every frame received via a tap that records the raw bytes
       *before* Connection's read loop dispatches them, and feed the list
       into :func:`assert_classifies_per_spec`.

    Distribution check — the test additionally asserts the captured set
    contains at least one of each :class:`MessageType` (REQUEST is rare on
    the controller side; the bidirectional Renewal flow in Phase 19 will
    likely supply it). The Phase 5 acceptance bar is "zero
    misclassifications", but seeing one of every type provides positive
    evidence that the classifier is exercised end-to-end, not just on a
    monoculture of Responses.
    """
    pytest.fail(
        "Body deferred to Phase 11. The classification helper "
        "`assert_classifies_per_spec` is implemented and tested above; "
        "this test only awaits the operational-traffic harness."
    )
