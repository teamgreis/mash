"""Phase 14 interop: a Python :class:`PASEServer` accepts a live mash-go
PASE client.

This is the reverse of Phase 8. Phase 8 stood up a Go device and drove
:class:`PASEClient` against it; Phase 14 stands up a Python device-side
listener (TLS commissioning context + :class:`PASEServer`) and lets a Go
PASE client (via the ``run_pase_client`` helper) drive the handshake.

Why a focused helper instead of `mash-controller -auto-commission`:
``mash-controller`` couples PASE to mDNS discovery and post-PASE cert
exchange (Phase 9). Phase 14 only owns the PASE state machine; the
helper exercises exactly that, without dragging discovery (Phase 18) or
cert-install (Phase 9 reverse direction) into the test.

Acceptance:

- Server returns a 32-byte ``K_main``.
- Helper exits 0 with the same ``K_main_hex`` in its JSON output —
  cryptographic proof both sides ran the same SPAKE2+ to completion.
"""

from __future__ import annotations

import asyncio
import contextlib
import json
import shutil
import socket
from pathlib import Path

import pytest
from cryptography.hazmat.primitives.asymmetric import ec

from mash_py.commissioning import (
    PASEServer,
    generate_verifier,
)
from mash_py.crypto.certs import generate_commissioning_cert
from mash_py.transport.tls import make_commissioning_context

REPO_ROOT = Path(__file__).resolve().parents[4]
HELPER_DIR = REPO_ROOT / "mash-py" / "tools" / "run_pase_client"


pytestmark = pytest.mark.interop


def _go_available() -> bool:
    return shutil.which("go") is not None


