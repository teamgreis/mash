"""Phase 11 interop: Read / Write / Subscribe / Invoke against ``mash-go``.

The Phase-11 acceptance gate per ``mash-py-tdd-plan.md``:

    All four operations work against a Go device with various
    endpoint/feature combinations.

This test reuses the Phase 10 commissioning + operational reconnect
flow, then drives the four operations through :class:`InteractionClient`:

  1. **Read** ``DeviceInfo`` (endpoint 0, feature 0x01) — verifies
     correlation works for the simplest case.
  2. **Write** ``DeviceInfo.label`` (attribute 31) — exercises the
     write path; ``location`` (30) and ``label`` (31) are the only
     readWrite attributes on DeviceInfo. The plan originally named
     ``DiscriminatorRevocation``, which doesn't exist in any feature
     YAML — see Phase 11 spec audit.
  3. **Subscribe** to Status (endpoint 1, feature 0x02) — verifies the
     priming-report path (``§6.2``) and that the local handler is
     wired up. We don't attempt to trigger a state change because
     that requires either a TestControl invocation or another
     controller writing limits; the Phase-11 acceptance gate is
     "Subscribe works", not "notifications stream end-to-end" (that
     is Phase 13's job).
  4. **Invoke** ``DeviceInfo.removeZone`` (command 0x10) — verifies
     parameterised commands. RemoveZone is a destructive operation
     that tears down the connection after responding; we run it
     last and accept either a clean response or response-then-close.

The test passes only against a mash-go that has the DEC-085
commissioning envelope (Phase 9) and exposes ``Status.OperatingState``
on EVSE endpoint 1 (the default for ``mash-device -type evse``).
"""

from __future__ import annotations

import asyncio
import contextlib
import os
import shutil
import signal
import socket
import subprocess
import tempfile
from pathlib import Path

import pytest

from mash_py.commissioning import PASEClient
from mash_py.commissioning.cert_exchange import CertExchangeClient
from mash_py.commissioning.cert_store import (
    ZoneType,
    generate_controller_cert,
    generate_zone_ca,
)
from mash_py.discovery import Browser, BrowseTimeoutError
from mash_py.interaction import (
    InteractionClient,
    StatusError,
    Subscription,
)
from mash_py.service.controller import open_operational_connection
from mash_py.transport.connection import (
    Connection,
    ConnectionState,
)
from mash_py.transport.tls import (
    make_commissioning_client_context,
    verify_alpn,
    verify_tls13,
)
from mash_py.transport.connection import _ssl_object
from mash_py.wire import Notification, Status

REPO_ROOT = Path(__file__).resolve().parents[4]
MASH_GO_DIR = REPO_ROOT / "mash-go"

pytestmark = pytest.mark.interop

# Feature / attribute / command IDs from docs/features/.
FEATURE_DEVICE_INFO = 0x01
FEATURE_STATUS = 0x02

ATTR_DEVICE_ID = 1
ATTR_VENDOR_NAME = 2
ATTR_PRODUCT_NAME = 3
ATTR_SERIAL_NUMBER = 4
ATTR_LABEL = 31  # DeviceInfo.label, readWrite/nullable string

ATTR_OPERATING_STATE = 1  # Status.operatingState (status/1.0.yaml)

CMD_REMOVE_ZONE = 0x10  # DeviceInfo.removeZone
CMD_REMOVE_ZONE_PARAM_ZONE_ID = 1  # zoneId param key per device-info/1.0.yaml
CMD_REMOVE_ZONE_RESP_REMOVED = 1  # removed bool response key

EVSE_ENDPOINT_ID = 1  # mash-device -type evse exposes EVSE on endpoint 1


# ---------------------------------------------------------------------------
# Process helpers (shared shape with Phase 10)
# ---------------------------------------------------------------------------


def _go_available() -> bool:
    return shutil.which("go") is not None


