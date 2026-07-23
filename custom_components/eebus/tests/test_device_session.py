"""Tests for DeviceSession: typed write RPCs with gRPC error classification."""

import asyncio
from unittest.mock import AsyncMock, patch

import grpc
from grpc.aio import AioRpcError, Metadata

from custom_components.eebus import proto_stubs
from custom_components.eebus.device_session import DeviceSession, WriteOutcome


def _session(ski: str = "test-ski") -> DeviceSession:
    return DeviceSession(ski, AsyncMock(return_value=None))


def test_write_success_returns_none_status_code():
    session = _session()
    call = AsyncMock(return_value=None)

    outcome = asyncio.run(session._write("label", call, "request"))

    assert outcome == WriteOutcome(status_code=None)
    call.assert_awaited_once_with("request", timeout=10)


def test_write_unimplemented_is_classified_not_raised():
    session = _session()
    err = AioRpcError(
        grpc.StatusCode.UNIMPLEMENTED, Metadata(), Metadata(), details="no use case"
    )
    call = AsyncMock(side_effect=err)

    outcome = asyncio.run(session._write("label", call, "request"))

    assert outcome.status_code == grpc.StatusCode.UNIMPLEMENTED
    assert outcome.unimplemented is True
    assert outcome.validation_error is None
    assert outcome.error is err


def test_write_validation_error_classified_when_validation_true():
    session = _session()
    err = AioRpcError(
        grpc.StatusCode.INVALID_ARGUMENT, Metadata(), Metadata(), details="bad value"
    )
    call = AsyncMock(side_effect=err)

    outcome = asyncio.run(session._write("Setpoint write", call, "request", validation=True))

    assert outcome.status_code == grpc.StatusCode.INVALID_ARGUMENT
    assert outcome.unimplemented is False
    assert outcome.validation_error == "Setpoint write failed: bad value"
    assert outcome.error is err


def test_write_validation_code_ignored_when_validation_false():
    session = _session()
    err = AioRpcError(
        grpc.StatusCode.INVALID_ARGUMENT, Metadata(), Metadata(), details="bad value"
    )
    call = AsyncMock(side_effect=err)

    outcome = asyncio.run(session._write("label", call, "request", validation=False))

    assert outcome.validation_error is None
    assert outcome.status_code == grpc.StatusCode.INVALID_ARGUMENT
    assert outcome.error is err


def test_write_unclassified_error_returned_not_raised():
    session = _session()
    err = AioRpcError(
        grpc.StatusCode.UNAVAILABLE, Metadata(), Metadata(), details="not ready"
    )
    call = AsyncMock(side_effect=err)

    outcome = asyncio.run(session._write("label", call, "request"))

    assert outcome.status_code == grpc.StatusCode.UNAVAILABLE
    assert outcome.unimplemented is False
    assert outcome.validation_error is None
    assert outcome.error is err


def test_write_lpc_limit_calls_lpc_stub():
    session = DeviceSession("test-ski", AsyncMock(return_value="channel"))
    stub = AsyncMock()
    stub.WriteConsumptionLimit = AsyncMock()
    with patch.object(proto_stubs, "lpc_service_stub", return_value=stub) as factory:
        outcome = asyncio.run(session.write_lpc_limit(1500.0))

    factory.assert_called_once_with("channel")
    stub.WriteConsumptionLimit.assert_awaited_once()
    request = stub.WriteConsumptionLimit.await_args.args[0]
    assert request.ski == "test-ski"
    assert request.value_watts == 1500.0
    assert request.is_active is True
    assert outcome == WriteOutcome(status_code=None)


def test_write_failsafe_limit_calls_lpc_stub():
    session = DeviceSession("test-ski", AsyncMock(return_value="channel"))
    stub = AsyncMock()
    stub.WriteFailsafeLimit = AsyncMock()
    with patch.object(proto_stubs, "lpc_service_stub", return_value=stub):
        outcome = asyncio.run(session.write_failsafe_limit(3500.0))

    request = stub.WriteFailsafeLimit.await_args.args[0]
    assert request.ski == "test-ski"
    assert request.value_watts == 3500.0
    assert outcome == WriteOutcome(status_code=None)


