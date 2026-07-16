package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/volschin/eebus-bridge/internal/config"
)

type fakeBridgeLifecycle struct {
	startErr  error
	started   chan struct{}
	startOnce sync.Once
	starts    atomic.Int32
	stops     atomic.Int32
	recorder  *shutdownRecorder
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

func (f *fakeBridgeLifecycle) RegisterRemoteSKI(string) {}

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
	stale        []string
	age          time.Duration
	hasAge       bool
	threshold    time.Duration
	gracePeriod  time.Duration
	staleInvoked chan struct{}
	invokeOnce   sync.Once
}

func (f *fakeMonitoringRegistry) StaleDevices(threshold, gracePeriod time.Duration) []string {
	f.threshold = threshold
	f.gracePeriod = gracePeriod
	if f.staleInvoked != nil {
		f.invokeOnce.Do(func() { close(f.staleInvoked) })
	}
	return append([]string(nil), f.stale...)
}

func (f *fakeMonitoringRegistry) MonitoringLastSuccessAge(string) (time.Duration, bool) {
	return f.age, f.hasAge
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
		cfg:                        &config.Config{},
		bridgeSvc:                  bridge,
		grpcSrv:                    grpcServer,
		lpcWrapper:                 heartbeat,
		registry:                   registry,
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
	var controlled *controlledShutdownError
	assert.ErrorAs(t, err, &controlled)
	assert.Equal(t, int32(1), bridge.stops.Load())
	assert.Equal(t, int32(1), grpcServer.stops.Load())
	assert.Equal(t, int32(1), heartbeat.stops.Load())
}

func TestApplicationStartWatchdogTriggersControlledShutdown(t *testing.T) {
	bridge := &fakeBridgeLifecycle{}
	grpcServer := &fakeGRPCLifecycle{release: make(chan struct{})}
	heartbeat := &fakeHeartbeatLifecycle{}
	registry := &fakeMonitoringRegistry{
		stale:  []string{"AA:BB:CC:DD:EE:FF"},
		age:    11 * time.Minute,
		hasAge: true,
	}
	app := newTestApplication(bridge, grpcServer, heartbeat, registry)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := app.Start(ctx)

	require.EqualError(t, err, "monitoring watchdog detected stale connected devices")
	assert.Equal(t, monitoringStaleThreshold, registry.threshold)
	assert.Equal(t, monitoringGracePeriod, registry.gracePeriod)
	assert.Equal(t, []bool{false}, grpcServer.healthValues())
	assert.Equal(t, int32(1), bridge.stops.Load())
	assert.Equal(t, int32(1), grpcServer.stops.Load())
	assert.Equal(t, int32(1), heartbeat.stops.Load())
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
