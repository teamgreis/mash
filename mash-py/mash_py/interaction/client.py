"""Controller-side interaction client.

Implements the four MASH operations (Read / Write / Subscribe / Invoke)
plus the message-id correlation and notification dispatch that bind
them together. Pending requests are stored in a ``messageId``-keyed
:class:`asyncio.Future` map; the read loop (driven by
:class:`mash_py.transport.connection.Connection`) fills the future when
a matching Response arrives.

Spec references:

- ``docs/interaction-model.md §3.1-3.5`` — request/response/notification
  envelopes and messageId allocation.
- ``docs/interaction-model.md §4-7`` — operation-specific payload forms.
- ``docs/interaction-model.md §8.3`` — 10s default request timeout.

Design notes (vs mash-go/pkg/interaction/client.go):

- mash-go pre-increments its messageId counter, emitting ``2`` on the
  first request. The spec (``§3.5``) says "Start at 1." mash-py honours
  the spec; see ``mash-py-spec-issues.md`` for the divergence note.
- mash-go has a single global notification handler. mash-py routes
  notifications by ``subscriptionId`` to per-subscription handlers,
  which is what Phase 11's plan specified and what the four-operation
  Subscribe API needs to expose to callers anyway.
- The Go client takes a ``RequestSender`` interface and exposes
  ``HandleResponse`` / ``HandleNotification`` for the user's read loop
  to call. mash-py inverts this: the client implements
  :class:`ConnectionHandler` and is registered with
  :meth:`Connection.start`; the connection's existing read loop drives
  it. Control frames (Ping/Pong/Close, DEC-083 tag-77) stay handled at
  the transport layer — the interaction client never sees them.
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Any, Union

from mash_py.transport.connection import (
    Connection,
    ConnectionClosedError,
)
from mash_py.wire import (
    InvokePayload,
    MessageType,
    Notification,
    Operation,
    ReadPayload,
    Response,
    Status,
    SubscribePayload,
    UnsubscribePayload,
    decode_notification,
    decode_response,
    encode_request,
    peek_message_type,
)
from mash_py.wire.payloads import Request

_log = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------


DEFAULT_REQUEST_TIMEOUT: float = 10.0
"""Default per-request timeout in seconds (``docs/interaction-model.md §8.3``)."""

_MAX_MESSAGE_ID: int = 0xFFFF_FFFF
"""Largest valid messageId (uint32 max). Wraparound restarts at 1; 0 is reserved
for notifications (``§3.5``)."""


#: Type of a notification handler. May be sync or async; the read loop awaits
#: the result if it's a coroutine. Handlers receive the decoded
#: :class:`Notification` (subscriptionId is implicit since it's the dispatch
#: key, but it's also on the message itself for clarity).
NotificationHandler = Union[
    Callable[[Notification], None],
    Callable[[Notification], Awaitable[None]],
]


# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------


class InteractionError(Exception):
    """Base class for interaction-layer errors."""


class InteractionClientClosedError(InteractionError):
    """The client (or its underlying connection) is closed; new requests
    cannot be sent and any in-flight ones have been cancelled."""


class InteractionTimeoutError(InteractionError):
    """A request did not receive its response within the configured timeout
    (default 10s per ``§8.3``). The connection is left open — callers
    decide whether to close per the spec's *SHOULD close on timeout*
    guidance."""


class StatusError(InteractionError):
    """The peer returned a non-success Status. Carries the structured
    fields so callers can branch on Status without parsing strings.

    Wire form for the optional message follows ``§8.2``: payload is
    ``{1: message}``, with key 1 omitted on success or when the device
    has no diagnostic to share.

    Not a frozen dataclass: Python sets ``__traceback__``/``__context__``
    on exception instances at raise time, which a frozen dataclass would
    refuse with :class:`FrozenInstanceError`. Plain ``__init__`` keeps
    the structured-field convenience without that pitfall.
    """

    def __init__(self, *, status: Status, message: str = "") -> None:
        self.status = status
        self.message = message
        super().__init__(self.__str__())

    def __str__(self) -> str:
        return (
            f"{self.status.name} ({int(self.status)})"
            + (f": {self.message}" if self.message else "")
        )

    def __repr__(self) -> str:
        return f"StatusError(status={self.status!r}, message={self.message!r})"


# ---------------------------------------------------------------------------
# Subscription handle
# ---------------------------------------------------------------------------


@dataclass
class Subscription:
    """Active subscription returned by :meth:`InteractionClient.subscribe`.

    Holds the assigned ``subscriptionId`` and the priming-report values
    (``§6.2``) so callers can establish a baseline before notifications
    start arriving.

    The handle is the canonical reference for unsubscription — callers
    pass it to :meth:`InteractionClient.unsubscribe` instead of
    remembering raw ids.
    """

    subscription_id: int
    endpoint_id: int
    feature_id: int
    initial_values: dict[int, Any]


# ---------------------------------------------------------------------------
# Client
# ---------------------------------------------------------------------------


class InteractionClient:
    """Send Read/Write/Subscribe/Invoke and route their responses.

    Lifecycle::

        conn = await connect(...)            # mash_py.transport
        client = InteractionClient(conn)
        client.start()                       # registers self as handler
        await client.read(0, 0x01)
        ...
        await client.close()                 # cancels pending, closes conn

    Concurrency: all public methods are coroutine-safe. The internal
    bookkeeping (pending map, subscriptions, message-id counter) is
    protected by a single :class:`asyncio.Lock` — contention is
    negligible because the read loop and most user code share one event
    loop, and the lock is held for O(1) work only.
    """

    def __init__(
        self,
        connection: Connection,
        *,
        request_timeout: float = DEFAULT_REQUEST_TIMEOUT,
    ) -> None:
        if request_timeout <= 0:
            raise ValueError(f"request_timeout must be > 0, got {request_timeout}")

        self._conn = connection
        self._timeout = request_timeout

        self._lock = asyncio.Lock()
        self._next_msg_id: int = 1  # spec §3.5: "Start at 1"
        self._pending: dict[int, asyncio.Future[Response]] = {}
        self._subscriptions: dict[int, NotificationHandler] = {}

        self._started: bool = False
        self._closed: bool = False
        # Settable from on_close so close() can fail-fast even if the
        # caller never reaches the lock.
        self._closed_event = asyncio.Event()

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------

    def start(self) -> None:
        """Register as the connection's handler and begin dispatching frames.

        Idempotent. May only be called before :meth:`close` — once
        closed, the client cannot be restarted (matching how
        :class:`Connection` is single-shot per DEC-040 reuse-only-fresh
        pattern).
        """
        if self._closed:
            raise InteractionClientClosedError("client is closed")
        if self._started:
            return
        self._started = True
        self._conn.start(self)

    async def close(self) -> None:
        """Tear down: fail all pending futures, drop subscriptions, close
        the underlying connection.

        Idempotent. Safe to call from anywhere — including from inside
        a notification handler, though the read loop may complete one
        more dispatch before the close takes effect.
        """
        if self._closed:
            return
        self._closed = True
        self._closed_event.set()

        # Snapshot under lock, then release — we don't want to hold the
        # lock across asyncio.Future.set_exception (which schedules
        # callbacks) or across Connection.close (which awaits the read
        # task).
        async with self._lock:
            pending = dict(self._pending)
            self._pending.clear()
            self._subscriptions.clear()

        for fut in pending.values():
            if not fut.done():
                fut.set_exception(InteractionClientClosedError("client closed"))

        # Closing the connection triggers its read loop to exit, which
        # in turn calls our on_close — which is a no-op past this point
        # because _closed is already True.
        with contextlib.suppress(Exception):
            await self._conn.close()

    @property
    def is_closed(self) -> bool:
        return self._closed

    # ------------------------------------------------------------------
    # Public operations (Read / Write / Subscribe / Unsubscribe / Invoke)
    # ------------------------------------------------------------------

    async def read(
        self,
        endpoint_id: int,
        feature_id: int,
        attribute_ids: list[int] | None = None,
        *,
        timeout: float | None = None,
    ) -> dict[int, Any]:
        """Read attributes from a feature.

        Per ``§4.3``: ``attribute_ids=None`` (or empty) means "read all".
        Returns the response payload as ``{attributeId: value}``. Raises
        :class:`StatusError` on non-success Status.
        """
        req = Request(
            message_id=0,  # filled in by _send
            operation=Operation.READ,
            endpoint_id=endpoint_id,
            feature_id=feature_id,
            payload=ReadPayload(attribute_ids=list(attribute_ids or [])),
        )
        resp = await self._send(req, timeout=timeout)
        return _coerce_int_keyed_map(resp.payload, "Read response")

    async def write(
        self,
        endpoint_id: int,
        feature_id: int,
        attributes: dict[int, Any],
        *,
        timeout: float | None = None,
    ) -> dict[int, Any]:
        """Write attributes (full replacement, ``§5.1``).

        Returns the resulting attribute values from the device — these
        may differ from the requested values (e.g. when a multi-zone
        limit is the most-restrictive of several writers; ``§5.2``).
        """
        if not isinstance(attributes, dict) or not attributes:
            raise ValueError("attributes must be a non-empty dict[int, Any]")
        # Encode as a bare CBOR map (key 5 = attributes-to-write, §5.1).
        # We trust the codec's _encode_payload fall-through for raw dicts.
        req = Request(
            message_id=0,
            operation=Operation.WRITE,
            endpoint_id=endpoint_id,
            feature_id=feature_id,
            payload=dict(attributes),
        )
        resp = await self._send(req, timeout=timeout)
        if resp.payload is None:
            return {}
        return _coerce_int_keyed_map(resp.payload, "Write response")

    async def subscribe(
        self,
        endpoint_id: int,
        feature_id: int,
        handler: NotificationHandler,
        *,
        attribute_ids: list[int] | None = None,
        min_interval_ms: int = 0,
        max_interval_ms: int = 0,
        timeout: float | None = None,
    ) -> Subscription:
        """Subscribe to attribute changes on a feature (``§6``).

        ``attribute_ids`` empty / ``None`` = subscribe to all attributes
        of the feature (DEC-052). ``min_interval_ms`` coalesces bursts;
        ``max_interval_ms`` enables heartbeat notifications.

        ``handler`` is invoked from the read loop for every notification
        with this subscription id; it may be sync or async.

        Returns a :class:`Subscription` carrying the priming-report
        values (``§6.2``).
        """
        if endpoint_id == 0 and feature_id == 0:
            # Reserved for unsubscribe (§6.4). Catching this client-side
            # surfaces the bug *before* sending a malformed Subscribe.
            raise ValueError("endpoint=0, feature=0 is reserved for unsubscribe")

        req = Request(
            message_id=0,
            operation=Operation.SUBSCRIBE,
            endpoint_id=endpoint_id,
            feature_id=feature_id,
            payload=SubscribePayload(
                attribute_ids=list(attribute_ids or []),
                min_interval=int(min_interval_ms),
                max_interval=int(max_interval_ms),
            ),
        )
        resp = await self._send(req, timeout=timeout)
        sub_id, initial = _parse_subscribe_response(resp.payload)

        async with self._lock:
            self._subscriptions[sub_id] = handler

        return Subscription(
            subscription_id=sub_id,
            endpoint_id=endpoint_id,
            feature_id=feature_id,
            initial_values=initial,
        )

    async def unsubscribe(
        self,
        subscription: Subscription | int,
        *,
        timeout: float | None = None,
    ) -> None:
        """Cancel a subscription (``§6.4``).

        Wire form: a Subscribe op with endpoint=0, feature=0, payload
        ``{1: subscriptionId}``. We deregister the local handler
        unconditionally — even if the device returns an error, we'll no
        longer route notifications for this id locally.
        """
        sub_id = (
            subscription.subscription_id
            if isinstance(subscription, Subscription)
            else int(subscription)
        )

        async with self._lock:
            self._subscriptions.pop(sub_id, None)

        req = Request(
            message_id=0,
            operation=Operation.SUBSCRIBE,
            endpoint_id=0,
            feature_id=0,
            payload=UnsubscribePayload(subscription_id=sub_id),
        )
        await self._send(req, timeout=timeout)

    async def invoke(
        self,
        endpoint_id: int,
        feature_id: int,
        command_id: int,
        parameters: Any = None,
        *,
        timeout: float | None = None,
    ) -> Any:
        """Invoke a command and return the response payload (``§7``).

        Returns the raw decoded payload (typically a CBOR map keyed by
        the command's response-parameter ids); the caller knows the
        feature/command shape. Raises :class:`StatusError` on
        non-success Status.
        """
        req = Request(
            message_id=0,
            operation=Operation.INVOKE,
            endpoint_id=endpoint_id,
            feature_id=feature_id,
            payload=InvokePayload(command_id=command_id, parameters=parameters),
        )
        resp = await self._send(req, timeout=timeout)
        return resp.payload

    # ------------------------------------------------------------------
    # ConnectionHandler protocol
    # ------------------------------------------------------------------

    async def on_message(self, data: bytes) -> None:
        """Dispatch one application-layer frame.

        Control frames never reach here — the Connection consumes them
        in its read loop per DEC-083 (every control frame begins with
        CBOR tag 77). So the only valid classifications are
        ``RESPONSE`` and ``NOTIFICATION``; ``REQUEST`` would mean a
        controller has somehow received an inbound request, which the
        bidirectional model (§11) supports but the Phase-11 client does
        not. We log and drop those rather than asserting, so a future
        bidirectional client can layer on top.
        """
        try:
            msg_type = peek_message_type(data)
        except (ValueError, KeyError) as exc:
            _log.warning("dropping unparseable frame (%d bytes): %r", len(data), exc)
            return

        if msg_type is MessageType.RESPONSE:
            await self._dispatch_response(data)
            return

        if msg_type is MessageType.NOTIFICATION:
            await self._dispatch_notification(data)
            return

        if msg_type is MessageType.REQUEST:
            _log.debug(
                "dropping inbound REQUEST frame (controller-side bidirectional "
                "is a Phase 11+ extension, not handled by InteractionClient)"
            )
            return

        # peek_message_type returns RESPONSE as the catch-all, so this
        # branch is theoretically unreachable. Log instead of raise to
        # keep the read loop alive.
        _log.warning("ignoring frame of unexpected type %s", msg_type)

    async def on_close(self, exc: BaseException | None) -> None:
        """The connection's read loop has exited. Fail all pending futures
        with :class:`InteractionClientClosedError` (or the underlying
        exception if there was one) so awaiting callers don't hang."""
        # Match close() semantics: if we initiated the shutdown, _closed
        # is already true and pending is already cleared. Otherwise this
        # is a remote-initiated or error close, and we need to surface it.
        if self._closed:
            return
        self._closed = True
        self._closed_event.set()

        async with self._lock:
            pending = dict(self._pending)
            self._pending.clear()
            self._subscriptions.clear()

        cause = exc if exc is not None else InteractionClientClosedError(
            "connection closed by peer"
        )
        for fut in pending.values():
            if not fut.done():
                fut.set_exception(cause)

    # ------------------------------------------------------------------
    # Internals
    # ------------------------------------------------------------------

    async def _send(
        self,
        req: Request,
        *,
        timeout: float | None,
    ) -> Response:
        """Allocate a message id, register a pending future, encode + send,
        await the response. Always cleans up the pending entry on exit.
        """
        if self._closed:
            raise InteractionClientClosedError("client is closed")

        loop = asyncio.get_running_loop()
        fut: asyncio.Future[Response] = loop.create_future()

        async with self._lock:
            if self._closed:
                raise InteractionClientClosedError("client is closed")
            msg_id = self._allocate_message_id_locked()
            req.message_id = msg_id
            self._pending[msg_id] = fut

        try:
            try:
                data = encode_request(req)
            except Exception:
                # Pop the pending entry and propagate the encode error.
                async with self._lock:
                    self._pending.pop(msg_id, None)
                raise

            try:
                await self._conn.send(data)
            except ConnectionClosedError as exc:
                async with self._lock:
                    self._pending.pop(msg_id, None)
                raise InteractionClientClosedError(str(exc)) from exc

            effective = self._timeout if timeout is None else timeout
            try:
                resp = await asyncio.wait_for(fut, timeout=effective)
            except TimeoutError as exc:
                raise InteractionTimeoutError(
                    f"request {msg_id} (op={req.operation.name}, "
                    f"endpoint={req.endpoint_id}, feature={req.feature_id}) "
                    f"timed out after {effective}s"
                ) from exc
        finally:
            async with self._lock:
                self._pending.pop(msg_id, None)

        if resp.status != Status.SUCCESS:
            raise StatusError(status=resp.status, message=_extract_error_message(resp.payload))
        return resp

    def _allocate_message_id_locked(self) -> int:
        """Return the next messageId. Caller must hold ``self._lock``.

        Per ``§3.5``: "Start at 1 ... increment monotonically ... on
        overflow, wrap to 1 (skipping 0)." mash-go pre-increments and
        emits ``2`` first; mash-py honours the spec literally (first
        request emits ``1``).
        """
        msg_id = self._next_msg_id
        nxt = msg_id + 1
        if nxt > _MAX_MESSAGE_ID:
            nxt = 1
        self._next_msg_id = nxt
        return msg_id

    async def _dispatch_response(self, data: bytes) -> None:
        try:
            resp = decode_response(data)
        except (ValueError, KeyError) as exc:
            _log.warning("malformed response frame: %r", exc)
            return

        async with self._lock:
            fut = self._pending.pop(resp.message_id, None)

        if fut is None:
            # mash-go's HandleResponse returns ErrUnexpectedReply here.
            # We log: an orphan response is a peer bug or a late delivery
            # for an already-timed-out request, neither of which we can
            # repair. Killing the read loop would amplify the damage.
            _log.warning(
                "received response for unknown messageId %d (status=%s); dropping",
                resp.message_id,
                resp.status.name,
            )
            return

        if not fut.done():
            fut.set_result(resp)

    async def _dispatch_notification(self, data: bytes) -> None:
        try:
            notif = decode_notification(data)
        except (ValueError, KeyError) as exc:
            _log.warning("malformed notification frame: %r", exc)
            return

        async with self._lock:
            handler = self._subscriptions.get(notif.subscription_id)

        if handler is None:
            # Per spec there is no "unsubscribe-ack" — notifications
            # arriving after a local unsubscribe are expected for at
            # most one minInterval window. Drop quietly.
            _log.debug(
                "no handler for subscriptionId %d (likely stale post-unsubscribe)",
                notif.subscription_id,
            )
            return

        try:
            result = handler(notif)
            if asyncio.iscoroutine(result):
                await result
        except Exception:
            _log.exception(
                "subscription handler for subscriptionId %d raised; continuing",
                notif.subscription_id,
            )


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _coerce_int_keyed_map(payload: Any, context: str) -> dict[int, Any]:
    """Convert a decoded CBOR map into ``dict[int, Any]`` with strict key checks.

    Read/Write responses (``§4.2``, ``§5.2``) are CBOR maps keyed by
    attribute id. cbor2 already gives us int keys for integer keys, but
    we re-validate so a peer that encodes ids as strings is rejected
    visibly rather than silently mis-keyed.
    """
    if payload is None:
        return {}
    if not isinstance(payload, dict):
        raise InteractionError(
            f"{context}: payload must be a CBOR map, got {type(payload).__name__}"
        )
    out: dict[int, Any] = {}
    for k, v in payload.items():
        if not isinstance(k, int) or isinstance(k, bool):
            raise InteractionError(f"{context}: non-int key {k!r}")
        out[k] = v
    return out


def _parse_subscribe_response(payload: Any) -> tuple[int, dict[int, Any]]:
    """Decode the Subscribe Response payload (``§6.2``).

    Wire form: ``{1: subscriptionId, 2: currentValues}``. ``currentValues``
    may be omitted by a stricter encoder when the priming report is
    empty; we tolerate that and return ``{}``.
    """
    if not isinstance(payload, dict):
        raise InteractionError(
            f"Subscribe response payload must be a map, got "
            f"{type(payload).__name__ if payload is not None else 'None'}"
        )
    sub_id_raw = payload.get(1)
    if not isinstance(sub_id_raw, int) or isinstance(sub_id_raw, bool):
        raise InteractionError(
            f"Subscribe response missing/invalid subscriptionId (key 1): {sub_id_raw!r}"
        )
    initial_raw = payload.get(2)
    if initial_raw is None:
        initial: dict[int, Any] = {}
    else:
        initial = _coerce_int_keyed_map(initial_raw, "Subscribe priming report")
    return sub_id_raw, initial


def _extract_error_message(payload: Any) -> str:
    """Pull the diagnostic string out of an error response payload (``§8.2``).

    The spec form is ``{1: message}``; older Go versions sometimes used
    a bare string. We accept both.
    """
    if payload is None:
        return ""
    if isinstance(payload, str):
        return payload
    if isinstance(payload, dict):
        msg = payload.get(1)
        if isinstance(msg, str):
            return msg
    return ""
