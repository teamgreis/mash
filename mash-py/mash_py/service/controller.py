"""Controller-side post-commissioning flows (Phase 10).

This module wraps the Phase-10 acceptance gate from
``mash-py-tdd-plan.md``: after :class:`PASEClient` (Phase 8) and
:class:`CertExchangeClient` (Phase 9) finish on the commissioning ALPN,
the controller closes that connection and opens a fresh TLS connection
on the operational ALPN ``mash/1`` with mutual TLS, then issues a Read
on DeviceInfo (endpoint 0, feature 0x01) and decodes the response.

The flow lives at the service layer because it stitches together
artifacts from three lower layers — discovery, commissioning, and
transport — and produces an artifact (the live operational
:class:`asyncio.StreamReader`/``StreamWriter`` pair plus the verified
device DeviceInfo payload) that higher-level callers (Phase 11
``InteractionClient``) will own.

Phase 10 deliberately stops at *one* Read. Subscribe / Write / Invoke
plumbing arrives in Phase 11 once the request/response correlation
machinery exists.

Spec sources:

- ``docs/security.md §4.5 / §5.1`` — operational reconnect after
  ``CommissioningComplete``; "Both sides present operational
  certificates during TLS handshake" and "Both verify certificates are
  signed by same Zone CA".
- ``docs/transport.md §5.3`` — ALPN ``mash/1`` only on the operational
  config; mDNS SRV is the authoritative port.
- ``docs/interaction-model.md §3 / §4`` — Request/Response wire form;
  Read with empty attribute list returns all attributes.
- ``docs/features/device-info/1.0.yaml`` — DeviceInfo attribute IDs;
  the controller only knows the IDs that survive into the response.

Where mash-go diverges from the spec, mash-py implements the spec — see
:mod:`mash_py.commissioning.cert_store` for the controller-cert example
(mash-go adds a DNS SAN that the spec does not require).
"""

from __future__ import annotations

import asyncio
import contextlib
import ssl
from dataclasses import dataclass
from typing import Any

from cryptography import x509

from mash_py.commissioning.cert_store import ControllerOperationalCert
from mash_py.transport.connection import _ssl_object
from mash_py.transport.tls import (
    make_operational_client_context,
    verify_alpn,
    verify_tls13,
)
from mash_py.wire import (
    DEFAULT_MAX_MESSAGE_SIZE,
    AsyncFrameReader,
    AsyncFrameWriter,
    Operation,
    ReadPayload,
    Request,
    Response,
    Status,
    decode_response,
    encode_request,
)

# DeviceInfo addressing per docs/features/protocol-versions.yaml + §1.
DEVICE_INFO_ENDPOINT_ID: int = 0
DEVICE_INFO_FEATURE_ID: int = 0x01


# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------


class OperationalConnectionError(Exception):
    """Base class for Phase-10 operational-connection failures."""


class OperationalHandshakeError(OperationalConnectionError):
    """The TLS handshake completed but failed verification (TLS 1.3, ALPN,
    chain-of-trust). Distinct from a network-level connect failure so
    callers can choose to surface a handshake bug differently from a
    transient connectivity issue."""


class OperationalProtocolError(OperationalConnectionError):
    """Peer returned a frame the controller could not parse, or returned
    a Response whose ``MessageID`` does not match the Request. Mirrors
    :class:`PASEProtocolError` in spirit — the protocol contract was
    violated and we refuse to absorb it silently."""


class DeviceInfoReadError(OperationalConnectionError):
    """The device returned a non-success status to the DeviceInfo Read.
    Carries the status code so callers can distinguish READ_ONLY (6),
    INVALID_ENDPOINT (1), etc. — see :class:`mash_py.wire.Status`.
    """

    def __init__(self, status: Status, message: str = "") -> None:
        super().__init__(
            f"DeviceInfo Read failed: status={int(status)} ({status.name})"
            + (f": {message}" if message else "")
        )
        self.status = status
        self.message = message


# ---------------------------------------------------------------------------
# Result type
# ---------------------------------------------------------------------------


@dataclass(frozen=True, slots=True)
class DeviceInfoSnapshot:
    """Decoded DeviceInfo attribute map plus context.

    The wire form of a Read response payload is a CBOR map keyed by
    attribute ID (``docs/interaction-model.md §4.2``). We hand the raw
    map to the caller — pretty-naming the IDs becomes Phase 12's job
    (codegen produces the attribute constants).

    ``device_id`` is the cert's CommonName per
    ``commissioning-pase.md §4.4``; surfacing it on the snapshot lets
    callers correlate the mDNS-discovered device-id with what the
    handshake actually authenticated against.
    """

    attributes: dict[int, Any]
    device_id: str
    peer_cert: x509.Certificate


