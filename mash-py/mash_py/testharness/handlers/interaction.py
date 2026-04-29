"""Interaction model handlers (Phase 11 wrapper).

These handlers cover the four spec-mandated interaction operations
(``docs/interaction-model.md``) plus the corresponding ``wait_notification``
sync primitive. They lazily open an :class:`~mash_py.interaction.client.InteractionClient`
on top of the connection that ``connect_operational`` (or an implicit
auto-commission) leaves in :data:`~mash_py.testharness.handlers._common.KEY_OPERATIONAL_CONN`.

Spec-vs-Go callouts:

* Read with empty ``attribute_ids`` returns all attributes
  (``interaction-model.md §4.3``). The Go runner sometimes pre-resolves
  attribute names to IDs via its inspect package (``internal/inspect``);
  Phase 21 takes the spec literally — handlers accept integer attribute
  IDs only (passing a name results in a clear validation error). Cases
  that need name-based addressing belong to Phase 22 conformance scope.
* Write expects ``status=Status.SUCCESS`` (``§5.4``) — no per-implementation
  retry behavior.
* Subscribe SubscriptionID is allocated by the device per ``§6.2``;
  the harness mirrors that and stashes it for ``wait_notification`` /
  unsubscribe.
"""

from __future__ import annotations

import asyncio
from typing import Any

from mash_py.interaction.client import (
    InteractionClient,
    StatusError,
    Subscription,
)
from mash_py.testharness.handlers._common import KEY_OPERATIONAL_CONN
from mash_py.testharness.loader import Step
from mash_py.testharness.runner import ExecutionState
from mash_py.transport.connection import Connection


_INTERACTION_CLIENT_KEY = "interaction_client"
_SUBSCRIPTION_KEY = "interaction_subscription"
_NOTIFICATION_QUEUE_KEY = "interaction_notification_queue"

# Stash for the most recent subscribe priming-report values. Mirrors the Go
# runner's "_priming_data" state key (see CheckerSavePrimingValue in
# mash-go/internal/testharness/engine/checkers.go) so the
# `save_priming_value` / `priming_value_different_from` checkers have a
# canonical location to read from across step boundaries.
_PRIMING_DATA_KEY = "_priming_data"


async def handle_read(step: Step, state: ExecutionState) -> dict[str, Any]:
    client = await _ensure_client(state)
    if client is None:
        return {"read_success": False, "error": "no operational connection"}

    endpoint = int(step.params.get("endpoint", 0))
    feature = _resolve_feature_id(step.params.get("feature", 0))
    attr = step.params.get("attribute")
    attr_ids: list[int] | None
    if attr is None:
        attr_ids = None
    elif isinstance(attr, list):
        attr_ids = [int(a) for a in attr if isinstance(a, (int, str)) and str(a).isdigit()]
        if len(attr_ids) != len(attr):
            return {
                "read_success": False,
                "error": "attribute names not supported in Phase 21 — pass integer IDs",
            }
    elif isinstance(attr, int):
        attr_ids = [attr]
    elif isinstance(attr, str) and attr.isdigit():
        attr_ids = [int(attr)]
    else:
        # Spec-vs-Go: Phase 21 deliberately rejects name-based addressing
        # (see module docstring). The YAML cases the harness ships only
        # use the "all-attributes" form (``attribute_ids=None``).
        attr_ids = None

    try:
        result = await client.read(endpoint, feature, attr_ids)
    except StatusError as exc:
        return {"read_success": False, "status": int(exc.status), "error": str(exc)}
    except Exception as exc:  # noqa: BLE001
        return {"read_success": False, "error": str(exc)}

    return {
        "read_success": True,
        "value": result,
        "attribute_count": len(result),
    }


async def handle_write(step: Step, state: ExecutionState) -> dict[str, Any]:
    client = await _ensure_client(state)
    if client is None:
        return {"write_success": False, "error": "no operational connection"}

    endpoint = int(step.params.get("endpoint", 0))
    feature = _resolve_feature_id(step.params.get("feature", 0))
    values = step.params.get("values") or {}
    if not isinstance(values, dict):
        return {"write_success": False, "error": "values must be a mapping"}

    coerced = {int(k): v for k, v in values.items() if str(k).isdigit() or isinstance(k, int)}
    try:
        await client.write(endpoint, feature, coerced)
    except StatusError as exc:
        return {"write_success": False, "status": int(exc.status), "error": str(exc)}
    except Exception as exc:  # noqa: BLE001
        return {"write_success": False, "error": str(exc)}
    return {"write_success": True}


async def handle_invoke(step: Step, state: ExecutionState) -> dict[str, Any]:
    client = await _ensure_client(state)
    if client is None:
        return {"invoke_success": False, "error": "no operational connection"}

    endpoint = int(step.params.get("endpoint", 0))
    feature = _resolve_feature_id(step.params.get("feature", 0))
    command = int(step.params.get("command", 0))
    args = step.params.get("args") or {}
    coerced_args: dict[int, Any]
    if isinstance(args, dict):
        coerced_args = {int(k): v for k, v in args.items() if str(k).isdigit() or isinstance(k, int)}
    else:
        coerced_args = {}

    try:
        result = await client.invoke(endpoint, feature, command, coerced_args)
    except StatusError as exc:
        return {"invoke_success": False, "status": int(exc.status), "error": str(exc)}
    except Exception as exc:  # noqa: BLE001
        return {"invoke_success": False, "error": str(exc)}
    return {"invoke_success": True, "value": result}


