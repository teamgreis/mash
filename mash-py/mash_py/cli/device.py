"""``mash-py-device`` — reference Python device for Phase 22 conformance.

Wires PASEServer + CertExchangeServer + DeviceService + Advertiser into one
two-port-deployment lifecycle (DEC-082) so that ``mash-go/cmd/mash-test``
running with ``--srv-resolve`` can drive a Python device through the same
conformance corpus the Go reference accepts.

Lifecycle:

1. **UNCOMMISSIONED** — only the commissioning listener is up; mDNS publishes
   ``_mash-comm._tcp`` with the discriminator. The first ``mash-comm/1``
   peer that arrives drives PASE + cert install. The peer's ``CertInstall``
   carries the Zone CA + operational cert + key the device persists.
2. **COMMISSIONED** — operational listener bound, mDNS publishes
   ``_mash._tcp`` (instance ``<zone-id>-<device-id>``). The commissioning
   listener is closed and its mDNS record withdrawn (single-zone
   simplification — see DEC-043; multi-zone Phase 20 is excluded from
   Phase 22).

State is persisted to ``state-dir`` so a restart short-circuits step 1 if
the device is already commissioned.

Run flags follow the Go ``cmd/mash-device`` surface where applicable. The
two-port shape adds ``--commissioning-port`` (the DEC-082 second listener);
``--port`` continues to mean "operational port" for parity with Go.
"""

from __future__ import annotations

import argparse
import asyncio
import contextlib
import json
import logging
import os
import secrets
import signal
import ssl
import sys
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path

from cryptography import x509
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.hazmat.primitives.asymmetric.ec import EllipticCurvePrivateKey

from mash_py.commissioning import (
    CertExchangeServer,
    CommissioningError,
    PASEServer,
    Verifier,
    encode_message,
    generate_verifier,
)
from mash_py.commissioning.messages import ERR_CODE_BUSY
from mash_py.commissioning.pase_server import (
    PASEAuthFailedError,
    PASEPhaseTimeoutError,
    PASEProtocolError,
)
from mash_py.wire.framing import AsyncFrameWriter, DEFAULT_MAX_MESSAGE_SIZE
from mash_py.crypto.certs import generate_commissioning_cert
from mash_py.discovery import (
    DEFAULT_OPERATIONAL_PORT,
    Advertiser,
    CommissionableAdvertisement,
    OperationalAdvertisement,
)
from mash_py.features.device_info_gen import (
    DEVICE_INFO_CMD_REMOVE_ZONE,
    REMOVE_ZONE_PARAM_ZONE_ID,
    REMOVE_ZONE_RESP_REMOVED,
    DeviceInfo,
)
from mash_py.features.test_control import (
    DEFAULT_ENABLE_KEY,
    TRIGGER_ENTER_COMMISSIONING_MODE,
    TRIGGER_EXIT_COMMISSIONING_MODE,
    TRIGGER_FACTORY_RESET,
    TRIGGER_RESET_TEST_STATE,
    TestControlFeature,
)
from mash_py.commissioning.cert_store import ZoneType
from mash_py.model import Device, Endpoint, EndpointType
from mash_py.service import (
    ConnectionCapExceededError,
    DeviceService,
    SharedConnectionCap,
    derive_connection_cap,
)
from mash_py.service.pase_tracker import PASEAttemptTracker
from mash_py.transport.tls import ZoneCertEntry, make_commissioning_context
from mash_py.zone import ZoneExistsError, ZonePool, ZoneTypeExistsError

_log = logging.getLogger("mash_py.cli.device")

# Default commissioning-port is implementation-defined per DEC-082; we pick
# a stable above-1024 default so casual interop runs don't collide with
# whatever the OS would auto-bind, and so Go's `mash-test --srv-resolve`
# always finds *something* even if the harness hasn't seen our mDNS yet.
DEFAULT_COMMISSIONING_PORT = 8444


# ---------------------------------------------------------------------------
# CLI args
# ---------------------------------------------------------------------------


@dataclass(slots=True)
class DeviceArgs:
    discriminator: int
    setup_code: str
    operational_port: int
    commissioning_port: int
    state_dir: Path | None
    reset: bool
    serial: str
    brand: str
    model: str
    name: str
    zone_name: str
    enable_key: str
    log_level: str


def _parse_args(argv: list[str] | None = None) -> DeviceArgs:
    p = argparse.ArgumentParser(
        prog="mash-py-device",
        description="Reference Python MASH device (DEC-082 two-port deployment).",
    )
    p.add_argument(
        "--discriminator",
        type=lambda s: int(s, 0),
        default=1234,
        help="Discriminator for commissioning (0-4095).",
    )
    p.add_argument(
        "--setup-code",
        default="20202021",
        help="8-digit numeric PASE setup code.",
    )
    p.add_argument(
        "--port",
        dest="operational_port",
        type=int,
        default=DEFAULT_OPERATIONAL_PORT,
        help="Operational listener port (default 8443). Use 0 for an OS-assigned port.",
    )
    p.add_argument(
        "--commissioning-port",
        type=int,
        default=DEFAULT_COMMISSIONING_PORT,
        help="Commissioning listener port (DEC-082; default 8444). Use 0 for an OS-assigned port.",
    )
    p.add_argument(
        "--state-dir",
        type=Path,
        default=None,
        help="Directory for persistent commissioning state (cert, key, verifier).",
    )
    p.add_argument(
        "--reset",
        action="store_true",
        help="Wipe persisted state and start fresh.",
    )
    p.add_argument("--serial", default="MASH-PY-001")
    p.add_argument("--brand", default="MASH")
    p.add_argument("--model", default="EVSE-PY")
    p.add_argument("--name", default="MASH Reference Python EVSE")
    p.add_argument(
        "--zone-name",
        default="phase22-test",
        help="Cosmetic zone name surfaced in TXT records and logs.",
    )
    p.add_argument(
        "--enable-key",
        default=DEFAULT_ENABLE_KEY,
        help=(
            "128-bit hex key (32 chars) for TestControl triggers. "
            "TestControl is always compiled in but testEventTriggersEnabled "
            "is only true when this is non-zero. Mirrors mash-go's default."
        ),
    )
    p.add_argument(
        "--log-level",
        default="info",
        choices=("debug", "info", "warn", "error"),
    )
    ns = p.parse_args(argv)
    if not (0 <= ns.discriminator <= 4095):
        p.error("discriminator must be in 0..4095")
    if not (ns.setup_code.isdigit() and len(ns.setup_code) == 8):
        p.error("setup-code must be 8 decimal digits")
    if len(ns.enable_key) != 32:
        p.error(
            f"enable-key must be 32 hex characters (128-bit), got {len(ns.enable_key)}"
        )
    try:
        int(ns.enable_key, 16)
    except ValueError:
        p.error("enable-key must be valid hex")
    return DeviceArgs(
        discriminator=ns.discriminator,
        setup_code=ns.setup_code,
        operational_port=ns.operational_port,
        commissioning_port=ns.commissioning_port,
        state_dir=ns.state_dir,
        reset=ns.reset,
        serial=ns.serial,
        brand=ns.brand,
        model=ns.model,
        name=ns.name,
        zone_name=ns.zone_name,
        enable_key=ns.enable_key,
        log_level=ns.log_level,
    )


# ---------------------------------------------------------------------------
# State persistence
# ---------------------------------------------------------------------------


@dataclass(slots=True)
class CommissionedState:
    """Artifacts that, taken together, let a restart skip commissioning."""

    zone_ca_cert: x509.Certificate
    operational_cert: x509.Certificate
    operational_key: EllipticCurvePrivateKey
    zone_type: int
    zone_priority: int
    device_id: str  # CommonName of operational_cert; 16 hex chars

    @property
    def zone_id(self) -> str:
        # DEC-088: zone_id is the first 16 hex chars (64 bits) of
        # SHA-256(Zone CA cert DER). NOT the public-key bytes -- the full
        # cert. mash-go's `pkg/discovery/fingerprint.go::ZoneIDFromCertificate`
        # is the reference implementation; lowercase hex matches its output
        # so harnesses that compare instance names byte-for-byte agree.
        from hashlib import sha256

        cert_der = self.zone_ca_cert.public_bytes(serialization.Encoding.DER)
        return sha256(cert_der).hexdigest()[:16]


_STATE_VERSION = 1


