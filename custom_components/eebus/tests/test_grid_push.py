"""Tests for the coordinator's MGCP grid-data push path."""

from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock

from custom_components.eebus import proto_stubs
from custom_components.eebus.coordinator import EebusCoordinator
from custom_components.eebus.providers import (
    ENERGY_UNIT_TO_WH,
    POWER_UNIT_TO_W,
    ProviderManager,
    ProviderMappings,
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
        ProviderMappings(
            grid_power=power,
            grid_feed_in_energy=feed_in,
            grid_consumption_energy=consumed,
        ),
        supports_feature=lambda feature: feature
        == proto_stubs.FeatureId.FEATURE_PROVIDER_SAMPLE_INVALIDATION,
    )
    return coordinator


def test_read_sensor_value_normalizes_power_units():
    """kW is scaled to W; W passes through; export stays negative."""
    coordinator = _make_grid_coordinator({"sensor.p": _state("-1.5", "kW")}, power="sensor.p")
    assert coordinator._provider_manager._read_sensor_value("sensor.p", POWER_UNIT_TO_W, "power") == -1500.0


def test_read_sensor_value_normalizes_energy_units():
    """kWh is scaled to Wh."""
    coordinator = _make_grid_coordinator({"sensor.e": _state("12.34", "kWh")})
    assert coordinator._provider_manager._read_sensor_value("sensor.e", ENERGY_UNIT_TO_WH, "energy") == 12340.0


def test_read_sensor_value_unknown_unit_assumes_base():
    """Unknown unit falls back to the base unit (factor 1)."""
    coordinator = _make_grid_coordinator({"sensor.p": _state("42", None)})
    assert coordinator._provider_manager._read_sensor_value("sensor.p", POWER_UNIT_TO_W, "power") == 42.0


def test_read_sensor_value_unavailable_returns_none():
    """Unavailable / non-numeric states yield None so the field is omitted."""
    coordinator = _make_grid_coordinator({"sensor.u": _state("unavailable", "W"), "sensor.x": _state("foo", "W")})
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
    coordinator = _make_grid_coordinator(states, power="sensor.power", feed_in="sensor.feed", consumed="sensor.missing")

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
    assert request.HasField("sample") is True
    assert request.sample.invalid is False
    assert request.sample.valid_until.seconds > request.sample.observed_at.seconds
    assert request.power_w == -2000.0
    assert request.HasField("feed_in_wh") is True
    assert request.feed_in_wh == 5000.0
    # Consumed sensor is absent → field omitted, not zero.
    assert request.HasField("consumed_wh") is False


async def test_push_invalidates_when_power_unavailable(monkeypatch):
    """Unavailable required power sensor sends an explicit invalidation."""
    states = {"sensor.power": _state("unavailable", "W")}
    coordinator = _make_grid_coordinator(states, power="sensor.power")
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
    assert request.HasField("sample") is True
    assert request.sample.invalid is True


async def test_invalid_required_sensor_is_latched_until_valid_sample(monkeypatch):
    """One invalid streak emits one invalidation and resets after recovery."""
    states = {"sensor.power": _state("unavailable", "W")}
    coordinator = _make_grid_coordinator(states, power="sensor.power")
    requests = []

    class _FakeStub:
        def __init__(self, _channel):
            pass

        async def PublishGridData(self, request, timeout=None):  # noqa: N802
            requests.append(request)
            return proto_stubs.Empty()

    monkeypatch.setattr(proto_stubs, "GridServiceStub", _FakeStub)
    await coordinator.async_push_grid_data()
    await coordinator.async_push_grid_data()
    assert len(requests) == 1
    assert requests[0].sample.invalid is True

    states["sensor.power"] = _state("100", "W")
    await coordinator.async_push_grid_data()
    states["sensor.power"] = _state("unavailable", "W")
    await coordinator.async_push_grid_data()

    assert [request.sample.invalid for request in requests] == [True, False, True]
