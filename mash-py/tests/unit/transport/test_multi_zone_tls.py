"""Multi-zone TLS context tests (Phase 20).

Verify that ``make_multi_zone_operational_context`` selects the right
per-zone server cert based on the client's SNI extension, and falls
back to the default cert when SNI is missing or unmatched.

The "right cert" check inspects ``getpeercert(binary_form=True)`` on
the client side and compares the leaf cert's CommonName against the
expected per-zone device-id. This mirrors how a controller in mash-go
verifies the device after a successful handshake.

Spec sources:
- ``docs/security.md §5.1`` — post-handshake zone identification.
- ``mash-go/pkg/service/device_service_lifecycle.go::buildOperationalTLSConfig``
  — reference behaviour we mirror.
"""

from __future__ import annotations

import asyncio
from collections.abc import AsyncIterator
from dataclasses import dataclass

import pytest
from cryptography import x509
from cryptography.hazmat.primitives.asymmetric.ec import EllipticCurvePrivateKey
from cryptography.x509.oid import NameOID

from mash_py.crypto.certs import (
    CSRInfo,
    create_csr,
    generate_ecdsa_p256_keypair,
    generate_zone_ca,
    sign_csr,
)
from mash_py.transport.tls import (
    TLSConfigError,
    ZoneCertEntry,
    make_multi_zone_operational_context,
    make_operational_client_context,
)


# ---------------------------------------------------------------------------
# Fixtures: build two complete zones
# ---------------------------------------------------------------------------


@dataclass
class ZoneFixture:
    name: str
    device_id: str
    zone_ca: x509.Certificate
    zone_ca_key: EllipticCurvePrivateKey
    device_cert: x509.Certificate
    device_key: EllipticCurvePrivateKey
    controller_cert: x509.Certificate
    controller_key: EllipticCurvePrivateKey


def _build_zone(name: str) -> ZoneFixture:
    """Build a complete zone with a device cert + a controller cert.

    Note: ``sign_csr`` re-derives the operational cert's CommonName
    from the CSR public key per ``commissioning-pase.md §4.4`` —
    ``SHA256(SPKI_DER)[:8].hex().upper()`` — so we pull the actual
    device-id back out of the signed cert rather than passing one in.
    """
    ca_key = generate_ecdsa_p256_keypair()
    ca_cert = generate_zone_ca(ca_key, zone_name=name)

    device_key = generate_ecdsa_p256_keypair()
    device_csr = create_csr(device_key, info=CSRInfo(common_name="placeholder"))
    device_cert = sign_csr(ca_cert, ca_key, device_csr, zone_name=name)
    device_id = device_cert.subject.get_attributes_for_oid(NameOID.COMMON_NAME)[
        0
    ].value

    controller_key = generate_ecdsa_p256_keypair()
    controller_csr = create_csr(
        controller_key, info=CSRInfo(common_name=f"controller-{name}")
    )
    controller_cert = sign_csr(ca_cert, ca_key, controller_csr, zone_name=name)

    return ZoneFixture(
        name=name,
        device_id=device_id,
        zone_ca=ca_cert,
        zone_ca_key=ca_key,
        device_cert=device_cert,
        device_key=device_key,
        controller_cert=controller_cert,
        controller_key=controller_key,
    )


@pytest.fixture
def grid_zone() -> ZoneFixture:
    return _build_zone(name="grid-zone")


@pytest.fixture
def local_zone() -> ZoneFixture:
    return _build_zone(name="local-zone")


# ---------------------------------------------------------------------------
# Helpers: server boot + cert capture
# ---------------------------------------------------------------------------


async def _boot_multi_zone_server(
    grid: ZoneFixture, local: ZoneFixture
) -> tuple[asyncio.Server, int]:
    """Start an asyncio server with a multi-zone operational context.
    Each accepted connection completes the TLS handshake and then
    closes — we don't need the application loop here."""
    ctx = make_multi_zone_operational_context(
        entries=[
            ZoneCertEntry(
                device_id=grid.device_id,
                operational_cert=grid.device_cert,
                operational_key=grid.device_key,
                zone_ca=grid.zone_ca,
            ),
            ZoneCertEntry(
                device_id=local.device_id,
                operational_cert=local.device_cert,
                operational_key=local.device_key,
                zone_ca=local.zone_ca,
            ),
        ]
    )

    async def _handle(
        reader: asyncio.StreamReader, writer: asyncio.StreamWriter
    ) -> None:
        # Just complete the handshake and close. The handshake has
        # already happened by the time this coroutine runs.
        try:
            writer.close()
            await writer.wait_closed()
        except (ConnectionError, OSError):
            pass

    server = await asyncio.start_server(
        _handle, host="127.0.0.1", port=0, ssl=ctx
    )
    port = server.sockets[0].getsockname()[1]
    return server, port


