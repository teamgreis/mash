"""Attribute access and data-type enums.

These mirror ``mash-go/pkg/model/attribute.go`` (the ``Access`` and
``DataType`` constants). Python is dynamically typed, so the runtime
checks they enable are looser than Go's, but the metadata is still
authoritative for codegen and for the operational server's wire-side
validation.
"""

from __future__ import annotations

from enum import StrEnum


class Access(StrEnum):
    """Attribute access mode (matches the YAML ``access`` field)."""

    READ_ONLY = "readOnly"
    READ_WRITE = "readWrite"
    READ = "read"

    def is_writable(self) -> bool:
        return self is Access.READ_WRITE


class DataType(StrEnum):
    """Wire-level data type for an attribute, command parameter, or response field."""

    UINT8 = "uint8"
    UINT16 = "uint16"
    UINT32 = "uint32"
    UINT64 = "uint64"
    INT8 = "int8"
    INT16 = "int16"
    INT32 = "int32"
    INT64 = "int64"
    FLOAT32 = "float32"
    FLOAT64 = "float64"
    BOOL = "bool"
    STRING = "string"
    BYTES = "bytes"
    ARRAY = "array"
    MAP = "map"
    UNKNOWN = "unknown"

    @classmethod
    def from_yaml(cls, name: str) -> DataType:
        try:
            return cls(name)
        except ValueError:
            return cls.UNKNOWN