def _free_port() -> int:
    """Bind a kernel-assigned port and immediately release it."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


async def _run_go_helper(
    *,
    target: str,
    setup_code: str,
    discriminator: int,
    timeout_s: float = 60.0,
) -> tuple[int, bytes, bytes]:
    """Spawn ``go run ./run_pase_client`` and return ``(exit, stdout, stderr)``.

    Uses ``create_subprocess_exec`` (no shell) so flag values aren't
    parsed by anything but the helper itself. The helper's working dir
    is the tools/ root so its ``go.mod`` ``replace`` directive picks up
    the local mash-go checkout — same pattern as
    ``test_pase_against_go_device.py`` uses for ``print_verifier``.
    """
    proc = await asyncio.create_subprocess_exec(
        "go",
        "run",
        "./run_pase_client",
        "--target",
        target,
        "--setup-code",
        setup_code,
        "--discriminator",
        str(discriminator),
        "--timeout",
        str(timeout_s),
        cwd=str(HELPER_DIR.parent),
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    try:
        stdout, stderr = await asyncio.wait_for(
            proc.communicate(), timeout=timeout_s + 30.0
        )
    except TimeoutError:
        proc.kill()
        await proc.wait()
        raise
    return proc.returncode or 0, stdout, stderr


@pytest.mark.skipif(not _go_available(), reason="go toolchain not available")
@pytest.mark.slow
@pytest.mark.timeout(120)
async def test_python_pase_server_accepts_go_pase_client() -> None:
    """Acceptance gate for Phase 14.

    Sequence:
      1. Generate a self-signed commissioning cert + matching SSLContext.
      2. Stand up an asyncio TLS listener on a kernel-assigned port; the
         accepted connection is handed to a fresh :class:`PASEServer`.
      3. Spawn ``go run ./run_pase_client`` against ``127.0.0.1:port``
         with the matching setup code + discriminator.
      4. Assert: helper exits 0, server returns 32-byte K_main,
         hex(server.K_main) == helper JSON's ``k_main_hex``.

    The full PBKDF2 100k iteration count is in play here (the helper
    uses mash-go's stock ``GenerateVerifier`` and mash-py's stock
    ``generate_verifier``); on a slow runner a single handshake takes a
    few seconds. The 120 s pytest timeout has wide headroom.
    """
    setup_code = "20202021"
    discriminator = 3840

    # --- Server-side setup ------------------------------------------------
    device_key = ec.generate_private_key(ec.SECP256R1())
    device_cert = generate_commissioning_cert(device_key, str(discriminator))
    ssl_ctx = make_commissioning_context(device_cert, device_key)

    verifier = generate_verifier(setup_code, discriminator)

    server_k_main: bytes | None = None
    handshake_error: BaseException | None = None
    handshake_done = asyncio.Event()

    async def handle_client(
        reader: asyncio.StreamReader, writer: asyncio.StreamWriter
    ) -> None:
        """Per-connection PASE server handler."""
        nonlocal server_k_main, handshake_error
        try:
            pase_server = PASEServer(verifier)
            server_k_main = await pase_server.handshake(reader, writer)
        except BaseException as exc:
            handshake_error = exc
        finally:
            handshake_done.set()
            writer.close()
            with contextlib.suppress(ConnectionError, OSError):
                await writer.wait_closed()

    port = _free_port()
    listener = await asyncio.start_server(
        handle_client, host="127.0.0.1", port=port, ssl=ssl_ctx
    )

    try:
        rc, stdout, stderr = await _run_go_helper(
            target=f"127.0.0.1:{port}",
            setup_code=setup_code,
            discriminator=discriminator,
        )
        if rc != 0:
            pytest.fail(
                f"helper exit {rc}\n"
                f"stdout: {stdout.decode('utf-8', errors='replace')}\n"
                f"stderr: {stderr.decode('utf-8', errors='replace')}",
                pytrace=False,
            )

        # The helper exited 0, so the server-side handshake almost
        # certainly already completed; small wait to keep CI tolerant.
        await asyncio.wait_for(handshake_done.wait(), timeout=5.0)

        if handshake_error is not None:
            raise AssertionError(
                f"server-side PASE failed: {handshake_error!r}"
            )
        assert server_k_main is not None
        assert len(server_k_main) == 32

        helper_out = json.loads(stdout.decode("utf-8"))
        assert helper_out["setup_code"] == setup_code
        assert helper_out["discriminator"] == discriminator
        assert helper_out["k_main_hex"] == server_k_main.hex(), (
            "Python server and Go client derived different K_main values — "
            "SPAKE2+ transcript mismatch or wire-format divergence."
        )
    finally:
        listener.close()
        await listener.wait_closed()


@pytest.mark.skipif(not _go_available(), reason="go toolchain not available")
@pytest.mark.slow
@pytest.mark.timeout(120)
async def test_python_pase_server_rejects_wrong_setup_code() -> None:
    """A Go client using the wrong setup code triggers the §2.5 MAC
    mismatch path on the Python server. Server emits
    ``PASEComplete{ErrCodeAuthFailed}``; helper exits non-zero with the
    auth-failure surface.

    This is the cross-impl mirror of
    ``test_wrong_setup_code_emits_pase_complete_auth_failed`` in the
    unit suite — both sides agree on the failure-envelope contract.
    """
    server_setup_code = "20202021"
    wrong_setup_code = "11111111"
    discriminator = 3840

    device_key = ec.generate_private_key(ec.SECP256R1())
    device_cert = generate_commissioning_cert(device_key, str(discriminator))
    ssl_ctx = make_commissioning_context(device_cert, device_key)
    verifier = generate_verifier(server_setup_code, discriminator)

    handshake_done = asyncio.Event()
    handshake_outcome: tuple[str, str] | None = None

    async def handle_client(
        reader: asyncio.StreamReader, writer: asyncio.StreamWriter
    ) -> None:
        nonlocal handshake_outcome
        try:
            await PASEServer(
                verifier, error_delay_floor_s=0.0
            ).handshake(reader, writer)
            handshake_outcome = ("ok", "unexpected success")
        except BaseException as exc:
            handshake_outcome = (type(exc).__name__, str(exc))
        finally:
            handshake_done.set()
            writer.close()
            with contextlib.suppress(ConnectionError, OSError):
                await writer.wait_closed()

    port = _free_port()
    listener = await asyncio.start_server(
        handle_client, host="127.0.0.1", port=port, ssl=ssl_ctx
    )

    try:
        rc, stdout, stderr = await _run_go_helper(
            target=f"127.0.0.1:{port}",
            setup_code=wrong_setup_code,
            discriminator=discriminator,
        )

        # Helper MUST report a failure — wrong setup code can't succeed.
        assert rc != 0, (
            f"helper unexpectedly succeeded with wrong setup code:\n"
            f"stdout: {stdout.decode('utf-8', errors='replace')}"
        )
        # Confirm we hit a PASE-level failure, not (e.g.) a TLS dial
        # error — the latter would indicate a different bug.
        stderr_text = stderr.decode("utf-8", errors="replace")
        assert "PASE handshake failed" in stderr_text, (
            f"unexpected helper failure shape:\n{stderr_text}"
        )

        await asyncio.wait_for(handshake_done.wait(), timeout=5.0)
        assert handshake_outcome is not None
        kind, msg = handshake_outcome
        assert kind == "PASEAuthFailedError", (
            f"server raised {kind}: {msg} — expected PASEAuthFailedError"
        )
        assert "confirmA mismatch" in msg
    finally:
        listener.close()
        await listener.wait_closed()
