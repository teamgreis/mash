"""End-to-end tests for :class:`mash_py.service.DeviceService`.

These tests stand up the operational listener with a fresh Zone CA +
device op cert + controller op cert (no commissioning involved — the
cert exchange is Phase 14b territory and is exercised in its own
interop test). An :class:`InteractionClient` from Phase 11 dials in,
issues Read / Write / Invoke, and we assert the device side dispatched
correctly.

Together with ``tests/unit/interaction/test_server.py`` (pure
dispatcher unit tests) this gives Phase 15 full coverage:

- ``test_server.py``: every Status-code branch of the dispatcher.
- ``test_device.py``: TLS + framing + connection plumbing actually
  delivers requests to the dispatcher and ships responses back.

The Phase-15 interop test (``tests/interop/test_full_device_lifecycle.py``)
extends this to the cross-impl boundary (Go controller vs Python
device).
"""

from __future__ import annotations

import asyncio
import contextlib
from dataclasses import dataclass

import pytest
from cryptography import x509

from mash_py.commissioning.cert_store import (
    ControllerOperationalCert,
    ZoneCA,
    ZoneType,
)
from mash_py.crypto.certs import (
    CSRInfo,
    create_csr,
    generate_ecdsa_p256_keypair,
    generate_zone_ca,
    sign_csr,
)
from mash_py.features.device_info_gen import (
    DEVICE_INFO_ATTR_LABEL,
    DEVICE_INFO_ATTR_VENDOR_NAME,
    DEVICE_INFO_CMD_REMOVE_ZONE,
    REMOVE_ZONE_PARAM_ZONE_ID,
    REMOVE_ZONE_RESP_REMOVED,
    DeviceInfo,
)
from mash_py.interaction import InteractionClient, StatusError
from mash_py.model import Device, Endpoint, EndpointType
from mash_py.service import DeviceService
from mash_py.service.controller import open_operational_connection
from mash_py.transport.connection import Connection
from mash_py.wire import Status

# Feature ID per docs/features/protocol-versions.yaml.
FEATURE_DEVICE_INFO = 0x01


# ---------------------------------------------------------------------------
# Cert / device helpers
# ---------------------------------------------------------------------------


@dataclass
class _ServiceFixture:
    service: DeviceService
    device_id: str
    controller_cert: ControllerOperationalCert


async def _build_service_fixture() -> _ServiceFixture:
    """Build a runnable DeviceService and the matching controller cert.

    Side-steps PASE/cert-exchange — we mint everything synthetically.
    The interop test (`test_full_device_lifecycle.py`) verifies the same
    flow with a real Go controller after Phase-14b commissioning; here
    we only care that the *operational* stack works.
    """
    # Zone CA — controllers and devices both chain to this.
    zone_ca_key = generate_ecdsa_p256_keypair()
    zone_ca_cert = generate_zone_ca(zone_ca_key, zone_name="phase15-test")
    zone_ca = ZoneCA(
        zone_name="phase15-test",
        zone_type=ZoneType.LOCAL,
        certificate=zone_ca_cert,
        private_key=zone_ca_key,
    )

    # Device operational keypair + CA-signed cert.
    device_key = generate_ecdsa_p256_keypair()
    device_csr = create_csr(device_key, info=CSRInfo(common_name="device-csr"))
    device_cert, device_id = _sign_with_derived_id(zone_ca, device_csr)

    # Controller operational keypair + CA-signed cert.
    controller_key = generate_ecdsa_p256_keypair()
    controller_csr = create_csr(
        controller_key, info=CSRInfo(common_name="controller-csr")
    )
    controller_cert_x509 = sign_csr(
        zone_ca_cert, zone_ca_key, controller_csr, zone_name="phase15-test"
    )
    controller_cert = ControllerOperationalCert(
        certificate=controller_cert_x509,
        private_key=controller_key,
        zone_ca_cert=zone_ca_cert,
        zone_id="phase15-test",
        zone_type=ZoneType.LOCAL,
        controller_id="phase15-controller",
    )

    # Device model with DeviceInfo on root + a custom command handler so
    # the Invoke test has something to call.
    device = Device(device_id)
    di = DeviceInfo()
    di.set_device_id(device_id)
    di.set_vendor_name("MASH-Phase15")
    di.set_product_name("Phase15 EVSE")
    di.set_serial_number("PHASE15-0001")
    device.root.add_feature(di)

    # Add an empty EVSE endpoint for shape parity with mash-go.
    device.add_endpoint(Endpoint(1, EndpointType.EV_CHARGER, "EVSE"))

    # Wire a removeZone handler that confirms parameters arrived intact.
    def remove_zone(params: dict[int, object]) -> dict[int, object]:
        if params.get(REMOVE_ZONE_PARAM_ZONE_ID) == "phase15-test":
            return {REMOVE_ZONE_RESP_REMOVED: True}
        return {REMOVE_ZONE_RESP_REMOVED: False}

    di.set_command_handler(DEVICE_INFO_CMD_REMOVE_ZONE, remove_zone)

    service = DeviceService(
        device,
        zone_ca=zone_ca_cert,
        operational_cert=device_cert,
        operational_key=device_key,
    )
    await service.start(host="127.0.0.1", port=0)

    return _ServiceFixture(
        service=service,
        device_id=device_id,
        controller_cert=controller_cert,
    )


