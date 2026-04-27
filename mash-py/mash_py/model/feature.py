"""Feature, Attribute, and Command runtime base classes.

This is the surface that ``mash_py.features.*_gen`` modules import to
construct feature instances. The generated code calls
``add_attribute()`` and ``add_command()`` from a generated constructor,
then exposes typed getters/setters that read and write through the
metadata-keyed attribute store kept on the feature.

Mirrors ``mash-go/pkg/model/feature.go``, ``attribute.go``, and
``command.go`` at the API level. Phase 13 added:

- Access enforcement on the external write path
  (:meth:`Feature.write_attribute`).
- An internal-only write path
  (:meth:`Feature.set_attribute_internal`) for telemetry and state
  publishing on read-only attributes (matches Go's ``SetAttributeInternal``).
- Type / range / nullable validation for both write paths.
- Subscriber notifications on every write that actually changes the
  value (matches Go's ``FeatureSubscriber`` callback contract — see
  ``pkg/model/feature.go``).
"""

from __future__ import annotations

import contextlib
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from typing import Any

from mash_py.model.feature_type_gen import FeatureType
from mash_py.model.types import Access, DataType

# A command handler receives the integer-keyed parameter map (per
# docs/interaction-model.md §7.1) and returns the integer-keyed response
# map (per §7.2). Sync and async handlers are both allowed; the
# operational server awaits the result if it's a coroutine.
CommandHandler = (
    Callable[[dict[int, Any]], dict[int, Any]]
    | Callable[[dict[int, Any]], Awaitable[dict[int, Any]]]
)

# An attribute observer is invoked after a successful write that changed
# the stored value. Receives ``(feature_type, attr_id, value)`` — same
# signature as Go's ``FeatureSubscriber.OnAttributeChanged``.
AttributeObserver = Callable[[FeatureType, int, Any], None]


class ModelAttributeError(Exception):
    """Base class for attribute-level errors (unknown id, validation failures)."""


class AttributeNotFoundError(ModelAttributeError):
    """Raised when an attribute id is not present on the feature."""


class AttributeNotWritableError(ModelAttributeError):
    """Raised by the external write path when the attribute is read-only."""


class AttributeNotNullableError(ModelAttributeError):
    """Raised when ``None`` is written to an attribute that is not nullable."""


class AttributeValueTypeError(ModelAttributeError):
    """Raised when a value's type does not match the declared :class:`DataType`."""


class AttributeOutOfRangeError(ModelAttributeError):
    """Raised when a numeric value falls outside the declared min/max range."""


class CommandNotFoundError(ModelAttributeError):
    """Raised when a command id is not present on the feature."""


@dataclass(slots=True)
class AttributeMetadata:
    """Static description of an attribute, populated from YAML at codegen time."""

    id: int
    name: str
    type: DataType
    access: Access = Access.READ_ONLY
    nullable: bool = False
    default: Any = None
    min_value: Any = None
    max_value: Any = None
    unit: str = ""
    description: str = ""


@dataclass(slots=True)
class ParameterMetadata:
    """Description of a command parameter or response field."""

    id: int
    name: str
    type: DataType
    required: bool = False


@dataclass(slots=True)
class CommandMetadata:
    """Static description of a command, populated from YAML at codegen time."""

    id: int
    name: str
    description: str = ""
    parameters: list[ParameterMetadata] = field(default_factory=list)


@dataclass(slots=True)
class Attribute:
    """A live attribute slot: metadata + current value + dirty flag."""

    meta: AttributeMetadata
    value: Any = None
    dirty: bool = False

    def read(self) -> Any:
        return self.value

    def write(self, value: Any) -> bool:
        """Store ``value``. Returns True if the value actually changed."""
        if _safe_equal(self.value, value):
            return False
        self.value = value
        self.dirty = True
        return True


@dataclass(slots=True)
class Command:
    """A registered command: metadata + dispatcher callback."""

    meta: CommandMetadata
    handler: CommandHandler | None = None


