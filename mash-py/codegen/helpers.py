"""Naming and type-mapping helpers for the Python code generator.

The Go generator uses an *initialism* set ("AC", "DC", "EV", "ID", "PV",
...) to keep abbreviations all-caps in Go identifiers — e.g. ``deviceId``
becomes ``DeviceID``, not ``DeviceId``. The same logic feeds the Python
output: snake_case names keep the abbreviation as a single word
(``device_id``, ``ac_active_power``), while module-level CONSTANTS
upper-case it (``DEVICE_ID``, ``AC_ACTIVE_POWER``).

Keeping the initialism set aligned with Go matters because tests compare
Python and Go symbol coverage by transformed-name parity.
"""

from __future__ import annotations

from typing import Any

from codegen.specparse import RawAttribute, RawParameter

# Same set used in mash-go/cmd/mash-featgen/generate.go:knownInitialisms.
INITIALISMS: frozenset[str] = frozenset(
    {
        "AC",
        "DC",
        "EV",
        "ID",
        "PV",
        "AB",
        "BC",
        "CA",
        "EVSE",
        "EVCC",
        "PCID",
        "MAC",
        "EUI",
        "RFID",
        "VIN",
        "HVAC",
    }
)


def _split_camel(s: str) -> list[str]:
    """Split a camelCase identifier on internal capital letters.

    ``acActivePower`` -> ``["ac", "Active", "Power"]``. Empty input
    returns an empty list. Already-uppercase runs aren't split apart
    (we treat them as a single token), since the spec uses lowerCamelCase
    consistently and the only uppercase runs we'll see are deliberate
    initialisms inside individual words.
    """
    if not s:
        return []
    words: list[str] = []
    start = 0
    for i in range(1, len(s)):
        if s[i].isupper():
            words.append(s[start:i])
            start = i
    words.append(s[start:])
    return words


def snake_case(name: str) -> str:
    """``acActivePower`` -> ``ac_active_power`` (lowercased, `_` separated).

    Initialism-aware via :func:`_split_camel` then ``.lower()`` per word.
    """
    return "_".join(w.lower() for w in _split_camel(name))


def upper_snake(name: str) -> str:
    """``acActivePower`` -> ``AC_ACTIVE_POWER``."""
    return "_".join(w.upper() for w in _split_camel(name))


def screaming_to_member(name: str) -> str:
    """``PLUGGED_IN_NO_DEMAND`` -> ``PLUGGED_IN_NO_DEMAND`` (no-op for now).

    Python ``IntEnum`` members are conventionally all-caps with
    underscores, which is exactly the YAML form already, so this is a
    pass-through. Kept as a named function so the templates can express
    intent at call sites.
    """
    return name


def class_case(name: str) -> str:
    """Pass-through for already-PascalCase YAML names (``ChargingState``).

    Sanity-checks that the first character is uppercase; raises a clean
    error if the YAML drifts. Kept separate from :func:`snake_case` so a
    template can fail loudly rather than silently mangling a PascalCase
    type name.
    """
    if not name:
        return ""
    if not name[0].isupper():
        raise ValueError(f"expected PascalCase identifier, got {name!r}")
    return name


# Module-level attribute / command / parameter / response ID constants
# follow ``<FEATURE_UPPER>_<SLOT>_<NAME_UPPER>`` (Go uses the analogous
# ``MeasurementAttrACActivePower``).


def attribute_const(feature_name: str, attr_name: str) -> str:
    return f"{upper_snake(feature_name)}_ATTR_{upper_snake(attr_name)}"


def command_const(feature_name: str, cmd_name: str) -> str:
    return f"{upper_snake(feature_name)}_CMD_{upper_snake(cmd_name)}"


def feature_revision_const(feature_name: str) -> str:
    return f"{upper_snake(feature_name)}_FEATURE_REVISION"


def param_const(cmd_name: str, param_name: str) -> str:
    return f"{upper_snake(cmd_name)}_PARAM_{upper_snake(param_name)}"


def resp_const(cmd_name: str, field_name: str) -> str:
    return f"{upper_snake(cmd_name)}_RESP_{upper_snake(field_name)}"


# ---------------------------------------------------------------------------
# Type mapping
# ---------------------------------------------------------------------------


# Mapping from YAML primitive type -> (Python annotation, runtime type)
# used by getters/setters and request/response dataclasses. ``int`` is
# the home for every integer width since Python ints are arbitrary
# precision; the ``DataType`` enum still records the spec-level width
# for the runtime to enforce later (Phase 13).
_PRIMITIVE_PY_TYPE: dict[str, str] = {
    "uint8": "int",
    "uint16": "int",
    "uint32": "int",
    "uint64": "int",
    "int8": "int",
    "int16": "int",
    "int32": "int",
    "int64": "int",
    "float32": "float",
    "float64": "float",
    "bool": "bool",
    "string": "str",
    "bytes": "bytes",
}


