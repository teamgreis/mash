"""Service-layer orchestration for MASH (DeviceService, ControllerService,
listeners). Phase 4 introduces only :class:`TwoPortListener`; Phase 10
adds the controller-side post-commissioning operational flow; later
phases fill in device service and the full ``InteractionClient``."""

from __future__ import annotations

from mash_py.service.controller import (
    DEVICE_INFO_ENDPOINT_ID,
    DEVICE_INFO_FEATURE_ID,
    DeviceInfoReadError,
    DeviceInfoSnapshot,
    OperationalConnectionError,
    OperationalHandshakeError,
    OperationalProtocolError,
    open_operational_connection,
    read_device_info,
    reconnect_and_read_device_info,
)
from mash_py.service.device import (
    DeviceService,
    DeviceServiceConfig,
)
from mash_py.service.listeners import (
    ConnectionCapExceededError,
    ListenerConfig,
    SharedConnectionCap,
    TwoPortListener,
    derive_connection_cap,
)

__all__ = [
    "DEVICE_INFO_ENDPOINT_ID",
    "DEVICE_INFO_FEATURE_ID",
    "ConnectionCapExceededError",
    "DeviceInfoReadError",
    "DeviceInfoSnapshot",
    "DeviceService",
    "DeviceServiceConfig",
    "ListenerConfig",
    "OperationalConnectionError",
    "OperationalHandshakeError",
    "OperationalProtocolError",
    "SharedConnectionCap",
    "TwoPortListener",
    "derive_connection_cap",
    "open_operational_connection",
    "read_device_info",
    "reconnect_and_read_device_info",
]