def _state_paths(state_dir: Path) -> dict[str, Path]:
    return {
        "meta": state_dir / "state.json",
        "comm_cert": state_dir / "commissioning-cert.pem",
        "comm_key": state_dir / "commissioning-key.pem",
        "zone_ca": state_dir / "zone-ca.pem",
        "op_cert": state_dir / "operational-cert.pem",
        "op_key": state_dir / "operational-key.pem",
    }


def _load_or_make_commissioning_cert(
    state_dir: Path | None, discriminator: int
) -> tuple[x509.Certificate, EllipticCurvePrivateKey]:
    """Either reload the device's stable commissioning cert or mint one.

    The commissioning cert is self-signed (DEC-067 §"stable cert"). Once minted
    it MUST be reused across restarts so peers that have cached its TLS
    fingerprint don't see a different cert on reconnect.
    """
    if state_dir is None:
        key = ec.generate_private_key(ec.SECP256R1())
        cert = generate_commissioning_cert(key, str(discriminator))
        return cert, key

    paths = _state_paths(state_dir)
    if paths["comm_cert"].is_file() and paths["comm_key"].is_file():
        cert = x509.load_pem_x509_certificate(paths["comm_cert"].read_bytes())
        key = serialization.load_pem_private_key(
            paths["comm_key"].read_bytes(), password=None
        )
        if not isinstance(key, EllipticCurvePrivateKey):
            raise RuntimeError(f"unexpected key type in {paths['comm_key']}")
        return cert, key

    state_dir.mkdir(parents=True, exist_ok=True)
    key = ec.generate_private_key(ec.SECP256R1())
    cert = generate_commissioning_cert(key, str(discriminator))
    paths["comm_cert"].write_bytes(cert.public_bytes(serialization.Encoding.PEM))
    paths["comm_key"].write_bytes(
        key.private_bytes(
            encoding=serialization.Encoding.PEM,
            format=serialization.PrivateFormat.PKCS8,
            encryption_algorithm=serialization.NoEncryption(),
        )
    )
    return cert, key


def _load_commissioned_state(state_dir: Path) -> CommissionedState | None:
    paths = _state_paths(state_dir)
    if not paths["meta"].is_file():
        return None
    if not (
        paths["zone_ca"].is_file()
        and paths["op_cert"].is_file()
        and paths["op_key"].is_file()
    ):
        return None
    meta = json.loads(paths["meta"].read_text())
    if meta.get("version") != _STATE_VERSION:
        _log.warning(
            "ignoring state with version=%r (expected %d)",
            meta.get("version"),
            _STATE_VERSION,
        )
        return None
    zone_ca = x509.load_pem_x509_certificate(paths["zone_ca"].read_bytes())
    op_cert = x509.load_pem_x509_certificate(paths["op_cert"].read_bytes())
    op_key = serialization.load_pem_private_key(
        paths["op_key"].read_bytes(), password=None
    )
    if not isinstance(op_key, EllipticCurvePrivateKey):
        raise RuntimeError(f"unexpected operational key type in {paths['op_key']}")
    return CommissionedState(
        zone_ca_cert=zone_ca,
        operational_cert=op_cert,
        operational_key=op_key,
        zone_type=int(meta["zone_type"]),
        zone_priority=int(meta["zone_priority"]),
        device_id=str(meta["device_id"]),
    )


def _persist_commissioned_state(state_dir: Path, state: CommissionedState) -> None:
    state_dir.mkdir(parents=True, exist_ok=True)
    paths = _state_paths(state_dir)
    paths["zone_ca"].write_bytes(
        state.zone_ca_cert.public_bytes(serialization.Encoding.PEM)
    )
    paths["op_cert"].write_bytes(
        state.operational_cert.public_bytes(serialization.Encoding.PEM)
    )
    paths["op_key"].write_bytes(
        state.operational_key.private_bytes(
            encoding=serialization.Encoding.PEM,
            format=serialization.PrivateFormat.PKCS8,
            encryption_algorithm=serialization.NoEncryption(),
        )
    )
    paths["meta"].write_text(
        json.dumps(
            {
                "version": _STATE_VERSION,
                "zone_type": state.zone_type,
                "zone_priority": state.zone_priority,
                "device_id": state.device_id,
            },
            indent=2,
        )
    )


def _wipe_state(state_dir: Path) -> None:
    if not state_dir.exists():
        return
    for path in _state_paths(state_dir).values():
        with contextlib.suppress(FileNotFoundError):
            path.unlink()


# ---------------------------------------------------------------------------
# Device model
# ---------------------------------------------------------------------------


def _device_id_from_cert(cert: x509.Certificate) -> str:
    """Derive the device id from the operational cert's public key.

    DEC-088: ``device_id`` = first 16 hex chars (64 bits) of
    SHA-256(operational cert public key DER). Lowercase hex matches mash-go's
    `pkg/discovery/fingerprint.go::DeviceIDFromPublicKey`. The CommonName of
    the cert is the same value by construction (cert-exchange controller
    sets it), so either works -- but hashing the public key is the
    spec-canonical derivation and survives certs whose CN was set
    incorrectly.
    """
    from hashlib import sha256

    pub_key_der = cert.public_key().public_bytes(
        encoding=serialization.Encoding.DER,
        format=serialization.PublicFormat.SubjectPublicKeyInfo,
    )
    return sha256(pub_key_der).hexdigest()[:16]


def _build_device(
    args: DeviceArgs,
    *,
    device_id: str,
    test_control: TestControlFeature | None = None,
    remove_zone_callback: Callable[[str], bool] | None = None,
) -> Device:
    """Build the Device tree.

    EVSE-shaped fixture: root endpoint + DeviceInfo + one EV_CHARGER endpoint.
    DeviceInfo carries the vendor/product/serial the conformance Read tests
    sample; the removeZone command handler is registered so Invoke tests
    succeed. When ``test_control`` is supplied, the feature is added to the
    root endpoint so the conformance harness's TestControl-driven preconditions
    (re-open commissioning window, reset state) work — mirroring
    ``mash-go/cmd/mash-device/main.go``'s pattern of always-compiled-in
    TestControl with enable-key gating.

    ``remove_zone_callback`` is invoked with the requested zone-id on each
    inbound RemoveZone command; it returns ``True`` if the zone was
    actually dropped (in-memory state + pool + mDNS), ``False`` otherwise.
    The lifecycle owns this callback so the wire response (the
    ``removed`` boolean) accurately reflects the device's post-call
    state — without it, multi-zone tests that fill capacity, send
    RemoveZone for cleanup, then re-commission a same-type zone hit
    ``ZoneTypeExistsError`` because the pool still holds the stale entry.
    """
    device = Device(device_id)

    di = DeviceInfo()
    di.set_device_id(device_id)
    di.set_vendor_name(args.brand)
    di.set_product_name(args.model)
    di.set_serial_number(args.serial)
    device.root.add_feature(di)

    def remove_zone(params: dict[int, object]) -> dict[int, object]:
        zone_id = params.get(REMOVE_ZONE_PARAM_ZONE_ID, "")
        if remove_zone_callback is None or not isinstance(zone_id, str):
            return {REMOVE_ZONE_RESP_REMOVED: bool(zone_id)}
        return {REMOVE_ZONE_RESP_REMOVED: remove_zone_callback(zone_id)}

    di.set_command_handler(DEVICE_INFO_CMD_REMOVE_ZONE, remove_zone)

    device.add_endpoint(Endpoint(1, EndpointType.EV_CHARGER, args.name))

    if test_control is not None:
        device.root.add_feature(test_control)

    # Refresh DeviceInfo.endpoints AFTER all features are attached so
    # auto-PICS discovery on the harness side sees the full feature
    # surface (including TestControl on the root endpoint, which gates
    # MASH.S.ZONE.MAX). Mirrors mash-go's late-bind pattern in
    # cmd/mash-device/main.go.
    di.set_endpoints(_endpoint_infos(device))
    return device


def _endpoint_infos(device: Device) -> list[dict[int, object]]:
    """Build the list-of-EndpointInfo payload DeviceInfo.endpoints carries.

    Wire shape (mash-go ``pkg/model/endpoint.go::EndpointInfo``):
        {1: id, 2: type, 3: label, 4: [feature_type_ids...]}

    Field 3 is omitempty on the Go side; we mirror that by skipping it
    when the label is empty so byte-for-byte CBOR equality with mash-go
    holds for the same fixture.
    """
    out: list[dict[int, object]] = []
    for ep in device.endpoints():
        info: dict[int, object] = {
            1: ep.id,
            2: int(ep.endpoint_type),
            4: [int(ft) for ft in ep.feature_types()],
        }
        if ep.label:
            info[3] = ep.label
        out.append(info)
    return out


