"""TestControl runtime — hand-written companion to ``test_control_gen``.

The generated module supplies attribute / command IDs and the
:class:`TestControl` feature class with the spec yaml's metadata. This
hand-written module provides what the YAML cannot describe:

* the **trigger opcode catalogue** (domain-encoded uint64s that the
  ``triggerTestEvent`` command dispatches on; the canonical registry
  lives at ``docs/features/test-control/triggers.yaml`` per DEC-087,
  and the constants below mirror it byte-for-byte),
* a Python-level **dispatcher contract** so a service can register
  domain-specific effect callbacks (open commissioning window, set
  fault, etc.) and the wire-side handler routes opcodes to them,
* the **enable-key safety gate** the spec requires.

It mirrors ``mash-go/pkg/features/test_control.go`` (the constants) and
``mash-go/pkg/service/test_event_handler.go`` (the dispatcher) but lives
in one place because mash-py keeps service-side wiring closer to the
feature.

Spec references (DEC-087 codified the semantics this module implements;
prior to DEC-087 these were Issue #15 sub-A/B/C in
``mash-py-spec-issues.md``):

A. ``triggerTestEvent`` failures use the regular non-success Status
   envelope per ``docs/interaction-model.md §8`` and the failure-Status
   table in ``docs/testing/test-process.md §7``. The bare
   ``{1: success: bool}`` payload is reserved for the success path
   only — devices MUST NOT return it from a failure. mash-py raises
   :class:`CommandError` with ``Status.NOT_AUTHORIZED`` for both the
   TEST-zone gate and the enable-key mismatch (same Status for both
   auth-style failures avoids leaking which check failed to a network
   observer).

B. ``test-process.md §7`` makes the TEST-zone gate a normative MUST.
   mash-py implements the gate when a ``zone_type_accessor`` is
   supplied; it degrades open without an accessor so unit tests that
   instantiate the feature without a session context still work.
   Production callers wire the accessor through the device service.

C. ``docs/features/test-control/triggers.yaml`` is the canonical
   opcode registry. The constants below are kept in lockstep manually
   for now; a future codegen task (per DEC-087) will emit them
   directly from the YAML in both implementations.

mash-go has a manually-registered ``getTestState`` cmd 0x03 that the
runner uses for test state-leak detection. It is NOT in the spec yaml;
mash-py omits it for now. If the runner ever drives mash-py directly,
plug it in via a follow-up registration helper on this module.
"""

from __future__ import annotations

from collections.abc import Awaitable, Callable
from typing import Any

from mash_py.features.test_control_gen import (
    SET_COMMISSIONING_WINDOW_DURATION_PARAM_DURATION_SECONDS,
    SET_COMMISSIONING_WINDOW_DURATION_PARAM_ENABLE_KEY,
    SET_COMMISSIONING_WINDOW_DURATION_RESP_SUCCESS,
    TRIGGER_TEST_EVENT_PARAM_ENABLE_KEY,
    TRIGGER_TEST_EVENT_PARAM_EVENT_TRIGGER,
    TRIGGER_TEST_EVENT_RESP_SUCCESS,
)
from mash_py.features.test_control_gen import (
    TestControl as _GeneratedTestControl,
)
from mash_py.interaction.server import CommandError
from mash_py.wire import Status

