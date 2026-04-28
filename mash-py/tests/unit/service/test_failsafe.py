"""Unit tests for ``mash_py.service.failsafe``.

Covers the per-zone failsafe-timer state machine. Real-time durations
would make tests slow; we drive the timer with sub-validation
durations (``validate_duration=False``) and ``asyncio.sleep`` to
exercise transitions in milliseconds.

Spec sources:
- ``docs/multi-zone.md §7`` — per-zone semantics.
- ``docs/testing/behavior/failsafe-timing.md`` — state transitions,
  timer accuracy, grace period.
"""

from __future__ import annotations

import asyncio

import pytest

from mash_py.service.failsafe import (
    DEFAULT_DURATION,
    DEFAULT_GRACE_PERIOD,
    MAX_DURATION,
    MIN_DURATION,
    FailsafeLimits,
    FailsafeState,
    FailsafeTimer,
    InvalidDurationError,
    calculate_accuracy,
)


_async = pytest.mark.asyncio


# ---------------------------------------------------------------------------
# Construction + validation
# ---------------------------------------------------------------------------


@_async
class TestConstruction:
    async def test_defaults(self) -> None:
        timer = FailsafeTimer()
        assert timer.state is FailsafeState.NORMAL
        assert timer.duration == DEFAULT_DURATION
        assert timer.is_failsafe is False
        assert timer.is_timer_running is False

    async def test_validates_duration_below_min(self) -> None:
        with pytest.raises(InvalidDurationError):
            FailsafeTimer(duration=60.0)  # below MIN_DURATION (7200)

    async def test_validates_duration_above_max(self) -> None:
        with pytest.raises(InvalidDurationError):
            FailsafeTimer(duration=MAX_DURATION + 1.0)

    async def test_validation_can_be_bypassed_for_testing(self) -> None:
        # Sub-second durations are how we keep these tests fast — but
        # only valid via the explicit bypass.
        timer = FailsafeTimer(duration=0.05, validate_duration=False)
        assert timer.duration == 0.05

    async def test_set_duration_validates(self) -> None:
        timer = FailsafeTimer()
        with pytest.raises(InvalidDurationError):
            timer.set_duration(60.0)
        timer.set_duration(MIN_DURATION)
        assert timer.duration == MIN_DURATION


# ---------------------------------------------------------------------------
# State machine
# ---------------------------------------------------------------------------


@_async
class TestStateMachine:
    async def test_start_transitions_normal_to_running(self) -> None:
        timer = FailsafeTimer(duration=10.0, validate_duration=False)
        timer.start()
        assert timer.state is FailsafeState.TIMER_RUNNING
        assert timer.is_timer_running

    async def test_start_is_idempotent_when_already_running(self) -> None:
        timer = FailsafeTimer(duration=10.0, validate_duration=False)
        timer.start()
        # Second start should be a no-op (no exception, no extra timer)
        timer.start()
        assert timer.state is FailsafeState.TIMER_RUNNING
        timer.reset()

    async def test_timer_fires_into_failsafe(self) -> None:
        """After ``duration`` elapses, the timer enters FAILSAFE."""
        timer = FailsafeTimer(duration=0.05, validate_duration=False)
        timer.start()
        await asyncio.sleep(0.1)
        assert timer.state is FailsafeState.FAILSAFE
        assert timer.is_failsafe

    async def test_stop_cancels_running_timer(self) -> None:
        """A running timer that's stopped before duration goes back to
        NORMAL — the failsafe-entry callback must not fire."""
        timer = FailsafeTimer(duration=0.2, validate_duration=False)
        entered = asyncio.Event()
        timer.on_failsafe_enter(lambda _limits: entered.set())

        timer.start()
        await asyncio.sleep(0.05)
        timer.stop()

        # Wait long enough for the original deadline; the callback
        # must not fire.
        await asyncio.sleep(0.25)
        assert timer.state is FailsafeState.NORMAL
        assert entered.is_set() is False

    async def test_stop_from_failsafe_enters_grace_period(self) -> None:
        timer = FailsafeTimer(
            duration=0.05,
            grace_period=0.2,
            validate_duration=False,
        )
        timer.start()
        await asyncio.sleep(0.1)
        assert timer.state is FailsafeState.FAILSAFE
        timer.stop()
        assert timer.state is FailsafeState.GRACE_PERIOD

    async def test_grace_period_expires_to_normal(self) -> None:
        timer = FailsafeTimer(
            duration=0.05,
            grace_period=0.05,
            validate_duration=False,
        )
        timer.start()
        await asyncio.sleep(0.1)
        timer.stop()
        assert timer.state is FailsafeState.GRACE_PERIOD
        await asyncio.sleep(0.1)
        assert timer.state is FailsafeState.NORMAL

    async def test_stop_with_zero_grace_skips_grace(self) -> None:
        timer = FailsafeTimer(
            duration=0.05,
            grace_period=0.0,
            validate_duration=False,
        )
        timer.start()
        await asyncio.sleep(0.1)
        assert timer.state is FailsafeState.FAILSAFE
        timer.stop()
        assert timer.state is FailsafeState.NORMAL

    async def test_reset_cancels_unconditionally(self) -> None:
        """``reset()`` must work from any state and not fire any
        callbacks (it's a wipe-and-go operation, not a state change
        with semantic meaning)."""
        timer = FailsafeTimer(duration=0.05, validate_duration=False)

        exit_count = 0

        def _exit() -> None:
            nonlocal exit_count
            exit_count += 1

        timer.on_failsafe_exit(_exit)

        timer.start()
        await asyncio.sleep(0.1)
        assert timer.state is FailsafeState.FAILSAFE
        timer.reset()
        assert timer.state is FailsafeState.NORMAL
        # Reset deliberately does NOT fire on_failsafe_exit (the timer
        # is being torn down, not exited under normal operation).
        assert exit_count == 0


