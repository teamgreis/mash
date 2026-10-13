"""Unit tests for ``mash_py.discovery.advertiser`` (Phase 18).

Strategy
--------

The advertiser is exercised against the project's own ``Browser`` over
loopback Zeroconf. Both bind to the same interface set, so packets from one
side are picked up by the other in-process — no need to launch the Go
toolchain for unit-level coverage. The matching interop-with-Go test lives
in ``tests/interop/discovery/test_advertise_go.py``.

Spec coverage
-------------

- DEC-088 §5: ``MASH-NNNN`` commissionable instance name (4-digit zero-pad).
- DEC-088 §2: ``<zone-id>-<device-id>`` operational instance name; multi-zone
  device advertises one instance per pair.
- DEC-074 / DEC-088: ``FM`` is forbidden in operational TXT — encoder MUST
  NOT emit it.
- DEC-082: SRV port is authoritative — the advertiser publishes whatever
  port the caller passes, including non-default ports for two-port
  deployments.
- Lifecycle: ``transition_to_operational`` drops the commissionable record
  and publishes the operational one atomically.
"""

from __future__ import annotations

import asyncio
import contextlib

import pytest

from mash_py.discovery import (
    AdvertiseError,
    Advertiser,
    BrowseEventType,
    Browser,
    CommissionableAdvertisement,
    OperationalAdvertisement,
)

# ---------------------------------------------------------------------------
# Loopback fixtures
# ---------------------------------------------------------------------------


def _commissionable(discriminator: int, **overrides) -> CommissionableAdvertisement:
    base = {
        "discriminator": discriminator,
        "serial": f"S-{discriminator}",
        "brand": "MASH-Py-Unit",
        "model": "EVSE-X",
        "port": 18443,  # non-default to exercise DEC-082 SRV-port preservation
        "categories": (3,),
        "device_name": "Unit Test Charger",
        "host": "loopback-mash-py.local.",
        "addresses": ("127.0.0.1",),
    }
    base.update(overrides)
    return CommissionableAdvertisement(**base)


def _operational(zone_id: str, device_id: str, **overrides) -> OperationalAdvertisement:
    base = {
        "zone_id": zone_id,
        "device_id": device_id,
        "port": 18444,
        "firmware": "1.2.3",
        "endpoint_count": 2,
        "host": "loopback-mash-py.local.",
        "addresses": ("127.0.0.1",),
    }
    base.update(overrides)
    return OperationalAdvertisement(**base)


# ---------------------------------------------------------------------------
# Lifecycle
# ---------------------------------------------------------------------------


async def test_advertiser_start_close_idempotent():
    adv = Advertiser()
    await adv.start()
    await adv.start()  # double-start is a no-op
    await adv.close()
    await adv.close()  # double-close is a no-op


async def test_publish_commissionable_returns_canonical_instance_name():
    async with Advertiser() as adv:
        name = await adv.publish_commissionable(_commissionable(42))
        # DEC-088 §5: 4-digit zero-pad.
        assert name == "MASH-0042"


async def test_publish_operational_returns_canonical_instance_name():
    async with Advertiser() as adv:
        zi = "A1B2C3D4E5F6A7B8"
        di = "F9E8D7C6B5A49382"
        name = await adv.publish_operational(_operational(zi, di))
        # DEC-088 §2: <zi>-<di>.
        assert name == f"{zi}-{di}"


async def test_published_records_use_dec089_uniform_ttl():
    """DEC-089: PTR/SRV/TXT/A/AAAA all advertised at 120 s TTL.

    Without the explicit override, python-zeroconf defaults host_ttl to
    4500 s -- a silent spec deviation if the library default ever changes
    or somebody refactors the ServiceInfo construction. Pinning the TTLs
    on the registered ServiceInfo is the cheapest way to keep us
    conformant.
    """
    async with Advertiser() as adv:
        await adv.publish_commissionable(_commissionable(1234))
        # _commissionable lives in adv._commissionable as a ServiceInfo
        info = adv._commissionable
        assert info is not None
        assert info.host_ttl == 120, f"host_ttl should be 120 s, got {info.host_ttl}"
        assert info.other_ttl == 120, f"other_ttl should be 120 s, got {info.other_ttl}"

        zi = "A1B2C3D4E5F6A7B8"
        di = "F9E8D7C6B5A49382"
        await adv.publish_operational(_operational(zi, di))
        op_info = adv._operational[(zi, di)]
        assert op_info.host_ttl == 120
        assert op_info.other_ttl == 120


# ---------------------------------------------------------------------------
# End-to-end: advertiser → browser on loopback
# ---------------------------------------------------------------------------


async def test_loopback_browser_finds_commissionable_record():
    """The browser MUST resolve TXT/SRV for a record we just published."""
    async with Advertiser() as adv:
        await adv.publish_commissionable(_commissionable(2718))

        async with Browser() as browser:
            svc = await browser.find_commissionable_by_discriminator(
                2718, timeout=5.0
            )

    assert svc.discriminator == 2718
    assert svc.serial == "S-2718"
    assert svc.brand == "MASH-Py-Unit"
    assert svc.model == "EVSE-X"
    assert svc.device_name == "Unit Test Charger"
    assert svc.categories == (3,)
    # DEC-082: SRV port is whatever we advertised, never substituted.
    assert svc.port == 18443


