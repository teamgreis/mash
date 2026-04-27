"""Device-side request dispatcher (Phase 15).

Receives wire :class:`mash_py.wire.Request` messages and produces
:class:`mash_py.wire.Response` messages by routing Read / Write / Invoke
to the appropriate ``Endpoint → Feature`` on a :class:`mash_py.model.Device`.
Subscribe is rejected with :data:`Status.UNSUPPORTED` until Phase 16 wires
up the subscription dispatcher.

Spec references:

- ``docs/interaction-model.md §3``  — request / response wire shape.
- ``docs/interaction-model.md §4``  — Read.
- ``docs/interaction-model.md §5``  — Write.
- ``docs/interaction-model.md §7``  — Invoke.
- ``docs/interaction-model.md §8.1`` — Status code numbering.

Spec-vs-Go alignments applied here (chosen 2026-04-27):

- Read payload at request key 5 is the bare CBOR array
  (``5: [1, 2, 3]``) per §4.1. mash-py's controller already emits this
  shape; mash-go was patched in the same workstream so its Go device
  side accepts it too.
- Unsubscribe is gated on BOTH ``endpointId == 0`` AND ``featureId == 0``
  per §6.4 (mash-go was tightened to match — Subscribe is still
  unsupported here, but the gating shape is the spec one).
- Partial Read with an unknown attribute id returns ``INVALID_ATTRIBUTE``
  on first failure (matches mash-go; spec §4.2 was silent — see
  Phase 11 finding ambiguity note, file a clarification issue if needed).

The dispatcher is *deliberately* synchronous and stateless except for
the device model it's bound to. Async work (handler coroutines for
Invoke) is awaited on the way through; the dispatcher itself does not
hold any per-connection state. The Phase 16 :class:`SubscriptionManager`
will sit alongside as a separate object.
"""

from __future__ import annotations

import asyncio
import logging
from typing import Any

from mash_py.model import (
    AttributeNotFoundError,
    AttributeNotNullableError,
    AttributeNotWritableError,
    AttributeOutOfRangeError,
    AttributeValueTypeError,
    CommandNotFoundError,
    Device,
    Feature,
)
from mash_py.model.endpoint import (
    FeatureNotFoundError,
)
from mash_py.wire import (
    ErrorPayload,
    Operation,
    Request,
    Response,
    Status,
)

_log = logging.getLogger(__name__)


__all__ = [
    "CommandError",
    "OperationalServer",
]


