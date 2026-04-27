"""Endpoint: a functional unit within a device.

An endpoint owns a set of features keyed by ``FeatureType`` and carries
its own type (e.g. ``EV_CHARGER``, ``BATTERY``) and label. The
operational server addresses every Read / Write / Subscribe / Invoke
through ``(endpointId, featureId, attribute_or_command)``.

Mirrors ``mash-go/pkg/model/endpoint.go`` at the API level.
"""

from __future__ import annotations

from typing import Any

from mash_py.model.endpoint_type_gen import EndpointType
from mash_py.model.feature import (
    AttributeNotFoundError,
    Feature,
)
from mash_py.model.feature_type_gen import FeatureType


class EndpointError(Exception):
    """Base class for endpoint-level errors."""


class FeatureNotFoundError(EndpointError):
    """Raised when a feature is not present on an endpoint."""


class DuplicateFeatureError(EndpointError):
    """Raised when adding a feature whose type is already on this endpoint."""


_ENDPOINT_ID_MAX = 0xFF  # endpointId is uint8 per docs/interaction-model.md §3.1


class Endpoint:
    """A functional unit within a device.

    Endpoint 0 is reserved for ``DEVICE_ROOT`` (carries the
    ``DeviceInfo`` feature); endpoints 1..255 are functional. The id
    range mirrors the ``uint8`` field in the request/response wire shape.
    """

    def __init__(
        self,
        endpoint_id: int,
        endpoint_type: EndpointType,
        label: str = "",
    ) -> None:
        if not 0 <= endpoint_id <= _ENDPOINT_ID_MAX:
            raise ValueError(
                f"endpoint id {endpoint_id} out of uint8 range [0, 255]"
            )
        self._id = endpoint_id
        self._endpoint_type = endpoint_type
        self._label = label
        self._features: dict[FeatureType, Feature] = {}

    # --- Identity ---

    @property
    def id(self) -> int:
        return self._id

    @property
    def endpoint_type(self) -> EndpointType:
        return self._endpoint_type

    @property
    def label(self) -> str:
        return self._label

    def set_label(self, label: str) -> None:
        self._label = label

    # --- Feature table ---

    def add_feature(self, feature: Feature) -> None:
        ft = feature.feature_type
        if ft in self._features:
            raise DuplicateFeatureError(
                f"feature {ft.name} already present on endpoint {self._id}"
            )
        self._features[ft] = feature

    def get_feature(self, feature_type: FeatureType) -> Feature:
        try:
            return self._features[feature_type]
        except KeyError as e:
            raise FeatureNotFoundError(
                f"feature {feature_type.name} not on endpoint {self._id}"
            ) from e

    def get_feature_by_id(self, feature_id: int) -> Feature:
        return self.get_feature(FeatureType(feature_id))

    def has_feature(self, feature_type: FeatureType) -> bool:
        return feature_type in self._features

    def features(self) -> list[Feature]:
        return list(self._features.values())

    def feature_types(self) -> list[FeatureType]:
        """Sorted by integer feature-type id (matches the wire ordering)."""
        return sorted(self._features.keys(), key=int)

    # --- Attribute / command pass-through ---

    def read_attribute(self, feature_id: int, attr_id: int) -> Any:
        return self.get_feature_by_id(feature_id).read_attribute(attr_id)

    def write_attribute(self, feature_id: int, attr_id: int, value: Any) -> None:
        self.get_feature_by_id(feature_id).write_attribute(attr_id, value)

    def has_attribute(self, feature_id: int, attr_id: int) -> bool:
        try:
            return self.get_feature_by_id(feature_id).has_attribute(attr_id)
        except (FeatureNotFoundError, AttributeNotFoundError, ValueError):
            return False
