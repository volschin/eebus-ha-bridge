"""Contract negotiation and compatibility-matrix tests."""

from unittest.mock import AsyncMock, MagicMock, patch

import grpc
import pytest
from grpc.aio import AioRpcError, Metadata

from custom_components.eebus import proto_stubs
from custom_components.eebus.runtime import BridgeRuntime, BridgeRuntimeKey
from custom_components.eebus.server_info import (
    BridgeContract,
    IncompatibleAPIMajorError,
    async_read_bridge_contract,
)


def _rpc_error(code: grpc.StatusCode) -> AioRpcError:
    return AioRpcError(code, Metadata(), Metadata(), details=code.name)


async def test_new_client_negotiates_new_bridge_and_ignores_unknown_features() -> None:
    response = proto_stubs.ServerInfo(
        api_major=1,
        api_minor=2,
        bridge_build_version="v1.2.3",
        local_ski="LOCAL",
        features=[
            proto_stubs.FeatureId.FEATURE_CONSOLIDATED_DEVICE_STREAM,
            proto_stubs.FeatureId.FEATURE_PROVIDER_SAMPLE_INVALIDATION,
            proto_stubs.FeatureId.FEATURE_OPERATIONAL_DIAGNOSTICS,
            999,
        ],
    )
    stub = MagicMock(GetServerInfo=AsyncMock(return_value=response))
    with patch("custom_components.eebus.proto_stubs.device_service_stub", return_value=stub):
        contract = await async_read_bridge_contract(MagicMock())

    assert contract.api_major == 1
    assert contract.build_version == "v1.2.3"
    assert contract.local_ski == "LOCAL"
    assert contract.legacy is False
    assert contract.supports(proto_stubs.FeatureId.FEATURE_CONSOLIDATED_DEVICE_STREAM)
    assert contract.supports(proto_stubs.FeatureId.FEATURE_OPERATIONAL_DIAGNOSTICS)
    assert 999 not in contract.features


async def test_new_client_uses_documented_old_bridge_fallback() -> None:
    stub = MagicMock(
        GetServerInfo=AsyncMock(side_effect=_rpc_error(grpc.StatusCode.UNIMPLEMENTED)),
        GetStatus=AsyncMock(return_value=proto_stubs.ServiceStatus(running=True, local_ski="LEGACY")),
    )
    with patch("custom_components.eebus.proto_stubs.device_service_stub", return_value=stub):
        contract = await async_read_bridge_contract(MagicMock())

    assert contract.legacy is True
    assert contract.features == frozenset()
    assert contract.local_ski == "LEGACY"
    stub.GetStatus.assert_awaited_once()


async def test_incompatible_major_is_rejected() -> None:
    stub = MagicMock(GetServerInfo=AsyncMock(return_value=proto_stubs.ServerInfo(api_major=2)))
    with (
        patch("custom_components.eebus.proto_stubs.device_service_stub", return_value=stub),
        pytest.raises(IncompatibleAPIMajorError),
    ):
        await async_read_bridge_contract(MagicMock())


async def test_runtime_caches_contract_across_device_sessions() -> None:
    runtime = BridgeRuntime(
        BridgeRuntimeKey.from_connection("bridge", 50051, "loopback", None, None),
        None,
        None,
    )
    channel = MagicMock()
    channel.close = AsyncMock()
    reader = AsyncMock(
        return_value=BridgeContract(
            1,
            0,
            "test",
            frozenset({int(proto_stubs.FeatureId.FEATURE_CONSOLIDATED_DEVICE_STREAM)}),
            "LOCAL",
        )
    )
    with (
        patch("custom_components.eebus.grpc_client.create_grpc_channel", return_value=channel),
        patch("custom_components.eebus.runtime.async_read_bridge_contract", reader),
    ):
        first = await runtime.ensure_contract()
        second = await runtime.ensure_contract()

    assert first is second
    reader.assert_awaited_once()


async def test_runtime_renegotiates_contract_on_channel_rebuild() -> None:
    runtime = BridgeRuntime(
        BridgeRuntimeKey.from_connection("bridge", 50051, "loopback", None, None),
        None,
        None,
    )
    channels = [MagicMock(), MagicMock()]
    for channel in channels:
        channel.close = AsyncMock()
    old_contract = BridgeContract(1, 0, "old", frozenset(), "LOCAL")
    diagnostics_feature = int(
        proto_stubs.FeatureId.FEATURE_OPERATIONAL_DIAGNOSTICS
    )
    new_contract = BridgeContract(
        1, 1, "new", frozenset({diagnostics_feature}), "LOCAL"
    )
    reader = AsyncMock(side_effect=[old_contract, new_contract])
    active_session = MagicMock()
    runtime._sessions["AABB"] = active_session

    with (
        patch(
            "custom_components.eebus.grpc_client.create_grpc_channel",
            side_effect=channels,
        ),
        patch("custom_components.eebus.runtime.async_read_bridge_contract", reader),
    ):
        assert await runtime.ensure_contract() is old_contract
        await runtime.channel_manager.invalidate()
        assert await runtime.ensure_contract() is new_contract

    assert runtime.supports(diagnostics_feature) is True
    assert reader.await_count == 2
    active_session.streams.contract_changed.assert_called_once_with()
