"""Action handlers for the mash-py test harness (Phase 21).

Per ``mash-py-tdd-plan.md`` Phase 21, this package implements the
*minimum useful subset* of actions referenced by the YAML test cases in
``mash-go/testdata/cases/`` that the Phase-21 acceptance gate requires:

* ``connect`` / ``connect_operational`` / ``disconnect``
* ``commission``
* ``read``, ``write``, ``invoke``, ``subscribe``, ``wait_notification``
* ``browse_mdns`` / ``start_discovery`` / ``wait_for_device`` /
  ``stop_discovery`` / ``verify_txt_records``
* ``wait``, ``compare``, ``compare_values``, ``evaluate``
* ``trigger_test_event``
* ``verify_commissioning_state``

Anything outside that list is intentionally absent; YAML cases that
require it are expected to be filtered out at runner-config time (via
``-files`` / ``-tags`` / ``-filter``) so the user never sees an "unknown
action" error mid-suite. The CLI surface in :mod:`mash_py.cli.test` ships
a default filter that only targets the five Phase-21 acceptance cases.
"""

from mash_py.testharness.handlers.commission import (
    handle_commission,
    setup_preconditions,
)
from mash_py.testharness.handlers.connect import (
    handle_connect,
    handle_connect_operational,
    handle_disconnect,
)
from mash_py.testharness.handlers.discovery import (
    handle_browse_mdns,
    handle_start_discovery,
    handle_stop_discovery,
    handle_verify_txt_records,
    handle_wait_for_device,
)
from mash_py.testharness.handlers.interaction import (
    handle_invoke,
    handle_read,
    handle_subscribe,
    handle_wait_notification,
    handle_write,
)
from mash_py.testharness.handlers.util import (
    handle_compare,
    handle_compare_values,
    handle_evaluate,
    handle_trigger_test_event,
    handle_verify_commissioning_state,
    handle_wait,
)
from mash_py.testharness.runner import Runner


def register_default_handlers(runner: Runner) -> None:
    """Attach the Phase-21 handler set to ``runner``."""
    runner.register("commission", handle_commission)

    runner.register("connect", handle_connect)
    runner.register("connect_operational", handle_connect_operational)
    runner.register("disconnect", handle_disconnect)

    runner.register("read", handle_read)
    runner.register("write", handle_write)
    runner.register("invoke", handle_invoke)
    runner.register("subscribe", handle_subscribe)
    runner.register("wait_notification", handle_wait_notification)

    runner.register("browse_mdns", handle_browse_mdns)
    runner.register("start_discovery", handle_start_discovery)
    runner.register("stop_discovery", handle_stop_discovery)
    runner.register("wait_for_device", handle_wait_for_device)
    runner.register("verify_txt_records", handle_verify_txt_records)

    runner.register("wait", handle_wait)
    runner.register("compare", handle_compare)
    runner.register("compare_values", handle_compare_values)
    runner.register("evaluate", handle_evaluate)
    runner.register("verify_commissioning_state", handle_verify_commissioning_state)
    runner.register("trigger_test_event", handle_trigger_test_event)

    runner.set_precondition_setup(setup_preconditions)


__all__ = [
    "handle_browse_mdns",
    "handle_commission",
    "handle_compare",
    "handle_compare_values",
    "handle_connect",
    "handle_connect_operational",
    "handle_disconnect",
    "handle_evaluate",
    "handle_invoke",
    "handle_read",
    "handle_start_discovery",
    "handle_stop_discovery",
    "handle_subscribe",
    "handle_trigger_test_event",
    "handle_verify_commissioning_state",
    "handle_verify_txt_records",
    "handle_wait",
    "handle_wait_for_device",
    "handle_wait_notification",
    "handle_write",
    "register_default_handlers",
    "setup_preconditions",
]