def test_set_lpc_active_reads_current_limit_then_writes():
    session = DeviceSession("test-ski", AsyncMock(return_value="channel"))
    stub = AsyncMock()
    stub.GetConsumptionLimit = AsyncMock(
        return_value=proto_stubs.LoadLimit(value_watts=2000.0, is_active=False)
    )
    stub.WriteConsumptionLimit = AsyncMock()
    with patch.object(proto_stubs, "lpc_service_stub", return_value=stub):
        outcome = asyncio.run(session.set_lpc_active(True))

    stub.GetConsumptionLimit.assert_awaited_once()
    request = stub.WriteConsumptionLimit.await_args.args[0]
    assert request.value_watts == 2000.0
    assert request.is_active is True
    assert outcome == WriteOutcome(status_code=None)


def test_control_compressor_classifies_precondition_as_validation_error():
    session = DeviceSession("test-ski", AsyncMock(return_value="channel"))
    err_stub = AsyncMock()
    err = AioRpcError(
        grpc.StatusCode.FAILED_PRECONDITION,
        Metadata(),
        Metadata(),
        details="process is not pausable",
    )
    err_stub.ControlCompressorFlexibility = AsyncMock(side_effect=err)
    with patch.object(proto_stubs, "ohpcf_service_stub", return_value=err_stub):
        outcome = asyncio.run(
            session.control_compressor(
                proto_stubs.OHPCFAction.OHPCF_ACTION_SCHEDULE
            )
        )

    assert (
        outcome.validation_error
        == "OHPCF control failed: process is not pausable"
    )
    assert outcome.error is err


def test_write_dhw_setpoint_uses_validation():
    session = DeviceSession("test-ski", AsyncMock(return_value="channel"))
    stub = AsyncMock()
    err = AioRpcError(
        grpc.StatusCode.INVALID_ARGUMENT, Metadata(), Metadata(), details="out of range"
    )
    stub.SetDHWSetpoint = AsyncMock(side_effect=err)
    with patch.object(proto_stubs, "dhw_service_stub", return_value=stub):
        outcome = asyncio.run(session.write_dhw_setpoint(55.0))

    assert outcome.validation_error == "Domestic hot water setpoint failed: out of range"


def test_set_dhw_boost_calls_dhw_stub():
    session = DeviceSession("test-ski", AsyncMock(return_value="channel"))
    stub = AsyncMock()
    stub.SetDHWBoost = AsyncMock()
    with patch.object(proto_stubs, "dhw_service_stub", return_value=stub):
        outcome = asyncio.run(session.set_dhw_boost(True))

    request = stub.SetDHWBoost.await_args.args[0]
    assert request.active is True
    assert outcome == WriteOutcome(status_code=None)


def test_set_dhw_operation_mode_calls_dhw_stub():
    session = DeviceSession("test-ski", AsyncMock(return_value="channel"))
    stub = AsyncMock()
    stub.SetDHWOperationMode = AsyncMock()
    with patch.object(proto_stubs, "dhw_service_stub", return_value=stub):
        outcome = asyncio.run(session.set_dhw_operation_mode("hotWaterNormal"))

    request = stub.SetDHWOperationMode.await_args.args[0]
    assert request.mode == "hotWaterNormal"
    assert outcome == WriteOutcome(status_code=None)


def test_set_room_heating_temperature_calls_hvac_stub():
    session = DeviceSession("test-ski", AsyncMock(return_value="channel"))
    stub = AsyncMock()
    stub.SetRoomHeatingTemperature = AsyncMock()
    with patch.object(proto_stubs, "hvac_service_stub", return_value=stub):
        outcome = asyncio.run(session.set_room_heating_temperature(21.5))

    request = stub.SetRoomHeatingTemperature.await_args.args[0]
    assert request.value_celsius == 21.5
    assert outcome == WriteOutcome(status_code=None)


def test_set_room_heating_mode_calls_hvac_stub():
    session = DeviceSession("test-ski", AsyncMock(return_value="channel"))
    stub = AsyncMock()
    stub.SetRoomHeatingMode = AsyncMock()
    with patch.object(proto_stubs, "hvac_service_stub", return_value=stub):
        outcome = asyncio.run(session.set_room_heating_mode("auto"))

    request = stub.SetRoomHeatingMode.await_args.args[0]
    assert request.mode == "auto"
    assert outcome == WriteOutcome(status_code=None)
