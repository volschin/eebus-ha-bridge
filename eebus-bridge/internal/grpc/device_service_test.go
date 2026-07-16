package grpc_test

import (
	"context"
	"errors"
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

type recordingTrustController struct {
	registerCalls   []string
	unregisterCalls []string
	registerErr     error
	unregisterErr   error
}

func (c *recordingTrustController) RegisterSKI(ski string) error {
	c.registerCalls = append(c.registerCalls, ski)
	return c.registerErr
}

func (c *recordingTrustController) UnregisterSKI(ski string) error {
	c.unregisterCalls = append(c.unregisterCalls, ski)
	return c.unregisterErr
}

func setupDeviceTest(t *testing.T) pb.DeviceServiceClient {
	t.Helper()

	bus := eebus.NewEventBus()
	callbacks := eebus.NewCallbacks(bus, false)
	svc := bridgegrpc.NewDeviceService(callbacks, bus, "test-local-ski", eebus.NewDeviceRegistry(), &recordingTrustController{})

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

func TestGetDeviceStatus(t *testing.T) {
	bus := eebus.NewEventBus()
	registry := eebus.NewDeviceRegistry()
	svc := bridgegrpc.NewDeviceService(
		eebus.NewCallbacks(bus, false),
		bus,
		"test-local-ski",
		registry,
		&recordingTrustController{},
	)

	registry.MarkConnected(testValidSKI)
	connected, err := svc.GetDeviceStatus(context.Background(), &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil {
		t.Fatalf("GetDeviceStatus(connected): %v", err)
	}
	if !connected.Connected || connected.LastTransition == nil {
		t.Errorf("GetDeviceStatus(connected) = %+v, want connected with transition timestamp", connected)
	}

	registry.MarkDisconnected(testValidSKI)
	disconnected, err := svc.GetDeviceStatus(context.Background(), &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil {
		t.Fatalf("GetDeviceStatus(disconnected): %v", err)
	}
	if disconnected.Connected || disconnected.LastTransition == nil {
		t.Errorf("GetDeviceStatus(disconnected) = %+v, want disconnected with transition timestamp", disconnected)
	}

	unknownSKI := "782f708ceba5df9adcb9e6787ea911d9fc3ac490"
	unknown, err := svc.GetDeviceStatus(context.Background(), &pb.DeviceRequest{Ski: unknownSKI})
	if err != nil {
		t.Fatalf("GetDeviceStatus(unknown): %v", err)
	}
	if unknown.Connected || unknown.LastTransition != nil {
		t.Errorf("GetDeviceStatus(unknown) = %+v, want disconnected without transition timestamp", unknown)
	}
}

func TestGetDeviceStatusRejectsMalformedSKI(t *testing.T) {
	bus := eebus.NewEventBus()
	svc := bridgegrpc.NewDeviceService(
		eebus.NewCallbacks(bus, false),
		bus,
		"test-local-ski",
		eebus.NewDeviceRegistry(),
		&recordingTrustController{},
	)

	response, err := svc.GetDeviceStatus(context.Background(), &pb.DeviceRequest{Ski: "not-a-ski"})
	if response != nil {
		t.Errorf("GetDeviceStatus(malformed) response = %+v, want nil", response)
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("GetDeviceStatus(malformed) code = %v, want InvalidArgument", status.Code(err))
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
	svc := bridgegrpc.NewDeviceService(callbacks, bus, "local", registry, &recordingTrustController{})

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
	trust := &recordingTrustController{}
	svc := bridgegrpc.NewDeviceService(callbacks, bus, "test-local-ski", eebus.NewDeviceRegistry(), trust)

	colonSKI := "68:2f:70:8c:eb:a5:df:9a:dc:b9:e6:78:7e:a9:11:d9:fc:3a:c4:90"
	if _, err := svc.RegisterRemoteSKI(context.Background(), &pb.RegisterSKIRequest{Ski: colonSKI}); err != nil {
		t.Fatalf("RegisterRemoteSKI(colon-separated): %v", err)
	}

	want := "682F708CEBA5DF9ADCB9E6787EA911D9FC3AC490"
	if len(trust.registerCalls) != 1 || trust.registerCalls[0] != want {
		t.Fatalf("RegisterSKI calls = %v, want [%s]", trust.registerCalls, want)
	}
}

func TestUnregisterRemoteSKIRejectsMalformedSKI(t *testing.T) {
	client := setupDeviceTest(t)

	_, err := client.UnregisterRemoteSKI(context.Background(), &pb.RegisterSKIRequest{Ski: "not-a-ski"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UnregisterRemoteSKI(malformed) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestPairingCommandsBypassCongestedEventBus(t *testing.T) {
	bus := eebus.NewEventBus()
	callbacks := eebus.NewCallbacks(bus, false)
	trust := &recordingTrustController{}
	svc := bridgegrpc.NewDeviceService(callbacks, bus, "test-local-ski", eebus.NewDeviceRegistry(), trust)

	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)
	for range cap(ch) {
		bus.Publish(eebus.Event{SKI: testValidSKI, Type: eebus.EventTypeDeviceConnected})
	}
	if len(ch) != cap(ch) {
		t.Fatalf("subscriber buffer length = %d, want full capacity %d", len(ch), cap(ch))
	}

	if _, err := svc.RegisterRemoteSKI(context.Background(), &pb.RegisterSKIRequest{Ski: testValidSKI}); err != nil {
		t.Fatalf("RegisterRemoteSKI: %v", err)
	}
	if _, err := svc.UnregisterRemoteSKI(context.Background(), &pb.RegisterSKIRequest{Ski: testValidSKI}); err != nil {
		t.Fatalf("UnregisterRemoteSKI: %v", err)
	}

	want := "682F708CEBA5DF9ADCB9E6787EA911D9FC3AC490"
	if len(trust.registerCalls) != 1 || trust.registerCalls[0] != want {
		t.Errorf("RegisterSKI calls = %v, want [%s]", trust.registerCalls, want)
	}
	if len(trust.unregisterCalls) != 1 || trust.unregisterCalls[0] != want {
		t.Errorf("UnregisterSKI calls = %v, want [%s]", trust.unregisterCalls, want)
	}
	if len(ch) != cap(ch) {
		t.Errorf("subscriber buffer length = %d after commands, want %d", len(ch), cap(ch))
	}
}

func TestPairingControllerErrorsReturnInternalStatus(t *testing.T) {
	tests := []struct {
		name  string
		call  func(*bridgegrpc.DeviceService) (*pb.Empty, error)
		trust *recordingTrustController
	}{
		{
			name: "register",
			call: func(svc *bridgegrpc.DeviceService) (*pb.Empty, error) {
				return svc.RegisterRemoteSKI(context.Background(), &pb.RegisterSKIRequest{Ski: testValidSKI})
			},
			trust: &recordingTrustController{registerErr: errors.New("register failed")},
		},
		{
			name: "unregister",
			call: func(svc *bridgegrpc.DeviceService) (*pb.Empty, error) {
				return svc.UnregisterRemoteSKI(context.Background(), &pb.RegisterSKIRequest{Ski: testValidSKI})
			},
			trust: &recordingTrustController{unregisterErr: errors.New("unregister failed")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus := eebus.NewEventBus()
			svc := bridgegrpc.NewDeviceService(eebus.NewCallbacks(bus, false), bus, "test-local-ski", eebus.NewDeviceRegistry(), tt.trust)

			resp, err := tt.call(svc)
			if resp != nil {
				t.Errorf("response = %v, want nil", resp)
			}
			if status.Code(err) != codes.Internal {
				t.Fatalf("status code = %v, want Internal (error: %v)", status.Code(err), err)
			}
		})
	}
}

func TestSubscribeDeviceEventsTrustRemoved(t *testing.T) {
	bus := eebus.NewEventBus()
	callbacks := eebus.NewCallbacks(bus, false)
	svc := bridgegrpc.NewDeviceService(callbacks, bus, "test-local-ski", eebus.NewDeviceRegistry(), &recordingTrustController{})

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
