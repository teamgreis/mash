"""Use case definition types — hand-written counterpart to mash-go's
``pkg/usecase/types.go``.

The generated :mod:`mash_py.usecase.definitions_gen` instantiates these
dataclasses for every use case YAML in ``docs/usecases/<version>/``. The
field names are :pep:`8` snake_case (the YAML uses camelCase, but the
codegen layer translates them on the way in).
"""

from __future__ import annotations

from dataclasses import dataclass, field

from mash_py.model import UseCaseType


@dataclass(slots=True)
class AttributeRequirement:
    """An attribute a scenario requires to be present (and optionally set)."""

    name: str
    attr_id: int
    required_value: bool | None = None


@dataclass(slots=True)
class CommandRequirement:
    """A command a scenario requires the device to support."""

    name: str
    command_id: int


@dataclass(slots=True)
class FeatureRequirement:
    """A feature a scenario depends on (with attribute / command sub-requirements)."""

    feature_name: str
    feature_id: int
    required: bool = False
    attributes: list[AttributeRequirement] = field(default_factory=list)
    commands: list[CommandRequirement] = field(default_factory=list)
    subscribe_all: bool = False


@dataclass(slots=True)
class ScenarioDef:
    """One scenario within a use case (bit position in the scenario bitmap)."""

    bit: int
    name: str
    description: str = ""
    requires: list[str] = field(default_factory=list)
    requires_any: list[str] = field(default_factory=list)
    endpoint_types: list[str] = field(default_factory=list)
    features: list[FeatureRequirement] = field(default_factory=list)


@dataclass(slots=True)
class UseCaseDef:
    """Resolved description of a single use case."""

    name: UseCaseType
    id: int
    full_name: str = ""
    description: str = ""
    spec_version: str = "1.0"
    major: int = 1
    minor: int = 0
    endpoint_types: list[str] = field(default_factory=list)
    scenarios: list[ScenarioDef] = field(default_factory=list)
    commands: list[str] = field(default_factory=list)

    def all_features(self) -> list[FeatureRequirement]:
        """Union of all scenarios' feature requirements (deduped by feature name)."""
        seen: set[str] = set()
        out: list[FeatureRequirement] = []
        for s in self.scenarios:
            for f in s.features:
                if f.feature_name not in seen:
                    seen.add(f.feature_name)
                    out.append(f)
        return out

    def base_scenario(self) -> ScenarioDef | None:
        """Return the scenario with bit 0 (BASE), or ``None`` if absent."""
        for s in self.scenarios:
            if s.bit == 0:
                return s
        return None

    def defined_scenario_mask(self) -> int:
        """Bitmap with one bit set per scenario this use case declares."""
        mask = 0
        for s in self.scenarios:
            mask |= 1 << s.bit
        return mask
