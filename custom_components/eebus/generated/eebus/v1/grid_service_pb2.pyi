from . import common_pb2 as _common_pb2
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Optional as _Optional

DESCRIPTOR: _descriptor.FileDescriptor

class GridData(_message.Message):
    __slots__ = ("power_w", "feed_in_wh", "consumed_wh")
    POWER_W_FIELD_NUMBER: _ClassVar[int]
    FEED_IN_WH_FIELD_NUMBER: _ClassVar[int]
    CONSUMED_WH_FIELD_NUMBER: _ClassVar[int]
    power_w: float
    feed_in_wh: float
    consumed_wh: float
    def __init__(self, power_w: _Optional[float] = ..., feed_in_wh: _Optional[float] = ..., consumed_wh: _Optional[float] = ...) -> None: ...
