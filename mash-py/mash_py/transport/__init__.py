"""MASH transport — TLS 1.3 + length-prefixed framing on top of asyncio.

Two-port deployment per DEC-082: each :class:`ssl.SSLContext` advertises one
ALPN protocol exclusively. The :class:`mash_py.service.listeners.TwoPortListener`
binds one socket per ALPN and shares a single per-device connection cap counter
(DEC-064) across both accept loops.

The Connection class is a single-direction async pipe — ``read_loop`` consumes
frames, dispatches application messages to a handler, and answers Ping/Pong/
Close inline. The codec for those control messages comes from
:mod:`mash_py.wire.codec`; the framing comes from :mod:`mash_py.wire.framing`.
"""

from __future__ import annotations

from mash_py.transport.alpn import (
    ALPN_COMMISSIONING,
    ALPN_OPERATIONAL,
    DEFAULT_OPERATIONAL_PORT,
    SUPPORTED_ALPN_PROTOCOLS,
)
from mash_py.transport.connection import (
    Connection,
    ConnectionClosedError,
    ConnectionConfig,
    ConnectionHandler,
    ConnectionState,
    ConnectionVerificationError,
    accept,
    connect,
)
from mash_py.transport.tls import (
    make_commissioning_client_context,
    make_commissioning_context,
    make_operational_client_context,
    make_operational_context,
    verify_alpn,
    verify_tls13,
)

__all__ = [
    "ALPN_COMMISSIONING",
    "ALPN_OPERATIONAL",
    "DEFAULT_OPERATIONAL_PORT",
    "SUPPORTED_ALPN_PROTOCOLS",
    "Connection",
    "ConnectionClosedError",
    "ConnectionConfig",
    "ConnectionHandler",
    "ConnectionState",
    "ConnectionVerificationError",
    "accept",
    "connect",
    "make_commissioning_client_context",
    "make_commissioning_context",
    "make_operational_client_context",
    "make_operational_context",
    "verify_alpn",
    "verify_tls13",
]