def _free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _pick_target_host(svc) -> str:
    if "127.0.0.1" in svc.addresses:
        return "127.0.0.1"
    if svc.addresses:
        ipv4 = next((a for a in svc.addresses if ":" not in a), None)
        return ipv4 or svc.addresses[0]
    return svc.host.rstrip(".")


@pytest.fixture
def go_device_state_dir():
    d = tempfile.mkdtemp(prefix="mash-py-phase11-")
    try:
        yield d
    finally:
        shutil.rmtree(d, ignore_errors=True)


@pytest.fixture
def live_go_device(go_device_state_dir):
    """Spawn a fresh mash-go device for one Phase-11 test.

    Discriminator distinct from Phases 8/9/10 (4032/3072/2048) to avoid
    accidental cross-test mDNS overlap when running the full suite.
    """
    if not _go_available():
        pytest.skip("Go toolchain not available")
    if not MASH_GO_DIR.is_dir():
        pytest.skip(f"mash-go checkout not found at {MASH_GO_DIR}")

    discriminator = 1024
    setup_code = "20202021"
    port = _free_port()
    cmd = [
        "go",
        "run",
        "./cmd/mash-device",
        "-type",
        "evse",
        "-discriminator",
        str(discriminator),
        "-setup-code",
        setup_code,
        "-port",
        str(port),
        "-state-dir",
        go_device_state_dir,
        "-log-level",
        "warn",
        "-serial",
        "PHASE11-INTEROP-001",
        "-brand",
        "MASH-Phase11",
        "-model",
        "EVSE-X",
        "-name",
        "Phase 11 Interop Device",
    ]
    env = os.environ.copy()
    env["MASH_NON_INTERACTIVE"] = "1"

    proc = subprocess.Popen(
        cmd,
        cwd=MASH_GO_DIR,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env=env,
        start_new_session=True,
    )

    try:
        yield proc, discriminator, setup_code, port
    finally:
        pgid = None
        with contextlib.suppress(ProcessLookupError, OSError):
            pgid = os.getpgid(proc.pid)
        if pgid is not None:
            with contextlib.suppress(ProcessLookupError, PermissionError):
                os.killpg(pgid, signal.SIGTERM)
        try:
            proc.wait(timeout=10.0)
        except subprocess.TimeoutExpired:
            if pgid is not None:
                with contextlib.suppress(ProcessLookupError, PermissionError):
                    os.killpg(pgid, signal.SIGKILL)
            proc.wait(timeout=5.0)
        if proc.returncode and proc.returncode not in (0, -signal.SIGTERM):
            err = (
                proc.stderr.read().decode("utf-8", errors="replace")
                if proc.stderr
                else ""
            )
            print(
                f"\n--- mash-device stderr (exit {proc.returncode}) ---\n{err}"
            )


# ---------------------------------------------------------------------------
# Commissioning helper — same shape as Phase 10, returns the pieces Phase 11
# needs to open an operational Connection wrapped by InteractionClient.
# ---------------------------------------------------------------------------


