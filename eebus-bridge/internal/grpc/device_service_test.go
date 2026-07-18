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

func setupDeviceStateTest(t *testing.T) (pb.DeviceServiceClient, *eebus.EventBus, *eebus.DeviceRegistry) {
	t.Helper()
	bus := eebus.NewEventBus()
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice(testValidSKI, eebus.DeviceInfo{})
	svc := bridgegrpc.NewDeviceService(
		eebus.NewCallbacks(bus, false), bus, "test-local-ski", registry, &recordingTrustController{},
	)
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
	return pb.NewDeviceServiceClient(conn), bus, registry
}

func TestSubscribeDeviceStateStartsWithRevisionAndResync(t *testing.T) {
	client, bus, _ := setupDeviceStateTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := client.SubscribeDeviceState(ctx, &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil {
		t.Fatalf("SubscribeDeviceState: %v", err)
	}
	initial, err := stream.Recv()
	if err != nil {
		t.Fatalf("initial Recv: %v", err)
	}
	if initial.Ski != eebus.NormalizeSKI(testValidSKI) || initial.Revision != 0 ||
		initial.GetResyncRequired().GetReason() != pb.ResyncReason_RESYNC_REASON_INITIAL_STATE_REQUIRED ||
		initial.EventTime == nil {
		t.Fatalf("initial envelope = %+v", initial)
	}

	bus.Publish(eebus.Event{SKI: testValidSKI, Type: eebus.EventTypeLPCLimitUpdated})
	event, err := stream.Recv()
	if err != nil {
		t.Fatalf("event Recv: %v", err)
	}
	if event.Revision != 1 || event.GetLpc().GetEventType() != pb.LPCEventType_LPC_EVENT_LIMIT_UPDATED {
		t.Fatalf("LPC envelope = %+v", event)
	}
}

func TestSubscribeDeviceStatePublishesCapabilityTruth(t *testing.T) {
	client, bus, registry := setupDeviceStateTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := client.SubscribeDeviceState(ctx, &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil {
		t.Fatalf("SubscribeDeviceState: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("initial Recv: %v", err)
	}
	registry.RecordCapabilitySupport(testValidSKI, eebus.CapabilityDHW, false)
	bus.Publish(eebus.Event{SKI: testValidSKI, Type: eebus.EventTypeDHWUseCaseSupportUpdated})
	event, err := stream.Recv()
	if err != nil {
		t.Fatalf("capability Recv: %v", err)
	}
	capabilities := event.GetCapability()
	if capabilities == nil || capabilities.Ski != eebus.NormalizeSKI(testValidSKI) {
		t.Fatalf("capability envelope = %+v", event)
	}
	for _, capability := range capabilities.Capabilities {
		if capability.Id == pb.CapabilityId_CAPABILITY_DHW {
			if capability.State != pb.CapabilityState_CAPABILITY_STATE_UNSUPPORTED {
				t.Fatalf("DHW capability = %+v", capability)
			}
			return
		}
	}
	t.Fatal("DHW capability missing")
}

func TestSubscribeDeviceStateAttachesBestEffortPayload(t *testing.T) {
	bus := eebus.NewEventBus()
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice(testValidSKI, eebus.DeviceInfo{})
	hvacService := bridgegrpc.NewHVACService(
		nil,
		nil,
		fakeDHWTemperatureReader{value: 20.5},
		bus,
		registry,
	)
	svc := bridgegrpc.NewDeviceService(
		eebus.NewCallbacks(bus, false),
		bus,
		"test-local-ski",
		registry,
		&recordingTrustController{},
		bridgegrpc.WithDeviceStatePayloads(bridgegrpc.DeviceStatePayloadSources{HVAC: hvacService}),
	)
	srv := bridgegrpc.NewServer("127.0.0.1", 0, false)
	pb.RegisterDeviceServiceServer(srv.GRPCServer(), svc)
	go srv.Start()
	t.Cleanup(srv.Stop)
	time.Sleep(100 * time.Millisecond)
	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := pb.NewDeviceServiceClient(conn).SubscribeDeviceState(ctx, &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil {
		t.Fatalf("SubscribeDeviceState: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("initial Recv: %v", err)
	}
	bus.Publish(eebus.Event{SKI: testValidSKI, Type: eebus.EventTypeRoomTemperatureUpdated})

	event, err := stream.Recv()
	if err != nil {
		t.Fatalf("event Recv: %v", err)
	}
	state := event.GetHvac().GetState()
	if event.GetHvac().GetEventType() != pb.RoomHeatingEventType_ROOM_HEATING_EVENT_CURRENT_TEMPERATURE_UPDATED ||
		state == nil || state.GetCurrentTemperatureCelsius() != 20.5 {
		t.Fatalf("device state HVAC payload = %+v", event)
	}
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

func TestGetServerInfoAdvertisesOnlyImplementedFeatures(t *testing.T) {
	client := setupDeviceTest(t)
	info, err := client.GetServerInfo(context.Background(), &pb.Empty{})
	if err != nil {
		t.Fatalf("GetServerInfo: %v", err)
	}
	if info.GetApiMajor() != bridgegrpc.APIMajor || info.GetApiMinor() != bridgegrpc.APIMinor {
		t.Fatalf("API version = %d.%d", info.GetApiMajor(), info.GetApiMinor())
	}
	if info.GetBridgeBuildVersion() == "" || info.GetLocalSki() != "test-local-ski" {
		t.Fatalf("server info = %v", info)
	}
	features := make(map[pb.FeatureId]bool, len(info.GetFeatures()))
	for _, feature := range info.GetFeatures() {
		features[feature] = true
	}
	for _, feature := range []pb.FeatureId{
		pb.FeatureId_FEATURE_EXPLICIT_CAPABILITIES,
		pb.FeatureId_FEATURE_CONSOLIDATED_DEVICE_STREAM,
		pb.FeatureId_FEATURE_PROVIDER_SAMPLE_INVALIDATION,
	} {
		if !features[feature] {
			t.Errorf("implemented feature %s not advertised", feature)
		}
	}
	for _, feature := range []pb.FeatureId{
		pb.FeatureId_FEATURE_DEVICE_SNAPSHOT,
		pb.FeatureId_FEATURE_TYPED_MEASUREMENTS,
	} {
		if features[feature] {
			t.Errorf("future feature %s advertised before implementation", feature)
		}
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

func TestGetDeviceCapabilitiesContractRemoteNotAdvertised(t *testing.T) {
	bus := eebus.NewEventBus()
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice(testValidSKI, eebus.DeviceInfo{})
	registry.RecordCapabilitySupport(testValidSKI, eebus.CapabilityDHW, false)
	svc := bridgegrpc.NewDeviceService(
		eebus.NewCallbacks(bus, false), bus, "local", registry, &recordingTrustController{},
	)

	response, err := svc.GetDeviceCapabilities(context.Background(), &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil {
		t.Fatalf("GetDeviceCapabilities: %v", err)
	}
	if response.Ski != eebus.NormalizeSKI(testValidSKI) || len(response.Capabilities) != len(eebus.AllCapabilities) {
		t.Fatalf("GetDeviceCapabilities = %+v", response)
	}
	for _, capability := range response.Capabilities {
		if capability.Id == pb.CapabilityId_CAPABILITY_DHW {
			if capability.State != pb.CapabilityState_CAPABILITY_STATE_UNSUPPORTED ||
				capability.Reason != pb.CapabilityReason_CAPABILITY_REASON_REMOTE_NOT_ADVERTISED ||
				capability.LastChanged == nil {
				t.Fatalf("DHW capability = %+v", capability)
			}
			return
		}
	}
	t.Fatal("DHW capability missing")
}

func deviceCapabilityContract(t *testing.T, registry *eebus.DeviceRegistry, id pb.CapabilityId) *pb.DeviceCapability {
	t.Helper()
	if !registry.KnownDevice(testValidSKI) {
		registry.AddDevice(testValidSKI, eebus.DeviceInfo{})
	}
	bus := eebus.NewEventBus()
	svc := bridgegrpc.NewDeviceService(
		eebus.NewCallbacks(bus, false), bus, "local", registry, &recordingTrustController{},
	)
	response, err := svc.GetDeviceCapabilities(context.Background(), &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil {
		t.Fatalf("GetDeviceCapabilities: %v", err)
	}
	for _, capability := range response.Capabilities {
		if capability.Id == id {
			return capability
		}
	}
	t.Fatalf("capability %v missing", id)
	return nil
}

func TestGetDeviceCapabilitiesUnknownSKIReturnsNotFoundWithoutMutation(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	bus := eebus.NewEventBus()
	svc := bridgegrpc.NewDeviceService(
		eebus.NewCallbacks(bus, false), bus, "local", registry, &recordingTrustController{},
	)

	_, err := svc.GetDeviceCapabilities(context.Background(), &pb.DeviceRequest{Ski: testValidSKI})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v, want NotFound", status.Code(err))
	}
	if _, exists := registry.DeviceCapabilities(testValidSKI); exists {
		t.Fatal("GetDeviceCapabilities mutated registry for unknown SKI")
	}
}

func TestGetDeviceCapabilitiesContractLocalDisabled(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	registry.SetLocalCapabilityEnabled(eebus.CapabilityOHPCF, false)
	capability := deviceCapabilityContract(t, registry, pb.CapabilityId_CAPABILITY_OHPCF)
	if capability.State != pb.CapabilityState_CAPABILITY_STATE_UNSUPPORTED || capability.Reason != pb.CapabilityReason_CAPABILITY_REASON_LOCAL_DISABLED {
		t.Fatalf("capability = %+v", capability)
	}
}

func TestGetDeviceCapabilitiesContractEntityNotBound(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	registry.RecordCapabilityEntityNotBound(testValidSKI, eebus.CapabilityLPC)
	capability := deviceCapabilityContract(t, registry, pb.CapabilityId_CAPABILITY_LPC)
	if capability.State != pb.CapabilityState_CAPABILITY_STATE_TEMPORARILY_UNAVAILABLE || capability.Reason != pb.CapabilityReason_CAPABILITY_REASON_ENTITY_NOT_BOUND {
		t.Fatalf("capability = %+v", capability)
	}
}

func TestGetDeviceCapabilitiesContractTemporaryReadFailure(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	registry.RecordCapabilityRead(testValidSKI, eebus.CapabilityMonitoring, errors.New("read failed"))
	capability := deviceCapabilityContract(t, registry, pb.CapabilityId_CAPABILITY_MONITORING)
	if capability.State != pb.CapabilityState_CAPABILITY_STATE_TEMPORARILY_UNAVAILABLE || capability.Reason != pb.CapabilityReason_CAPABILITY_REASON_READ_FAILED {
		t.Fatalf("capability = %+v", capability)
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

func TestPairingCommandsRejectNilRequest(t *testing.T) {
	bus := eebus.NewEventBus()
	svc := bridgegrpc.NewDeviceService(
		eebus.NewCallbacks(bus, false), bus, "local", eebus.NewDeviceRegistry(), &recordingTrustController{},
	)
	if _, err := svc.RegisterRemoteSKI(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("RegisterRemoteSKI(nil) code = %v", status.Code(err))
	}
	if _, err := svc.UnregisterRemoteSKI(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UnregisterRemoteSKI(nil) code = %v", status.Code(err))
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
