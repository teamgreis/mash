"""YAML loaders for the MASH spec.

Mirrors ``mash-go/internal/specparse`` at the data-shape level. The
loaders return plain dataclasses (not dicts) so the generator and tests
both type-check the structure they're walking.

The dataclass field names match what the YAML declares (camelCase
preserved for ``mapKeyType`` / ``structName`` etc.) — these names are
spec terms, not Python style choices, and the generator uses them to
build identifiers.
"""

from __future__ import annotations

import os
import re
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import yaml

# ---------------------------------------------------------------------------
# Feature definitions
# ---------------------------------------------------------------------------


@dataclass(slots=True)
class RawArrayField:
    """A field within an array-of-objects element."""

    name: str
    type: str
    enum: str = ""


@dataclass(slots=True)
class RawArrayItems:
    """Description of array element shape (for ``type: array`` attributes)."""

    type: str = ""
    enum: str = ""
    structName: str = ""
    fields: list[RawArrayField] = field(default_factory=list)


@dataclass(slots=True)
class RawAttribute:
    id: int
    name: str
    type: str
    enum: str = ""
    mapKeyType: str = ""
    mapValueType: str = ""
    items: RawArrayItems | None = None
    access: str = "readOnly"
    mandatory: bool = False
    nullable: bool = False
    default: Any = None
    min: Any = None
    max: Any = None
    unit: str = ""
    description: str = ""


@dataclass(slots=True)
class RawParameter:
    """Command parameter or response field."""

    id: int
    name: str
    type: str
    enum: str = ""
    required: bool = False
    description: str = ""
    items: RawArrayItems | None = None


@dataclass(slots=True)
class RawCommand:
    id: int
    name: str
    mandatory: bool = False
    description: str = ""
    parameters: list[RawParameter] = field(default_factory=list)
    response: list[RawParameter] = field(default_factory=list)


@dataclass(slots=True)
class RawEnumValue:
    name: str
    value: int
    description: str = ""


@dataclass(slots=True)
class RawEnum:
    name: str
    type: str = "uint8"
    description: str = ""
    values: list[RawEnumValue] = field(default_factory=list)


@dataclass(slots=True)
class RawFeature:
    name: str
    id: int
    revision: int = 1
    mandatory: bool = False
    description: str = ""
    enums: list[RawEnum] = field(default_factory=list)
    attributes: list[RawAttribute] = field(default_factory=list)
    commands: list[RawCommand] = field(default_factory=list)


# ---------------------------------------------------------------------------
# Protocol versions / shared / use case registries
# ---------------------------------------------------------------------------


@dataclass(slots=True)
class RawModelType:
    """Entry in ``feature_types``, ``endpoint_types``, or ``use_case_types``."""

    name: str
    id: int
    description: str = ""


@dataclass(slots=True)
class RawProtocolVersion:
    description: str = ""
    features: dict[str, str] = field(default_factory=dict)
    shared: str = "1.0"
    feature_types: list[RawModelType] = field(default_factory=list)
    endpoint_types: list[RawModelType] = field(default_factory=list)
    use_case_types: list[RawModelType] = field(default_factory=list)


@dataclass(slots=True)
class RawShared:
    version: str = "1.0"
    enums: list[RawEnum] = field(default_factory=list)


# ---------------------------------------------------------------------------
# Use case definitions
# ---------------------------------------------------------------------------


@dataclass(slots=True)
class RawUseCaseAttrReq:
    name: str
    requiredValue: bool | None = None


@dataclass(slots=True)
class RawUseCaseFeatureReq:
    feature: str
    required: bool = False
    attributes: list[RawUseCaseAttrReq] = field(default_factory=list)
    commands: list[str] = field(default_factory=list)
    subscribe: str = ""  # "all" or empty


@dataclass(slots=True)
class RawUseCaseScenario:
    bit: int
    name: str
    description: str = ""
    requires: list[str] = field(default_factory=list)
    requiresAny: list[str] = field(default_factory=list)
    endpointTypes: list[str] = field(default_factory=list)
    features: list[RawUseCaseFeatureReq] = field(default_factory=list)


@dataclass(slots=True)
class RawUseCase:
    name: str
    id: int
    fullName: str = ""
    specVersion: str = "1.0"
    major: int = 1
    minor: int = 0
    description: str = ""
    endpointTypes: list[str] = field(default_factory=list)
    scenarios: list[RawUseCaseScenario] = field(default_factory=list)
    commands: list[str] = field(default_factory=list)


# ---------------------------------------------------------------------------
# Loaders
# ---------------------------------------------------------------------------