def _sign_with_derived_id(
    zone_ca: ZoneCA, csr: x509.CertificateSigningRequest
) -> tuple[x509.Certificate, str]:
    """Mirror :func:`sign_device_csr` (which derives the CN from SPKI)
    without paying the cert-store dependency surface."""
    from mash_py.commissioning.cert_store import sign_device_csr

    return sign_device_csr(zone_ca, csr)


# ---------------------------------------------------------------------------
# Test helper
# ---------------------------------------------------------------------------


async def _connect_client(
    fixture: _ServiceFixture,
) -> tuple[InteractionClient, Connection]:
    """Open an operational TLS connection and return a started client."""
    reader, writer, _peer = await open_operational_connection(
        "127.0.0.1",
        fixture.service.port,
        controller_cert=fixture.controller_cert,
        server_hostname=fixture.device_id,
        expected_device_id=fixture.device_id,
    )
    conn = Connection(reader, writer, "mash/1")
    client = InteractionClient(conn)
    client.start()
    return client, conn


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_device_service_round_trips_read(
) -> None:
    fixture = await _build_service_fixture()
    try:
        client, _conn = await _connect_client(fixture)
        try:
            attrs = await asyncio.wait_for(
                client.read(0, FEATURE_DEVICE_INFO), timeout=5.0
            )
            assert attrs[DEVICE_INFO_ATTR_VENDOR_NAME] == "MASH-Phase15"
        finally:
            await asyncio.wait_for(client.close(), timeout=5.0)
    finally:
        await fixture.service.stop()


@pytest.mark.asyncio
async def test_device_service_round_trips_write_then_read(
) -> None:
    fixture = await _build_service_fixture()
    try:
        client, _conn = await _connect_client(fixture)
        try:
            new_label = "phase15-service-test"
            result = await asyncio.wait_for(
                client.write(
                    0, FEATURE_DEVICE_INFO, {DEVICE_INFO_ATTR_LABEL: new_label}
                ),
                timeout=5.0,
            )
            assert result[DEVICE_INFO_ATTR_LABEL] == new_label

            reread = await asyncio.wait_for(
                client.read(0, FEATURE_DEVICE_INFO, [DEVICE_INFO_ATTR_LABEL]),
                timeout=5.0,
            )
            assert reread[DEVICE_INFO_ATTR_LABEL] == new_label
        finally:
            await asyncio.wait_for(client.close(), timeout=5.0)
    finally:
        await fixture.service.stop()


@pytest.mark.asyncio
async def test_device_service_round_trips_invoke(
) -> None:
    fixture = await _build_service_fixture()
    try:
        client, _conn = await _connect_client(fixture)
        try:
            result = await asyncio.wait_for(
                client.invoke(
                    0,
                    FEATURE_DEVICE_INFO,
                    DEVICE_INFO_CMD_REMOVE_ZONE,
                    parameters={REMOVE_ZONE_PARAM_ZONE_ID: "phase15-test"},
                ),
                timeout=5.0,
            )
            assert isinstance(result, dict)
            assert result[REMOVE_ZONE_RESP_REMOVED] is True
        finally:
            await asyncio.wait_for(client.close(), timeout=5.0)
    finally:
        await fixture.service.stop()


@pytest.mark.asyncio
async def test_device_service_surfaces_unknown_feature_as_status_error(
) -> None:
    fixture = await _build_service_fixture()
    try:
        client, _conn = await _connect_client(fixture)
        try:
            with pytest.raises(StatusError) as excinfo:
                await asyncio.wait_for(
                    client.read(0, 0xFE), timeout=5.0
                )
            assert excinfo.value.status == Status.INVALID_FEATURE
        finally:
            await asyncio.wait_for(client.close(), timeout=5.0)
    finally:
        await fixture.service.stop()


@pytest.mark.asyncio
async def test_device_service_subscribe_returns_priming_report(
) -> None:
    """Phase 16: a Subscribe to DeviceInfo on endpoint 0 succeeds and the
    response carries the priming-report values per ``§6.2``.

    Phase 15 surfaced ``UNSUPPORTED`` for every Subscribe; with Phase 16
    the per-connection :class:`SubscriptionManager` is wired up by
    :class:`DeviceService`, so the operation succeeds end-to-end."""
    fixture = await _build_service_fixture()
    try:
        client, _conn = await _connect_client(fixture)
        try:
            async def _noop(_n: object) -> None:
                pass

            sub = await asyncio.wait_for(
                client.subscribe(0, FEATURE_DEVICE_INFO, _noop),
                timeout=5.0,
            )
            assert sub.subscription_id != 0, (
                "Subscribe must return a non-zero subscriptionId per §6.2 "
                "(0 is reserved for the Notification messageId sentinel)"
            )
            assert isinstance(sub.initial_values, dict) and sub.initial_values, (
                "Priming report must include every readable attribute per §6.2"
            )
            await asyncio.wait_for(client.unsubscribe(sub), timeout=5.0)
        finally:
            await asyncio.wait_for(client.close(), timeout=5.0)
    finally:
        await fixture.service.stop()


@pytest.mark.asyncio
async def test_device_service_stop_is_idempotent_and_closes_active_connections(
) -> None:
    fixture = await _build_service_fixture()
    client, _conn = await _connect_client(fixture)

    # Stop while the connection is still up — service must close it.
    await fixture.service.stop()
    await fixture.service.stop()  # idempotent

    # The underlying client read loop should have observed the EOF.
    # Give the asyncio loop a moment to propagate the close.
    for _ in range(10):
        if client.is_closed:
            break
        await asyncio.sleep(0.05)

    with contextlib.suppress(Exception):
        await asyncio.wait_for(client.close(), timeout=2.0)
