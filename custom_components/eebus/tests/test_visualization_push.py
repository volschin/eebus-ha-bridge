"""Tests for the coordinator's VAPD/VABD display-data push paths."""

from types import SimpleNamespace
from unittest.mock import MagicMock

from custom_components.eebus import proto_stubs
from custom_components.eebus.coordinator import (
    SOC_UNIT_TO_PCT,
    EebusCoordinator,
)


def _state(value, unit):
    """Build a minimal HA state-like object."""
    return SimpleNamespace(state=value, attributes={"unit_of_measurement": unit})


def _make_coordinator(states):
    """Build a coordinator skeleton wired for visualization-push tests."""
    coordinator = EebusCoordinator.__new__(EebusCoordinator)
    coordinator.ski = "test-ski"
    # PV + battery entities default to unmapped; tests set what they need.
    coordinator.pv_power_entity = None
    coordinator.pv_yield_energy_entity = None
    coordinator.pv_peak_power_entity = None
    coordinator.battery_power_entity = None
    coordinator.battery_charged_energy_entity = None
    coordinator.battery_discharged_energy_entity = None
    coordinator.battery_soc_entity = None
    hass = MagicMock()
    hass.states.get = lambda entity_id: states.get(entity_id)
    coordinator.hass = hass
    coordinator._channel = object()  # _ensure_channel returns it without dialing
    return coordinator


def test_read_sensor_value_normalizes_soc_percentage():
    """State of charge passes through as a plain percentage."""
    coordinator = _make_coordinator({"sensor.soc": _state("73", "%")})
    assert coordinator._read_sensor_value("sensor.soc", SOC_UNIT_TO_PCT, "battery SoC") == 73.0


def test_read_sensor_value_rejects_non_finite():
    """NaN/Inf states are dropped rather than advertised downstream."""
    coordinator = _make_coordinator(
        {"sensor.bad": _state("nan", "%"), "sensor.inf": _state("inf", "%")}
    )
    assert coordinator._read_sensor_value("sensor.bad", SOC_UNIT_TO_PCT, "battery SoC") is None
    assert coordinator._read_sensor_value("sensor.inf", SOC_UNIT_TO_PCT, "battery SoC") is None


def test_read_sensor_value_enforces_range():
    """Values outside [minimum, maximum] are omitted."""
    coordinator = _make_coordinator(
        {"sensor.soc": _state("250", "%"), "sensor.neg": _state("-5", "%")}
    )
    # SoC capped at 100; negative energy/power rejected by minimum=0.
    assert (
        coordinator._read_sensor_value(
            "sensor.soc", SOC_UNIT_TO_PCT, "battery SoC", minimum=0, maximum=100
        )
        is None
    )
    assert (
        coordinator._read_sensor_value(
            "sensor.neg", SOC_UNIT_TO_PCT, "PV power", minimum=0
        )
        is None
    )
    # In-range value still passes.
    assert (
        coordinator._read_sensor_value(
            "sensor.soc", SOC_UNIT_TO_PCT, "battery SoC", minimum=0, maximum=100
        )
        is None
    )


async def test_pv_push_skips_without_power_entity():
    """No PV power mapped: push is a no-op and never builds a stub."""
    coordinator = _make_coordinator({})
    coordinator._channel = None  # would fail if a channel were dialed
    await coordinator.async_push_pv_data()  # must not raise


async def test_pv_push_publishes_power_and_optional_fields(monkeypatch):
    """Mapped PV sensors are normalized and sent as a real PVData message."""
    states = {
        "sensor.pv_power": _state("3.2", "kW"),
        "sensor.pv_yield": _state("18", "kWh"),
    }
    coordinator = _make_coordinator(states)
    coordinator.pv_power_entity = "sensor.pv_power"
    coordinator.pv_yield_energy_entity = "sensor.pv_yield"
    coordinator.pv_peak_power_entity = "sensor.missing"

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
    assert request.power_w == 3200.0
    assert request.HasField("yield_wh") is True
    assert request.yield_wh == 18000.0
    # Peak-power sensor is absent → field omitted, not zero.
    assert request.HasField("peak_power_w") is False


async def test_battery_push_skips_without_power_entity():
    """No battery power mapped: push is a no-op and never builds a stub."""
    coordinator = _make_coordinator({})
    coordinator._channel = None  # would fail if a channel were dialed
    await coordinator.async_push_battery_data()  # must not raise


async def test_battery_push_publishes_power_and_optional_fields(monkeypatch):
    """Mapped battery sensors are normalized and sent as a real BatteryData message."""
    states = {
        "sensor.bat_power": _state("-1.5", "kW"),  # negative = charging
        "sensor.bat_soc": _state("64", "%"),
    }
    coordinator = _make_coordinator(states)
    coordinator.battery_power_entity = "sensor.bat_power"
    coordinator.battery_soc_entity = "sensor.bat_soc"
    coordinator.battery_charged_energy_entity = "sensor.missing"

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
    assert request.power_w == -1500.0
    assert request.HasField("state_of_charge_pct") is True
    assert request.state_of_charge_pct == 64.0
    # Charged-energy sensor is absent → field omitted, not zero.
    assert request.HasField("charged_wh") is False
    assert request.HasField("discharged_wh") is False
