"""Unit tests for ``mash_py.discovery.browser``.

The browser is exercised against a *real* loopback Zeroconf advertiser, not
mocks. python-zeroconf's stack runs entirely in-process when both the
advertiser and the browser bind to the same interface set; this gives us
end-to-end confidence in the TXT/SRV resolve path without dragging in mash-go.

The same multicast-on-loopback caveat documented in the Phase 6 plan applies
here: macOS sometimes refuses to deliver multicast on ``lo0``. We use
``InterfaceChoice.All`` for that reason — every interface listens, so packets
sent to any one of them are picked up by the others. CI should run these
tests on Linux and macOS hosts; if the macOS box flakes, mark with
``pytest.mark.flaky`` once we adopt that plugin (deferred — the plan does not
require it).
"""

from __future__ import annotations

import asyncio
import socket

import pytest
from zeroconf import IPVersion, ServiceInfo
from zeroconf.asyncio import AsyncZeroconf

from mash_py.discovery import (
    SERVICE_TYPE_COMMISSIONABLE,
    SERVICE_TYPE_OPERATIONAL,
    BrowseEventType,
    Browser,
    BrowseTimeoutError,
)


def _loopback_ip() -> str:
    """A stable IP we can bind on every CI host."""
    return "127.0.0.1"


async def _register(
    aiozc: AsyncZeroconf,
    service_type: str,
    instance_name: str,
    port: int,
    properties: dict[str, str],
) -> ServiceInfo:
    """Advertise a single service instance on loopback for the duration of a test."""
    full_name = f"{instance_name}.{service_type}"
    server = f"{instance_name}.local."
    info = ServiceInfo(
        type_=service_type,
        name=full_name,
        port=port,
        properties=properties,
        server=server,
        addresses=[socket.inet_aton(_loopback_ip())],
    )
    await aiozc.async_register_service(info)
    return info


@pytest.fixture
async def advertiser():
    """A scoped AsyncZeroconf used only for advertising."""
    aiozc = AsyncZeroconf(ip_version=IPVersion.V4Only)
    try:
        yield aiozc
    finally:
        await aiozc.async_unregister_all_services()
        await aiozc.async_close()


# ---------------------------------------------------------------------------
# Find by discriminator (commissionable)
# ---------------------------------------------------------------------------


async def test_find_commissionable_by_discriminator(advertiser):
    info = await _register(
        advertiser,
        SERVICE_TYPE_COMMISSIONABLE,
        "MASH-1234",
        port=8443,
        properties={
            "D": "1234",
            "cat": "3",
            "serial": "WB-2024-001234",
            "brand": "ChargePoint",
            "model": "Home Flex",
            "DN": "Garage Charger",
        },
    )
    assert info  # silence linter — info is implicitly used by the advertiser

    async with Browser() as browser:
        svc = await browser.find_commissionable_by_discriminator(1234, timeout=5.0)

    assert svc.discriminator == 1234
    assert svc.categories == (3,)
    assert svc.serial == "WB-2024-001234"
    assert svc.brand == "ChargePoint"
    assert svc.model == "Home Flex"
    assert svc.device_name == "Garage Charger"
    # DEC-082: SRV port is authoritative; even on loopback the browser must
    # surface what the advertiser said, not assume 8443.
    assert svc.port == 8443
    assert _loopback_ip() in svc.addresses


async def test_find_commissionable_with_nonstandard_port(advertiser):
    """DEC-082 two-port case: commissioning port differs from operational."""
    await _register(
        advertiser,
        SERVICE_TYPE_COMMISSIONABLE,
        "MASH-2222",
        port=51234,  # non-default commissioning port
        properties={
            "D": "2222",
            "cat": "5",
            "serial": "S",
            "brand": "B",
            "model": "M",
        },
    )

    async with Browser() as browser:
        svc = await browser.find_commissionable_by_discriminator(2222, timeout=5.0)

    assert svc.port == 51234


async def test_find_commissionable_timeout(advertiser):
    """No matching device → BrowseTimeoutError, not a hang."""
    async with Browser() as browser:
        with pytest.raises(BrowseTimeoutError):
            # 0.5s is well below the resolve budget but still gives the loop
            # a chance to enter the select(); we want to assert "no match"
            # not "timer fired before any packet flew".
            await browser.find_commissionable_by_discriminator(9999, timeout=0.5)


# ---------------------------------------------------------------------------
# Operational
# ---------------------------------------------------------------------------