class Feature:
    """Base class for generated feature classes.

    Generated subclasses populate the attribute/command tables in their
    ``__init__`` then expose typed getters/setters that delegate here.
    Direct callers can use :meth:`read_attribute` / :meth:`write_attribute`
    to address attributes by id (the generic dispatch path).
    """

    def __init__(self, feature_type: FeatureType, revision: int) -> None:
        self._feature_type = feature_type
        self._revision = revision
        self._feature_map: int = 0
        self._attributes: dict[int, Attribute] = {}
        self._commands: dict[int, Command] = {}
        self._observers: list[AttributeObserver] = []

    # --- Identity ---

    @property
    def feature_type(self) -> FeatureType:
        return self._feature_type

    @property
    def revision(self) -> int:
        return self._revision

    @property
    def feature_map(self) -> int:
        return self._feature_map

    def set_feature_map(self, bitmap: int) -> None:
        self._feature_map = bitmap

    # --- Attribute table ---

    def add_attribute(self, meta: AttributeMetadata) -> Attribute:
        attr = Attribute(meta=meta, value=meta.default)
        self._attributes[meta.id] = attr
        return attr

    def get_attribute(self, attr_id: int) -> Attribute:
        try:
            return self._attributes[attr_id]
        except KeyError as e:
            raise AttributeNotFoundError(
                f"unknown attribute id {attr_id} on {self._feature_type.name}"
            ) from e

    def has_attribute(self, attr_id: int) -> bool:
        return attr_id in self._attributes

    def attribute_ids(self) -> list[int]:
        return sorted(self._attributes.keys())

    def read_attribute(self, attr_id: int) -> Any:
        return self.get_attribute(attr_id).read()

    def read_all_attributes(self) -> dict[int, Any]:
        """Snapshot every readable attribute as ``{id: value}``."""
        return {aid: a.value for aid, a in self._attributes.items() if a.meta.access.is_readable()}

    def write_attribute(self, attr_id: int, value: Any) -> None:
        """External write path. Enforces ``access`` (must be writable).

        Validates type, nullable, and numeric range. Notifies observers
        only when the stored value actually changes.
        """
        attr = self.get_attribute(attr_id)
        if not attr.meta.access.is_writable():
            raise AttributeNotWritableError(
                f"attribute {attr.meta.name} (id {attr_id}) on "
                f"{self._feature_type.name} is {attr.meta.access.value}"
            )
        self._do_write(attr, value)

    def set_attribute_internal(self, attr_id: int, value: Any) -> None:
        """Internal write path. Bypasses ``access`` (for telemetry / state publishing).

        Validation (type, range, nullable) and observer notification
        still apply — only the writability check is skipped. Mirrors
        Go's ``Feature.SetAttributeInternal``.
        """
        attr = self.get_attribute(attr_id)
        self._do_write(attr, value)

    def _do_write(self, attr: Attribute, value: Any) -> None:
        _validate(attr.meta, value)
        if attr.write(value):
            self._notify(attr.meta.id, value)

    def clear_dirty(self) -> None:
        """Clear the dirty flag on every attribute (post-report bookkeeping)."""
        for attr in self._attributes.values():
            attr.dirty = False

    def dirty_attributes(self) -> dict[int, Any]:
        """Snapshot of attributes whose value changed since the last clear."""
        return {aid: a.value for aid, a in self._attributes.items() if a.dirty}

    # --- Command table ---

    def add_command(
        self, meta: CommandMetadata, handler: CommandHandler | None = None
    ) -> Command:
        cmd = Command(meta=meta, handler=handler)
        self._commands[meta.id] = cmd
        return cmd

    def get_command(self, cmd_id: int) -> Command:
        try:
            return self._commands[cmd_id]
        except KeyError as e:
            raise CommandNotFoundError(
                f"unknown command id {cmd_id} on {self._feature_type.name}"
            ) from e

    def has_command(self, cmd_id: int) -> bool:
        return cmd_id in self._commands

    def command_ids(self) -> list[int]:
        return sorted(self._commands.keys())

    def set_command_handler(self, cmd_id: int, handler: CommandHandler | None) -> None:
        self.get_command(cmd_id).handler = handler

    # --- Observers ---

    def subscribe(self, observer: AttributeObserver) -> None:
        self._observers.append(observer)

    def unsubscribe(self, observer: AttributeObserver) -> None:
        with contextlib.suppress(ValueError):
            self._observers.remove(observer)

    def _notify(self, attr_id: int, value: Any) -> None:
        for obs in list(self._observers):
            obs(self._feature_type, attr_id, value)


