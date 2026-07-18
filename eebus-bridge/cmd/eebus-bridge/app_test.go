package main

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/volschin/eebus-bridge/internal/config"
	"github.com/volschin/eebus-bridge/internal/eebus"
	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
)

type fakeBridgeLifecycle struct {
	mu           sync.Mutex
	startErr     error
	started      chan struct{}
	startOnce    sync.Once
	starts       atomic.Int32
	stops        atomic.Int32
	recorder     *shutdownRecorder
	registers    []string
	unregisters  []string
	registered   chan struct{}
	registerOnce sync.Once
}

func (f *fakeBridgeLifecycle) Setup() error { return nil }

func (f *fakeBridgeLifecycle) Start() error {
	f.starts.Add(1)
	if f.started != nil {
		f.startOnce.Do(func() { close(f.started) })
	}
	return f.startErr
}

func (f *fakeBridgeLifecycle) Shutdown() {
	f.stops.Add(1)
	if f.recorder != nil {
		f.recorder.add("bridge")
	}
}

func (f *fakeBridgeLifecycle) RegisterRemoteSKI(ski string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registers = append(f.registers, ski)
	if f.registered != nil {
		f.registerOnce.Do(func() { close(f.registered) })
	}
}

func (f *fakeBridgeLifecycle) UnregisterRemoteSKI(ski string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unregisters = append(f.unregisters, ski)
}

func (f *fakeBridgeLifecycle) registerValues() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.registers...)
}

func (f *fakeBridgeLifecycle) unregisterValues() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.unregisters...)
}

type fakeGRPCLifecycle struct {
	startErr error
	release  chan struct{}
	stopOnce sync.Once
	starts   atomic.Int32
	stops    atomic.Int32
	recorder *shutdownRecorder

	healthMu sync.Mutex
	health   []bool
}

func (f *fakeGRPCLifecycle) Start() error {
	f.starts.Add(1)
	if f.startErr != nil {
		return f.startErr
	}
	if f.release != nil {
		<-f.release
	}
	return nil
}

func (f *fakeGRPCLifecycle) WaitReady(context.Context) error {
	return f.startErr
}

func (f *fakeGRPCLifecycle) Stop() {
	f.stops.Add(1)
	if f.recorder != nil {
		f.recorder.add("grpc")
	}
	if f.release != nil {
		f.stopOnce.Do(func() { close(f.release) })
	}
}

func (f *fakeGRPCLifecycle) SetHealthy(healthy bool) {
	f.healthMu.Lock()
	f.health = append(f.health, healthy)
	f.healthMu.Unlock()
}

func (f *fakeGRPCLifecycle) healthValues() []bool {
	f.healthMu.Lock()
	defer f.healthMu.Unlock()
	return append([]bool(nil), f.health...)
}

func TestOHPCFModuleRegistersGRPCServiceWhenDisabled(t *testing.T) {
	srv := bridgegrpc.NewServer("127.0.0.1", 0, false)
	module := newOHPCFModule(
		nil,
		nil,
		nil,
		bridgegrpc.NewOHPCFService(nil, eebus.NewEventBus(), eebus.NewDeviceRegistry()),
	)

	require.NoError(t, module.setup())
	names, err := module.registerUseCases()
	require.NoError(t, err)
	assert.Empty(t, names)

	module.registerGRPC(srv)
	assert.Contains(t, srv.GRPCServer().GetServiceInfo(), "eebus.v1.OHPCFService")
}

func TestProductionCompositionRootIsInertUntilStartAndRegistersAllUseCases(t *testing.T) {
	autoGenerate := true
	ohpcfEnabled := true
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	eebusPort := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	cfg := &config.Config{
		GRPC: config.GRPCConfig{
			Bind: "127.0.0.1", Port: 0,
			Security: config.GRPCSecurityConfig{Mode: config.GRPCSecurityModeLoopback},
		},
		EEBUS: config.EEBUSConfig{
			Port: eebusPort, Vendor: "test", Brand: "test", Model: "test", Serial: "test",
		},
		Certificates: config.CertificatesConfig{AutoGenerate: &autoGenerate, StoragePath: t.TempDir()},
		OHPCF:        config.OHPCFConfig{Enabled: &ohpcfEnabled},
	}

	app, err := NewApplication(cfg)
	require.NoError(t, err)
	t.Cleanup(app.Stop)
	require.False(t, app.prepared)
	server, ok := app.grpcSrv.(*bridgegrpc.Server)
	require.True(t, ok)
	assert.Empty(t, server.Addr(), "NewApplication must not start the gRPC listener")
	assert.Empty(t, app.registeredUseCases, "NewApplication must not run EEBUS setup")

	require.NoError(t, app.prepareForStart())
	assert.ElementsMatch(t, []string{
		"LPC", "Monitoring", "DHWMonitoring", "MRT", "MOT", "DHWTemperature",
		"DHWSystemFunction", "RoomHeatingTemperature", "RoomHeatingSystemFunction", "OHPCF",
	}, app.registeredUseCases)
}