class _Yaml12SafeLoader(yaml.SafeLoader):
    """SafeLoader with YAML 1.2 boolean rules.

    pyyaml ships YAML 1.1 by default, which coerces 22 spellings of
    yes/no/on/off/y/n/true/false to bool. Go's ``yaml.v3`` follows YAML
    1.2, where only ``true`` / ``false`` are booleans. mash-go and the
    spec docs agree on the 1.2 reading; we mirror that here so an enum
    value declared as ``name: OFF`` reads as the string ``"OFF"`` instead
    of being silently turned into ``False``.
    """


# Drop the inherited bool resolver entirely, then add a YAML-1.2-shaped one
# (matches ``true`` and ``false`` only). Pulling the implicit-resolver table
# apart this way keeps the subclass independent of pyyaml's internal layout.
_Yaml12SafeLoader.yaml_implicit_resolvers = {
    key: [(tag, regex) for tag, regex in resolvers if tag != "tag:yaml.org,2002:bool"]
    for key, resolvers in yaml.SafeLoader.yaml_implicit_resolvers.items()
}
_Yaml12SafeLoader.add_implicit_resolver(
    "tag:yaml.org,2002:bool",
    re.compile(r"^(?:true|false)$"),
    list("tf"),
)


def _load_yaml(path: Path | str) -> Any:
    with open(path, encoding="utf-8") as f:
        return yaml.load(f, Loader=_Yaml12SafeLoader)


def _array_items(raw: dict[str, Any] | None) -> RawArrayItems | None:
    if not raw:
        return None
    fields_raw = raw.get("fields") or []
    return RawArrayItems(
        type=raw.get("type", ""),
        enum=raw.get("enum", ""),
        structName=raw.get("structName", ""),
        fields=[
            RawArrayField(
                name=f.get("name", ""),
                type=f.get("type", ""),
                enum=f.get("enum", ""),
            )
            for f in fields_raw
        ],
    )


def parse_feature(data: dict[str, Any]) -> RawFeature:
    """Build a :class:`RawFeature` from a parsed YAML dict."""
    if not data.get("name"):
        raise ValueError("feature definition missing 'name'")

    enums = [
        RawEnum(
            name=e["name"],
            type=e.get("type", "uint8"),
            description=e.get("description", ""),
            values=[
                RawEnumValue(
                    name=v["name"],
                    value=int(v["value"]),
                    description=v.get("description", ""),
                )
                for v in e.get("values", [])
            ],
        )
        for e in data.get("enums") or []
    ]

    attrs = [
        RawAttribute(
            id=int(a["id"]),
            name=a["name"],
            type=a.get("type", ""),
            enum=a.get("enum", ""),
            mapKeyType=a.get("mapKeyType", ""),
            mapValueType=a.get("mapValueType", ""),
            items=_array_items(a.get("items")),
            access=a.get("access", "readOnly"),
            mandatory=bool(a.get("mandatory", False)),
            nullable=bool(a.get("nullable", False)),
            default=a.get("default"),
            min=a.get("min"),
            max=a.get("max"),
            unit=a.get("unit", ""),
            description=a.get("description", ""),
        )
        for a in data.get("attributes") or []
    ]

    commands = [
        RawCommand(
            id=int(c["id"]),
            name=c["name"],
            mandatory=bool(c.get("mandatory", False)),
            description=c.get("description", ""),
            parameters=[_parse_param(p) for p in c.get("parameters") or []],
            response=[_parse_param(p) for p in c.get("response") or []],
        )
        for c in data.get("commands") or []
    ]

    return RawFeature(
        name=data["name"],
        id=int(data["id"]),
        revision=int(data.get("revision", 1)),
        mandatory=bool(data.get("mandatory", False)),
        description=data.get("description", ""),
        enums=enums,
        attributes=attrs,
        commands=commands,
    )


def _parse_param(raw: dict[str, Any]) -> RawParameter:
    return RawParameter(
        id=int(raw["id"]),
        name=raw["name"],
        type=raw.get("type", ""),
        enum=raw.get("enum", ""),
        required=bool(raw.get("required", False)),
        description=raw.get("description", ""),
        items=_array_items(raw.get("items")),
    )


def load_feature(path: Path | str) -> RawFeature:
    return parse_feature(_load_yaml(path))


def load_shared(path: Path | str) -> RawShared:
    data = _load_yaml(path) or {}
    return RawShared(
        version=str(data.get("version", "1.0")),
        enums=[
            RawEnum(
                name=e["name"],
                type=e.get("type", "uint8"),
                description=e.get("description", ""),
                values=[
                    RawEnumValue(
                        name=v["name"],
                        value=int(v["value"]),
                        description=v.get("description", ""),
                    )
                    for v in e.get("values", [])
                ],
            )
            for e in data.get("enums") or []
        ],
    )


