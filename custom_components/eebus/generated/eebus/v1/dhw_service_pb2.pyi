from . import common_pb2 as _common_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class DHWEventType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    DHW_EVENT_UNSPECIFIED: _ClassVar[DHWEventType]
    DHW_EVENT_SUPPORT_UPDATED: _ClassVar[DHWEventType]
    DHW_EVENT_SETPOINT_UPDATED: _ClassVar[DHWEventType]

class DHWBoostStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    DHW_BOOST_STATUS_UNSPECIFIED: _ClassVar[DHWBoostStatus]
    DHW_BOOST_STATUS_INACTIVE: _ClassVar[DHWBoostStatus]
    DHW_BOOST_STATUS_ACTIVE: _ClassVar[DHWBoostStatus]
    DHW_BOOST_STATUS_RUNNING: _ClassVar[DHWBoostStatus]
    DHW_BOOST_STATUS_FINISHED: _ClassVar[DHWBoostStatus]

class DHWSystemFunctionEventType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    DHW_SYSTEM_FUNCTION_EVENT_UNSPECIFIED: _ClassVar[DHWSystemFunctionEventType]
    DHW_SYSTEM_FUNCTION_EVENT_SUPPORT_UPDATED: _ClassVar[DHWSystemFunctionEventType]
    DHW_SYSTEM_FUNCTION_EVENT_STATE_UPDATED: _ClassVar[DHWSystemFunctionEventType]
DHW_EVENT_UNSPECIFIED: DHWEventType
DHW_EVENT_SUPPORT_UPDATED: DHWEventType
DHW_EVENT_SETPOINT_UPDATED: DHWEventType
DHW_BOOST_STATUS_UNSPECIFIED: DHWBoostStatus
DHW_BOOST_STATUS_INACTIVE: DHWBoostStatus
DHW_BOOST_STATUS_ACTIVE: DHWBoostStatus
DHW_BOOST_STATUS_RUNNING: DHWBoostStatus
DHW_BOOST_STATUS_FINISHED: DHWBoostStatus
DHW_SYSTEM_FUNCTION_EVENT_UNSPECIFIED: DHWSystemFunctionEventType
DHW_SYSTEM_FUNCTION_EVENT_SUPPORT_UPDATED: DHWSystemFunctionEventType
DHW_SYSTEM_FUNCTION_EVENT_STATE_UPDATED: DHWSystemFunctionEventType

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

class DHWSystemFunctionState(_message.Message):
    __slots__ = ("boost_status", "boost_writable", "operation_mode", "available_modes", "mode_writable")
    BOOST_STATUS_FIELD_NUMBER: _ClassVar[int]
    BOOST_WRITABLE_FIELD_NUMBER: _ClassVar[int]
    OPERATION_MODE_FIELD_NUMBER: _ClassVar[int]
    AVAILABLE_MODES_FIELD_NUMBER: _ClassVar[int]
    MODE_WRITABLE_FIELD_NUMBER: _ClassVar[int]
    boost_status: DHWBoostStatus
    boost_writable: bool
    operation_mode: str
    available_modes: _containers.RepeatedScalarFieldContainer[str]
    mode_writable: bool
    def __init__(self, boost_status: _Optional[_Union[DHWBoostStatus, str]] = ..., boost_writable: bool = ..., operation_mode: _Optional[str] = ..., available_modes: _Optional[_Iterable[str]] = ..., mode_writable: bool = ...) -> None: ...

class SetDHWBoostRequest(_message.Message):
    __slots__ = ("ski", "active")
    SKI_FIELD_NUMBER: _ClassVar[int]
    ACTIVE_FIELD_NUMBER: _ClassVar[int]
    ski: str
    active: bool
    def __init__(self, ski: _Optional[str] = ..., active: bool = ...) -> None: ...

class SetDHWOperationModeRequest(_message.Message):
    __slots__ = ("ski", "mode")
    SKI_FIELD_NUMBER: _ClassVar[int]
    MODE_FIELD_NUMBER: _ClassVar[int]
    ski: str
    mode: str
    def __init__(self, ski: _Optional[str] = ..., mode: _Optional[str] = ...) -> None: ...

class DHWSystemFunctionEvent(_message.Message):
    __slots__ = ("ski", "event_type", "state")
    SKI_FIELD_NUMBER: _ClassVar[int]
    EVENT_TYPE_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    ski: str
    event_type: DHWSystemFunctionEventType
    state: DHWSystemFunctionState
    def __init__(self, ski: _Optional[str] = ..., event_type: _Optional[_Union[DHWSystemFunctionEventType, str]] = ..., state: _Optional[_Union[DHWSystemFunctionState, _Mapping]] = ...) -> None: ...