type fakeHeartbeatLifecycle struct {
	starts   atomic.Int32
	stops    atomic.Int32
	recorder *shutdownRecorder
}

func (f *fakeHeartbeatLifecycle) StartHeartbeat(string) error {
	f.starts.Add(1)
	return nil
}

func (f *fakeHeartbeatLifecycle) StopHeartbeat() error {
	f.stops.Add(1)
	if f.recorder != nil {
		f.recorder.add("heartbeat")
	}
	return nil
}

type fakeMonitoringRegistry struct {
	mu           sync.Mutex
	stale        []string
	age          time.Duration
	hasAge       bool
	successAt    time.Time
	hasSuccess   bool
	threshold    time.Duration
	gracePeriod  time.Duration
	staleInvoked chan struct{}
	invokeOnce   sync.Once
	clearCalls   []string
}

type fakeClock struct {
	now time.Time
}

func (f *fakeClock) Now() time.Time {
	return f.now
}

func (f *fakeMonitoringRegistry) StaleDevices(threshold, gracePeriod time.Duration) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.threshold = threshold
	f.gracePeriod = gracePeriod
	if f.staleInvoked != nil {
		f.invokeOnce.Do(func() { close(f.staleInvoked) })
	}
	return append([]string(nil), f.stale...)
}

func (f *fakeMonitoringRegistry) MonitoringLastSuccessAge(string) (time.Duration, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.age, f.hasAge
}

func (f *fakeMonitoringRegistry) MonitoringSuccessSince(_ string, since time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hasSuccess && f.successAt.After(since)
}

func (f *fakeMonitoringRegistry) ClearEntities(ski string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clearCalls = append(f.clearCalls, ski)
}

func (f *fakeMonitoringRegistry) clearValues() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.clearCalls...)
}

func (f *fakeMonitoringRegistry) watchdogDurations() (time.Duration, time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.threshold, f.gracePeriod
}

func (f *fakeMonitoringRegistry) setStale(stale []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stale = append([]string(nil), stale...)
}

func (f *fakeMonitoringRegistry) recordSuccess(at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.successAt = at
	f.hasSuccess = true
}

type shutdownRecorder struct {
	mu    sync.Mutex
	order []string
}

func (r *shutdownRecorder) add(component string) {
	r.mu.Lock()
	r.order = append(r.order, component)
	r.mu.Unlock()
}

func (r *shutdownRecorder) values() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

func newTestApplication(
	bridge bridgeLifecycle,
	grpcServer grpcLifecycle,
	heartbeat heartbeatLifecycle,
	registry monitoringRegistry,
) *Application {
	return &Application{
		cfg:       &config.Config{},
		bridgeSvc: bridge,
		grpcSrv:   grpcServer,
		registry:  registry,
		modules: []applicationModule{{
			name: "heartbeat",
			stop: heartbeat.StopHeartbeat,
		}},
		monitoringWatchdogInterval: time.Millisecond,
		backgroundFailures:         make(chan backgroundFailure, 1),
	}
}

func TestApplicationStartReturnsPartialStartupFailure(t *testing.T) {
	startErr := errors.New("bridge start failed")
	bridge := &fakeBridgeLifecycle{startErr: startErr}
	grpcServer := &fakeGRPCLifecycle{release: make(chan struct{})}
	heartbeat := &fakeHeartbeatLifecycle{}
	app := newTestApplication(bridge, grpcServer, heartbeat, nil)

	err := app.Start(context.Background())

	require.ErrorIs(t, err, startErr)
	assert.Contains(t, err.Error(), "EEBUS service start")
	assert.Equal(t, int32(1), bridge.stops.Load())
	assert.Equal(t, int32(1), grpcServer.stops.Load())
	assert.Equal(t, int32(1), heartbeat.stops.Load())
}

func TestApplicationStartGRPCServeFailureTriggersControlledShutdown(t *testing.T) {
	serveErr := errors.New("serve failed")
	bridge := &fakeBridgeLifecycle{}
	grpcServer := &fakeGRPCLifecycle{startErr: serveErr}
	heartbeat := &fakeHeartbeatLifecycle{}
	app := newTestApplication(bridge, grpcServer, heartbeat, nil)

	err := app.Start(context.Background())

	require.Error(t, err)
	require.ErrorIs(t, err, serveErr)
	assert.Contains(t, err.Error(), "gRPC server readiness")
	assert.Equal(t, int32(1), bridge.stops.Load())
	assert.Equal(t, int32(1), grpcServer.stops.Load())
	assert.Equal(t, int32(1), heartbeat.stops.Load())
}

