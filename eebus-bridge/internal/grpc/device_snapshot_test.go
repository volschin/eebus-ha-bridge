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
	"google.golang.org/protobuf/types/known/timestamppb"
)

type snapshotPayloads struct {
	started  chan struct{}
	release  chan struct{}
	registry *eebus.DeviceRegistry
}

func (f *snapshotPayloads) SnapshotMeasurements(ski string) (*pb.MeasurementList, error) {
	if f.started != nil {
		close(f.started)
		<-f.release
	}
	if f.registry != nil {
		f.registry.RecordCapabilityRead(ski, eebus.CapabilityMonitoring, nil)
	}
	return &pb.MeasurementList{Measurements: []*pb.MeasurementEntry{{
		Type: "power_consumption", Id: pb.MeasurementId_MEASUREMENT_ID_POWER_CONSUMPTION.Enum(),
		Value: 0, Unit: "W", Timestamp: timestamppb.Now(),
	}}}, nil
}

func (*snapshotPayloads) AttachMeasurementPayload(event *pb.MeasurementEvent, _ string, eventType pb.MeasurementEventType) {
	switch eventType {
	case pb.MeasurementEventType_MEASUREMENT_EVENT_POWER_UPDATED:
		event.Data = &pb.MeasurementEvent_Power{Power: &pb.PowerMeasurement{Watts: 0}}
	case pb.MeasurementEventType_MEASUREMENT_EVENT_DEVICE_OPERATING_STATE_UPDATED:
		event.Data = &pb.MeasurementEvent_DeviceDiagnostics{DeviceDiagnostics: &pb.DeviceDiagnosticsData{OperatingState: "running"}}
	}
}

func (f *snapshotPayloads) AttachLPCPayload(event *pb.LPCEvent, ski string, eventType pb.LPCEventType) {
	if f.registry != nil {
		f.registry.RecordCapabilityRead(ski, eebus.CapabilityLPC, nil)
		f.registry.RecordCapabilityRead(ski, eebus.CapabilityFailsafe, nil)
	}
	switch eventType {
	case pb.LPCEventType_LPC_EVENT_LIMIT_UPDATED:
		event.Data = &pb.LPCEvent_LimitUpdate{LimitUpdate: &pb.LoadLimit{ValueWatts: 0}}
	case pb.LPCEventType_LPC_EVENT_FAILSAFE_UPDATED:
		event.Data = &pb.LPCEvent_FailsafeUpdate{FailsafeUpdate: &pb.FailsafeLimit{ValueWatts: 0}}
	}
}

func (f *snapshotPayloads) SnapshotHeartbeat(ski string) (*pb.HeartbeatStatus, error) {
	if f.registry != nil {
		f.registry.RecordCapabilityRead(ski, eebus.CapabilityHeartbeat, nil)
	}
	return &pb.HeartbeatStatus{Running: true, WithinDuration: true}, nil
}

func (*snapshotPayloads) AttachDHWPayload(event *pb.DHWEvent, _ string) {
	event.Setpoint = &pb.DHWSetpoint{ValueCelsius: 0}
}

func (*snapshotPayloads) AttachDHWSystemFunctionPayload(event *pb.DHWSystemFunctionEvent, _ string) {
	event.State = &pb.DHWSystemFunctionState{OperationMode: "auto"}
}

func (*snapshotPayloads) AttachRoomHeatingPayload(event *pb.RoomHeatingEvent, _ string) bool {
	event.State = &pb.RoomHeatingState{Setpoint: &pb.RoomHeatingSetpoint{ValueCelsius: 0}}
	return true
}

func (*snapshotPayloads) AttachOHPCFPayload(event *pb.OHPCFEvent, _ string, _ eebus.EventType) bool {
	event.Flexibility = &pb.CompressorFlexibility{Available: false}
	return true
}

func snapshotService(bus *eebus.EventBus, registry *eebus.DeviceRegistry, payloads *snapshotPayloads) *bridgegrpc.DeviceService {
	payloads.registry = registry
	sources := bridgegrpc.DeviceStatePayloadSources{
		Monitoring: payloads, LPC: payloads, DHW: payloads, HVAC: payloads, OHPCF: payloads,
	}
	return bridgegrpc.NewDeviceService(
		eebus.NewCallbacks(bus, false), bus, "local-ski", registry, &recordingTrustController{},
		bridgegrpc.WithDeviceStatePayloads(sources),
	)
}

