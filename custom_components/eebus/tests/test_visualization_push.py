"""Tests for the coordinator's VAPD/VABD display-data push paths."""

from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock

from custom_components.eebus import proto_stubs
from custom_components.eebus.coordinator import EebusCoordinator
from custom_components.eebus.providers import (
    SOC_UNIT_TO_PCT,
    ProviderManager,
    ProviderMappings,
)


def _state(value, unit):
    """Build a minimal HA state-like object."""
    return SimpleNamespace(state=value, attributes={"unit_of_measurement": unit})


def _make_coordinator(
    states,
    *,
    pv_power=None,
    pv_yield=None,
    pv_peak=None,
    battery_power=None,
    battery_charged=None,
    battery_discharged=None,
    battery_soc=None,
):
    """Build a coordinator skeleton wired for visualization-push tests."""
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator.ski = "test-ski"
    hass = MagicMock()
    hass.states.get = lambda entity_id: states.get(entity_id)
    coordinator.hass = hass
    coordinator._ensure_channel = AsyncMock(return_value=object())
    coordinator._provider_manager = ProviderManager(
        hass,
        coordinator.ski,
        lambda: coordinator._ensure_channel(),
        ProviderMappings(
            pv_power=pv_power,
            pv_yield_energy=pv_yield,
            pv_peak_power=pv_peak,
            battery_power=battery_power,
            battery_charged_energy=battery_charged,
            battery_discharged_energy=battery_discharged,
            battery_soc=battery_soc,
        ),
        supports_feature=lambda feature: feature
        == proto_stubs.FeatureId.FEATURE_PROVIDER_SAMPLE_INVALIDATION,
    )
    return coordinator


def test_read_sensor_value_normalizes_soc_percentage():
    """State of charge passes through as a plain percentage."""
    coordinator = _make_coordinator({"sensor.soc": _state("73", "%")})
    assert coordinator._provider_manager._read_sensor_value("sensor.soc", SOC_UNIT_TO_PCT, "battery SoC") == 73.0


def test_read_sensor_value_rejects_non_finite():
    """NaN/Inf states are dropped rather than advertised downstream."""
    coordinator = _make_coordinator({"sensor.bad": _state("nan", "%"), "sensor.inf": _state("inf", "%")})
    manager = coordinator._provider_manager
    assert manager._read_sensor_value("sensor.bad", SOC_UNIT_TO_PCT, "battery SoC") is None
    assert manager._read_sensor_value("sensor.inf", SOC_UNIT_TO_PCT, "battery SoC") is None


def test_read_sensor_value_enforces_range():
    """Values outside [minimum, maximum] are omitted."""
    coordinator = _make_coordinator({"sensor.soc": _state("250", "%"), "sensor.neg": _state("-5", "%")})
    # SoC capped at 100; negative energy/power rejected by minimum=0.
    assert (
        coordinator._provider_manager._read_sensor_value(
            "sensor.soc", SOC_UNIT_TO_PCT, "battery SoC", minimum=0, maximum=100
        )
        is None
    )
    assert (
        coordinator._provider_manager._read_sensor_value("sensor.neg", SOC_UNIT_TO_PCT, "PV power", minimum=0) is None
    )
    # In-range value still passes.
    assert (
        coordinator._provider_manager._read_sensor_value(
            "sensor.soc", SOC_UNIT_TO_PCT, "battery SoC", minimum=0, maximum=100
        )
        is None
    )


async def test_pv_push_skips_without_power_entity():
    """No PV power mapped: push is a no-op and never builds a stub."""
    coordinator = _make_coordinator({})
    coordinator._ensure_channel = AsyncMock(side_effect=AssertionError("unexpected dial"))
    await coordinator.async_push_pv_data()  # must not raise


async def test_pv_push_publishes_power_and_optional_fields(monkeypatch):
    """Mapped PV sensors are normalized and sent as a real PVData message."""
    states = {
        "sensor.pv_power": _state("3.2", "kW"),
        "sensor.pv_yield": _state("18", "kWh"),
    }
    coordinator = _make_coordinator(
        states,
        pv_power="sensor.pv_power",
        pv_yield="sensor.pv_yield",
        pv_peak="sensor.missing",
    )

    captured = {}

    class _FakeStub:
        def __init__(self, _channel):
            pass

        async def PublishPVData(self, request, timeout=None):  # noqa: N802
            captured["request"] = request
            return proto_stubs.Empty()

    monkeypatch.setattr(proto_stubs, "VisualizationServiceStub", _FakeStub)
    await coordinator.async_push_pv_data()

    request = captured["request"]
    assert request.HasField("sample") is True
    assert request.sample.invalid is False
    assert request.sample.valid_until.seconds > request.sample.observed_at.seconds
    assert request.power_w == 3200.0
    assert request.HasField("yield_wh") is True
    assert request.yield_wh == 18000.0
    # Peak-power sensor is absent → field omitted, not zero.
    assert request.HasField("peak_power_w") is False


