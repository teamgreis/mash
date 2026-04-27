"""Feature, Attribute, and Command runtime base classes.

This is the small surface that ``mash_py.features.*_gen`` modules import
to construct feature instances. The generated code calls
``add_attribute()`` and ``add_command()`` from a generated constructor,
then exposes typed getters/setters that read and write through the
metadata-keyed attribute store kept on the feature.

Mirrors ``mash-go/pkg/model/feature.go``, ``attribute.go``, and
``command.go`` at the API level — Python is dynamically typed so the
runtime checks are minimal in this phase. Phase 13 will tighten
validation (range checks, type coercion, subscription notifications).
"""

from __future__ import annotations

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


class ModelAttributeError(Exception):
    """Raised when an attribute id is unknown or its access rule is violated.

    Distinct from Python's builtin :class:`AttributeError` so callers can
    catch model-level issues without accidentally swallowing typos.
    """


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
    """A live attribute slot: metadata + current value."""

    meta: AttributeMetadata
    value: Any = None

    def read(self) -> Any:
        return self.value

    def write(self, value: Any) -> None:
        self.value = value


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
        self._attributes: dict[int, Attribute] = {}
        self._commands: dict[int, Command] = {}

    # --- Identity ---

    @property
    def feature_type(self) -> FeatureType:
        return self._feature_type

    @property
    def revision(self) -> int:
        return self._revision

    # --- Attribute table ---

    def add_attribute(self, meta: AttributeMetadata) -> Attribute:
        attr = Attribute(meta=meta, value=meta.default)
        self._attributes[meta.id] = attr
        return attr

    def get_attribute(self, attr_id: int) -> Attribute:
        try:
            return self._attributes[attr_id]
        except KeyError as e:
            raise ModelAttributeError(f"unknown attribute id {attr_id}") from e

    def has_attribute(self, attr_id: int) -> bool:
        return attr_id in self._attributes

    def attribute_ids(self) -> list[int]:
        return sorted(self._attributes.keys())

    def read_attribute(self, attr_id: int) -> Any:
        return self.get_attribute(attr_id).read()

    def write_attribute(self, attr_id: int, value: Any) -> None:
        self.get_attribute(attr_id).write(value)

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
            raise ModelAttributeError(f"unknown command id {cmd_id}") from e

    def has_command(self, cmd_id: int) -> bool:
        return cmd_id in self._commands

    def command_ids(self) -> list[int]:
        return sorted(self._commands.keys())

    def set_command_handler(self, cmd_id: int, handler: CommandHandler | None) -> None:
        self.get_command(cmd_id).handler = handler
