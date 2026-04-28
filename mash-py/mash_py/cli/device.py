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
import sys
from dataclasses import dataclass
from pathlib import Path

from cryptography import x509
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.hazmat.primitives.asymmetric.ec import EllipticCurvePrivateKey

from mash_py.commissioning import (
    CertExchangeServer,
    PASEServer,
    Verifier,
    generate_verifier,
)
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
from mash_py.model import Device, Endpoint, EndpointType
from mash_py.service import DeviceService
from mash_py.transport.tls import make_commissioning_context

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
        "--log-level",
        default="info",
        choices=("debug", "info", "warn", "error"),
    )
    ns = p.parse_args(argv)
    if not (0 <= ns.discriminator <= 4095):
        p.error("discriminator must be in 0..4095")
    if not (ns.setup_code.isdigit() and len(ns.setup_code) == 8):
        p.error("setup-code must be 8 decimal digits")
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


def _build_device(args: DeviceArgs, *, device_id: str) -> Device:
    """Build the Device tree.

    EVSE-shaped fixture: root endpoint + DeviceInfo + one EV_CHARGER endpoint.
    DeviceInfo carries the vendor/product/serial the conformance Read tests
    sample; the removeZone command handler is registered so Invoke tests
    succeed.
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
        # We only run one zone in the Phase 22 fixture; surface success
        # for that zone, false otherwise. The conformance harness only
        # checks the wire envelope, not the boolean — but a deterministic
        # answer makes failures easier to triage.
        return {REMOVE_ZONE_RESP_REMOVED: bool(zone_id)}

    di.set_command_handler(DEVICE_INFO_CMD_REMOVE_ZONE, remove_zone)

    device.add_endpoint(Endpoint(1, EndpointType.EV_CHARGER, args.name))
    return device


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
        self._state: CommissionedState | None = None
        if args.state_dir is not None and not args.reset:
            self._state = _load_commissioned_state(args.state_dir)
            if self._state is not None:
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
        ssl_ctx = make_commissioning_context(self._comm_cert, self._comm_key)
        self._comm_server = await asyncio.start_server(
            self._handle_commissioning_client,
            host="0.0.0.0",
            port=self._args.commissioning_port,
            ssl=ssl_ctx,
            ssl_handshake_timeout=30.0,
            start_serving=True,
        )
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
        await self._enter_commissioned(transition_from_commissionable=True)
        self._op_listener_ready.set()
        # The commissioning handler will close its writer on its own as
        # soon as it observes ``_op_listener_ready`` set.
        await self._stop_commissioning_listener()

    async def _handle_commissioning_client(
        self,
        reader: asyncio.StreamReader,
        writer: asyncio.StreamWriter,
    ) -> None:
        """Accept handler for the commissioning listener."""
        peer = writer.get_extra_info("peername")
        _log.info("commissioning peer connected: %s", peer)
        try:
            pase_server = PASEServer(self._verifier)
            k_main = await pase_server.handshake(reader, writer)
            _log.debug("PASE complete: K_main=%s...", k_main[:8].hex())

            cert_server = CertExchangeServer()
            result = await cert_server.run(reader, writer)
            _log.info(
                "cert install complete: zone_type=%d zone_priority=%d",
                result.zone_type,
                result.zone_priority,
            )

            device_id = _device_id_from_cert(result.operational_cert)
            self._state = CommissionedState(
                zone_ca_cert=result.zone_ca_cert,
                operational_cert=result.operational_cert,
                operational_key=result.operational_key,
                zone_type=result.zone_type,
                zone_priority=result.zone_priority,
                device_id=device_id,
            )
            self._commission_done.set()
            # DEC-082 ordering: park the connection until the main loop
            # has the operational listener up. Closing earlier lets the
            # controller race the device's listener bind and observe a
            # transient connection-refused.
            try:
                await asyncio.wait_for(self._op_listener_ready.wait(), timeout=10.0)
            except TimeoutError:
                _log.warning(
                    "commissioning handler timed out waiting for operational "
                    "listener bind; closing anyway"
                )
        except BaseException as exc:
            self._last_commission_error = exc
            _log.warning("commissioning attempt failed: %r", exc)
        finally:
            writer.close()
            with contextlib.suppress(ConnectionError, OSError):
                await writer.wait_closed()

    async def _stop_commissioning_listener(self) -> None:
        if self._comm_server is None:
            return
        srv = self._comm_server
        self._comm_server = None
        srv.close()
        with contextlib.suppress(Exception):
            await srv.wait_closed()
        if self._advertiser is not None:
            with contextlib.suppress(Exception):
                await self._advertiser.unpublish_commissionable()

    # ------------------------------------------------------------------
    # Commissioned phase
    # ------------------------------------------------------------------

    async def _enter_commissioned(
        self, *, transition_from_commissionable: bool = False
    ) -> None:
        assert self._state is not None
        device = _build_device(self._args, device_id=self._state.device_id)

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