async def test_find_operational_by_device_id(advertiser):
    """DEC-088 operational record: ZI + DI required, instance is <ZI>-<DI>.

    A legacy ``FM`` key is in the TXT record on purpose — DEC-074 forbids
    encoding it but DEC-088 says decoders MUST silently ignore any legacy
    ``FM`` key. We verify the parser doesn't raise on this and doesn't
    surface the value anywhere.
    """
    zi = "A1B2C3D4E5F6A7B8"
    di = "F9E8D7C6B5A49382"
    await _register(
        advertiser,
        SERVICE_TYPE_OPERATIONAL,
        f"{zi}-{di}",
        port=8443,
        properties={
            "ZI": zi,
            "DI": di,
            "VP": "1234:5678",
            "FW": "1.2.3",
            "EP": "2",
            "FM": "0x001B",  # legacy — must be ignored, not an error
        },
    )

    async with Browser() as browser:
        svc = await browser.find_operational_by_device_id(di, timeout=5.0)

    assert svc.zone_id == zi
    assert svc.device_id == di
    assert svc.vendor_product == "1234:5678"
    assert svc.firmware == "1.2.3"
    assert svc.endpoint_count == 2
    assert not hasattr(svc, "feature_map")  # DEC-074 / DEC-088


# ---------------------------------------------------------------------------
# Streaming
# ---------------------------------------------------------------------------


async def test_browse_commissionable_stream_yields_added(advertiser):
    """The stream must surface an ADDED event for a freshly-registered service.

    We filter the consumer by ``instance_name`` so a concurrent MASH device
    on the same multicast network (or a stale ``mash-device`` process from a
    prior interop run) cannot pollute the assertion.
    """
    target_name = "MASH-PY-UNIT-3333"
    async with Browser() as browser:

        async def consumer():
            async for ev in browser.browse_commissionable():
                if (
                    ev.event == BrowseEventType.ADDED
                    and ev.instance_name == target_name
                    and ev.service is not None
                ):
                    return ev

        consume_task = asyncio.create_task(consumer())
        # Yield once so the listener is wired up before we advertise.
        await asyncio.sleep(0.1)
        await _register(
            advertiser,
            SERVICE_TYPE_COMMISSIONABLE,
            target_name,
            port=8443,
            properties={
                "D": "3333",
                "cat": "2",
                "serial": "S",
                "brand": "B",
                "model": "M",
            },
        )
        ev = await asyncio.wait_for(consume_task, timeout=5.0)

    assert ev.event == BrowseEventType.ADDED
    assert ev.instance_name == target_name
    assert ev.service is not None
    assert ev.service.discriminator == 3333


async def test_browse_commissionable_stream_yields_removed(advertiser):
    """Unregistering a service should produce a REMOVED event.

    We do NOT assert ADDED-before-REMOVED ordering. zeroconf can deliver the
    ADDED state-change before the TXT/SRV records reach its cache, in which
    case our resolver returns ``None`` and we silently skip the event (the
    contract — surface nothing when the record set is incomplete and let the
    next state change carry corrected data). REMOVED then arrives without a
    preceding ADDED visible to the consumer. That is correct behaviour, not
    a missed event.
    """
    info = await _register(
        advertiser,
        SERVICE_TYPE_COMMISSIONABLE,
        "MASH-4444",
        port=8443,
        properties={
            "D": "4444",
            "cat": "2",
            "serial": "S",
            "brand": "B",
            "model": "M",
        },
    )

    async with Browser() as browser:

        async def consumer():
            async for ev in browser.browse_commissionable():
                if (
                    ev.event == BrowseEventType.REMOVED
                    and ev.instance_name == "MASH-4444"
                ):
                    return ev

        consume_task = asyncio.create_task(consumer())
        await asyncio.sleep(0.5)
        await advertiser.async_unregister_service(info)
        ev = await asyncio.wait_for(consume_task, timeout=5.0)

    assert ev.event == BrowseEventType.REMOVED
    # zeroconf does not include TXT records on removal; service is None.
    assert ev.service is None


# ---------------------------------------------------------------------------
# Lifecycle
# ---------------------------------------------------------------------------


async def test_browser_requires_start():
    browser = Browser()
    with pytest.raises(RuntimeError, match="not started"):
        await browser.find_commissionable_by_discriminator(1, timeout=0.1)


async def test_browser_close_idempotent():
    browser = Browser()
    await browser.start()
    await browser.close()
    # A second close must not raise.
    await browser.close()