# ---------------------------------------------------------------------------
# Operational connect helper
# ---------------------------------------------------------------------------


async def open_operational_connection(
    host: str,
    port: int,
    *,
    controller_cert: ControllerOperationalCert,
    server_hostname: str | None = None,
    expected_device_id: str | None = None,
) -> tuple[asyncio.StreamReader, asyncio.StreamWriter, x509.Certificate]:
    """Open a TLS 1.3 mTLS connection on the operational ALPN.

    Returns ``(reader, writer, peer_cert)``. The caller owns the
    writer — they MUST call ``writer.close()`` and ``await
    writer.wait_closed()`` to tear it down. This split mirrors Phase 8
    / Phase 9 (PASE / cert-install ran on a caller-owned commissioning
    connection) so the same pattern carries forward into Phase 11.

    ``server_hostname`` is forwarded to :func:`asyncio.open_connection`
    purely for SNI; mash-py's operational SSLContext disables hostname
    verification (peers identify each other by cert CN, not DNS — see
    ``mash_py.transport.tls``). When ``server_hostname`` is ``None`` we
    pass ``host`` so the SNI extension is at least populated.

    ``expected_device_id`` is an optional sanity check: when provided,
    the device's leaf cert CN MUST match. Mismatch raises
    :class:`OperationalHandshakeError`. The mDNS browse already gives
    us a device-id via the ``DI`` TXT record, so plumbing that value
    through here lets the caller catch a wrong-device handshake before
    sending application traffic.
    """
    ssl_ctx = make_operational_client_context(
        zone_ca=controller_cert.zone_ca_cert,
        controller_cert=controller_cert.certificate,
        controller_key=controller_cert.private_key,
    )

    reader, writer = await asyncio.open_connection(
        host=host,
        port=port,
        ssl=ssl_ctx,
        server_hostname=server_hostname or host,
    )

    try:
        sslobj = _ssl_object(writer)
        if sslobj is None:
            raise OperationalHandshakeError("transport is not TLS")
        try:
            verify_tls13(sslobj)
            alpn = verify_alpn(sslobj)
        except Exception as exc:
            raise OperationalHandshakeError(str(exc)) from exc
        if alpn != "mash/1":
            raise OperationalHandshakeError(
                f"expected ALPN mash/1, got {alpn!r}"
            )

        peer_der = sslobj.getpeercert(binary_form=True)
        if not peer_der:
            raise OperationalHandshakeError(
                "no peer certificate after mTLS handshake"
            )
        peer_cert = x509.load_der_x509_certificate(peer_der)

        if expected_device_id is not None:
            from cryptography.x509.oid import NameOID

            cn_attrs = peer_cert.subject.get_attributes_for_oid(
                NameOID.COMMON_NAME
            )
            if not cn_attrs:
                raise OperationalHandshakeError(
                    "device cert has no CommonName"
                )
            if cn_attrs[0].value != expected_device_id:
                raise OperationalHandshakeError(
                    f"device cert CN {cn_attrs[0].value!r} does not match "
                    f"expected device-id {expected_device_id!r}"
                )

        return reader, writer, peer_cert
    except BaseException:
        writer.close()
        with contextlib.suppress(Exception):
            await writer.wait_closed()
        raise


# ---------------------------------------------------------------------------
# DeviceInfo Read
# ---------------------------------------------------------------------------


