"""Async mDNS browser for MASH discovery (``docs/discovery.md §2``).

This is the **browser-only** half of Phase 6. The matching advertiser ships in
Phase 18 (``mash_py.discovery.advertiser``) once the device service can answer
the connections that follow a successful discovery.

Spec contract (canonical, not derived from mash-go):

- Browse ``_mash-comm._tcp.local.`` to find devices in commissioning mode
  (``§2.1``, ``§2.2``).
- Browse ``_mash._tcp.local.`` to find commissioned operational devices
  (``§2.1``, ``§2.3``).
- The SRV port is **authoritative** per service (DEC-082): two-port deployments
  advertise different ports for commissioning vs. operational. The browser
  surfaces the SRV port verbatim and never substitutes a default.

zeroconf integration notes
--------------------------

The python-zeroconf package's :class:`AsyncServiceBrowser` is push-based: it
delivers ``(zeroconf, service_type, name, state_change)`` tuples to a handler.
We translate that into a pull-based async iterator and a one-shot
``find_*`` convenience by feeding events through an :class:`asyncio.Queue`.

Each browse call binds its own ``AsyncServiceBrowser`` to a fresh queue. This
keeps streams independent — a long-running ``browse_commissionable`` consumer
does not see updates intended for a transient ``find_*`` call, and vice versa.

The browser is a ``contextlib.AsyncExitStack``-friendly async context manager;
``Browser.close()`` cancels every in-flight stream and shuts the underlying
``AsyncZeroconf`` instance down cleanly. This matches the rest of mash-py's
asyncio resource discipline (see ``Connection`` in ``mash_py.transport``).
"""

from __future__ import annotations

import asyncio
import contextlib
from collections.abc import AsyncIterator
from dataclasses import dataclass
from enum import Enum
from typing import TYPE_CHECKING, Any

from mash_py.discovery.types import (
    SERVICE_TYPE_COMMISSIONABLE,
    SERVICE_TYPE_OPERATIONAL,
    BrowseTimeoutError,
    CommissionableService,
    OperationalService,
    TXTRecordError,
    parse_commissionable_txt,
    parse_operational_txt,
)

if TYPE_CHECKING:
    from zeroconf import ServiceInfo
    from zeroconf.asyncio import AsyncZeroconf

# zeroconf imports are deferred to ``Browser.start`` so importing this module
# is cheap (e.g. for type hints) and so test files that do not exercise the
# browser do not pay the multicast-socket cost.
_DEFAULT_RESOLVE_TIMEOUT_S: float = 3.0
"""How long to wait for a TXT/SRV/A record set after the browser sees a name.

Three seconds is the same value the python-zeroconf docs recommend for
``ServiceInfo.async_request``. Below ~1s, transient packet loss starts to
swallow legitimate matches; above ~5s the wait dominates the latency of a
``find_*`` call. The interop test budget (~2s per the plan) accommodates this.
"""


class BrowseEventType(Enum):
    """Lifecycle event emitted on the browse stream.

    These mirror python-zeroconf's :class:`ServiceStateChange` enum but stay
    decoupled from it so callers do not need to import zeroconf themselves.
    """

    ADDED = "added"
    UPDATED = "updated"
    REMOVED = "removed"


@dataclass(frozen=True, slots=True)
class CommissionableEvent:
    """An event from the commissionable browse stream.

    On ``REMOVED`` the ``service`` field is ``None`` — by definition the device
    is no longer answering, so we cannot resolve its TXT/SRV records. Callers
    keying off ``instance_name`` can still drop the entry from a cache.
    """

    event: BrowseEventType
    instance_name: str
    service: CommissionableService | None


@dataclass(frozen=True, slots=True)
class OperationalEvent:
    event: BrowseEventType
    instance_name: str
    service: OperationalService | None