# Grouped by trigger-opcode domain for readability rather than
# alphabetised — the domain layout matches mash-go's catalogue and the
# spec's eventual triggers.yaml registry. RUF022 wants alphabetic order;
# we suppress because grouped order is the documentation intent.
__all__ = [  # noqa: RUF022
    "DEFAULT_ENABLE_KEY",
    "TestControlFeature",
    "TriggerHandler",
    "trigger_domain",
    # DeviceInfo-domain triggers
    "TRIGGER_ENTER_COMMISSIONING_MODE",
    "TRIGGER_EXIT_COMMISSIONING_MODE",
    "TRIGGER_FACTORY_RESET",
    "TRIGGER_RESET_TEST_STATE",
    "TRIGGER_ADJUST_CLOCK_BASE",
    # Status-domain triggers
    "TRIGGER_FAULT",
    "TRIGGER_CLEAR_FAULT",
    "TRIGGER_SET_STANDBY",
    "TRIGGER_SET_RUNNING",
    # Measurement-domain triggers
    "TRIGGER_SET_POWER_100",
    "TRIGGER_SET_POWER_1000",
    "TRIGGER_SET_POWER_ZERO",
    "TRIGGER_SET_SOC_50",
    "TRIGGER_SET_SOC_100",
    "TRIGGER_SET_POWER_CUSTOM_BASE",
    # ChargingSession-domain triggers
    "TRIGGER_EV_PLUG_IN",
    "TRIGGER_EV_UNPLUG",
    "TRIGGER_EV_REQUEST_CHARGE",
    # EnergyControl-domain triggers
    "TRIGGER_CONTROL_STATE_AUTONOMOUS",
    "TRIGGER_CONTROL_STATE_CONTROLLED",
    "TRIGGER_CONTROL_STATE_LIMITED",
    "TRIGGER_CONTROL_STATE_FAILSAFE",
    "TRIGGER_CONTROL_STATE_OVERRIDE",
    "TRIGGER_PROCESS_STATE_NONE",
    "TRIGGER_PROCESS_STATE_AVAILABLE",
    "TRIGGER_PROCESS_STATE_SCHEDULED",
    "TRIGGER_PROCESS_STATE_RUNNING",
    "TRIGGER_PROCESS_STATE_PAUSED",
    "TRIGGER_PROCESS_STATE_COMPLETED",
    "TRIGGER_PROCESS_STATE_ABORTED",
]


# ---------------------------------------------------------------------------
# Default enable-key
# ---------------------------------------------------------------------------

DEFAULT_ENABLE_KEY: str = "00112233445566778899aabbccddeeff"
"""128-bit hex key matching mash-go's ``mash-device -enable-key`` default.

The spec says the key is 32 hex chars / 128-bit but does not prescribe a
default. We mirror mash-go so an interop fixture that defaults both
sides will line up out of the box. ``"0" * 32`` is treated as
"triggers disabled" (matches mash-go's ``isEnableKeyValid`` check)."""


# ---------------------------------------------------------------------------
# Trigger opcode catalogue
# ---------------------------------------------------------------------------
# Encoded layout (matches ``docs/features/test-control/triggers.yaml`` per
# DEC-087, and ``mash-go/pkg/features/test_control.go`` byte-for-byte):
#
#   bits 63..48 — feature-id domain (matches ``FeatureType`` numeric ids)
#   bits 47..32 — sub-domain selector (parameterised triggers only)
#   bits 31..0  — payload value or fixed selector
#
# Canonical registry: ``docs/features/test-control/triggers.yaml``.
# Until the codegen pipeline emits these constants directly from the
# YAML (planned per DEC-087), the constants below are kept in lockstep
# by hand. Add a new trigger to the YAML first; mirror here second.

# DeviceInfo (0x0001).
TRIGGER_ENTER_COMMISSIONING_MODE: int = 0x0001_0000_0000_0001
TRIGGER_EXIT_COMMISSIONING_MODE: int = 0x0001_0000_0000_0002
TRIGGER_FACTORY_RESET: int = 0x0001_0000_0000_0003
TRIGGER_RESET_TEST_STATE: int = 0x0001_0000_0000_0004
TRIGGER_ADJUST_CLOCK_BASE: int = 0x0001_0001_0000_0000
"""Parameterised: low 32 bits encode signed seconds offset (two's complement)."""

# Status (0x0002).
TRIGGER_FAULT: int = 0x0002_0000_0000_0001
TRIGGER_CLEAR_FAULT: int = 0x0002_0000_0000_0002
TRIGGER_SET_STANDBY: int = 0x0002_0000_0000_0003
TRIGGER_SET_RUNNING: int = 0x0002_0000_0000_0004

