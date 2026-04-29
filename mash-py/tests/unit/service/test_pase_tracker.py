"""Unit tests for :class:`mash_py.service.pase_tracker.PASEAttemptTracker`.

Mirrors mash-go's ``pkg/service/pase_tracker_test.go`` — same scenarios,
async-flavoured for the Python port.
"""

from __future__ import annotations

import asyncio

import pytest

from mash_py.service.pase_tracker import (
    DEFAULT_BACKOFF_TIERS,
    PASEAttemptTracker,
)


@pytest.mark.asyncio
async def test_get_delay_tier_progression() -> None:
    """Each tier kicks in at the documented attempt boundaries."""
    tracker = PASEAttemptTracker(tiers=(0.0, 1.0, 3.0, 10.0))

    # Attempts 1-3 → tier 0
    for _ in range(3):
        assert await tracker.get_delay() == 0.0
        await tracker.record_failure()

    # Attempts 4-6 → tier 1
    for _ in range(3):
        assert await tracker.get_delay() == 1.0
        await tracker.record_failure()

    # Attempts 7-10 → tier 2
    for _ in range(4):
        assert await tracker.get_delay() == 3.0
        await tracker.record_failure()

    # Attempts 11+ → tier 3
    for _ in range(3):
        assert await tracker.get_delay() == 10.0
        await tracker.record_failure()


@pytest.mark.asyncio
async def test_record_failure_increments_counter() -> None:
    tracker = PASEAttemptTracker()
    assert await tracker.attempt_count() == 0
    await tracker.record_failure()
    assert await tracker.attempt_count() == 1
    await tracker.record_failure()
    await tracker.record_failure()
    assert await tracker.attempt_count() == 3


@pytest.mark.asyncio
async def test_reset_clears_counter() -> None:
    tracker = PASEAttemptTracker()
    for _ in range(5):
        await tracker.record_failure()
    assert await tracker.attempt_count() == 5

    await tracker.reset()
    assert await tracker.attempt_count() == 0
    # And the next attempt is back at tier 0.
    assert await tracker.get_delay() == DEFAULT_BACKOFF_TIERS[0]


@pytest.mark.asyncio
async def test_concurrent_record_and_query_is_consistent() -> None:
    """Concurrent recorders + queriers don't race the counter."""
    tracker = PASEAttemptTracker()

    async def hammer() -> None:
        for _ in range(50):
            await tracker.record_failure()
            await tracker.get_delay()

    await asyncio.gather(*(hammer() for _ in range(4)))
    # 4 tasks * 50 iterations each = 200 failures.
    assert await tracker.attempt_count() == 200


@pytest.mark.asyncio
async def test_default_tiers_match_spec() -> None:
    """Spec § 5.5 / DEC-047: 0/1/3/10 seconds across the four tiers."""
    assert DEFAULT_BACKOFF_TIERS == (0.0, 1.0, 3.0, 10.0)
    tracker = PASEAttemptTracker()
    for _ in range(3):
        assert await tracker.get_delay() == 0.0
        await tracker.record_failure()
    assert await tracker.get_delay() == 1.0  # tier 2
