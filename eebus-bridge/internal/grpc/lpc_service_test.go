package grpc_test

import (
	"context"
	"math"
	"testing"
	"time"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

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
	svc := bridgegrpc.NewLPCService(nil, bus, eebus.NewDeviceRegistry())

	srv := bridgegrpc.NewServer("127.0.0.1", 0, false)
	pb.RegisterLPCServiceServer(srv.GRPCServer(), svc)
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
