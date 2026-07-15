package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/volschin/eebus-bridge/internal/config"
	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
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

	host := cfg.GRPC.Bind
	serverName := ""
	if cfg.GRPC.Security.Mode == config.GRPCSecurityModeTLSToken {
		serverName, err = tlsCertificateServerName(cfg.GRPC.Security.TLSCertFile)
		if err != nil {
			return fmt.Errorf("reading TLS server identity: %w", err)
		}
		switch host {
		case "0.0.0.0":
			host = "127.0.0.1"
		case "::":
			host = "::1"
		}
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", cfg.GRPC.Port))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := bridgegrpc.NewClient(addr, bridgegrpc.ClientSecurityConfig{
		Mode:       cfg.GRPC.Security.Mode,
		CACertFile: cfg.GRPC.Security.TLSCertFile,
		TokenFile:  cfg.GRPC.Security.TokenFile,
		ServerName: serverName,
	})
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	resp, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		return fmt.Errorf("health check %s: %w", addr, err)
	}
	if resp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		return fmt.Errorf("status %s", resp.GetStatus())
	}
	return nil
}

func tlsCertificateServerName(certFile string) (string, error) {
	data, err := os.ReadFile(certFile) // #nosec G304 -- operator-supplied configuration path
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", fmt.Errorf("no PEM certificate found in %q", certFile)
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	if len(certificate.DNSNames) > 0 {
		return certificate.DNSNames[0], nil
	}
	if len(certificate.IPAddresses) > 0 {
		return certificate.IPAddresses[0].String(), nil
	}
	return "", fmt.Errorf("certificate %q has no DNS or IP subject alternative name", certFile)
}