class OperationalServer:
    """Routes one Request to its Response against a :class:`Device`.

    Lifecycle: instantiate once per :class:`Device`; share across
    connections. The dispatcher is concurrency-safe in the sense that
    every method is reentrant — there is no shared mutable state on
    the server itself. The model it routes against is shared, and the
    runtime model uses Python's GIL plus single-event-loop semantics
    for write-after-read consistency the same way mash-py's
    :class:`mash_py.commissioning` modules do.

    Subscribe currently returns ``Status.UNSUPPORTED``; Phase 16 will
    plug a real :class:`SubscriptionManager` into ``handle_subscribe``.
    """

    def __init__(self, device: Device) -> None:
        self._device = device

    # ------------------------------------------------------------------
    # Public surface
    # ------------------------------------------------------------------

    @property
    def device(self) -> Device:
        return self._device

    async def handle_request(self, req: Request) -> Response:
        """Route ``req`` and return the resulting :class:`Response`.

        Decodes per-operation payload shapes here rather than at the
        codec — the codec hands us the raw CBOR-decoded payload object
        so the dispatcher can apply the spec-vs-Go acceptance rules
        documented at the module level.

        Validation order matches the Go reference: endpoint, then
        feature, then operation-specific. This keeps Status precedence
        consistent with what controllers already expect.
        """
        # Operation enum is already validated by decode_request (any
        # uint outside 1..4 is rejected before reaching us). Defensive
        # check for completeness — surfaces UNSUPPORTED rather than
        # raising if a future codec relaxes that check.
        if not isinstance(req.operation, Operation) or not req.operation.is_valid():
            return _err(req.message_id, Status.UNSUPPORTED, "unsupported operation")

        if req.operation is Operation.SUBSCRIBE:
            return self._handle_subscribe(req)

        # Read / Write / Invoke all share the same endpoint / feature
        # lookup precedence. Pull them out once.
        try:
            endpoint = self._device.get_endpoint(req.endpoint_id)
        except Exception:
            return _err(
                req.message_id,
                Status.INVALID_ENDPOINT,
                f"endpoint {req.endpoint_id} not found",
            )

        try:
            feature = endpoint.get_feature_by_id(req.feature_id)
        except (FeatureNotFoundError, ValueError):
            return _err(
                req.message_id,
                Status.INVALID_FEATURE,
                f"feature 0x{req.feature_id:02x} not found on endpoint {req.endpoint_id}",
            )

        if req.operation is Operation.READ:
            return self._handle_read(req, feature)
        if req.operation is Operation.WRITE:
            return self._handle_write(req, feature)
        if req.operation is Operation.INVOKE:
            return await self._handle_invoke(req, feature)

        # Unreachable — kept for type-narrowing.
        return _err(req.message_id, Status.UNSUPPORTED, "unsupported operation")

    # ------------------------------------------------------------------
    # Read
    # ------------------------------------------------------------------

    def _handle_read(self, req: Request, feature: Feature) -> Response:
        """Read attributes per ``§4.1 / §4.2``.

        Empty / missing payload → "read all". A request asking for a
        specific attribute that the feature does not have → fail with
        ``INVALID_ATTRIBUTE`` on first miss (matches mash-go). The
        spec is silent on partial-Read precedence; we document the
        chosen behavior at the module level.
        """
        attr_ids = _extract_read_attribute_ids(req.payload)

        if attr_ids is None:
            return _err(
                req.message_id,
                Status.INVALID_PARAMETER,
                "Read payload must be a CBOR array of attribute IDs",
            )

        if len(attr_ids) == 0:
            payload: dict[int, Any] = feature.read_all_attributes()
            return Response(
                message_id=req.message_id,
                status=Status.SUCCESS,
                payload=payload,
            )

        result: dict[int, Any] = {}
        for attr_id in attr_ids:
            try:
                value = feature.read_attribute(attr_id)
            except AttributeNotFoundError:
                return _err(
                    req.message_id,
                    Status.INVALID_ATTRIBUTE,
                    f"attribute 0x{attr_id:04x} not found",
                )
            result[attr_id] = value

        return Response(
            message_id=req.message_id,
            status=Status.SUCCESS,
            payload=result,
        )

    # ------------------------------------------------------------------
    # Write
    # ------------------------------------------------------------------

    def _handle_write(self, req: Request, feature: Feature) -> Response:
        """Write attributes per ``§5.1 / §5.2``.

        Wire form (``§5.1``): payload at key 5 is a CBOR map keyed by
        attribute id. After CBOR decode this surfaces as a Python
        ``dict`` (cbor2 keeps integer keys as ``int``). Empty / missing
        payload → ``INVALID_PARAMETER``.

        Status precedence on first failure (matches mash-go):

        - unknown attribute → ``INVALID_ATTRIBUTE``
        - read-only attribute → ``READ_ONLY``
        - type / range / nullability → ``CONSTRAINT_ERROR``

        Successful writes are echoed back in the response payload as
        ``{attrId: actualValue}``. ``actualValue`` is the value the
        device decided to store, which may differ from the requested
        value (e.g. zone-aware effective limits) — re-read after the
        write so the response reflects what the model actually has.
        """
        write_payload = _extract_write_payload(req.payload)
        if write_payload is None:
            return _err(
                req.message_id,
                Status.INVALID_PARAMETER,
                "Write payload must be a CBOR map of attribute IDs to values",
            )

        result: dict[int, Any] = {}
        for attr_id, value in write_payload.items():
            try:
                feature.write_attribute(attr_id, value)
            except AttributeNotFoundError:
                return _err(
                    req.message_id,
                    Status.INVALID_ATTRIBUTE,
                    f"attribute 0x{attr_id:04x} not found",
                )
            except AttributeNotWritableError:
                return _err(
                    req.message_id,
                    Status.READ_ONLY,
                    f"attribute 0x{attr_id:04x} is read-only",
                )
            except (
                AttributeValueTypeError,
                AttributeOutOfRangeError,
                AttributeNotNullableError,
            ) as exc:
                return _err(
                    req.message_id, Status.CONSTRAINT_ERROR, str(exc)
                )
            # Read back so the response carries the stored value, not
            # the (possibly-coerced) requested value.
            try:
                result[attr_id] = feature.read_attribute(attr_id)
            except AttributeNotFoundError:
                # Should be unreachable post-write_attribute success;
                # fall back to the requested value rather than wedging.
                result[attr_id] = value

        return Response(
            message_id=req.message_id,
            status=Status.SUCCESS,
            payload=result,
        )

    # ------------------------------------------------------------------
    # Invoke
    # ------------------------------------------------------------------

    async def _handle_invoke(self, req: Request, feature: Feature) -> Response:
        """Invoke a command per ``§7.1 / §7.2``.

        Wire form: payload at key 5 is ``{1: commandId, 2: parameters}``.
        ``parameters`` is integer-keyed per ``§7.1`` (Issue #11 fix).
        Both `parameters` absent and `parameters: {}` are accepted —
        commands with no required parameters need not carry a key 2.
        """
        cmd_id, params = _extract_invoke_payload(req.payload)
        if cmd_id is None:
            return _err(
                req.message_id,
                Status.INVALID_PARAMETER,
                "Invoke payload must be a CBOR map with key 1 = commandId",
            )

        try:
            command = feature.get_command(cmd_id)
        except CommandNotFoundError:
            return _err(
                req.message_id,
                Status.INVALID_COMMAND,
                f"command 0x{cmd_id:02x} not found",
            )

        handler = command.handler
        if handler is None:
            return _err(
                req.message_id,
                Status.UNSUPPORTED,
                f"command 0x{cmd_id:02x} has no registered handler",
            )

        try:
            result_obj = handler(params)
            if asyncio.iscoroutine(result_obj):
                result_obj = await result_obj
        except CommandError as exc:
            # Handlers raise CommandError to surface a specific Status
            # code (e.g. CONSTRAINT_ERROR for "zoneId mismatch").
            return _err(req.message_id, exc.status, exc.message)
        except Exception as exc:
            _log.exception(
                "command handler 0x%02x raised; mapping to CONSTRAINT_ERROR",
                cmd_id,
            )
            return _err(
                req.message_id,
                Status.CONSTRAINT_ERROR,
                f"command failed: {exc}",
            )

        return Response(
            message_id=req.message_id,
            status=Status.SUCCESS,
            payload=result_obj,
        )

    # ------------------------------------------------------------------
    # Subscribe (Phase 16 placeholder)
    # ------------------------------------------------------------------

    def _handle_subscribe(self, req: Request) -> Response:
        """Reject Subscribe with ``UNSUPPORTED`` until Phase 16.

        Per ``§6.4`` an unsubscribe sentinel is ``endpointId=0,
        featureId=0``; both forms — subscribe and unsubscribe — surface
        the same status here for now. A controller seeing
        ``UNSUPPORTED`` knows the device does not yet support the
        Subscribe operation, which is more informative than
        silently-failing-to-deliver.
        """
        return _err(
            req.message_id,
            Status.UNSUPPORTED,
            "Subscribe is not yet implemented (Phase 16)",
        )


# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------


class CommandError(Exception):
    """Raised inside a command handler to surface a specific
    :class:`Status` code on the response.

    Named to mirror mash-go's ``wire.CommandError`` — handlers
    shouldn't have to know how to build a Response, just *which*
    error to surface. Distinct from the controller-side
    :class:`mash_py.interaction.StatusError` (which represents an
    error *received* from the peer).
    """

    def __init__(self, status: Status, message: str = "") -> None:
        self.status = status
        self.message = message
        super().__init__(self.__str__())

    def __str__(self) -> str:
        return (
            f"{self.status.name}({int(self.status)})"
            + (f": {self.message}" if self.message else "")
        )


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _err(message_id: int, status: Status, msg: str) -> Response:
    """Build a non-success response with an :class:`ErrorPayload`."""
    return Response(
        message_id=message_id,
        status=status,
        payload=ErrorPayload(message=msg),
    )


def _extract_read_attribute_ids(payload: Any) -> list[int] | None:
    """Spec-aware Read-payload decoder.

    Per ``§4.1`` the canonical wire form is a bare CBOR array at key 5.
    cbor2 decodes that as a Python ``list`` of ``int``. We *also*
    accept the legacy ``{1: [...]}`` map form because Go's
    ``wire.ReadPayload`` struct used to encode that way (and a
    pre-fix Go device only accepted that shape — see the Go fix
    landed in the same workstream).

    Returns ``[]`` for "read all", a list of ids for partial Read, or
    ``None`` if the payload shape is not recognized — caller maps
    ``None`` to ``INVALID_PARAMETER``.
    """
    if payload is None:
        return []  # missing payload === read-all

    if isinstance(payload, list):
        ids: list[int] = []
        for item in payload:
            if not isinstance(item, int) or isinstance(item, bool):
                return None
            ids.append(item)
        return ids

    if isinstance(payload, dict):
        # Legacy {1: [...]} shape — accept defensively. Empty dict is
        # a valid "read all".
        if not payload:
            return []
        inner = payload.get(1)
        if inner is None:
            # Map without key 1 is meaningless for Read.
            return None
        if not isinstance(inner, list):
            return None
        ids = []
        for item in inner:
            if not isinstance(item, int) or isinstance(item, bool):
                return None
            ids.append(item)
        return ids

    return None