# --- Validation helpers ---

_INT_TYPES: frozenset[DataType] = frozenset(
    {
        DataType.INT8,
        DataType.INT16,
        DataType.INT32,
        DataType.INT64,
        DataType.UINT8,
        DataType.UINT16,
        DataType.UINT32,
        DataType.UINT64,
    }
)
_FLOAT_TYPES: frozenset[DataType] = frozenset({DataType.FLOAT32, DataType.FLOAT64})
_RANGE_TYPES: frozenset[DataType] = _INT_TYPES | _FLOAT_TYPES


def _validate(meta: AttributeMetadata, value: Any) -> None:
    if value is None:
        if not meta.nullable:
            raise AttributeNotNullableError(
                f"attribute {meta.name} (id {meta.id}) does not accept None"
            )
        return
    _check_type(meta, value)
    if meta.min_value is not None or meta.max_value is not None:
        _check_range(meta, value)


def _check_type(meta: AttributeMetadata, value: Any) -> None:
    t = meta.type
    if t == DataType.BOOL:
        if not isinstance(value, bool):
            raise AttributeValueTypeError(
                f"attribute {meta.name}: expected bool, got {type(value).__name__}"
            )
    elif t in _INT_TYPES:
        # bool is a subclass of int in Python; reject it explicitly so
        # an integer attribute doesn't silently swallow True/False.
        if isinstance(value, bool) or not isinstance(value, int):
            raise AttributeValueTypeError(
                f"attribute {meta.name}: expected int, got {type(value).__name__}"
            )
    elif t in _FLOAT_TYPES:
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise AttributeValueTypeError(
                f"attribute {meta.name}: expected float, got {type(value).__name__}"
            )
    elif t == DataType.STRING:
        if not isinstance(value, str):
            raise AttributeValueTypeError(
                f"attribute {meta.name}: expected str, got {type(value).__name__}"
            )
    elif t == DataType.BYTES and not isinstance(value, (bytes, bytearray)):
        raise AttributeValueTypeError(
            f"attribute {meta.name}: expected bytes, got {type(value).__name__}"
        )
    # ARRAY / MAP / UNKNOWN: no structural check (matches Go's
    # validateValue switch — those types fall through).


def _check_range(meta: AttributeMetadata, value: Any) -> None:
    if meta.type not in _RANGE_TYPES:
        return
    if meta.min_value is not None and value < meta.min_value:
        raise AttributeOutOfRangeError(
            f"attribute {meta.name}: {value} < min {meta.min_value}"
        )
    if meta.max_value is not None and value > meta.max_value:
        raise AttributeOutOfRangeError(
            f"attribute {meta.name}: {value} > max {meta.max_value}"
        )


def _safe_equal(a: Any, b: Any) -> bool:
    """Equality test that tolerates non-comparable types (slices/maps).

    Mirrors Go's ``safeNotEqual`` recover trick — anything that raises is
    treated as "not equal", causing the write to proceed and observers to
    fire. This is the conservative choice for collections that could
    have been mutated in place.
    """
    try:
        return bool(a == b)
    except Exception:  # pragma: no cover — defensive
        return False
