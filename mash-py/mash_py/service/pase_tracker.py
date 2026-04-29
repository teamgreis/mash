"""PASE attempt backoff tracker (DEC-047 / commissioning-pase.md §5.5).

Mirrors :go:`mash-go/pkg/service.PASEAttemptTracker`. The setup code carries
~27 bits of entropy (8 decimal digits = 10^8 values), so without rate-limiting
on failed attempts a stolen-verifier attacker would brute-force the original
code in seconds. The backoff tiers exist to raise the per-attempt cost so that
an opportunistic attacker is held to bounded throughput while a fumbling
legitimate user is barely inconvenienced::

    Attempts 1-3:  no delay        (normal mistakes)
    Attempts 4-6:  1000 ms         (possible attack)
    Attempts 7-10: 3000 ms         (likely attack)
    Attempts 11+:  10000 ms        (definite attack)

The counter resets when the commissioning window closes (operator regained
physical control) or a PASE handshake completes successfully (legitimate user
got it right; second-controller commissioning in the same session deserves a
fresh budget).

Counter scope is service-wide (one tracker per device, not per peer). Per-peer
tracking is trivially defeated by IP rotation; the global counter throttles all
clients uniformly during an active attack at the cost of slightly delaying a
coincidentally-fumbling legitimate user.

Concurrency: backed by :class:`asyncio.Lock`. Every public method acquires the
lock so the counter and tier evaluation are consistent under concurrent
handlers (matches mash-go's :go:`sync.Mutex`).
"""

from __future__ import annotations

import asyncio
from typing import Final


# Tier delays in seconds (DEC-047 / commissioning-pase.md §5.5). Indexed by tier
# 0..3, mapping to attempt-count buckets (1-3, 4-6, 7-10, 11+).
DEFAULT_BACKOFF_TIERS: Final[tuple[float, float, float, float]] = (
    0.0,
    1.0,
    3.0,
    10.0,
)


class PASEAttemptTracker:
    """Per-device tracker for failed PASE attempts.

    Args:
        tiers: Four-element tuple of tier delays in seconds. Defaults to
            :data:`DEFAULT_BACKOFF_TIERS` (0/1/3/10 s, matches mash-go and
            the spec).
    """

    __slots__ = ("_tiers", "_failed_attempts", "_lock")

    def __init__(
        self,
        tiers: tuple[float, float, float, float] = DEFAULT_BACKOFF_TIERS,
    ) -> None:
        self._tiers = tiers
        self._failed_attempts = 0
        # asyncio.Lock() in 3.10+ binds to the running loop lazily on
        # first acquire, so constructing it eagerly here is safe even
        # outside a running event loop (unit-test friendly).
        self._lock = asyncio.Lock()

    async def get_delay(self) -> float:
        """Return the delay (seconds) to apply before responding to the
        next attempt.

        ``failed_attempts`` is the count of *past* failures, so the
        attempt number being considered is ``failed_attempts + 1``. The
        delay scales by tier: 1-3 → tier 0, 4-6 → tier 1, 7-10 → tier 2,
        11+ → tier 3.
        """
        async with self._lock:
            attempt = self._failed_attempts + 1
            if attempt <= 3:
                return self._tiers[0]
            if attempt <= 6:
                return self._tiers[1]
            if attempt <= 10:
                return self._tiers[2]
            return self._tiers[3]

    async def record_failure(self) -> None:
        """Increment the failed-attempt counter. Call after a PASE
        verification failure (wrong setup code, bad pA, MAC mismatch, or
        a busy-rejection that is not "already in progress").
        """
        async with self._lock:
            self._failed_attempts += 1

    async def reset(self) -> None:
        """Clear the failed-attempt counter. Call when the commissioning
        window closes or a PASE handshake completes successfully.
        """
        async with self._lock:
            self._failed_attempts = 0

    async def attempt_count(self) -> int:
        """Return the current failed-attempt count. For tests + diagnostics."""
        async with self._lock:
            return self._failed_attempts


__all__ = ["DEFAULT_BACKOFF_TIERS", "PASEAttemptTracker"]