# Measurement (0x0004).
TRIGGER_SET_POWER_100: int = 0x0004_0000_0000_0001
TRIGGER_SET_POWER_1000: int = 0x0004_0000_0000_0002
TRIGGER_SET_POWER_ZERO: int = 0x0004_0000_0000_0003
TRIGGER_SET_SOC_50: int = 0x0004_0000_0000_0010
TRIGGER_SET_SOC_100: int = 0x0004_0000_0000_0011
TRIGGER_SET_POWER_CUSTOM_BASE: int = 0x0004_0001_0000_0000
"""Parameterised: low 32 bits encode signed milliwatts (two's complement)."""

# EnergyControl (0x0005).
TRIGGER_CONTROL_STATE_AUTONOMOUS: int = 0x0005_0000_0000_0001
TRIGGER_CONTROL_STATE_CONTROLLED: int = 0x0005_0000_0000_0002
TRIGGER_CONTROL_STATE_LIMITED: int = 0x0005_0000_0000_0003
TRIGGER_CONTROL_STATE_FAILSAFE: int = 0x0005_0000_0000_0004
TRIGGER_CONTROL_STATE_OVERRIDE: int = 0x0005_0000_0000_0005
TRIGGER_PROCESS_STATE_NONE: int = 0x0005_0000_0000_0010
TRIGGER_PROCESS_STATE_AVAILABLE: int = 0x0005_0000_0000_0011
TRIGGER_PROCESS_STATE_SCHEDULED: int = 0x0005_0000_0000_0012
TRIGGER_PROCESS_STATE_RUNNING: int = 0x0005_0000_0000_0013
TRIGGER_PROCESS_STATE_PAUSED: int = 0x0005_0000_0000_0014
TRIGGER_PROCESS_STATE_COMPLETED: int = 0x0005_0000_0000_0015
TRIGGER_PROCESS_STATE_ABORTED: int = 0x0005_0000_0000_0016

# ChargingSession (0x0006).
TRIGGER_EV_PLUG_IN: int = 0x0006_0000_0000_0001
TRIGGER_EV_UNPLUG: int = 0x0006_0000_0000_0002
TRIGGER_EV_REQUEST_CHARGE: int = 0x0006_0000_0000_0003


def trigger_domain(opcode: int) -> int:
    """Return the upper-2-byte feature-id domain of a trigger opcode."""
    return (opcode >> 48) & 0xFFFF


# ---------------------------------------------------------------------------
# Dispatcher type
# ---------------------------------------------------------------------------

TriggerHandler = (
    Callable[[int], None] | Callable[[int], Awaitable[None]]
)
"""Effect callback registered by the device service.

Receives a 64-bit trigger opcode and is expected to apply the effect (or
raise :class:`CommandError` to surface a Status code on the wire).
Returning normally is reported as ``success: True`` per
``§7.2``-equivalent semantics for this command. Sync and async are both
allowed; the operational server awaits coroutines the same way it does
for any other command handler.
"""

DurationHandler = (
    Callable[[int], None] | Callable[[int], Awaitable[None]]
)
"""Effect callback for ``setCommissioningWindowDuration``.

Receives the validated, clamped duration in seconds. Raises
:class:`CommandError` to surface a Status code; returning normally
results in ``success: True`` on the wire.
"""

ZoneTypeAccessor = Callable[[], str | None]
"""Returns the caller-zone-type label (``"GRID"`` / ``"LOCAL"`` /
``"TEST"``) or ``None`` if it cannot be determined.

Used by the Go-parity TEST-zone safety gate (Spec Issue #15.B). When
``None`` is returned the gate degrades open — appropriate for unit tests
that don't carry a session context."""


# ---------------------------------------------------------------------------
# Feature class
# ---------------------------------------------------------------------------


