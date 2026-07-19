package grpc_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	ucapi "github.com/enbility/eebus-go/usecases/api"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type fakeLPCController struct {
	entity spineapi.EntityRemoteInterface
}

func (f fakeLPCController) CompatibleEntityForScenario(string, uint) eebus.EntityResolution {
	return eebus.EntityResolution{Entity: f.entity, DeviceCount: 1}
}
func (fakeLPCController) ConsumptionLimit(spineapi.EntityRemoteInterface) (ucapi.LoadLimit, error) {
	return ucapi.LoadLimit{Value: 1200}, nil
}
func (fakeLPCController) WriteConsumptionLimit(spineapi.EntityRemoteInterface, ucapi.LoadLimit) error {
	return nil
}
func (fakeLPCController) FailsafeConsumptionActivePowerLimit(spineapi.EntityRemoteInterface) (float64, error) {
	return 2400, nil
}
func (fakeLPCController) WriteFailsafeConsumptionActivePowerLimit(spineapi.EntityRemoteInterface, float64) error {
	return nil
}
func (fakeLPCController) FailsafeDurationMinimum(spineapi.EntityRemoteInterface) (time.Duration, error) {
	return time.Minute, nil
}
func (fakeLPCController) WriteFailsafeDurationMinimum(spineapi.EntityRemoteInterface, time.Duration) error {
	return nil
}
func (fakeLPCController) StartHeartbeat(string) error { return nil }
func (fakeLPCController) StopHeartbeat() error        { return nil }
func (fakeLPCController) IsHeartbeatRunning() bool    { return true }
func (fakeLPCController) IsHeartbeatWithinDuration(spineapi.EntityRemoteInterface) bool {
	return true
}

type recordingLPCController struct {
	fakeLPCController
	consumptionErr        error
	failsafePowerErr      error
	failsafeDurationErr   error
	writeConsumptionErr   error
	writeFailsafePowerErr error
	writeFailsafeTimeErr  error
	startErr              error
	stopErr               error
	writtenLimit          ucapi.LoadLimit
	writtenFailsafePower  float64
	writtenDuration       time.Duration
	startedSKI            string
	stopCalls             int
}

func (f *recordingLPCController) ConsumptionLimit(spineapi.EntityRemoteInterface) (ucapi.LoadLimit, error) {
	return ucapi.LoadLimit{Value: 1200, Duration: 2 * time.Minute, IsActive: true, IsChangeable: true}, f.consumptionErr
}
func (f *recordingLPCController) WriteConsumptionLimit(_ spineapi.EntityRemoteInterface, limit ucapi.LoadLimit) error {
	f.writtenLimit = limit
	return f.writeConsumptionErr
}
func (f *recordingLPCController) FailsafeConsumptionActivePowerLimit(spineapi.EntityRemoteInterface) (float64, error) {
	return 2400, f.failsafePowerErr
}
func (f *recordingLPCController) WriteFailsafeConsumptionActivePowerLimit(_ spineapi.EntityRemoteInterface, value float64) error {
	f.writtenFailsafePower = value
	return f.writeFailsafePowerErr
}
func (f *recordingLPCController) FailsafeDurationMinimum(spineapi.EntityRemoteInterface) (time.Duration, error) {
	return 90 * time.Second, f.failsafeDurationErr
}
func (f *recordingLPCController) WriteFailsafeDurationMinimum(_ spineapi.EntityRemoteInterface, duration time.Duration) error {
	f.writtenDuration = duration
	return f.writeFailsafeTimeErr
}
func (f *recordingLPCController) StartHeartbeat(ski string) error {
	f.startedSKI = ski
	return f.startErr
}
func (f *recordingLPCController) StopHeartbeat() error {
	f.stopCalls++
	return f.stopErr
}

