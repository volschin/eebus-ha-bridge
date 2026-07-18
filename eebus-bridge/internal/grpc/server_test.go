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
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func TestServerStartStop(t *testing.T) {
	srv := bridgegrpc.NewServer("127.0.0.1", 0, false) // port 0 = random free port

	go func() {
		if err := srv.Start(); err != nil {
			t.Logf("server stopped: %v", err)
		}
	}()

	time.Sleep(100 * time.Millisecond)
	addr := srv.Addr()
	if addr == "" {
		t.Fatal("server has no address")
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	client := grpc_health_v1.NewHealthClient(conn)
	initial, err := client.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("initial health check failed: %v", err)
	}
	if initial.Status != grpc_health_v1.HealthCheckResponse_NOT_SERVING {
		t.Errorf("initial health status = %v, want NOT_SERVING", initial.Status)
	}
	srv.SetHealthy(true)
	resp, err := client.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Errorf("health status = %v, want SERVING", resp.Status)
	}

	srv.Stop()
}

func TestServerReadinessGatesApplicationRPCsButNotHealth(t *testing.T) {
	bus := eebus.NewEventBus()
	registry := eebus.NewDeviceRegistry()
	srv := bridgegrpc.NewServer("127.0.0.1", 0, false)
	pb.RegisterDeviceServiceServer(
		srv.GRPCServer(),
		bridgegrpc.NewDeviceService(eebus.NewCallbacks(bus, false), bus, "local", registry, nil),
	)
	go func() { _ = srv.Start() }()
	t.Cleanup(srv.Stop)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	health, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil || health.Status != grpc_health_v1.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("pre-commit health = %v, %v", health, err)
	}
	client := pb.NewDeviceServiceClient(conn)
	if _, err := client.GetStatus(ctx, &pb.Empty{}); status.Code(err) != codes.Unavailable {
		t.Fatalf("pre-commit unary error = %v, want unavailable", err)
	}
	stream, err := client.SubscribeDeviceEvents(ctx, &pb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); status.Code(err) != codes.Unavailable {
		t.Fatalf("pre-commit stream error = %v, want unavailable", err)
	}

	srv.SetHealthy(true)
	if _, err := client.GetStatus(ctx, &pb.Empty{}); err != nil {
		t.Fatalf("post-commit unary: %v", err)
	}
}

// TestServerStopReturnsWithOpenStream guards against a regression where Stop
// blocks forever behind grpc.Server.GracefulStop when a client holds a
// long-lived stream open (e.g. HA's SubscribeMeasurements) and never cancels
// it — exactly what a controlled shutdown (SIGTERM, RF-06 watchdog restart)
// must not depend on.
func TestServerStopReturnsWithOpenStream(t *testing.T) {
	srv := bridgegrpc.NewServer("127.0.0.1", 0, false)

	go func() {
		if err := srv.Start(); err != nil {
			t.Logf("server stopped: %v", err)
		}
	}()

	time.Sleep(100 * time.Millisecond)
	addr := srv.Addr()
	if addr == "" {
		t.Fatal("server has no address")
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	client := grpc_health_v1.NewHealthClient(conn)
	// Watch is a server-streaming RPC; leaving its context uncancelled
	// simulates a client (HA) that never tears down its subscribe stream.
	stream, err := client.Watch(context.Background(), &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("watch failed: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("initial watch recv failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		srv.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop() did not return within 10s with an open stream; GracefulStop is hanging")
	}
}
