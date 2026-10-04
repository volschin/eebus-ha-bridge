package eebus_test

import (
	"testing"
	"time"

	eglpc "github.com/enbility/eebus-go/usecases/eg/lpc"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
)

func TestCachedMonitoringReadsCannotHideMissingRemoteHeartbeat(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	registry := eebus.NewDeviceRegistryWithClock(clock)
	registry.MarkConnected("aa11")
	registry.RecordCapabilitySupport("aa11", eebus.CapabilityHeartbeat, true)
	lpc := usecases.NewLPCWrapper(nil, registry, false)
	lpc.HandleEvent("aa11", nil, nil, eglpc.DataUpdateHeartbeat)
	registry.RecordMonitoringSuccess("aa11")
	clock.Advance(3 * time.Minute)
	registry.RecordMonitoringSuccess("aa11") // successful lookup of cached SPINE entity
	if got := registry.StaleDevices(10*time.Minute, 2*time.Minute); len(got) != 1 || got[0] != "AA11" {
		t.Fatalf("missing heartbeat masked by cached lookup: stale=%v", got)
	}
	// A new receive event, rather than another cached read, restores liveness.
	lpc.HandleEvent("aa11", nil, nil, eglpc.DataUpdateHeartbeat)
	if got := registry.StaleDevices(10*time.Minute, 2*time.Minute); len(got) != 0 {
		t.Fatalf("fresh remote heartbeat did not restore liveness: %v", got)
	}
}

func TestReconnectRequiresNewRemoteHeartbeatBeforeRecoveryCompletes(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	registry := eebus.NewDeviceRegistryWithClock(clock)
	registry.MarkConnected("aa11")
	registry.RecordCapabilitySupport("aa11", eebus.CapabilityHeartbeat, true)
	lpc := usecases.NewLPCWrapper(nil, registry, false)
	lpc.HandleEvent("aa11", nil, nil, eglpc.DataUpdateHeartbeat)
	registry.RecordMonitoringSuccess("aa11")
	clock.Advance(time.Minute)
	registry.MarkDisconnected("aa11")
	lpc.HandleEvent("aa11", nil, nil, eglpc.UseCaseSupportUpdate) // scenario teardown
	registry.MarkConnected("aa11")
	attempt := clock.Now()
	clock.Advance(time.Second)
	registry.RecordMonitoringSuccess("aa11")
	health, _ := registry.DeviceHealth("aa11")
	if health.MonitoringSuccessOnConnect || registry.MonitoringSuccessSince("aa11", attempt) {
		t.Fatal("cached reads completed recovery without a heartbeat on the new connection")
	}
	lpc.HandleEvent("aa11", nil, nil, eglpc.DataUpdateHeartbeat)
	health, _ = registry.DeviceHealth("aa11")
	if !health.MonitoringSuccessOnConnect || !registry.MonitoringSuccessSince("aa11", attempt) {
		t.Fatal("fresh heartbeat and monitoring did not complete recovery")
	}
}

func TestHeartbeatSupportBeforeConnectionDoesNotCreateHealth(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	registry := eebus.NewDeviceRegistryWithClock(clock)
	registry.RecordCapabilitySupport("aa11", eebus.CapabilityHeartbeat, true)
	if _, known := registry.DeviceHealth("aa11"); known {
		t.Fatal("support discovery created an unconnected health record")
	}
	registry.MarkConnected("aa11")
	registry.RecordMonitoringSuccess("aa11")
	clock.Advance(3 * time.Minute)
	if len(registry.StaleDevices(10*time.Minute, 2*time.Minute)) != 1 {
		t.Fatal("support discovered before connection was not retained")
	}
}

type heartbeatReconnectController struct {
	registry *eebus.DeviceRegistry
	attempts int
}

func (c *heartbeatReconnectController) UnregisterRemoteSKI(ski string) {
	c.registry.MarkDisconnected(ski)
	c.registry.RecordCapabilitySupport(ski, eebus.CapabilityHeartbeat, false)
}

func (c *heartbeatReconnectController) RegisterRemoteSKI(ski string) {
	c.attempts++
	c.registry.MarkConnected(ski)
}

