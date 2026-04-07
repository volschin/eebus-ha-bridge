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

func TestSubscribeMeasurements(t *testing.T) {
	bus := eebus.NewEventBus()
	svc := bridgegrpc.NewMonitoringService(nil, bus)

	srv := bridgegrpc.NewServer(0)
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
