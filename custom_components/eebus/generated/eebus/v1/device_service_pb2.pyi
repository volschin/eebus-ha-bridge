import datetime

from . import common_pb2 as _common_pb2
from . import dhw_service_pb2 as _dhw_service_pb2
from . import hvac_service_pb2 as _hvac_service_pb2
from . import lpc_service_pb2 as _lpc_service_pb2
from . import monitoring_service_pb2 as _monitoring_service_pb2
from . import ohpcf_service_pb2 as _ohpcf_service_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class CapabilityId(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CAPABILITY_UNSPECIFIED: _ClassVar[CapabilityId]
    CAPABILITY_MONITORING: _ClassVar[CapabilityId]
    CAPABILITY_LPC: _ClassVar[CapabilityId]
    CAPABILITY_FAILSAFE: _ClassVar[CapabilityId]
    CAPABILITY_HEARTBEAT: _ClassVar[CapabilityId]
    CAPABILITY_OHPCF: _ClassVar[CapabilityId]
    CAPABILITY_DHW: _ClassVar[CapabilityId]
    CAPABILITY_DHW_SYSTEM_FUNCTION: _ClassVar[CapabilityId]
    CAPABILITY_ROOM_HEATING: _ClassVar[CapabilityId]

class CapabilityState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CAPABILITY_STATE_UNKNOWN: _ClassVar[CapabilityState]
    CAPABILITY_STATE_AVAILABLE: _ClassVar[CapabilityState]
    CAPABILITY_STATE_TEMPORARILY_UNAVAILABLE: _ClassVar[CapabilityState]
    CAPABILITY_STATE_UNSUPPORTED: _ClassVar[CapabilityState]

class CapabilityReason(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CAPABILITY_REASON_UNSPECIFIED: _ClassVar[CapabilityReason]
    CAPABILITY_REASON_LOCAL_DISABLED: _ClassVar[CapabilityReason]
    CAPABILITY_REASON_REMOTE_NOT_ADVERTISED: _ClassVar[CapabilityReason]
    CAPABILITY_REASON_ENTITY_NOT_BOUND: _ClassVar[CapabilityReason]
    CAPABILITY_REASON_READ_FAILED: _ClassVar[CapabilityReason]
    CAPABILITY_REASON_DEVICE_DISCONNECTED: _ClassVar[CapabilityReason]

class FeatureId(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    FEATURE_UNSPECIFIED: _ClassVar[FeatureId]
    FEATURE_EXPLICIT_CAPABILITIES: _ClassVar[FeatureId]
    FEATURE_CONSOLIDATED_DEVICE_STREAM: _ClassVar[FeatureId]
    FEATURE_DEVICE_SNAPSHOT: _ClassVar[FeatureId]
    FEATURE_PROVIDER_SAMPLE_INVALIDATION: _ClassVar[FeatureId]
    FEATURE_TYPED_MEASUREMENTS: _ClassVar[FeatureId]
    FEATURE_OPERATIONAL_DIAGNOSTICS: _ClassVar[FeatureId]

class DeviceReadinessState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    DEVICE_READINESS_UNKNOWN: _ClassVar[DeviceReadinessState]
    DEVICE_READINESS_UNTRUSTED: _ClassVar[DeviceReadinessState]
    DEVICE_READINESS_DISCONNECTED: _ClassVar[DeviceReadinessState]
    DEVICE_READINESS_GRACE_PERIOD: _ClassVar[DeviceReadinessState]
    DEVICE_READINESS_RECOVERING: _ClassVar[DeviceReadinessState]
    DEVICE_READINESS_READY: _ClassVar[DeviceReadinessState]
    DEVICE_READINESS_EXHAUSTED: _ClassVar[DeviceReadinessState]

class ProviderSampleState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    PROVIDER_SAMPLE_STATE_UNSPECIFIED: _ClassVar[ProviderSampleState]
    PROVIDER_SAMPLE_STATE_EMPTY: _ClassVar[ProviderSampleState]
    PROVIDER_SAMPLE_STATE_CURRENT: _ClassVar[ProviderSampleState]
    PROVIDER_SAMPLE_STATE_EXPIRED: _ClassVar[ProviderSampleState]
    PROVIDER_SAMPLE_STATE_CLOSED: _ClassVar[ProviderSampleState]

class SnapshotValueState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    SNAPSHOT_VALUE_STATE_UNKNOWN: _ClassVar[SnapshotValueState]
    SNAPSHOT_VALUE_STATE_AVAILABLE: _ClassVar[SnapshotValueState]
    SNAPSHOT_VALUE_STATE_TEMPORARILY_UNAVAILABLE: _ClassVar[SnapshotValueState]
    SNAPSHOT_VALUE_STATE_UNSUPPORTED: _ClassVar[SnapshotValueState]

class SnapshotFieldId(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    SNAPSHOT_FIELD_UNSPECIFIED: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_CONNECTED: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_LOCAL_SKI: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_DEVICE_INFO: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_DEVICE_OPERATING_STATE: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_POWER_L1: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_POWER_L2: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_POWER_L3: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_CURRENT_L1: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_CURRENT_L2: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_CURRENT_L3: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_VOLTAGE_L1: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_VOLTAGE_L2: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_VOLTAGE_L3: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_FREQUENCY: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_ENERGY_PRODUCED: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_DHW_TEMPERATURE: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_ROOM_TEMPERATURE: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_OUTDOOR_TEMPERATURE: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_FLOW_TEMPERATURE: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_RETURN_TEMPERATURE: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_COMPRESSOR_TEMPERATURE: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_COMPRESSOR_POWER: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_POWER: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_ENERGY_CONSUMED_HEATING: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_ENERGY_CONSUMED_DHW: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_ENERGY_CONSUMED: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_CONSUMPTION_LIMIT: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_FAILSAFE_LIMIT: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_HEARTBEAT_STATUS: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_COMPRESSOR_FLEXIBILITY: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_DHW_SETPOINT: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_DHW_SYSTEM_FUNCTION: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_ROOM_HEATING_SETPOINT: _ClassVar[SnapshotFieldId]
    SNAPSHOT_FIELD_ROOM_HEATING_SYSTEM_FUNCTION: _ClassVar[SnapshotFieldId]

class EventAvailability(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    EVENT_AVAILABILITY_UNSPECIFIED: _ClassVar[EventAvailability]
    EVENT_AVAILABILITY_AVAILABLE: _ClassVar[EventAvailability]
    EVENT_AVAILABILITY_TEMPORARILY_UNAVAILABLE: _ClassVar[EventAvailability]
    EVENT_AVAILABILITY_UNSUPPORTED: _ClassVar[EventAvailability]

class ResyncReason(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    RESYNC_REASON_UNSPECIFIED: _ClassVar[ResyncReason]
    RESYNC_REASON_INITIAL_STATE_REQUIRED: _ClassVar[ResyncReason]
    RESYNC_REASON_EVENT_DROPPED: _ClassVar[ResyncReason]
    RESYNC_REASON_UNCLASSIFIED_EVENT: _ClassVar[ResyncReason]

class DeviceEventType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    DEVICE_EVENT_UNSPECIFIED: _ClassVar[DeviceEventType]
    DEVICE_EVENT_CONNECTED: _ClassVar[DeviceEventType]
    DEVICE_EVENT_DISCONNECTED: _ClassVar[DeviceEventType]
    DEVICE_EVENT_TRUST_REMOVED: _ClassVar[DeviceEventType]
    DEVICE_EVENT_PROVIDER_UPDATED: _ClassVar[DeviceEventType]
CAPABILITY_UNSPECIFIED: CapabilityId
CAPABILITY_MONITORING: CapabilityId
CAPABILITY_LPC: CapabilityId
CAPABILITY_FAILSAFE: CapabilityId
CAPABILITY_HEARTBEAT: CapabilityId
CAPABILITY_OHPCF: CapabilityId
CAPABILITY_DHW: CapabilityId
CAPABILITY_DHW_SYSTEM_FUNCTION: CapabilityId
CAPABILITY_ROOM_HEATING: CapabilityId
CAPABILITY_STATE_UNKNOWN: CapabilityState
CAPABILITY_STATE_AVAILABLE: CapabilityState
CAPABILITY_STATE_TEMPORARILY_UNAVAILABLE: CapabilityState
CAPABILITY_STATE_UNSUPPORTED: CapabilityState
CAPABILITY_REASON_UNSPECIFIED: CapabilityReason
CAPABILITY_REASON_LOCAL_DISABLED: CapabilityReason
CAPABILITY_REASON_REMOTE_NOT_ADVERTISED: CapabilityReason
CAPABILITY_REASON_ENTITY_NOT_BOUND: CapabilityReason
CAPABILITY_REASON_READ_FAILED: CapabilityReason
CAPABILITY_REASON_DEVICE_DISCONNECTED: CapabilityReason
FEATURE_UNSPECIFIED: FeatureId
FEATURE_EXPLICIT_CAPABILITIES: FeatureId
FEATURE_CONSOLIDATED_DEVICE_STREAM: FeatureId
FEATURE_DEVICE_SNAPSHOT: FeatureId
FEATURE_PROVIDER_SAMPLE_INVALIDATION: FeatureId
FEATURE_TYPED_MEASUREMENTS: FeatureId
FEATURE_OPERATIONAL_DIAGNOSTICS: FeatureId
DEVICE_READINESS_UNKNOWN: DeviceReadinessState
DEVICE_READINESS_UNTRUSTED: DeviceReadinessState
DEVICE_READINESS_DISCONNECTED: DeviceReadinessState
DEVICE_READINESS_GRACE_PERIOD: DeviceReadinessState
DEVICE_READINESS_RECOVERING: DeviceReadinessState
DEVICE_READINESS_READY: DeviceReadinessState
DEVICE_READINESS_EXHAUSTED: DeviceReadinessState
PROVIDER_SAMPLE_STATE_UNSPECIFIED: ProviderSampleState
PROVIDER_SAMPLE_STATE_EMPTY: ProviderSampleState
PROVIDER_SAMPLE_STATE_CURRENT: ProviderSampleState
PROVIDER_SAMPLE_STATE_EXPIRED: ProviderSampleState
PROVIDER_SAMPLE_STATE_CLOSED: ProviderSampleState
SNAPSHOT_VALUE_STATE_UNKNOWN: SnapshotValueState
SNAPSHOT_VALUE_STATE_AVAILABLE: SnapshotValueState
SNAPSHOT_VALUE_STATE_TEMPORARILY_UNAVAILABLE: SnapshotValueState
SNAPSHOT_VALUE_STATE_UNSUPPORTED: SnapshotValueState
SNAPSHOT_FIELD_UNSPECIFIED: SnapshotFieldId
SNAPSHOT_FIELD_CONNECTED: SnapshotFieldId
SNAPSHOT_FIELD_LOCAL_SKI: SnapshotFieldId
SNAPSHOT_FIELD_DEVICE_INFO: SnapshotFieldId
SNAPSHOT_FIELD_DEVICE_OPERATING_STATE: SnapshotFieldId
SNAPSHOT_FIELD_POWER_L1: SnapshotFieldId
SNAPSHOT_FIELD_POWER_L2: SnapshotFieldId
SNAPSHOT_FIELD_POWER_L3: SnapshotFieldId
SNAPSHOT_FIELD_CURRENT_L1: SnapshotFieldId
SNAPSHOT_FIELD_CURRENT_L2: SnapshotFieldId
SNAPSHOT_FIELD_CURRENT_L3: SnapshotFieldId
SNAPSHOT_FIELD_VOLTAGE_L1: SnapshotFieldId
SNAPSHOT_FIELD_VOLTAGE_L2: SnapshotFieldId
SNAPSHOT_FIELD_VOLTAGE_L3: SnapshotFieldId
SNAPSHOT_FIELD_FREQUENCY: SnapshotFieldId
SNAPSHOT_FIELD_ENERGY_PRODUCED: SnapshotFieldId
SNAPSHOT_FIELD_DHW_TEMPERATURE: SnapshotFieldId
SNAPSHOT_FIELD_ROOM_TEMPERATURE: SnapshotFieldId
SNAPSHOT_FIELD_OUTDOOR_TEMPERATURE: SnapshotFieldId
SNAPSHOT_FIELD_FLOW_TEMPERATURE: SnapshotFieldId
SNAPSHOT_FIELD_RETURN_TEMPERATURE: SnapshotFieldId
SNAPSHOT_FIELD_COMPRESSOR_TEMPERATURE: SnapshotFieldId
SNAPSHOT_FIELD_COMPRESSOR_POWER: SnapshotFieldId
SNAPSHOT_FIELD_POWER: SnapshotFieldId
SNAPSHOT_FIELD_ENERGY_CONSUMED_HEATING: SnapshotFieldId
SNAPSHOT_FIELD_ENERGY_CONSUMED_DHW: SnapshotFieldId
SNAPSHOT_FIELD_ENERGY_CONSUMED: SnapshotFieldId
SNAPSHOT_FIELD_CONSUMPTION_LIMIT: SnapshotFieldId
SNAPSHOT_FIELD_FAILSAFE_LIMIT: SnapshotFieldId
SNAPSHOT_FIELD_HEARTBEAT_STATUS: SnapshotFieldId
SNAPSHOT_FIELD_COMPRESSOR_FLEXIBILITY: SnapshotFieldId
SNAPSHOT_FIELD_DHW_SETPOINT: SnapshotFieldId
SNAPSHOT_FIELD_DHW_SYSTEM_FUNCTION: SnapshotFieldId
SNAPSHOT_FIELD_ROOM_HEATING_SETPOINT: SnapshotFieldId
SNAPSHOT_FIELD_ROOM_HEATING_SYSTEM_FUNCTION: SnapshotFieldId
EVENT_AVAILABILITY_UNSPECIFIED: EventAvailability
EVENT_AVAILABILITY_AVAILABLE: EventAvailability
EVENT_AVAILABILITY_TEMPORARILY_UNAVAILABLE: EventAvailability
EVENT_AVAILABILITY_UNSUPPORTED: EventAvailability
RESYNC_REASON_UNSPECIFIED: ResyncReason
RESYNC_REASON_INITIAL_STATE_REQUIRED: ResyncReason
RESYNC_REASON_EVENT_DROPPED: ResyncReason
RESYNC_REASON_UNCLASSIFIED_EVENT: ResyncReason
DEVICE_EVENT_UNSPECIFIED: DeviceEventType
DEVICE_EVENT_CONNECTED: DeviceEventType
DEVICE_EVENT_DISCONNECTED: DeviceEventType
DEVICE_EVENT_TRUST_REMOVED: DeviceEventType
DEVICE_EVENT_PROVIDER_UPDATED: DeviceEventType

class DeviceCapability(_message.Message):
    __slots__ = ("id", "state", "reason", "last_changed")
    ID_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    LAST_CHANGED_FIELD_NUMBER: _ClassVar[int]
    id: CapabilityId
    state: CapabilityState
    reason: CapabilityReason
    last_changed: _timestamp_pb2.Timestamp
    def __init__(self, id: _Optional[_Union[CapabilityId, str]] = ..., state: _Optional[_Union[CapabilityState, str]] = ..., reason: _Optional[_Union[CapabilityReason, str]] = ..., last_changed: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class DeviceCapabilities(_message.Message):
    __slots__ = ("ski", "capabilities")
    SKI_FIELD_NUMBER: _ClassVar[int]
    CAPABILITIES_FIELD_NUMBER: _ClassVar[int]
    ski: str
    capabilities: _containers.RepeatedCompositeFieldContainer[DeviceCapability]
    def __init__(self, ski: _Optional[str] = ..., capabilities: _Optional[_Iterable[_Union[DeviceCapability, _Mapping]]] = ...) -> None: ...

class ServiceStatus(_message.Message):
    __slots__ = ("running", "local_ski")
    RUNNING_FIELD_NUMBER: _ClassVar[int]
    LOCAL_SKI_FIELD_NUMBER: _ClassVar[int]
    running: bool
    local_ski: str
    def __init__(self, running: bool = ..., local_ski: _Optional[str] = ...) -> None: ...

class ServerInfo(_message.Message):
    __slots__ = ("api_major", "api_minor", "bridge_build_version", "features", "local_ski")
    API_MAJOR_FIELD_NUMBER: _ClassVar[int]
    API_MINOR_FIELD_NUMBER: _ClassVar[int]
    BRIDGE_BUILD_VERSION_FIELD_NUMBER: _ClassVar[int]
    FEATURES_FIELD_NUMBER: _ClassVar[int]
    LOCAL_SKI_FIELD_NUMBER: _ClassVar[int]
    api_major: int
    api_minor: int
    bridge_build_version: str
    features: _containers.RepeatedScalarFieldContainer[FeatureId]
    local_ski: str
    def __init__(self, api_major: _Optional[int] = ..., api_minor: _Optional[int] = ..., bridge_build_version: _Optional[str] = ..., features: _Optional[_Iterable[_Union[FeatureId, str]]] = ..., local_ski: _Optional[str] = ...) -> None: ...

class DeviceStatus(_message.Message):
    __slots__ = ("connected", "last_transition", "readiness", "recovery")
    CONNECTED_FIELD_NUMBER: _ClassVar[int]
    LAST_TRANSITION_FIELD_NUMBER: _ClassVar[int]
    READINESS_FIELD_NUMBER: _ClassVar[int]
    RECOVERY_FIELD_NUMBER: _ClassVar[int]
    connected: bool
    last_transition: _timestamp_pb2.Timestamp
    readiness: DeviceReadinessState
    recovery: RecoveryDiagnostics
    def __init__(self, connected: bool = ..., last_transition: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., readiness: _Optional[_Union[DeviceReadinessState, str]] = ..., recovery: _Optional[_Union[RecoveryDiagnostics, _Mapping]] = ...) -> None: ...

class RecoveryDiagnostics(_message.Message):
    __slots__ = ("state", "attempts", "first_stale_at", "last_attempt_at", "next_attempt_at", "last_transition_at")
    STATE_FIELD_NUMBER: _ClassVar[int]
    ATTEMPTS_FIELD_NUMBER: _ClassVar[int]
    FIRST_STALE_AT_FIELD_NUMBER: _ClassVar[int]
    LAST_ATTEMPT_AT_FIELD_NUMBER: _ClassVar[int]
    NEXT_ATTEMPT_AT_FIELD_NUMBER: _ClassVar[int]
    LAST_TRANSITION_AT_FIELD_NUMBER: _ClassVar[int]
    state: DeviceReadinessState
    attempts: int
    first_stale_at: _timestamp_pb2.Timestamp
    last_attempt_at: _timestamp_pb2.Timestamp
    next_attempt_at: _timestamp_pb2.Timestamp
    last_transition_at: _timestamp_pb2.Timestamp
    def __init__(self, state: _Optional[_Union[DeviceReadinessState, str]] = ..., attempts: _Optional[int] = ..., first_stale_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., last_attempt_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., next_attempt_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., last_transition_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class EventTransportDiagnostics(_message.Message):
    __slots__ = ("revision", "dropped_events", "resync_count", "unresolved_events")
    REVISION_FIELD_NUMBER: _ClassVar[int]
    DROPPED_EVENTS_FIELD_NUMBER: _ClassVar[int]
    RESYNC_COUNT_FIELD_NUMBER: _ClassVar[int]
    UNRESOLVED_EVENTS_FIELD_NUMBER: _ClassVar[int]
    revision: int
    dropped_events: int
    resync_count: int
    unresolved_events: int
    def __init__(self, revision: _Optional[int] = ..., dropped_events: _Optional[int] = ..., resync_count: _Optional[int] = ..., unresolved_events: _Optional[int] = ...) -> None: ...

class SnapshotReadDiagnostics(_message.Message):
    __slots__ = ("duration_milliseconds", "last_success")
    DURATION_MILLISECONDS_FIELD_NUMBER: _ClassVar[int]
    LAST_SUCCESS_FIELD_NUMBER: _ClassVar[int]
    duration_milliseconds: int
    last_success: _timestamp_pb2.Timestamp
    def __init__(self, duration_milliseconds: _Optional[int] = ..., last_success: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class ProviderSampleDiagnostics(_message.Message):
    __slots__ = ("provider", "state", "observed_at", "valid_until")
    PROVIDER_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    OBSERVED_AT_FIELD_NUMBER: _ClassVar[int]
    VALID_UNTIL_FIELD_NUMBER: _ClassVar[int]
    provider: str
    state: ProviderSampleState
    observed_at: _timestamp_pb2.Timestamp
    valid_until: _timestamp_pb2.Timestamp
    def __init__(self, provider: _Optional[str] = ..., state: _Optional[_Union[ProviderSampleState, str]] = ..., observed_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., valid_until: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class DeviceOperationalDiagnostics(_message.Message):
    __slots__ = ("redacted_ski", "readiness", "recovery", "events", "monitoring_last_success_age_seconds", "snapshot_reads", "providers", "features", "connection_age_seconds")
    REDACTED_SKI_FIELD_NUMBER: _ClassVar[int]
    READINESS_FIELD_NUMBER: _ClassVar[int]
    RECOVERY_FIELD_NUMBER: _ClassVar[int]
    EVENTS_FIELD_NUMBER: _ClassVar[int]
    MONITORING_LAST_SUCCESS_AGE_SECONDS_FIELD_NUMBER: _ClassVar[int]
    SNAPSHOT_READS_FIELD_NUMBER: _ClassVar[int]
    PROVIDERS_FIELD_NUMBER: _ClassVar[int]
    FEATURES_FIELD_NUMBER: _ClassVar[int]
    CONNECTION_AGE_SECONDS_FIELD_NUMBER: _ClassVar[int]
    redacted_ski: str
    readiness: DeviceReadinessState
    recovery: RecoveryDiagnostics
    events: EventTransportDiagnostics
    monitoring_last_success_age_seconds: int
    snapshot_reads: SnapshotReadDiagnostics
    providers: _containers.RepeatedCompositeFieldContainer[ProviderSampleDiagnostics]
    features: _containers.RepeatedScalarFieldContainer[FeatureId]
    connection_age_seconds: int
    def __init__(self, redacted_ski: _Optional[str] = ..., readiness: _Optional[_Union[DeviceReadinessState, str]] = ..., recovery: _Optional[_Union[RecoveryDiagnostics, _Mapping]] = ..., events: _Optional[_Union[EventTransportDiagnostics, _Mapping]] = ..., monitoring_last_success_age_seconds: _Optional[int] = ..., snapshot_reads: _Optional[_Union[SnapshotReadDiagnostics, _Mapping]] = ..., providers: _Optional[_Iterable[_Union[ProviderSampleDiagnostics, _Mapping]]] = ..., features: _Optional[_Iterable[_Union[FeatureId, str]]] = ..., connection_age_seconds: _Optional[int] = ...) -> None: ...

class DiscoveredDevice(_message.Message):
    __slots__ = ("ski", "brand", "model", "serial", "device_type", "host")
    SKI_FIELD_NUMBER: _ClassVar[int]
    BRAND_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    SERIAL_FIELD_NUMBER: _ClassVar[int]
    DEVICE_TYPE_FIELD_NUMBER: _ClassVar[int]
    HOST_FIELD_NUMBER: _ClassVar[int]
    ski: str
    brand: str
    model: str
    serial: str
    device_type: str
    host: str
    def __init__(self, ski: _Optional[str] = ..., brand: _Optional[str] = ..., model: _Optional[str] = ..., serial: _Optional[str] = ..., device_type: _Optional[str] = ..., host: _Optional[str] = ...) -> None: ...

class ListDevicesResponse(_message.Message):
    __slots__ = ("devices",)
    DEVICES_FIELD_NUMBER: _ClassVar[int]
    devices: _containers.RepeatedCompositeFieldContainer[DiscoveredDevice]
    def __init__(self, devices: _Optional[_Iterable[_Union[DiscoveredDevice, _Mapping]]] = ...) -> None: ...

class RegisterSKIRequest(_message.Message):
    __slots__ = ("ski",)
    SKI_FIELD_NUMBER: _ClassVar[int]
    ski: str
    def __init__(self, ski: _Optional[str] = ...) -> None: ...

class PairedDevice(_message.Message):
    __slots__ = ("ski", "brand", "model", "serial", "device_type", "supported_use_cases", "software_revision", "hardware_revision")
    SKI_FIELD_NUMBER: _ClassVar[int]
    BRAND_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    SERIAL_FIELD_NUMBER: _ClassVar[int]
    DEVICE_TYPE_FIELD_NUMBER: _ClassVar[int]
    SUPPORTED_USE_CASES_FIELD_NUMBER: _ClassVar[int]
    SOFTWARE_REVISION_FIELD_NUMBER: _ClassVar[int]
    HARDWARE_REVISION_FIELD_NUMBER: _ClassVar[int]
    ski: str
    brand: str
    model: str
    serial: str
    device_type: str
    supported_use_cases: _containers.RepeatedScalarFieldContainer[str]
    software_revision: str
    hardware_revision: str
    def __init__(self, ski: _Optional[str] = ..., brand: _Optional[str] = ..., model: _Optional[str] = ..., serial: _Optional[str] = ..., device_type: _Optional[str] = ..., supported_use_cases: _Optional[_Iterable[str]] = ..., software_revision: _Optional[str] = ..., hardware_revision: _Optional[str] = ...) -> None: ...

class ListPairedDevicesResponse(_message.Message):
    __slots__ = ("devices",)
    DEVICES_FIELD_NUMBER: _ClassVar[int]
    devices: _containers.RepeatedCompositeFieldContainer[PairedDevice]
    def __init__(self, devices: _Optional[_Iterable[_Union[PairedDevice, _Mapping]]] = ...) -> None: ...

class DeviceEvent(_message.Message):
    __slots__ = ("ski", "event_type")
    SKI_FIELD_NUMBER: _ClassVar[int]
    EVENT_TYPE_FIELD_NUMBER: _ClassVar[int]
    ski: str
    event_type: DeviceEventType
    def __init__(self, ski: _Optional[str] = ..., event_type: _Optional[_Union[DeviceEventType, str]] = ...) -> None: ...

class DeviceStateEvent(_message.Message):
    __slots__ = ("ski", "revision", "event_time", "device", "measurement", "lpc", "dhw", "hvac", "ohpcf", "capability", "resync_required", "initial_snapshot", "availability")
    SKI_FIELD_NUMBER: _ClassVar[int]
    REVISION_FIELD_NUMBER: _ClassVar[int]
    EVENT_TIME_FIELD_NUMBER: _ClassVar[int]
    DEVICE_FIELD_NUMBER: _ClassVar[int]
    MEASUREMENT_FIELD_NUMBER: _ClassVar[int]
    LPC_FIELD_NUMBER: _ClassVar[int]
    DHW_FIELD_NUMBER: _ClassVar[int]
    HVAC_FIELD_NUMBER: _ClassVar[int]
    OHPCF_FIELD_NUMBER: _ClassVar[int]
    CAPABILITY_FIELD_NUMBER: _ClassVar[int]
    RESYNC_REQUIRED_FIELD_NUMBER: _ClassVar[int]
    INITIAL_SNAPSHOT_FIELD_NUMBER: _ClassVar[int]
    AVAILABILITY_FIELD_NUMBER: _ClassVar[int]
    ski: str
    revision: int
    event_time: _timestamp_pb2.Timestamp
    device: DeviceEvent
    measurement: _monitoring_service_pb2.MeasurementEvent
    lpc: _lpc_service_pb2.LPCEvent
    dhw: DeviceStateDHWEvent
    hvac: _hvac_service_pb2.RoomHeatingEvent
    ohpcf: _ohpcf_service_pb2.OHPCFEvent
    capability: DeviceCapabilities
    resync_required: ResyncRequired
    initial_snapshot: DeviceSnapshot
    availability: EventAvailability
    def __init__(self, ski: _Optional[str] = ..., revision: _Optional[int] = ..., event_time: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., device: _Optional[_Union[DeviceEvent, _Mapping]] = ..., measurement: _Optional[_Union[_monitoring_service_pb2.MeasurementEvent, _Mapping]] = ..., lpc: _Optional[_Union[_lpc_service_pb2.LPCEvent, _Mapping]] = ..., dhw: _Optional[_Union[DeviceStateDHWEvent, _Mapping]] = ..., hvac: _Optional[_Union[_hvac_service_pb2.RoomHeatingEvent, _Mapping]] = ..., ohpcf: _Optional[_Union[_ohpcf_service_pb2.OHPCFEvent, _Mapping]] = ..., capability: _Optional[_Union[DeviceCapabilities, _Mapping]] = ..., resync_required: _Optional[_Union[ResyncRequired, _Mapping]] = ..., initial_snapshot: _Optional[_Union[DeviceSnapshot, _Mapping]] = ..., availability: _Optional[_Union[EventAvailability, str]] = ...) -> None: ...

class SnapshotFieldStatus(_message.Message):
    __slots__ = ("id", "state")
    ID_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    id: SnapshotFieldId
    state: SnapshotValueState
    def __init__(self, id: _Optional[_Union[SnapshotFieldId, str]] = ..., state: _Optional[_Union[SnapshotValueState, str]] = ...) -> None: ...

class DeviceSnapshot(_message.Message):
    __slots__ = ("ski", "captured_at", "event_revision", "connection", "connection_state", "classification", "classification_state", "capabilities", "measurements", "measurements_state", "consumption_limit", "consumption_limit_state", "failsafe_limit", "failsafe_limit_state", "heartbeat", "heartbeat_state", "dhw_setpoint", "dhw_setpoint_state", "dhw_system_function", "dhw_system_function_state", "room_heating", "room_heating_state", "compressor_flexibility", "compressor_flexibility_state", "device_diagnostics", "device_diagnostics_state", "local_ski", "field_states")
    SKI_FIELD_NUMBER: _ClassVar[int]
    CAPTURED_AT_FIELD_NUMBER: _ClassVar[int]
    EVENT_REVISION_FIELD_NUMBER: _ClassVar[int]
    CONNECTION_FIELD_NUMBER: _ClassVar[int]
    CONNECTION_STATE_FIELD_NUMBER: _ClassVar[int]
    CLASSIFICATION_FIELD_NUMBER: _ClassVar[int]
    CLASSIFICATION_STATE_FIELD_NUMBER: _ClassVar[int]
    CAPABILITIES_FIELD_NUMBER: _ClassVar[int]
    MEASUREMENTS_FIELD_NUMBER: _ClassVar[int]
    MEASUREMENTS_STATE_FIELD_NUMBER: _ClassVar[int]
    CONSUMPTION_LIMIT_FIELD_NUMBER: _ClassVar[int]
    CONSUMPTION_LIMIT_STATE_FIELD_NUMBER: _ClassVar[int]
    FAILSAFE_LIMIT_FIELD_NUMBER: _ClassVar[int]
    FAILSAFE_LIMIT_STATE_FIELD_NUMBER: _ClassVar[int]
    HEARTBEAT_FIELD_NUMBER: _ClassVar[int]
    HEARTBEAT_STATE_FIELD_NUMBER: _ClassVar[int]
    DHW_SETPOINT_FIELD_NUMBER: _ClassVar[int]
    DHW_SETPOINT_STATE_FIELD_NUMBER: _ClassVar[int]
    DHW_SYSTEM_FUNCTION_FIELD_NUMBER: _ClassVar[int]
    DHW_SYSTEM_FUNCTION_STATE_FIELD_NUMBER: _ClassVar[int]
    ROOM_HEATING_FIELD_NUMBER: _ClassVar[int]
    ROOM_HEATING_STATE_FIELD_NUMBER: _ClassVar[int]
    COMPRESSOR_FLEXIBILITY_FIELD_NUMBER: _ClassVar[int]
    COMPRESSOR_FLEXIBILITY_STATE_FIELD_NUMBER: _ClassVar[int]
    DEVICE_DIAGNOSTICS_FIELD_NUMBER: _ClassVar[int]
    DEVICE_DIAGNOSTICS_STATE_FIELD_NUMBER: _ClassVar[int]
    LOCAL_SKI_FIELD_NUMBER: _ClassVar[int]
    FIELD_STATES_FIELD_NUMBER: _ClassVar[int]
    ski: str
    captured_at: _timestamp_pb2.Timestamp
    event_revision: int
    connection: DeviceStatus
    connection_state: SnapshotValueState
    classification: PairedDevice
    classification_state: SnapshotValueState
    capabilities: DeviceCapabilities
    measurements: _containers.RepeatedCompositeFieldContainer[_common_pb2.MeasurementEntry]
    measurements_state: SnapshotValueState
    consumption_limit: _common_pb2.LoadLimit
    consumption_limit_state: SnapshotValueState
    failsafe_limit: _lpc_service_pb2.FailsafeLimit
    failsafe_limit_state: SnapshotValueState
    heartbeat: _lpc_service_pb2.HeartbeatStatus
    heartbeat_state: SnapshotValueState
    dhw_setpoint: _dhw_service_pb2.DHWSetpoint
    dhw_setpoint_state: SnapshotValueState
    dhw_system_function: _dhw_service_pb2.DHWSystemFunctionState
    dhw_system_function_state: SnapshotValueState
    room_heating: _hvac_service_pb2.RoomHeatingState
    room_heating_state: SnapshotValueState
    compressor_flexibility: _ohpcf_service_pb2.CompressorFlexibility
    compressor_flexibility_state: SnapshotValueState
    device_diagnostics: _monitoring_service_pb2.DeviceDiagnosticsData
    device_diagnostics_state: SnapshotValueState
    local_ski: str
    field_states: _containers.RepeatedCompositeFieldContainer[SnapshotFieldStatus]
    def __init__(self, ski: _Optional[str] = ..., captured_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., event_revision: _Optional[int] = ..., connection: _Optional[_Union[DeviceStatus, _Mapping]] = ..., connection_state: _Optional[_Union[SnapshotValueState, str]] = ..., classification: _Optional[_Union[PairedDevice, _Mapping]] = ..., classification_state: _Optional[_Union[SnapshotValueState, str]] = ..., capabilities: _Optional[_Union[DeviceCapabilities, _Mapping]] = ..., measurements: _Optional[_Iterable[_Union[_common_pb2.MeasurementEntry, _Mapping]]] = ..., measurements_state: _Optional[_Union[SnapshotValueState, str]] = ..., consumption_limit: _Optional[_Union[_common_pb2.LoadLimit, _Mapping]] = ..., consumption_limit_state: _Optional[_Union[SnapshotValueState, str]] = ..., failsafe_limit: _Optional[_Union[_lpc_service_pb2.FailsafeLimit, _Mapping]] = ..., failsafe_limit_state: _Optional[_Union[SnapshotValueState, str]] = ..., heartbeat: _Optional[_Union[_lpc_service_pb2.HeartbeatStatus, _Mapping]] = ..., heartbeat_state: _Optional[_Union[SnapshotValueState, str]] = ..., dhw_setpoint: _Optional[_Union[_dhw_service_pb2.DHWSetpoint, _Mapping]] = ..., dhw_setpoint_state: _Optional[_Union[SnapshotValueState, str]] = ..., dhw_system_function: _Optional[_Union[_dhw_service_pb2.DHWSystemFunctionState, _Mapping]] = ..., dhw_system_function_state: _Optional[_Union[SnapshotValueState, str]] = ..., room_heating: _Optional[_Union[_hvac_service_pb2.RoomHeatingState, _Mapping]] = ..., room_heating_state: _Optional[_Union[SnapshotValueState, str]] = ..., compressor_flexibility: _Optional[_Union[_ohpcf_service_pb2.CompressorFlexibility, _Mapping]] = ..., compressor_flexibility_state: _Optional[_Union[SnapshotValueState, str]] = ..., device_diagnostics: _Optional[_Union[_monitoring_service_pb2.DeviceDiagnosticsData, _Mapping]] = ..., device_diagnostics_state: _Optional[_Union[SnapshotValueState, str]] = ..., local_ski: _Optional[str] = ..., field_states: _Optional[_Iterable[_Union[SnapshotFieldStatus, _Mapping]]] = ...) -> None: ...

class DeviceStateDHWEvent(_message.Message):
    __slots__ = ("temperature", "system_function")
    TEMPERATURE_FIELD_NUMBER: _ClassVar[int]
    SYSTEM_FUNCTION_FIELD_NUMBER: _ClassVar[int]
    temperature: _dhw_service_pb2.DHWEvent
    system_function: _dhw_service_pb2.DHWSystemFunctionEvent
    def __init__(self, temperature: _Optional[_Union[_dhw_service_pb2.DHWEvent, _Mapping]] = ..., system_function: _Optional[_Union[_dhw_service_pb2.DHWSystemFunctionEvent, _Mapping]] = ...) -> None: ...

class ResyncRequired(_message.Message):
    __slots__ = ("reason", "dropped_events")
    REASON_FIELD_NUMBER: _ClassVar[int]
    DROPPED_EVENTS_FIELD_NUMBER: _ClassVar[int]
    reason: ResyncReason
    dropped_events: int
    def __init__(self, reason: _Optional[_Union[ResyncReason, str]] = ..., dropped_events: _Optional[int] = ...) -> None: ...
