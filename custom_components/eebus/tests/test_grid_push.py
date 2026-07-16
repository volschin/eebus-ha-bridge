"""Tests for the coordinator's MGCP grid-data push path."""

from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock

from custom_components.eebus import proto_stubs
from custom_components.eebus.coordinator import EebusCoordinator
from custom_components.eebus.providers import (
    ENERGY_UNIT_TO_WH,
    POWER_UNIT_TO_W,
    ProviderManager,
)


def _state(value, unit):
    """Build a minimal HA state-like object."""
    return SimpleNamespace(state=value, attributes={"unit_of_measurement": unit})


def _make_grid_coordinator(states, power=None, feed_in=None, consumed=None):
    """Build a coordinator skeleton wired for grid-push tests."""
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
        grid_power_entity=power,
        grid_feed_in_energy_entity=feed_in,
        grid_consumption_energy_entity=consumed,
    )
    return coordinator


def test_read_sensor_value_normalizes_power_units():
    """kW is scaled to W; W passes through; export stays negative."""
    coordinator = _make_grid_coordinator({"sensor.p": _state("-1.5", "kW")}, power="sensor.p")
    assert (
        coordinator._provider_manager._read_sensor_value(
            "sensor.p", POWER_UNIT_TO_W, "power"
        )
        == -1500.0
    )


def test_read_sensor_value_normalizes_energy_units():
    """kWh is scaled to Wh."""
    coordinator = _make_grid_coordinator({"sensor.e": _state("12.34", "kWh")})
    assert (
        coordinator._provider_manager._read_sensor_value(
            "sensor.e", ENERGY_UNIT_TO_WH, "energy"
        )
        == 12340.0
    )


def test_read_sensor_value_unknown_unit_assumes_base():
    """Unknown unit falls back to the base unit (factor 1)."""
    coordinator = _make_grid_coordinator({"sensor.p": _state("42", None)})
    assert (
        coordinator._provider_manager._read_sensor_value(
            "sensor.p", POWER_UNIT_TO_W, "power"
        )
        == 42.0
    )


def test_read_sensor_value_unavailable_returns_none():
    """Unavailable / non-numeric states yield None so the field is omitted."""
    coordinator = _make_grid_coordinator(
        {"sensor.u": _state("unavailable", "W"), "sensor.x": _state("foo", "W")}
    )
    manager = coordinator._provider_manager
    assert manager._read_sensor_value("sensor.u", POWER_UNIT_TO_W, "power") is None
    assert manager._read_sensor_value("sensor.x", POWER_UNIT_TO_W, "power") is None
    assert manager._read_sensor_value(None, POWER_UNIT_TO_W, "power") is None


async def test_push_skips_without_power_entity():
    """No grid power mapped: push is a no-op and never builds a stub."""
    coordinator = _make_grid_coordinator({}, power=None)
    coordinator._ensure_channel = AsyncMock(side_effect=AssertionError("unexpected dial"))
    await coordinator.async_push_grid_data()  # must not raise


async def test_push_publishes_power_and_optional_energy(monkeypatch):
    """Mapped sensors are normalized and sent as a real GridData message."""
    states = {
        "sensor.power": _state("-2", "kW"),
        "sensor.feed": _state("5", "kWh"),
    }
    coordinator = _make_grid_coordinator(
        states, power="sensor.power", feed_in="sensor.feed", consumed="sensor.missing"
    )

    captured = {}

    class _FakeStub:
        def __init__(self, _channel):
            pass

        async def PublishGridData(self, request, timeout=None):  # noqa: N802
            captured["request"] = request
            return proto_stubs.Empty()

    monkeypatch.setattr(proto_stubs, "GridServiceStub", _FakeStub)

    await coordinator.async_push_grid_data()

    request = captured["request"]
    assert request.power_w == -2000.0
    assert request.HasField("feed_in_wh") is True
    assert request.feed_in_wh == 5000.0
    # Consumed sensor is absent → field omitted, not zero.
    assert request.HasField("consumed_wh") is False
