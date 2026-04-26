"""SSLContext factories for MASH transport (DEC-082 two-port deployment).

Each factory returns an :class:`ssl.SSLContext` pinned to TLS 1.3 with a single
ALPN value. Operational contexts require mutual authentication against a Zone
CA pool; commissioning contexts do not authenticate the peer at the TLS layer
(SPAKE2+ handles that during the application-layer PASE exchange — see
``docs/security.md §4.6`` and ``docs/transport.md §5.3``).

cryptography → ssl bridge
-------------------------

We accept ``cryptography``'s :class:`x509.Certificate` and
:class:`EllipticCurvePrivateKey` objects directly because that is what the rest
of mash-py produces (see :mod:`mash_py.crypto.certs`). Python's :mod:`ssl`
module wants PEM file paths or PEM blobs, so the factories serialise to PEM
and load via :meth:`ssl.SSLContext.load_cert_chain` /
:meth:`ssl.SSLContext.load_verify_locations`.

We use ``load_cert_chain(cadata=...)`` and similar in-memory variants where
available; for chain loading we write to a temporary file because
:meth:`ssl.SSLContext.load_cert_chain` does not accept in-memory PEM. The
temp files are deleted as soon as ``load_cert_chain`` returns; the parsed
material lives inside the SSLContext.
"""

from __future__ import annotations

import contextlib
import os
import ssl
import tempfile

from cryptography import x509
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric.ec import EllipticCurvePrivateKey

from mash_py.transport.alpn import (
    ALPN_COMMISSIONING,
    ALPN_OPERATIONAL,
    SUPPORTED_ALPN_PROTOCOLS,
)

# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------


class TLSConfigError(ValueError):
    """Raised when a TLS context cannot be constructed from the given inputs."""


# ---------------------------------------------------------------------------
# Server-side contexts
# ---------------------------------------------------------------------------


def make_commissioning_context(
    self_signed_cert: x509.Certificate,
    private_key: EllipticCurvePrivateKey,
) -> ssl.SSLContext:
    """Build a server :class:`ssl.SSLContext` for the commissioning listener.

    Per ``docs/transport.md §5.3`` (DEC-082 two-port pattern):

    - TLS 1.3 only.
    - Server certificate is the device's self-signed commissioning cert
      (``security.md §4.6``).
    - ``CERT_NONE`` — clients are NOT authenticated at the TLS layer
      (PASE/SPAKE2+ does that at the application layer).
    - ALPN advertises ``mash-comm/1`` exclusively. A client offering only
      ``mash/1`` must be rejected at handshake.

    The cert/key are accepted as cryptography objects and serialised to PEM
    on the way into :meth:`ssl.SSLContext.load_cert_chain`.
    """
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    _pin_to_tls13(ctx)
    ctx.set_alpn_protocols([ALPN_COMMISSIONING])

    # No client cert verification during commissioning.
    ctx.verify_mode = ssl.CERT_NONE
    ctx.check_hostname = False

    # Disable session resumption for parity with mash-go (SessionTicketsDisabled).
    # TLS 1.3 session resumption rides on session tickets in Python's ssl.
    ctx.options |= ssl.OP_NO_TICKET

    _load_cert_and_key(ctx, self_signed_cert, private_key)
    return ctx


def make_operational_context(
    zone_ca: x509.Certificate,
    operational_cert: x509.Certificate,
    operational_key: EllipticCurvePrivateKey,
) -> ssl.SSLContext:
    """Build a server :class:`ssl.SSLContext` for the operational listener.

    Per ``docs/transport.md §5.3`` (DEC-082 two-port pattern) and ``security.md``:

    - TLS 1.3 only.
    - Server presents the device's operational certificate (signed by
      ``zone_ca``).
    - Client mutual auth: ``CERT_REQUIRED`` — Python rejects controllers
      whose cert chain does not validate against ``zone_ca``.
    - ALPN advertises ``mash/1`` exclusively.

    A single Zone CA per context is the multi-zone-friendly default; devices
    paired into multiple zones build one operational context per zone and
    pick by SNI / by listener (DEC-082 §"Implementation notes").
    """
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    _pin_to_tls13(ctx)
    ctx.set_alpn_protocols([ALPN_OPERATIONAL])

    ctx.verify_mode = ssl.CERT_REQUIRED
    # Hostname verification is meaningless for MASH — peers identify each
    # other by certificate CommonName == device-id, not by DNS name.
    # The application layer enforces device-id matching after the handshake.
    ctx.check_hostname = False

    ctx.options |= ssl.OP_NO_TICKET

    _load_cert_and_key(ctx, operational_cert, operational_key)
    _load_ca_pool(ctx, zone_ca)
    return ctx


# ---------------------------------------------------------------------------
# Client-side contexts
# ---------------------------------------------------------------------------


