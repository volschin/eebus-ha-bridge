import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class MeasurementId(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    MEASUREMENT_ID_UNSPECIFIED: _ClassVar[MeasurementId]
    MEASUREMENT_ID_POWER_CONSUMPTION: _ClassVar[MeasurementId]
    MEASUREMENT_ID_ENERGY_CONSUMED: _ClassVar[MeasurementId]
    MEASUREMENT_ID_POWER_L1: _ClassVar[MeasurementId]
    MEASUREMENT_ID_POWER_L2: _ClassVar[MeasurementId]
    MEASUREMENT_ID_POWER_L3: _ClassVar[MeasurementId]
    MEASUREMENT_ID_CURRENT_L1: _ClassVar[MeasurementId]
    MEASUREMENT_ID_CURRENT_L2: _ClassVar[MeasurementId]
    MEASUREMENT_ID_CURRENT_L3: _ClassVar[MeasurementId]
    MEASUREMENT_ID_VOLTAGE_L1: _ClassVar[MeasurementId]
    MEASUREMENT_ID_VOLTAGE_L2: _ClassVar[MeasurementId]
    MEASUREMENT_ID_VOLTAGE_L3: _ClassVar[MeasurementId]
    MEASUREMENT_ID_FREQUENCY: _ClassVar[MeasurementId]
    MEASUREMENT_ID_ENERGY_PRODUCED: _ClassVar[MeasurementId]
    MEASUREMENT_ID_DHW_TEMPERATURE: _ClassVar[MeasurementId]
    MEASUREMENT_ID_ROOM_TEMPERATURE: _ClassVar[MeasurementId]
    MEASUREMENT_ID_OUTDOOR_TEMPERATURE: _ClassVar[MeasurementId]
    MEASUREMENT_ID_FLOW_TEMPERATURE: _ClassVar[MeasurementId]
    MEASUREMENT_ID_RETURN_TEMPERATURE: _ClassVar[MeasurementId]
    MEASUREMENT_ID_COMPRESSOR_TEMPERATURE: _ClassVar[MeasurementId]
    MEASUREMENT_ID_COMPRESSOR_POWER: _ClassVar[MeasurementId]
    MEASUREMENT_ID_ENERGY_CONSUMED_HEATING: _ClassVar[MeasurementId]
    MEASUREMENT_ID_ENERGY_CONSUMED_DHW: _ClassVar[MeasurementId]
MEASUREMENT_ID_UNSPECIFIED: MeasurementId
MEASUREMENT_ID_POWER_CONSUMPTION: MeasurementId
MEASUREMENT_ID_ENERGY_CONSUMED: MeasurementId
MEASUREMENT_ID_POWER_L1: MeasurementId
MEASUREMENT_ID_POWER_L2: MeasurementId
MEASUREMENT_ID_POWER_L3: MeasurementId
MEASUREMENT_ID_CURRENT_L1: MeasurementId
MEASUREMENT_ID_CURRENT_L2: MeasurementId
MEASUREMENT_ID_CURRENT_L3: MeasurementId
MEASUREMENT_ID_VOLTAGE_L1: MeasurementId
MEASUREMENT_ID_VOLTAGE_L2: MeasurementId
MEASUREMENT_ID_VOLTAGE_L3: MeasurementId
MEASUREMENT_ID_FREQUENCY: MeasurementId
MEASUREMENT_ID_ENERGY_PRODUCED: MeasurementId
MEASUREMENT_ID_DHW_TEMPERATURE: MeasurementId
MEASUREMENT_ID_ROOM_TEMPERATURE: MeasurementId
MEASUREMENT_ID_OUTDOOR_TEMPERATURE: MeasurementId
MEASUREMENT_ID_FLOW_TEMPERATURE: MeasurementId
MEASUREMENT_ID_RETURN_TEMPERATURE: MeasurementId
MEASUREMENT_ID_COMPRESSOR_TEMPERATURE: MeasurementId
MEASUREMENT_ID_COMPRESSOR_POWER: MeasurementId
MEASUREMENT_ID_ENERGY_CONSUMED_HEATING: MeasurementId
MEASUREMENT_ID_ENERGY_CONSUMED_DHW: MeasurementId

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
    __slots__ = ("type", "value", "unit", "timestamp", "id")
    TYPE_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    UNIT_FIELD_NUMBER: _ClassVar[int]
    TIMESTAMP_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    type: str
    value: float
    unit: str
    timestamp: _timestamp_pb2.Timestamp
    id: MeasurementId
    def __init__(self, type: _Optional[str] = ..., value: _Optional[float] = ..., unit: _Optional[str] = ..., timestamp: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., id: _Optional[_Union[MeasurementId, str]] = ...) -> None: ...
