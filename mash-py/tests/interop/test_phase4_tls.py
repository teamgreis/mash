"""Phase 4 interop placeholder: Python client ↔ Go device TLS ping/pong.

The full interop test plan (per ``mash-py-tdd-plan.md`` Phase 4):

1. Spawn ``mash-go/cmd/mash-device`` with a fresh discriminator.
2. Discover the device's commissioning port via mDNS (``_mash-comm._tcp``) —
   needs Phase 6's mDNS browser, not yet implemented.
3. Connect with Python client via ``mash-comm/1`` ALPN.
4. Verify TLS 1.3 handshake completes.
5. Send ``ControlMessage{Type: Ping, Sequence: 42}`` → expect Pong.

This file currently contains only the test scaffold; the body is a stub
that will be filled in once Phase 6 (mDNS) lands. The scaffold lives here
so Phase 4 reviewers can confirm the test surface.
"""

from __future__ import annotations

import shutil
import subprocess

import pytest

pytestmark = pytest.mark.interop


def _go_binary_available() -> bool:
    """The interop tests need a built ``mash-device`` binary on PATH or in
    the sibling ``mash-go`` checkout. We skip rather than fail when it is
    absent — running the unit suite must not require the Go toolchain."""
    return shutil.which("mash-device") is not None or shutil.which("go") is not None


@pytest.mark.skipif(not _go_binary_available(), reason="mash-go binary not available")
def test_python_client_connects_to_go_device():
    """Placeholder — full body lands with Phase 6 (mDNS browser).

    Until then, this asserts only that the Go binary can be executed,
    which is a useful precondition signal for CI (it surfaces broken
    Go toolchain installs without burying the failure in a downstream
    phase).
    """
    # Sanity check the toolchain is callable; the real interop is deferred.
    result = subprocess.run(
        ["go", "version"], capture_output=True, text=True, timeout=10
    )
    assert result.returncode == 0
    assert result.stdout.startswith("go version")


def test_go_controller_connects_to_python_device():
    """Phase 14 has shipped — the real Go-controller-↔-Python-device
    PASE interop lives in
    ``tests/interop/commissioning/test_pase_server_with_go_controller.py``.

    Kept as a no-op marker so the file's two-test layout (Python
    client ↔ Go device, Go controller ↔ Python device) still
    advertises both interop directions to readers browsing the test
    tree.
    """
