import datetime

from . import common_pb2 as _common_pb2
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

class DeviceEventType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    DEVICE_EVENT_UNSPECIFIED: _ClassVar[DeviceEventType]
    DEVICE_EVENT_CONNECTED: _ClassVar[DeviceEventType]
    DEVICE_EVENT_DISCONNECTED: _ClassVar[DeviceEventType]
    DEVICE_EVENT_TRUST_REMOVED: _ClassVar[DeviceEventType]
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
DEVICE_EVENT_UNSPECIFIED: DeviceEventType
DEVICE_EVENT_CONNECTED: DeviceEventType
DEVICE_EVENT_DISCONNECTED: DeviceEventType
DEVICE_EVENT_TRUST_REMOVED: DeviceEventType

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

class DeviceStatus(_message.Message):
    __slots__ = ("connected", "last_transition")
    CONNECTED_FIELD_NUMBER: _ClassVar[int]
    LAST_TRANSITION_FIELD_NUMBER: _ClassVar[int]
    connected: bool
    last_transition: _timestamp_pb2.Timestamp
    def __init__(self, connected: bool = ..., last_transition: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

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
    __slots__ = ("ski", "brand", "model", "serial", "device_type", "supported_use_cases")
    SKI_FIELD_NUMBER: _ClassVar[int]
    BRAND_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    SERIAL_FIELD_NUMBER: _ClassVar[int]
    DEVICE_TYPE_FIELD_NUMBER: _ClassVar[int]
    SUPPORTED_USE_CASES_FIELD_NUMBER: _ClassVar[int]
    ski: str
    brand: str
    model: str
    serial: str
    device_type: str
    supported_use_cases: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, ski: _Optional[str] = ..., brand: _Optional[str] = ..., model: _Optional[str] = ..., serial: _Optional[str] = ..., device_type: _Optional[str] = ..., supported_use_cases: _Optional[_Iterable[str]] = ...) -> None: ...

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