func TestApplicationStartWatchdogStartsDeviceScopedRecovery(t *testing.T) {
	bridge := &fakeBridgeLifecycle{registered: make(chan struct{})}
	grpcServer := &fakeGRPCLifecycle{release: make(chan struct{})}
	heartbeat := &fakeHeartbeatLifecycle{}
	registry := &fakeMonitoringRegistry{
		stale:        []string{"AA:BB:CC:DD:EE:FF"},
		age:          11 * time.Minute,
		hasAge:       true,
		staleInvoked: make(chan struct{}),
	}
	app := newTestApplication(bridge, grpcServer, heartbeat, registry)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	result := make(chan error, 1)
	go func() { result <- app.Start(ctx) }()

	select {
	case <-bridge.registered:
	case <-time.After(time.Second):
		t.Fatal("watchdog did not trigger device recovery")
	}
	cancel()

	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("application did not stop after context cancellation")
	}
	threshold, gracePeriod := registry.watchdogDurations()
	assert.Equal(t, monitoringStaleThreshold, threshold)
	assert.Equal(t, monitoringGracePeriod, gracePeriod)
	assert.Contains(t, grpcServer.healthValues(), false)
	assert.Equal(t, []string{"AABBCCDDEEFF"}, registry.clearValues())
	assert.Equal(t, []string{"AABBCCDDEEFF"}, bridge.unregisterValues())
	assert.Equal(t, []string{"AABBCCDDEEFF"}, bridge.registerValues())
	assert.Equal(t, int32(1), bridge.stops.Load())
	assert.Equal(t, int32(1), grpcServer.stops.Load())
	assert.Equal(t, int32(1), heartbeat.stops.Load())
}

func TestMonitoringRecoveryDoesNotTouchOtherDevicesOrRunInParallel(t *testing.T) {
	bridge := &fakeBridgeLifecycle{}
	grpcServer := &fakeGRPCLifecycle{}
	clock := &fakeClock{now: time.Unix(100, 0)}
	registry := eebus.NewDeviceRegistryWithClock(clock)
	targetEntity := mocks.NewEntityRemoteInterface(t)
	otherEntity := mocks.NewEntityRemoteInterface(t)
	for _, entity := range []*mocks.EntityRemoteInterface{targetEntity, otherEntity} {
		entity.On("Address").Return((*model.EntityAddressType)(nil)).Maybe()
		entity.On("EntityType").Return(model.EntityTypeTypeCEM).Maybe()
		entity.On("Features").Return([]spineapi.FeatureRemoteInterface(nil)).Maybe()
	}
	registry.AddDevice("AA11", eebus.DeviceInfo{})
	registry.UpsertObservation("AA11", nil, targetEntity, "monitoring")
	registry.MarkConnected("AA11")
	registry.AddDevice("BB22", eebus.DeviceInfo{})
	registry.UpsertObservation("BB22", nil, otherEntity, "monitoring")
	registry.MarkConnected("BB22")
	registry.RecordMonitoringSuccess("BB22")
	app := newTestApplication(bridge, grpcServer, &fakeHeartbeatLifecycle{}, registry)
	now := clock.now.Add(monitoringGracePeriod + time.Second)
	clock.now = now

	require.False(t, app.handleMonitoringWatchdogTick(now))
	require.False(t, app.handleMonitoringWatchdogTick(now.Add(time.Second)))

	assert.Equal(t, []string{"AA11"}, bridge.unregisterValues())
	assert.Equal(t, []string{"AA11"}, bridge.registerValues())
	target, ok := registry.GetDevice("AA11")
	require.True(t, ok)
	assert.Empty(t, target.RemoteEntities)
	other, ok := registry.GetDevice("BB22")
	require.True(t, ok)
	assert.Len(t, other.RemoteEntities, 1)
	assert.Same(t, otherEntity, other.RemoteEntities[0])
	assert.Equal(t, []bool{false, false}, grpcServer.healthValues())
}

func TestMonitoringRecoverySuccessfulRebindPreventsRestart(t *testing.T) {
	bridge := &fakeBridgeLifecycle{}
	grpcServer := &fakeGRPCLifecycle{}
	registry := &fakeMonitoringRegistry{stale: []string{"AA11"}}
	app := newTestApplication(bridge, grpcServer, &fakeHeartbeatLifecycle{}, registry)
	now := time.Unix(100, 0)

	require.False(t, app.handleMonitoringWatchdogTick(now))
	registry.setStale(nil)
	registry.recordSuccess(now.Add(time.Second))
	require.False(t, app.handleMonitoringWatchdogTick(now.Add(monitoringGracePeriod+time.Second)))

	assert.Empty(t, app.backgroundFailures)
	assert.Empty(t, app.deviceRecoveries)
	assert.Equal(t, []string{"AA11"}, registry.clearValues())
	assert.Equal(t, []string{"AA11"}, bridge.unregisterValues())
	assert.Equal(t, []string{"AA11"}, bridge.registerValues())
	assert.Equal(t, []bool{false, true}, grpcServer.healthValues())
}

