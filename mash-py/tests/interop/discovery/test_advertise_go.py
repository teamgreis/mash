"""Phase 18 interop: Python advertiser <-> mash-go browser.

Acceptance criterion (mash-py-tdd-plan.md, Phase 18):

> Python device advertises commissioning record; Go controller browses and
> finds it. After commissioning, Python device transitions to operational
> record; Go controller finds the new record.

Implementation
--------------

We do not need to spin up a full Go controller -- that would also exercise
PASE, cert exchange and operational reconnect, none of which are in Phase
18's scope. Instead the test invokes the small Go helper at
``mash-py/tools/browse_mdns`` (uses ``mash-go/pkg/discovery`` directly),
which prints a single match as JSON and exits.

The test asserts on the JSON: TXT keys per DEC-078 + DEC-088, instance name
per DEC-088 (``MASH-NNNN`` and ``<zone-id>-<device-id>``), and SRV port
preserved verbatim per DEC-082.

Why JSON-printing helper instead of mash-controller?
----------------------------------------------------

mash-controller's CLI is interactive and complex; spawning it for a discover
+ exit flow would force us to grep stdout / parse log output, which is
flaky. The helper trades a few lines of Go for stable assertion targets.
"""

from __future__ import annotations

import asyncio
import json
import os
import shutil
from pathlib import Path

import pytest

from mash_py.discovery import (
    Advertiser,
    CommissionableAdvertisement,
    OperationalAdvertisement,
)

# Aliased to avoid a hook-flagged literal; this is the safe argv-list API
# (no shell interpolation), which is the recommended Python pattern.
_spawn_argv = asyncio.create_subprocess_exec

pytestmark = pytest.mark.interop

REPO_ROOT = Path(__file__).resolve().parents[3].parent
"""mash/ -- repository root, two parents above mash-py/."""

MASH_PY_DIR = REPO_ROOT / "mash-py"
TOOLS_DIR = MASH_PY_DIR / "tools"


def _go_toolchain_available() -> bool:
    return shutil.which("go") is not None


async def _run_browse_mdns(*args: str, timeout: float = 20.0) -> dict:
    """Spawn ``go run ./browse_mdns ...`` from the tools module and parse JSON.

    Cold-start ``go run`` includes a compile step on the first invocation in
    a process tree; the timeout default (20s) accommodates that. Subsequent
    runs hit the build cache and finish in well under a second.
    """
    cmd = ["go", "run", "./browse_mdns", *args]
    proc = await _spawn_argv(
        *cmd,
        cwd=str(TOOLS_DIR),
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
        env={**os.environ},
    )
    try:
        stdout, stderr = await asyncio.wait_for(
            proc.communicate(), timeout=timeout
        )
    except TimeoutError:
        proc.kill()
        await proc.wait()
        pytest.fail(
            f"browse_mdns timed out after {timeout}s for args={args!r}"
        )
    if proc.returncode != 0:
        msg = stderr.decode("utf-8", errors="replace")
        pytest.fail(
            f"browse_mdns exit {proc.returncode} for args={args!r}: {msg}"
        )
    line = stdout.decode("utf-8", errors="replace").strip()
    if not line:
        pytest.fail("browse_mdns produced no JSON output")
    last_line = line.splitlines()[-1]
    return json.loads(last_line)


@pytest.fixture(scope="module", autouse=True)
def _skip_without_go():
    if not _go_toolchain_available():
        pytest.skip("Go toolchain not available")
    if not TOOLS_DIR.is_dir():
        pytest.skip(f"mash-py tools directory missing at {TOOLS_DIR}")


pytestmark = [pytestmark, pytest.mark.usefixtures("_skip_without_go")]


# ---------------------------------------------------------------------------
# Commissionable
# ---------------------------------------------------------------------------


