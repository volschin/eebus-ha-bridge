package grpc

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/config"
	"github.com/volschin/eebus-bridge/internal/eebus"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

type acceptingLPCService struct {
	pb.UnimplementedLPCServiceServer
}

type acceptingTrustController struct{}

func (acceptingTrustController) RegisterSKI(string) error   { return nil }
func (acceptingTrustController) UnregisterSKI(string) error { return nil }

func (acceptingLPCService) WriteConsumptionLimit(context.Context, *pb.WriteLoadLimitRequest) (*pb.Empty, error) {
	return &pb.Empty{}, nil
}

func writeTestCredentials(t *testing.T, token string) (string, string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile := filepath.Join(dir, "server.crt")
	keyFile := filepath.Join(dir, "server.key")
	tokenFile := filepath.Join(dir, "token")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenFile, []byte(token+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile, tokenFile
}

func waitForServer(t *testing.T, srv *Server, startErr <-chan error) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if addr := srv.Addr(); addr != "" {
			return addr
		}
		select {
		case err := <-startErr:
			t.Fatalf("server start: %v", err)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server did not start")
	return ""
}

func TestTLSTokenSecuresUnaryWriteStreamAndHealth(t *testing.T) {
	const token = "integration-secret-token-5f0fd55d"
	certFile, keyFile, tokenFile := writeTestCredentials(t, token)
	keyMaterial, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previousLogWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousLogWriter) })
	security := config.GRPCSecurityConfig{
		Mode: config.GRPCSecurityModeTLSToken, TLSCertFile: certFile, TLSKeyFile: keyFile, TokenFile: tokenFile,
	}
	srv, err := NewServerWithSecurity("127.0.0.1", 0, true, security)
	if err != nil {
		t.Fatal(err)
	}
	bus := eebus.NewEventBus()
	registry := eebus.NewDeviceRegistry()
	const remoteSKI = "0123456789abcdef0123456789abcdef01234567"
	registry.AddDevice(remoteSKI, eebus.DeviceInfo{})
	device := NewDeviceService(
		eebus.NewCallbacks(bus, false), bus, "local-ski", registry, acceptingTrustController{},
		WithDeviceStatePayloads(DeviceStatePayloadSources{}),
	)
	pb.RegisterDeviceServiceServer(srv.GRPCServer(), device)
	pb.RegisterLPCServiceServer(srv.GRPCServer(), acceptingLPCService{})
	startErr := make(chan error, 1)
	go func() { startErr <- srv.Start() }()
	defer srv.Stop()
	addr := waitForServer(t, srv, startErr)
	srv.SetHealthy(true)

	valid, err := NewClient(addr, ClientSecurityConfig{
		Mode: config.GRPCSecurityModeTLSToken, CACertFile: certFile, TokenFile: tokenFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer valid.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dc := pb.NewDeviceServiceClient(valid)
	if _, err := dc.GetStatus(ctx, &pb.Empty{}); err != nil {
		t.Fatalf("secured read: %v", err)
	}
	if _, err := dc.GetServerInfo(ctx, &pb.Empty{}); err != nil {
		t.Fatalf("secured server info: %v", err)
	}
	if _, err := dc.RegisterRemoteSKI(ctx, &pb.RegisterSKIRequest{Ski: remoteSKI}); err != nil {
		t.Fatalf("RegisterRemoteSKI: %v", err)
	}
	if _, err := dc.GetDeviceSnapshot(ctx, &pb.DeviceRequest{Ski: remoteSKI}); err != nil {
		t.Fatalf("secured device snapshot: %v", err)
	}
	diagnostics, err := dc.GetDeviceDiagnostics(ctx, &pb.DeviceRequest{Ski: remoteSKI})
	if err != nil {
		t.Fatalf("secured operational diagnostics: %v", err)
	}
	if diagnostics.GetRedactedSki() == "" || strings.Contains(diagnostics.GetRedactedSki(), strings.ToUpper(remoteSKI)) {
		t.Fatalf("operational diagnostics exposed full SKI: %q", diagnostics.GetRedactedSki())
	}
	stateStream, err := dc.SubscribeDeviceState(ctx, &pb.DeviceRequest{Ski: remoteSKI})
	if err != nil {
		t.Fatalf("secured device-state stream: %v", err)
	}
	if initial, recvErr := stateStream.Recv(); recvErr != nil || initial.GetInitialSnapshot() == nil {
		t.Fatalf("secured device-state initial snapshot = (%v, %v)", initial, recvErr)
	}
	if _, err := pb.NewLPCServiceClient(valid).WriteConsumptionLimit(ctx, &pb.WriteLoadLimitRequest{
		Ski: remoteSKI, ValueWatts: 2500, IsActive: true,
	}); err != nil {
		t.Fatalf("secured LPC write: %v", err)
	}
	if response, err := grpc_health_v1.NewHealthClient(valid).Check(ctx, &grpc_health_v1.HealthCheckRequest{}); err != nil {
		t.Fatalf("secured healthcheck: %v", err)
	} else if response.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("health status = %s", response.GetStatus())
	}
	stream, err := dc.SubscribeDeviceEvents(ctx, &pb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	bus.Publish(eebus.Event{SKI: remoteSKI, Type: eebus.EventTypeDeviceConnected})
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("secured stream: %v", err)
	}

	transport, err := credentials.NewClientTLSFromFile(certFile, "")
	if err != nil {
		t.Fatal(err)
	}
	missing, err := grpcgo.NewClient(addr, grpcgo.WithTransportCredentials(transport))
	if err != nil {
		t.Fatal(err)
	}
	defer missing.Close()
	wrongTokenFile := filepath.Join(t.TempDir(), "wrong-token")
	if err := os.WriteFile(wrongTokenFile, []byte("wrong-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	wrong, err := NewClient(addr, ClientSecurityConfig{
		Mode: config.GRPCSecurityModeTLSToken, CACertFile: certFile, TokenFile: wrongTokenFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.Close()

	for name, conn := range map[string]*grpcgo.ClientConn{"missing": missing, "wrong": wrong} {
		t.Run(name, func(t *testing.T) {
			unauthorizedCtx, unauthorizedCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer unauthorizedCancel()
			client := pb.NewDeviceServiceClient(conn)
			if _, err := client.GetStatus(unauthorizedCtx, &pb.Empty{}); status.Code(err) != codes.Unauthenticated {
				t.Fatalf("unary code = %s, want Unauthenticated", status.Code(err))
			}
			if _, err := client.GetServerInfo(unauthorizedCtx, &pb.Empty{}); status.Code(err) != codes.Unauthenticated {
				t.Fatalf("server info code = %s, want Unauthenticated", status.Code(err))
			}
			if _, err := client.GetDeviceSnapshot(unauthorizedCtx, &pb.DeviceRequest{Ski: remoteSKI}); status.Code(err) != codes.Unauthenticated {
				t.Fatalf("device snapshot code = %s, want Unauthenticated", status.Code(err))
			}
			if _, err := client.GetDeviceDiagnostics(unauthorizedCtx, &pb.DeviceRequest{Ski: remoteSKI}); status.Code(err) != codes.Unauthenticated {
				t.Fatalf("operational diagnostics code = %s, want Unauthenticated", status.Code(err))
			}
			stateStream, err := client.SubscribeDeviceState(unauthorizedCtx, &pb.DeviceRequest{Ski: remoteSKI})
			if err == nil {
				_, err = stateStream.Recv()
			}
			if status.Code(err) != codes.Unauthenticated {
				t.Fatalf("device-state stream code = %s, want Unauthenticated", status.Code(err))
			}
			stream, err := client.SubscribeDeviceEvents(unauthorizedCtx, &pb.Empty{})
			if err == nil {
				_, err = stream.Recv()
			}
			if status.Code(err) != codes.Unauthenticated {
				t.Fatalf("stream code = %s, want Unauthenticated", status.Code(err))
			}
		})
	}
	if strings.Contains(logs.String(), token) || strings.Contains(logs.String(), string(keyMaterial)) {
		t.Fatalf("secured RPC logs exposed token or private key material: %q", logs.String())
	}
}