def _extract_write_payload(payload: Any) -> dict[int, Any] | None:
    """Decode the Write payload (``§5.1``).

    Per spec the payload is a CBOR map keyed by attribute id. cbor2
    yields ``dict[int, Any]`` directly. Returns ``None`` for any
    other shape (caller maps to ``INVALID_PARAMETER``).
    """
    if payload is None:
        return None
    if not isinstance(payload, dict):
        return None
    if not payload:
        # Empty dict is a no-op Write — reject as invalid rather than
        # quietly returning success-with-empty-payload, which would
        # hide a controller bug.
        return None
    out: dict[int, Any] = {}
    for k, v in payload.items():
        if not isinstance(k, int) or isinstance(k, bool):
            return None
        out[k] = v
    return out


def _extract_invoke_payload(payload: Any) -> tuple[int | None, dict[int, Any]]:
    """Decode the Invoke payload (``§7.1``).

    Wire form: ``{1: commandId, 2: parameters}``. ``parameters`` is
    integer-keyed per ``§7.1`` (Issue #11 fix). Returns
    ``(command_id, parameters)``; ``command_id`` is ``None`` when the
    shape is malformed and caller maps to ``INVALID_PARAMETER``.
    Missing key 2 → empty parameter map (commands without required
    parameters are common).
    """
    if not isinstance(payload, dict):
        return None, {}

    cmd_raw = payload.get(1)
    if not isinstance(cmd_raw, int) or isinstance(cmd_raw, bool):
        return None, {}

    params_raw = payload.get(2)
    params: dict[int, Any] = {}
    if params_raw is not None:
        if not isinstance(params_raw, dict):
            return None, {}
        for k, v in params_raw.items():
            if not isinstance(k, int) or isinstance(k, bool):
                return None, {}
            params[k] = v
    return cmd_raw, params


