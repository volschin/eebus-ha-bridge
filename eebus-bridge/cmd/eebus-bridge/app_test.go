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
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/config"
	"github.com/volschin/eebus-bridge/internal/eebus"
	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
	"github.com/volschin/eebus-bridge/internal/usecases"
)

type fakeBridgeLifecycle struct {
	mu           sync.Mutex
	setupErr     error
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
	setupStarted chan struct{}
	setupRelease chan struct{}
	setupOnce    sync.Once
}

func (f *fakeBridgeLifecycle) Setup() error {
	if f.setupStarted != nil {
		f.setupOnce.Do(func() { close(f.setupStarted) })
	}
	if f.setupRelease != nil {
		<-f.setupRelease
	}
	return f.setupErr
}

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
	startErr  error
	release   chan struct{}
	stopOnce  sync.Once
	serveOnce sync.Once
	serving   chan struct{}
	starts    atomic.Int32
	stops     atomic.Int32
	recorder  *shutdownRecorder

	healthMu sync.Mutex
	health   []bool

	deviceHealthMu sync.Mutex
	deviceHealth   []bool
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
	if healthy && f.serving != nil {
		f.serveOnce.Do(func() { close(f.serving) })
	}
}

func (f *fakeGRPCLifecycle) SetDeviceHealthy(healthy bool) {
	f.deviceHealthMu.Lock()
	f.deviceHealth = append(f.deviceHealth, healthy)
	f.deviceHealthMu.Unlock()
}

func (f *fakeGRPCLifecycle) deviceHealthValues() []bool {
	f.deviceHealthMu.Lock()
	defer f.deviceHealthMu.Unlock()
	return append([]bool(nil), f.deviceHealth...)
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
		Experimental: config.ExperimentalConfig{
			MGCPProvider: true,
			VAPDProvider: true,
			VABDProvider: true,
		},
	}

	app, err := NewApplication(cfg)
	require.NoError(t, err)
	t.Cleanup(app.Stop)
	require.False(t, app.prepared)
	server, ok := app.grpcSrv.(*bridgegrpc.Server)
	require.True(t, ok)
	assert.Empty(t, server.Addr(), "NewApplication must not start the gRPC listener")
	assert.Empty(t, app.registeredUseCases, "NewApplication must not run EEBUS setup")

	require.NoError(t, app.bridgeSvc.Setup())
	t.Cleanup(app.bridgeSvc.Shutdown)
	require.NoError(t, app.prepareForStart())
	assert.ElementsMatch(t, []string{
		"LPC", "Monitoring", "DHWMonitoring", "MRT", "MOT", "DHWTemperature",
		"MDSF", "DHWSystemFunctionConfiguration", "RoomHeatingTemperature", "MRHSF", "RoomHeatingSystemFunctionConfiguration", "OHPCF",
		"MGCP", "VAPD", "VABD",
	}, app.registeredUseCases)
	providerModule := app.modules[len(app.modules)-1]
	require.Equal(t, "Providers", providerModule.name)
	require.NoError(t, providerModule.start())
	require.NoError(t, providerModule.stop())
}

func TestProviderSampleStateConversion(t *testing.T) {
	tests := []struct {
		in   usecases.ProviderSnapshotState
		want pb.ProviderSampleState
	}{
		{usecases.ProviderSnapshotEmpty, pb.ProviderSampleState_PROVIDER_SAMPLE_STATE_EMPTY},
		{usecases.ProviderSnapshotCurrent, pb.ProviderSampleState_PROVIDER_SAMPLE_STATE_CURRENT},
		{usecases.ProviderSnapshotExpired, pb.ProviderSampleState_PROVIDER_SAMPLE_STATE_EXPIRED},
		{usecases.ProviderSnapshotClosed, pb.ProviderSampleState_PROVIDER_SAMPLE_STATE_CLOSED},
		{usecases.ProviderSnapshotState(99), pb.ProviderSampleState_PROVIDER_SAMPLE_STATE_UNSPECIFIED},
	}
	for _, test := range tests {
		if got := providerSampleState(test.in); got != test.want {
			t.Errorf("providerSampleState(%d) = %s, want %s", test.in, got, test.want)
		}
	}
	if got := (applicationProviderDiagnostics{}).ProviderDiagnostics(time.Now()); len(got) != 0 {
		t.Fatalf("empty provider diagnostics = %+v", got)
	}
}