func snapshotFieldState(snapshot *pb.DeviceSnapshot, id pb.SnapshotFieldId) pb.SnapshotValueState {
	for _, field := range snapshot.GetFieldStates() {
		if field.GetId() == id {
			return field.GetState()
		}
	}
	return pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_UNKNOWN
}

func TestDeviceSnapshotKeepsZeroValuesAndPerFieldState(t *testing.T) {
	bus := eebus.NewEventBus()
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice(testValidSKI, eebus.DeviceInfo{Brand: "Vaillant", Model: "VR940"})
	registry.MarkConnected(testValidSKI)
	bus.Publish(eebus.Event{SKI: testValidSKI, Type: eebus.EventTypeMonitoringPowerUpdated})
	service := snapshotService(bus, registry, &snapshotPayloads{})

	snapshot, err := service.GetDeviceSnapshot(context.Background(), &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil {
		t.Fatalf("GetDeviceSnapshot: %v", err)
	}
	if snapshot.GetSki() != eebus.NormalizeSKI(testValidSKI) || snapshot.GetLocalSki() != "local-ski" || snapshot.GetEventRevision() != 1 {
		t.Fatalf("snapshot identity = %+v", snapshot)
	}
	if !snapshot.GetConnection().GetConnected() || snapshot.GetClassification().GetBrand() != "Vaillant" {
		t.Fatalf("snapshot identity state = %+v", snapshot)
	}
	measurement := snapshot.GetMeasurements()[0]
	if measurement.GetValue() != 0 || measurement.GetId() != pb.MeasurementId_MEASUREMENT_ID_POWER_CONSUMPTION || measurement.GetUnit() != "W" {
		t.Fatalf("zero measurement = %+v", measurement)
	}
	if snapshotFieldState(snapshot, pb.SnapshotFieldId_SNAPSHOT_FIELD_POWER) != pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_AVAILABLE ||
		snapshotFieldState(snapshot, pb.SnapshotFieldId_SNAPSHOT_FIELD_ENERGY_CONSUMED) == pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_AVAILABLE {
		t.Fatalf("field states = %+v", snapshot.GetFieldStates())
	}
	if len(snapshot.GetFieldStates()) != 34 {
		t.Fatalf("field state count = %d, want 34", len(snapshot.GetFieldStates()))
	}
	for _, capability := range snapshot.GetCapabilities().GetCapabilities() {
		if capability.GetId() == pb.CapabilityId_CAPABILITY_MONITORING &&
			capability.GetState() != pb.CapabilityState_CAPABILITY_STATE_AVAILABLE {
			t.Fatalf("monitoring capability is stale after successful read: %v", capability)
		}
	}
}

func TestDeviceSnapshotRejectsUnknownExplicitSKIWithoutRegistering(t *testing.T) {
	trust := &recordingTrustController{}
	service := bridgegrpc.NewDeviceService(
		eebus.NewCallbacks(eebus.NewEventBus(), false), eebus.NewEventBus(), "local", eebus.NewDeviceRegistry(), trust,
		bridgegrpc.WithDeviceStatePayloads(bridgegrpc.DeviceStatePayloadSources{}),
	)
	result, err := service.GetDeviceSnapshot(context.Background(), &pb.DeviceRequest{Ski: testValidSKI})
	if result != nil || status.Code(err) != codes.NotFound || len(trust.registerCalls) != 0 {
		t.Fatalf("GetDeviceSnapshot = (%v, %v), registrations=%v", result, err, trust.registerCalls)
	}
}

func TestUnsupportedOHPCFDoesNotDiscardMonitoringOrConnection(t *testing.T) {
	bus := eebus.NewEventBus()
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice(testValidSKI, eebus.DeviceInfo{})
	registry.MarkConnected(testValidSKI)
	registry.SetCapability(
		testValidSKI,
		eebus.CapabilityOHPCF,
		eebus.CapabilityStateUnsupported,
		eebus.CapabilityReasonRemoteNotAdvertised,
	)
	payloads := &snapshotPayloads{}
	service := bridgegrpc.NewDeviceService(
		eebus.NewCallbacks(bus, false), bus, "local", registry, &recordingTrustController{},
		bridgegrpc.WithDeviceStatePayloads(bridgegrpc.DeviceStatePayloadSources{
			Monitoring: payloads,
			LPC:        payloads,
		}),
	)
	snapshot, err := service.GetDeviceSnapshot(context.Background(), &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.GetConnection().GetConnected() || len(snapshot.GetMeasurements()) == 0 {
		t.Fatalf("partial snapshot lost usable domains: %+v", snapshot)
	}
	if snapshot.GetCompressorFlexibility() != nil || snapshot.GetCompressorFlexibilityState() != pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_UNSUPPORTED {
		t.Fatalf("OHPCF partial status = %+v", snapshot)
	}
}

func TestInitialSnapshotBuffersConcurrentEvent(t *testing.T) {
	bus := eebus.NewEventBus()
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice(testValidSKI, eebus.DeviceInfo{})
	payloads := &snapshotPayloads{started: make(chan struct{}), release: make(chan struct{})}
	service := snapshotService(bus, registry, payloads)
	server := bridgegrpc.NewServer("127.0.0.1", 0, false)
	pb.RegisterDeviceServiceServer(server.GRPCServer(), service)
	server.SetHealthy(true)
	go server.Start()
	t.Cleanup(server.Stop)
	readyContext, readyCancel := context.WithTimeout(context.Background(), time.Second)
	defer readyCancel()
	if err := server.WaitReady(readyContext); err != nil {
		t.Fatal(err)
	}
	connection, err := grpc.NewClient(server.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := pb.NewDeviceServiceClient(connection).SubscribeDeviceState(ctx, &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil {
		t.Fatal(err)
	}
	firstResult := make(chan *pb.DeviceStateEvent, 1)
	firstError := make(chan error, 1)
	go func() {
		first, recvErr := stream.Recv()
		if recvErr != nil {
			firstError <- recvErr
			return
		}
		firstResult <- first
	}()
	select {
	case <-payloads.started:
	case err := <-firstError:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	bus.Publish(eebus.Event{SKI: testValidSKI, Type: eebus.EventTypeMonitoringPowerUpdated})
	close(payloads.release)
	first := <-firstResult
	if first.GetInitialSnapshot() == nil || first.GetRevision() != 0 {
		t.Fatalf("initial = %+v", first)
	}
	second, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if second.GetRevision() != 1 || second.GetMeasurement().GetPower().GetWatts() != 0 {
		t.Fatalf("buffered delta = %+v", second)
	}
}

// activeDiagnosticsPayloads embeds snapshotPayloads and additionally
// implements snapshotDeviceDiagnosticsSource, returning a value that differs
// from AttachMeasurementPayload's canned "running" so the two paths are
// distinguishable in assertions.
type activeDiagnosticsPayloads struct {
	*snapshotPayloads
	calls int
}

func (f *activeDiagnosticsPayloads) SnapshotDeviceDiagnostics(string) *pb.DeviceDiagnosticsData {
	f.calls++
	return &pb.DeviceDiagnosticsData{OperatingState: "standby"}
}

func TestDeviceSnapshotPrefersActiveDiagnosticsReadOverCache(t *testing.T) {
	bus := eebus.NewEventBus()
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice(testValidSKI, eebus.DeviceInfo{})
	registry.MarkConnected(testValidSKI)
	payloads := &activeDiagnosticsPayloads{snapshotPayloads: &snapshotPayloads{registry: registry}}
	service := bridgegrpc.NewDeviceService(
		eebus.NewCallbacks(bus, false), bus, "local", registry, &recordingTrustController{},
		bridgegrpc.WithDeviceStatePayloads(bridgegrpc.DeviceStatePayloadSources{Monitoring: payloads}),
	)

	snapshot, err := service.GetDeviceSnapshot(context.Background(), &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil {
		t.Fatalf("GetDeviceSnapshot: %v", err)
	}
	if payloads.calls != 1 {
		t.Fatalf("SnapshotDeviceDiagnostics calls = %d, want 1", payloads.calls)
	}
	if got := snapshot.GetDeviceDiagnostics().GetOperatingState(); got != "standby" {
		t.Fatalf("operating state = %q, want active-read value %q", got, "standby")
	}
	if snapshotFieldState(snapshot, pb.SnapshotFieldId_SNAPSHOT_FIELD_DEVICE_OPERATING_STATE) != pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_AVAILABLE {
		t.Fatalf("device operating state field = %+v", snapshot.GetFieldStates())
	}
}
