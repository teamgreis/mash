"""Per-zone failsafe timer (Phase 20).

Mirrors ``mash-go/pkg/failsafe/timer.go``. One :class:`FailsafeTimer`
instance per zone; the device service holds them in a
``dict[zone_id -> FailsafeTimer]``.

State machine
-------------

::

    NORMAL  ──Start()──►  TIMER_RUNNING  ──duration elapsed──►  FAILSAFE
       ▲                       │                                   │
       │                    Stop()                              Stop()
       │                    Reset()                                 │
       └─── grace expires ◄───┴──── enters GRACE_PERIOD ────────────┘
                                    (only if was FAILSAFE)

- ``Start()``: NORMAL → TIMER_RUNNING. Idempotent: a second Start while
  already running or in failsafe is a no-op.
- ``Stop()``: cancels timers; if was FAILSAFE and a grace period is
  configured, transitions to GRACE_PERIOD; otherwise to NORMAL.
- ``Reset()``: hard reset to NORMAL, cancelling any running timer
  unconditionally. Used when a zone is removed entirely.

Spec sources
------------

- ``docs/multi-zone.md §7`` — per-zone failsafe semantics.
- ``docs/testing/behavior/failsafe-timing.md`` — timing requirements,
  including the §5 patches landed alongside this phase that aligned the
  per-zone vs all-zones contradiction in favour of per-zone.
- ``DEC-026`` — EnergyControl ``failsafeDuration`` (2-24h, ``uint32``
  seconds). The 7200/86400 constants below are seconds, matching Go.

Note on duration: the Go API takes ``time.Duration`` (nanoseconds);
the Python API uses **seconds** as ``float``. Conversion at the wire
boundary (CBOR uint32 seconds) is the caller's responsibility.
"""

from __future__ import annotations

import asyncio
import enum
import logging
import time
from collections.abc import Awaitable, Callable
from dataclasses import dataclass


__all__ = [
    "ACCURACY_ABSOLUTE",
    "ACCURACY_PERCENT",
    "DEFAULT_DURATION",
    "DEFAULT_GRACE_PERIOD",
    "FailsafeLimits",
    "FailsafeState",
    "FailsafeTimer",
    "FailsafeTimerError",
    "InvalidDurationError",
    "MAX_DURATION",
    "MIN_DURATION",
    "calculate_accuracy",
]


_log = logging.getLogger(__name__)


# Per docs/features/energy-control.md "Validation Rules" + DEC-026:
# failsafeDuration valid range is 7200–86400 seconds (2–24 hours).
MIN_DURATION: float = 7200.0
MAX_DURATION: float = 86400.0

# Default duration mid-range: 4 hours. Mirrors mash-go.
DEFAULT_DURATION: float = 4 * 3600.0

# Default grace period after reconnection: 5 minutes. Mirrors mash-go.
DEFAULT_GRACE_PERIOD: float = 300.0

# Timer accuracy: ±1% or ±60 s, whichever is greater (failsafe-timing.md §4.1).
ACCURACY_PERCENT: float = 1.0
ACCURACY_ABSOLUTE: float = 60.0


# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------


class FailsafeTimerError(Exception):
    """Base class for failsafe-timer errors."""


class InvalidDurationError(FailsafeTimerError):
    """Raised when a configured duration is outside the [MIN, MAX] range."""


# ---------------------------------------------------------------------------
# State
# ---------------------------------------------------------------------------


class FailsafeState(enum.Enum):
    """Per-zone failsafe state.

    Mirrors ``failsafe.State`` in mash-go. The string values are the
    same human-readable form Go emits in events / telemetry, so log
    output stays consistent across implementations.
    """

    NORMAL = "NORMAL"
    TIMER_RUNNING = "TIMER_RUNNING"
    FAILSAFE = "FAILSAFE"
    GRACE_PERIOD = "GRACE_PERIOD"


# ---------------------------------------------------------------------------
# Limits
# ---------------------------------------------------------------------------


@dataclass(slots=True)
class FailsafeLimits:
    """Failsafe power limits (positive values per energy-control.md).

    Either or both may be ``None`` to mean "no limit" / "not
    applicable" — for a one-direction device (e.g. a sub-meter or PV
    string) only the relevant direction's limit is set.
    """

    consumption_limit: int | None = None
    production_limit: int | None = None


# ---------------------------------------------------------------------------
# Timer
# ---------------------------------------------------------------------------


