"""Async mDNS advertiser for MASH discovery (Phase 18 of mash-py-tdd-plan.md).

Spec contract (canonical):

- ``docs/discovery.md §2.2`` + DEC-078 — commissionable record (``D``,
  ``cat``, ``serial``, ``brand``, ``model``, ``DN``); instance name
  ``MASH-NNNN`` (4-digit zero-padded discriminator, DEC-088 §5).
- ``docs/discovery.md §2.3`` + DEC-088 — operational record (``ZI``, ``DI``
  required; ``VP``, ``FW``, ``EP`` optional; ``FM`` forbidden per DEC-074);
  instance name ``<zone-id>-<device-id>``. One instance per (device, zone)
  pair (DEC-082 multi-zone).
- ``docs/discovery.md §2.1`` + DEC-082 — SRV port is authoritative; advertise
  whatever port the listener bound, never substitute a default.

Implementation notes
--------------------

This wraps ``zeroconf.asyncio.AsyncZeroconf.async_register_service`` /
``async_unregister_service`` with the same lifecycle discipline as
``mash_py.discovery.browser.Browser``: a single ``AsyncZeroconf`` per
``Advertiser`` instance, idempotent ``start`` / ``close``, and an
``AsyncExitStack``-friendly async context manager protocol.

The advertiser tracks at most one commissionable instance and a dict of
operational instances keyed by ``(zone_id, device_id)``. Re-publishing an
existing key transparently unregisters the previous record first — this
matches how mash-go's ``MDNSAdvertiser.AdvertiseOperational`` handles
re-registration (``mash-go/pkg/discovery/mdns.go``).

Hostname / addresses
~~~~~~~~~~~~~~~~~~~~

The advertiser derives the SRV ``server`` and the A/AAAA records as follows:

1. If the caller passes ``host`` and / or ``addresses``, those are used
   verbatim. ``host`` is emitted with a trailing dot (zeroconf requirement);
   addresses are converted from string form to bytes.
2. Otherwise, ``host`` defaults to ``socket.gethostname()`` (sans any
   trailing ``.local`` to avoid double-suffixing) plus ``.local.``, and
   ``addresses`` defaults to every non-loopback IPv4 address bound on the
   local machine (matching how mash-go's ``interfaceIPs`` falls back).

This keeps loopback-only test scenarios working when the caller pins
``host="127.0.0.1.local."`` and ``addresses=("127.0.0.1",)`` explicitly.
"""

from __future__ import annotations

import socket
from typing import TYPE_CHECKING, Any

from mash_py.discovery.types import (
    SERVICE_TYPE_COMMISSIONABLE,
    SERVICE_TYPE_OPERATIONAL,
    AdvertiseError,
    CommissionableAdvertisement,
    OperationalAdvertisement,
    build_commissionable_txt,
    build_operational_txt,
    commissionable_instance_name,
    operational_instance_name,
)

if TYPE_CHECKING:
    from zeroconf import ServiceInfo
    from zeroconf.asyncio import AsyncZeroconf