async def _commission_and_get_op_target(
    proc, discriminator: int, setup_code: str
) -> tuple[str, int, str, "ControllerOperationalCert"]:
    """Drive commissioning end-to-end, return ``(host, port, device_id, cert)``."""
    from mash_py.commissioning.cert_store import ControllerOperationalCert  # noqa: F401

    async with Browser() as browser:
        try:
            comm_svc = await browser.find_commissionable_by_discriminator(
                discriminator, timeout=30.0
            )
        except BrowseTimeoutError:
            stderr = (
                proc.stderr.read(8192).decode("utf-8", errors="replace")
                if proc.stderr
                else "<no stderr>"
            )
            pytest.fail(
                f"Go device did not surface on _mash-comm._tcp within 30s. "
                f"stderr tail:\n{stderr}",
                pytrace=False,
            )

    target_host = _pick_target_host(comm_svc)
    comm_ctx = make_commissioning_client_context()
    comm_reader, comm_writer = await asyncio.open_connection(
        host=target_host,
        port=comm_svc.port,
        ssl=comm_ctx,
        server_hostname=target_host,
    )

    zone_ca = generate_zone_ca("mash-py-phase11", ZoneType.LOCAL)
    try:
        sslobj = _ssl_object(comm_writer)
        assert sslobj is not None
        verify_tls13(sslobj)
        alpn = verify_alpn(sslobj)
        assert alpn == "mash-comm/1"

        pase = PASEClient(setup_code, discriminator)
        await asyncio.wait_for(
            pase.handshake(comm_reader, comm_writer), timeout=15.0
        )

        cert_client = CertExchangeClient(zone_ca=zone_ca, zone_priority=1)
        result = await asyncio.wait_for(
            cert_client.run(comm_reader, comm_writer), timeout=30.0
        )
        device_id = result.device_id
    finally:
        comm_writer.close()
        with contextlib.suppress(ConnectionError, OSError):
            await comm_writer.wait_closed()

    assert device_id and len(device_id) == 16

    async with Browser() as browser:
        try:
            op_svc = await browser.find_operational_by_device_id(
                device_id, timeout=30.0
            )
        except BrowseTimeoutError:
            pytest.fail(
                f"device did not advertise on _mash._tcp with DI={device_id!r}",
                pytrace=False,
            )

    op_host = _pick_target_host(op_svc)
    controller_cert = generate_controller_cert(
        zone_ca, controller_id="phase11-controller"
    )
    return op_host, op_svc.port, device_id, controller_cert


async def _open_operational_connection(
    op_host: str, op_port: int, device_id: str, controller_cert
) -> Connection:
    """Open the operational TLS stream and wrap it in a Connection."""
    reader, writer, _peer_cert = await open_operational_connection(
        op_host,
        op_port,
        controller_cert=controller_cert,
        server_hostname=device_id,
        expected_device_id=device_id,
    )
    return Connection(reader, writer, "mash/1")


# ---------------------------------------------------------------------------
# The Phase-11 acceptance gate.
#
# Split into three tests so the Invoke divergence (Issue #11) surfaces as
# a focused xfail without dragging the Read/Write/Subscribe pass with it:
#
#   - test_read_write_subscribe_against_go_device  -> happy-path pass
#   - test_invoke_remove_zone_against_go_device    -> xfail(strict=True) on Issue #11
#   - test_invalid_attribute_id_returns_status_error  -> negative-path sanity
#
# Each test commissions a fresh device. Sharing a single commissioned
# device across tests would be cheaper but also leaks state (zone
# present in one test affects another); the cost is tolerable here
# because Phase 11 only adds three interop tests total.
# ---------------------------------------------------------------------------