def load_protocol_version(path: Path | str, version: str = "1.0") -> RawProtocolVersion:
    data = _load_yaml(path) or {}
    versions = data.get("versions") or {}
    if version not in versions:
        raise KeyError(
            f"protocol version {version!r} not found in {path}; "
            f"available: {sorted(versions.keys())}"
        )
    raw = versions[version]
    return RawProtocolVersion(
        description=raw.get("description", ""),
        features=dict(raw.get("features") or {}),
        shared=str(raw.get("shared", "1.0")),
        feature_types=[
            RawModelType(name=t["name"], id=int(t["id"]), description=t.get("description", ""))
            for t in raw.get("feature_types") or []
        ],
        endpoint_types=[
            RawModelType(name=t["name"], id=int(t["id"]), description=t.get("description", ""))
            for t in raw.get("endpoint_types") or []
        ],
        use_case_types=[
            RawModelType(name=t["name"], id=int(t["id"]), description=t.get("description", ""))
            for t in raw.get("use_case_types") or []
        ],
    )


def load_use_case(path: Path | str) -> RawUseCase:
    data = _load_yaml(path) or {}
    scenarios = []
    for s in data.get("scenarios") or []:
        feats = []
        for f in s.get("features") or []:
            attrs = [
                RawUseCaseAttrReq(
                    name=a["name"],
                    requiredValue=a.get("requiredValue"),
                )
                for a in f.get("attributes") or []
            ]
            feats.append(
                RawUseCaseFeatureReq(
                    feature=f["feature"],
                    required=bool(f.get("required", False)),
                    attributes=attrs,
                    commands=list(f.get("commands") or []),
                    subscribe=str(f.get("subscribe", "")),
                )
            )
        scenarios.append(
            RawUseCaseScenario(
                bit=int(s["bit"]),
                name=s["name"],
                description=s.get("description", ""),
                requires=list(s.get("requires") or []),
                requiresAny=list(s.get("requiresAny") or []),
                endpointTypes=list(s.get("endpointTypes") or []),
                features=feats,
            )
        )
    return RawUseCase(
        name=data["name"],
        id=int(data["id"]),
        fullName=data.get("fullName", ""),
        specVersion=str(data.get("specVersion", "1.0")),
        major=int(data.get("major", 1)),
        minor=int(data.get("minor", 0)),
        description=str(data.get("description", "")).strip(),
        endpointTypes=list(data.get("endpointTypes") or []),
        scenarios=scenarios,
        commands=list(data.get("commands") or []),
    )


# ---------------------------------------------------------------------------
# Convenience: load every feature listed in protocol-versions.yaml
# ---------------------------------------------------------------------------


def feature_dir_name(feature_name: str) -> str:
    """``DeviceInfo`` -> ``device-info`` (matches the ``docs/features/`` layout)."""
    out = []
    for i, ch in enumerate(feature_name):
        if ch.isupper() and i > 0:
            out.append("-")
        out.append(ch.lower())
    return "".join(out)


def load_all_features(features_dir: Path | str, protocol: RawProtocolVersion) -> list[RawFeature]:
    """Load every feature referenced from the protocol version registry.

    Iterates ``protocol.feature_types`` (the canonical declaration order
    from ``protocol-versions.yaml``) rather than the ``features`` map,
    matching the determinism fix that mash-go landed in commit 9b45850.
    """
    base = Path(features_dir)
    out: list[RawFeature] = []
    for ft in protocol.feature_types:
        version = protocol.features.get(ft.name)
        if version is None:
            continue
        path = base / feature_dir_name(ft.name) / f"{version}.yaml"
        out.append(load_feature(path))
    return out


def load_all_use_cases(usecases_dir: Path | str) -> list[RawUseCase]:
    """Load every ``*.yaml`` under ``usecases_dir``, sorted by use case ID."""
    base = Path(usecases_dir)
    out: list[RawUseCase] = []
    for entry in sorted(os.listdir(base)):
        if not entry.endswith(".yaml"):
            continue
        out.append(load_use_case(base / entry))
    out.sort(key=lambda u: u.id)
    return out


def validate_feature_ids(types: list[RawModelType], features: list[RawFeature]) -> None:
    """Check every loaded feature has a registry entry with the same ID.

    Raises :class:`ValueError` if any mismatch is found. Mirrors the
    Go-side validation in ``cmd/mash-featgen/generate.go``.
    """
    registry = {t.name: t.id for t in types}
    errors: list[str] = []
    for f in features:
        reg_id = registry.get(f.name)
        if reg_id is None:
            errors.append(f"feature {f.name!r} loaded but missing from feature_types registry")
            continue
        if reg_id != f.id:
            errors.append(
                f"feature {f.name!r}: YAML id=0x{f.id:02X} but registry id=0x{reg_id:02X}"
            )
    if errors:
        raise ValueError("feature ID validation errors:\n  " + "\n  ".join(errors))
