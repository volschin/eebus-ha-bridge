package grpc_test

import (
	"context"
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

func (f fakeDeviceOperatingStateReader) OperatingState(string) (string, error) {
	return f.value, f.err
}

func (f fakeDeviceOperatingStateReader) CachedOperatingState(string) (string, error) {
	return f.value, f.err
}

func TestSubscribeMeasurements(t *testing.T) {
	bus := eebus.NewEventBus()
	svc := bridgegrpc.NewMonitoringService(nil, bridgegrpc.MonitoringReaders{}, bus, eebus.NewDeviceRegistry())

	srv := bridgegrpc.NewServer("127.0.0.1", 0, false)
	pb.RegisterMonitoringServiceServer(srv.GRPCServer(), svc)
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
		value float64
	}{
		{typ: "dhw_temperature", value: 48.5},
		{typ: "room_temperature", value: 21.25},
		{typ: "outdoor_temperature", value: 7.75},
		{typ: "flow_temperature", value: 42.5},
		{typ: "return_temperature", value: 37.25},
	}
	for index, expected := range want {
		measurement := result.Measurements[index]
		if measurement.Type != expected.typ || measurement.Value != expected.value || measurement.Unit != "degC" {
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
