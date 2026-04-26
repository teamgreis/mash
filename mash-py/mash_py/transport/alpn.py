"""ALPN protocol identifiers and well-known ports for MASH transport.

Per ``docs/transport.md §5.3`` and DEC-082:

- Operational connections negotiate ``mash/1`` (mutual TLS, Zone CA-signed certs).
- Commissioning connections negotiate ``mash-comm/1`` (server-only self-signed
  cert; client authenticated via PASE/SPAKE2+, not TLS).

Each :class:`ssl.SSLContext` in the two-port deployment advertises one of these
protocols exclusively. The default operational port is 8443; commissioning has
no canonical default port — implementations choose a free port and advertise it
via the ``_mash-comm._tcp`` SRV record (Phase 6 / Phase 18).
"""

from __future__ import annotations

ALPN_OPERATIONAL: str = "mash/1"
"""ALPN identifier for operational connections (post-commissioning, mTLS)."""

ALPN_COMMISSIONING: str = "mash-comm/1"
"""ALPN identifier for commissioning connections (pre-PASE, server-only TLS)."""

SUPPORTED_ALPN_PROTOCOLS: tuple[str, ...] = (ALPN_OPERATIONAL, ALPN_COMMISSIONING)
"""Every ALPN value MASH peers may legitimately negotiate. Used by
:func:`mash_py.transport.tls.verify_alpn` to fail fast on a wrong-listener
connection."""

DEFAULT_OPERATIONAL_PORT: int = 8443
"""Default port for the operational listener (``docs/transport.md §5.3``).
Commissioning has no canonical default — peers consult the mDNS SRV record."""


__all__ = [
    "ALPN_COMMISSIONING",
    "ALPN_OPERATIONAL",
    "DEFAULT_OPERATIONAL_PORT",
    "SUPPORTED_ALPN_PROTOCOLS",
]