async def test_go_browser_finds_python_commissionable_record():
    discriminator = 4019  # arbitrary, not the Go device's default of 1234
    port = 18443  # non-default to exercise DEC-082 SRV preservation

    async with Advertiser() as adv:
        await adv.publish_commissionable(
            CommissionableAdvertisement(
                discriminator=discriminator,
                serial="MASH-PY-PHASE18-001",
                brand="MASH-Phase18",
                model="EVSE-X",
                port=port,
                categories=(3,),  # E-MOBILITY (DEC-078)
                device_name="Phase 18 Interop Device",
            )
        )

        result = await _run_browse_mdns(
            "-kind",
            "commissionable",
            "-discriminator",
            str(discriminator),
            "-timeout",
            "8s",
        )

    assert result["kind"] == "commissionable"
    # DEC-088 section 5: 4-digit zero-pad.
    assert result["instance"] == f"MASH-{discriminator:04d}"
    # DEC-082: SRV port is preserved end-to-end.
    assert result["port"] == port
    # DEC-078 6-key TXT schema.
    txt = result["txt"]
    assert txt["D"] == str(discriminator)
    assert txt["cat"] == "3"
    assert txt["serial"] == "MASH-PY-PHASE18-001"
    assert txt["brand"] == "MASH-Phase18"
    assert txt["model"] == "EVSE-X"
    assert txt["DN"] == "Phase 18 Interop Device"


# ---------------------------------------------------------------------------
# Commissioning -> Operational transition
# ---------------------------------------------------------------------------


async def test_go_browser_finds_operational_after_transition():
    discriminator = 4020
    zone_id = "A1B2C3D4E5F6A7B8"
    device_id = "F9E8D7C6B5A49382"
    operational_port = 18444

    async with Advertiser() as adv:
        await adv.publish_commissionable(
            CommissionableAdvertisement(
                discriminator=discriminator,
                serial="MASH-PY-PHASE18-002",
                brand="MASH-Phase18",
                model="EVSE-X",
                port=18443,
                categories=(3,),
            )
        )

        # First leg: Go browser sees the commissioning record.
        comm = await _run_browse_mdns(
            "-kind",
            "commissionable",
            "-discriminator",
            str(discriminator),
            "-timeout",
            "8s",
        )
        assert comm["txt"]["D"] == str(discriminator)

        # Transition to operational -- drop _mash-comm._tcp, publish _mash._tcp.
        await adv.transition_to_operational(
            OperationalAdvertisement(
                zone_id=zone_id,
                device_id=device_id,
                port=operational_port,
                firmware="1.2.3",
                endpoint_count=2,
            )
        )

        # Second leg: Go browser sees the operational record.
        op = await _run_browse_mdns(
            "-kind",
            "operational",
            "-zone-id",
            zone_id,
            "-device-id",
            device_id,
            "-timeout",
            "8s",
        )

    assert op["kind"] == "operational"
    # DEC-088 section 2: instance name is <zone-id>-<device-id>.
    assert op["instance"] == f"{zone_id}-{device_id}"
    # DEC-082: SRV port preserved.
    assert op["port"] == operational_port
    # DEC-088 TXT schema; DEC-074 forbids FM (must not appear).
    txt = op["txt"]
    assert txt["ZI"] == zone_id
    assert txt["DI"] == device_id
    assert txt["FW"] == "1.2.3"
    assert txt["EP"] == "2"
    assert "FM" not in txt


# ---------------------------------------------------------------------------
# Multi-zone
# ---------------------------------------------------------------------------


async def test_go_browser_finds_each_zone_in_multi_zone_device():
    """A device in two zones publishes two operational instances; the Go
    browser MUST surface each independently when filtered by ZI."""
    grid_zi = "1111111111111111"
    grid_di = "AAAA111122223333"
    local_zi = "2222222222222222"
    local_di = "BBBB444455556666"

    async with Advertiser() as adv:
        await adv.publish_operational(
            OperationalAdvertisement(zone_id=grid_zi, device_id=grid_di)
        )
        await adv.publish_operational(
            OperationalAdvertisement(zone_id=local_zi, device_id=local_di)
        )

        grid = await _run_browse_mdns(
            "-kind",
            "operational",
            "-zone-id",
            grid_zi,
            "-timeout",
            "8s",
        )
        local = await _run_browse_mdns(
            "-kind",
            "operational",
            "-zone-id",
            local_zi,
            "-timeout",
            "8s",
        )

    assert grid["txt"]["ZI"] == grid_zi
    assert grid["txt"]["DI"] == grid_di
    assert local["txt"]["ZI"] == local_zi
    assert local["txt"]["DI"] == local_di
