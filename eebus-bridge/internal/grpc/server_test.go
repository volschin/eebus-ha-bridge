package grpc_test

import (
	"context"
	"testing"
	"time"

	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
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
	resp, err := client.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Errorf("health status = %v, want SERVING", resp.Status)
	}

	srv.Stop()
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