_DATA_TYPE_CONST: dict[str, str] = {
    "uint8": "DataType.UINT8",
    "uint16": "DataType.UINT16",
    "uint32": "DataType.UINT32",
    "uint64": "DataType.UINT64",
    "int8": "DataType.INT8",
    "int16": "DataType.INT16",
    "int32": "DataType.INT32",
    "int64": "DataType.INT64",
    "float32": "DataType.FLOAT32",
    "float64": "DataType.FLOAT64",
    "bool": "DataType.BOOL",
    "string": "DataType.STRING",
    "bytes": "DataType.BYTES",
    "array": "DataType.ARRAY",
    "map": "DataType.MAP",
}


def data_type_const(yaml_type: str) -> str:
    """``int64`` -> ``DataType.INT64``; unknown -> ``DataType.UNKNOWN``."""
    return _DATA_TYPE_CONST.get(yaml_type, "DataType.UNKNOWN")


def access_const(access: str) -> str:
    return {
        "readOnly": "Access.READ_ONLY",
        "readWrite": "Access.READ_WRITE",
        "read": "Access.READ",
    }.get(access, "Access.READ_ONLY")


def py_attr_type(attr: RawAttribute) -> str:
    """Python type annotation for an attribute getter return value.

    The shape is:
    - enum-typed (``enum: ...``)            -> ``EnumName``
    - map (``type: map``)                    -> ``dict[KeyType, ValueType]``
    - array of objects                       -> ``list[StructName]``
    - array of enums                         -> ``list[EnumName]``
    - array of primitives                    -> ``list[PyType]``
    - primitive                              -> ``int|str|bool|float|bytes``
    Nullable wrapping is the caller's concern.
    """
    if attr.enum:
        return attr.enum
    if attr.type == "map":
        key = attr.mapKeyType or "Any"
        val = _PRIMITIVE_PY_TYPE.get(attr.mapValueType, attr.mapValueType or "Any")
        return f"dict[{key}, {val}]"
    if attr.type == "array":
        if attr.items is None:
            return "list[Any]"
        if attr.items.enum:
            return f"list[{attr.items.enum}]"
        if attr.items.type == "object":
            return f"list[{attr.items.structName}]"
        return f"list[{_PRIMITIVE_PY_TYPE.get(attr.items.type, 'Any')}]"
    return _PRIMITIVE_PY_TYPE.get(attr.type, "Any")


def py_param_type(p: RawParameter) -> str:
    """Python type annotation for a command parameter / response field."""
    if p.enum:
        return p.enum
    if p.type == "map":
        # Command parameters use bare ``map`` (string-keyed in tests).
        return "dict[str, Any]"
    if p.type == "array":
        if p.items is None:
            return "list[Any]"
        if p.items.enum:
            return f"list[{p.items.enum}]"
        if p.items.type == "object":
            return f"list[{p.items.structName}]"
        return f"list[{_PRIMITIVE_PY_TYPE.get(p.items.type, 'Any')}]"
    return _PRIMITIVE_PY_TYPE.get(p.type, "Any")


def py_zero_value(yaml_type: str) -> str:
    return {
        "uint8": "0",
        "uint16": "0",
        "uint32": "0",
        "uint64": "0",
        "int8": "0",
        "int16": "0",
        "int32": "0",
        "int64": "0",
        "float32": "0.0",
        "float64": "0.0",
        "bool": "False",
        "string": '""',
        "bytes": "b''",
        "array": "[]",
        "map": "{}",
    }.get(yaml_type, "None")


def py_literal(yaml_type: str, value: Any) -> str:
    """Render a YAML default value as a Python literal."""
    if value is None:
        return "None"
    if yaml_type == "bool":
        return "True" if value else "False"
    if yaml_type in {"string"}:
        return repr(str(value))
    if yaml_type in {"float32", "float64"}:
        return repr(float(value))
    if yaml_type.startswith(("int", "uint")):
        return repr(int(value))
    if yaml_type == "bytes":
        if isinstance(value, (bytes, bytearray)):
            return repr(bytes(value))
        return repr(value)
    return repr(value)


def enum_default_member(enum_name: str, value: Any, enum_values: list[str]) -> str:
    """Resolve an enum default to ``EnumName.MEMBER``.

    ``enum_values`` is the list of declared member names for the enum;
    if the YAML default doesn't match, fall back to the first declared
    member (mirrors Go's :func:`enumDefaultConst`).
    """
    if isinstance(value, str) and value in enum_values:
        return f"{enum_name}.{value}"
    if enum_values:
        return f"{enum_name}.{enum_values[0]}"
    return "0"


# ---------------------------------------------------------------------------
# Module-name helpers
# ---------------------------------------------------------------------------


def feature_module_name(feature_name: str) -> str:
    """``DeviceInfo`` -> ``device_info`` (Python file stem)."""
    return snake_case(feature_name)


def needs_typing_any(features: list[RawAttribute]) -> bool:
    """True when at least one attribute would render ``Any`` in its type."""
    for attr in features:
        if attr.type in ("array", "map") and not attr.enum:
            return True
        if attr.type and attr.type not in _PRIMITIVE_PY_TYPE and not attr.enum:
            return True
    return False