async def read_device_info(
    reader: asyncio.StreamReader,
    writer: asyncio.StreamWriter,
    *,
    message_id: int = 1,
    max_message_size: int = DEFAULT_MAX_MESSAGE_SIZE,
) -> dict[int, Any]:
    """Issue a single Read for endpoint 0 / feature 0x01 / all attributes.

    Returns the decoded attribute map (CBOR uint -> attribute value) per
    ``docs/interaction-model.md §4.2``. Empty attribute list means
    "read all" (§4.3).

    Validates that the response's ``MessageID`` matches the request's;
    the spec lets the device interleave notifications on the same
    stream, so a mismatch could indicate either a bug or a notification
    arriving before the response. We treat any mismatch as a protocol
    error in Phase 10 and let Phase 11's :class:`InteractionClient`
    layer in proper correlation.
    """
    if message_id <= 0:
        raise ValueError(
            f"message_id must be in 1..2^32-1, got {message_id} (0 is reserved "
            "for notifications per docs/interaction-model.md §3.5)"
        )

    frame_writer = AsyncFrameWriter(writer, max_message_size=max_message_size)
    frame_reader = AsyncFrameReader(reader, max_message_size=max_message_size)

    request = Request(
        message_id=message_id,
        operation=Operation.READ,
        endpoint_id=DEVICE_INFO_ENDPOINT_ID,
        feature_id=DEVICE_INFO_FEATURE_ID,
        payload=ReadPayload(attribute_ids=[]),  # empty = all attributes
    )

    try:
        await frame_writer.write_frame(encode_request(request))
        await writer.drain()
    except (OSError, ssl.SSLError) as exc:
        raise OperationalProtocolError(
            f"failed to send DeviceInfo Read: {exc}"
        ) from exc

    try:
        frame = await frame_reader.read_frame()
    except (asyncio.IncompleteReadError, OSError, ssl.SSLError) as exc:
        raise OperationalProtocolError(
            f"failed to read DeviceInfo Response: {exc}"
        ) from exc

    try:
        response: Response = decode_response(frame)
    except (ValueError, KeyError) as exc:
        raise OperationalProtocolError(
            f"malformed DeviceInfo Response: {exc}"
        ) from exc

    if response.message_id != message_id:
        raise OperationalProtocolError(
            f"DeviceInfo Response messageId {response.message_id} does not "
            f"match Request {message_id}"
        )

    if response.status != Status.SUCCESS:
        # Try to surface the device's error message if it included one.
        # Per ``interaction-model.md §8.2`` the error payload is
        # ``{1: message}`` (omitempty); the codec leaves the dict raw.
        msg = ""
        if isinstance(response.payload, dict):
            raw = response.payload.get(1)
            if isinstance(raw, str):
                msg = raw
        raise DeviceInfoReadError(Status(response.status), msg)

    if response.payload is None:
        # A success Read with no attributes — possible if the feature
        # is empty, but DeviceInfo is mandatory (see device-info/1.0.yaml)
        # and always carries at least deviceId, vendorName, productName.
        return {}

    if not isinstance(response.payload, dict):
        raise OperationalProtocolError(
            f"DeviceInfo Response payload must be a CBOR map, got "
            f"{type(response.payload).__name__}"
        )

    # Coerce the map keys to int. cbor2 already gives us ints for integer
    # keys, but a stricter type check guards against a peer encoding
    # attribute IDs as strings (which would be a spec violation).
    out: dict[int, Any] = {}
    for k, v in response.payload.items():
        if not isinstance(k, int):
            raise OperationalProtocolError(
                f"DeviceInfo Response payload has non-int key {k!r}"
            )
        out[k] = v
    return out


# ---------------------------------------------------------------------------
# End-to-end convenience
# ---------------------------------------------------------------------------


async def reconnect_and_read_device_info(
    host: str,
    port: int,
    *,
    controller_cert: ControllerOperationalCert,
    server_hostname: str | None = None,
    expected_device_id: str | None = None,
    timeout: float = 30.0,
) -> DeviceInfoSnapshot:
    """Open the operational TLS connection, Read DeviceInfo, close.

    Phase-10 acceptance gate convenience: combines
    :func:`open_operational_connection` and :func:`read_device_info`
    into a single call so the interop test (and future controller
    bootstrap code) does not need to manage the writer lifecycle.

    The ``timeout`` covers the entire flow — handshake + Read.
    """

    async def _flow() -> DeviceInfoSnapshot:
        reader, writer, peer_cert = await open_operational_connection(
            host,
            port,
            controller_cert=controller_cert,
            server_hostname=server_hostname,
            expected_device_id=expected_device_id,
        )
        try:
            attrs = await read_device_info(reader, writer)
        finally:
            writer.close()
            with contextlib.suppress(ConnectionError, OSError):
                await writer.wait_closed()

        # Pull the device-id out of the cert CN. We've already verified
        # the cert chains to the Zone CA via the SSLContext; the CN is
        # the protocol-level identity.
        from cryptography.x509.oid import NameOID

        cn_attrs = peer_cert.subject.get_attributes_for_oid(
            NameOID.COMMON_NAME
        )
        device_id = cn_attrs[0].value if cn_attrs else ""
        return DeviceInfoSnapshot(
            attributes=attrs,
            device_id=device_id,
            peer_cert=peer_cert,
        )

    return await asyncio.wait_for(_flow(), timeout=timeout)


__all__ = [
    "DEVICE_INFO_ENDPOINT_ID",
    "DEVICE_INFO_FEATURE_ID",
    "DeviceInfoReadError",
    "DeviceInfoSnapshot",
    "OperationalConnectionError",
    "OperationalHandshakeError",
    "OperationalProtocolError",
    "open_operational_connection",
    "read_device_info",
    "reconnect_and_read_device_info",
]
