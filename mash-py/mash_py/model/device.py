"""Device: the top-level container in the MASH device model.

Hierarchy: Device > Endpoint > Feature
(see ``docs/protocol-overview.md`` §4).

A :class:`Device` always owns an Endpoint 0 of type
:attr:`EndpointType.DEVICE_ROOT` (per spec §4.4). The caller composes
the rest of the model — typically by attaching the ``DeviceInfo``
feature to endpoint 0 and one or more functional endpoints (e.g.
``EV_CHARGER`` carrying ``Electrical``, ``Measurement``,
``EnergyControl``, ``Status``).

Mirrors ``mash-go/pkg/model/device.go`` at the API level.
"""

from __future__ import annotations

from typing import Any

from mash_py.model.endpoint import Endpoint
from mash_py.model.endpoint_type_gen import EndpointType
from mash_py.model.feature import Feature
from mash_py.model.feature_type_gen import FeatureType


class DeviceError(Exception):
    """Base class for device-level errors."""


class EndpointNotFoundError(DeviceError):
    """Raised when an endpoint id is not present on the device."""


class DuplicateEndpointError(DeviceError):
    """Raised when adding an endpoint whose id is already in use."""


class Device:
    """Top-level container for the device model.

    The ``device_id`` carried here is the spec ``DeviceInfo.deviceId``
    (PEN-or-fallback form per ``docs/features/device-info.md`` "Device
    ID Format"). It is *not* the certificate CN (which is a 16-hex
    SPKI-derived identifier — see Issue #9).
    """

    def __init__(self, device_id: str) -> None:
        self._device_id = device_id
        self._endpoints: dict[int, Endpoint] = {}

        # Per docs/protocol-overview.md §4: endpoint 0 is always present and
        # carries DEVICE_ROOT. Caller is responsible for attaching DeviceInfo.
        self._endpoints[0] = Endpoint(0, EndpointType.DEVICE_ROOT)

    # --- Identity ---

    @property
    def device_id(self) -> str:
        return self._device_id

    @property
    def root(self) -> Endpoint:
        """Endpoint 0 (DEVICE_ROOT). Always present."""
        return self._endpoints[0]

    # --- Endpoint table ---

    def add_endpoint(self, endpoint: Endpoint) -> None:
        if endpoint.id in self._endpoints:
            raise DuplicateEndpointError(
                f"endpoint id {endpoint.id} already present"
            )
        self._endpoints[endpoint.id] = endpoint

    def get_endpoint(self, endpoint_id: int) -> Endpoint:
        try:
            return self._endpoints[endpoint_id]
        except KeyError as e:
            raise EndpointNotFoundError(
                f"endpoint id {endpoint_id} not on device"
            ) from e

    def has_endpoint(self, endpoint_id: int) -> bool:
        return endpoint_id in self._endpoints

    def endpoints(self) -> list[Endpoint]:
        """All endpoints, ordered by id (deterministic for serialization)."""
        return [self._endpoints[i] for i in sorted(self._endpoints.keys())]

    def endpoint_ids(self) -> list[int]:
        return sorted(self._endpoints.keys())

    def endpoint_count(self) -> int:
        return len(self._endpoints)

    # --- Look-ups ---

    def find_endpoints_by_type(
        self, endpoint_type: EndpointType
    ) -> list[Endpoint]:
        return [ep for ep in self.endpoints() if ep.endpoint_type == endpoint_type]

    def find_endpoints_with_feature(
        self, feature_type: FeatureType
    ) -> list[Endpoint]:
        return [ep for ep in self.endpoints() if ep.has_feature(feature_type)]

    def get_feature(
        self, endpoint_id: int, feature_type: FeatureType
    ) -> Feature:
        return self.get_endpoint(endpoint_id).get_feature(feature_type)

    # --- Attribute pass-through ---

    def read_attribute(
        self, endpoint_id: int, feature_type: FeatureType, attr_id: int
    ) -> Any:
        return self.get_feature(endpoint_id, feature_type).read_attribute(attr_id)

    def write_attribute(
        self,
        endpoint_id: int,
        feature_type: FeatureType,
        attr_id: int,
        value: Any,
    ) -> None:
        self.get_feature(endpoint_id, feature_type).write_attribute(attr_id, value)