class Advertiser:
    """Async wrapper around :class:`zeroconf.asyncio.AsyncZeroconf` for publishing.

    Use as an async context manager::

        async with Advertiser() as adv:
            await adv.publish_commissionable(adv_info)
            ...

    or with explicit lifecycle::

        adv = Advertiser()
        await adv.start()
        try:
            await adv.publish_commissionable(adv_info)
        finally:
            await adv.close()

    The class is thread-affine to the asyncio event loop it was created on.
    """

    def __init__(self) -> None:
        self._azc: AsyncZeroconf | None = None
        self._commissionable: ServiceInfo | None = None
        self._operational: dict[tuple[str, str], ServiceInfo] = {}

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------

    async def __aenter__(self) -> Advertiser:
        await self.start()
        return self

    async def __aexit__(self, exc_type: Any, exc: Any, tb: Any) -> None:
        await self.close()

    async def start(self) -> None:
        """Initialise the underlying :class:`AsyncZeroconf`. Idempotent."""
        if self._azc is not None:
            return
        from zeroconf.asyncio import AsyncZeroconf

        self._azc = AsyncZeroconf()

    async def close(self) -> None:
        """Send mDNS goodbye for every published instance and shut down zeroconf.

        Idempotent — safe to call multiple times. The underlying
        ``AsyncZeroconf.async_unregister_all_services`` fires goodbye packets
        (TTL=0) so co-located browsers see the records disappear within
        their announce / cache TTL.
        """
        azc, self._azc = self._azc, None
        self._commissionable = None
        self._operational.clear()
        if azc is None:
            return
        await azc.async_unregister_all_services()
        await azc.async_close()

    # ------------------------------------------------------------------
    # Commissionable
    # ------------------------------------------------------------------

    async def publish_commissionable(
        self, advertisement: CommissionableAdvertisement
    ) -> str:
        """Advertise a single ``_mash-comm._tcp`` instance.

        Returns the published instance name (``MASH-NNNN``). If a
        commissionable instance is already published, it is unregistered
        first to keep "one open commissioning window per device" honest.
        """
        azc = self._ensure_started()
        instance_name = commissionable_instance_name(advertisement.discriminator)
        txt = build_commissionable_txt(advertisement)

        info = self._build_service_info(
            service_type=SERVICE_TYPE_COMMISSIONABLE,
            instance_name=instance_name,
            port=advertisement.port,
            host=advertisement.host,
            addresses=advertisement.addresses,
            txt=txt,
        )

        if self._commissionable is not None:
            await azc.async_unregister_service(self._commissionable)
            self._commissionable = None
        await azc.async_register_service(info)
        self._commissionable = info
        return instance_name

    async def unpublish_commissionable(self) -> None:
        """Send the commissionable record's mDNS goodbye, if any.

        No-op when no commissionable instance is currently published.
        """
        azc = self._azc
        info = self._commissionable
        if azc is None or info is None:
            self._commissionable = None
            return
        await azc.async_unregister_service(info)
        self._commissionable = None

    # ------------------------------------------------------------------
    # Operational (per-zone)
    # ------------------------------------------------------------------

    async def publish_operational(
        self, advertisement: OperationalAdvertisement
    ) -> str:
        """Advertise a single ``_mash._tcp`` instance for one (zone, device).

        Multiple zones live side-by-side; calling this for two different
        ``(zone_id, device_id)`` keys publishes two simultaneous instances
        (DEC-082). Re-publishing an existing key transparently unregisters
        the previous record so callers can use this method to update TXT
        without bouncing the underlying zeroconf instance.

        Returns the published instance name (``<zone-id>-<device-id>``).
        """
        azc = self._ensure_started()
        instance_name = operational_instance_name(
            advertisement.zone_id, advertisement.device_id
        )
        txt = build_operational_txt(advertisement)

        info = self._build_service_info(
            service_type=SERVICE_TYPE_OPERATIONAL,
            instance_name=instance_name,
            port=advertisement.port,
            host=advertisement.host,
            addresses=advertisement.addresses,
            txt=txt,
        )

        key = (advertisement.zone_id, advertisement.device_id)
        prior = self._operational.get(key)
        if prior is not None:
            await azc.async_unregister_service(prior)
        await azc.async_register_service(info)
        self._operational[key] = info
        return instance_name

    async def unpublish_operational(self, zone_id: str, device_id: str) -> None:
        """Remove a previously-published operational instance.

        Raises :class:`AdvertiseError` if no instance is registered for the
        given key — callers that don't care about the absence case can
        check :meth:`is_operational_published` first.
        """
        azc = self._azc
        key = (zone_id, device_id)
        info = self._operational.get(key)
        if info is None:
            raise AdvertiseError(
                f"no operational instance published for "
                f"zone_id={zone_id!r}, device_id={device_id!r}"
            )
        if azc is not None:
            await azc.async_unregister_service(info)
        del self._operational[key]

    def is_operational_published(self, zone_id: str, device_id: str) -> bool:
        return (zone_id, device_id) in self._operational

    def is_commissionable_published(self) -> bool:
        return self._commissionable is not None

    # ------------------------------------------------------------------
    # Convenience: commissioning -> operational transition
    # ------------------------------------------------------------------

    async def transition_to_operational(
        self, advertisement: OperationalAdvertisement
    ) -> str:
        """Atomically unpublish the commissionable record and publish an
        operational one.

        This is the canonical post-PASE flow: commissioning succeeds, the
        device's first zone is provisioned, and the device must drop its
        ``_mash-comm._tcp`` record while announcing ``_mash._tcp``. Calling
        this on a device that wasn't advertising commissionable simply
        publishes the operational record.
        """
        await self.unpublish_commissionable()
        return await self.publish_operational(advertisement)

    # ------------------------------------------------------------------
    # Internals
    # ------------------------------------------------------------------

    def _ensure_started(self) -> AsyncZeroconf:
        if self._azc is None:
            raise RuntimeError(
                "Advertiser is not started — call `await advertiser.start()` "
                "or use `async with Advertiser() as advertiser:`"
            )
        return self._azc

    @staticmethod
    def _build_service_info(
        *,
        service_type: str,
        instance_name: str,
        port: int,
        host: str,
        addresses: tuple[str, ...],
        txt: dict[str, str],
    ) -> ServiceInfo:
        from zeroconf import ServiceInfo

        full_name = f"{instance_name}.{service_type}"
        server = _normalise_server(host)
        addr_bytes = _addresses_to_bytes(addresses)
        return ServiceInfo(
            type_=service_type,
            name=full_name,
            port=port,
            properties=txt,
            server=server,
            addresses=addr_bytes,
        )