class TestControlFeature(_GeneratedTestControl):
    """Runtime ``TestControl`` feature with dispatcher hooks.

    The generated parent class supplies the metadata (attribute,
    commands, parameter ids). This subclass adds:

    1. an enable-key (configurable; defaults to :data:`DEFAULT_ENABLE_KEY`),
    2. a registration surface for the trigger and
       set-commissioning-window-duration callbacks,
    3. internal command handlers wired into the parent at construction,
    4. an optional TEST-zone gate (Spec Issue #15.B).

    The ``testEventTriggersEnabled`` attribute is updated automatically
    so a controller doing ``Read(TestControl.testEventTriggersEnabled)``
    sees the same truth the dispatcher uses.
    """

    __test__ = False  # tells pytest "this is not a test class" (name matches Go's TestControl)

    def __init__(
        self,
        *,
        enable_key: str = DEFAULT_ENABLE_KEY,
        zone_type_accessor: ZoneTypeAccessor | None = None,
    ) -> None:
        super().__init__()
        self._enable_key: str = enable_key
        self._zone_type_accessor: ZoneTypeAccessor | None = zone_type_accessor
        self._trigger_handler: TriggerHandler | None = None
        self._duration_handler: DurationHandler | None = None

        # Reflect "triggers enabled" on the readable attribute so a
        # controller can poll it before issuing a triggerTestEvent.
        # The "all-zeros" sentinel matches mash-go's isEnableKeyValid.
        self.set_test_event_triggers_enabled(self._is_enable_key_active())

        # Wire the command handlers through the parent's command table.
        # The generated class exposes set_command_handler from the
        # Feature base; we use it here rather than overriding the
        # generated class so the codegen output stays untouched.
        from mash_py.features.test_control_gen import (
            TEST_CONTROL_CMD_SET_COMMISSIONING_WINDOW_DURATION,
            TEST_CONTROL_CMD_TRIGGER_TEST_EVENT,
        )

        self.set_command_handler(
            TEST_CONTROL_CMD_TRIGGER_TEST_EVENT,
            self._handle_trigger_test_event,
        )
        self.set_command_handler(
            TEST_CONTROL_CMD_SET_COMMISSIONING_WINDOW_DURATION,
            self._handle_set_commissioning_window_duration,
        )

    # ------------------------------------------------------------------
    # Configuration
    # ------------------------------------------------------------------

    @property
    def enable_key(self) -> str:
        return self._enable_key

    def set_enable_key(self, key: str) -> None:
        """Update the enable-key. Useful for tests that mint a fresh key."""
        self._enable_key = key
        self.set_test_event_triggers_enabled(self._is_enable_key_active())

    def set_trigger_handler(self, handler: TriggerHandler | None) -> None:
        """Register the callback that turns trigger opcodes into effects.

        Pass ``None`` to clear (any incoming trigger then surfaces as
        ``UNSUPPORTED`` — matches mash-go's "no callback registered"
        path that returns ``{1: false}`` but with a real Status to keep
        it diagnosable).
        """
        self._trigger_handler = handler

    def set_duration_handler(self, handler: DurationHandler | None) -> None:
        """Register the callback for ``setCommissioningWindowDuration``."""
        self._duration_handler = handler

    # ------------------------------------------------------------------
    # Internal: command handlers
    # ------------------------------------------------------------------

    async def _handle_trigger_test_event(
        self, params: dict[int, Any]
    ) -> dict[int, Any]:
        """Implements ``cmd 0x01`` per spec yaml + Issue #15.A semantics."""
        self._gate_test_zone()

        enable_key = self._extract_string(
            params, TRIGGER_TEST_EVENT_PARAM_ENABLE_KEY, "enableKey"
        )
        opcode = self._extract_uint(
            params, TRIGGER_TEST_EVENT_PARAM_EVENT_TRIGGER, "eventTrigger"
        )

        if enable_key != self._enable_key:
            raise CommandError(
                Status.NOT_AUTHORIZED, "triggerTestEvent: enable-key mismatch"
            )

        handler = self._trigger_handler
        if handler is None:
            raise CommandError(
                Status.UNSUPPORTED,
                "triggerTestEvent: no trigger handler registered",
            )

        result: Any = handler(opcode)
        if hasattr(result, "__await__"):
            await result

        return {TRIGGER_TEST_EVENT_RESP_SUCCESS: True}

    async def _handle_set_commissioning_window_duration(
        self, params: dict[int, Any]
    ) -> dict[int, Any]:
        """Implements ``cmd 0x02``: validates key, clamps duration,
        delegates to the registered handler.

        Clamp range mirrors mash-go: ``[3s, 10800s]``. The spec yaml
        allows any uint32; the clamp is a sanity guard so a buggy test
        cannot wedge the device with a 100-year window.
        """
        self._gate_test_zone()

        enable_key = self._extract_string(
            params,
            SET_COMMISSIONING_WINDOW_DURATION_PARAM_ENABLE_KEY,
            "enableKey",
        )
        duration = self._extract_uint(
            params,
            SET_COMMISSIONING_WINDOW_DURATION_PARAM_DURATION_SECONDS,
            "durationSeconds",
        )

        if enable_key != self._enable_key:
            raise CommandError(
                Status.NOT_AUTHORIZED,
                "setCommissioningWindowDuration: enable-key mismatch",
            )

        handler = self._duration_handler
        if handler is None:
            raise CommandError(
                Status.UNSUPPORTED,
                "setCommissioningWindowDuration: no handler registered",
            )

        clamped = max(3, min(10800, duration))
        result: Any = handler(clamped)
        if hasattr(result, "__await__"):
            await result

        return {SET_COMMISSIONING_WINDOW_DURATION_RESP_SUCCESS: True}

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    def _gate_test_zone(self) -> None:
        """Implements Spec Issue #15.B (Go-parity TEST-zone gate).

        Degrades open when no accessor is configured so unit tests that
        instantiate the feature without a session context still work.
        """
        if self._zone_type_accessor is None:
            return
        zt = self._zone_type_accessor()
        if zt is None:
            # Caller couldn't determine the zone type; treat as
            # unauthorised. Better to surface a clear status than to
            # silently allow a dangerous trigger.
            raise CommandError(
                Status.NOT_AUTHORIZED,
                "TestControl commands require a TEST-zone session context",
            )
        if zt.upper() != "TEST":
            raise CommandError(
                Status.NOT_AUTHORIZED,
                f"TestControl commands require a TEST zone (caller zone is {zt!r})",
            )

    def _is_enable_key_active(self) -> bool:
        """Mirror mash-go's ``isEnableKeyValid``: 32 hex chars + non-zero."""
        if len(self._enable_key) != 32:
            return False
        if self._enable_key == "0" * 32:
            return False
        try:
            int(self._enable_key, 16)
        except ValueError:
            return False
        return True

    @staticmethod
    def _extract_string(
        params: dict[int, Any], param_id: int, label: str
    ) -> str:
        if param_id not in params:
            raise CommandError(
                Status.INVALID_PARAMETER, f"{label}: missing parameter"
            )
        value = params[param_id]
        if not isinstance(value, str):
            raise CommandError(
                Status.INVALID_PARAMETER, f"{label}: must be a string"
            )
        return value

    @staticmethod
    def _extract_uint(
        params: dict[int, Any], param_id: int, label: str
    ) -> int:
        if param_id not in params:
            raise CommandError(
                Status.INVALID_PARAMETER, f"{label}: missing parameter"
            )
        value = params[param_id]
        if isinstance(value, bool) or not isinstance(value, int):
            raise CommandError(
                Status.INVALID_PARAMETER, f"{label}: must be an integer"
            )
        ivalue: int = value
        if ivalue < 0:
            raise CommandError(
                Status.INVALID_PARAMETER, f"{label}: must be non-negative"
            )
        return ivalue