# ---------------------------------------------------------------------------
# Verifier persistence
# ---------------------------------------------------------------------------


def _load_or_make_verifier(
    state_dir: Path | None, args: DeviceArgs
) -> Verifier:
    """Re-derive on every start.

    The verifier is deterministic in (setup-code, discriminator), so caching
    it costs more than re-deriving and risks staleness when the user passes
    a different ``--setup-code`` on a subsequent run. The 100K-iteration
    PBKDF2 cost is bounded (~tens of ms on a desktop) and only runs once
    per process startup.
    """
    return generate_verifier(args.setup_code, args.discriminator)


# ---------------------------------------------------------------------------
# Lifecycle orchestrator
# ---------------------------------------------------------------------------


class _DeviceLifecycle:
    """Single-zone lifecycle orchestrator.

    The class is event-loop affine: ``run`` is meant to be awaited from a
    single :func:`asyncio.run` call.
    """

    def __init__(self, args: DeviceArgs) -> None:
        self._args = args
        self._verifier = _load_or_make_verifier(args.state_dir, args)
        self._comm_cert, self._comm_key = _load_or_make_commissioning_cert(
            args.state_dir, args.discriminator
        )
        # ``_state`` flips from None (uncommissioned) to populated after
        # cert install completes. The operational task watches this flag
        # and brings up the operational listener once it transitions.
        # ``_state`` always refers to the *primary* zone — the first one
        # commissioned, which the harness uses as the suite control
        # channel. Additional zones live in ``_zones`` and are tracked
        # in ``_zone_pool`` (DEC-043 / DEC-060: max 1 per type, 3
        # total). DeviceService keeps serving the primary zone's TLS
        # context — multi-zone TLS (per-zone SNI cert selection) is
        # available in :func:`mash_py.transport.tls.make_multi_zone_operational_context`
        # but we don't swap it on the live listener here because the
        # conformance corpus doesn't drive operational connections to
        # secondary zones; the multi-zone wire shape is exercised at
        # the cert-install layer + mDNS layer, both of which work
        # without rebinding.
        self._state: CommissionedState | None = None
        self._zones: dict[str, CommissionedState] = {}
        self._zone_pool = ZonePool()
        if args.state_dir is not None and not args.reset:
            self._state = _load_commissioned_state(args.state_dir)
            if self._state is not None:
                self._zones[self._state.zone_id] = self._state
                _log.info(
                    "loaded commissioned state from %s (zone_type=%d, device_id=%s)",
                    args.state_dir,
                    self._state.zone_type,
                    self._state.device_id,
                )

        self._advertiser: Advertiser | None = None
        self._comm_server: asyncio.Server | None = None
        self._device_service: DeviceService | None = None
        # Single-fire signal: the commissioning handler sets it after a
        # successful cert install so the main loop can transition.
        self._commission_done = asyncio.Event()
        # Counterpart event: the main loop sets this once the operational
        # listener is bound so the commissioning handler can safely close
        # its connection. Without this barrier, controllers immediately
        # reconnect-operational after their commissioning conn closes and
        # race the device's listener-bind step (DEC-082 two-port: op
        # listener MUST be reachable before commissioning conn drops).
        self._op_listener_ready = asyncio.Event()
        # If commissioning errored, the handler stashes the exception
        # here for the main loop to log. We don't tear down the listener
        # on failure -- the next ``mash-comm/1`` peer gets a fresh
        # PASEServer + CertExchangeServer (PASE is single-use, but the
        # listener accepts more connections).
        self._last_commission_error: BaseException | None = None
        # Set by run() so the uncommissioned wait can race against shutdown.
        self._stop_event: asyncio.Event | None = None

        # Re-entry support (TestControl-driven). After the initial
        # commissioning succeeds and we're operational, the harness can
        # re-open the commissioning window via TRIGGER_ENTER_COMMISSIONING_MODE.
        # We serialize all listener-mutation paths (initial bind, re-entry,
        # exit, teardown) through this lock so concurrent triggers + the
        # SIGTERM teardown can't race a half-bound asyncio.Server.
        self._comm_lock = asyncio.Lock()
        # Generation counter for the commissioning window. Mirrors
        # mash-go's ``DeviceService.commissioningEpoch`` (see
        # ``device_service_commissioning.go::exitCommissioningForEpoch``).
        # Every call into :meth:`_enter_commissioning_mode` bumps this
        # under the lock, including the no-op "already up" path — the
        # counter expresses intent ("a peer asked us to keep the window
        # open"), not just physical bind events. Callers that issue an
        # exit *after* a slow operation (e.g. ``_enter_uncommissioned``
        # waits on mDNS publish before its trailing close) capture the
        # epoch beforehand and call :meth:`_exit_commissioning_for_epoch`,
        # which skips the close if a re-entry has overtaken them. Without
        # this, the post-initial-commission close races trigger-driven
        # re-entry from the operational connection and tears down the
        # listener the harness expects to be open — every subsequent
        # ``device_in_commissioning_mode`` precondition then dials a
        # closed comm port and times out at PASE-read.
        self._comm_epoch: int = 0
        # Latest setCommissioningWindowDuration request from the harness.
        # Persisted across enter/exit cycles so the next enter can honor
        # it (matches mash-go's CommissioningWindowDuration). Currently
        # informational — the listener stays open until exit_commissioning
        # is called or a fresh peer commissions us — but we surface the
        # value in logs so operators can see what the harness asked for.
        self._comm_window_duration_s: int | None = None

        # DEC-048 / commissioning-window-timing.md: self-closing window
        # timer. ``setCommissioningWindowDuration`` stores a duration on
        # this lifecycle; ``_enter_commissioning_mode`` (re-)arms a
        # one-shot timer that retracts the comm listener + mDNS record
        # when it fires. Resets on successful commissioning, explicit
        # exit, and lifecycle teardown.
        self._comm_window_timer: asyncio.TimerHandle | None = None
        # Epoch at which the active timer was armed. The timer callback
        # checks this against ``_comm_epoch`` before acting so a window
        # that's been re-armed by a fresh enter/exit cycle doesn't get
        # closed by a stale timer.
        self._comm_window_timer_epoch: int = -1

        # DEC-047 / commissioning-pase.md §5.5: per-device PASE attempt
        # tracker. The handler applies the tier delay before responding
        # to PASERequest, records failures on auth/protocol error paths,
        # and resets on successful commissioning + window close.
        self._pase_tracker = PASEAttemptTracker()
        # DEC-063 / TC-SEC-CONN-001: only one in-flight PASE handshake
        # per device. Newcomers receive ``CommissioningError{Busy}``
        # with a non-zero ``retry_after_ms`` hint and the connection is
        # closed. Counter (not Lock) because the rejection path also
        # needs to send a frame — a Lock would block the second handler
        # entirely; we need it to read the request, decline, and close.
        self._pase_in_flight: int = 0
        # Spec doesn't pin a retry-after value; mash-go derives one
        # dynamically. Phase 22 keeps it simple: a fixed 5 s hint, which
        # is the order-of-magnitude mash-go returns for an active PASE
        # rejection (TestComputeBusyRetryAfter_Branches: 5000 ms).
        self._busy_retry_after_ms: int = 5000

        # DEC-062 / DEC-064: pre-handshake connection cap on the comm
        # port. Capacity follows DEC-064 (``max_zones + 1``) where
        # ``max_zones`` is **2** per DEC-043 (1 GRID + 1 LOCAL): the TEST
        # zone (DEC-060) is an *extra observer slot* that does not count
        # against the cap, so production-conformant capacity is 3 even
        # though :class:`ZonePool`'s :data:`MAX_ZONES` is 3 (which
        # includes TEST for type-uniqueness bookkeeping).
        # Applied only to the commissioning listener for now — the
        # operational listener uses ``TwoPortListener``'s post-handshake
        # cap. The flood test (TC-SEC-CONN-005) only exercises the comm
        # ALPN, and pre-handshake rejection there is what flips the
        # harness's peak-concurrent count from O(rate*duration) to
        # ``max_zones + 1``.
        self._comm_conn_cap = SharedConnectionCap(
            capacity=derive_connection_cap(2)
        )

    # ------------------------------------------------------------------
    # Run
    # ------------------------------------------------------------------

    async def run(self, stop_event: asyncio.Event) -> None:
        self._advertiser = Advertiser()
        await self._advertiser.start()
        self._stop_event = stop_event
        try:
            if self._state is None:
                await self._enter_uncommissioned()
            else:
                await self._enter_commissioned()
            await stop_event.wait()
        finally:
            await self._teardown()

    # ------------------------------------------------------------------
    # Uncommissioned phase
    # ------------------------------------------------------------------

    async def _enter_uncommissioned(self) -> None:
        async with self._comm_lock:
            self._comm_server = await self._start_comm_listener()
            self._comm_epoch += 1
            actual_port = _resolve_first_port(self._comm_server)
            _log.info("commissioning listener up on :%d", actual_port)

            assert self._advertiser is not None
            await self._advertiser.publish_commissionable(
                CommissionableAdvertisement(
                    discriminator=self._args.discriminator,
                    serial=self._args.serial,
                    brand=self._args.brand,
                    model=self._args.model,
                    port=actual_port,
                    device_name=self._args.name,
                )
            )
            _log.info(
                "advertised _mash-comm._tcp (discriminator=%d, port=%d)",
                self._args.discriminator,
                actual_port,
            )

        # Wait for first successful commissioning OR a shutdown signal.
        # Without racing the stop_event here, SIGTERM during the
        # uncommissioned phase would block forever in commission_done.wait()
        # and the harness escalating to SIGKILL would skip the advertiser
        # close path -- which is what fires the mDNS goodbye packets.
        # Stale MASH-NNNN records on the LAN trace back to this exact race.
        assert self._stop_event is not None, "run() must populate _stop_event"
        commission_task = asyncio.create_task(self._commission_done.wait())
        stop_task = asyncio.create_task(self._stop_event.wait())
        try:
            await asyncio.wait(
                {commission_task, stop_task},
                return_when=asyncio.FIRST_COMPLETED,
            )
        finally:
            for t in (commission_task, stop_task):
                if not t.done():
                    t.cancel()
                    with contextlib.suppress(asyncio.CancelledError):
                        await t
        if self._stop_event.is_set() and not self._commission_done.is_set():
            # Stopped before any peer commissioned us; let the run() finally
            # block fire teardown so the advertiser sends goodbye for the
            # commissionable record.
            return
        if self._state is None:
            # Defensive: the handler should have set ``self._state``
            # before ``_commission_done`` fires.
            raise RuntimeError("commission_done set without state")
        _log.info(
            "commissioning complete: zone_type=%d zone_priority=%d device_id=%s",
            self._state.zone_type,
            self._state.zone_priority,
            self._state.device_id,
        )

        # Persist state (best-effort; state_dir may be None in ad-hoc runs).
        if self._args.state_dir is not None:
            _persist_commissioned_state(self._args.state_dir, self._state)

        # CRITICAL ordering for DEC-082: bring up the operational listener
        # FIRST, then signal the commissioning handler that it can close
        # its socket. Without this barrier the controller's
        # operational-reconnect lands during the listener-bind window and
        # gets ECONNREFUSED. Single-port devices (mash-go) don't see this
        # because the same socket continues to accept after the
        # commissioning ALPN handshake completes; two-port devices need
        # the explicit barrier.
        #
        # Capture the comm epoch BEFORE the slow ``_enter_commissioned``
        # step. The harness's auto-PICS phase opens an operational TLS
        # session as soon as the listener is bound (well before our mDNS
        # publish completes) and immediately dispatches
        # TriggerEnterCommissioningMode (and, at test boundaries,
        # TriggerResetTestState). Those triggers race with our trailing
        # close below; the epoch lets the trailing close detect that a
        # peer has re-armed the window and skip itself, mirroring
        # mash-go's ``exitCommissioningForEpoch``. Without it, the close
        # always tears down the just-reopened listener and every
        # subsequent ``device_in_commissioning_mode`` precondition fails
        # at PASE-read.
        captured_epoch = self._comm_epoch
        await self._enter_commissioned(transition_from_commissionable=True)
        self._op_listener_ready.set()
        # The commissioning handler will close its writer on its own as
        # soon as it observes ``_op_listener_ready`` set.
        await self._exit_commissioning_for_epoch(captured_epoch)

    async def _handle_commissioning_client(
        self,
        reader: asyncio.StreamReader,
        writer: asyncio.StreamWriter,
    ) -> None:
        """Accept handler for the commissioning listener.

        Used for both initial commissioning (``self._state is None``) and
        TestControl-driven re-entry (``self._state is not None``). The
        first path goes through the DEC-082 op_listener_ready barrier;
        the re-entry path replaces zone state in place — the operational
        listener is already up, so no barrier is required.
        """
        peer = writer.get_extra_info("peername")
        is_initial = self._state is None
        _log.info(
            "commissioning peer connected: %s (initial=%s)", peer, is_initial
        )
        # Tracks whether this handler has reserved the in-flight PASE
        # slot, so the outer ``finally`` releases it exactly once even
        # on the busy-rejection / read-error early-return paths.
        cap_held = False
        try:
            pase_server = PASEServer(self._verifier)
            # DEC-061 / DEC-047 ordering: read the first frame *before*
            # touching shared state (backoff tracker, in-flight cap,
            # SPAKE2+ math). A peer that just opens TLS and never
            # speaks drops on the framing read without blocking
            # legitimate concurrent attempts.
            try:
                request = await pase_server.read_request(reader)
            except (PASEProtocolError, asyncio.IncompleteReadError):
                _log.debug(
                    "commissioning peer %s closed before sending PASERequest",
                    peer,
                )
                return

            # DEC-063 / TC-SEC-CONN-001: at most one in-flight PASE per
            # device. If another handler is already past the cap check,
            # send ``CommissioningError{Busy, RetryAfter}`` and close —
            # the peer surfaces this as a ``PASEBusyError``. The cap
            # is held across PASE + cert install, released in the outer
            # ``finally``.
            if self._pase_in_flight > 0:
                _log.info(
                    "commissioning peer %s rejected (busy, in-flight=%d)",
                    peer,
                    self._pase_in_flight,
                )
                await _send_commissioning_busy(
                    writer,
                    retry_after_ms=self._busy_retry_after_ms,
                    reason="commissioning already in progress",
                )
                # DEC-047: a busy rejection where another PASE is
                # actually in progress does NOT count as a failed
                # attempt — the second peer never got to verify
                # anything, so the global tier counter would be
                # double-counting.
                return

            self._pase_in_flight += 1
            cap_held = True
            # DEC-047 backoff: apply the per-tier wall-clock delay
            # *after* we have a peer-supplied PASERequest in hand but
            # *before* spending CPU on SPAKE2+ math. The harness measures
            # time from "PASERequest sent" to the failure response;
            # injecting the sleep here surfaces the tier delay on the
            # wire and matches mash-go (which applies the same delay
            # before ``acceptCommissioningConnection``).
            delay = await self._pase_tracker.get_delay()
            if delay > 0:
                _log.debug(
                    "applying PASE backoff delay: %.3fs (failed_attempts=%d)",
                    delay,
                    await self._pase_tracker.attempt_count(),
                )
                await asyncio.sleep(delay)

            try:
                k_main = await pase_server.handshake(
                    reader, writer, request=request
                )
            except (
                PASEAuthFailedError,
                PASEProtocolError,
                PASEPhaseTimeoutError,
            ):
                # DEC-047: every PASE-failure path increments the backoff
                # tier. The PASE server has already emitted the
                # appropriate envelope (``PASEComplete{auth_failed}`` for
                # auth failures; nothing for phase timeout) — record
                # and re-raise so the outer ``finally`` closes the
                # socket.
                await self._pase_tracker.record_failure()
                raise
            _log.debug("PASE complete: K_main=%s...", k_main[:8].hex())
            # DEC-047: reset the backoff tier on successful PASE. Cert
            # install can still fail downstream, but the peer demonstrated
            # knowledge of the setup code so the rate limit no longer
            # applies.
            await self._pase_tracker.reset()
            # DEC-063: the in-flight cap is per-PASE-handshake, not
            # per-commissioning-attempt. A peer that has finished the
            # SPAKE2+ exchange has consumed its slot; let the next
            # peer through *before* we wait through cert install + the
            # DEC-082 op-listener-ready barrier (~1.5 s for mDNS
            # publish on the initial-commission path). Without this,
            # the auto-PICS commission keeps the cap held for the
            # whole mDNS publish window, and the test harness's first
            # ``pase_attempts`` connection lands during that window
            # and gets a spurious busy rejection — which propagates as
            # a missed-failure on the backoff counter.
            if cap_held:
                self._pase_in_flight -= 1
                cap_held = False

            cert_server = CertExchangeServer()
            result = await cert_server.run(reader, writer)
            _log.info(
                "cert install complete: zone_type=%d zone_priority=%d",
                result.zone_type,
                result.zone_priority,
            )

            device_id = _device_id_from_cert(result.operational_cert)
            new_state = CommissionedState(
                zone_ca_cert=result.zone_ca_cert,
                operational_cert=result.operational_cert,
                operational_key=result.operational_key,
                zone_type=result.zone_type,
                zone_priority=result.zone_priority,
                device_id=device_id,
            )

            if is_initial:
                self._state = new_state
                self._zones[new_state.zone_id] = new_state
                # Register the primary zone in the pool so re-entry
                # commissions can be type-checked + capacity-checked
                # against it (DEC-043 / DEC-060). Suppress error here;
                # an empty pool has no conflicts on first add, but we
                # don't want any pool exception to derail the initial
                # commission.
                with contextlib.suppress(Exception):
                    await self._zone_pool.add_zone(
                        new_state.zone_id, ZoneType(new_state.zone_type)
                    )
                self._commission_done.set()
                # DEC-082 ordering: park the connection until the main
                # loop has the operational listener up. Closing earlier
                # lets the controller race the device's listener bind
                # and observe a transient connection-refused.
                try:
                    await asyncio.wait_for(
                        self._op_listener_ready.wait(), timeout=10.0
                    )
                except TimeoutError:
                    _log.warning(
                        "commissioning handler timed out waiting for "
                        "operational listener bind; closing anyway"
                    )
            else:
                # Re-entry path: a TestControl trigger reopened the
                # window and a new peer completed PASE+CertInstall.
                # Track the new zone alongside the primary (suite)
                # zone — DEC-043 / DEC-060 max 1 zone per type, 3
                # total, enforced by ZonePool. mash-go's multi-zone
                # implementation registers each zone in its
                # ``zones`` map; we mirror that by keying every zone
                # on its ``zone_id`` (SHA-256(Zone CA DER)[:16]) and
                # leaving ``self._state`` pinned to the primary so
                # the harness's suite control channel — the
                # operational TLS conn for the first-commissioned
                # (TEST) zone — keeps working.
                #
                # We deliberately do NOT close the comm listener
                # here. Multi-zone tests like TC-COMM-004 commission
                # multiple zones back-to-back without an intervening
                # ``enter_commissioning_mode`` step — the second
                # ``commission`` action immediately dials the comm
                # port and gets ECONNREFUSED if the first commission
                # auto-closed it. The harness drives explicit
                # close via ``TRIGGER_EXIT_COMMISSIONING_MODE`` or
                # cycles via ``TRIGGER_RESET_TEST_STATE`` between
                # tests; respect that. Tests that explicitly check
                # "window closed after commission" (TC-COMM-WINDOW-005)
                # always run from a factory-reset state, which goes
                # through the ``is_initial=True`` path that DOES
                # close via ``_exit_commissioning_for_epoch``.
                new_zone_id = new_state.zone_id
                try:
                    await self._zone_pool.add_zone(
                        new_zone_id, ZoneType(new_state.zone_type)
                    )
                except (ZoneExistsError, ZoneTypeExistsError) as exc:
                    _log.warning(
                        "re-entry commission rejected by zone pool: %r",
                        exc,
                    )
                else:
                    self._zones[new_zone_id] = new_state
                    _log.info(
                        "re-entry commission accepted: zone_type=%d "
                        "zone_id=%s device_id=%s (zones=%d)",
                        new_state.zone_type,
                        new_zone_id,
                        new_state.device_id,
                        len(self._zones),
                    )
                    # Multi-zone TLS swap (Phase 20): rebuild the
                    # operational listener's TLS context so controllers
                    # in the new zone can mTLS-authenticate. Without
                    # this, only suite-zone controllers pass mTLS and
                    # tests like TC-COMM-004 that
                    # ``extract_cert_device_id`` for non-suite zones
                    # find no connection. Best-effort: a rebind failure
                    # doesn't tank the commissioning exchange (it
                    # already succeeded on the wire); the new zone's
                    # operational reachability is the only thing
                    # affected.
                    if self._device_service is not None:
                        entries = self._build_zone_cert_entries()
                        with contextlib.suppress(Exception):
                            await self._device_service.rebind_with_zone_entries(entries)
                    # Per-zone operational mDNS instance per DEC-088 /
                    # Phase 18 (the advertiser supports holding multiple
                    # _mash._tcp instances simultaneously). Best-effort:
                    # advertise failure shouldn't fail the commissioning
                    # exchange, which already completed on the wire.
                    if self._advertiser is not None:
                        with contextlib.suppress(Exception):
                            await self._advertiser.publish_operational(
                                OperationalAdvertisement(
                                    zone_id=new_zone_id,
                                    device_id=new_state.device_id,
                                    port=self._args.operational_port
                                    if self._device_service is None
                                    else self._device_service.port,
                                    vendor_product=(
                                        f"{self._args.brand}:{self._args.model}"
                                    ),
                                    firmware="mash-py-1.0",
                                    endpoint_count=1,
                                )
                            )
        except BaseException as exc:
            self._last_commission_error = exc
            _log.warning("commissioning attempt failed: %r", exc)
        finally:
            if cap_held:
                self._pase_in_flight -= 1
            writer.close()
            with contextlib.suppress(ConnectionError, OSError):
                await writer.wait_closed()

    async def _start_comm_listener(self) -> asyncio.Server:
        """Bind a raw TCP listener, then upgrade to TLS per accepted conn.

        DEC-062 wants the connection cap enforced *before* TLS handshake
        completes so a flooding peer can't burn slot budget by completing
        thousands of TLS handshakes the device would have to immediately
        close. ``asyncio.start_server(ssl=...)`` hides the handshake
        inside its accept path — by the time our handler is called, TLS
        is done and the harness's ``tls.Dial`` has already returned
        success. ``loop.start_tls`` lets us interleave: accept raw TCP,
        check the cap, refuse pre-handshake on overflow, otherwise
        upgrade to TLS and run the existing handler.

        The cap slot is held for the duration of the connection
        (handshake + PASE + cert install). Slot release happens in the
        outer try/finally inside ``_handle_commissioning_client``.
        """
        ssl_ctx = make_commissioning_context(self._comm_cert, self._comm_key)

        async def _on_raw_accept(
            reader: asyncio.StreamReader,
            writer: asyncio.StreamWriter,
        ) -> None:
            peer = writer.get_extra_info("peername")
            try:
                slot = self._comm_conn_cap.acquire()
            except ConnectionCapExceededError:
                # Pre-handshake reject. Closing the raw TCP socket here
                # surfaces on the peer as a TLS-handshake failure (peer
                # was about to send ClientHello), which the flood
                # harness counts as ``rejected`` rather than
                # ``accepted``. The whole point of pre-handshake cap is
                # to make that count match DEC-064 (max_zones + 1).
                _log.debug(
                    "rejecting comm conn from %s: cap exhausted (active=%d/%d)",
                    peer,
                    self._comm_conn_cap.active,
                    self._comm_conn_cap.capacity,
                )
                writer.close()
                with contextlib.suppress(Exception):
                    await writer.wait_closed()
                return
            try:
                # Upgrade the raw TCP transport to TLS in place. The
                # StreamReaderProtocol attached to the existing transport
                # keeps feeding the StreamReader, so ``reader`` continues
                # to work after the upgrade. ``writer._transport`` must
                # be repointed at the new TLS transport — otherwise
                # writes would go to the now-orphaned raw transport.
                loop = asyncio.get_running_loop()
                raw_transport = writer.transport
                protocol = raw_transport.get_protocol()
                tls_transport = await loop.start_tls(
                    raw_transport,
                    protocol,
                    ssl_ctx,
                    server_side=True,
                    ssl_handshake_timeout=30.0,
                )
                # Repoint the writer at the new TLS transport so frames
                # written via ``writer.write`` actually reach the peer
                # encrypted. Documented asyncio quirk:
                # https://docs.python.org/3/library/asyncio-stream.html
                # # asyncio.StreamWriter.start_tls
                writer._transport = tls_transport  # noqa: SLF001
                await self._handle_commissioning_client(reader, writer)
            except (ssl.SSLError, ConnectionError, OSError, asyncio.TimeoutError) as exc:
                _log.debug(
                    "comm conn from %s aborted during TLS upgrade: %r",
                    peer,
                    exc,
                )
                writer.close()
                with contextlib.suppress(Exception):
                    await writer.wait_closed()
            finally:
                slot.release()

        return await asyncio.start_server(
            _on_raw_accept,
            host="0.0.0.0",
            port=self._args.commissioning_port,
            # No ssl=... here on purpose: we upgrade per-connection
            # inside ``_on_raw_accept`` after the cap check.
            start_serving=True,
        )

    async def _stop_commissioning_listener(self) -> None:
        if self._comm_server is None:
            return
        srv = self._comm_server
        self._comm_server = None
        srv.close()
        # Bounded drain. ``asyncio.Server.wait_closed()`` waits for every
        # active connection to fully close, including TLS shutdown
        # (close_notify round-trip). When the auto-close after a
        # re-entry commission is scheduled from the connection's own
        # handler — typical mid-suite flow — the writer is still in
        # ``await writer.wait_closed()``, and ``srv.wait_closed()`` waits
        # for the handler to return, while the handler waits for the
        # peer's TLS shutdown. A slow peer pins this for seconds, and
        # because we hold ``_comm_lock`` here, the harness's next
        # ``TriggerResetTestState`` (sent over the operational channel
        # with a 5 s deadline) blocks behind us and times out — leaving
        # the comm port closed for the rest of the run. ``srv.close()``
        # alone is enough to stop accepting new connections; in-flight
        # ones drain on their own. We bound the wait at 0.5 s so it can
        # complete on the happy path but never starve a concurrent
        # trigger.
        with contextlib.suppress(Exception):
            await asyncio.wait_for(srv.wait_closed(), timeout=0.5)
        if self._advertiser is not None:
            with contextlib.suppress(Exception):
                await self._advertiser.unpublish_commissionable()

    # ------------------------------------------------------------------
    # TestControl-driven re-entry
    # ------------------------------------------------------------------

    async def _enter_commissioning_mode(self) -> None:
        """Re-open the commissioning ALPN listener + advertise it.

        Idempotent: a second call while the listener is already up is a
        no-op. Mirrors mash-go's ``DeviceService.EnterCommissioningMode``
        in the two-port deployment shape (DEC-082).

        The comm-epoch is bumped on every call, including the no-op path
        — see :attr:`_comm_epoch` for the rationale.
        """
        async with self._comm_lock:
            self._comm_epoch += 1
            armed_epoch = self._comm_epoch
            if self._comm_server is not None:
                _log.debug(
                    "enter_commissioning_mode: listener already up "
                    "(epoch=%d)",
                    self._comm_epoch,
                )
                # Even on the idempotent path, (re-)arm the
                # window-expiry timer below — the harness's
                # ``setCommissioningWindowDuration`` + ``enter`` sequence
                # expects "open window for N seconds from now", which
                # mash-go honours regardless of whether the listener
                # was already bound.
            else:
                self._comm_server = await self._start_comm_listener()
                actual_port = _resolve_first_port(self._comm_server)
                _log.info(
                    "re-opened commissioning listener on :%d (window=%ss epoch=%d)",
                    actual_port,
                    self._comm_window_duration_s
                    if self._comm_window_duration_s is not None
                    else "default",
                    self._comm_epoch,
                )
                assert self._advertiser is not None
                await self._advertiser.publish_commissionable(
                    CommissionableAdvertisement(
                        discriminator=self._args.discriminator,
                        serial=self._args.serial,
                        brand=self._args.brand,
                        model=self._args.model,
                        port=actual_port,
                        device_name=self._args.name,
                    )
                )
                _log.info(
                    "re-advertised _mash-comm._tcp (discriminator=%d, port=%d)",
                    self._args.discriminator,
                    actual_port,
                )

        # Arm the window-expiry timer outside the comm lock so the
        # callback (which acquires the same lock) cannot deadlock with
        # an enter/exit sequence racing it.
        self._arm_window_timer(armed_epoch)

    def _arm_window_timer(self, armed_epoch: int) -> None:
        """Schedule a one-shot timer that retracts the commissioning
        window after ``_comm_window_duration_s`` seconds.

        Cancels any previously-armed timer first — a fresh
        ``enter_commissioning_mode`` (or factory-reset cycle) restarts
        the window's clock per DEC-048. The callback short-circuits if
        the comm-epoch has moved past ``armed_epoch`` (a re-entry has
        overtaken us); without that guard, an expiry from a stale
        window would close the listener the harness expects to be open.
        """
        self._cancel_window_timer()
        if self._comm_window_duration_s is None:
            return
        loop = asyncio.get_running_loop()
        self._comm_window_timer_epoch = armed_epoch
        self._comm_window_timer = loop.call_later(
            self._comm_window_duration_s,
            lambda: asyncio.ensure_future(
                self._on_window_expired(armed_epoch)
            ),
        )
        _log.debug(
            "armed window-expiry timer: %ds (epoch=%d)",
            self._comm_window_duration_s,
            armed_epoch,
        )

    def _cancel_window_timer(self) -> None:
        """Cancel any pending window-expiry timer. Safe to call when no
        timer is armed."""
        if self._comm_window_timer is not None:
            self._comm_window_timer.cancel()
            self._comm_window_timer = None
            self._comm_window_timer_epoch = -1

    async def _on_window_expired(self, armed_epoch: int) -> None:
        """Window-expiry callback. Closes the listener + retracts mDNS
        when no fresh enter has overtaken ``armed_epoch``.

        Mirrors mash-go's ``windowExpiryCloser`` — the spec (DEC-048 +
        commissioning-window-timing.md §5) requires the window to
        self-close on duration expiry, indistinguishable on the wire
        from an explicit ``exit_commissioning_mode``.
        """
        if self._comm_epoch != armed_epoch:
            _log.debug(
                "window-expiry skipped: epoch advanced %d → %d",
                armed_epoch,
                self._comm_epoch,
            )
            return
        _log.info(
            "commissioning window expired after %ds; closing listener",
            self._comm_window_duration_s,
        )
        await self._exit_commissioning_mode()

    async def _exit_commissioning_mode(self) -> None:
        """Close the commissioning listener + retract the mDNS record.

        Idempotent. The operational service is left untouched —
        controllers in the existing zone keep their connections.

        DEC-047 / commissioning-pase.md §5.5: window-close resets the
        backoff counter — the operator regained physical control by
        ending the commissioning window, so the next time it reopens
        the rate-limit budget starts fresh. Also cancels the
        DEC-048 window-expiry timer so a self-close from a fresh
        enter doesn't fire after this manual exit.
        """
        self._cancel_window_timer()
        async with self._comm_lock:
            await self._stop_commissioning_listener()
        await self._pase_tracker.reset()

    def _remove_zone_sync(self, zone_id: str) -> bool:
        """Sync entry point for the DeviceInfo.removeZone command handler.

        The wire-protocol command response must be synchronous — the
        Invoke handler returns a dict that's encoded directly. We
        validate the zone exists synchronously (the truthy "removed"
        boolean is what controllers care about), then schedule the
        actual teardown (mDNS goodbye, pool drop, multi-zone TLS
        rebind) on the event loop. Without scheduling async, the
        command would block on slow operations like mDNS unpublish.

        Refusing to drop the suite (primary) zone matches mash-go's
        behaviour: a controller cannot RemoveZone the zone whose
        connection it's using (it would saw off the branch it's
        sitting on); the harness's per-test cleanup specifically
        targets non-suite zones.
        """
        if zone_id not in self._zones:
            return False
        if self._state is not None and zone_id == self._state.zone_id:
            _log.warning(
                "remove_zone refused: %s is the primary (suite) zone",
                zone_id,
            )
            return False
        # Schedule the async teardown. The command response races the
        # task slightly — the wire ack returns ``removed=true`` while
        # the mDNS goodbye + pool drop run in parallel. That's fine:
        # the harness's next test cycle waits for mDNS state to
        # stabilise before proceeding.
        asyncio.create_task(self._remove_zone_async(zone_id))
        return True

    async def _remove_zone_async(self, zone_id: str) -> None:
        """Asynchronous companion to :meth:`_remove_zone_sync`.

        Drops the zone from in-memory state, withdraws its operational
        mDNS record, removes it from :class:`ZonePool`, and rebinds
        the operational listener with the remaining zone-cert entries
        so subsequent re-commissions of the same zone-type can
        succeed."""
        zstate = self._zones.pop(zone_id, None)
        if zstate is None:
            return
        with contextlib.suppress(Exception):
            await self._zone_pool.remove_zone(zone_id)
        if self._advertiser is not None:
            with contextlib.suppress(Exception):
                await self._advertiser.unpublish_operational(
                    zone_id, zstate.device_id
                )
        if self._device_service is not None and self._zones:
            entries = self._build_zone_cert_entries()
            with contextlib.suppress(Exception):
                await self._device_service.rebind_with_zone_entries(entries)
        _log.info(
            "removed zone %s (zone_type=%d, %d zone(s) remain)",
            zone_id,
            zstate.zone_type,
            len(self._zones),
        )

    def _build_zone_cert_entries(self) -> list[ZoneCertEntry]:
        """Snapshot ``self._zones`` as a list of ``ZoneCertEntry``
        ready for ``make_multi_zone_operational_context`` /
        ``DeviceService.rebind_with_zone_entries``.

        Order matters for the multi-zone context: the first entry is
        the SNI fallback. Pin the primary (suite) zone first so a
        controller that connects without SNI hits the suite cert —
        same fallback behaviour as mash-go.
        """
        entries: list[ZoneCertEntry] = []
        if self._state is not None and self._state.zone_id in self._zones:
            entries.append(
                ZoneCertEntry(
                    device_id=self._state.device_id,
                    operational_cert=self._state.operational_cert,
                    operational_key=self._state.operational_key,
                    zone_ca=self._state.zone_ca_cert,
                )
            )
        primary_zone_id = self._state.zone_id if self._state else None
        for zid, zstate in self._zones.items():
            if zid == primary_zone_id:
                continue
            entries.append(
                ZoneCertEntry(
                    device_id=zstate.device_id,
                    operational_cert=zstate.operational_cert,
                    operational_key=zstate.operational_key,
                    zone_ca=zstate.zone_ca_cert,
                )
            )
        return entries

    async def _factory_reset(self) -> None:
        """Wipe every commissioned zone, drop the operational service,
        and re-enter the uncommissioned phase.

        Mirrors mash-go's ``TriggerFactoryReset`` semantics: the device
        sheds its zone memberships, withdraws operational mDNS, and
        re-advertises ``_mash-comm._tcp`` as if it were freshly
        provisioned. The persisted state directory is wiped so a
        process restart sees an uncommissioned device.

        Out-of-band: any in-flight operational TLS sessions get torn
        down by ``device_service.stop()``. Trigger callers (the harness)
        therefore should not try to reuse the suite zone connection
        after this — they need to re-discover and re-commission.
        """
        # Snapshot the zone records we need for mDNS goodbyes BEFORE
        # we mutate state — `unpublish_operational` keys on
        # (zone_id, device_id) and we want to send a goodbye for every
        # one we've published.
        zones_snapshot = list(self._zones.values())

        # Stop the operational listener first so no new connections
        # arrive while we're tearing down. The bounded
        # ``listener.wait_closed`` (Phase 22 fix) caps this at 0.5s.
        if self._device_service is not None:
            with contextlib.suppress(Exception):
                await self._device_service.stop()
            self._device_service = None

        # Withdraw every per-zone operational mDNS record.
        if self._advertiser is not None:
            for z in zones_snapshot:
                with contextlib.suppress(Exception):
                    await self._advertiser.unpublish_operational(
                        z.zone_id, z.device_id
                    )

        # Drop in-memory zone state.
        self._zones.clear()
        for z in zones_snapshot:
            with contextlib.suppress(Exception):
                await self._zone_pool.remove_zone(z.zone_id)
        self._state = None

        # Wipe persisted artifacts so a restart is clean. Best-effort:
        # missing files are normal (state_dir may be None for ad-hoc
        # runs).
        if self._args.state_dir is not None:
            with contextlib.suppress(Exception):
                _wipe_state(self._args.state_dir)

        # Re-arm the commission_done event so the next initial
        # commission can fire it. ``_op_listener_ready`` is consumed
        # once-per-uncommissioned phase too.
        self._commission_done = asyncio.Event()
        self._op_listener_ready = asyncio.Event()
        # Bring the comm listener up (uncommissioned-phase advertise +
        # listen). ``_enter_commissioning_mode`` is idempotent under the
        # comm lock and bumps the epoch — if the listener is already
        # up (common: factory_reset right after a successful
        # commission), this is just a re-advertise; otherwise it
        # rebinds.
        await self._enter_commissioning_mode()
        # Spawn a fresh background task that watches for the next
        # initial commission and brings the operational service back
        # up — same path the original ``_enter_uncommissioned`` runs.
        # Without this, a factory_reset → commission round-trip would
        # leave the device in commissionable mode forever.
        asyncio.create_task(self._await_recommission_after_reset())

    async def _await_recommission_after_reset(self) -> None:
        """After ``_factory_reset``, wait for ``_commission_done`` and
        bring the operational service back up.

        Mirrors :meth:`_enter_uncommissioned`'s tail (post-wait), minus
        the first listener bind which ``_factory_reset`` already did.
        """
        assert self._stop_event is not None
        commission_task = asyncio.create_task(self._commission_done.wait())
        stop_task = asyncio.create_task(self._stop_event.wait())
        try:
            await asyncio.wait(
                {commission_task, stop_task},
                return_when=asyncio.FIRST_COMPLETED,
            )
        finally:
            for t in (commission_task, stop_task):
                if not t.done():
                    t.cancel()
                    with contextlib.suppress(asyncio.CancelledError):
                        await t
        if self._stop_event.is_set() and not self._commission_done.is_set():
            return
        if self._state is None:
            _log.warning(
                "post-factory-reset: commission_done set without state"
            )
            return
        if self._args.state_dir is not None:
            _persist_commissioned_state(self._args.state_dir, self._state)
        captured_epoch = self._comm_epoch
        await self._enter_commissioned(transition_from_commissionable=True)
        self._op_listener_ready.set()
        await self._exit_commissioning_for_epoch(captured_epoch)

    async def _exit_commissioning_for_epoch(self, captured_epoch: int) -> None:
        """Close the listener only if no Enter has overtaken ``captured_epoch``.

        Mirrors ``mash-go``'s ``exitCommissioningForEpoch``. The
        post-initial-commission cleanup in :meth:`_enter_uncommissioned`
        captures the epoch *before* its slow ``_enter_commissioned``
        step; if a TestControl trigger has re-armed the window in the
        meantime, the captured epoch is stale and we skip the close —
        the harness expects the listener open and the freshly-bound one
        is the right thing to keep.
        """
        async with self._comm_lock:
            if self._comm_epoch != captured_epoch:
                _log.debug(
                    "exit_commissioning_for_epoch: skipped "
                    "(captured=%d, current=%d — re-entry overtook close)",
                    captured_epoch,
                    self._comm_epoch,
                )
                return
            await self._stop_commissioning_listener()

    def _build_test_control(self) -> TestControlFeature:
        """Construct a :class:`TestControlFeature` wired to lifecycle effects.

        The feature handles the wire-side gate (enable-key match,
        per-DEC-087 status precedence) inside :class:`TestControlFeature`
        itself; we only supply the effect callbacks here. The
        zone-type accessor returns the single zone's type label so the
        TEST-zone gate (Spec Issue #15.B / DEC-087) rejects triggers
        that arrive over a non-TEST zone connection.
        """
        # ZoneType integers per mash-go ``pkg/cert/types.go`` (1/2/3 —
        # 0 is reserved). ``zone_type`` on :class:`CommissionedState` is
        # whatever the cert install put there, lowercased here so the
        # gate's case-insensitive comparison works regardless of how
        # mash-py spells it internally.
        zone_type_label_map = {1: "GRID", 2: "LOCAL", 3: "TEST"}

        def zone_type_accessor() -> str | None:
            if self._state is None:
                return None
            return zone_type_label_map.get(self._state.zone_type)

        feature = TestControlFeature(
            enable_key=self._args.enable_key,
            zone_type_accessor=zone_type_accessor,
        )
        feature.set_trigger_handler(self._dispatch_trigger)
        feature.set_duration_handler(self._dispatch_duration)
        return feature

    async def _dispatch_trigger(self, opcode: int) -> None:
        """Route a TestControl trigger opcode to a lifecycle effect.

        Mirrors ``mash-go/pkg/service/test_event_handler.go`` — only the
        triggers a Phase 22 conformance run actually exercises are wired
        here; the rest no-op (logged at debug). When a use case requires
        more, add a branch below and document it in
        ``docs/features/test-control/triggers.yaml``.
        """
        if opcode == TRIGGER_ENTER_COMMISSIONING_MODE:
            _log.info("trigger: entering commissioning mode")
            await self._enter_commissioning_mode()
        elif opcode == TRIGGER_EXIT_COMMISSIONING_MODE:
            _log.info("trigger: exiting commissioning mode")
            await self._exit_commissioning_mode()
        elif opcode == TRIGGER_FACTORY_RESET:
            _log.info("trigger: factory reset")
            await self._factory_reset()
        elif opcode == TRIGGER_RESET_TEST_STATE:
            _log.debug("trigger: reset test state — start")
            # Two effects on each reset:
            #
            # 1. **Drop leaked non-suite, non-TEST zones.** mash-go's
            #    handler does the same thing
            #    (``test_event_handler.go``: "Remove leaked/partial
            #    non-TEST zones") so the next test starts with the
            #    suite control channel as the only live zone. Without
            #    this, a test that fills capacity (e.g.
            #    TC-SEC-CONN-002 commissions GRID + LOCAL) leaves
            #    those zones in the pool, and a follow-up test that
            #    tries to commission the same zone-type hits
            #    ZoneTypeExistsError. The Phase 22 fixture only
            #    carries DeviceInfo + TestControl on the root endpoint,
            #    so there's no Status / Measurement / EnergyControl
            #    attributes to reset to defaults the way mash-go does.
            #
            # 2. **Cycle the commissioning window.** Tests with
            #    ``device_in_commissioning_mode`` precondition rely on
            #    the window being open when their ``commission`` action
            #    dials the comm port — without an exit→enter cycle
            #    here, a closed window from a prior test leaks through
            #    and every commission-precondition test fails at
            #    PASE-read.
            _log.debug("trigger: reset test state")
            primary_zone_id = (
                self._state.zone_id if self._state is not None else None
            )
            stale_zone_ids = [
                zid
                for zid, zstate in self._zones.items()
                if zid != primary_zone_id
                and zstate.zone_type != int(ZoneType.TEST)
            ]
            for zid in stale_zone_ids:
                await self._remove_zone_async(zid)
            await self._exit_commissioning_mode()
            await self._enter_commissioning_mode()
            _log.debug("trigger: reset test state — done")
        else:
            _log.info("trigger: unsupported opcode 0x%016x (no-op)", opcode)

    async def _dispatch_duration(self, duration_s: int) -> None:
        """Stash the next-window duration for ``_enter_commissioning_mode``.

        mash-go applies the duration as a window-close timer. mash-py's
        comm listener stays open until either a successful PASE+install
        (handler-side close) or an explicit
        :py:meth:`_exit_commissioning_mode` from the harness. The
        conformance harness drives those explicitly, so a stored
        duration is currently informational — but we keep it for parity
        and so future phases can implement a self-closing timer here.
        """
        self._comm_window_duration_s = duration_s
        _log.info("set_commissioning_window_duration: %ds", duration_s)

    # ------------------------------------------------------------------
    # Commissioned phase
    # ------------------------------------------------------------------

    async def _enter_commissioned(
        self, *, transition_from_commissionable: bool = False
    ) -> None:
        assert self._state is not None

        test_control = self._build_test_control()
        device = _build_device(
            self._args,
            device_id=self._state.device_id,
            test_control=test_control,
            remove_zone_callback=self._remove_zone_sync,
        )

        self._device_service = DeviceService(
            device,
            zone_ca=self._state.zone_ca_cert,
            operational_cert=self._state.operational_cert,
            operational_key=self._state.operational_key,
        )
        await self._device_service.start(
            host="0.0.0.0", port=self._args.operational_port
        )
        op_port = self._device_service.port
        _log.info("operational listener up on :%d", op_port)

        assert self._advertiser is not None
        ad = OperationalAdvertisement(
            zone_id=self._state.zone_id,
            device_id=self._state.device_id,
            port=op_port,
            vendor_product=f"{self._args.brand}:{self._args.model}",
            firmware="mash-py-1.0",
            endpoint_count=len(device.endpoints()),
        )
        if transition_from_commissionable:
            await self._advertiser.transition_to_operational(ad)
        else:
            await self._advertiser.publish_operational(ad)
        _log.info(
            "advertised _mash._tcp (zone_id=%s device_id=%s port=%d)",
            self._state.zone_id,
            self._state.device_id,
            op_port,
        )

    # ------------------------------------------------------------------
    # Teardown
    # ------------------------------------------------------------------

    async def _teardown(self) -> None:
        _log.info("teardown: stopping device service + listeners")
        self._cancel_window_timer()
        if self._device_service is not None:
            with contextlib.suppress(Exception):
                await self._device_service.stop()
            self._device_service = None
        await self._stop_commissioning_listener()
        if self._advertiser is not None:
            if self._state is not None:
                with contextlib.suppress(Exception):
                    await self._advertiser.unpublish_operational(
                        self._state.zone_id, self._state.device_id
                    )
            with contextlib.suppress(Exception):
                await self._advertiser.close()
            self._advertiser = None
        _log.info("teardown: complete (mDNS goodbyes sent)")


