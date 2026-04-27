"""Interaction model client (Phase 11).

Implements the controller-side correlation layer described in
``docs/interaction-model.md``: messageId-keyed pending calls,
notification dispatch by subscriptionId, and high-level
``read``/``write``/``subscribe``/``invoke`` methods.

The Go reference (``mash-go/pkg/interaction``) splits the same
responsibilities across :class:`Client` and an external read loop the
caller owns; mash-py folds the read loop into the client by making it a
:class:`mash_py.transport.connection.ConnectionHandler`. The
:class:`Connection` already serialises frames into ``on_message``
callbacks and handles control frames per DEC-083, so the
:class:`InteractionClient` only needs to classify *application* frames
and route them.
"""

from __future__ import annotations

from mash_py.interaction.client import (
    DEFAULT_REQUEST_TIMEOUT,
    InteractionClient,
    InteractionClientClosedError,
    InteractionError,
    InteractionTimeoutError,
    NotificationHandler,
    StatusError,
    Subscription,
)
from mash_py.interaction.server import (
    CommandError,
    OperationalServer,
)

__all__ = [
    "DEFAULT_REQUEST_TIMEOUT",
    "CommandError",
    "InteractionClient",
    "InteractionClientClosedError",
    "InteractionError",
    "InteractionTimeoutError",
    "NotificationHandler",
    "OperationalServer",
    "StatusError",
    "Subscription",
]
