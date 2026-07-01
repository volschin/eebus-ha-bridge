package grpc_test

import (
	"context"
	"testing"
	"time"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func setupDeviceTest(t *testing.T) pb.DeviceServiceClient {
	t.Helper()

	bus := eebus.NewEventBus()
	callbacks := eebus.NewCallbacks(bus, false)
	svc := bridgegrpc.NewDeviceService(callbacks, bus, "test-local-ski", eebus.NewDeviceRegistry())

	srv := bridgegrpc.NewServer("127.0.0.1", 0, false)
	pb.RegisterDeviceServiceServer(srv.GRPCServer(), svc)

	go srv.Start()
	t.Cleanup(srv.Stop)

	time.Sleep(100 * time.Millisecond)
	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	return pb.NewDeviceServiceClient(conn)
}

func TestGetStatus(t *testing.T) {
	client := setupDeviceTest(t)

	resp, err := client.GetStatus(context.Background(), &pb.Empty{})
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}

	if resp.LocalSki != "test-local-ski" {
		t.Errorf("LocalSki = %q, want test-local-ski", resp.LocalSki)
	}
	if !resp.Running {
		t.Error("Running = false, want true")
	}
}

const testValidSKI = "682f708ceba5df9adcb9e6787ea911d9fc3ac490"

func TestRegisterRemoteSKIRejectsMalformedSKI(t *testing.T) {
	client := setupDeviceTest(t)

	_, err := client.RegisterRemoteSKI(context.Background(), &pb.RegisterSKIRequest{Ski: "not-a-ski"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("RegisterRemoteSKI(malformed) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestRegisterRemoteSKIAcceptsWellFormedSKI(t *testing.T) {
	client := setupDeviceTest(t)

	if _, err := client.RegisterRemoteSKI(context.Background(), &pb.RegisterSKIRequest{Ski: testValidSKI}); err != nil {
		t.Fatalf("RegisterRemoteSKI(valid): %v", err)
	}
}

func TestUnregisterRemoteSKIRejectsMalformedSKI(t *testing.T) {
	client := setupDeviceTest(t)

	_, err := client.UnregisterRemoteSKI(context.Background(), &pb.RegisterSKIRequest{Ski: "not-a-ski"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UnregisterRemoteSKI(malformed) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestUnregisterRemoteSKIPublishesEvent(t *testing.T) {
	bus := eebus.NewEventBus()
	callbacks := eebus.NewCallbacks(bus, false)
	svc := bridgegrpc.NewDeviceService(callbacks, bus, "test-local-ski", eebus.NewDeviceRegistry())

	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	if _, err := svc.UnregisterRemoteSKI(context.Background(), &pb.RegisterSKIRequest{Ski: testValidSKI}); err != nil {
		t.Fatalf("UnregisterRemoteSKI: %v", err)
	}

	select {
	case evt := <-ch:
		if evt.Type != "device.unregister_ski" || evt.SKI != testValidSKI {
			t.Errorf("event = %+v, want type=device.unregister_ski ski=%s", evt, testValidSKI)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for unregister event")
	}
}

func TestSubscribeDeviceEventsTrustRemoved(t *testing.T) {
	bus := eebus.NewEventBus()
	callbacks := eebus.NewCallbacks(bus, false)
	svc := bridgegrpc.NewDeviceService(callbacks, bus, "test-local-ski", eebus.NewDeviceRegistry())

	srv := bridgegrpc.NewServer("127.0.0.1", 0, false)
	pb.RegisterDeviceServiceServer(srv.GRPCServer(), svc)
	go srv.Start()
	t.Cleanup(srv.Stop)
	time.Sleep(100 * time.Millisecond)

	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	client := pb.NewDeviceServiceClient(conn)

	stream, err := client.SubscribeDeviceEvents(context.Background(), &pb.Empty{})
	if err != nil {
		t.Fatalf("SubscribeDeviceEvents: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	bus.Publish(eebus.Event{SKI: testValidSKI, Type: "device.trust_removed"})

	evt, err := stream.Recv()
	if err != nil {
		t.Fatalf("stream.Recv: %v", err)
	}
	if evt.EventType != pb.DeviceEventType_DEVICE_EVENT_TRUST_REMOVED {
		t.Errorf("EventType = %v, want DEVICE_EVENT_TRUST_REMOVED", evt.EventType)
	}
	if evt.Ski != testValidSKI {
		t.Errorf("Ski = %q, want %q", evt.Ski, testValidSKI)
	}
}