class Browser:
    """Async wrapper around :class:`zeroconf.asyncio.AsyncZeroconf`.

    Use as an async context manager::

        async with Browser() as browser:
            service = await browser.find_commissionable_by_discriminator(1234)

    or as a long-lived stream::

        async with Browser() as browser:
            async for ev in browser.browse_commissionable():
                handle(ev)

    The class is thread-affine to the asyncio event loop it was created on.
    """

    def __init__(self) -> None:
        self._azc: AsyncZeroconf | None = None
        self._resolve_timeout_s: float = _DEFAULT_RESOLVE_TIMEOUT_S

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------

    async def __aenter__(self) -> Browser:
        await self.start()
        return self

    async def __aexit__(self, exc_type: Any, exc: Any, tb: Any) -> None:
        await self.close()

    async def start(self) -> None:
        """Initialise the underlying :class:`AsyncZeroconf`.

        Idempotent — calling twice is a no-op so the class is safe to use both
        as a context manager and via explicit ``start``/``close``.
        """
        if self._azc is not None:
            return
        # Imported lazily so users can ``import mash_py.discovery.browser``
        # without paying the zeroconf socket-bind cost up front.
        from zeroconf.asyncio import AsyncZeroconf

        self._azc = AsyncZeroconf()

    async def close(self) -> None:
        """Shut down the AsyncZeroconf instance.

        Per-call :class:`AsyncServiceBrowser` instances are torn down by their
        owning generators / coroutines via ``finally`` blocks; closing the
        underlying zeroconf is idempotent and also cancels any stragglers.
        """
        azc, self._azc = self._azc, None
        if azc is None:
            return
        await azc.async_close()

    # ------------------------------------------------------------------
    # Streaming browse APIs
    # ------------------------------------------------------------------

    def browse_commissionable(self) -> AsyncIterator[CommissionableEvent]:
        """Yield commissionable-device events as they arrive.

        The iterator stops when the consumer breaks out, when the underlying
        :class:`Browser` is closed, or when the surrounding task is cancelled.
        """
        return self._browse_stream(
            SERVICE_TYPE_COMMISSIONABLE,
            self._resolve_commissionable,
            CommissionableEvent,
        )

    def browse_operational(self) -> AsyncIterator[OperationalEvent]:
        """Yield operational-device events as they arrive."""
        return self._browse_stream(
            SERVICE_TYPE_OPERATIONAL,
            self._resolve_operational,
            OperationalEvent,
        )

    # ------------------------------------------------------------------
    # One-shot find APIs
    # ------------------------------------------------------------------

    async def find_commissionable_by_discriminator(
        self, discriminator: int, timeout: float = 2.0
    ) -> CommissionableService:
        """Block until a commissionable device with the given ``D`` appears.

        Raises :class:`BrowseTimeoutError` if no match arrives within
        ``timeout`` seconds. The 2 second default matches the Phase 6 plan
        acceptance criterion ("interop browse succeeds within 2 seconds").
        """

        async def _matches(svc: CommissionableService) -> bool:
            return svc.discriminator == discriminator

        result = await self._find_first(
            SERVICE_TYPE_COMMISSIONABLE,
            self._resolve_commissionable,
            _matches,
            timeout,
            f"discriminator={discriminator}",
        )
        assert isinstance(result, CommissionableService)
        return result

    async def find_operational_by_device_id(
        self, device_id: str, timeout: float = 2.0
    ) -> OperationalService:
        """Block until an operational device with the given ``DI`` appears."""

        async def _matches(svc: OperationalService) -> bool:
            return svc.device_id == device_id

        result = await self._find_first(
            SERVICE_TYPE_OPERATIONAL,
            self._resolve_operational,
            _matches,
            timeout,
            f"device_id={device_id!r}",
        )
        assert isinstance(result, OperationalService)
        return result

    # ------------------------------------------------------------------
    # Internals
    # ------------------------------------------------------------------

    def _ensure_started(self) -> AsyncZeroconf:
        if self._azc is None:
            raise RuntimeError(
                "Browser is not started — call `await browser.start()` "
                "or use `async with Browser() as browser:`"
            )
        return self._azc

    async def _browse_stream(
        self,
        service_type: str,
        resolver: Any,
        event_cls: Any,
    ) -> AsyncIterator[Any]:
        """Adapt the push-based zeroconf browser to a pull-based async iterator.

        The handler closure pushes raw ``(state_change, name)`` tuples onto a
        queue; the iterator body resolves names to service objects and emits
        typed events. The handler is invoked from a zeroconf worker thread, so
        we schedule the queue insert via :func:`asyncio.AbstractEventLoop.call_soon_threadsafe`.
        """
        from zeroconf import ServiceStateChange
        from zeroconf.asyncio import AsyncServiceBrowser

        azc = self._ensure_started()
        queue: asyncio.Queue[tuple[ServiceStateChange, str]] = asyncio.Queue()
        loop = asyncio.get_running_loop()

        def _handler(
            zeroconf: Any,
            service_type: str,
            name: str,
            state_change: ServiceStateChange,
        ) -> None:
            loop.call_soon_threadsafe(queue.put_nowait, (state_change, name))

        sb = AsyncServiceBrowser(azc.zeroconf, service_type, handlers=[_handler])

        try:
            while True:
                state_change, name = await queue.get()
                instance_name = _strip_service_suffix(name, service_type)
                if state_change == ServiceStateChange.Removed:
                    yield event_cls(
                        event=BrowseEventType.REMOVED,
                        instance_name=instance_name,
                        service=None,
                    )
                    continue

                event_type = (
                    BrowseEventType.ADDED
                    if state_change == ServiceStateChange.Added
                    else BrowseEventType.UPDATED
                )
                try:
                    service = await resolver(name, service_type)
                except TXTRecordError:
                    # Malformed record — surface nothing; the next state
                    # change (e.g. a corrected re-announce) will retry.
                    continue
                if service is None:
                    # Record set could not be resolved within the timeout —
                    # also surface nothing; the next state change will retry.
                    continue
                yield event_cls(
                    event=event_type, instance_name=instance_name, service=service
                )
        finally:
            with contextlib.suppress(Exception):
                await sb.async_cancel()

    async def _find_first(
        self,
        service_type: str,
        resolver: Any,
        match_predicate: Any,
        timeout: float,
        criterion_repr: str,
    ) -> Any:
        """Run a temporary browser until a matching service is resolved."""
        from zeroconf import ServiceStateChange
        from zeroconf.asyncio import AsyncServiceBrowser

        azc = self._ensure_started()
        loop = asyncio.get_running_loop()
        queue: asyncio.Queue[str] = asyncio.Queue()

        def _handler(
            zeroconf: Any,
            service_type: str,
            name: str,
            state_change: ServiceStateChange,
        ) -> None:
            if state_change == ServiceStateChange.Removed:
                return
            loop.call_soon_threadsafe(queue.put_nowait, name)

        sb = AsyncServiceBrowser(azc.zeroconf, service_type, handlers=[_handler])
        try:
            async with asyncio.timeout(timeout):
                while True:
                    name = await queue.get()
                    try:
                        service = await resolver(name, service_type)
                    except TXTRecordError:
                        continue
                    if service is None:
                        continue
                    if await match_predicate(service):
                        return service
        except TimeoutError as exc:
            raise BrowseTimeoutError(
                f"no {service_type} match for {criterion_repr} within {timeout}s"
            ) from exc
        finally:
            with contextlib.suppress(Exception):
                await sb.async_cancel()

    async def _resolve_commissionable(
        self, name: str, service_type: str
    ) -> CommissionableService | None:
        info = await self._resolve_service_info(name, service_type)
        if info is None:
            return None
        instance_name = _strip_service_suffix(name, service_type)
        return parse_commissionable_txt(
            instance_name=instance_name,
            host=info.server or "",
            port=info.port or 0,
            addresses=tuple(info.parsed_addresses()),
            txt=info.decoded_properties,
        )

    async def _resolve_operational(
        self, name: str, service_type: str
    ) -> OperationalService | None:
        info = await self._resolve_service_info(name, service_type)
        if info is None:
            return None
        instance_name = _strip_service_suffix(name, service_type)
        return parse_operational_txt(
            instance_name=instance_name,
            host=info.server or "",
            port=info.port or 0,
            addresses=tuple(info.parsed_addresses()),
            txt=info.decoded_properties,
        )

    async def _resolve_service_info(
        self, name: str, service_type: str
    ) -> ServiceInfo | None:
        """Issue a TXT/SRV/A query for the named instance.

        Returns ``None`` if the answer does not arrive within
        ``self._resolve_timeout_s``. zeroconf's ``async_request`` handles
        retransmission internally.
        """
        from zeroconf.asyncio import AsyncServiceInfo

        azc = self._ensure_started()
        info = AsyncServiceInfo(service_type, name)
        ok = await info.async_request(azc.zeroconf, self._resolve_timeout_s * 1000)
        if not ok:
            return None
        # async_request returns True even when only A records arrived; if
        # there is no SRV port the record is unusable for our purposes.
        if info.port is None:
            return None
        return info


def _strip_service_suffix(name: str, service_type: str) -> str:
    """Return the bare instance name without the ``._mash-comm._tcp.local.`` tail.

    zeroconf gives names in fully-qualified form ("MASH-1234._mash-comm._tcp.local.").
    Callers consistently want the leading label only.
    """
    suffix = "." + service_type
    if name.endswith(suffix):
        return name[: -len(suffix)]
    # Defensive — zeroconf has historically been consistent here, but if a
    # future version returns the bare label we still produce a clean result.
    return name.split(".")[0]


__all__ = [
    "BrowseEventType",
    "Browser",
    "CommissionableEvent",
    "OperationalEvent",
]