_StateChangeCallback = Callable[
    [FailsafeState, FailsafeState], Awaitable[None] | None
]
_FailsafeEnterCallback = Callable[[FailsafeLimits], Awaitable[None] | None]
_FailsafeExitCallback = Callable[[], Awaitable[None] | None]


class FailsafeTimer:
    """Per-zone failsafe timer.

    Async-friendly: timers are scheduled via :func:`asyncio.get_event_loop().call_later`
    (or ``call_at`` for monotonic precision). Callbacks may be sync or
    async; if async, they are scheduled via :func:`asyncio.create_task`
    so the timer's own deadline-handling task does not block.

    Construction does NOT start the timer — the caller is responsible
    for calling :meth:`start` at the appropriate event (zone connection
    loss, in mash-go's case).
    """

    def __init__(
        self,
        *,
        duration: float = DEFAULT_DURATION,
        grace_period: float = DEFAULT_GRACE_PERIOD,
        limits: FailsafeLimits | None = None,
        validate_duration: bool = True,
    ) -> None:
        """Create a timer.

        ``validate_duration`` may be set to False to allow arbitrary
        durations for testing — mirrors Go's ``NewTestTimer``. The
        production constructor (and the public API surface that the
        device service calls) keeps validation on.
        """
        if validate_duration and not (MIN_DURATION <= duration <= MAX_DURATION):
            raise InvalidDurationError(
                f"duration {duration} s outside valid range "
                f"[{MIN_DURATION}, {MAX_DURATION}]"
            )

        self._state: FailsafeState = FailsafeState.NORMAL
        self._duration: float = duration
        self._grace_period: float = grace_period
        self._limits: FailsafeLimits = limits or FailsafeLimits()

        self._failsafe_handle: asyncio.TimerHandle | None = None
        self._grace_handle: asyncio.TimerHandle | None = None
        self._started_at: float = 0.0

        self._on_state_change: _StateChangeCallback | None = None
        self._on_failsafe_enter: _FailsafeEnterCallback | None = None
        self._on_failsafe_exit: _FailsafeExitCallback | None = None

    # ------------------------------------------------------------------
    # State accessors
    # ------------------------------------------------------------------

    @property
    def state(self) -> FailsafeState:
        return self._state

    @property
    def is_failsafe(self) -> bool:
        return self._state is FailsafeState.FAILSAFE

    @property
    def is_timer_running(self) -> bool:
        return self._state is FailsafeState.TIMER_RUNNING

    @property
    def duration(self) -> float:
        return self._duration

    @property
    def limits(self) -> FailsafeLimits:
        return self._limits

    def remaining(self) -> float:
        """Seconds remaining on the active failsafe timer. ``0.0`` if
        not in TIMER_RUNNING.
        """
        if self._state is not FailsafeState.TIMER_RUNNING:
            return 0.0
        elapsed = time.monotonic() - self._started_at
        remaining = self._duration - elapsed
        return remaining if remaining > 0 else 0.0

    # ------------------------------------------------------------------
    # Configuration
    # ------------------------------------------------------------------

    def set_duration(self, duration: float) -> None:
        """Update the configured duration. Validates against [MIN, MAX].

        Has no effect on a currently-running timer — call :meth:`reset`
        first if you need the new duration to take effect immediately.
        """
        if not (MIN_DURATION <= duration <= MAX_DURATION):
            raise InvalidDurationError(
                f"duration {duration} s outside valid range "
                f"[{MIN_DURATION}, {MAX_DURATION}]"
            )
        self._duration = duration

    def set_limits(self, limits: FailsafeLimits) -> None:
        """Update the failsafe limits applied on FAILSAFE entry."""
        self._limits = limits

    # ------------------------------------------------------------------
    # State transitions
    # ------------------------------------------------------------------

    def start(self) -> None:
        """Move from NORMAL to TIMER_RUNNING. Idempotent if already
        running or already in FAILSAFE."""
        if self._state in (
            FailsafeState.TIMER_RUNNING,
            FailsafeState.FAILSAFE,
        ):
            return

        old = self._state
        self._state = FailsafeState.TIMER_RUNNING
        self._started_at = time.monotonic()

        loop = asyncio.get_event_loop()
        self._failsafe_handle = loop.call_later(
            self._duration, self._enter_failsafe
        )

        self._fire_state_change(old, self._state)

    def stop(self) -> None:
        """Cancel the active timer.

        Behaviour depends on current state:
        - TIMER_RUNNING → NORMAL (cancel the failsafe-entry deadline)
        - FAILSAFE → GRACE_PERIOD (start grace timer) if grace>0,
          otherwise NORMAL
        - NORMAL / GRACE_PERIOD → no-op
        """
        if self._state is FailsafeState.NORMAL:
            return
        if self._state is FailsafeState.GRACE_PERIOD:
            return

        old = self._state
        was_failsafe = self._state is FailsafeState.FAILSAFE

        if self._failsafe_handle is not None:
            self._failsafe_handle.cancel()
            self._failsafe_handle = None

        if was_failsafe and self._grace_period > 0:
            self._state = FailsafeState.GRACE_PERIOD
            loop = asyncio.get_event_loop()
            self._grace_handle = loop.call_later(
                self._grace_period, self._exit_grace_period
            )
        else:
            self._state = FailsafeState.NORMAL

        self._fire_state_change(old, self._state)
        if was_failsafe and self._on_failsafe_exit is not None:
            self._dispatch(self._on_failsafe_exit)

    def reset(self) -> None:
        """Hard-reset to NORMAL, cancelling any active timer or grace
        period. Does NOT fire the failsafe-exit callback even if the
        timer was in FAILSAFE — :meth:`reset` is for "wipe state, no
        further action" scenarios (zone removal, service shutdown)."""
        old = self._state

        if self._failsafe_handle is not None:
            self._failsafe_handle.cancel()
            self._failsafe_handle = None
        if self._grace_handle is not None:
            self._grace_handle.cancel()
            self._grace_handle = None

        self._state = FailsafeState.NORMAL
        self._started_at = 0.0

        if old is not FailsafeState.NORMAL:
            self._fire_state_change(old, self._state)

    # ------------------------------------------------------------------
    # Callback registration
    # ------------------------------------------------------------------

    def on_state_change(self, fn: _StateChangeCallback) -> None:
        self._on_state_change = fn

    def on_failsafe_enter(self, fn: _FailsafeEnterCallback) -> None:
        self._on_failsafe_enter = fn

    def on_failsafe_exit(self, fn: _FailsafeExitCallback) -> None:
        self._on_failsafe_exit = fn

    # ------------------------------------------------------------------
    # Internals
    # ------------------------------------------------------------------

    def _enter_failsafe(self) -> None:
        """Called when the TIMER_RUNNING countdown reaches zero."""
        if self._state is not FailsafeState.TIMER_RUNNING:
            # Defensive: a stale callback from a cancelled timer can
            # still fire if cancel() races with the loop's bookkeeping.
            return

        old = self._state
        self._state = FailsafeState.FAILSAFE
        self._failsafe_handle = None

        self._fire_state_change(old, self._state)
        if self._on_failsafe_enter is not None:
            self._dispatch(self._on_failsafe_enter, self._limits)

    def _exit_grace_period(self) -> None:
        """Called when the GRACE_PERIOD timer reaches zero."""
        if self._state is not FailsafeState.GRACE_PERIOD:
            return

        old = self._state
        self._state = FailsafeState.NORMAL
        self._grace_handle = None

        self._fire_state_change(old, self._state)

    def _fire_state_change(
        self, old: FailsafeState, new: FailsafeState
    ) -> None:
        if self._on_state_change is not None:
            self._dispatch(self._on_state_change, old, new)

    @staticmethod
    def _dispatch(fn: Callable, *args: object) -> None:
        """Invoke ``fn(*args)``. If it returns a coroutine, schedule it
        as a background task. Errors are logged so a flaky callback
        cannot wedge the timer's bookkeeping."""
        try:
            result = fn(*args)
        except Exception:  # pragma: no cover — defensive
            _log.exception("failsafe callback raised synchronously")
            return
        if asyncio.iscoroutine(result):
            asyncio.create_task(_swallow_callback_errors(result))


async def _swallow_callback_errors(coro: Awaitable[None]) -> None:
    """Await ``coro`` and log any exception. Async failsafe callbacks
    must not propagate — they run in a task we don't have a handle for,
    so an uncaught exception would only surface as an asyncio warning.
    """
    try:
        await coro
    except Exception:  # pragma: no cover — defensive
        _log.exception("failsafe async callback raised")


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def calculate_accuracy(duration: float) -> float:
    """Acceptable timer accuracy for ``duration`` (seconds).

    Per ``failsafe-timing.md §4.1``: ±1% or ±60 s, whichever is greater.
    Used by tests asserting timer fires within tolerance.
    """
    percent_acc = duration * (ACCURACY_PERCENT / 100.0)
    return percent_acc if percent_acc > ACCURACY_ABSOLUTE else ACCURACY_ABSOLUTE
