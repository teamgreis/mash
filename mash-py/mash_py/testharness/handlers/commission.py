"""``commission`` action + precondition setup helpers.

The commission handler drives PASE (Phase 8) and CSR-based cert install
(Phase 9) end-to-end against a Go device, mirroring the existing
``tests/interop/commissioning/test_full_commissioning_against_go.py``.
The result is stashed on ``state.custom`` so subsequent steps (e.g.
``connect_operational``, ``read``) can reuse the operational cert and
zone CA without re-commissioning.

Spec sources (canonical, NOT the Go runner):

* PASE shape — ``docs/commissioning-pase.md``, DEC-084.
* Cert exchange envelope — DEC-085 (``commissioning-cert-exchange.md``).
* Operational ALPN + reconnect — ``transport.md §5.3``, ``security.md §4.5``.

Precondition setup translates the YAML ``preconditions:`` list into
real-world state. Only conditions that are common across the Phase-21
acceptance set are honored:

* ``device_in_commissioning_mode`` / ``tls_connection_established`` /
  ``device_advertising`` / ``device_in_network`` — no-op (the harness
  assumes the device is already running in commissioning mode; the CLI
  driver is responsible for that).
* ``device_commissioned`` / ``operational_connection_established`` /
  ``zone_created`` / ``fresh_commission`` — invoke ``commission`` once
  with default params, stashing the result so steps see it as done.
* Anything else — pass-through (the test author is responsible for
  ensuring the device matches; see Phase 21 findings re: harness limits).
"""

from __future__ import annotations

import asyncio
import contextlib
from typing import Any

from cryptography import x509
from cryptography.hazmat.primitives import serialization
from cryptography.x509.oid import NameOID

from mash_py.commissioning import PASEClient
from mash_py.commissioning.cert_exchange import CertExchangeClient
from mash_py.commissioning.cert_store import (
    ZoneCA,
    ZoneType,
    generate_controller_cert,
    generate_zone_ca,
)
from mash_py.crypto.certs import derive_device_id, verify_cert_chain
from mash_py.discovery import Browser
from mash_py.testharness.handlers._common import (
    KEY_COMMISSIONING_RESULT,
    get_discriminator,
    get_setup_code,
    pick_target_host,
)
from mash_py.testharness.loader import Step, TestCase
from mash_py.testharness.runner import ExecutionState
from mash_py.transport.tls import (
    make_commissioning_client_context,
    verify_alpn,
    verify_tls13,
)
from mash_py.transport.connection import _ssl_object


_ZONE_TYPE_MAP = {
    "GRID": ZoneType.GRID,
    "LOCAL": ZoneType.LOCAL,
    "TEST": ZoneType.TEST,
}