func TestMonitoringRecoveryDisconnectedOrGraceDoesNotResetRecovery(t *testing.T) {
	bridge := &fakeBridgeLifecycle{}
	grpcServer := &fakeGRPCLifecycle{}
	registry := &fakeMonitoringRegistry{stale: []string{"AA11"}}
	app := newTestApplication(bridge, grpcServer, &fakeHeartbeatLifecycle{}, registry)
	now := time.Unix(100, 0)

	require.False(t, app.handleMonitoringWatchdogTick(now))
	registry.setStale(nil)
	require.False(t, app.handleMonitoringWatchdogTick(now.Add(time.Second)))
	require.False(t, app.handleMonitoringWatchdogTick(now.Add(monitoringGracePeriod+time.Second)))

	assert.Len(t, registry.clearValues(), 2)
	assert.Len(t, bridge.unregisterValues(), 2)
	assert.Len(t, bridge.registerValues(), 2)
	assert.Equal(t, deviceRecoveryRecovering, app.deviceRecoveries["AA11"].status)
	assert.Equal(t, 2, app.deviceRecoveries["AA11"].attempts)
	assert.Empty(t, app.backgroundFailures)
}

func TestMonitoringRecoveryPersistentFailureEscalatesOnce(t *testing.T) {
	bridge := &fakeBridgeLifecycle{}
	grpcServer := &fakeGRPCLifecycle{}
	registry := &fakeMonitoringRegistry{
		stale:  []string{"AA11"},
		age:    42 * time.Minute,
		hasAge: true,
	}
	app := newTestApplication(bridge, grpcServer, &fakeHeartbeatLifecycle{}, registry)
	now := time.Unix(100, 0)

	for attempt := 0; attempt < monitoringRecoveryMaxAttempts; attempt++ {
		require.False(t, app.handleMonitoringWatchdogTick(now.Add(time.Duration(attempt)*(monitoringGracePeriod+time.Second))))
	}
	require.True(t, app.handleMonitoringWatchdogTick(now.Add(time.Duration(monitoringRecoveryMaxAttempts)*(monitoringGracePeriod+time.Second))))
	require.False(t, app.handleMonitoringWatchdogTick(now.Add(time.Duration(monitoringRecoveryMaxAttempts+1)*(monitoringGracePeriod+time.Second))))

	assert.Len(t, app.backgroundFailures, 1)
	assert.Len(t, registry.clearValues(), monitoringRecoveryMaxAttempts)
	assert.Len(t, bridge.unregisterValues(), monitoringRecoveryMaxAttempts)
	assert.Len(t, bridge.registerValues(), monitoringRecoveryMaxAttempts)
	assert.Equal(t, []bool{false, false, false, false, false}, grpcServer.healthValues())
}

func TestApplicationStartSignalTriggersShutdown(t *testing.T) {
	bridge := &fakeBridgeLifecycle{started: make(chan struct{})}
	grpcServer := &fakeGRPCLifecycle{release: make(chan struct{})}
	heartbeat := &fakeHeartbeatLifecycle{}
	app := newTestApplication(bridge, grpcServer, heartbeat, nil)
	ctx, cancel := context.WithCancelCause(context.Background())
	result := make(chan error, 1)
	go func() { result <- app.Start(ctx) }()

	select {
	case <-bridge.started:
	case <-time.After(time.Second):
		t.Fatal("application did not start")
	}
	cancel(&signalShutdown{signal: syscall.SIGTERM})

	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("application did not stop after signal cancellation")
	}
	assert.Equal(t, int32(1), bridge.stops.Load())
	assert.Equal(t, int32(1), grpcServer.stops.Load())
	assert.Equal(t, int32(1), heartbeat.stops.Load())
	assert.Equal(t, []bool{true, false}, grpcServer.healthValues())
}

func TestApplicationStopIsIdempotentAndOrdered(t *testing.T) {
	recorder := &shutdownRecorder{}
	bridge := &fakeBridgeLifecycle{recorder: recorder}
	grpcServer := &fakeGRPCLifecycle{recorder: recorder, release: make(chan struct{})}
	heartbeat := &fakeHeartbeatLifecycle{recorder: recorder}
	app := newTestApplication(bridge, grpcServer, heartbeat, nil)

	require.NotPanics(t, func() {
		app.Stop()
		app.Stop()
	})

	assert.Equal(t, []string{"heartbeat", "grpc", "bridge"}, recorder.values())
	assert.Equal(t, int32(1), bridge.stops.Load())
	assert.Equal(t, int32(1), grpcServer.stops.Load())
	assert.Equal(t, int32(1), heartbeat.stops.Load())
}