async def _send_commissioning_busy(
    writer: asyncio.StreamWriter,
    *,
    retry_after_ms: int,
    reason: str,
) -> None:
    """Emit a single ``CommissioningError{Busy, RetryAfter}`` frame and drain.

    Used by the comm handler for DEC-063 busy rejections. ``CommissioningError``
    is the only legitimate envelope on the comm channel before/instead of
    ``PASEResponse``; PASE-internal failures use ``PASEComplete{auth_failed}``.
    Best-effort write — the peer may have already torn down the socket; in
    that case we still want to fall through and let the handler's ``finally``
    close cleanly.
    """
    msg = CommissioningError(
        error_code=ERR_CODE_BUSY,
        message=reason,
        retry_after_ms=retry_after_ms,
    )
    try:
        framer = AsyncFrameWriter(writer, max_message_size=DEFAULT_MAX_MESSAGE_SIZE)
        encoded = encode_message(msg)
        _log.debug(
            "sending CommissioningError{Busy} (%d bytes, retry_after=%dms)",
            len(encoded),
            retry_after_ms,
        )
        await framer.write_frame(encoded)
        await writer.drain()
    except (ConnectionError, OSError, asyncio.TimeoutError) as exc:
        # Peer already gone — outer finally closes the writer. Suppress so
        # busy-rejection telemetry doesn't trip the broad-except handler
        # in the caller.
        _log.debug("CommissioningError{Busy} write failed (peer gone?): %r", exc)


