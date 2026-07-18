"""Python client semantics against the production Go gRPC event adapter."""

from __future__ import annotations

import asyncio
import select
import shutil
import subprocess
from collections.abc import Iterator
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock

import pytest

from custom_components.eebus import proto_stubs
from custom_components.eebus.device_streams import DeviceStreams
from custom_components.eebus.grpc_client import GrpcChannelManager
from custom_components.eebus.server_info import async_read_bridge_contract
from custom_components.eebus.state import DeviceStateStore, StateField

ROOT = Path(__file__).resolve().parents[3]
SKI_A = "1111111111111111111111111111111111111111"
SKI_B = "2222222222222222222222222222222222222222"


def _terminate(process: subprocess.Popen[str]) -> None:
    process.terminate()
    try:
        process.wait(timeout=10)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait()


@pytest.fixture(scope="module")
def go_contract_server(tmp_path_factory: pytest.TempPathFactory) -> Iterator[str]:
    if shutil.which("go") is None:
        pytest.skip("Go toolchain not available; the cross-language contract test runs in CI")
    binary = tmp_path_factory.mktemp("contract-server") / "eebus-contract-testserver"
    subprocess.run(  # noqa: S603
        ["go", "build", "-o", str(binary), "./cmd/eebus-contract-testserver"],
        cwd=ROOT / "eebus-bridge",
        check=True,
    )
    stderr_path = tmp_path_factory.mktemp("contract-server-logs") / "stderr.log"
    with stderr_path.open("w", encoding="utf-8") as stderr_sink:
        process = subprocess.Popen(  # noqa: S603
            [str(binary)],
            cwd=ROOT / "eebus-bridge",
            stdout=subprocess.PIPE,
            stderr=stderr_sink,
            text=True,
        )
    try:
        assert process.stdout is not None
        readable, _, _ = select.select([process.stdout], [], [], 15)
        if not readable:
            raise AssertionError(f"Go contract server did not become ready: {stderr_path.read_text()}")
        ready = process.stdout.readline().strip()
        assert ready.startswith("READY "), ready
        yield ready.removeprefix("READY ")
    finally:
        _terminate(process)


async def _read_events(stream: object, count: int) -> list[object]:
    events = []
    for _ in range(count):
        events.append(await asyncio.wait_for(stream.read(), timeout=5))  # type: ignore[attr-defined]
    return events


