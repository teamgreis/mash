"""SSLContext factory tests.

Spec drives these tests — DEC-082, ``docs/transport.md §3`` (TLS 1.3 only),
``docs/transport.md §5.3`` (per-listener exclusive ALPN). We assert the
context's *settings*, not its handshake behaviour; loopback handshake
behaviour is exercised by ``test_connection.py``.
"""

from __future__ import annotations

import ssl

import pytest

from mash_py.transport.alpn import (
    ALPN_COMMISSIONING,
    ALPN_OPERATIONAL,
    SUPPORTED_ALPN_PROTOCOLS,
)
from mash_py.transport.tls import (
    TLSConfigError,
    make_commissioning_client_context,
    make_commissioning_context,
    make_operational_client_context,
    make_operational_context,
    verify_alpn,
    verify_tls13,
)

# ---------------------------------------------------------------------------
# Server contexts
# ---------------------------------------------------------------------------


class TestCommissioningContext:
    def test_pinned_to_tls13(self, commissioning_bundle):
        ctx = make_commissioning_context(commissioning_bundle.cert, commissioning_bundle.key)
        assert ctx.minimum_version == ssl.TLSVersion.TLSv1_3
        assert ctx.maximum_version == ssl.TLSVersion.TLSv1_3

    def test_no_client_cert(self, commissioning_bundle):
        # PASE/SPAKE2+ provides authentication during commissioning, so the
        # TLS layer does NOT verify the client cert (security.md §4.6).
        ctx = make_commissioning_context(commissioning_bundle.cert, commissioning_bundle.key)
        assert ctx.verify_mode == ssl.CERT_NONE
        assert ctx.check_hostname is False

    def test_advertises_only_commissioning_alpn(self, commissioning_bundle):
        # Each listener exclusively advertises one ALPN per DEC-082.
        ctx = make_commissioning_context(commissioning_bundle.cert, commissioning_bundle.key)
        # ssl.SSLContext does not expose set protocols readback directly; we
        # verify behavioural exclusivity via loopback in test_connection.py.
        # Here we just ensure the factory accepts the inputs without error.
        assert isinstance(ctx, ssl.SSLContext)


class TestOperationalContext:
    def test_requires_client_cert(self, device_op_bundle):
        ctx = make_operational_context(
            device_op_bundle.zone_ca, device_op_bundle.cert, device_op_bundle.key
        )
        assert ctx.verify_mode == ssl.CERT_REQUIRED
        # Hostname verification stays off — MASH peers identify each other
        # by certificate CommonName == device-id, not by DNS name.
        assert ctx.check_hostname is False

    def test_pinned_to_tls13(self, device_op_bundle):
        ctx = make_operational_context(
            device_op_bundle.zone_ca, device_op_bundle.cert, device_op_bundle.key
        )
        assert ctx.minimum_version == ssl.TLSVersion.TLSv1_3
        assert ctx.maximum_version == ssl.TLSVersion.TLSv1_3


# ---------------------------------------------------------------------------
# Client contexts
# ---------------------------------------------------------------------------


class TestClientContexts:
    def test_commissioning_client_skips_verification(self):
        ctx = make_commissioning_client_context()
        assert ctx.verify_mode == ssl.CERT_NONE
        assert ctx.check_hostname is False
        assert ctx.minimum_version == ssl.TLSVersion.TLSv1_3

    def test_operational_client_verifies_against_zone_ca(self, controller_op_bundle):
        ctx = make_operational_client_context(
            controller_op_bundle.zone_ca,
            controller_op_bundle.cert,
            controller_op_bundle.key,
        )
        assert ctx.verify_mode == ssl.CERT_REQUIRED
        assert ctx.check_hostname is False


# ---------------------------------------------------------------------------
# Post-handshake verification helpers
# ---------------------------------------------------------------------------


class _FakeSSLObj:
    """Minimal stand-in exposing the methods the verify helpers use."""

    def __init__(self, version: str, alpn: str | None) -> None:
        self._version = version
        self._alpn = alpn

    def version(self) -> str:
        return self._version

    def selected_alpn_protocol(self) -> str | None:
        return self._alpn


class TestVerifyTLS13:
    def test_accepts_tls13(self):
        verify_tls13(_FakeSSLObj("TLSv1.3", ALPN_OPERATIONAL))  # type: ignore[arg-type]

    @pytest.mark.parametrize("version", ["TLSv1.2", "TLSv1.1", "SSLv3", "unknown"])
    def test_rejects_non_tls13(self, version: str):
        with pytest.raises(TLSConfigError):
            verify_tls13(_FakeSSLObj(version, ALPN_OPERATIONAL))  # type: ignore[arg-type]


class TestVerifyALPN:
    @pytest.mark.parametrize("alpn", list(SUPPORTED_ALPN_PROTOCOLS))
    def test_accepts_supported(self, alpn: str):
        assert verify_alpn(_FakeSSLObj("TLSv1.3", alpn)) == alpn  # type: ignore[arg-type]

    def test_rejects_no_alpn(self):
        with pytest.raises(TLSConfigError, match="no ALPN protocol"):
            verify_alpn(_FakeSSLObj("TLSv1.3", None))  # type: ignore[arg-type]

    @pytest.mark.parametrize("alpn", ["http/1.1", "h2", "mash/2", "mash-comm/2", ""])
    def test_rejects_unknown(self, alpn: str):
        with pytest.raises(TLSConfigError, match="not a supported MASH version"):
            verify_alpn(_FakeSSLObj("TLSv1.3", alpn))  # type: ignore[arg-type]


# ---------------------------------------------------------------------------
# ALPN constants
# ---------------------------------------------------------------------------


def test_alpn_constants_match_spec():
    """Spec values must not drift from docs/transport.md §5.3."""
    assert ALPN_OPERATIONAL == "mash/1"
    assert ALPN_COMMISSIONING == "mash-comm/1"
    assert SUPPORTED_ALPN_PROTOCOLS == ("mash/1", "mash-comm/1")
