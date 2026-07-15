package grpc_test

import (
	"context"
	"testing"
	"time"

	shipapi "github.com/enbility/ship-go/api"
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

func TestListDevicesResponsesAreSortedByNormalizedSKI(t *testing.T) {
	bus := eebus.NewEventBus()
	callbacks := eebus.NewCallbacks(bus, false)
	callbacks.VisibleRemoteMdnsServicesUpdated(nil, []shipapi.RemoteMdnsService{
		{Ski: "cc:03"},
		{Ski: "AA-01"},
		{Ski: " bb 02 "},
	})
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice("cc:03", eebus.DeviceInfo{})
	registry.AddDevice("AA-01", eebus.DeviceInfo{})
	registry.AddDevice(" bb 02 ", eebus.DeviceInfo{})
	svc := bridgegrpc.NewDeviceService(callbacks, bus, "local", registry)

	discovered, err := svc.ListDiscoveredDevices(context.Background(), &pb.Empty{})
	if err != nil {
		t.Fatalf("ListDiscoveredDevices: %v", err)
	}
	for index, want := range []string{"AA-01", " bb 02 ", "cc:03"} {
		if discovered.Devices[index].Ski != want {
			t.Errorf("discovered[%d].ski = %q, want %q", index, discovered.Devices[index].Ski, want)
		}
	}

	paired, err := svc.ListPairedDevices(context.Background(), &pb.Empty{})
	if err != nil {
		t.Fatalf("ListPairedDevices: %v", err)
	}
	for index, want := range []string{"AA01", "BB02", "CC03"} {
		if paired.Devices[index].Ski != want {
			t.Errorf("paired[%d].ski = %q, want %q", index, paired.Devices[index].Ski, want)
		}
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

func TestRegisterRemoteSKINormalizesColonSeparatedSKI(t *testing.T) {
	bus := eebus.NewEventBus()
	callbacks := eebus.NewCallbacks(bus, false)
	svc := bridgegrpc.NewDeviceService(callbacks, bus, "test-local-ski", eebus.NewDeviceRegistry())

	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	colonSKI := "68:2f:70:8c:eb:a5:df:9a:dc:b9:e6:78:7e:a9:11:d9:fc:3a:c4:90"
	if _, err := svc.RegisterRemoteSKI(context.Background(), &pb.RegisterSKIRequest{Ski: colonSKI}); err != nil {
		t.Fatalf("RegisterRemoteSKI(colon-separated): %v", err)
	}

	want := eebus.NormalizeSKI(testValidSKI)
	select {
	case evt := <-ch:
		if evt.SKI != want {
			t.Errorf("published SKI = %q, want normalized %q", evt.SKI, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for register event")
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

	want := eebus.NormalizeSKI(testValidSKI)
	select {
	case evt := <-ch:
		if evt.Type != eebus.EventTypeDeviceUnregisterSKI || evt.SKI != want {
			t.Errorf("event = %+v, want type=device.unregister_ski ski=%s", evt, want)
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

	bus.Publish(eebus.Event{SKI: testValidSKI, Type: eebus.EventTypeDeviceTrustRemoved})

	evt, err := stream.Recv()
	if err != nil {
		t.Fatalf("stream.Recv: %v", err)
	}
	if evt.EventType != pb.DeviceEventType_DEVICE_EVENT_TRUST_REMOVED {
		t.Errorf("EventType = %v, want DEVICE_EVENT_TRUST_REMOVED", evt.EventType)
	}
	want := eebus.NormalizeSKI(testValidSKI)
	if evt.Ski != want {
		t.Errorf("Ski = %q, want %q", evt.Ski, want)
	}
}
