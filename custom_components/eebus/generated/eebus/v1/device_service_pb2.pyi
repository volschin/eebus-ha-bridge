from . import common_pb2 as _common_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class PairingState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    PAIRING_STATE_UNSPECIFIED: _ClassVar[PairingState]
    PAIRING_STATE_PENDING: _ClassVar[PairingState]
    PAIRING_STATE_WAITING_FOR_TRUST: _ClassVar[PairingState]
    PAIRING_STATE_TRUSTED: _ClassVar[PairingState]
    PAIRING_STATE_DENIED: _ClassVar[PairingState]

class DeviceEventType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    DEVICE_EVENT_UNSPECIFIED: _ClassVar[DeviceEventType]
    DEVICE_EVENT_CONNECTED: _ClassVar[DeviceEventType]
    DEVICE_EVENT_DISCONNECTED: _ClassVar[DeviceEventType]
    DEVICE_EVENT_TRUST_REMOVED: _ClassVar[DeviceEventType]
PAIRING_STATE_UNSPECIFIED: PairingState
PAIRING_STATE_PENDING: PairingState
PAIRING_STATE_WAITING_FOR_TRUST: PairingState
PAIRING_STATE_TRUSTED: PairingState
PAIRING_STATE_DENIED: PairingState
DEVICE_EVENT_UNSPECIFIED: DeviceEventType
DEVICE_EVENT_CONNECTED: DeviceEventType
DEVICE_EVENT_DISCONNECTED: DeviceEventType
DEVICE_EVENT_TRUST_REMOVED: DeviceEventType

class ServiceStatus(_message.Message):
    __slots__ = ("running", "local_ski")
    RUNNING_FIELD_NUMBER: _ClassVar[int]
    LOCAL_SKI_FIELD_NUMBER: _ClassVar[int]
    running: bool
    local_ski: str
    def __init__(self, running: bool = ..., local_ski: _Optional[str] = ...) -> None: ...

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

class PairingStatus(_message.Message):
    __slots__ = ("ski", "state")
    SKI_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    ski: str
    state: PairingState
    def __init__(self, ski: _Optional[str] = ..., state: _Optional[_Union[PairingState, str]] = ...) -> None: ...

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
