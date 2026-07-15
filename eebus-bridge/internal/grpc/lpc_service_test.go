package grpc_test

import (
	"context"
	"testing"
	"time"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

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

	stream, err := client.SubscribeLPCEvents(ctx, &pb.DeviceRequest{Ski: "test-ski"})
	if err != nil {
		t.Fatal(err)
	}

	// Give the server-side handler goroutine time to subscribe before publishing.
	time.Sleep(50 * time.Millisecond)
	bus.Publish(eebus.Event{SKI: "test-ski", Type: eebus.EventTypeLPCLimitUpdated})

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

	stream, err := client.SubscribeLPCEvents(ctx, &pb.DeviceRequest{Ski: "test-ski"})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	bus.Publish(eebus.Event{SKI: "test-ski", Type: eebus.EventTypeLPCHeartbeatUpdated})

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
	if _, err := svc.GetHeartbeatStatus(ctx, nil); err == nil {
		t.Error("GetHeartbeatStatus(nil request) should error")
	}
	if _, err := svc.GetHeartbeatStatus(ctx, &pb.DeviceRequest{Ski: "x"}); err == nil {
		t.Error("GetHeartbeatStatus with nil lpc should error (Unavailable)")
	}
}
