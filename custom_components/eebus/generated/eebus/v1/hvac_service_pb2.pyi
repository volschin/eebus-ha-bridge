from . import common_pb2 as _common_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class RoomHeatingEventType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    ROOM_HEATING_EVENT_UNSPECIFIED: _ClassVar[RoomHeatingEventType]
    ROOM_HEATING_EVENT_SUPPORT_UPDATED: _ClassVar[RoomHeatingEventType]
    ROOM_HEATING_EVENT_CURRENT_TEMPERATURE_UPDATED: _ClassVar[RoomHeatingEventType]
    ROOM_HEATING_EVENT_SETPOINT_UPDATED: _ClassVar[RoomHeatingEventType]
    ROOM_HEATING_EVENT_SYSTEM_FUNCTION_UPDATED: _ClassVar[RoomHeatingEventType]
ROOM_HEATING_EVENT_UNSPECIFIED: RoomHeatingEventType
ROOM_HEATING_EVENT_SUPPORT_UPDATED: RoomHeatingEventType
ROOM_HEATING_EVENT_CURRENT_TEMPERATURE_UPDATED: RoomHeatingEventType
ROOM_HEATING_EVENT_SETPOINT_UPDATED: RoomHeatingEventType
ROOM_HEATING_EVENT_SYSTEM_FUNCTION_UPDATED: RoomHeatingEventType

class RoomHeatingState(_message.Message):
    __slots__ = ("current_temperature_celsius", "setpoint", "system_function")
    CURRENT_TEMPERATURE_CELSIUS_FIELD_NUMBER: _ClassVar[int]
    SETPOINT_FIELD_NUMBER: _ClassVar[int]
    SYSTEM_FUNCTION_FIELD_NUMBER: _ClassVar[int]
    current_temperature_celsius: float
    setpoint: RoomHeatingSetpoint
    system_function: RoomHeatingSystemFunction
    def __init__(self, current_temperature_celsius: _Optional[float] = ..., setpoint: _Optional[_Union[RoomHeatingSetpoint, _Mapping]] = ..., system_function: _Optional[_Union[RoomHeatingSystemFunction, _Mapping]] = ...) -> None: ...

class RoomHeatingSetpoint(_message.Message):
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

class RoomHeatingSystemFunction(_message.Message):
    __slots__ = ("operation_mode", "available_modes", "mode_writable")
    OPERATION_MODE_FIELD_NUMBER: _ClassVar[int]
    AVAILABLE_MODES_FIELD_NUMBER: _ClassVar[int]
    MODE_WRITABLE_FIELD_NUMBER: _ClassVar[int]
    operation_mode: str
    available_modes: _containers.RepeatedScalarFieldContainer[str]
    mode_writable: bool
    def __init__(self, operation_mode: _Optional[str] = ..., available_modes: _Optional[_Iterable[str]] = ..., mode_writable: bool = ...) -> None: ...

class SetRoomHeatingTemperatureRequest(_message.Message):
    __slots__ = ("ski", "value_celsius")
    SKI_FIELD_NUMBER: _ClassVar[int]
    VALUE_CELSIUS_FIELD_NUMBER: _ClassVar[int]
    ski: str
    value_celsius: float
    def __init__(self, ski: _Optional[str] = ..., value_celsius: _Optional[float] = ...) -> None: ...

class SetRoomHeatingModeRequest(_message.Message):
    __slots__ = ("ski", "mode")
    SKI_FIELD_NUMBER: _ClassVar[int]
    MODE_FIELD_NUMBER: _ClassVar[int]
    ski: str
    mode: str
    def __init__(self, ski: _Optional[str] = ..., mode: _Optional[str] = ...) -> None: ...

class RoomHeatingEvent(_message.Message):
    __slots__ = ("ski", "event_type", "state")
    SKI_FIELD_NUMBER: _ClassVar[int]
    EVENT_TYPE_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    ski: str
    event_type: RoomHeatingEventType
    state: RoomHeatingState
    def __init__(self, ski: _Optional[str] = ..., event_type: _Optional[_Union[RoomHeatingEventType, str]] = ..., state: _Optional[_Union[RoomHeatingState, _Mapping]] = ...) -> None: ...