async def _connect_and_capture_server_cn(
    port: int,
    controller_zone: ZoneFixture,
    server_hostname: str | None,
) -> str:
    """Connect from ``controller_zone``'s perspective with the given
    SNI; return the CN of the server cert the device presented."""
    client_ctx = make_operational_client_context(
        zone_ca=controller_zone.zone_ca,
        controller_cert=controller_zone.controller_cert,
        controller_key=controller_zone.controller_key,
    )
    reader, writer = await asyncio.open_connection(
        host="127.0.0.1",
        port=port,
        ssl=client_ctx,
        server_hostname=server_hostname or "",
    )
    try:
        sslobj = writer.get_extra_info("ssl_object")
        peer_der = sslobj.getpeercert(binary_form=True)
        peer_cert = x509.load_der_x509_certificate(peer_der)
        cn_attrs = peer_cert.subject.get_attributes_for_oid(NameOID.COMMON_NAME)
        return cn_attrs[0].value
    finally:
        writer.close()
        try:
            await writer.wait_closed()
        except (ConnectionError, OSError):
            pass


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_grid_sni_selects_grid_cert(
    grid_zone: ZoneFixture, local_zone: ZoneFixture
) -> None:
    """Controller from GRID zone sends SNI = grid device-id; server
    must present the grid zone's cert."""
    server, port = await _boot_multi_zone_server(grid_zone, local_zone)
    try:
        cn = await _connect_and_capture_server_cn(
            port, controller_zone=grid_zone, server_hostname=grid_zone.device_id
        )
        assert cn == grid_zone.device_id
    finally:
        server.close()
        await server.wait_closed()


@pytest.mark.asyncio
async def test_local_sni_selects_local_cert(
    grid_zone: ZoneFixture, local_zone: ZoneFixture
) -> None:
    server, port = await _boot_multi_zone_server(grid_zone, local_zone)
    try:
        cn = await _connect_and_capture_server_cn(
            port, controller_zone=local_zone, server_hostname=local_zone.device_id
        )
        assert cn == local_zone.device_id
    finally:
        server.close()
        await server.wait_closed()


@pytest.mark.asyncio
async def test_sni_lookup_is_case_insensitive(
    grid_zone: ZoneFixture, local_zone: ZoneFixture
) -> None:
    """Controllers may not normalise device-id casing; the lookup
    must match Go's behaviour and treat it as case-insensitive."""
    server, port = await _boot_multi_zone_server(grid_zone, local_zone)
    try:
        cn = await _connect_and_capture_server_cn(
            port,
            controller_zone=grid_zone,
            server_hostname=grid_zone.device_id.lower(),
        )
        assert cn == grid_zone.device_id
    finally:
        server.close()
        await server.wait_closed()


@pytest.mark.asyncio
async def test_unknown_sni_falls_back_to_default(
    grid_zone: ZoneFixture, local_zone: ZoneFixture
) -> None:
    """SNI that doesn't match any device-id should not abort the
    handshake — the server just presents the default (first) entry's
    cert. The client's mTLS pool covers both zones, so verification
    still succeeds."""
    server, port = await _boot_multi_zone_server(grid_zone, local_zone)
    try:
        cn = await _connect_and_capture_server_cn(
            port,
            controller_zone=grid_zone,
            server_hostname="some-other-device-id",
        )
        # Default = first entry = grid
        assert cn == grid_zone.device_id
    finally:
        server.close()
        await server.wait_closed()


@pytest.mark.asyncio
async def test_explicit_default_overrides_first_entry(
    grid_zone: ZoneFixture, local_zone: ZoneFixture
) -> None:
    """``default_entry`` lets the caller pick which cert is presented
    on unknown SNI, regardless of insertion order."""
    ctx = make_multi_zone_operational_context(
        entries=[
            ZoneCertEntry(
                device_id=grid_zone.device_id,
                operational_cert=grid_zone.device_cert,
                operational_key=grid_zone.device_key,
                zone_ca=grid_zone.zone_ca,
            ),
            ZoneCertEntry(
                device_id=local_zone.device_id,
                operational_cert=local_zone.device_cert,
                operational_key=local_zone.device_key,
                zone_ca=local_zone.zone_ca,
            ),
        ],
        default_entry=ZoneCertEntry(
            device_id=local_zone.device_id,
            operational_cert=local_zone.device_cert,
            operational_key=local_zone.device_key,
            zone_ca=local_zone.zone_ca,
        ),
    )

    async def _handle(reader, writer) -> None:
        writer.close()
        try:
            await writer.wait_closed()
        except (ConnectionError, OSError):
            pass

    server = await asyncio.start_server(
        _handle, host="127.0.0.1", port=0, ssl=ctx
    )
    port = server.sockets[0].getsockname()[1]
    try:
        # Controller from LOCAL zone (matches the explicit default).
        # Sending unknown SNI should fall back to the default cert,
        # which the local-zone controller can verify against its CA.
        cn = await _connect_and_capture_server_cn(
            port,
            controller_zone=local_zone,
            server_hostname="unknown-device",
        )
        # Default entry = local (overrides first-entry default)
        assert cn == local_zone.device_id
    finally:
        server.close()
        await server.wait_closed()


def test_empty_entries_rejected() -> None:
    with pytest.raises(TLSConfigError):
        make_multi_zone_operational_context(entries=[])
