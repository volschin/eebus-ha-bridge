from . import common_pb2 as _common_pb2
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class LPCEventType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    LPC_EVENT_UNSPECIFIED: _ClassVar[LPCEventType]
    LPC_EVENT_LIMIT_UPDATED: _ClassVar[LPCEventType]
    LPC_EVENT_FAILSAFE_UPDATED: _ClassVar[LPCEventType]
    LPC_EVENT_HEARTBEAT_TIMEOUT: _ClassVar[LPCEventType]
LPC_EVENT_UNSPECIFIED: LPCEventType
LPC_EVENT_LIMIT_UPDATED: LPCEventType
LPC_EVENT_FAILSAFE_UPDATED: LPCEventType
LPC_EVENT_HEARTBEAT_TIMEOUT: LPCEventType

class WriteLoadLimitRequest(_message.Message):
    __slots__ = ("ski", "value_watts", "duration_seconds", "is_active")
    SKI_FIELD_NUMBER: _ClassVar[int]
    VALUE_WATTS_FIELD_NUMBER: _ClassVar[int]
    DURATION_SECONDS_FIELD_NUMBER: _ClassVar[int]
    IS_ACTIVE_FIELD_NUMBER: _ClassVar[int]
    ski: str
    value_watts: float
    duration_seconds: int
    is_active: bool
    def __init__(self, ski: _Optional[str] = ..., value_watts: _Optional[float] = ..., duration_seconds: _Optional[int] = ..., is_active: bool = ...) -> None: ...

class FailsafeLimit(_message.Message):
    __slots__ = ("value_watts", "duration_minimum_seconds")
    VALUE_WATTS_FIELD_NUMBER: _ClassVar[int]
    DURATION_MINIMUM_SECONDS_FIELD_NUMBER: _ClassVar[int]
    value_watts: float
    duration_minimum_seconds: int
    def __init__(self, value_watts: _Optional[float] = ..., duration_minimum_seconds: _Optional[int] = ...) -> None: ...

class WriteFailsafeLimitRequest(_message.Message):
    __slots__ = ("ski", "value_watts", "duration_minimum_seconds")
    SKI_FIELD_NUMBER: _ClassVar[int]
    VALUE_WATTS_FIELD_NUMBER: _ClassVar[int]
    DURATION_MINIMUM_SECONDS_FIELD_NUMBER: _ClassVar[int]
    ski: str
    value_watts: float
    duration_minimum_seconds: int
    def __init__(self, ski: _Optional[str] = ..., value_watts: _Optional[float] = ..., duration_minimum_seconds: _Optional[int] = ...) -> None: ...

class HeartbeatStatus(_message.Message):
    __slots__ = ("running", "within_duration")
    RUNNING_FIELD_NUMBER: _ClassVar[int]
    WITHIN_DURATION_FIELD_NUMBER: _ClassVar[int]
    running: bool
    within_duration: bool
    def __init__(self, running: bool = ..., within_duration: bool = ...) -> None: ...

class LPCEvent(_message.Message):
    __slots__ = ("ski", "event_type", "limit_update", "failsafe_update", "heartbeat_update")
    SKI_FIELD_NUMBER: _ClassVar[int]
    EVENT_TYPE_FIELD_NUMBER: _ClassVar[int]
    LIMIT_UPDATE_FIELD_NUMBER: _ClassVar[int]
    FAILSAFE_UPDATE_FIELD_NUMBER: _ClassVar[int]
    HEARTBEAT_UPDATE_FIELD_NUMBER: _ClassVar[int]
    ski: str
    event_type: LPCEventType
    limit_update: _common_pb2.LoadLimit
    failsafe_update: FailsafeLimit
    heartbeat_update: HeartbeatStatus
    def __init__(self, ski: _Optional[str] = ..., event_type: _Optional[_Union[LPCEventType, str]] = ..., limit_update: _Optional[_Union[_common_pb2.LoadLimit, _Mapping]] = ..., failsafe_update: _Optional[_Union[FailsafeLimit, _Mapping]] = ..., heartbeat_update: _Optional[_Union[HeartbeatStatus, _Mapping]] = ...) -> None: ...
