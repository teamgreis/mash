"""Discovery-handler unit tests (no live mDNS).

These tests exercise the lifecycle / book-keeping of the discovery
handlers without spinning up a real zeroconf instance — the
``handle_browse_mdns`` / ``handle_wait_for_device`` paths that talk to
the network are covered by the interop suite.
"""

from __future__ import annotations

import pytest

from mash_py.testharness.handlers._common import (
    KEY_ACTIVE_DISCOVERY,
    KEY_DISCOVERED_SERVICES,
)
from mash_py.testharness.handlers.discovery import (
    handle_start_discovery,
    handle_stop_discovery,
    handle_verify_txt_records,
)
from mash_py.testharness.loader import Step
from mash_py.testharness.runner import ExecutionState

pytestmark = pytest.mark.asyncio


async def test_start_then_stop_clears_state() -> None:
    state = ExecutionState()
    out = await handle_start_discovery(
        Step(action="start_discovery", params={"timeout_ms": 3000}),
        state,
    )
    assert out["discovery_started"] is True
    assert state.custom[KEY_ACTIVE_DISCOVERY] == {"timeout_ms": 3000}

    state.custom[KEY_DISCOVERED_SERVICES] = [{"x": 1}]
    out = await handle_stop_discovery(Step(action="stop_discovery"), state)
    assert out["discovery_stopped"] is True
    assert KEY_ACTIVE_DISCOVERY not in state.custom
    assert KEY_DISCOVERED_SERVICES not in state.custom


async def test_verify_txt_records_no_services() -> None:
    state = ExecutionState()
    out = await handle_verify_txt_records(
        Step(action="verify_txt_records", params={"required_keys": ["D"]}),
        state,
    )
    assert out["txt_valid"] is False
    assert "no services" in out["error"]


async def test_verify_txt_records_required_keys_present() -> None:
    state = ExecutionState()
    state.custom[KEY_DISCOVERED_SERVICES] = [
        {
            "instance_name": "MASH-3072",
            "host": "host.local.",
            "port": 12345,
            "addresses": ["fe80::1"],
            "txt": {
                "D": "3072",
                "cat": "3",
                "serial": "X",
                "brand": "B",
                "model": "M",
            },
        }
    ]
    out = await handle_verify_txt_records(
        Step(
            action="verify_txt_records",
            params={
                "required_keys": ["D", "cat", "serial"],
                "optional_keys": ["DN"],
            },
        ),
        state,
    )
    assert out["txt_valid"] is True


async def test_verify_txt_records_required_keys_missing() -> None:
    state = ExecutionState()
    state.custom[KEY_DISCOVERED_SERVICES] = [
        {
            "instance_name": "MASH-3072",
            "host": "host.local.",
            "port": 12345,
            "addresses": [],
            "txt": {"D": "3072"},
        }
    ]
    out = await handle_verify_txt_records(
        Step(
            action="verify_txt_records",
            params={"required_keys": ["D", "cat"]},
        ),
        state,
    )
    assert out["txt_valid"] is False
    assert out["missing_keys"] == ["cat"]


async def test_verify_txt_records_operational_zi_di_decoded() -> None:
    """Spec-mandated ZI/DI checks (DEC-088) come back as observable flags."""
    state = ExecutionState()
    state.custom[KEY_DISCOVERED_SERVICES] = [
        {
            "instance_name": "ABCD-1234-5678-9ABC",
            "host": "host.local.",
            "port": 8443,
            "addresses": ["fe80::1"],
            "txt": {
                "ZI": "0123456789ABCDEF",
                "DI": "FEDCBA9876543210",
            },
        }
    ]
    out = await handle_verify_txt_records(
        Step(
            action="verify_txt_records",
            params={"required_keys": ["ZI", "DI"]},
        ),
        state,
    )
    assert out["txt_valid"] is True
    assert out["zone_id_length"] == 16
    assert out["device_id_length"] == 16
    assert out["zone_id_hex_valid"] is True
    assert out["device_id_hex_valid"] is True
