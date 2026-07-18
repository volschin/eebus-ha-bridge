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

class MeasurementUpdateField(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    MEASUREMENT_UPDATE_FIELD_UNSPECIFIED: _ClassVar[MeasurementUpdateField]
    MEASUREMENT_UPDATE_FIELD_POWER_PER_PHASE: _ClassVar[MeasurementUpdateField]
    MEASUREMENT_UPDATE_FIELD_ENERGY_PRODUCED: _ClassVar[MeasurementUpdateField]
    MEASUREMENT_UPDATE_FIELD_CURRENT_PER_PHASE: _ClassVar[MeasurementUpdateField]
    MEASUREMENT_UPDATE_FIELD_VOLTAGE_PER_PHASE: _ClassVar[MeasurementUpdateField]
    MEASUREMENT_UPDATE_FIELD_FREQUENCY: _ClassVar[MeasurementUpdateField]

class MeasurementEventType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    MEASUREMENT_EVENT_UNSPECIFIED: _ClassVar[MeasurementEventType]
    MEASUREMENT_EVENT_POWER_UPDATED: _ClassVar[MeasurementEventType]
    MEASUREMENT_EVENT_ENERGY_UPDATED: _ClassVar[MeasurementEventType]
    MEASUREMENT_EVENT_DHW_TEMPERATURE_UPDATED: _ClassVar[MeasurementEventType]
    MEASUREMENT_EVENT_DHW_TEMPERATURE_SUPPORT_UPDATED: _ClassVar[MeasurementEventType]
    MEASUREMENT_EVENT_ROOM_TEMPERATURE_UPDATED: _ClassVar[MeasurementEventType]
    MEASUREMENT_EVENT_ROOM_TEMPERATURE_SUPPORT_UPDATED: _ClassVar[MeasurementEventType]
    MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_UPDATED: _ClassVar[MeasurementEventType]
    MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_SUPPORT_UPDATED: _ClassVar[MeasurementEventType]
    MEASUREMENT_EVENT_FLOW_TEMPERATURE_UPDATED: _ClassVar[MeasurementEventType]
    MEASUREMENT_EVENT_RETURN_TEMPERATURE_UPDATED: _ClassVar[MeasurementEventType]
    MEASUREMENT_EVENT_DEVICE_OPERATING_STATE_UPDATED: _ClassVar[MeasurementEventType]
    MEASUREMENT_EVENT_POWER_PER_PHASE_UPDATED: _ClassVar[MeasurementEventType]
    MEASUREMENT_EVENT_ENERGY_PRODUCED_UPDATED: _ClassVar[MeasurementEventType]
    MEASUREMENT_EVENT_CURRENT_PER_PHASE_UPDATED: _ClassVar[MeasurementEventType]
    MEASUREMENT_EVENT_VOLTAGE_PER_PHASE_UPDATED: _ClassVar[MeasurementEventType]
    MEASUREMENT_EVENT_FREQUENCY_UPDATED: _ClassVar[MeasurementEventType]
MEASUREMENT_UPDATE_FIELD_UNSPECIFIED: MeasurementUpdateField
MEASUREMENT_UPDATE_FIELD_POWER_PER_PHASE: MeasurementUpdateField
MEASUREMENT_UPDATE_FIELD_ENERGY_PRODUCED: MeasurementUpdateField
MEASUREMENT_UPDATE_FIELD_CURRENT_PER_PHASE: MeasurementUpdateField
MEASUREMENT_UPDATE_FIELD_VOLTAGE_PER_PHASE: MeasurementUpdateField
MEASUREMENT_UPDATE_FIELD_FREQUENCY: MeasurementUpdateField
MEASUREMENT_EVENT_UNSPECIFIED: MeasurementEventType
MEASUREMENT_EVENT_POWER_UPDATED: MeasurementEventType
MEASUREMENT_EVENT_ENERGY_UPDATED: MeasurementEventType
MEASUREMENT_EVENT_DHW_TEMPERATURE_UPDATED: MeasurementEventType
MEASUREMENT_EVENT_DHW_TEMPERATURE_SUPPORT_UPDATED: MeasurementEventType
MEASUREMENT_EVENT_ROOM_TEMPERATURE_UPDATED: MeasurementEventType
MEASUREMENT_EVENT_ROOM_TEMPERATURE_SUPPORT_UPDATED: MeasurementEventType
MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_UPDATED: MeasurementEventType
MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_SUPPORT_UPDATED: MeasurementEventType
MEASUREMENT_EVENT_FLOW_TEMPERATURE_UPDATED: MeasurementEventType
MEASUREMENT_EVENT_RETURN_TEMPERATURE_UPDATED: MeasurementEventType
MEASUREMENT_EVENT_DEVICE_OPERATING_STATE_UPDATED: MeasurementEventType
MEASUREMENT_EVENT_POWER_PER_PHASE_UPDATED: MeasurementEventType
MEASUREMENT_EVENT_ENERGY_PRODUCED_UPDATED: MeasurementEventType
MEASUREMENT_EVENT_CURRENT_PER_PHASE_UPDATED: MeasurementEventType
MEASUREMENT_EVENT_VOLTAGE_PER_PHASE_UPDATED: MeasurementEventType
MEASUREMENT_EVENT_FREQUENCY_UPDATED: MeasurementEventType

class EnergyMeasurement(_message.Message):
    __slots__ = ("kilowatt_hours", "timestamp")
    KILOWATT_HOURS_FIELD_NUMBER: _ClassVar[int]
    TIMESTAMP_FIELD_NUMBER: _ClassVar[int]
    kilowatt_hours: float
    timestamp: _timestamp_pb2.Timestamp
    def __init__(self, kilowatt_hours: _Optional[float] = ..., timestamp: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class MeasurementList(_message.Message):
    __slots__ = ("measurements",)
    MEASUREMENTS_FIELD_NUMBER: _ClassVar[int]
    measurements: _containers.RepeatedCompositeFieldContainer[_common_pb2.MeasurementEntry]
    def __init__(self, measurements: _Optional[_Iterable[_Union[_common_pb2.MeasurementEntry, _Mapping]]] = ...) -> None: ...

class DeviceDiagnosticsData(_message.Message):
    __slots__ = ("operating_state", "timestamp")
    OPERATING_STATE_FIELD_NUMBER: _ClassVar[int]
    TIMESTAMP_FIELD_NUMBER: _ClassVar[int]
    operating_state: str
    timestamp: _timestamp_pb2.Timestamp
    def __init__(self, operating_state: _Optional[str] = ..., timestamp: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class MeasurementEvent(_message.Message):
    __slots__ = ("ski", "event_type", "power", "energy", "measurement", "device_diagnostics", "measurements", "update_field")
    SKI_FIELD_NUMBER: _ClassVar[int]
    EVENT_TYPE_FIELD_NUMBER: _ClassVar[int]
    POWER_FIELD_NUMBER: _ClassVar[int]
    ENERGY_FIELD_NUMBER: _ClassVar[int]
    MEASUREMENT_FIELD_NUMBER: _ClassVar[int]
    DEVICE_DIAGNOSTICS_FIELD_NUMBER: _ClassVar[int]
    MEASUREMENTS_FIELD_NUMBER: _ClassVar[int]
    UPDATE_FIELD_FIELD_NUMBER: _ClassVar[int]
    ski: str
    event_type: MeasurementEventType
    power: _common_pb2.PowerMeasurement
    energy: EnergyMeasurement
    measurement: _common_pb2.MeasurementEntry
    device_diagnostics: DeviceDiagnosticsData
    measurements: MeasurementList
    update_field: MeasurementUpdateField
    def __init__(self, ski: _Optional[str] = ..., event_type: _Optional[_Union[MeasurementEventType, str]] = ..., power: _Optional[_Union[_common_pb2.PowerMeasurement, _Mapping]] = ..., energy: _Optional[_Union[EnergyMeasurement, _Mapping]] = ..., measurement: _Optional[_Union[_common_pb2.MeasurementEntry, _Mapping]] = ..., device_diagnostics: _Optional[_Union[DeviceDiagnosticsData, _Mapping]] = ..., measurements: _Optional[_Union[MeasurementList, _Mapping]] = ..., update_field: _Optional[_Union[MeasurementUpdateField, str]] = ...) -> None: ...
