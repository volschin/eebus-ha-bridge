"""Convenience re-exports for generated protobuf stubs.

Run `generate_proto.sh` to regenerate after proto changes.

mypy's `--strict` implies `no_implicit_reexport`: a plain `from x import y`
here does not make `y` part of this module's public API as far as mypy is
concerned, so callers importing it back out get `attr-defined` errors. The
explicit `__all__` below is what actually re-exports these names.
"""

import grpc.aio

from .generated.eebus.v1.common_pb2 import (
    DeviceRequest,
    Empty,
    LoadLimit,
    MeasurementEntry,
    MeasurementId,
    PowerMeasurement,
    ProviderSampleMeta,
)
from .generated.eebus.v1.device_service_pb2 import (
    CapabilityId,
    CapabilityReason,
    CapabilityState,
    DeviceCapabilities,
    DeviceEventType,
    DeviceOperationalDiagnostics,
    DeviceReadinessState,
    DeviceSnapshot,
    DeviceStateDHWEvent,
    DeviceStateEvent,
    DeviceStatus,
    EventAvailability,
    EventTransportDiagnostics,
    FeatureId,
    ProviderSampleDiagnostics,
    ProviderSampleState,
    RecoveryDiagnostics,
    RegisterSKIRequest,
    ResyncReason,
    ResyncRequired,
    ServerInfo,
    ServiceStatus,
    SnapshotFieldId,
    SnapshotFieldStatus,
    SnapshotReadDiagnostics,
    SnapshotValueState,
)
from .generated.eebus.v1.device_service_pb2_grpc import DeviceServiceStub
from .generated.eebus.v1.dhw_service_pb2 import (
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
from .generated.eebus.v1.dhw_service_pb2_grpc import DHWServiceStub
from .generated.eebus.v1.grid_service_pb2 import GridData
from .generated.eebus.v1.grid_service_pb2_grpc import GridServiceStub
from .generated.eebus.v1.hvac_service_pb2 import (
    RoomHeatingEvent,
    RoomHeatingEventType,
    RoomHeatingSetpoint,
    RoomHeatingState,
    RoomHeatingSystemFunction,
    SetRoomHeatingModeRequest,
    SetRoomHeatingTemperatureRequest,
)
from .generated.eebus.v1.hvac_service_pb2_grpc import HVACServiceStub
from .generated.eebus.v1.lpc_service_pb2 import (
    FailsafeLimit,
    HeartbeatStatus,
    LPCEventType,
    WriteFailsafeLimitRequest,
    WriteLoadLimitRequest,
)
from .generated.eebus.v1.lpc_service_pb2_grpc import LPCServiceStub
from .generated.eebus.v1.monitoring_service_pb2 import (
    DeviceDiagnosticsData,
    EnergyMeasurement,
    MeasurementEventType,
    MeasurementList,
    MeasurementUpdateField,
)
from .generated.eebus.v1.monitoring_service_pb2_grpc import MonitoringServiceStub
from .generated.eebus.v1.ohpcf_service_pb2 import (
    CompressorFlexibility,
    CompressorPowerConsumptionState,
    ControlCompressorRequest,
    OHPCFAction,
    OHPCFEventType,
    OHPCFUpdateField,
)
from .generated.eebus.v1.ohpcf_service_pb2_grpc import OHPCFServiceStub
from .generated.eebus.v1.visualization_service_pb2 import (
    BatteryData,
    PVData,
    PVPeakPowerData,
)
from .generated.eebus.v1.visualization_service_pb2_grpc import (
    VisualizationServiceStub,
)


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
    "DHWBoostStatus",
    "DHWEventType",
    "DHWServiceStub",
    "DHWSetpoint",
    "DHWSystemFunctionEvent",
    "DHWSystemFunctionEventType",
    "DHWSystemFunctionState",
    "DeviceCapabilities",
    "DeviceDiagnosticsData",
    "DeviceEventType",
    "DeviceOperationalDiagnostics",
    "DeviceReadinessState",
    "DeviceRequest",
    "DeviceServiceStub",
    "DeviceSnapshot",
    "DeviceStateDHWEvent",
    "DeviceStateEvent",
    "DeviceStatus",
    "Empty",
    "EnergyMeasurement",
    "EventAvailability",
    "EventTransportDiagnostics",
    "FailsafeLimit",
    "FeatureId",
    "GridData",
    "GridServiceStub",
    "HVACServiceStub",
    "HeartbeatStatus",
    "LPCEventType",
    "LPCServiceStub",
    "LoadLimit",
    "MeasurementEntry",
    "MeasurementEventType",
    "MeasurementId",
    "MeasurementList",
    "MeasurementUpdateField",
    "MonitoringServiceStub",
    "OHPCFAction",
    "OHPCFEventType",
    "OHPCFServiceStub",
    "OHPCFUpdateField",
    "PVData",
    "PVPeakPowerData",
    "PowerMeasurement",
    "ProviderSampleDiagnostics",
    "ProviderSampleMeta",
    "ProviderSampleState",
    "RecoveryDiagnostics",
    "RegisterSKIRequest",
    "ResyncReason",
    "ResyncRequired",
    "RoomHeatingEvent",
    "RoomHeatingEventType",
    "RoomHeatingSetpoint",
    "RoomHeatingState",
    "RoomHeatingSystemFunction",
    "ServerInfo",
    "ServiceStatus",
    "SetDHWBoostRequest",
    "SetDHWOperationModeRequest",
    "SetDHWSetpointRequest",
    "SetRoomHeatingModeRequest",
    "SetRoomHeatingTemperatureRequest",
    "SnapshotFieldId",
    "SnapshotFieldStatus",
    "SnapshotReadDiagnostics",
    "SnapshotValueState",
    "VisualizationServiceStub",
    "WriteFailsafeLimitRequest",
    "WriteLoadLimitRequest",
    "device_service_stub",
    "dhw_service_stub",
    "grid_service_stub",
    "hvac_service_stub",
    "lpc_service_stub",
    "monitoring_service_stub",
    "ohpcf_service_stub",
    "visualization_service_stub",
]
