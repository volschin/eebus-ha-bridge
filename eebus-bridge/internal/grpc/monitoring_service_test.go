package grpc_test

import (
	"context"
	"errors"
	"testing"
	"time"

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

type fakeDHWTemperatureReader struct {
	value float64
	err   error
}

func (f fakeDHWTemperatureReader) Temperature(string) (float64, error) {
	return f.value, f.err
}

type fakeDeviceOperatingStateReader struct {
	value string
	err   error
}

type fakeMonitoringReader struct {
	entity          spineapi.EntityRemoteInterface
	compatibleCalls int
	powerPhaseErr   error
}

func (f *fakeMonitoringReader) CompatibleEntity(string) eebus.EntityResolution {
	f.compatibleCalls++
	return eebus.EntityResolution{Entity: f.entity, DeviceCount: 1}
}

func (*fakeMonitoringReader) Power(spineapi.EntityRemoteInterface) (float64, error) {
	return 600, nil
}

func (f *fakeMonitoringReader) PowerPerPhase(spineapi.EntityRemoteInterface) ([]float64, error) {
	return []float64{100, 200, 300}, f.powerPhaseErr
}

func (*fakeMonitoringReader) EnergyConsumed(spineapi.EntityRemoteInterface) (float64, error) {
	return 42, nil
}

func (*fakeMonitoringReader) EnergyProduced(spineapi.EntityRemoteInterface) (float64, error) {
	return 12, nil
}

func (*fakeMonitoringReader) CurrentPerPhase(spineapi.EntityRemoteInterface) ([]float64, error) {
	return []float64{1, 2, 3}, nil
}

func (*fakeMonitoringReader) VoltagePerPhase(spineapi.EntityRemoteInterface) ([]float64, error) {
	return []float64{230, 231, 232}, nil
}

func (*fakeMonitoringReader) Frequency(spineapi.EntityRemoteInterface) (float64, error) {
	return 50, nil
}

func (*fakeMonitoringReader) GenericMeasurements(string) ([]usecases.GenericMeasurement, error) {
	return nil, nil
}

func (f fakeDeviceOperatingStateReader) OperatingState(string) (string, error) {
	return f.value, f.err
}

func (f fakeDeviceOperatingStateReader) CachedOperatingState(string) (string, error) {
	return f.value, f.err
}

func TestSnapshotMeasurementsClassifiesPartialReadFailuresPerLeaf(t *testing.T) {
	reader := &fakeMonitoringReader{
		entity:        mocks.NewEntityRemoteInterface(t),
		powerPhaseErr: errors.New("phase cache temporarily unavailable"),
	}
	svc := bridgegrpc.NewMonitoringService(reader, bridgegrpc.MonitoringReaders{}, eebus.NewEventBus(), eebus.NewDeviceRegistry())
	measurements, states, err := svc.SnapshotMeasurementsWithStates(testValidSKI)
	if err != nil || len(measurements.GetMeasurements()) == 0 {
		t.Fatalf("partial snapshot = (%v, %v)", measurements, err)
	}
	if states[pb.MeasurementId_MEASUREMENT_ID_POWER_CONSUMPTION] != pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_AVAILABLE {
		t.Fatalf("power state = %s", states[pb.MeasurementId_MEASUREMENT_ID_POWER_CONSUMPTION])
	}
	for _, id := range []pb.MeasurementId{
		pb.MeasurementId_MEASUREMENT_ID_POWER_L1,
		pb.MeasurementId_MEASUREMENT_ID_POWER_L2,
		pb.MeasurementId_MEASUREMENT_ID_POWER_L3,
	} {
		if states[id] != pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_TEMPORARILY_UNAVAILABLE {
			t.Fatalf("%s state = %s", id, states[id])
		}
	}
}

func TestAttachMeasurementPayloadConvertsEveryDetailDomainOnce(t *testing.T) {
	reader := &fakeMonitoringReader{entity: mocks.NewEntityRemoteInterface(t)}
	svc := bridgegrpc.NewMonitoringService(reader, bridgegrpc.MonitoringReaders{}, eebus.NewEventBus(), eebus.NewDeviceRegistry())
	tests := []struct {
		eventType pb.MeasurementEventType
		types     []string
		ids       []pb.MeasurementId
		values    []float64
		unit      string
	}{
		{pb.MeasurementEventType_MEASUREMENT_EVENT_POWER_PER_PHASE_UPDATED, []string{"power_l1", "power_l2", "power_l3"}, []pb.MeasurementId{pb.MeasurementId_MEASUREMENT_ID_POWER_L1, pb.MeasurementId_MEASUREMENT_ID_POWER_L2, pb.MeasurementId_MEASUREMENT_ID_POWER_L3}, []float64{100, 200, 300}, "W"},
		{pb.MeasurementEventType_MEASUREMENT_EVENT_CURRENT_PER_PHASE_UPDATED, []string{"current_l1", "current_l2", "current_l3"}, []pb.MeasurementId{pb.MeasurementId_MEASUREMENT_ID_CURRENT_L1, pb.MeasurementId_MEASUREMENT_ID_CURRENT_L2, pb.MeasurementId_MEASUREMENT_ID_CURRENT_L3}, []float64{1, 2, 3}, "A"},
		{pb.MeasurementEventType_MEASUREMENT_EVENT_VOLTAGE_PER_PHASE_UPDATED, []string{"voltage_l1", "voltage_l2", "voltage_l3"}, []pb.MeasurementId{pb.MeasurementId_MEASUREMENT_ID_VOLTAGE_L1, pb.MeasurementId_MEASUREMENT_ID_VOLTAGE_L2, pb.MeasurementId_MEASUREMENT_ID_VOLTAGE_L3}, []float64{230, 231, 232}, "V"},
		{pb.MeasurementEventType_MEASUREMENT_EVENT_FREQUENCY_UPDATED, []string{"frequency"}, []pb.MeasurementId{pb.MeasurementId_MEASUREMENT_ID_FREQUENCY}, []float64{50}, "Hz"},
		{pb.MeasurementEventType_MEASUREMENT_EVENT_ENERGY_PRODUCED_UPDATED, []string{"energy_produced"}, []pb.MeasurementId{pb.MeasurementId_MEASUREMENT_ID_ENERGY_PRODUCED}, []float64{12}, "kWh"},
	}
	for _, test := range tests {
		t.Run(test.eventType.String(), func(t *testing.T) {
			reader.compatibleCalls = 0
			event := &pb.MeasurementEvent{Ski: testValidSKI, EventType: test.eventType}
			svc.AttachMeasurementPayload(event, testValidSKI, test.eventType)
			measurements := event.GetMeasurements().GetMeasurements()
			if len(measurements) != len(test.types) {
				t.Fatalf("measurements = %+v", measurements)
			}
			for index, measurement := range measurements {
				if measurement.GetType() != test.types[index] || measurement.GetId() != test.ids[index] || measurement.GetValue() != test.values[index] || measurement.GetUnit() != test.unit {
					t.Fatalf("measurement[%d] = %+v", index, measurement)
				}
			}
			if reader.compatibleCalls != 1 {
				t.Fatalf("entity resolutions = %d, want 1", reader.compatibleCalls)
			}
		})
	}
}

func TestSubscribeMeasurements(t *testing.T) {
	bus := eebus.NewEventBus()
	svc := bridgegrpc.NewMonitoringService(nil, bridgegrpc.MonitoringReaders{}, bus, eebus.NewDeviceRegistry())

	srv := bridgegrpc.NewServer("127.0.0.1", 0, false)
	pb.RegisterMonitoringServiceServer(srv.GRPCServer(), svc)
	srv.SetHealthy(true)
	go srv.Start()
	t.Cleanup(srv.Stop)

	time.Sleep(100 * time.Millisecond)
	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	client := pb.NewMonitoringServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stream, err := client.SubscribeMeasurements(ctx, &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil {
		t.Fatal(err)
	}

	// Give the server-side handler goroutine time to subscribe before publishing.
	time.Sleep(50 * time.Millisecond)
	bus.Publish(eebus.Event{SKI: testValidSKI, Type: eebus.EventTypeMonitoringPowerUpdated})

	evt, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if evt.EventType != pb.MeasurementEventType_MEASUREMENT_EVENT_POWER_UPDATED {
		t.Errorf("EventType = %v, want MEASUREMENT_EVENT_POWER_UPDATED", evt.EventType)
	}
}

func TestGetMeasurementsIncludesTemperatureUseCases(t *testing.T) {
	svc := bridgegrpc.NewMonitoringService(
		usecases.NewMonitoringWrapper(nil, eebus.NewDeviceRegistry(), false),
		bridgegrpc.MonitoringReaders{
			DHW:     fakeDHWTemperatureReader{value: 48.5},
			Room:    fakeDHWTemperatureReader{value: 21.25},
			Outdoor: fakeDHWTemperatureReader{value: 7.75},
			Flow:    fakeDHWTemperatureReader{value: 42.5},
			Return:  fakeDHWTemperatureReader{value: 37.25},
		},
		eebus.NewEventBus(),
		eebus.NewDeviceRegistry(),
	)

	result, err := svc.GetMeasurements(context.Background(), &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil {
		t.Fatalf("GetMeasurements() error = %v", err)
	}
	if len(result.Measurements) != 5 {
		t.Fatalf("GetMeasurements() = %+v", result.Measurements)
	}
	want := []struct {
		typ   string
		id    pb.MeasurementId
		value float64
	}{
		{typ: "dhw_temperature", id: pb.MeasurementId_MEASUREMENT_ID_DHW_TEMPERATURE, value: 48.5},
		{typ: "room_temperature", id: pb.MeasurementId_MEASUREMENT_ID_ROOM_TEMPERATURE, value: 21.25},
		{typ: "outdoor_temperature", id: pb.MeasurementId_MEASUREMENT_ID_OUTDOOR_TEMPERATURE, value: 7.75},
		{typ: "flow_temperature", id: pb.MeasurementId_MEASUREMENT_ID_FLOW_TEMPERATURE, value: 42.5},
		{typ: "return_temperature", id: pb.MeasurementId_MEASUREMENT_ID_RETURN_TEMPERATURE, value: 37.25},
	}
	for index, expected := range want {
		measurement := result.Measurements[index]
		if measurement.Type != expected.typ || measurement.GetId() != expected.id || measurement.Value != expected.value || measurement.Unit != "degC" {
			t.Fatalf("measurement[%d] = %+v, want type=%s value=%g unit=degC", index, measurement, expected.typ, expected.value)
		}
	}
}

func TestGetMeasurementsMissingCompatibleEntityIsNotFound(t *testing.T) {
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice(testValidSKI, eebus.DeviceInfo{
		RemoteEntities: []spineapi.EntityRemoteInterface{mocks.NewEntityRemoteInterface(t)},
	})
	svc := bridgegrpc.NewMonitoringService(
		usecases.NewMonitoringWrapper(nil, registry, false),
		bridgegrpc.MonitoringReaders{DHW: fakeDHWTemperatureReader{err: context.DeadlineExceeded}},
		eebus.NewEventBus(),
		registry,
	)

	result, err := svc.GetMeasurements(context.Background(), &pb.DeviceRequest{Ski: testValidSKI})
	if result != nil || status.Code(err) != codes.NotFound {
		t.Fatalf("GetMeasurements() = (%+v, %v), want nil/NotFound", result, err)
	}
	capabilities, _ := registry.DeviceCapabilities(testValidSKI)
	for _, capability := range capabilities {
		if capability.ID == eebus.CapabilityMonitoring {
			if capability.State != eebus.CapabilityStateUnsupported || capability.Reason != eebus.CapabilityReasonRemoteNotAdvertised {
				t.Fatalf("monitoring capability = %+v", capability)
			}
			return
		}
	}
	t.Fatal("monitoring capability missing")
}

func TestSubscribeMeasurementsIncludesTemperaturePayloads(t *testing.T) {
	bus := eebus.NewEventBus()
	svc := bridgegrpc.NewMonitoringService(
		nil,
		bridgegrpc.MonitoringReaders{
			DHW:     fakeDHWTemperatureReader{value: 49},
			Room:    fakeDHWTemperatureReader{value: 20.5},
			Outdoor: fakeDHWTemperatureReader{value: 6.5},
			Flow:    fakeDHWTemperatureReader{value: 43},
			Return:  fakeDHWTemperatureReader{value: 38},
		},
		bus,
		eebus.NewDeviceRegistry(),
	)

	srv := bridgegrpc.NewServer("127.0.0.1", 0, false)
	pb.RegisterMonitoringServiceServer(srv.GRPCServer(), svc)
	srv.SetHealthy(true)
	go srv.Start()
	t.Cleanup(srv.Stop)

	time.Sleep(100 * time.Millisecond)
	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	client := pb.NewMonitoringServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := client.SubscribeMeasurements(ctx, &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	tests := []struct {
		bridgeEvent eebus.EventType
		eventType   pb.MeasurementEventType
		typ         string
		value       float64
	}{
		{eebus.EventTypeDHWTemperatureUpdated, pb.MeasurementEventType_MEASUREMENT_EVENT_DHW_TEMPERATURE_UPDATED, "dhw_temperature", 49},
		{eebus.EventTypeRoomTemperatureUpdated, pb.MeasurementEventType_MEASUREMENT_EVENT_ROOM_TEMPERATURE_UPDATED, "room_temperature", 20.5},
		{eebus.EventTypeOutdoorTemperatureUpdated, pb.MeasurementEventType_MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_UPDATED, "outdoor_temperature", 6.5},
		{eebus.EventTypeMonitoringFlowTemperatureUpdated, pb.MeasurementEventType_MEASUREMENT_EVENT_FLOW_TEMPERATURE_UPDATED, "flow_temperature", 43},
		{eebus.EventTypeMonitoringReturnTemperatureUpdated, pb.MeasurementEventType_MEASUREMENT_EVENT_RETURN_TEMPERATURE_UPDATED, "return_temperature", 38},
	}
	for _, tt := range tests {
		bus.Publish(eebus.Event{SKI: testValidSKI, Type: tt.bridgeEvent})
		event, err := stream.Recv()
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		measurement := event.GetMeasurement()
		if event.EventType != tt.eventType || measurement.GetType() != tt.typ ||
			measurement.GetValue() != tt.value || measurement.GetUnit() != "degC" {
			t.Fatalf("event = %+v", event)
		}
	}

	supportTests := []struct {
		bridgeEvent eebus.EventType
		eventType   pb.MeasurementEventType
	}{
		{eebus.EventTypeRoomMonitoringSupportUpdated, pb.MeasurementEventType_MEASUREMENT_EVENT_ROOM_TEMPERATURE_SUPPORT_UPDATED},
		{eebus.EventTypeOutdoorMonitoringSupportUpdated, pb.MeasurementEventType_MEASUREMENT_EVENT_OUTDOOR_TEMPERATURE_SUPPORT_UPDATED},
	}
	for _, tt := range supportTests {
		bus.Publish(eebus.Event{SKI: testValidSKI, Type: tt.bridgeEvent})
		event, err := stream.Recv()
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if event.EventType != tt.eventType || event.GetMeasurement() != nil {
			t.Fatalf("support event = %+v", event)
		}
	}
}

func TestGetDeviceDiagnostics(t *testing.T) {
	svc := bridgegrpc.NewMonitoringService(
		nil,
		bridgegrpc.MonitoringReaders{Diagnostics: fakeDeviceOperatingStateReader{value: "normalOperation"}},
		eebus.NewEventBus(),
		eebus.NewDeviceRegistry(),
	)

	result, err := svc.GetDeviceDiagnostics(context.Background(), &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil {
		t.Fatalf("GetDeviceDiagnostics() error = %v", err)
	}
	if result.OperatingState != "normalOperation" || result.Timestamp == nil {
		t.Fatalf("GetDeviceDiagnostics() = %+v", result)
	}
}

func TestGetDeviceDiagnosticsReturnsUnavailableWhenUnavailable(t *testing.T) {
	svc := bridgegrpc.NewMonitoringService(
		nil,
		bridgegrpc.MonitoringReaders{Diagnostics: fakeDeviceOperatingStateReader{err: usecases.ErrDeviceOperatingStateUnavailable}},
		eebus.NewEventBus(),
		eebus.NewDeviceRegistry(),
	)

	_, err := svc.GetDeviceDiagnostics(context.Background(), &pb.DeviceRequest{Ski: testValidSKI})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("GetDeviceDiagnostics() code = %v, want Unavailable", status.Code(err))
	}
}

func TestSubscribeMeasurementsForwardsDeviceOperatingState(t *testing.T) {
	bus := eebus.NewEventBus()
	svc := bridgegrpc.NewMonitoringService(
		nil,
		bridgegrpc.MonitoringReaders{Diagnostics: fakeDeviceOperatingStateReader{value: "futureVendorState"}},
		bus,
		eebus.NewDeviceRegistry(),
	)

	srv := bridgegrpc.NewServer("127.0.0.1", 0, false)
	pb.RegisterMonitoringServiceServer(srv.GRPCServer(), svc)
	srv.SetHealthy(true)
	go srv.Start()
	t.Cleanup(srv.Stop)

	time.Sleep(100 * time.Millisecond)
	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	client := pb.NewMonitoringServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := client.SubscribeMeasurements(ctx, &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	bus.Publish(eebus.Event{SKI: testValidSKI, Type: eebus.EventTypeMonitoringDeviceOperatingStateUpdated})

	event, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if event.EventType != pb.MeasurementEventType_MEASUREMENT_EVENT_DEVICE_OPERATING_STATE_UPDATED ||
		event.GetDeviceDiagnostics().GetOperatingState() != "futureVendorState" ||
		event.GetDeviceDiagnostics().GetTimestamp() == nil {
		t.Fatalf("event = %+v", event)
	}
}
