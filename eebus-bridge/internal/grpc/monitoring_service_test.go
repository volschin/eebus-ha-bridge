package grpc_test

import (
	"context"
	"testing"
	"time"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fakeDHWTemperatureReader struct {
	value float64
	err   error
}

func (f fakeDHWTemperatureReader) Temperature(string) (float64, error) {
	return f.value, f.err
}

func TestSubscribeMeasurements(t *testing.T) {
	bus := eebus.NewEventBus()
	svc := bridgegrpc.NewMonitoringService(nil, nil, bus, eebus.NewDeviceRegistry())

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

	stream, err := client.SubscribeMeasurements(ctx, &pb.DeviceRequest{Ski: "test-ski"})
	if err != nil {
		t.Fatal(err)
	}

	// Give the server-side handler goroutine time to subscribe before publishing.
	time.Sleep(50 * time.Millisecond)
	bus.Publish(eebus.Event{SKI: "test-ski", Type: "monitoring.power_updated"})

	evt, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if evt.EventType != pb.MeasurementEventType_MEASUREMENT_EVENT_POWER_UPDATED {
		t.Errorf("EventType = %v, want MEASUREMENT_EVENT_POWER_UPDATED", evt.EventType)
	}
}

func TestGetMeasurementsIncludesMDTTemperature(t *testing.T) {
	svc := bridgegrpc.NewMonitoringService(
		usecases.NewMonitoringWrapper(nil, eebus.NewDeviceRegistry(), false),
		fakeDHWTemperatureReader{value: 48.5},
		eebus.NewEventBus(),
		eebus.NewDeviceRegistry(),
	)

	result, err := svc.GetMeasurements(context.Background(), &pb.DeviceRequest{Ski: "test-ski"})
	if err != nil {
		t.Fatalf("GetMeasurements() error = %v", err)
	}
	if len(result.Measurements) != 1 || result.Measurements[0].Type != "dhw_temperature" ||
		result.Measurements[0].Value != 48.5 || result.Measurements[0].Unit != "degC" {
		t.Fatalf("GetMeasurements() = %+v", result.Measurements)
	}
}

func TestSubscribeMeasurementsIncludesMDTTemperaturePayload(t *testing.T) {
	bus := eebus.NewEventBus()
	svc := bridgegrpc.NewMonitoringService(
		nil,
		fakeDHWTemperatureReader{value: 49},
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
	stream, err := client.SubscribeMeasurements(ctx, &pb.DeviceRequest{Ski: "test-ski"})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	bus.Publish(eebus.Event{SKI: "test-ski", Type: "dhw.temperature_updated"})
	event, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if event.EventType != pb.MeasurementEventType_MEASUREMENT_EVENT_DHW_TEMPERATURE_UPDATED ||
		event.GetMeasurement().GetValue() != 49 {
		t.Fatalf("event = %+v", event)
	}
}