@pytest.mark.skipif(not _go_available(), reason="go toolchain not available")
@pytest.mark.slow
@pytest.mark.timeout(300)
async def test_read_write_subscribe_against_go_device(live_go_device) -> None:
    """Read / Write / Subscribe / Unsubscribe end-to-end against mash-go.

    Each operation gets its own assertion block. Notifications routing
    is exercised structurally — we don't try to provoke a Status state
    change because that requires either TestControl invocation or
    multi-controller coordination, both Phase 13+. The priming report
    is sufficient evidence the Subscribe path works.
    """
    proc, discriminator, setup_code, _port = live_go_device

    op_host, op_port, device_id, controller_cert = (
        await _commission_and_get_op_target(proc, discriminator, setup_code)
    )
    conn = await _open_operational_connection(op_host, op_port, device_id, controller_cert)
    client = InteractionClient(conn)
    client.start()

    notifications: list[Notification] = []
    sub: Subscription | None = None

    try:
        # 1) Read DeviceInfo.
        attrs = await asyncio.wait_for(
            client.read(0, FEATURE_DEVICE_INFO), timeout=15.0
        )
        assert attrs, "DeviceInfo Read returned empty map"
        assert attrs.get(ATTR_VENDOR_NAME) == "MASH-Phase11"
        assert attrs.get(ATTR_PRODUCT_NAME) == "EVSE-X"
        assert attrs.get(ATTR_SERIAL_NUMBER) == "PHASE11-INTEROP-001"
        di_attr = attrs.get(ATTR_DEVICE_ID)
        assert isinstance(di_attr, str) and di_attr

        # 2) Write DeviceInfo.label and read it back.
        new_label = "phase11-write-test"
        write_result = await asyncio.wait_for(
            client.write(0, FEATURE_DEVICE_INFO, {ATTR_LABEL: new_label}),
            timeout=15.0,
        )
        assert ATTR_LABEL in write_result, (
            f"Write response missing label (key {ATTR_LABEL}); got {write_result!r}"
        )
        assert write_result[ATTR_LABEL] == new_label

        reread = await asyncio.wait_for(
            client.read(0, FEATURE_DEVICE_INFO, [ATTR_LABEL]), timeout=15.0
        )
        assert reread.get(ATTR_LABEL) == new_label

        # 3) Subscribe to Status on the EVSE endpoint.
        async def on_notify(notif: Notification) -> None:
            notifications.append(notif)

        sub = await asyncio.wait_for(
            client.subscribe(EVSE_ENDPOINT_ID, FEATURE_STATUS, on_notify),
            timeout=15.0,
        )
        assert sub.subscription_id != 0, "device assigned subscriptionId 0 (reserved)"
        assert ATTR_OPERATING_STATE in sub.initial_values, (
            f"Subscribe priming report missing operatingState ({ATTR_OPERATING_STATE}); "
            f"got keys {sorted(sub.initial_values.keys())}"
        )
        assert isinstance(sub.initial_values[ATTR_OPERATING_STATE], int)

        # 4) Unsubscribe — verifies the §6.4 wire form (Subscribe op,
        #    endpoint=0, feature=0, payload {1: subscriptionId}) round-trips.
        await asyncio.wait_for(client.unsubscribe(sub), timeout=15.0)
        sub = None

    finally:
        if sub is not None:
            with contextlib.suppress(Exception):
                await asyncio.wait_for(client.unsubscribe(sub), timeout=5.0)
        with contextlib.suppress(Exception):
            await asyncio.wait_for(client.close(), timeout=10.0)

    assert client.is_closed
    assert conn.state is not ConnectionState.CONNECTED


# ---------------------------------------------------------------------------
# Invoke — XFAIL pending Issue #11
# ---------------------------------------------------------------------------


