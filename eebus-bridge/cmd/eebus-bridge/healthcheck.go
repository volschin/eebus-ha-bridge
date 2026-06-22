package main

import (
	"context"
	"fmt"
	"time"

	"github.com/volschin/eebus-bridge/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// runHealthcheck dials the bridge's gRPC health service and reports SERVING.
// It is invoked via `eebus-bridge -healthcheck` from the Docker HEALTHCHECK so
// the probe reuses the single binary instead of shipping grpc_health_probe.
func runHealthcheck(configPath string) error {
	cfg, err := config.LoadFromFile(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	addr := fmt.Sprintf("%s:%d", cfg.GRPC.Bind, cfg.GRPC.Port)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()

	resp, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		return fmt.Errorf("health check %s: %w", addr, err)
	}
	if resp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		return fmt.Errorf("status %s", resp.GetStatus())
	}
	return nil
}
