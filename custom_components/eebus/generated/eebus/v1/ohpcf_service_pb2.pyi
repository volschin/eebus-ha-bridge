import datetime

from . import common_pb2 as _common_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class CompressorPowerConsumptionState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    COMPRESSOR_STATE_UNSPECIFIED: _ClassVar[CompressorPowerConsumptionState]
    COMPRESSOR_STATE_AVAILABLE: _ClassVar[CompressorPowerConsumptionState]
    COMPRESSOR_STATE_SCHEDULED: _ClassVar[CompressorPowerConsumptionState]
    COMPRESSOR_STATE_RUNNING: _ClassVar[CompressorPowerConsumptionState]
    COMPRESSOR_STATE_PAUSED: _ClassVar[CompressorPowerConsumptionState]
    COMPRESSOR_STATE_COMPLETED: _ClassVar[CompressorPowerConsumptionState]
    COMPRESSOR_STATE_STOPPED: _ClassVar[CompressorPowerConsumptionState]

class OHPCFAction(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    OHPCF_ACTION_UNSPECIFIED: _ClassVar[OHPCFAction]
    OHPCF_ACTION_SCHEDULE: _ClassVar[OHPCFAction]
    OHPCF_ACTION_PAUSE: _ClassVar[OHPCFAction]
    OHPCF_ACTION_RESUME: _ClassVar[OHPCFAction]
    OHPCF_ACTION_ABORT: _ClassVar[OHPCFAction]

class OHPCFEventType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    OHPCF_EVENT_UNSPECIFIED: _ClassVar[OHPCFEventType]
    OHPCF_EVENT_SUPPORT_UPDATED: _ClassVar[OHPCFEventType]
    OHPCF_EVENT_STATE_UPDATED: _ClassVar[OHPCFEventType]
    OHPCF_EVENT_DATA_UPDATED: _ClassVar[OHPCFEventType]

class OHPCFUpdateField(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    OHPCF_UPDATE_FIELD_UNSPECIFIED: _ClassVar[OHPCFUpdateField]
    OHPCF_UPDATE_FIELD_STATE: _ClassVar[OHPCFUpdateField]
    OHPCF_UPDATE_FIELD_STOPPABLE: _ClassVar[OHPCFUpdateField]
    OHPCF_UPDATE_FIELD_PAUSABLE: _ClassVar[OHPCFUpdateField]
    OHPCF_UPDATE_FIELD_START_TIME: _ClassVar[OHPCFUpdateField]
    OHPCF_UPDATE_FIELD_REQUESTED_POWER_ESTIMATE: _ClassVar[OHPCFUpdateField]
    OHPCF_UPDATE_FIELD_REQUESTED_POWER_MAX: _ClassVar[OHPCFUpdateField]
    OHPCF_UPDATE_FIELD_MINIMAL_RUN_DURATION: _ClassVar[OHPCFUpdateField]
    OHPCF_UPDATE_FIELD_MINIMAL_PAUSE_DURATION: _ClassVar[OHPCFUpdateField]
COMPRESSOR_STATE_UNSPECIFIED: CompressorPowerConsumptionState
COMPRESSOR_STATE_AVAILABLE: CompressorPowerConsumptionState
COMPRESSOR_STATE_SCHEDULED: CompressorPowerConsumptionState
COMPRESSOR_STATE_RUNNING: CompressorPowerConsumptionState
COMPRESSOR_STATE_PAUSED: CompressorPowerConsumptionState
COMPRESSOR_STATE_COMPLETED: CompressorPowerConsumptionState
COMPRESSOR_STATE_STOPPED: CompressorPowerConsumptionState
OHPCF_ACTION_UNSPECIFIED: OHPCFAction
OHPCF_ACTION_SCHEDULE: OHPCFAction
OHPCF_ACTION_PAUSE: OHPCFAction
OHPCF_ACTION_RESUME: OHPCFAction
OHPCF_ACTION_ABORT: OHPCFAction
OHPCF_EVENT_UNSPECIFIED: OHPCFEventType
OHPCF_EVENT_SUPPORT_UPDATED: OHPCFEventType
OHPCF_EVENT_STATE_UPDATED: OHPCFEventType
OHPCF_EVENT_DATA_UPDATED: OHPCFEventType
OHPCF_UPDATE_FIELD_UNSPECIFIED: OHPCFUpdateField
OHPCF_UPDATE_FIELD_STATE: OHPCFUpdateField
OHPCF_UPDATE_FIELD_STOPPABLE: OHPCFUpdateField
OHPCF_UPDATE_FIELD_PAUSABLE: OHPCFUpdateField
OHPCF_UPDATE_FIELD_START_TIME: OHPCFUpdateField
OHPCF_UPDATE_FIELD_REQUESTED_POWER_ESTIMATE: OHPCFUpdateField
OHPCF_UPDATE_FIELD_REQUESTED_POWER_MAX: OHPCFUpdateField
OHPCF_UPDATE_FIELD_MINIMAL_RUN_DURATION: OHPCFUpdateField
OHPCF_UPDATE_FIELD_MINIMAL_PAUSE_DURATION: OHPCFUpdateField

class CompressorFlexibility(_message.Message):
    __slots__ = ("available", "requested_power_estimate_w", "requested_power_max_w", "is_stoppable", "is_pausable", "state", "minimal_run_seconds", "minimal_pause_seconds", "start_time")
    AVAILABLE_FIELD_NUMBER: _ClassVar[int]
    REQUESTED_POWER_ESTIMATE_W_FIELD_NUMBER: _ClassVar[int]
    REQUESTED_POWER_MAX_W_FIELD_NUMBER: _ClassVar[int]
    IS_STOPPABLE_FIELD_NUMBER: _ClassVar[int]
    IS_PAUSABLE_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    MINIMAL_RUN_SECONDS_FIELD_NUMBER: _ClassVar[int]
    MINIMAL_PAUSE_SECONDS_FIELD_NUMBER: _ClassVar[int]
    START_TIME_FIELD_NUMBER: _ClassVar[int]
    available: bool
    requested_power_estimate_w: float
    requested_power_max_w: float
    is_stoppable: bool
    is_pausable: bool
    state: CompressorPowerConsumptionState
    minimal_run_seconds: int
    minimal_pause_seconds: int
    start_time: _timestamp_pb2.Timestamp
    def __init__(self, available: bool = ..., requested_power_estimate_w: _Optional[float] = ..., requested_power_max_w: _Optional[float] = ..., is_stoppable: bool = ..., is_pausable: bool = ..., state: _Optional[_Union[CompressorPowerConsumptionState, str]] = ..., minimal_run_seconds: _Optional[int] = ..., minimal_pause_seconds: _Optional[int] = ..., start_time: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class ControlCompressorRequest(_message.Message):
    __slots__ = ("ski", "action", "start_time")
    SKI_FIELD_NUMBER: _ClassVar[int]
    ACTION_FIELD_NUMBER: _ClassVar[int]
    START_TIME_FIELD_NUMBER: _ClassVar[int]
    ski: str
    action: OHPCFAction
    start_time: _timestamp_pb2.Timestamp
    def __init__(self, ski: _Optional[str] = ..., action: _Optional[_Union[OHPCFAction, str]] = ..., start_time: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class OHPCFEvent(_message.Message):
    __slots__ = ("ski", "event_type", "flexibility", "update_field")
    SKI_FIELD_NUMBER: _ClassVar[int]
    EVENT_TYPE_FIELD_NUMBER: _ClassVar[int]
    FLEXIBILITY_FIELD_NUMBER: _ClassVar[int]
    UPDATE_FIELD_FIELD_NUMBER: _ClassVar[int]
    ski: str
    event_type: OHPCFEventType
    flexibility: CompressorFlexibility
    update_field: OHPCFUpdateField
    def __init__(self, ski: _Optional[str] = ..., event_type: _Optional[_Union[OHPCFEventType, str]] = ..., flexibility: _Optional[_Union[CompressorFlexibility, _Mapping]] = ..., update_field: _Optional[_Union[OHPCFUpdateField, str]] = ...) -> None: ...