@pytest.mark.skipif(not _go_available(), reason="go toolchain not available")
@pytest.mark.slow
@pytest.mark.timeout(300)
@pytest.mark.xfail(
    reason=(
        "Issue #11: mash-go decodes Invoke parameters as map[string]any "
        "and silently drops integer-keyed entries (pkg/service/dispatch/"
        "protocol_handler.go:738). mash-py is spec-faithful per "
        "interaction-model.md §7.1 (integer keys). This test will flip "
        "to passing when mash-go is updated to decode and return "
        "integer-keyed parameter maps."
    ),
    strict=True,
)
async def test_invoke_remove_zone_against_go_device(live_go_device) -> None:
    """Invoke ``DeviceInfo.removeZone`` with spec-conformant integer-keyed
    parameters.

    Expected post-fix behaviour:
        - Request parameters: ``{1: "mash-py-phase11"}`` (zoneId at param id 1).
        - Response payload: ``{1: True}`` (removed at response field 1).

    Current behaviour against mash-go:
        - Device's ``protocol_handler.handleInvoke`` decodes the
          ``map[any]any`` parameters and drops every non-string key →
          ``params`` is empty → ``params["zoneId"]`` lookup misses →
          handler returns ``ErrInvalidParameters`` → wire status
          ``INVALID_PARAMETER (5)`` → :class:`StatusError` raised here.

    The xfail is ``strict=True``: if mash-go is fixed and this test
    starts passing, pytest will flag it (XPASS) so we know to remove
    the marker in the same change that closes Issue #11.
    """
    proc, discriminator, setup_code, _port = live_go_device

    op_host, op_port, device_id, controller_cert = (
        await _commission_and_get_op_target(proc, discriminator, setup_code)
    )
    conn = await _open_operational_connection(op_host, op_port, device_id, controller_cert)
    client = InteractionClient(conn)
    client.start()

    try:
        # Spec-conformant integer-keyed parameters per
        # interaction-model.md §7.1. mash-go's current decoder drops
        # these silently; once #11 lands the device will accept this
        # exact wire form.
        invoke_resp = await asyncio.wait_for(
            client.invoke(
                0,
                FEATURE_DEVICE_INFO,
                CMD_REMOVE_ZONE,
                parameters={CMD_REMOVE_ZONE_PARAM_ZONE_ID: "mash-py-phase11"},
            ),
            timeout=15.0,
        )
        # Spec-conformant integer-keyed response per §7.2.
        assert isinstance(invoke_resp, dict), (
            f"removeZone response must be a CBOR map; got {type(invoke_resp).__name__}"
        )
        assert invoke_resp.get(CMD_REMOVE_ZONE_RESP_REMOVED) is True, (
            f"removeZone returned removed!=True: {invoke_resp!r}"
        )
    finally:
        with contextlib.suppress(Exception):
            await asyncio.wait_for(client.close(), timeout=10.0)


# ---------------------------------------------------------------------------
# Negative path: StatusError surfaces device-side validation errors.
# ---------------------------------------------------------------------------


@pytest.mark.skipif(not _go_available(), reason="go toolchain not available")
@pytest.mark.slow
@pytest.mark.timeout(300)
async def test_invalid_feature_id_returns_status_error(live_go_device) -> None:
    """A Read for a non-existent feature MUST surface as
    :class:`StatusError`.

    We target a feature id that is not assigned in
    ``protocol-versions.yaml`` (``0xFE``). Per ``§8.1`` the device
    returns ``INVALID_FEATURE (2)``; older or stricter implementations
    may also return ``UNSUPPORTED (10)``. Either is a non-success
    status and surfaces as :class:`StatusError`.

    *Why not test invalid-attribute id?* The spec is ambiguous on
    partial Reads — mash-go returns a permissive ``Status.SUCCESS``
    with the unknown attribute simply omitted from the response map.
    That behaviour is defensible (saves a round-trip when most
    attributes are known) but not strictly required by ``§4.2``.
    Until the spec resolves the ambiguity, the negative-path sanity
    check uses a feature id, which ``§8.1`` explicitly classifies.
    """
    proc, discriminator, setup_code, _port = live_go_device

    op_host, op_port, device_id, controller_cert = (
        await _commission_and_get_op_target(proc, discriminator, setup_code)
    )
    conn = await _open_operational_connection(op_host, op_port, device_id, controller_cert)
    client = InteractionClient(conn)
    client.start()

    try:
        # Feature 0xFE is unassigned (protocol-versions.yaml feature_types
        # ranges from 0x01..0x0A). Reading it must fail.
        with pytest.raises(StatusError) as excinfo:
            await asyncio.wait_for(
                client.read(0, 0xFE), timeout=15.0
            )
        assert excinfo.value.status in (
            Status.INVALID_FEATURE,
            Status.UNSUPPORTED,
        ), (
            f"expected INVALID_FEATURE or UNSUPPORTED for unknown feature 0xFE; "
            f"got {excinfo.value.status.name}"
        )
    finally:
        with contextlib.suppress(Exception):
            await asyncio.wait_for(client.close(), timeout=10.0)