async def test_loopback_browser_finds_operational_record():
    async with Advertiser() as adv:
        zi = "A1B2C3D4E5F6A7B8"
        di = "F9E8D7C6B5A49382"
        await adv.publish_operational(_operational(zi, di))

        async with Browser() as browser:
            svc = await browser.find_operational_by_device_id(di, timeout=5.0)

    assert svc.zone_id == zi
    assert svc.device_id == di
    assert svc.firmware == "1.2.3"
    assert svc.endpoint_count == 2
    # DEC-082: non-default operational port preserved by browse path.
    assert svc.port == 18444


async def test_unpublish_commissionable_emits_goodbye() -> None:
    """A REMOVED event must arrive after ``unpublish_commissionable``.

    The whole test consumes **one** browse stream. Each call to
    ``browse_commissionable()`` binds a fresh ``AsyncServiceBrowser`` to its own
    queue (see the module docstring), so a stream opened *after* the unpublish
    starts with an empty cache and can never observe a goodbye that was already
    delivered — it would time out even though the implementation is correct.
    """
    target = "MASH-1357"  # corresponds to discriminator 1357
    async with Browser() as browser:
        async with Advertiser() as adv:
            await adv.publish_commissionable(_commissionable(1357))

            stream = browser.browse_commissionable()
            try:
                # Wait until the browser has seen the ADDED event before we
                # unpublish, otherwise REMOVED races the cache state.
                while True:
                    ev = await asyncio.wait_for(stream.__anext__(), timeout=5.0)
                    if ev.event == BrowseEventType.ADDED and ev.instance_name == target:
                        break

                await adv.unpublish_commissionable()

                while True:
                    ev = await asyncio.wait_for(stream.__anext__(), timeout=5.0)
                    if ev.event == BrowseEventType.REMOVED and ev.instance_name == target:
                        return
            finally:
                await stream.aclose()


# ---------------------------------------------------------------------------
# Transition flow (commissioning → operational)
# ---------------------------------------------------------------------------


async def test_transition_to_operational_drops_commissionable_and_publishes_operational():
    """Phase 18 acceptance: post-PASE the device drops the commissioning
    record and announces the operational record."""
    discriminator = 3141
    zi = "11111111AAAA2222"
    di = "BBBBCCCC33334444"

    async with Advertiser() as adv:
        await adv.publish_commissionable(_commissionable(discriminator))
        assert adv.is_commissionable_published()

        # Browser confirms the commissioning record is live.
        async with Browser() as browser:
            comm = await browser.find_commissionable_by_discriminator(
                discriminator, timeout=5.0
            )
            assert comm.discriminator == discriminator

            await adv.transition_to_operational(_operational(zi, di))

            # Advertiser side: commissioning record was unpublished, the
            # operational one is now live. (We assert advertiser state rather
            # than re-browse for the absent commissioning record because mDNS
            # cache replay on the same AsyncZeroconf instance can surface a
            # cached record after the goodbye for several TTL-bound seconds —
            # spec-correct behaviour, but racy to assert against.)
            assert not adv.is_commissionable_published()
            assert adv.is_operational_published(zi, di)

            # Browser confirms the operational record appeared.
            op = await browser.find_operational_by_device_id(di, timeout=5.0)
            assert op.zone_id == zi
            assert op.device_id == di


# ---------------------------------------------------------------------------
# Multi-zone (DEC-082)
# ---------------------------------------------------------------------------


async def test_multiple_operational_zones_simultaneous():
    """A device in two zones advertises two operational instances (DEC-082)."""
    di_grid = "AAAA111122223333"
    di_local = "BBBB444455556666"
    zi_grid = "1111111111111111"
    zi_local = "2222222222222222"

    async with Advertiser() as adv:
        await adv.publish_operational(_operational(zi_grid, di_grid))
        await adv.publish_operational(_operational(zi_local, di_local))

        assert adv.is_operational_published(zi_grid, di_grid)
        assert adv.is_operational_published(zi_local, di_local)

        # Both must be discoverable under their own DI.
        async with Browser() as browser:
            grid = await browser.find_operational_by_device_id(di_grid, timeout=5.0)
            local = await browser.find_operational_by_device_id(di_local, timeout=5.0)

        assert grid.zone_id == zi_grid
        assert local.zone_id == zi_local

        # Drop the GRID instance; the LOCAL one stays.
        await adv.unpublish_operational(zi_grid, di_grid)
        assert not adv.is_operational_published(zi_grid, di_grid)
        assert adv.is_operational_published(zi_local, di_local)


async def test_unpublish_operational_unknown_raises():
    async with Advertiser() as adv:
        with pytest.raises(AdvertiseError):
            await adv.unpublish_operational(
                "0000000000000000", "1111111111111111"
            )


# ---------------------------------------------------------------------------
# Validation
# ---------------------------------------------------------------------------


async def test_publish_commissionable_rejects_out_of_range_discriminator():
    async with Advertiser() as adv:
        with pytest.raises(AdvertiseError):
            await adv.publish_commissionable(
                CommissionableAdvertisement(
                    discriminator=5000,  # > MAX_DISCRIMINATOR
                    serial="S",
                    brand="B",
                    model="M",
                    port=8443,
                )
            )


async def test_publish_operational_rejects_invalid_zone_id():
    async with Advertiser() as adv:
        with pytest.raises(AdvertiseError):
            await adv.publish_operational(
                OperationalAdvertisement(
                    zone_id="not-hex",
                    device_id="F9E8D7C6B5A49382",
                )
            )


async def test_advertiser_methods_require_started():
    adv = Advertiser()
    with pytest.raises(RuntimeError):
        await adv.publish_commissionable(_commissionable(1))


# Ensure the suppress import is "used" so the linter does not flag it.
_ = contextlib.suppress