func TestApplicationStartConfigurationAndStateGuards(t *testing.T) {
	ctx := context.Background()
	if err := (&Application{}).Start(ctx); err == nil || err.Error() != "EEBUS bridge service is not configured" {
		t.Fatalf("missing bridge error = %v", err)
	}
	if err := (&Application{bridgeSvc: &fakeBridgeLifecycle{}}).Start(ctx); err == nil || err.Error() != "gRPC server is not configured" {
		t.Fatalf("missing gRPC error = %v", err)
	}
	stopped := &Application{bridgeSvc: &fakeBridgeLifecycle{}, grpcSrv: &fakeGRPCLifecycle{}, stopped: true}
	if err := stopped.Start(ctx); err == nil || err.Error() != "application is stopped" {
		t.Fatalf("stopped application error = %v", err)
	}
	alreadyStarted := &Application{
		bridgeSvc: &fakeBridgeLifecycle{}, grpcSrv: &fakeGRPCLifecycle{}, lifecycle: newLifecycleTransaction(),
	}
	if err := alreadyStarted.Start(ctx); err == nil || err.Error() != "application is already started" {
		t.Fatalf("already-started application error = %v", err)
	}
}

func TestApplicationErrorWrappersAndLogging(t *testing.T) {
	want := errors.New("runtime failure")
	controlled := &controlledShutdownError{err: want}
	if controlled.Error() != want.Error() || !errors.Is(controlled, want) {
		t.Fatalf("controlled error = %v", controlled)
	}
	signalErr := &signalShutdown{signal: syscall.SIGTERM}
	if signalErr.Error() != "received signal terminated" {
		t.Fatalf("signal error = %q", signalErr.Error())
	}
	logRunError(controlled)
	logRunError(want)
}

func TestNotifySignalContextStopsAndCapturesSignal(t *testing.T) {
	t.Run("explicit stop", func(t *testing.T) {
		ctx, stop := notifySignalContext(context.Background(), syscall.SIGUSR1)
		stop()
		stop()
		if !errors.Is(context.Cause(ctx), context.Canceled) {
			t.Fatalf("context cause = %v", context.Cause(ctx))
		}
	})

	t.Run("signal", func(t *testing.T) {
		ctx, stop := notifySignalContext(context.Background(), syscall.SIGUSR1)
		defer stop()
		require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGUSR1))
		select {
		case <-ctx.Done():
			var signalCause *signalShutdown
			if !errors.As(context.Cause(ctx), &signalCause) || signalCause.signal != syscall.SIGUSR1 {
				t.Fatalf("context cause = %v", context.Cause(ctx))
			}
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for signal context")
		}
	})
}

type fakeHeartbeatLifecycle struct {
	starts   atomic.Int32
	stops    atomic.Int32
	recorder *shutdownRecorder
	startErr error
}

func (f *fakeHeartbeatLifecycle) StartHeartbeat(string) error {
	f.starts.Add(1)
	return f.startErr
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

func (f *fakeMonitoringRegistry) DeviceHealth(ski string) (eebus.DeviceHealthSnapshot, bool) {
	ski = eebus.NormalizeSKI(ski)
	for _, device := range f.ListDeviceHealth() {
		if device.SKI == ski {
			return device, true
		}
	}
	return eebus.DeviceHealthSnapshot{}, false
}

func (f *fakeMonitoringRegistry) ListDeviceHealth() []eebus.DeviceHealthSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := make(map[string]struct{})
	result := make([]eebus.DeviceHealthSnapshot, 0, len(f.stale)+len(f.clearCalls))
	for _, values := range [][]string{f.stale, f.clearCalls} {
		for _, ski := range values {
			ski = eebus.NormalizeSKI(ski)
			if _, ok := seen[ski]; ok {
				continue
			}
			seen[ski] = struct{}{}
			result = append(result, eebus.DeviceHealthSnapshot{
				SKI: ski, Connected: true, MonitoringSuccessOnConnect: f.hasSuccess,
				LastMonitoringSuccess: f.successAt,
			})
		}
	}
	return result
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
	app := &Application{
		cfg:       &config.Config{},
		bridgeSvc: bridge,
		grpcSrv:   grpcServer,
		registry:  registry,
		modules: []applicationModule{{
			name:  "heartbeat",
			start: func() error { return heartbeat.StartHeartbeat("") },
			stop:  heartbeat.StopHeartbeat,
		}},
		monitoringWatchdogInterval: time.Millisecond,
		backgroundFailures:         make(chan backgroundFailure, 1),
	}
	app.recoverySupervisor = eebus.NewRecoverySupervisor(registry, bridge, eebus.RecoveryConfig{
		StaleThreshold: monitoringStaleThreshold,
		GracePeriod:    monitoringGracePeriod,
		BaseBackoff:    monitoringGracePeriod,
		MaxBackoff:     monitoringGracePeriod,
		MaxAttempts:    monitoringRecoveryMaxAttempts,
	})
	return app
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
	assert.Equal(t, int32(0), grpcServer.stops.Load())
	assert.Equal(t, int32(0), heartbeat.stops.Load())
}