async def test_pv_push_publishes_peak_power_separately(monkeypatch):
    """PV peak power is static config and is sent outside atomic PVData."""
    states = {
        "sensor.pv_power": _state("3.2", "kW"),
        "sensor.pv_peak": _state("5", "kW"),
    }
    coordinator = _make_coordinator(
        states,
        pv_power="sensor.pv_power",
        pv_peak="sensor.pv_peak",
    )
    captured = {}

    class _FakeStub:
        def __init__(self, _channel):
            pass

        async def PublishPVData(self, request, timeout=None):  # noqa: N802
            captured["pv"] = request
            return proto_stubs.Empty()

        async def PublishPVPeakPower(self, request, timeout=None):  # noqa: N802
            captured["peak"] = request
            return proto_stubs.Empty()

    monkeypatch.setattr(proto_stubs, "VisualizationServiceStub", _FakeStub)

    await coordinator.async_push_pv_data()

    assert captured["pv"].HasField("peak_power_w") is False
    assert captured["peak"].peak_power_w == 5000.0


async def test_pv_push_invalidates_when_power_unavailable(monkeypatch):
    """Unavailable required PV power sensor sends an explicit invalidation."""
    states = {"sensor.pv_power": _state("unavailable", "W")}
    coordinator = _make_coordinator(states, pv_power="sensor.pv_power")
    captured = {}

    class _FakeStub:
        def __init__(self, _channel):
            pass

        async def PublishPVData(self, request, timeout=None):  # noqa: N802
            captured["request"] = request
            return proto_stubs.Empty()

    monkeypatch.setattr(proto_stubs, "VisualizationServiceStub", _FakeStub)
    await coordinator.async_push_pv_data()

    request = captured["request"]
    assert request.HasField("sample") is True
    assert request.sample.invalid is True


async def test_battery_push_skips_without_power_entity():
    """No battery power mapped: push is a no-op and never builds a stub."""
    coordinator = _make_coordinator({})
    coordinator._ensure_channel = AsyncMock(side_effect=AssertionError("unexpected dial"))
    await coordinator.async_push_battery_data()  # must not raise


async def test_battery_push_publishes_power_and_optional_fields(monkeypatch):
    """Mapped battery sensors are normalized and sent as a real BatteryData message."""
    states = {
        "sensor.bat_power": _state("-1.5", "kW"),  # negative = charging
        "sensor.bat_soc": _state("64", "%"),
    }
    coordinator = _make_coordinator(
        states,
        battery_power="sensor.bat_power",
        battery_soc="sensor.bat_soc",
        battery_charged="sensor.missing",
    )

    captured = {}

    class _FakeStub:
        def __init__(self, _channel):
            pass

        async def PublishBatteryData(self, request, timeout=None):  # noqa: N802
            captured["request"] = request
            return proto_stubs.Empty()

    monkeypatch.setattr(proto_stubs, "VisualizationServiceStub", _FakeStub)
    await coordinator.async_push_battery_data()

    request = captured["request"]
    assert request.HasField("sample") is True
    assert request.sample.invalid is False
    assert request.sample.valid_until.seconds > request.sample.observed_at.seconds
    assert request.power_w == -1500.0
    assert request.HasField("state_of_charge_pct") is True
    assert request.state_of_charge_pct == 64.0
    # Charged-energy sensor is absent → field omitted, not zero.
    assert request.HasField("charged_wh") is False
    assert request.HasField("discharged_wh") is False


async def test_battery_push_invalidates_when_power_unavailable(monkeypatch):
    """Unavailable required battery power sensor sends an explicit invalidation."""
    states = {"sensor.bat_power": _state("unknown", "W")}
    coordinator = _make_coordinator(states, battery_power="sensor.bat_power")
    captured = {}

    class _FakeStub:
        def __init__(self, _channel):
            pass

        async def PublishBatteryData(self, request, timeout=None):  # noqa: N802
            captured["request"] = request
            return proto_stubs.Empty()

    monkeypatch.setattr(proto_stubs, "VisualizationServiceStub", _FakeStub)
    await coordinator.async_push_battery_data()

    request = captured["request"]
    assert request.HasField("sample") is True
    assert request.sample.invalid is True
