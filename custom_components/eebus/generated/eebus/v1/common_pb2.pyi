import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class Empty(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ProviderSampleMeta(_message.Message):
    __slots__ = ("observed_at", "valid_until", "invalid")
    OBSERVED_AT_FIELD_NUMBER: _ClassVar[int]
    VALID_UNTIL_FIELD_NUMBER: _ClassVar[int]
    INVALID_FIELD_NUMBER: _ClassVar[int]
    observed_at: _timestamp_pb2.Timestamp
    valid_until: _timestamp_pb2.Timestamp
    invalid: bool
    def __init__(self, observed_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., valid_until: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., invalid: bool = ...) -> None: ...

class DeviceRequest(_message.Message):
    __slots__ = ("ski",)
    SKI_FIELD_NUMBER: _ClassVar[int]
    ski: str
    def __init__(self, ski: _Optional[str] = ...) -> None: ...

class LoadLimit(_message.Message):
    __slots__ = ("value_watts", "duration_seconds", "is_active", "is_changeable")
    VALUE_WATTS_FIELD_NUMBER: _ClassVar[int]
    DURATION_SECONDS_FIELD_NUMBER: _ClassVar[int]
    IS_ACTIVE_FIELD_NUMBER: _ClassVar[int]
    IS_CHANGEABLE_FIELD_NUMBER: _ClassVar[int]
    value_watts: float
    duration_seconds: int
    is_active: bool
    is_changeable: bool
    def __init__(self, value_watts: _Optional[float] = ..., duration_seconds: _Optional[int] = ..., is_active: bool = ..., is_changeable: bool = ...) -> None: ...

class PowerMeasurement(_message.Message):
    __slots__ = ("watts", "timestamp")
    WATTS_FIELD_NUMBER: _ClassVar[int]
    TIMESTAMP_FIELD_NUMBER: _ClassVar[int]
    watts: float
    timestamp: _timestamp_pb2.Timestamp
    def __init__(self, watts: _Optional[float] = ..., timestamp: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class MeasurementEntry(_message.Message):
    __slots__ = ("type", "value", "unit", "timestamp")
    TYPE_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    UNIT_FIELD_NUMBER: _ClassVar[int]
    TIMESTAMP_FIELD_NUMBER: _ClassVar[int]
    type: str
    value: float
    unit: str
    timestamp: _timestamp_pb2.Timestamp
    def __init__(self, type: _Optional[str] = ..., value: _Optional[float] = ..., unit: _Optional[str] = ..., timestamp: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...
