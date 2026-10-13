"""cbor2 version compatibility: normalise decoded values to mutable types.

cbor2 changed the decode result type for values nested inside a CBOR tag
(DEC-083 wraps every MASH control message in tag 77, so this is the common
path for control frames):

- cbor2 5.x: maps decode to ``dict``, arrays to ``list``, sets to ``set``.
- cbor2 6.x: values *inside a tag* decode to ``frozendict`` / ``tuple`` /
  ``frozenset``, regardless of the ``immutable=`` flag.

MASH decoders type-check with ``isinstance(value, dict)`` in ~35 places, and
``frozendict`` is not a ``dict`` subclass (it only satisfies ``Mapping``). On
cbor2 6.x that made every control frame fail to decode, which cascaded into
ping/pong timeouts and aborted connection closes.

This module is the single adaptation point: every MASH decode path routes its
``cbor2.loads`` result through :func:`to_mutable`, so the rest of the codebase
sees the same types it did on cbor2 5.x.
"""

from __future__ import annotations

from collections.abc import Mapping
from typing import Any

import cbor2

__all__ = ["to_mutable"]


def to_mutable(value: Any) -> Any:
    """Recursively convert cbor2's immutable decode results to plain types.

    ``frozendict`` -> ``dict``, ``tuple`` -> ``list``, ``frozenset`` -> ``set``.
    ``CBORTag`` values are normalised (tag number preserved). Everything else
    is returned unchanged, so ``bytes``, ``str``, ``int``, ``float``, ``bool``
    and ``None`` keep their identity.

    The map check uses ``Mapping`` rather than ``dict`` because
    ``cbor2.frozendict`` is *not* a ``dict`` subclass — it only satisfies
    ``Mapping``. Already-mutable values pass through; the conversion is
    idempotent.
    """
    if isinstance(value, cbor2.CBORTag):
        return cbor2.CBORTag(value.tag, to_mutable(value.value))
    if isinstance(value, Mapping):
        return {k: to_mutable(v) for k, v in value.items()}
    if isinstance(value, tuple):
        return [to_mutable(v) for v in value]
    if isinstance(value, frozenset):
        return {to_mutable(v) for v in value}
    return value
