package eebus

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recoveryRegistryFake struct {
	mu        sync.Mutex
	stale     []string
	health    []DeviceHealthSnapshot
	successAt map[string]time.Time
	clears    []string
}

func (f *recoveryRegistryFake) StaleDevices(time.Duration, time.Duration) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.stale...)
}

func (f *recoveryRegistryFake) MonitoringLastSuccessAge(string) (time.Duration, bool) {
	return 0, false
}

func (f *recoveryRegistryFake) MonitoringSuccessSince(ski string, since time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.successAt[NormalizeSKI(ski)].After(since)
}

func (f *recoveryRegistryFake) ClearEntities(ski string) {
	f.mu.Lock()
	f.clears = append(f.clears, NormalizeSKI(ski))
	f.mu.Unlock()
}

func (f *recoveryRegistryFake) DeviceHealth(ski string) (DeviceHealthSnapshot, bool) {
	ski = NormalizeSKI(ski)
	for _, device := range f.ListDeviceHealth() {
		if device.SKI == ski {
			return device, true
		}
	}
	return DeviceHealthSnapshot{}, false
}

func (f *recoveryRegistryFake) ListDeviceHealth() []DeviceHealthSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]DeviceHealthSnapshot(nil), f.health...)
}

type recoveryControllerFake struct {
	registers    atomic.Int32
	unregisters  atomic.Int32
	onUnregister func()
}

func (f *recoveryControllerFake) RegisterRemoteSKI(string) { f.registers.Add(1) }

func (f *recoveryControllerFake) UnregisterRemoteSKI(string) {
	f.unregisters.Add(1)
	if f.onUnregister != nil {
		f.onUnregister()
	}
}

func recoveryTestConfig() RecoveryConfig {
	return RecoveryConfig{
		StaleThreshold: time.Minute,
		GracePeriod:    time.Minute,
		BaseBackoff:    time.Minute,
		MaxBackoff:     4 * time.Minute,
		MaxAttempts:    3,
	}
}

func TestRecoverySupervisorDoesNotHoldStateLockAcrossRecoveryIOAndIsSingleFlight(t *testing.T) {
	now := time.Unix(100, 0)
	registry := &recoveryRegistryFake{
		stale:     []string{"aa:11"},
		health:    []DeviceHealthSnapshot{{SKI: "AA11", Connected: true}},
		successAt: make(map[string]time.Time),
	}
	controller := &recoveryControllerFake{}
	supervisor := NewRecoverySupervisor(registry, controller, recoveryTestConfig())
	entered := make(chan struct{})
	release := make(chan struct{})
	controller.onUnregister = func() {
		if state := supervisor.Snapshot("AA11", now).State; state != RecoveryStateRecovering {
			t.Errorf("state during external recovery = %s, want recovering", state)
		}
		close(entered)
		<-release
	}

	done := make(chan struct{})
	go func() {
		supervisor.Tick(now)
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("recovery did not reach external controller")
	}

	secondDone := make(chan struct{})
	go func() {
		supervisor.Tick(now)
		close(secondDone)
	}()
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("concurrent Tick blocked on external recovery")
	}
	if got := controller.unregisters.Load(); got != 1 {
		t.Fatalf("concurrent unregisters = %d, want one", got)
	}

	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("first recovery did not finish")
	}
	snapshot := supervisor.Snapshot("AA11", now)
	if snapshot.State != RecoveryStateGracePeriod || snapshot.Attempts != 1 {
		t.Fatalf("snapshot after recovery = %+v", snapshot)
	}
}

func TestRecoverySupervisorKeepsHealthyDeviceServingUntilAllRelevantDevicesExhausted(t *testing.T) {
	now := time.Unix(100, 0)
	registry := &recoveryRegistryFake{
		stale: []string{"BB22"},
		health: []DeviceHealthSnapshot{
			{SKI: "AA11", Connected: true, MonitoringSuccessOnConnect: true},
			{SKI: "BB22", Connected: true},
		},
		successAt: make(map[string]time.Time),
	}
	config := recoveryTestConfig()
	config.MaxAttempts = 1
	controller := &recoveryControllerFake{}
	supervisor := NewRecoverySupervisor(registry, controller, config)

	if result := supervisor.Tick(now); result.RestartRequired {
		t.Fatal("first device-scoped recovery requested restart")
	}
	result := supervisor.Tick(now.Add(time.Minute))
	if result.RestartRequired {
		t.Fatal("one exhausted device requested restart while another is healthy")
	}
	if state := supervisor.Snapshot("AA11", now).State; state != RecoveryStateHealthy {
		t.Fatalf("healthy device state = %s", state)
	}
	if state := supervisor.Snapshot("BB22", now).State; state != RecoveryStateExhausted {
		t.Fatalf("failed device state = %s", state)
	}

	registry.mu.Lock()
	registry.stale = []string{"AA11", "BB22"}
	registry.mu.Unlock()
	supervisor.Tick(now.Add(2 * time.Minute))
	result = supervisor.Tick(now.Add(3 * time.Minute))
	if !result.RestartRequired || len(result.ExhaustedSKIs) != 2 {
		t.Fatalf("all-device exhaustion = %+v, want one restart", result)
	}
	if again := supervisor.Tick(now.Add(4 * time.Minute)); again.RestartRequired {
		t.Fatal("unchanged exhaustion requested a duplicate restart")
	}
}