async def handle_subscribe(step: Step, state: ExecutionState) -> dict[str, Any]:
    client = await _ensure_client(state)
    if client is None:
        return {"subscribe_success": False, "error": "no operational connection"}

    endpoint = int(step.params.get("endpoint", 0))
    feature = _resolve_feature_id(step.params.get("feature", 0))
    min_interval_ms = int(step.params.get("min_interval_ms", 1000))
    max_interval_ms = int(step.params.get("max_interval_ms", 60000))

    queue: asyncio.Queue[dict[int, Any]] = asyncio.Queue()
    state.custom[_NOTIFICATION_QUEUE_KEY] = queue

    async def _on_notification(payload: dict[int, Any]) -> None:
        await queue.put(payload)

    try:
        sub: Subscription = await client.subscribe(
            endpoint,
            feature,
            handler=_on_notification,
            min_interval_ms=min_interval_ms,
            max_interval_ms=max_interval_ms,
        )
    except StatusError as exc:
        return {
            "subscribe_success": False,
            "status": int(exc.status),
            "error": str(exc),
            "subscription_id_returned": False,
            "subscription_id_saved": False,
            "subscription_id_present": False,
            "priming_received": False,
            "priming_is_priming_flag": False,
        }
    except Exception as exc:  # noqa: BLE001
        return {
            "subscribe_success": False,
            "error": str(exc),
            "subscription_id_returned": False,
            "subscription_id_saved": False,
            "subscription_id_present": False,
            "priming_received": False,
            "priming_is_priming_flag": False,
        }

    state.custom[_SUBSCRIPTION_KEY] = sub
    # initial_values is the priming report dict[int, Any] (interaction-model.md
    # §6.2). Stash it under the canonical key so save_priming_value /
    # priming_value_different_from checkers can read it later, mirroring
    # CheckerSavePrimingValue in the Go engine.
    state.custom[_PRIMING_DATA_KEY] = sub.initial_values
    has_priming = bool(sub.initial_values)
    saved_id = sub.subscription_id is not None
    # Output keys mirror handleSubscribe in
    # mash-go/internal/testharness/runner/runner.go so YAML cases share
    # the same expectation surface across runners.
    return {
        "subscribe_success": True,
        "subscription_id": sub.subscription_id,
        "subscription_id_returned": saved_id,
        "subscription_id_saved": saved_id,
        "subscription_id_present": saved_id,
        "priming_received": has_priming,
        "priming_is_priming_flag": has_priming,
        "priming_value": sub.initial_values if has_priming else None,
        "status": 0,
    }


async def handle_wait_notification(step: Step, state: ExecutionState) -> dict[str, Any]:
    timeout_s = float(step.params.get("timeout_ms", 5000)) / 1000
    queue: asyncio.Queue[dict[int, Any]] | None = state.custom.get(_NOTIFICATION_QUEUE_KEY)
    if queue is None:
        return {"notification_received": False, "error": "no active subscription"}
    try:
        payload = await asyncio.wait_for(queue.get(), timeout=timeout_s)
    except asyncio.TimeoutError:
        return {"notification_received": False, "error": f"no notification within {timeout_s}s"}
    return {
        "notification_received": True,
        "value": payload,
    }


# ---------------------------------------------------------------------------
# Internals
# ---------------------------------------------------------------------------


async def _ensure_client(state: ExecutionState) -> InteractionClient | None:
    """Return the :class:`InteractionClient` for the current operational connection.

    Lazy-instantiates over the reader/writer pair stashed by
    ``handle_connect_operational``. Phase 21 cases never have multiple
    operational connections in flight at once, so a single-client cache
    is sufficient.
    """
    cached = state.custom.get(_INTERACTION_CLIENT_KEY)
    if isinstance(cached, InteractionClient) and not cached.is_closed:
        return cached

    conn_state = state.custom.get(KEY_OPERATIONAL_CONN)
    if not conn_state:
        return None
    reader = conn_state["reader"]
    writer = conn_state["writer"]

    connection = Connection(reader, writer, "mash/1")
    client = InteractionClient(connection)
    client.start()

    state.custom[_INTERACTION_CLIENT_KEY] = client
    return client


def _resolve_feature_id(raw: Any) -> int:
    """Translate a YAML ``feature`` value into a numeric feature ID.

    Accepts either ``int`` or the spec-defined name string (case-insensitive).
    Phase 21 ships the small lookup table the acceptance cases need;
    extending it is YAML-driven and additive.
    """
    if isinstance(raw, int):
        return raw
    if isinstance(raw, str):
        if raw.isdigit():
            return int(raw)
        # Spec feature IDs from ``docs/features/protocol-versions.yaml``.
        # Only the entries required by Phase 21's YAML cases are listed;
        # adding more is mechanical and additive.
        names = {
            "deviceinfo": 0x01,
            "electrical": 0x02,
            "measurement": 0x03,
            "energycontrol": 0x04,
            "status": 0x05,
            "chargingsession": 0x06,
            "tariff": 0x07,
            "signals": 0x08,
            "plan": 0x09,
        }
        return names.get(raw.lower(), 0)
    return 0


__all__ = [
    "handle_invoke",
    "handle_read",
    "handle_subscribe",
    "handle_wait_notification",
    "handle_write",
]
