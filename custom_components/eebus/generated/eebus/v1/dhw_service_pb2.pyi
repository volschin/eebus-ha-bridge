from . import common_pb2 as _common_pb2
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class DHWEventType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    DHW_EVENT_UNSPECIFIED: _ClassVar[DHWEventType]
    DHW_EVENT_SUPPORT_UPDATED: _ClassVar[DHWEventType]
    DHW_EVENT_SETPOINT_UPDATED: _ClassVar[DHWEventType]
DHW_EVENT_UNSPECIFIED: DHWEventType
DHW_EVENT_SUPPORT_UPDATED: DHWEventType
DHW_EVENT_SETPOINT_UPDATED: DHWEventType

class DHWSetpoint(_message.Message):
    __slots__ = ("value_celsius", "min_celsius", "max_celsius", "step_celsius", "writable")
    VALUE_CELSIUS_FIELD_NUMBER: _ClassVar[int]
    MIN_CELSIUS_FIELD_NUMBER: _ClassVar[int]
    MAX_CELSIUS_FIELD_NUMBER: _ClassVar[int]
    STEP_CELSIUS_FIELD_NUMBER: _ClassVar[int]
    WRITABLE_FIELD_NUMBER: _ClassVar[int]
    value_celsius: float
    min_celsius: float
    max_celsius: float
    step_celsius: float
    writable: bool
    def __init__(self, value_celsius: _Optional[float] = ..., min_celsius: _Optional[float] = ..., max_celsius: _Optional[float] = ..., step_celsius: _Optional[float] = ..., writable: bool = ...) -> None: ...

class SetDHWSetpointRequest(_message.Message):
    __slots__ = ("ski", "value_celsius")
    SKI_FIELD_NUMBER: _ClassVar[int]
    VALUE_CELSIUS_FIELD_NUMBER: _ClassVar[int]
    ski: str
    value_celsius: float
    def __init__(self, ski: _Optional[str] = ..., value_celsius: _Optional[float] = ...) -> None: ...

class DHWEvent(_message.Message):
    __slots__ = ("ski", "event_type", "setpoint")
    SKI_FIELD_NUMBER: _ClassVar[int]
    EVENT_TYPE_FIELD_NUMBER: _ClassVar[int]
    SETPOINT_FIELD_NUMBER: _ClassVar[int]
    ski: str
    event_type: DHWEventType
    setpoint: DHWSetpoint
    def __init__(self, ski: _Optional[str] = ..., event_type: _Optional[_Union[DHWEventType, str]] = ..., setpoint: _Optional[_Union[DHWSetpoint, _Mapping]] = ...) -> None: ...