func TestLPCHeartbeatStartFailureIsNonFatal(t *testing.T) {
	heartbeat := &fakeHeartbeatLifecycle{startErr: errors.New("heartbeat unavailable")}
	require.NoError(t, startLPCHeartbeat(heartbeat))
	assert.Equal(t, int32(1), heartbeat.starts.Load())
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

func TestApplicationHeartbeatStartFailurePreventsServing(t *testing.T) {
	heartbeatErr := errors.New("heartbeat unavailable")
	bridge := &fakeBridgeLifecycle{}
	grpcServer := &fakeGRPCLifecycle{}
	heartbeat := &fakeHeartbeatLifecycle{startErr: heartbeatErr}
	app := newTestApplication(bridge, grpcServer, heartbeat, nil)

	err := app.Start(context.Background())

	require.ErrorIs(t, err, heartbeatErr)
	assert.Contains(t, err.Error(), "starting heartbeat module")
	assert.Equal(t, int32(1), heartbeat.starts.Load())
	assert.Equal(t, int32(0), heartbeat.stops.Load())
	assert.Equal(t, int32(1), bridge.stops.Load())
	assert.Equal(t, int32(0), grpcServer.starts.Load())
	assert.NotContains(t, grpcServer.healthValues(), true)
}

func TestApplicationStopDuringCompositionCannotRestartEEBUS(t *testing.T) {
	setupStarted := make(chan struct{})
	setupRelease := make(chan struct{})
	bridge := &fakeBridgeLifecycle{setupStarted: setupStarted, setupRelease: setupRelease}
	grpcServer := &fakeGRPCLifecycle{}
	heartbeat := &fakeHeartbeatLifecycle{}
	app := newTestApplication(bridge, grpcServer, heartbeat, nil)
	result := make(chan error, 1)
	go func() { result <- app.Start(context.Background()) }()

	select {
	case <-setupStarted:
	case <-time.After(time.Second):
		t.Fatal("application did not enter EEBUS setup")
	}
	stopDone := make(chan struct{})
	go func() {
		app.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
		t.Fatal("Stop returned while startup still owned the composition transaction")
	case <-time.After(20 * time.Millisecond):
	}
	close(setupRelease)

	select {
	case err := <-result:
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("Start did not return after concurrent Stop")
	}
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("concurrent Stop did not finish")
	}
	assert.Equal(t, int32(0), bridge.starts.Load())
	assert.Equal(t, int32(1), bridge.stops.Load())
	assert.Equal(t, int32(0), heartbeat.starts.Load())
	assert.Equal(t, int32(0), grpcServer.starts.Load())
	assert.NotContains(t, grpcServer.healthValues(), true)
}