@pytest.mark.cross_language
async def test_go_server_to_python_state_contract(go_contract_server: str) -> None:
    host, raw_port = go_contract_server.rsplit(":", 1)
    manager = GrpcChannelManager(host, int(raw_port), "loopback", None, None)
    channel = await manager.ensure_channel()
    contract = await async_read_bridge_contract(channel)
    assert contract.api_major == 1
    assert contract.build_version == "contract-test"
    assert contract.supports(proto_stubs.FeatureId.FEATURE_CONSOLIDATED_DEVICE_STREAM)
    assert contract.supports(proto_stubs.FeatureId.FEATURE_PROVIDER_SAMPLE_INVALIDATION)
    assert contract.supports(proto_stubs.FeatureId.FEATURE_DEVICE_SNAPSHOT)
    assert contract.supports(proto_stubs.FeatureId.FEATURE_TYPED_MEASUREMENTS)
    assert contract.supports(proto_stubs.FeatureId.FEATURE_OPERATIONAL_DIAGNOSTICS)

    stub = proto_stubs.device_service_stub(channel)
    stream_a = stub.SubscribeDeviceState(proto_stubs.DeviceRequest(ski=SKI_A))
    stream_b = stub.SubscribeDeviceState(proto_stubs.DeviceRequest(ski=SKI_B))
    initial_a, initial_b = await asyncio.gather(stream_a.read(), stream_b.read())
    assert initial_a.resync_required.reason == proto_stubs.ResyncReason.RESYNC_REASON_INITIAL_STATE_REQUIRED
    assert initial_b.resync_required.reason == proto_stubs.ResyncReason.RESYNC_REASON_INITIAL_STATE_REQUIRED

    await stub.RegisterRemoteSKI(proto_stubs.RegisterSKIRequest(ski=SKI_A), timeout=5)
    events_a = await _read_events(stream_a, 8)
    snapshot_a = await stub.GetDeviceSnapshot(proto_stubs.DeviceRequest(ski=SKI_A), timeout=5)
    assert snapshot_a.event_revision == 8
    assert snapshot_a.measurements[0].HasField("id")
    power_status = next(
        status
        for status in snapshot_a.field_states
        if status.id == proto_stubs.SnapshotFieldId.SNAPSHOT_FIELD_POWER
    )
    assert power_status.state == proto_stubs.SnapshotValueState.SNAPSHOT_VALUE_STATE_AVAILABLE
    initialized_stream = stub.SubscribeDeviceState(proto_stubs.DeviceRequest(ski=SKI_A))
    initialized = await asyncio.wait_for(initialized_stream.read(), timeout=5)
    assert initialized.revision == 8
    assert initialized.HasField("initial_snapshot")
    initial_publish = MagicMock()
    initial_refresh = AsyncMock()
    initial_consumer = DeviceStreams(
        MagicMock(), manager, SKI_A, DeviceStateStore(publish=initial_publish), initial_refresh, contract.supports
    )
    initial_consumer.handle_device_state_event(initialized)
    assert initial_consumer._store.state.measurements.power_watts == 600
    assert initial_consumer._store.state.lpc.heartbeat_status is not None
    initial_publish.assert_called_once()
    initial_refresh.assert_not_awaited()
    await stub.RegisterRemoteSKI(proto_stubs.RegisterSKIRequest(ski=SKI_B), timeout=5)
    events_b = await _read_events(stream_b, 8)
    snapshot_b = await stub.GetDeviceSnapshot(proto_stubs.DeviceRequest(ski=SKI_B), timeout=5)
    assert snapshot_a.ski == SKI_A.upper()
    assert snapshot_b.ski == SKI_B.upper()
    assert snapshot_a.measurements[0].value == 600
    assert snapshot_b.measurements[0].value == 700
    assert [event.revision for event in events_a] == list(range(1, 9))
    assert [event.revision for event in events_b] == list(range(1, 9))
    assert {event.ski for event in events_a} == {SKI_A.upper()}
    assert {event.ski for event in events_b} == {SKI_B.upper()}
    assert all(
        event.availability == proto_stubs.EventAvailability.EVENT_AVAILABILITY_AVAILABLE for event in events_a
    )
    assert events_a[1].measurement.event_type == proto_stubs.MeasurementEventType.MEASUREMENT_EVENT_UNSPECIFIED
    assert events_a[1].measurement.HasField("measurements")
    assert events_a[5].ohpcf.event_type in (
        proto_stubs.OHPCFEventType.OHPCF_EVENT_STATE_UPDATED,
        proto_stubs.OHPCFEventType.OHPCF_EVENT_DATA_UPDATED,
    )

    publish = MagicMock()
    refresh = AsyncMock()
    hass = MagicMock()
    hass.async_create_task.side_effect = lambda coroutine: coroutine.close()
    streams = DeviceStreams(
        hass,
        manager,
        SKI_A,
        DeviceStateStore(publish=publish),
        refresh,
        contract.supports,
    )
    for event in events_a:
        streams.handle_device_state_event(event)

    state = streams._store.state
    assert state.connection.connected is True
    assert state.measurements.power_l1_w == 100
    assert state.measurements.power_l2_w == 200
    assert state.measurements.power_l3_w == 300
    assert state.lpc.consumption_limit is not None
    assert state.lpc.consumption_limit.value_watts == 4200
    assert state.dhw.setpoint is not None
    assert state.dhw.setpoint.value_celsius == 50
    assert state.hvac.setpoint is not None
    assert state.hvac.setpoint.value_celsius == 21
    assert state.ohpcf.compressor_flexibility is not None
    assert StateField.COMPRESSOR_FLEXIBILITY in state.fresh_fields
    assert publish.call_count == 7
    refresh.assert_not_awaited()
    await manager.close()