func TestHeartbeatSilenceExhaustsBoundedRecoveryDespiteCachedReads(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	registry := eebus.NewDeviceRegistryWithClock(clock)
	registry.MarkConnected("aa11")
	registry.RecordCapabilitySupport("aa11", eebus.CapabilityHeartbeat, true)
	registry.RecordRemoteHeartbeat("aa11")
	registry.RecordMonitoringSuccess("aa11")
	controller := &heartbeatReconnectController{registry: registry}
	supervisor := eebus.NewRecoverySupervisor(registry, controller, eebus.RecoveryConfig{
		StaleThreshold: 10 * time.Minute, GracePeriod: 2 * time.Minute,
		BaseBackoff: 2 * time.Minute, MaxBackoff: 2 * time.Minute, MaxAttempts: 3,
	})
	for attempt := 1; attempt <= 3; attempt++ {
		clock.Advance(3 * time.Minute)
		registry.RecordMonitoringSuccess("aa11")
		if supervisor.Tick(clock.Now()).RestartRequired || controller.attempts != attempt {
			t.Fatalf("attempt %d did not use bounded reconnect (count=%d)", attempt, controller.attempts)
		}
		clock.Advance(time.Second)
		registry.RecordMonitoringSuccess("aa11")
		supervisor.Tick(clock.Now())
		if supervisor.Snapshot("aa11", clock.Now()).Attempts != attempt {
			t.Fatal("cached read reset recovery attempts without a new heartbeat")
		}
	}
	clock.Advance(3 * time.Minute)
	registry.RecordMonitoringSuccess("aa11")
	if !supervisor.Tick(clock.Now()).RestartRequired || supervisor.Tick(clock.Now()).RestartRequired {
		t.Fatal("exhausted recovery must request exactly one restart")
	}
	// Recovery can still complete when real receive-side progress resumes.
	clock.Advance(time.Second)
	registry.RecordRemoteHeartbeat("aa11")
	registry.RecordMonitoringSuccess("aa11")
	supervisor.Tick(clock.Now())
	if supervisor.Snapshot("aa11", clock.Now()).State != eebus.RecoveryStateHealthy {
		t.Fatal("fresh heartbeat did not recover exhausted device")
	}
}

func TestHeartbeatRecoveryIsDeviceScopedAndIgnoresLateCallbacks(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	registry := eebus.NewDeviceRegistryWithClock(clock)
	for _, ski := range []string{"aa11", "bb22", "cc33"} {
		registry.MarkConnected(ski)
		registry.RecordMonitoringSuccess(ski)
	}
	registry.RecordCapabilitySupport("aa11", eebus.CapabilityHeartbeat, true)
	registry.RecordCapabilitySupport("bb22", eebus.CapabilityHeartbeat, true)
	registry.RecordCapabilitySupport("cc33", eebus.CapabilityHeartbeat, false)
	clock.Advance(3 * time.Minute)
	registry.RecordRemoteHeartbeat("aa11")
	if got := registry.StaleDevices(10*time.Minute, 2*time.Minute); len(got) != 1 || got[0] != "BB22" {
		t.Fatalf("fresh heartbeat must only rescue its own device; non-LPC uses monitoring: %v", got)
	}
	registry.MarkDisconnected("bb22")
	registry.RecordRemoteHeartbeat("bb22")
	registry.MarkConnected("bb22")
	registry.RecordMonitoringSuccess("bb22")
	if health, _ := registry.DeviceHealth("bb22"); health.MonitoringSuccessOnConnect {
		t.Fatal("late heartbeat while disconnected completed recovery")
	}
	registry.RemoveDevice("bb22")
	registry.RecordRemoteHeartbeat("bb22")
	registry.RecordCapabilitySupport("bb22", eebus.CapabilityHeartbeat, true)
	registry.RecordRemoteHeartbeat("unknown")
	if _, known := registry.DeviceHealth("bb22"); known {
		t.Fatal("late heartbeat resurrected removed device")
	}
	registry.MarkTrusted("bb22")
	registry.MarkConnected("bb22")
	registry.RecordMonitoringSuccess("bb22")
	if health, _ := registry.DeviceHealth("bb22"); !health.MonitoringSuccessOnConnect {
		t.Fatal("explicit removal did not clear remembered heartbeat requirement")
	}
}
