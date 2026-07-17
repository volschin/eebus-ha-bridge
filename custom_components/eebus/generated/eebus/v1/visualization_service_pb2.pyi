from . import common_pb2 as _common_pb2
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class PVData(_message.Message):
    __slots__ = ("power_w", "yield_wh", "peak_power_w", "sample")
    POWER_W_FIELD_NUMBER: _ClassVar[int]
    YIELD_WH_FIELD_NUMBER: _ClassVar[int]
    PEAK_POWER_W_FIELD_NUMBER: _ClassVar[int]
    SAMPLE_FIELD_NUMBER: _ClassVar[int]
    power_w: float
    yield_wh: float
    peak_power_w: float
    sample: _common_pb2.ProviderSampleMeta
    def __init__(self, power_w: _Optional[float] = ..., yield_wh: _Optional[float] = ..., peak_power_w: _Optional[float] = ..., sample: _Optional[_Union[_common_pb2.ProviderSampleMeta, _Mapping]] = ...) -> None: ...

class PVPeakPowerData(_message.Message):
    __slots__ = ("peak_power_w",)
    PEAK_POWER_W_FIELD_NUMBER: _ClassVar[int]
    peak_power_w: float
    def __init__(self, peak_power_w: _Optional[float] = ...) -> None: ...

class BatteryData(_message.Message):
    __slots__ = ("power_w", "charged_wh", "discharged_wh", "state_of_charge_pct", "sample")
    POWER_W_FIELD_NUMBER: _ClassVar[int]
    CHARGED_WH_FIELD_NUMBER: _ClassVar[int]
    DISCHARGED_WH_FIELD_NUMBER: _ClassVar[int]
    STATE_OF_CHARGE_PCT_FIELD_NUMBER: _ClassVar[int]
    SAMPLE_FIELD_NUMBER: _ClassVar[int]
    power_w: float
    charged_wh: float
    discharged_wh: float
    state_of_charge_pct: float
    sample: _common_pb2.ProviderSampleMeta
    def __init__(self, power_w: _Optional[float] = ..., charged_wh: _Optional[float] = ..., discharged_wh: _Optional[float] = ..., state_of_charge_pct: _Optional[float] = ..., sample: _Optional[_Union[_common_pb2.ProviderSampleMeta, _Mapping]] = ...) -> None: ...
