"""Device model: endpoints, features, attributes, commands.

Phase 12 ships the minimum runtime that generated feature modules need to
import: a :class:`Feature` base class, attribute/command metadata holders,
and the ``DataType`` / ``Access`` enums referenced from generated
constructors. Phase 13 will extend this with endpoint hierarchy, value
validation, subscription bookkeeping, and the read/write hooks the
operational server needs.

The generated ``*_gen.py`` files in ``mash_py.features`` and the type
enums in ``mash_py.model`` (``feature_type_gen.py``, ``endpoint_type_gen.py``,
``use_case_type_gen.py``, ``shared_gen.py``) are produced from the YAML
spec by ``codegen``; do not edit them by hand.
"""

from __future__ import annotations

from mash_py.model.device import Device
from mash_py.model.endpoint import Endpoint
from mash_py.model.endpoint_type_gen import EndpointType
from mash_py.model.feature import (
    Attribute,
    AttributeMetadata,
    AttributeNotFoundError,
    AttributeNotNullableError,
    AttributeNotWritableError,
    AttributeObserver,
    AttributeOutOfRangeError,
    AttributeValueTypeError,
    Command,
    CommandHandler,
    CommandMetadata,
    CommandNotFoundError,
    Feature,
    ModelAttributeError,
    ParameterMetadata,
)
from mash_py.model.feature_type_gen import FeatureType
from mash_py.model.shared_gen import (
    AsymmetricSupport,
    Direction,
    GridPhase,
    Phase,
    PhasePair,
)
from mash_py.model.types import Access, DataType
from mash_py.model.use_case_type_gen import UseCaseType

__all__ = [
    "Access",
    "AsymmetricSupport",
    "Attribute",
    "AttributeMetadata",
    "AttributeNotFoundError",
    "AttributeNotNullableError",
    "AttributeNotWritableError",
    "AttributeObserver",
    "AttributeOutOfRangeError",
    "AttributeValueTypeError",
    "Command",
    "CommandHandler",
    "CommandMetadata",
    "CommandNotFoundError",
    "DataType",
    "Device",
    "Direction",
    "Endpoint",
    "EndpointType",
    "Feature",
    "FeatureType",
    "GridPhase",
    "ModelAttributeError",
    "ParameterMetadata",
    "Phase",
    "PhasePair",
    "UseCaseType",
]