def _resolve_first_port(server: asyncio.Server) -> int:
    socks = server.sockets
    if not socks:
        raise RuntimeError("server has no bound socket")
    addr = socks[0].getsockname()
    if not (isinstance(addr, tuple) and len(addr) >= 2 and isinstance(addr[1], int)):
        raise RuntimeError(f"unexpected sockname: {addr!r}")
    return addr[1]


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------


def _setup_logging(level_name: str) -> None:
    levels = {
        "debug": logging.DEBUG,
        "info": logging.INFO,
        "warn": logging.WARNING,
        "error": logging.ERROR,
    }
    logging.basicConfig(
        level=levels[level_name],
        format="%(asctime)s %(levelname)-7s %(name)s: %(message)s",
        stream=sys.stderr,
    )


async def _async_main(args: DeviceArgs) -> int:
    if args.reset and args.state_dir is not None:
        _wipe_state(args.state_dir)

    lifecycle = _DeviceLifecycle(args)
    stop_event = asyncio.Event()

    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        with contextlib.suppress(NotImplementedError):
            loop.add_signal_handler(sig, stop_event.set)

    try:
        await lifecycle.run(stop_event)
    except asyncio.CancelledError:
        pass
    return 0


def main(argv: list[str] | None = None) -> int:
    args = _parse_args(argv if argv is not None else sys.argv[1:])
    _setup_logging(args.log_level)
    _log.info(
        "starting mash-py-device: discriminator=%d setup-code=%s ports=op/%d,comm/%d",
        args.discriminator,
        args.setup_code,
        args.operational_port,
        args.commissioning_port,
    )
    try:
        return asyncio.run(_async_main(args))
    except KeyboardInterrupt:
        return 130


if __name__ == "__main__":
    raise SystemExit(main())