# ---------------------------------------------------------------------------
# Callbacks
# ---------------------------------------------------------------------------


@_async
class TestCallbacks:
    async def test_on_failsafe_enter_receives_limits(self) -> None:
        limits = FailsafeLimits(consumption_limit=3000, production_limit=2000)
        timer = FailsafeTimer(
            duration=0.05,
            limits=limits,
            validate_duration=False,
        )
        received: list[FailsafeLimits] = []
        timer.on_failsafe_enter(lambda lim: received.append(lim))

        timer.start()
        await asyncio.sleep(0.1)
        assert len(received) == 1
        assert received[0].consumption_limit == 3000
        assert received[0].production_limit == 2000

    async def test_on_failsafe_exit_fires_on_stop_from_failsafe(self) -> None:
        timer = FailsafeTimer(
            duration=0.05,
            grace_period=0.0,
            validate_duration=False,
        )
        exit_called = asyncio.Event()
        timer.on_failsafe_exit(lambda: exit_called.set())

        timer.start()
        await asyncio.sleep(0.1)
        timer.stop()
        await asyncio.sleep(0)
        assert exit_called.is_set()

    async def test_on_state_change_records_old_and_new(self) -> None:
        timer = FailsafeTimer(duration=0.05, validate_duration=False)
        transitions: list[tuple[FailsafeState, FailsafeState]] = []

        def _record(old: FailsafeState, new: FailsafeState) -> None:
            transitions.append((old, new))

        timer.on_state_change(_record)
        timer.start()
        await asyncio.sleep(0.1)

        # NORMAL → TIMER_RUNNING → FAILSAFE
        assert (FailsafeState.NORMAL, FailsafeState.TIMER_RUNNING) in transitions
        assert (FailsafeState.TIMER_RUNNING, FailsafeState.FAILSAFE) in transitions

    async def test_async_callback_supported(self) -> None:
        timer = FailsafeTimer(duration=0.05, validate_duration=False)
        seen = asyncio.Event()

        async def _on_enter(_limits: FailsafeLimits) -> None:
            await asyncio.sleep(0)  # yield control
            seen.set()

        timer.on_failsafe_enter(_on_enter)
        timer.start()
        # Wait for both: the timer to fire (0.05s) AND the async
        # callback's task to schedule + complete (one tick).
        await asyncio.sleep(0.15)
        assert seen.is_set()


# ---------------------------------------------------------------------------
# Remaining time
# ---------------------------------------------------------------------------


@_async
class TestRemaining:
    async def test_remaining_zero_when_not_running(self) -> None:
        timer = FailsafeTimer()
        assert timer.remaining() == 0.0

    async def test_remaining_decreases_while_running(self) -> None:
        timer = FailsafeTimer(duration=1.0, validate_duration=False)
        timer.start()
        first = timer.remaining()
        await asyncio.sleep(0.1)
        second = timer.remaining()
        assert second < first
        assert second > 0
        timer.reset()


# ---------------------------------------------------------------------------
# Accuracy helper (sync — opt out of the module-level asyncio mark)
# ---------------------------------------------------------------------------


def test_calculate_accuracy_uses_60s_floor() -> None:
    """For short durations, the absolute floor (60 s) dominates."""
    # 1% of 1 hour = 36 s, below the 60 s floor → 60 s
    assert calculate_accuracy(3600.0) == 60.0


def test_calculate_accuracy_uses_one_percent_for_long_durations() -> None:
    # 1% of 24 hours = 864 s, above the 60 s floor → 864 s
    assert calculate_accuracy(86400.0) == pytest.approx(864.0)


def test_calculate_accuracy_at_break_even() -> None:
    # 1% of 6000 s = 60 s == floor: either branch gives the same
    # answer, but the implementation picks 60 (floor) because the
    # strict-greater check fails.
    assert calculate_accuracy(6000.0) == 60.0