# ---------------------------------------------------------------------------
# Hostname / address helpers
# ---------------------------------------------------------------------------


def _normalise_server(host: str) -> str:
    """Return a fully-qualified ``<host>.local.`` form for the SRV ``server`` field.

    ``zeroconf.ServiceInfo.server`` requires the trailing dot; passing a bare
    host name produces a non-FQDN that some resolvers reject. We also strip
    any pre-existing ``.local`` suffix so callers can pass either ``foo`` or
    ``foo.local`` without producing ``foo.local.local.``.
    """
    if not host:
        host = socket.gethostname() or "mash-device"
    host = host.rstrip(".")
    if host.endswith(".local"):
        host = host[: -len(".local")]
    return f"{host}.local."


def _addresses_to_bytes(addresses: tuple[str, ...]) -> list[bytes]:
    """Convert a tuple of IP-string addresses to the byte form zeroconf expects.

    Falls back to the local machine's non-loopback IPv4 addresses when the
    caller supplied none, matching the auto-discovery behaviour mash-go uses
    in ``interfaceIPs``. Loopback (``127.0.0.0/8``) is included in the
    fallback only when no other interface yields an address — this keeps the
    "loopback-only" test scenarios working out of the box.
    """
    if addresses:
        return [_ip_to_bytes(addr) for addr in addresses]

    chosen: list[str] = []
    try:
        infos = socket.getaddrinfo(socket.gethostname(), None)
    except OSError:
        infos = []
    for family, _socktype, _proto, _canon, sockaddr in infos:
        if family == socket.AF_INET:
            # IPv4 sockaddr is (host, port); host is always a str.
            ip = str(sockaddr[0])
            if ip and not ip.startswith("127."):
                chosen.append(ip)
    if not chosen:
        chosen.append("127.0.0.1")
    return [_ip_to_bytes(addr) for addr in chosen]


def _ip_to_bytes(ip: str) -> bytes:
    """Pack an IPv4 or IPv6 dotted/colon string into network bytes."""
    if ":" in ip:
        return socket.inet_pton(socket.AF_INET6, ip)
    return socket.inet_pton(socket.AF_INET, ip)


__all__ = ["Advertiser"]