async def handle_commission(step: Step, state: ExecutionState) -> dict[str, Any]:
    """Drive PASE + cert install against the device under test.

    Returns a flat outputs dict combining everything the YAML ``expect``
    blocks across the Phase-21 acceptance cases reference, e.g.
    ``session_established``, ``key_length``, ``commission_success``,
    ``has_operational_cert``, ``cert_signed_by_zone_ca``, etc.
    """
    params = dict(step.params)
    setup_code = get_setup_code(state, params)
    discriminator = get_discriminator(state, params)
    zone_type_name = str(params.get("zone_type") or "LOCAL").upper()
    if zone_type_name not in _ZONE_TYPE_MAP:
        raise ValueError(
            f"zone_type {zone_type_name!r} not in {sorted(_ZONE_TYPE_MAP)}"
        )
    zone_type = _ZONE_TYPE_MAP[zone_type_name]

    # 1) mDNS browse for the commissionable record.
    async with Browser() as browser:
        svc = await browser.find_commissionable_by_discriminator(
            discriminator, timeout=15.0
        )

    target_host = pick_target_host(svc.addresses, host=svc.host)

    # 2) TLS connect on the commissioning ALPN.
    ssl_ctx = make_commissioning_client_context()
    reader, writer = await asyncio.open_connection(
        host=target_host,
        port=svc.port,
        ssl=ssl_ctx,
        server_hostname=target_host,
    )

    operational_cert: x509.Certificate | None = None
    device_id: str | None = None
    zone_id: str | None = None
    k_main: bytes = b""

    # Use a deterministic per-zone-type CA name so multi-zone tests can
    # reuse a single Zone CA across calls if desired.
    zone_name = f"mash-py-test-{zone_type.name.lower()}"
    zone_ca: ZoneCA = state.custom.get(
        f"_zone_ca_{zone_type_name}"
    ) or generate_zone_ca(zone_name, zone_type)
    state.custom[f"_zone_ca_{zone_type_name}"] = zone_ca

    try:
        sslobj = _ssl_object(writer)
        if sslobj is None:
            raise RuntimeError("commissioning connection is not TLS")
        verify_tls13(sslobj)
        alpn = verify_alpn(sslobj)
        if alpn != "mash-comm/1":
            raise RuntimeError(f"unexpected ALPN: {alpn!r}")

        # 3) PASE handshake (DEC-084).
        pase = PASEClient(setup_code, discriminator)
        k_main = await asyncio.wait_for(pase.handshake(reader, writer), timeout=15.0)
        if len(k_main) != 32:
            raise RuntimeError(f"K_main must be 32 bytes per DEC-084, got {len(k_main)}")

        # 4) Cert install (DEC-085 envelope).
        cert_client = CertExchangeClient(zone_ca=zone_ca, zone_priority=1)
        result = await asyncio.wait_for(cert_client.run(reader, writer), timeout=30.0)
        operational_cert = result.operational_cert
        device_id = result.device_id
        zone_id = result.zone_id
    finally:
        writer.close()
        with contextlib.suppress(ConnectionError, OSError):
            await writer.wait_closed()

    if operational_cert is None or device_id is None:
        raise RuntimeError("cert exchange did not produce an operational cert")

    # 5) Verify the cert chain against the Zone CA we minted.
    cert_signed_by_zone_ca = verify_cert_chain(operational_cert, zone_ca.certificate)

    # Derive the device ID from the leaf pubkey to guard against the
    # device having installed a stale CSR keypair (matches the Phase-9
    # interop test's regression check).
    derived_id = derive_device_id(operational_cert.public_key())
    derived_matches = derived_id == device_id

    cn_attrs = operational_cert.subject.get_attributes_for_oid(NameOID.COMMON_NAME)
    cn = cn_attrs[0].value if cn_attrs else ""

    not_before = operational_cert.not_valid_before_utc
    not_after = operational_cert.not_valid_after_utc
    cert_validity_days = max(0, (not_after - not_before).days)

    # Generate a controller cert tied to the same Zone CA so subsequent
    # ``connect_operational`` / ``read`` steps can run mTLS without
    # re-commissioning.
    controller_cert = generate_controller_cert(
        zone_ca,
        controller_id="mash-py-test-controller",
    )

    commissioning_result: dict[str, Any] = {
        "device_id": device_id,
        "zone_id": zone_id,
        "zone_type": zone_type_name,
        "operational_cert": operational_cert,
        "operational_cert_der": operational_cert.public_bytes(serialization.Encoding.DER),
        "zone_ca": zone_ca,
        "controller_cert": controller_cert,
        "device_host": target_host,
        "k_main_len": len(k_main),
    }
    state.custom[KEY_COMMISSIONING_RESULT] = commissioning_result
    # Promote to runner-wide cache so subsequent cases that need a
    # commissioned device can skip re-running PASE (the device leaves
    # commissioning mode after success and a fresh attempt would hit
    # the device's busy-cooldown — DEC-063).
    if state.config is not None:
        state.config.state[KEY_COMMISSIONING_RESULT] = commissioning_result

    # Stash the connection-aware fields on outputs so subsequent steps
    # can address the device by mDNS-resolved host without re-browsing.
    state.set("_device_host", target_host)

    return {
        "session_established": True,
        "key_length": len(k_main),
        "key_not_zero": k_main != b"\x00" * 32,
        "commission_success": True,
        "has_operational_cert": True,
        "cert_signed_by_zone_ca": cert_signed_by_zone_ca,
        "cert_validity_days": cert_validity_days,
        "common_name_is_device_id": cn == device_id,
        "device_id_length": len(device_id),
        "device_id_hex_valid": _is_hex(device_id),
        "device_has_device_id": True,
        "device_id_matches_cert_cn": cn == device_id,
        "device_id_matches_derived": derived_matches,
        "zone_ca_stored": True,
        "operational_cert_stored": True,
        "device_id": device_id,
        "zone_id": zone_id,
        # Convenient single-key alias for ``compare`` steps.
        "common_name": cn,
    }


# ---------------------------------------------------------------------------
# Precondition setup
# ---------------------------------------------------------------------------


# Conditions whose entire fulfillment is "device must already be commissioned".
_COMMISSIONED_PRECONDITIONS = frozenset(
    {
        "device_commissioned",
        "operational_connection_established",
        "fresh_commission",
        "device_has_local_zone",
        "device_in_zone",
        "device_id_assigned",
    }
)

# Conditions the harness treats as no-ops because they describe the
# device's *external* state (handled by the device launcher in the
# fixture, not by the harness).
_NOOP_PRECONDITIONS = frozenset(
    {
        "device_in_commissioning_mode",
        "tls_connection_established",
        "device_advertising",
        "device_in_network",
        "device_uncommissioned",
        "zone_created",
        "device_address_valid",
        "connection_established",
    }
)


async def setup_preconditions(tc: TestCase, state: ExecutionState) -> None:
    """Translate the YAML preconditions into runner state."""
    needs_commission = False
    for cond in tc.preconditions:
        for key, want in cond.items():
            if key in _NOOP_PRECONDITIONS:
                continue
            if key in _COMMISSIONED_PRECONDITIONS and want:
                needs_commission = True
                continue
            # Anything else is silently allowed; if a step actually
            # needs the unmet state it will fail at the assertion.

    if needs_commission and KEY_COMMISSIONING_RESULT not in state.custom:
        # Reuse a cached commissioning result from a prior test in the
        # same suite — once the device is operational, re-running PASE
        # would fail the device's commissioning-window cooldown. The
        # mash-go runner does the same via its "suite zone" mechanism;
        # this is the spec-equivalent for Phase 21's narrower scope.
        cached = (
            state.config.state.get(KEY_COMMISSIONING_RESULT)
            if state.config is not None
            else None
        )
        if cached is not None:
            state.custom[KEY_COMMISSIONING_RESULT] = cached
            return

        synthetic = Step(action="commission", params={}, expect={})
        await handle_commission(synthetic, state)
        # Promote the freshly-minted result to the suite-wide cache.
        if state.config is not None:
            state.config.state[KEY_COMMISSIONING_RESULT] = state.custom[
                KEY_COMMISSIONING_RESULT
            ]


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _is_hex(value: str) -> bool:
    if not value:
        return False
    try:
        int(value, 16)
        return True
    except ValueError:
        return False


__all__ = ["handle_commission", "setup_preconditions"]
