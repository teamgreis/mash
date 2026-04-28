"""Per-session subscription state + notification dispatch (Phase 16).

Implements the device-side half of ``docs/interaction-model.md §6`` and the
write-then-notify ordering invariant of ``docs/transport.md §8.4`` (DEC-071).

Architecture
------------

Two classes plus a polling-task lifecycle:

- :class:`Subscription` — one record per active subscription. Tracks the
  intervals, the values last delivered (for delta / bounce-back checks),
  and the pending-changes buffer accumulated during a coalescing window.

- :class:`SubscriptionManager` — per-session container. Owns the table of
  subscriptions for ONE connection (per ``§6.5`` "On reconnect:
  Subscriptions are lost; client must re-subscribe" — subscription state
  is per-connection, not per-device).

- :class:`NotificationDispatcher` — wires a :class:`SubscriptionManager`
  to a wire-level notification sender (``encode_notification`` + the
  connection's ``send``) and runs the periodic-flush task.

Spec-vs-Go decisions ratified for this phase (see Phase 16 plan + the
2026-04-27 design discussion):

- *Synchronous fan-out on Write* — when a Write commits, the dispatcher
  delivers any due notifications **before** the Write Response is sent
  on the writer's stream. This is the DEC-071 ordering guarantee. The
  polling task is reserved for heartbeats and coalescing-window
  expiries only.
- *Heartbeat content is always FULL* — `§6.5` requires the heartbeat
  to carry current values "(same format as priming report)". Go's
  ``HeartbeatEmpty`` mode is not implemented (it's a Go-internal
  variant that contradicts the spec).
- *Bounce-back suppression* — a value that equals the last-notified
  value is not emitted. The spec text "delta notifications, only
  changed attributes" supports this.
- *Defaults* — `minInterval=1000` ms, `maxInterval=60000` ms when
  unspecified or ``0``. ``maxInterval=0`` *explicitly* (i.e. asking
  for "no heartbeat ever") is rejected upstream by
  :class:`mash_py.interaction.OperationalServer` with
  ``Status.INVALID_PARAMETER``.
- *Priming* lives only in the Subscribe **Response** payload
  (``§6.2``). No separate priming Notification frame is emitted.
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
import threading
import time
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from typing import Any

_log = logging.getLogger(__name__)

__all__ = [
    "DEFAULT_MIN_INTERVAL_MS",
    "DEFAULT_MAX_INTERVAL_MS",
    "DEFAULT_PROCESSING_INTERVAL_MS",
    "MAX_SUBSCRIPTIONS_PER_SESSION",
    "MAX_ATTRIBUTES_PER_SUB",
    "DueNotification",
    "NotificationSender",
    "Subscription",
    "SubscriptionManager",
    "NotificationDispatcher",
    "ResourceExhausted",
    "InvalidInterval",
]


# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------


DEFAULT_MIN_INTERVAL_MS: int = 1000
"""Default ``minInterval`` per ``§6.1`` example. Applied when the
controller omits ``minInterval`` or sends ``0``."""

DEFAULT_MAX_INTERVAL_MS: int = 60000
"""Default ``maxInterval`` per ``§6.1`` example. Applied when the
controller omits ``maxInterval``. An *explicit* zero is rejected
upstream — see module docstring."""

DEFAULT_PROCESSING_INTERVAL_MS: int = 100
"""Polling cadence for the heartbeat / coalescing-window flush task.
Mirrors mash-go ``pkg/service/dispatch/notification_dispatcher.go:57``
and is small enough that a heartbeat targeted at maxInterval = 1 s
still fires within ~10 % of the requested interval."""

MAX_SUBSCRIPTIONS_PER_SESSION: int = 50
"""Per-session subscription cap (matches Go's ``DefaultMaxSubscriptions``).
A controller hitting this cap means it's leaking subscriptions; we'd
rather surface ``RESOURCE_EXHAUSTED`` than silently grow unbounded."""

MAX_ATTRIBUTES_PER_SUB: int = 100
"""Per-subscription attribute-list cap (matches Go's
``DefaultMaxAttributesPerSub``). Empty = "all attributes" and is not
counted against this limit."""


# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------


class SubscriptionError(Exception):
    """Base class for subscription errors raised inside the manager."""


class ResourceExhausted(SubscriptionError):
    """Raised when the per-session subscription cap would be exceeded."""


class InvalidInterval(SubscriptionError):
    """Raised when interval validation fails (e.g. ``maxInterval=0``)."""


# ---------------------------------------------------------------------------
# Data types
# ---------------------------------------------------------------------------


#: Wire-level notification sender. The dispatcher hands the subscription id,
#: endpoint id, feature id, and changed-attributes map; the sender is
#: responsible for building the :class:`mash_py.wire.Notification`,
#: encoding it, and writing it to the connection. Async to allow for
#: connection-level back-pressure.
NotificationSender = Callable[[int, int, int, dict[int, Any]], Awaitable[None]]


@dataclass
class DueNotification:
    """A notification that the manager has decided is ready to send.

    Returned by :meth:`SubscriptionManager.flush_due` so the caller (the
    dispatcher) can encode + send each one. We don't build the
    :class:`mash_py.wire.Notification` here so this module stays free
    of wire-codec dependencies — easier to unit-test and easier to
    drive from a mock sender.
    """

    subscription_id: int
    endpoint_id: int
    feature_id: int
    changes: dict[int, Any]


class Subscription:
    """One active subscription. Per-session state.

    Concurrency: methods are safe to call from a single asyncio event
    loop (Python's GIL plus single-event-loop semantics). The
    :class:`threading.Lock` here is defensive — most call sites are
    awaited from the same loop, but a polling task and a Write handler
    may interleave with the lock held briefly.
    """

    def __init__(
        self,
        sub_id: int,
        endpoint_id: int,
        feature_id: int,
        attribute_ids: list[int],
        min_interval_ms: int,
        max_interval_ms: int,
        priming_values: dict[int, Any],
    ) -> None:
        self.id = sub_id
        self.endpoint_id = endpoint_id
        self.feature_id = feature_id
        # Empty list is the §6.5 "subscribe to all" sentinel; we keep
        # it as `None` internally so the matching code reads naturally.
        self.attribute_filter: frozenset[int] | None = (
            frozenset(attribute_ids) if attribute_ids else None
        )
        self.min_interval_s: float = min_interval_ms / 1000.0
        self.max_interval_s: float = max_interval_ms / 1000.0

        self._lock = threading.Lock()
        # Last-notified values; pre-seeded with priming so the first
        # post-priming change actually delivers (and a bounce-back to
        # priming is correctly suppressed).
        self._last_values: dict[int, Any] = dict(priming_values)
        self._pending_changes: dict[int, Any] = {}
        self._change_window_start: float = 0.0
        # Anchor "last delivered" to the priming time so:
        # - A heartbeat won't fire until maxInterval after Subscribe.
        # - The first delta won't fire faster than minInterval after
        #   priming (matches §6.5 "minInterval coalesces simultaneous
        #   changes").
        self._last_notified_at: float = time.monotonic()

    # ------------------------------------------------------------------
    # Filter
    # ------------------------------------------------------------------

    def matches_attribute(self, attr_id: int) -> bool:
        """Whether this subscription cares about ``attr_id``.

        ``attribute_filter is None`` means "subscribe to all" per
        ``§6.5`` (DEC-052) — every attribute on the feature matches.
        """
        return self.attribute_filter is None or attr_id in self.attribute_filter

    # ------------------------------------------------------------------
    # Change accumulation
    # ------------------------------------------------------------------

    def record_change(self, attr_id: int, value: Any) -> None:
        """Buffer a change. ``minInterval`` is enforced at flush time
        relative to ``last_notified_at`` (§6.5: "Minimum time between
        notifications"), so individual record-change calls never block."""
        with self._lock:
            if not self._pending_changes:
                # Tracked for diagnostics / future "stale change" detection;
                # the actual coalescing decision uses last_notified_at so
                # `min interval between notifications` reads the way §6.5
                # describes it.
                self._change_window_start = time.monotonic()
            self._pending_changes[attr_id] = value

    # ------------------------------------------------------------------
    # Flush decisions
    # ------------------------------------------------------------------

    def take_pending_if_due(self, now: float) -> dict[int, Any] | None:
        """If at least ``minInterval`` has elapsed since the last
        delivery and there are pending changes, return the delivery
        set; otherwise return ``None``.

        We measure ``minInterval`` from ``last_notified_at`` rather than
        from the first change in the buffer — that's the natural
        reading of ``§6.5`` ("Minimum time between notifications") and
        keeps the DEC-071 write-then-notify path tight: a Write past
        the coalescing window delivers immediately rather than waiting
        for the next polling tick.

        Bounce-back suppression: any attribute whose value equals the
        last-notified value is dropped from the delivery set. If every
        change is a bounce-back the buffer is cleared and ``None`` is
        returned (no notification emitted).
        """
        with self._lock:
            if not self._pending_changes:
                return None
            elapsed = now - self._last_notified_at
            if elapsed < self.min_interval_s:
                return None

            delivery: dict[int, Any] = {}
            for attr_id, new_value in self._pending_changes.items():
                if attr_id in self._last_values and _safe_equal(
                    self._last_values[attr_id], new_value
                ):
                    # Bounce-back: same as last delivered — suppress.
                    continue
                delivery[attr_id] = new_value
                self._last_values[attr_id] = new_value

            self._pending_changes.clear()

            if not delivery:
                return None

            self._last_notified_at = now
            return delivery

    def heartbeat_due(self, now: float) -> bool:
        """Whether `maxInterval` has elapsed without any delivery."""
        with self._lock:
            return (now - self._last_notified_at) >= self.max_interval_s

    def heartbeat_snapshot(self, now: float) -> dict[int, Any]:
        """Return the current values for a heartbeat delivery and stamp
        the last-notified time. Heartbeat content is FULL per ``§6.5``."""
        with self._lock:
            self._last_notified_at = now
            # Copy so the caller can mutate / encode without contending.
            return dict(self._last_values)


# ---------------------------------------------------------------------------
# SubscriptionManager
# ---------------------------------------------------------------------------


# Lightweight read-back closures so the manager can fetch priming /
# heartbeat values without taking a hard dependency on
# ``mash_py.model.Device`` (keeps this module unit-testable).
ReadAttributeFn = Callable[[int, int, int], Any]
ReadAllAttributesFn = Callable[[int, int], dict[int, Any]]


class SubscriptionManager:
    """Per-session subscription table + change accumulator.

    The OperationalServer creates one manager per accepted connection.
    On a Write, the server calls :meth:`record_change` for every
    attribute that actually changed. After the Write completes (and
    before the Response is sent — DEC-071) the server calls
    :meth:`flush_due` and emits the resulting notifications.

    Lifecycle is decoupled from the polling task:
    :class:`NotificationDispatcher` owns the asyncio task and calls into
    the manager. This keeps the manager testable in pure-sync code.
    """

    def __init__(
        self,
        *,
        read_attribute: ReadAttributeFn,
        read_all_attributes: ReadAllAttributesFn,
    ) -> None:
        self._read_attribute = read_attribute
        self._read_all_attributes = read_all_attributes

        self._lock = threading.Lock()
        self._subscriptions: dict[int, Subscription] = {}
        # (endpoint_id, feature_id) -> set of subscription ids — index
        # for O(matches) dispatch on record_change.
        self._feature_index: dict[tuple[int, int], set[int]] = {}
        # Subscription id allocator. Starts at 1 (0 is reserved as the
        # notification message-id sentinel). Wraps into the uint32 space.
        self._next_id: int = 1

    # ------------------------------------------------------------------
    # Subscription lifecycle
    # ------------------------------------------------------------------

    def subscribe(
        self,
        endpoint_id: int,
        feature_id: int,
        attribute_ids: list[int] | None,
        min_interval_ms: int,
        max_interval_ms: int,
    ) -> tuple[int, dict[int, Any]]:
        """Register a subscription; return ``(subscription_id, priming_values)``.

        Caller is responsible for shape-checking the request (endpoint
        / feature exist; intervals validated). This method's invariants:

        - ``min_interval_ms <= max_interval_ms`` (after defaulting).
        - ``len(attribute_ids) <= MAX_ATTRIBUTES_PER_SUB`` (empty list
          counts as zero — "subscribe to all" is not capped).
        - At most :data:`MAX_SUBSCRIPTIONS_PER_SESSION` active
          subscriptions per session.
        """
        if max_interval_ms <= 0:
            raise InvalidInterval(
                "maxInterval must be > 0 (a 'no heartbeat' subscription "
                "is rejected per docs/interaction-model.md §6 — controllers "
                "should set a finite heartbeat for liveness)"
            )
        if min_interval_ms < 0:
            raise InvalidInterval("minInterval must be >= 0")
        if min_interval_ms > max_interval_ms:
            raise InvalidInterval(
                f"minInterval ({min_interval_ms}) > maxInterval ({max_interval_ms})"
            )

        ids = list(attribute_ids or [])
        if len(ids) > MAX_ATTRIBUTES_PER_SUB:
            raise InvalidInterval(
                f"too many attributes per subscription "
                f"(got {len(ids)}, max {MAX_ATTRIBUTES_PER_SUB})"
            )

        # Snapshot priming values *outside* the lock — the read goes
        # through user-supplied closures that may take their own locks.
        priming = self._snapshot_priming(endpoint_id, feature_id, ids)

        with self._lock:
            if len(self._subscriptions) >= MAX_SUBSCRIPTIONS_PER_SESSION:
                raise ResourceExhausted(
                    f"per-session subscription cap reached "
                    f"({MAX_SUBSCRIPTIONS_PER_SESSION})"
                )
            sub_id = self._allocate_id_locked()
            sub = Subscription(
                sub_id=sub_id,
                endpoint_id=endpoint_id,
                feature_id=feature_id,
                attribute_ids=ids,
                min_interval_ms=min_interval_ms,
                max_interval_ms=max_interval_ms,
                priming_values=priming,
            )
            self._subscriptions[sub_id] = sub
            key = (endpoint_id, feature_id)
            self._feature_index.setdefault(key, set()).add(sub_id)

        return sub_id, priming

    def unsubscribe(self, sub_id: int) -> bool:
        """Remove a subscription. Returns False if it didn't exist."""
        with self._lock:
            sub = self._subscriptions.pop(sub_id, None)
            if sub is None:
                return False
            key = (sub.endpoint_id, sub.feature_id)
            ids = self._feature_index.get(key)
            if ids is not None:
                ids.discard(sub_id)
                if not ids:
                    del self._feature_index[key]
            return True

    def clear(self) -> None:
        """Drop every subscription. Called on connection close."""
        with self._lock:
            self._subscriptions.clear()
            self._feature_index.clear()

    def count(self) -> int:
        with self._lock:
            return len(self._subscriptions)

    def get(self, sub_id: int) -> Subscription | None:
        with self._lock:
            return self._subscriptions.get(sub_id)

    # ------------------------------------------------------------------
    # Change recording
    # ------------------------------------------------------------------

    def record_change(
        self,
        endpoint_id: int,
        feature_id: int,
        attr_id: int,
        value: Any,
    ) -> None:
        """Buffer one attribute change for every matching subscription."""
        with self._lock:
            ids = self._feature_index.get((endpoint_id, feature_id))
            if not ids:
                return
            # Snapshot the matching subs while holding the lock; record
            # changes per-subscription outside the manager lock — each
            # Subscription has its own internal lock.
            subs = [
                self._subscriptions[s]
                for s in ids
                if s in self._subscriptions
            ]
        for sub in subs:
            if sub.matches_attribute(attr_id):
                sub.record_change(attr_id, value)

    def record_changes(
        self,
        endpoint_id: int,
        feature_id: int,
        changes: dict[int, Any],
    ) -> None:
        """Bulk-record. Convenience for Write handlers that committed
        multiple attributes in one request."""
        for attr_id, value in changes.items():
            self.record_change(endpoint_id, feature_id, attr_id, value)

    # ------------------------------------------------------------------
    # Flushing
    # ------------------------------------------------------------------

    def flush_due(self, now: float | None = None) -> list[DueNotification]:
        """Return every notification that is due *right now*.

        Iterates each subscription:
        - Take the pending changes if minInterval has elapsed.
        - If no pending and maxInterval has elapsed → heartbeat.

        Pending-changes flushes take precedence over heartbeats: a
        subscription with both pending changes AND a stale
        last-notified time emits the *changes* and refreshes the
        heartbeat clock, rather than emitting two consecutive
        notifications.
        """
        if now is None:
            now = time.monotonic()
        with self._lock:
            subs = list(self._subscriptions.values())

        out: list[DueNotification] = []
        for sub in subs:
            delivery = sub.take_pending_if_due(now)
            if delivery is not None:
                out.append(
                    DueNotification(
                        subscription_id=sub.id,
                        endpoint_id=sub.endpoint_id,
                        feature_id=sub.feature_id,
                        changes=delivery,
                    )
                )
                continue
            if sub.heartbeat_due(now):
                snapshot = sub.heartbeat_snapshot(now)
                out.append(
                    DueNotification(
                        subscription_id=sub.id,
                        endpoint_id=sub.endpoint_id,
                        feature_id=sub.feature_id,
                        changes=snapshot,
                    )
                )
        return out

    # ------------------------------------------------------------------
    # Internals
    # ------------------------------------------------------------------

    def _allocate_id_locked(self) -> int:
        # 0 is reserved as the notification message-id sentinel
        # (§3.3); skip it on wraparound. The cap is 2^32-1.
        sid = self._next_id
        nxt = sid + 1
        if nxt > 0xFFFF_FFFF:
            nxt = 1
        self._next_id = nxt
        return sid

    def _snapshot_priming(
        self,
        endpoint_id: int,
        feature_id: int,
        attribute_ids: list[int],
    ) -> dict[int, Any]:
        """Build the priming-report payload for a Subscribe Response.

        Empty filter → all attributes (DEC-052). Specific ids → only
        those that exist on the feature; missing ids are skipped per
        the lenient "controller has the attributeList from discovery"
        model (DEC-051) — we do NOT raise ``INVALID_ATTRIBUTE`` because
        Subscribe predates Read in a typical commissioning flow and
        the controller may legitimately probe with a stale id.
        """
        if not attribute_ids:
            try:
                return self._read_all_attributes(endpoint_id, feature_id)
            except Exception:  # pragma: no cover — defensive
                _log.exception(
                    "subscription priming: read_all failed (ep=%d fid=%d)",
                    endpoint_id,
                    feature_id,
                )
                return {}

        out: dict[int, Any] = {}
        for attr_id in attribute_ids:
            try:
                out[attr_id] = self._read_attribute(
                    endpoint_id, feature_id, attr_id
                )
            except Exception:
                # Missing-on-feature is silently dropped — see docstring.
                continue
        return out


# ---------------------------------------------------------------------------
# NotificationDispatcher
# ---------------------------------------------------------------------------


class NotificationDispatcher:
    """Bridges a :class:`SubscriptionManager` to a wire sender.

    Two delivery paths:

    - **Synchronous** (``flush_now()``): called from the Write handler
      *before* the Write Response is built, satisfying DEC-071's
      write-then-notify ordering invariant.
    - **Periodic** (the polling task started by :meth:`start`): runs
      every :data:`DEFAULT_PROCESSING_INTERVAL_MS` ms, picks up
      coalescing-window expiries (when a coalesced burst becomes due
      *between* writes) and heartbeat fires.

    The sender callback is responsible for encoding + sending the
    wire frame. It receives raw fields (sub-id, endpoint, feature,
    changes) and should NOT raise — failures must be logged at the
    sender; the dispatcher swallows exceptions per §8.4 ("delivery
    failure on a subscriber's stream does NOT block the write
    response").
    """

    def __init__(
        self,
        manager: SubscriptionManager,
        sender: NotificationSender,
        *,
        processing_interval_ms: int = DEFAULT_PROCESSING_INTERVAL_MS,
    ) -> None:
        self._manager = manager
        self._sender = sender
        self._interval = max(1, processing_interval_ms) / 1000.0
        self._task: asyncio.Task[None] | None = None
        self._stopping = asyncio.Event()

    @property
    def manager(self) -> SubscriptionManager:
        return self._manager

    def start(self) -> None:
        """Start the polling task. Idempotent."""
        if self._task is not None:
            return
        self._stopping.clear()
        self._task = asyncio.create_task(
            self._run_loop(), name="mash-subscription-flush"
        )

    async def stop(self) -> None:
        """Stop the polling task. Drains a final flush so any pending
        deliveries that are due on close still go out (best effort)."""
        self._stopping.set()
        task = self._task
        self._task = None
        if task is not None:
            task.cancel()
            with contextlib.suppress(asyncio.CancelledError, Exception):
                await task

    async def flush_now(self) -> int:
        """Synchronously deliver every currently-due notification.

        Returns the count delivered. Errors from the sender are logged
        and counted as not-delivered, but otherwise swallowed (DEC-071).
        """
        due = self._manager.flush_due()
        if not due:
            return 0
        delivered = 0
        for n in due:
            try:
                await self._sender(
                    n.subscription_id,
                    n.endpoint_id,
                    n.feature_id,
                    n.changes,
                )
                delivered += 1
            except Exception:
                _log.exception(
                    "notification dispatch failed (sub=%d ep=%d fid=%d) — "
                    "dropping per §8.4 'failure MUST NOT block write response'",
                    n.subscription_id,
                    n.endpoint_id,
                    n.feature_id,
                )
        return delivered

    # ------------------------------------------------------------------
    # Internals
    # ------------------------------------------------------------------

    async def _run_loop(self) -> None:
        # Loop until stop() signals. We use wait_for on the stop-event
        # rather than asyncio.sleep so cancellation is prompt.
        try:
            while not self._stopping.is_set():
                try:
                    await asyncio.wait_for(
                        self._stopping.wait(), timeout=self._interval
                    )
                    return  # stop signalled
                except TimeoutError:
                    pass
                await self.flush_now()
        except asyncio.CancelledError:
            # Drain a final flush on cancellation so heartbeats / pending
            # deliveries due at the moment of close still go out.
            with contextlib.suppress(Exception):
                await self.flush_now()
            raise


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _safe_equal(a: Any, b: Any) -> bool:
    """Equality test that tolerates non-comparable types (mirrors
    :func:`mash_py.model.feature._safe_equal`).

    Anything that raises during comparison is "not equal" — the
    conservative choice, since a comparison that throws on a
    collection is more likely to mean "value mutated in place" than
    "values equal".
    """
    try:
        return bool(a == b)
    except Exception:  # pragma: no cover — defensive
        return False