func TestRecoverySupervisorMonitoringSuccessEndsRecoveryDeterministically(t *testing.T) {
	now := time.Unix(100, 0)
	registry := &recoveryRegistryFake{
		stale:     []string{"AA11"},
		health:    []DeviceHealthSnapshot{{SKI: "AA11", Connected: true}},
		successAt: make(map[string]time.Time),
	}
	supervisor := NewRecoverySupervisor(registry, &recoveryControllerFake{}, recoveryTestConfig())
	supervisor.Tick(now)

	registry.mu.Lock()
	registry.stale = nil
	registry.successAt["AA11"] = now.Add(time.Second)
	registry.health[0].MonitoringSuccessOnConnect = true
	registry.health[0].LastMonitoringSuccess = now.Add(time.Second)
	registry.mu.Unlock()
	supervisor.Tick(now.Add(time.Minute))

	snapshot := supervisor.Snapshot("AA11", now.Add(time.Minute))
	if snapshot.State != RecoveryStateHealthy || snapshot.Attempts != 0 {
		t.Fatalf("snapshot after monitoring success = %+v", snapshot)
	}
}

func TestRecoverySupervisorStartsFirstStaleAgeAtFirstStaleEpisode(t *testing.T) {
	now := time.Unix(100, 0)
	registry := &recoveryRegistryFake{
		health: []DeviceHealthSnapshot{{
			SKI: "AA11", Connected: true, MonitoringSuccessOnConnect: true,
		}},
		successAt: make(map[string]time.Time),
	}
	supervisor := NewRecoverySupervisor(registry, &recoveryControllerFake{}, recoveryTestConfig())
	supervisor.Tick(now)
	if snapshot := supervisor.Snapshot("AA11", now); !snapshot.FirstStaleAt.IsZero() {
		t.Fatalf("healthy device first stale time = %s, want zero", snapshot.FirstStaleAt)
	}

	staleAt := now.Add(10 * time.Minute)
	registry.mu.Lock()
	registry.stale = []string{"AA11"}
	registry.health[0].MonitoringSuccessOnConnect = false
	registry.mu.Unlock()
	supervisor.Tick(staleAt)

	if got := supervisor.Snapshot("AA11", staleAt).FirstStaleAt; !got.Equal(staleAt) {
		t.Fatalf("first stale time = %s, want %s", got, staleAt)
	}
}

func TestRecoverySupervisorMonitoringSuccessRevivesExhaustedDevice(t *testing.T) {
	now := time.Unix(100, 0)
	registry := &recoveryRegistryFake{
		stale:     []string{"AA11"},
		health:    []DeviceHealthSnapshot{{SKI: "AA11", Connected: true}},
		successAt: make(map[string]time.Time),
	}
	config := recoveryTestConfig()
	config.MaxAttempts = 1
	supervisor := NewRecoverySupervisor(registry, &recoveryControllerFake{}, config)
	supervisor.Tick(now)
	supervisor.Tick(now.Add(time.Minute))
	if state := supervisor.Snapshot("AA11", now).State; state != RecoveryStateExhausted {
		t.Fatalf("state before recovery read = %s, want exhausted", state)
	}

	registry.mu.Lock()
	registry.stale = nil
	registry.successAt["AA11"] = now.Add(2 * time.Minute)
	registry.health[0].MonitoringSuccessOnConnect = true
	registry.health[0].LastMonitoringSuccess = now.Add(2 * time.Minute)
	registry.mu.Unlock()
	supervisor.Tick(now.Add(2 * time.Minute))

	snapshot := supervisor.Snapshot("AA11", now.Add(2*time.Minute))
	if snapshot.State != RecoveryStateHealthy || snapshot.Attempts != 0 || !snapshot.NextAttemptAt.IsZero() {
		t.Fatalf("snapshot after recovery read = %+v, want healthy", snapshot)
	}

	registry.mu.Lock()
	registry.stale = []string{"AA11"}
	registry.health[0].MonitoringSuccessOnConnect = false
	registry.mu.Unlock()
	supervisor.Tick(now.Add(3 * time.Minute))

	snapshot = supervisor.Snapshot("AA11", now.Add(3*time.Minute))
	if snapshot.State != RecoveryStateGracePeriod || snapshot.Attempts != 1 {
		t.Fatalf("new stale episode = %+v, want a fresh first recovery attempt", snapshot)
	}
}

func TestRecoverySupervisorSnapshotDistinguishesUnknownDisconnectedAndGrace(t *testing.T) {
	now := time.Unix(100, 0)
	registry := &recoveryRegistryFake{
		health: []DeviceHealthSnapshot{
			{SKI: "AA11", Connected: false, LastTransitionAt: now},
			{SKI: "BB22", Connected: true, LastTransitionAt: now},
		},
		successAt: make(map[string]time.Time),
	}
	supervisor := NewRecoverySupervisor(registry, nil, recoveryTestConfig())

	if state := supervisor.Snapshot("missing", now).State; state != RecoveryStateUnknown {
		t.Fatalf("unknown state = %s", state)
	}
	if state := supervisor.Snapshot("AA11", now).State; state != RecoveryStateDisconnected {
		t.Fatalf("disconnected state = %s", state)
	}
	if state := supervisor.Snapshot("BB22", now).State; state != RecoveryStateGracePeriod {
		t.Fatalf("grace state = %s", state)
	}
}