def make_commissioning_client_context() -> ssl.SSLContext:
    """Build a client :class:`ssl.SSLContext` for connecting to a commissioning
    listener.

    The server's certificate is self-signed and not security-relevant
    (PASE/SPAKE2+ handles authentication). We disable verification so the TLS
    handshake completes; security is provided at the application layer.
    """
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
    _pin_to_tls13(ctx)
    ctx.set_alpn_protocols([ALPN_COMMISSIONING])

    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE

    ctx.options |= ssl.OP_NO_TICKET
    return ctx


def make_operational_client_context(
    zone_ca: x509.Certificate,
    controller_cert: x509.Certificate,
    controller_key: EllipticCurvePrivateKey,
) -> ssl.SSLContext:
    """Build a client :class:`ssl.SSLContext` for operational connections.

    The controller presents its zone-CA-signed operational certificate and
    validates the device's certificate against the same Zone CA. Hostname
    verification is disabled; the application layer matches the device's
    CommonName against the expected device ID after the handshake.
    """
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
    _pin_to_tls13(ctx)
    ctx.set_alpn_protocols([ALPN_OPERATIONAL])

    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_REQUIRED

    ctx.options |= ssl.OP_NO_TICKET

    _load_cert_and_key(ctx, controller_cert, controller_key)
    _load_ca_pool(ctx, zone_ca)
    return ctx


# ---------------------------------------------------------------------------
# Post-handshake verification
# ---------------------------------------------------------------------------


def verify_tls13(sslobj: ssl.SSLObject | ssl.SSLSocket) -> None:
    """Confirm the connection negotiated TLS 1.3.

    Mirrors mash-go's ``VerifyTLS13`` (``pkg/transport/tls.go:178``).
    Even with ``minimum_version`` pinned, defence-in-depth: a future Python
    update or a misconfigured context could allow a downgrade.
    """
    version = sslobj.version()
    if version != "TLSv1.3":
        raise TLSConfigError(f"TLS version {version!r} is not TLS 1.3")


def verify_alpn(sslobj: ssl.SSLObject | ssl.SSLSocket) -> str:
    """Confirm the negotiated ALPN protocol is a MASH-supported value.

    Returns the negotiated protocol so the caller can route based on it
    (e.g. ``mash-comm/1`` → PASE state machine, ``mash/1`` → operational
    dispatcher).

    Raises :class:`TLSConfigError` when the peer negotiated either no ALPN
    or an unknown protocol. mash-go's ``VerifyALPN`` accepts both
    ``mash/1`` and ``mash-comm/1``; we do the same.
    """
    proto = sslobj.selected_alpn_protocol()
    if proto is None:
        raise TLSConfigError("no ALPN protocol was negotiated")
    if proto not in SUPPORTED_ALPN_PROTOCOLS:
        raise TLSConfigError(f"ALPN protocol {proto!r} is not a supported MASH version")
    return proto


# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------


def _pin_to_tls13(ctx: ssl.SSLContext) -> None:
    ctx.minimum_version = ssl.TLSVersion.TLSv1_3
    ctx.maximum_version = ssl.TLSVersion.TLSv1_3


def _load_cert_and_key(
    ctx: ssl.SSLContext,
    cert: x509.Certificate,
    key: EllipticCurvePrivateKey,
) -> None:
    """Serialise a (cert, key) pair to a temp PEM bundle and load it.

    :meth:`ssl.SSLContext.load_cert_chain` does not accept in-memory PEM, so
    we write to a temporary file with mode 0o600, load, then unlink. The
    SSLContext keeps an internal copy of the parsed material.
    """
    cert_pem = cert.public_bytes(serialization.Encoding.PEM)
    key_pem = key.private_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PrivateFormat.PKCS8,
        encryption_algorithm=serialization.NoEncryption(),
    )

    fd, path = tempfile.mkstemp(suffix=".pem")
    try:
        with os.fdopen(fd, "wb") as fh:
            fh.write(cert_pem)
            fh.write(key_pem)
        ctx.load_cert_chain(certfile=path)
    finally:
        with contextlib.suppress(OSError):
            os.unlink(path)


def _load_ca_pool(ctx: ssl.SSLContext, ca: x509.Certificate) -> None:
    """Add ``ca`` to the SSLContext's verify pool.

    :meth:`ssl.SSLContext.load_verify_locations` does accept in-memory PEM via
    the ``cadata`` argument, which is the simpler path here.
    """
    ca_pem = ca.public_bytes(serialization.Encoding.PEM).decode("ascii")
    ctx.load_verify_locations(cadata=ca_pem)


__all__ = [
    "TLSConfigError",
    "make_commissioning_client_context",
    "make_commissioning_context",
    "make_operational_client_context",
    "make_operational_context",
    "verify_alpn",
    "verify_tls13",
]
