"""Convenience re-exports for generated protobuf stubs.

Run `generate_proto.sh` to regenerate after proto changes.

mypy's `--strict` implies `no_implicit_reexport`: a plain `from x import y`
here does not make `y` part of this module's public API as far as mypy is
concerned, so callers importing it back out get `attr-defined` errors. The
explicit `__all__` below is what actually re-exports these names.
"""

import grpc.aio

from .generated.eebus.v1.common_pb2 import (  # noqa: F401
    DeviceRequest,
    Empty,
    LoadLimit,
    MeasurementEntry,
    PowerMeasurement,
)
from .generated.eebus.v1.device_service_pb2 import (  # noqa: F401
    CapabilityId,
    CapabilityReason,
    CapabilityState,
    DeviceCapabilities,
    DeviceEventType,
    DeviceStatus,
    RegisterSKIRequest,
    ServiceStatus,
)
from .generated.eebus.v1.device_service_pb2_grpc import DeviceServiceStub  # noqa: F401
from .generated.eebus.v1.dhw_service_pb2 import (  # noqa: F401
    DHWBoostStatus,
    DHWEventType,
    DHWSetpoint,
    DHWSystemFunctionEvent,
    DHWSystemFunctionEventType,
    DHWSystemFunctionState,
    SetDHWBoostRequest,
    SetDHWOperationModeRequest,
    SetDHWSetpointRequest,
)
from .generated.eebus.v1.dhw_service_pb2_grpc import DHWServiceStub  # noqa: F401
from .generated.eebus.v1.grid_service_pb2 import GridData  # noqa: F401
from .generated.eebus.v1.hvac_service_pb2 import (  # noqa: F401
    RoomHeatingEvent,
    RoomHeatingEventType,
    RoomHeatingSetpoint,
    RoomHeatingState,
    RoomHeatingSystemFunction,
    SetRoomHeatingModeRequest,
    SetRoomHeatingTemperatureRequest,
)
from .generated.eebus.v1.hvac_service_pb2_grpc import HVACServiceStub  # noqa: F401
from .generated.eebus.v1.grid_service_pb2_grpc import GridServiceStub  # noqa: F401
from .generated.eebus.v1.lpc_service_pb2 import (  # noqa: F401
    FailsafeLimit,
    HeartbeatStatus,
    LPCEventType,
    WriteFailsafeLimitRequest,
    WriteLoadLimitRequest,
)
from .generated.eebus.v1.monitoring_service_pb2 import (  # noqa: F401
    DeviceDiagnosticsData,
    EnergyMeasurement,
    MeasurementList,
    MeasurementEventType,
)
from .generated.eebus.v1.lpc_service_pb2_grpc import LPCServiceStub  # noqa: F401
from .generated.eebus.v1.monitoring_service_pb2_grpc import MonitoringServiceStub  # noqa: F401
from .generated.eebus.v1.visualization_service_pb2 import (  # noqa: F401
    BatteryData,
    PVData,
)
from .generated.eebus.v1.visualization_service_pb2_grpc import (  # noqa: F401
    VisualizationServiceStub,
)
from .generated.eebus.v1.ohpcf_service_pb2 import (  # noqa: F401
    CompressorFlexibility,
    CompressorPowerConsumptionState,
    ControlCompressorRequest,
    OHPCFAction,
    OHPCFEventType,
)
from .generated.eebus.v1.ohpcf_service_pb2_grpc import OHPCFServiceStub  # noqa: F401


def device_service_stub(channel: grpc.aio.Channel) -> DeviceServiceStub:
    """Build a DeviceServiceStub.

    grpcio-tools emits untyped stub classes (no mypy_grpc plugin in
    generate_proto.sh), so the constructor call is untyped from mypy's
    point of view. Contained here instead of at each of the ~20 call
    sites in coordinator.py/config_flow.py.
    """
    return DeviceServiceStub(channel)  # type: ignore[no-untyped-call]


def dhw_service_stub(channel: grpc.aio.Channel) -> DHWServiceStub:
    return DHWServiceStub(channel)  # type: ignore[no-untyped-call]


def hvac_service_stub(channel: grpc.aio.Channel) -> HVACServiceStub:
    return HVACServiceStub(channel)  # type: ignore[no-untyped-call]


def monitoring_service_stub(channel: grpc.aio.Channel) -> MonitoringServiceStub:
    return MonitoringServiceStub(channel)  # type: ignore[no-untyped-call]


def lpc_service_stub(channel: grpc.aio.Channel) -> LPCServiceStub:
    return LPCServiceStub(channel)  # type: ignore[no-untyped-call]


def ohpcf_service_stub(channel: grpc.aio.Channel) -> OHPCFServiceStub:
    return OHPCFServiceStub(channel)  # type: ignore[no-untyped-call]


def grid_service_stub(channel: grpc.aio.Channel) -> GridServiceStub:
    return GridServiceStub(channel)  # type: ignore[no-untyped-call]


def visualization_service_stub(channel: grpc.aio.Channel) -> VisualizationServiceStub:
    return VisualizationServiceStub(channel)  # type: ignore[no-untyped-call]


__all__ = [
    "BatteryData",
    "CapabilityId",
    "CapabilityReason",
    "CapabilityState",
    "CompressorFlexibility",
    "CompressorPowerConsumptionState",
    "ControlCompressorRequest",
    "DeviceEventType",
    "DeviceCapabilities",
    "DeviceStatus",
    "DeviceDiagnosticsData",
    "DeviceRequest",
    "DeviceServiceStub",
    "DHWBoostStatus",
    "DHWEventType",
    "DHWSetpoint",
    "DHWServiceStub",
    "DHWSystemFunctionEvent",
    "DHWSystemFunctionEventType",
    "DHWSystemFunctionState",
    "Empty",
    "EnergyMeasurement",
    "FailsafeLimit",
    "GridData",
    "GridServiceStub",
    "HVACServiceStub",
    "HeartbeatStatus",
    "LPCEventType",
    "LPCServiceStub",
    "LoadLimit",
    "MeasurementEntry",
    "MeasurementEventType",
    "MeasurementList",
    "MonitoringServiceStub",
    "OHPCFAction",
    "OHPCFEventType",
    "OHPCFServiceStub",
    "PVData",
    "PowerMeasurement",
    "RegisterSKIRequest",
    "RoomHeatingEvent",
    "RoomHeatingEventType",
    "RoomHeatingSetpoint",
    "RoomHeatingState",
    "RoomHeatingSystemFunction",
    "SetDHWBoostRequest",
    "SetDHWOperationModeRequest",
    "SetDHWSetpointRequest",
    "SetRoomHeatingModeRequest",
    "SetRoomHeatingTemperatureRequest",
    "ServiceStatus",
    "VisualizationServiceStub",
    "WriteFailsafeLimitRequest",
    "WriteLoadLimitRequest",
]
