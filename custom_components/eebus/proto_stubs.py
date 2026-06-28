"""Convenience re-exports for generated protobuf stubs.

Run `generate_proto.sh` to regenerate after proto changes.
"""

from .generated.eebus.v1.common_pb2 import (  # noqa: F401
    DeviceRequest,
    Empty,
    LoadLimit,
    MeasurementEntry,
    PowerMeasurement,
)
from .generated.eebus.v1.device_service_pb2 import (  # noqa: F401
    DeviceEventType,
    RegisterSKIRequest,
)
from .generated.eebus.v1.device_service_pb2_grpc import DeviceServiceStub  # noqa: F401
from .generated.eebus.v1.grid_service_pb2 import GridData  # noqa: F401
from .generated.eebus.v1.grid_service_pb2_grpc import GridServiceStub  # noqa: F401
from .generated.eebus.v1.lpc_service_pb2 import (  # noqa: F401
    LPCEventType,
    WriteFailsafeLimitRequest,
    WriteLoadLimitRequest,
)
from .generated.eebus.v1.monitoring_service_pb2 import (  # noqa: F401
    MeasurementEventType,
)
from .generated.eebus.v1.lpc_service_pb2_grpc import LPCServiceStub  # noqa: F401
from .generated.eebus.v1.monitoring_service_pb2_grpc import MonitoringServiceStub  # noqa: F401
