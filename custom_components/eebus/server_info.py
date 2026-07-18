"""Explicit bridge API and feature negotiation."""

from __future__ import annotations

from dataclasses import dataclass

import grpc
import grpc.aio

from . import proto_stubs
from .grpc_client import RPC_TIMEOUT, is_unimplemented

SUPPORTED_API_MAJOR = 1

_KNOWN_FEATURES = frozenset(
    {
        int(proto_stubs.FeatureId.FEATURE_EXPLICIT_CAPABILITIES),
        int(proto_stubs.FeatureId.FEATURE_CONSOLIDATED_DEVICE_STREAM),
        int(proto_stubs.FeatureId.FEATURE_DEVICE_SNAPSHOT),
        int(proto_stubs.FeatureId.FEATURE_PROVIDER_SAMPLE_INVALIDATION),
        int(proto_stubs.FeatureId.FEATURE_TYPED_MEASUREMENTS),
    }
)


class IncompatibleAPIMajorError(Exception):
    """Raised when client and bridge cannot share the v1 API contract."""

    def __init__(self, actual: int) -> None:
        super().__init__(f"bridge API major {actual} is incompatible with client major {SUPPORTED_API_MAJOR}")
        self.actual = actual


@dataclass(frozen=True, slots=True)
class BridgeContract:
    """Cached compatibility information for one bridge runtime."""

    api_major: int
    api_minor: int
    build_version: str
    features: frozenset[int]
    local_ski: str
    legacy: bool = False

    def supports(self, feature: int) -> bool:
        """Return whether the bridge explicitly advertises a feature."""
        return int(feature) in self.features


async def async_read_bridge_contract(
    channel: grpc.aio.Channel,
    *,
    timeout: float = RPC_TIMEOUT,
) -> BridgeContract:
    """Read ServerInfo once, with a documented GetStatus legacy fallback."""
    stub = proto_stubs.device_service_stub(channel)
    try:
        info = await stub.GetServerInfo(proto_stubs.Empty(), timeout=timeout)
    except grpc.aio.AioRpcError as err:
        if not is_unimplemented(err):
            raise
        status = await stub.GetStatus(proto_stubs.Empty(), timeout=timeout)
        return BridgeContract(
            api_major=SUPPORTED_API_MAJOR,
            api_minor=0,
            build_version="legacy",
            features=frozenset(),
            local_ski=str(status.local_ski),
            legacy=True,
        )

    major = int(info.api_major)
    if major != SUPPORTED_API_MAJOR:
        raise IncompatibleAPIMajorError(major)
    return BridgeContract(
        api_major=major,
        api_minor=int(info.api_minor),
        build_version=str(info.bridge_build_version),
        features=frozenset(int(feature) for feature in info.features if int(feature) in _KNOWN_FEATURES),
        local_ski=str(info.local_ski),
    )