func TestApplicationStartRollsBackOnlySuccessfulStagesInReverseOrder(t *testing.T) {
	tests := []struct {
		name         string
		setupErr     error
		bridgeErr    error
		module1Err   error
		module2Err   error
		grpcErr      error
		wantRollback []string
	}{
		{name: "setup", setupErr: errors.New("setup"), wantRollback: nil},
		{name: "bridge", bridgeErr: errors.New("bridge"), wantRollback: []string{"bridge"}},
		{name: "first module", module1Err: errors.New("module"), wantRollback: []string{"bridge"}},
		{name: "second module", module2Err: errors.New("module"), wantRollback: []string{"module-one", "bridge"}},
		{name: "grpc", grpcErr: errors.New("grpc"), wantRollback: []string{"grpc", "module-two", "module-one", "bridge"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &shutdownRecorder{}
			bridge := &fakeBridgeLifecycle{
				setupErr: test.setupErr, startErr: test.bridgeErr, recorder: recorder,
			}
			grpcServer := &fakeGRPCLifecycle{startErr: test.grpcErr, recorder: recorder}
			app := newTestApplication(bridge, grpcServer, &fakeHeartbeatLifecycle{}, nil)
			app.modules = []applicationModule{
				{
					name:  "module-one",
					start: func() error { return test.module1Err },
					stop:  func() error { recorder.add("module-one"); return nil },
				},
				{
					name:  "module-two",
					start: func() error { return test.module2Err },
					stop:  func() error { recorder.add("module-two"); return nil },
				},
			}

			err := app.Start(context.Background())
			require.Error(t, err)
			assert.Equal(t, test.wantRollback, recorder.values())
		})
	}
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
	assert.Equal(t, []bool{false, false}, grpcServer.deviceHealthValues())
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
	recovery := app.recoverySupervisor.Snapshot("AA11", now.Add(monitoringGracePeriod+time.Second))
	assert.Equal(t, eebus.RecoveryStateHealthy, recovery.State)
	assert.Equal(t, []string{"AA11"}, registry.clearValues())
	assert.Equal(t, []string{"AA11"}, bridge.unregisterValues())
	assert.Equal(t, []string{"AA11"}, bridge.registerValues())
	assert.Equal(t, []bool{false, true}, grpcServer.deviceHealthValues())
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
	recovery := app.recoverySupervisor.Snapshot("AA11", now.Add(monitoringGracePeriod+time.Second))
	assert.Equal(t, eebus.RecoveryStateGracePeriod, recovery.State)
	assert.Equal(t, 2, recovery.Attempts)
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
	assert.Equal(t, []bool{false, false, false, false, false}, grpcServer.deviceHealthValues())
}

func TestMonitoringWatchdogHealthTracksTrustedDisconnectedDevice(t *testing.T) {
	bridge := &fakeBridgeLifecycle{}
	grpcServer := &fakeGRPCLifecycle{}
	registry := eebus.NewDeviceRegistry()
	registry.MarkTrusted("AA11")
	app := newTestApplication(bridge, grpcServer, &fakeHeartbeatLifecycle{}, registry)

	require.False(t, app.handleMonitoringWatchdogTick(time.Unix(100, 0)))
	assert.Equal(t, []bool{false}, grpcServer.deviceHealthValues())
}

func TestApplicationStartSignalTriggersShutdown(t *testing.T) {
	bridge := &fakeBridgeLifecycle{started: make(chan struct{})}
	grpcServer := &fakeGRPCLifecycle{release: make(chan struct{}), serving: make(chan struct{})}
	heartbeat := &fakeHeartbeatLifecycle{}
	app := newTestApplication(bridge, grpcServer, heartbeat, nil)
	ctx, cancel := context.WithCancelCause(context.Background())
	result := make(chan error, 1)
	go func() { result <- app.Start(ctx) }()

	select {
	case <-grpcServer.serving:
	case <-time.After(time.Second):
		t.Fatal("application did not become ready")
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
	assert.Equal(t, []bool{false, true, false}, grpcServer.healthValues())
}

func TestApplicationStopIsIdempotentAndOrdered(t *testing.T) {
	recorder := &shutdownRecorder{}
	bridge := &fakeBridgeLifecycle{recorder: recorder}
	grpcServer := &fakeGRPCLifecycle{recorder: recorder, release: make(chan struct{})}
	heartbeat := &fakeHeartbeatLifecycle{recorder: recorder}
	app := newTestApplication(bridge, grpcServer, heartbeat, nil)
	tx := newLifecycleTransaction()
	require.NoError(t, tx.add("bridge", func() error { bridge.Shutdown(); return nil }))
	require.NoError(t, tx.add("heartbeat", heartbeat.StopHeartbeat))
	require.NoError(t, tx.add("grpc", func() error { grpcServer.Stop(); return nil }))
	app.lifecycle = tx

	require.NotPanics(t, func() {
		var wait sync.WaitGroup
		for range 16 {
			wait.Add(1)
			go func() {
				defer wait.Done()
				app.Stop()
			}()
		}
		wait.Wait()
		app.Stop()
	})

	assert.Equal(t, []string{"grpc", "heartbeat", "bridge"}, recorder.values())
	assert.Equal(t, int32(1), bridge.stops.Load())
	assert.Equal(t, int32(1), grpcServer.stops.Load())
	assert.Equal(t, int32(1), heartbeat.stops.Load())
}
