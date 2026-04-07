//go:build integration

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

func TestIntegrationDeviceServiceRoundTrip(t *testing.T) {
	bus := eebus.NewEventBus()
	callbacks := eebus.NewCallbacks(bus)

	deviceSvc := bridgegrpc.NewDeviceService(callbacks, bus, "integration-test-ski")
	lpcSvc := bridgegrpc.NewLPCService(nil, bus)
	monSvc := bridgegrpc.NewMonitoringService(nil, bus)

	srv := bridgegrpc.NewServer(0)
	pb.RegisterDeviceServiceServer(srv.GRPCServer(), deviceSvc)
	pb.RegisterLPCServiceServer(srv.GRPCServer(), lpcSvc)
	pb.RegisterMonitoringServiceServer(srv.GRPCServer(), monSvc)

	go srv.Start()
	defer srv.Stop()

	time.Sleep(100 * time.Millisecond)

	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	ctx := context.Background()

	// Test DeviceService.GetStatus
	dc := pb.NewDeviceServiceClient(conn)
	status, err := dc.GetStatus(ctx, &pb.Empty{})
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if !status.Running {
		t.Error("status.Running = false")
	}
	if status.LocalSki != "integration-test-ski" {
		t.Errorf("status.LocalSki = %q", status.LocalSki)
	}

	// Test DeviceService.ListDiscoveredDevices
	devices, err := dc.ListDiscoveredDevices(ctx, &pb.Empty{})
	if err != nil {
		t.Fatalf("ListDiscoveredDevices: %v", err)
	}
	if devices == nil {
		t.Error("ListDiscoveredDevices returned nil")
	}

	// Test DeviceService event streaming
	streamCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	stream, err := dc.SubscribeDeviceEvents(streamCtx, &pb.Empty{})
	if err != nil {
		t.Fatalf("SubscribeDeviceEvents: %v", err)
	}

	// Allow server-side goroutine to register subscription before firing event.
	time.Sleep(50 * time.Millisecond)

	callbacks.RemoteSKIConnected(nil, "remote-ski-test")

	evt, err := stream.Recv()
	if err != nil {
		t.Fatalf("stream.Recv: %v", err)
	}
	if evt.Ski != "remote-ski-test" {
		t.Errorf("event SKI = %q", evt.Ski)
	}
	if evt.EventType != pb.DeviceEventType_DEVICE_EVENT_CONNECTED {
		t.Errorf("event type = %v", evt.EventType)
	}
}