func TestLPCRPCsReadWriteAndHeartbeat(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice(testValidSKI, eebus.DeviceInfo{})
	controller := &recordingLPCController{fakeLPCController: fakeLPCController{entity: mocks.NewEntityRemoteInterface(t)}}
	svc := bridgegrpc.NewLPCService(nil, eebus.NewEventBus(), registry, bridgegrpc.WithLPCController(controller))
	ctx := context.Background()

	limit, err := svc.GetConsumptionLimit(ctx, &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil || limit.GetValueWatts() != 1200 || limit.GetDurationSeconds() != 120 || !limit.GetIsActive() || !limit.GetIsChangeable() {
		t.Fatalf("GetConsumptionLimit() = (%+v, %v)", limit, err)
	}
	if _, err := svc.WriteConsumptionLimit(ctx, &pb.WriteLoadLimitRequest{
		Ski: testValidSKI, ValueWatts: 750, DurationSeconds: 30, IsActive: true,
	}); err != nil {
		t.Fatalf("WriteConsumptionLimit() error = %v", err)
	}
	if controller.writtenLimit.Value != 750 || controller.writtenLimit.Duration != 30*time.Second || !controller.writtenLimit.IsActive {
		t.Fatalf("written limit = %+v", controller.writtenLimit)
	}

	failsafe, err := svc.GetFailsafeLimit(ctx, &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil || failsafe.GetValueWatts() != 2400 || failsafe.GetDurationMinimumSeconds() != 90 {
		t.Fatalf("GetFailsafeLimit() = (%+v, %v)", failsafe, err)
	}
	if _, err := svc.WriteFailsafeLimit(ctx, &pb.WriteFailsafeLimitRequest{
		Ski: testValidSKI, ValueWatts: 1800, DurationMinimumSeconds: 45,
	}); err != nil {
		t.Fatalf("WriteFailsafeLimit() error = %v", err)
	}
	if controller.writtenFailsafePower != 1800 || controller.writtenDuration != 45*time.Second {
		t.Fatalf("written failsafe = (%g, %s)", controller.writtenFailsafePower, controller.writtenDuration)
	}

	if _, err := svc.StartHeartbeat(ctx, &pb.DeviceRequest{Ski: testValidSKI}); err != nil {
		t.Fatalf("StartHeartbeat() error = %v", err)
	}
	if _, err := svc.StopHeartbeat(ctx, &pb.DeviceRequest{Ski: testValidSKI}); err != nil {
		t.Fatalf("StopHeartbeat() error = %v", err)
	}
	if controller.startedSKI != testValidSKI || controller.stopCalls != 1 {
		t.Fatalf("heartbeat calls = start %q, stop %d", controller.startedSKI, controller.stopCalls)
	}
	statusResult, err := svc.GetHeartbeatStatus(ctx, &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil || !statusResult.GetRunning() || !statusResult.GetWithinDuration() {
		t.Fatalf("GetHeartbeatStatus() = (%+v, %v)", statusResult, err)
	}
}

func TestLPCRPCReadAndWriteErrors(t *testing.T) {
	want := errors.New("controller failure")
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice(testValidSKI, eebus.DeviceInfo{})
	controller := &recordingLPCController{fakeLPCController: fakeLPCController{entity: mocks.NewEntityRemoteInterface(t)}}
	svc := bridgegrpc.NewLPCService(nil, nil, registry, bridgegrpc.WithLPCController(controller))
	ctx := context.Background()

	checks := []struct {
		name string
		set  func()
		call func() error
	}{
		{"read consumption", func() { controller.consumptionErr = want }, func() error { _, err := svc.GetConsumptionLimit(ctx, &pb.DeviceRequest{Ski: testValidSKI}); return err }},
		{"write consumption", func() { controller.consumptionErr = nil; controller.writeConsumptionErr = want }, func() error {
			_, err := svc.WriteConsumptionLimit(ctx, &pb.WriteLoadLimitRequest{Ski: testValidSKI})
			return err
		}},
		{"read failsafe power", func() { controller.writeConsumptionErr = nil; controller.failsafePowerErr = want }, func() error { _, err := svc.GetFailsafeLimit(ctx, &pb.DeviceRequest{Ski: testValidSKI}); return err }},
		{"read failsafe duration", func() { controller.failsafePowerErr = nil; controller.failsafeDurationErr = want }, func() error { _, err := svc.GetFailsafeLimit(ctx, &pb.DeviceRequest{Ski: testValidSKI}); return err }},
		{"write failsafe power", func() { controller.failsafeDurationErr = nil; controller.writeFailsafePowerErr = want }, func() error {
			_, err := svc.WriteFailsafeLimit(ctx, &pb.WriteFailsafeLimitRequest{Ski: testValidSKI})
			return err
		}},
		{"write failsafe duration", func() { controller.writeFailsafePowerErr = nil; controller.writeFailsafeTimeErr = want }, func() error {
			_, err := svc.WriteFailsafeLimit(ctx, &pb.WriteFailsafeLimitRequest{Ski: testValidSKI, DurationMinimumSeconds: 1})
			return err
		}},
		{"start heartbeat", func() { controller.writeFailsafeTimeErr = nil; controller.startErr = want }, func() error { _, err := svc.StartHeartbeat(ctx, &pb.DeviceRequest{Ski: testValidSKI}); return err }},
		{"stop heartbeat", func() { controller.startErr = nil; controller.stopErr = want }, func() error { _, err := svc.StopHeartbeat(ctx, &pb.DeviceRequest{Ski: testValidSKI}); return err }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			check.set()
			if err := check.call(); err == nil {
				t.Fatal("expected controller error")
			}
		})
	}

	empty := bridgegrpc.NewLPCService(nil, nil, nil)
	if _, err := empty.GetConsumptionLimit(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("nil consumption request code = %v", status.Code(err))
	}
	if _, err := empty.GetFailsafeLimit(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("nil failsafe request code = %v", status.Code(err))
	}
	if _, err := empty.GetConsumptionLimit(ctx, &pb.DeviceRequest{Ski: testValidSKI}); status.Code(err) != codes.Unavailable {
		t.Fatalf("uninitialized consumption code = %v", status.Code(err))
	}
	if _, err := empty.GetFailsafeLimit(ctx, &pb.DeviceRequest{Ski: testValidSKI}); status.Code(err) != codes.Unavailable {
		t.Fatalf("uninitialized failsafe code = %v", status.Code(err))
	}
}

func TestLPCPayloadReadsUpdateCapabilityRegistry(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice(testValidSKI, eebus.DeviceInfo{})
	controller := fakeLPCController{entity: mocks.NewEntityRemoteInterface(t)}
	service := bridgegrpc.NewLPCService(
		nil,
		eebus.NewEventBus(),
		registry,
		bridgegrpc.WithLPCController(controller),
	)
	limit := &pb.LPCEvent{}
	service.AttachLPCPayload(limit, testValidSKI, pb.LPCEventType_LPC_EVENT_LIMIT_UPDATED)
	failsafe := &pb.LPCEvent{}
	service.AttachLPCPayload(failsafe, testValidSKI, pb.LPCEventType_LPC_EVENT_FAILSAFE_UPDATED)
	if limit.GetLimitUpdate() == nil || failsafe.GetFailsafeUpdate() == nil {
		t.Fatalf("payloads = (%v, %v)", limit, failsafe)
	}
	capabilities, _ := registry.DeviceCapabilities(testValidSKI)
	states := make(map[eebus.Capability]eebus.CapabilityState)
	for _, capability := range capabilities {
		states[capability.ID] = capability.State
	}
	if states[eebus.CapabilityLPC] != eebus.CapabilityStateAvailable ||
		states[eebus.CapabilityFailsafe] != eebus.CapabilityStateAvailable {
		t.Fatalf("LPC capability states = %v", states)
	}
}

func TestLPCNumericWriteValidation(t *testing.T) {
	svc := bridgegrpc.NewLPCService(nil, eebus.NewEventBus(), eebus.NewDeviceRegistry())
	ctx := context.Background()

	tests := []struct {
		name     string
		write    func() error
		wantCode codes.Code
	}{
		{
			name: "consumption limit NaN watts",
			write: func() error {
				_, err := svc.WriteConsumptionLimit(ctx, &pb.WriteLoadLimitRequest{Ski: testValidSKI, ValueWatts: math.NaN()})
				return err
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "consumption limit positive infinity watts",
			write: func() error {
				_, err := svc.WriteConsumptionLimit(ctx, &pb.WriteLoadLimitRequest{Ski: testValidSKI, ValueWatts: math.Inf(1)})
				return err
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "consumption limit negative infinity watts",
			write: func() error {
				_, err := svc.WriteConsumptionLimit(ctx, &pb.WriteLoadLimitRequest{Ski: testValidSKI, ValueWatts: math.Inf(-1)})
				return err
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "consumption limit negative watts",
			write: func() error {
				_, err := svc.WriteConsumptionLimit(ctx, &pb.WriteLoadLimitRequest{Ski: testValidSKI, ValueWatts: -1})
				return err
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "consumption limit negative duration",
			write: func() error {
				_, err := svc.WriteConsumptionLimit(ctx, &pb.WriteLoadLimitRequest{Ski: testValidSKI, DurationSeconds: -1})
				return err
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "consumption limit non-negative values",
			write: func() error {
				_, err := svc.WriteConsumptionLimit(ctx, &pb.WriteLoadLimitRequest{Ski: testValidSKI, ValueWatts: 1, DurationSeconds: 1})
				return err
			},
			wantCode: codes.Unavailable,
		},
		{
			name: "failsafe limit NaN watts",
			write: func() error {
				_, err := svc.WriteFailsafeLimit(ctx, &pb.WriteFailsafeLimitRequest{Ski: testValidSKI, ValueWatts: math.NaN()})
				return err
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "failsafe limit positive infinity watts",
			write: func() error {
				_, err := svc.WriteFailsafeLimit(ctx, &pb.WriteFailsafeLimitRequest{Ski: testValidSKI, ValueWatts: math.Inf(1)})
				return err
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "failsafe limit negative infinity watts",
			write: func() error {
				_, err := svc.WriteFailsafeLimit(ctx, &pb.WriteFailsafeLimitRequest{Ski: testValidSKI, ValueWatts: math.Inf(-1)})
				return err
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "failsafe limit negative watts",
			write: func() error {
				_, err := svc.WriteFailsafeLimit(ctx, &pb.WriteFailsafeLimitRequest{Ski: testValidSKI, ValueWatts: -1})
				return err
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "failsafe limit negative duration",
			write: func() error {
				_, err := svc.WriteFailsafeLimit(ctx, &pb.WriteFailsafeLimitRequest{Ski: testValidSKI, DurationMinimumSeconds: -1})
				return err
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "failsafe limit non-negative values",
			write: func() error {
				_, err := svc.WriteFailsafeLimit(ctx, &pb.WriteFailsafeLimitRequest{Ski: testValidSKI, ValueWatts: 1, DurationMinimumSeconds: 1})
				return err
			},
			wantCode: codes.Unavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.write(); status.Code(err) != tt.wantCode {
				t.Fatalf("status code = %v, want %v (error: %v)", status.Code(err), tt.wantCode, err)
			}
		})
	}
}

func TestSubscribeLPCEvents(t *testing.T) {
	bus := eebus.NewEventBus()
	svc := bridgegrpc.NewLPCService(nil, bus, eebus.NewDeviceRegistry())

	srv := bridgegrpc.NewServer("127.0.0.1", 0, false)
	pb.RegisterLPCServiceServer(srv.GRPCServer(), svc)
	srv.SetHealthy(true)
	go srv.Start()
	t.Cleanup(srv.Stop)

	time.Sleep(100 * time.Millisecond)
	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	client := pb.NewLPCServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stream, err := client.SubscribeLPCEvents(ctx, &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil {
		t.Fatal(err)
	}

	// Give the server-side handler goroutine time to subscribe before publishing.
	time.Sleep(50 * time.Millisecond)
	bus.Publish(eebus.Event{SKI: testValidSKI, Type: eebus.EventTypeLPCLimitUpdated})

	evt, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if evt.EventType != pb.LPCEventType_LPC_EVENT_LIMIT_UPDATED {
		t.Errorf("EventType = %v, want LPC_EVENT_LIMIT_UPDATED", evt.EventType)
	}
}

func TestSubscribeLPCEventsHeartbeat(t *testing.T) {
	bus := eebus.NewEventBus()
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice(testValidSKI, eebus.DeviceInfo{})
	svc := bridgegrpc.NewLPCService(
		nil,
		bus,
		registry,
		bridgegrpc.WithLPCController(fakeLPCController{entity: mocks.NewEntityRemoteInterface(t)}),
	)

	srv := bridgegrpc.NewServer("127.0.0.1", 0, false)
	pb.RegisterLPCServiceServer(srv.GRPCServer(), svc)
	srv.SetHealthy(true)
	go srv.Start()
	t.Cleanup(srv.Stop)

	time.Sleep(100 * time.Millisecond)
	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	client := pb.NewLPCServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stream, err := client.SubscribeLPCEvents(ctx, &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	bus.Publish(eebus.Event{SKI: testValidSKI, Type: eebus.EventTypeLPCHeartbeatUpdated})

	evt, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if evt.EventType != pb.LPCEventType_LPC_EVENT_HEARTBEAT_TIMEOUT {
		t.Errorf("EventType = %v, want LPC_EVENT_HEARTBEAT_TIMEOUT", evt.EventType)
	}
	if heartbeat := evt.GetHeartbeatUpdate(); heartbeat == nil || !heartbeat.GetRunning() || !heartbeat.GetWithinDuration() {
		t.Fatalf("heartbeat payload = %v", heartbeat)
	}
}

func TestHeartbeatHandlersValidation(t *testing.T) {
	// nil lpc wrapper: handlers must report Unavailable, never panic.
	svc := bridgegrpc.NewLPCService(nil, eebus.NewEventBus(), eebus.NewDeviceRegistry())
	ctx := context.Background()

	if _, err := svc.StartHeartbeat(ctx, nil); err == nil {
		t.Error("StartHeartbeat(nil request) should error")
	}
	if _, err := svc.StartHeartbeat(ctx, &pb.DeviceRequest{Ski: "x"}); err == nil {
		t.Error("StartHeartbeat with nil lpc should error (Unavailable)")
	}
	if _, err := svc.StopHeartbeat(ctx, &pb.DeviceRequest{}); err == nil {
		t.Error("StopHeartbeat with nil lpc should error (Unavailable)")
	}
	if _, err := svc.StopHeartbeat(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Error("StopHeartbeat(nil request) should return InvalidArgument")
	}
	if _, err := svc.GetHeartbeatStatus(ctx, nil); err == nil {
		t.Error("GetHeartbeatStatus(nil request) should error")
	}
	if _, err := svc.GetHeartbeatStatus(ctx, &pb.DeviceRequest{Ski: "x"}); err == nil {
		t.Error("GetHeartbeatStatus with nil lpc should error (Unavailable)")
	}
}

func TestGetHeartbeatStatusMissingEntityReturnsNotFound(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice(testValidSKI, eebus.DeviceInfo{})
	svc := bridgegrpc.NewLPCService(
		usecases.NewLPCWrapper(eebus.NewEventBus(), registry, false),
		eebus.NewEventBus(),
		registry,
	)

	result, err := svc.GetHeartbeatStatus(context.Background(), &pb.DeviceRequest{Ski: testValidSKI})
	if result != nil || status.Code(err) != codes.NotFound {
		t.Fatalf("GetHeartbeatStatus() = (%+v, %v), want nil/NotFound", result, err)
	}
}
